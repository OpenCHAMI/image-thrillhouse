// Package builder orchestrates the image building process.
// It coordinates between the backend (package manager), container operations,
// and publishers to create and distribute container images.
package builder

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mattn/go-shellwords"
	"github.com/travisbcotton/image-thrillhouse/internal/backend"
	"github.com/travisbcotton/image-thrillhouse/internal/config"
	"github.com/travisbcotton/image-thrillhouse/internal/container"
	"github.com/travisbcotton/image-thrillhouse/internal/fetch"
	"github.com/travisbcotton/image-thrillhouse/internal/labels"
	"github.com/travisbcotton/image-thrillhouse/internal/oscap"
	"github.com/travisbcotton/image-thrillhouse/internal/publisher"

	ibuildah "github.com/travisbcotton/image-thrillhouse/internal/buildah"
)

// Builder orchestrates the image building process.
// It manages the lifecycle of creating a container, running installations,
// executing commands, and publishing the result.
type Builder struct {
	cfg          *config.Config
	cfgPath      string // path to the config file; stored for reference but not used for path resolution (paths resolve relative to CWD)
	backend      backend.Backend
	newContainer func(context.Context, string, string, bool) (container.Container, error)
	publishers   []publisher.Publisher
	skipIfExists bool
}

// New constructs a Builder. cfgPath is the path to the config file that
// produced cfg. Note that relative paths inside the config (e.g. ansible.playbook,
// ansible.inventory) are resolved relative to the current working directory,
// not relative to the config file's location. Pass "" if there is no source
// path (e.g. an in-memory config).
func New(ctx context.Context, cfg *config.Config, cfgPath string, b backend.Backend, p []publisher.Publisher) *Builder {
	return &Builder{
		cfg:     cfg,
		cfgPath: cfgPath,
		backend: b,
		newContainer: func(ctx context.Context, name string, from string, tlsverify bool) (container.Container, error) {
			return ibuildah.NewContainer(ctx, name, from, tlsverify)
		},
		publishers: p,
	}
}

// SetSkipIfExists toggles the skip-if-exists guard. When true, Build will
// poll every configured publisher's Exists() before doing any work, and skip
// the whole build (no container created, no commands run, no publish) when
// all publishers report the image is already present for the configured
// name + tags.
func (b *Builder) SetSkipIfExists(v bool) {
	b.skipIfExists = v
}

// Build executes the complete image building process.
// The build process consists of the following steps:
//  1. Create a new container from the base image
//  2. Apply package manager configuration
//  3. Write repository configurations
//  4. Write custom files
//  5. Run package installations
//  6. Run custom commands
//  7. Publish to all configured destinations
//  8. Clean up the container
//
// Returns an error if any step fails. The container is automatically
// cleaned up via defer, even if the build fails.
func (b *Builder) Build(ctx context.Context) error {
	log := slog.With("component", "builder")

	log.Info("starting build",
		"name", b.cfg.Meta.Name,
		"from", b.cfg.Meta.From,
		"backend", b.cfg.Layer.Manager.Name,
		"tags", b.cfg.Meta.Tags,
		"publishers", publisherNames(b.publishers),
		"config", b.cfgPath,
	)

	if b.skipIfExists {
		exists, err := b.allExist(ctx, b.cfg.Meta.Name, b.cfg.Meta.Tags)
		if err != nil {
			return fmt.Errorf("check exists: %w", err)
		}
		if exists {
			log.Info("skipping build, image already exists",
				"name", b.cfg.Meta.Name,
				"tags", b.cfg.Meta.Tags)
			return nil
		}
	}

	c, err := b.newContainer(ctx, b.cfg.Meta.Name, b.cfg.Meta.From, b.cfg.Meta.TLSVerify())
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	defer c.Delete() // Always clean up the container when done

	log.Debug("created container", "id", c.GetID(), "name", c.GetName(), "from", c.GetParent())

	// Apply package manager configuration (e.g., dnf.conf)
	if err := b.applyManagerConfig(ctx, c); err != nil {
		return fmt.Errorf("write manager config: %w", err)
	}

	// Backend-specific scratch preparation (creating /etc/rpm, writing
	// macros, pre-creating the filesystem skeleton, rpm --initdb, etc.)
	// lives in each backend's Bootstrap; the builder only decides whether
	// to call it.
	if b.cfg.Meta.From == "scratch" {
		if err := b.backend.Bootstrap(ctx, c, c.MountPath()); err != nil {
			return fmt.Errorf("bootstrap %s: %w", b.cfg.Layer.Manager.Name, err)
		}
	}

	// Write repository configurations (e.g., yum repos)
	if err := b.writeRepos(ctx, c); err != nil {
		return fmt.Errorf("write repos: %w", err)
	}

	// Import GPG keys for repositories
	if err := b.importGPGKeys(ctx, c); err != nil {
		return fmt.Errorf("import GPG keys: %w", err)
	}

	// Write custom files to the container
	if err := b.writeFiles(ctx, c); err != nil {
		return fmt.Errorf("write files: %w", err)
	}

	// Recursively copy host directories into the container
	if err := b.writeDirectories(ctx, c); err != nil {
		return fmt.Errorf("write directories: %w", err)
	}

	// Install packages, groups, and modules
	if err := b.runInstall(ctx, c); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	// Run custom commands
	if err := b.runCommands(ctx, c); err != nil {
		return fmt.Errorf("run commands: %w", err)
	}

	// Remove packages if specified
	if err := b.removePackages(ctx, c); err != nil {
		return fmt.Errorf("remove packages: %w", err)
	}

	// Run OpenSCAP security scanning if configured
	if err := b.runOpenSCAP(ctx, c); err != nil {
		return fmt.Errorf("OpenSCAP scanning: %w", err)
	}

	// Generate image labels
	log.Debug("generating image labels")
	labelGen := labels.New(b.cfg)
	imageLabels := labelGen.Generate()
	log.Debug("generated labels", "count", len(imageLabels))

	// Publish to all configured destinations
	for _, p := range b.publishers {
		if err := p.Publish(ctx, c, b.cfg.Meta.Name, b.cfg.Meta.Tags, imageLabels); err != nil {
			return fmt.Errorf("publish %T: %w", p, err)
		}
	}

	return nil
}

// applyManagerConfig writes the package manager configuration file if specified.
// For example, this could write /etc/dnf/dnf.conf for DNF or /etc/zypp/zypp.conf for Zypper.
// If no config is specified in the configuration, this is a no-op.
//
// Backends that don't support a persistent config file (today: mmdebstrap)
// return "" from ConfigFilePath. If the user nevertheless set
// layer.manager.config for one of those backends, we fail loudly rather than
// writing the YAML to a bogus path — silent acceptance there used to mean
// the user's config did nothing without any warning.
func (b *Builder) applyManagerConfig(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")
	if b.cfg.Layer.Manager.Config == "" {
		return nil
	}
	path := b.backend.ConfigFilePath()
	if path == "" {
		return fmt.Errorf("backend %s does not support layer.manager.config", b.cfg.Layer.Manager.Name)
	}
	log.Info("writing manager config", "path", path)
	return c.WriteFile(ctx, config.File{
		Path:    path,
		Content: b.cfg.Layer.Manager.Config,
	})
}

// writeRepos writes all repository configuration files to the container.
// Repositories can be specified as inline content, local files, or URLs.
// The actual path where repos are written depends on the package manager.
func (b *Builder) writeRepos(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")
	for _, repo := range b.cfg.Layer.Repos {
		log.Info("writing repo", "repo", repo.Path)
		file := config.File{
			Path:    repo.Path,
			Content: repo.Content,
			URL:     repo.URL,
			Src:     repo.Src,
		}
		if err := c.WriteFile(ctx, file); err != nil {
			return fmt.Errorf("write repo %s: %w", repo.Path, err)
		}
	}
	return nil
}

// importGPGKeys imports GPG keys for repositories that specify them.
// This allows automatic verification of package signatures.
//
// The key bytes are fetched here in Go (ctx + timeout-aware) and written
// to disk. The backend is then asked for a command that operates on that
// local path — never on the URL. This is what closes the previous shell
// injection vector where a user-supplied URL was interpolated into a
// `sh -c "curl ..."` string.
//
// For scratch builds, the key is written to a host temp file and the
// resulting command runs on the host with --root semantics. For parent
// builds, the key is written inside the container via c.WriteFile and
// the command runs inside the container.
//
// If a repo has no GPG key, it's skipped (the user is expected to handle
// GPG in the repo config). Per-repo failures are warnings, not errors,
// to match prior behavior — some repos work without GPG.
func (b *Builder) importGPGKeys(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")

	isScratch := b.cfg.Meta.From == "scratch"
	rootPath := ""
	if isScratch {
		rootPath = c.MountPath()
	}

	for i, repo := range b.cfg.Layer.Repos {
		if repo.GPGKey == "" {
			continue
		}

		log.Info("importing gpg key for repository", "repo", repo.Path, "key", repo.GPGKey)

		keyBytes, err := fetch.Get(ctx, repo.GPGKey)
		if err != nil {
			log.Warn("failed to fetch gpg key (continuing)", "repo", repo.Path, "error", err)
			continue
		}

		keyPath, cleanup, err := b.placeGPGKey(ctx, c, isScratch, rootPath, i, keyBytes)
		if err != nil {
			log.Warn("failed to place gpg key (continuing)", "repo", repo.Path, "error", err)
			continue
		}

		cmd := b.backend.ImportGPGKeyCommand(keyPath, rootPath)
		if cmd == nil {
			log.Warn("backend does not support gpg key import", "backend", b.cfg.Layer.Manager.Name)
			cleanup()
			continue
		}

		runMode := container.RunModeContainer
		if isScratch {
			runMode = container.RunModeHost
		}

		if err := container.RunCmd(ctx, c, "builder", cmd, runMode); err != nil {
			log.Warn("failed to import gpg key (continuing)", "repo", repo.Path, "error", err)
		} else {
			log.Info("imported gpg key", "repo", repo.Path)
		}
		cleanup()
	}

	return nil
}

// placeGPGKey writes fetched key bytes to a location the backend's import
// command can reference. Returns the path it wrote to and a cleanup
// function that removes the file. The path's meaning depends on whether
// this is a scratch build:
//
//   - scratch: a *host* temp file. The import command runs on the host with
//     --root, so the key must be host-readable. cleanup removes the temp file.
//   - parent:  a path *inside* the container, placed via c.WriteFile. The
//     import command runs inside the container. cleanup is a no-op because
//     the file lives inside the (ephemeral) container; it would normally
//     be removed by the user's own commands or simply not matter once the
//     image is committed.
func (b *Builder) placeGPGKey(ctx context.Context, c container.Container, isScratch bool, rootPath string, idx int, keyBytes []byte) (string, func(), error) {
	if isScratch {
		f, err := os.CreateTemp("", "image-thrillhouse-gpg-key-*.bin")
		if err != nil {
			return "", func() {}, fmt.Errorf("create temp key file: %w", err)
		}
		if _, err := f.Write(keyBytes); err != nil {
			f.Close()
			os.Remove(f.Name())
			return "", func() {}, fmt.Errorf("write temp key file: %w", err)
		}
		f.Close()
		return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
	}

	// Parent build: place inside the container at a stable, codebase-controlled
	// path. We include the repo index so multiple repos don't collide.
	inContainer := fmt.Sprintf("/tmp/image-thrillhouse-gpg-key-%d.bin", idx)
	if err := c.WriteFile(ctx, config.File{
		Path:    inContainer,
		Content: string(keyBytes),
	}); err != nil {
		return "", func() {}, fmt.Errorf("write key into container: %w", err)
	}
	return inContainer, func() {}, nil
}

// writeFiles writes all custom files to the container.
// Files can be specified as inline content, local files, or URLs.
// This is useful for adding configuration files, scripts, etc.
func (b *Builder) writeFiles(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")
	for _, file := range b.cfg.Layer.Files {
		log.Info("writing file", "path", file.Path)
		if err := c.WriteFile(ctx, file); err != nil {
			return fmt.Errorf("write file %s: %w", file.Path, err)
		}
	}
	return nil
}

// writeDirectories recursively copies each configured host directory into the
// container in a single buildah operation per entry.
//
// ContentsOnly defaults to true when unset in the config (cp -a src/. dest/),
// matching the documented schema default. Any other unset option keeps
// buildah's own defaults: empty Mode preserves host modes, empty Owner +
// PreserveOwnership=false resets ownership to 0:0.
func (b *Builder) writeDirectories(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")
	for _, dir := range b.cfg.Layer.Directories {
		contentsOnly := true
		if dir.ContentsOnly != nil {
			contentsOnly = *dir.ContentsOnly
		}
		opts := container.CopyDirectoryOptions{
			Chmod:             dir.Mode,
			Chown:             dir.Owner,
			PreserveOwnership: dir.PreserveOwnership,
			Excludes:          dir.Excludes,
			ContentsOnly:      contentsOnly,
		}
		log.Info("copying directory", "src", dir.Src, "dest", dir.Path)
		if err := c.CopyDirectory(ctx, dir.Src, dir.Path, opts); err != nil {
			return fmt.Errorf("copy directory %s -> %s: %w", dir.Src, dir.Path, err)
		}
	}
	return nil
}

// runInstall installs packages, groups, and modules using the configured backend.
// It handles two different build modes:
//
//  1. Scratch builds (from == "scratch"):
//     - Uses installroot to bootstrap a new filesystem
//     - Runs package manager commands on the host, targeting the container mount
//     - Not all backends support this (e.g., apt doesn't, use mmdebstrap instead)
//
//  2. Parent builds (from != "scratch"):
//     - Runs package manager commands inside the container
//     - Requires the package manager to exist in the parent image
//     - Not all backends support this (e.g., mmdebstrap doesn't, use apt instead)
//
// The backend generates the appropriate commands for the selected mode.
func (b *Builder) runInstall(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")
	log.Info("starting install", "install", b.cfg.Layer.Actions.Install)

	var (
		cmds    [][]string
		mode    container.RunMode
		errVerb string // "run root" for scratch, "run" for parent — preserves prior error text
	)

	if b.cfg.Meta.From == "scratch" {
		// Scratch build: bootstrap a new filesystem from nothing. Backend.Bootstrap
		// (called earlier in Build) has already created the directory skeleton,
		// written any RPM macros, and initialised the RPM database for backends
		// that need it.
		if !b.backend.SupportsInstallRoot() {
			return fmt.Errorf("backend %s does not support scratch builds", b.cfg.Layer.Manager.Name)
		}
		cmds = b.backend.InstallRootCommands(b.cfg.Layer.Actions.Install, c.MountPath())
		mode = container.RunModeHost
		errVerb = "run root"
	} else {
		// Parent build: run commands inside the existing container.
		if !b.backend.SupportsParentInstall() {
			return fmt.Errorf("backend %s does not support parent image builds, use apt instead", b.cfg.Layer.Manager.Name)
		}
		cmds = b.backend.InstallCommands(b.cfg.Layer.Actions.Install)
		mode = container.RunModeContainer
		errVerb = "run"
	}

	if err := b.runInstallCommands(ctx, c, cmds, mode, errVerb, log); err != nil {
		return err
	}

	log.Info("install complete", "install", b.cfg.Layer.Actions.Install)
	return nil
}

// runInstallCommands executes a backend's install command list, applying the
// acceptable-exit-code dance the scratch and parent branches both used to
// duplicate. errVerb is folded into error messages ("run root" vs "run") so
// callers can keep their existing log/grep patterns.
func (b *Builder) runInstallCommands(
	ctx context.Context,
	c container.Container,
	cmds [][]string,
	mode container.RunMode,
	errVerb string,
	log *slog.Logger,
) error {
	for _, cmd := range cmds {
		log.Debug("install", "action", cmd)
		// Capture output so the backend can decide whether to tolerate a
		// non-zero exit (e.g. zypper post-install scriptlet noise).
		out := container.NewCapturingWriter(b.backend.OutputWriter())
		err := c.Run(ctx, cmd, mode, out)
		if err == nil {
			continue
		}

		exitCode := extractExitCode(err)
		log.Debug("install command exited non-zero",
			"exit_code", exitCode, "backend", b.cfg.Layer.Manager.Name, "cmd", cmd)
		if exitCode > 0 && b.backend.IsAcceptableExitCode(exitCode, out.String()) {
			log.Warn("install non-zero exit accepted; packages installed",
				"exit_code", exitCode, "cmd", cmd)
			continue
		}
		if exitCode == -1 {
			// Diagnostic for the case where the error chain doesn't contain
			// an *exec.ExitError — the acceptable-exit-code check can't even
			// run, so a backend that *would* tolerate this code silently
			// won't get the chance.
			log.Warn("could not determine exit code; acceptable-exit-code checks skipped",
				"error", err)
		}
		return fmt.Errorf("%s %v: %w", errVerb, cmd, err)
	}
	return nil
}

// resolveEnv converts EnvConfig structs into a slice of "KEY=VALUE" strings
// suitable for container.WithEnv(). It merges layer-level and command-level
// env configs, with command-level taking precedence.
//
// Variables in Pass are read from the host environment and must exist.
// Variables in Set are defined with explicit values in the configuration.
// If a required variable from Pass is not found on the host, an error is returned.
func (b *Builder) resolveEnv(layerEnv, cmdEnv *config.EnvConfig) ([]string, error) {
	envMap := make(map[string]string)

	// Process layer-level env first
	if layerEnv != nil {
		// Handle "pass" - variables from host environment
		for _, key := range layerEnv.Pass {
			value, exists := os.LookupEnv(key)
			if !exists {
				return nil, fmt.Errorf("required environment variable %q not found on host", key)
			}
			envMap[key] = value
		}

		// Handle "set" - explicit values
		for key, value := range layerEnv.Set {
			envMap[key] = value
		}
	}

	// Process command-level env (overrides layer-level)
	if cmdEnv != nil {
		for _, key := range cmdEnv.Pass {
			value, exists := os.LookupEnv(key)
			if !exists {
				return nil, fmt.Errorf("required environment variable %q not found on host", key)
			}
			envMap[key] = value
		}

		for key, value := range cmdEnv.Set {
			envMap[key] = value
		}
	}

	// Convert to []string format
	result := make([]string, 0, len(envMap))
	for key, value := range envMap {
		result = append(result, fmt.Sprintf("%s=%s", key, value))
	}

	return result, nil
}

// runCommands executes all custom commands specified in the configuration.
// Commands can be either simple one-liners or multi-line shell scripts.
//
// Three command types are supported:
//   - run: Simple command (e.g., "systemctl enable myservice")
//   - script: Multi-line bash script
//   - ansible: Ansible playbook execution
//
// All commands run inside the container using "buildah run".
func (b *Builder) runCommands(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")

	if len(b.cfg.Layer.Actions.Commands) > 0 {
		log.Info("starting commands", "count", len(b.cfg.Layer.Actions.Commands))
	}

	for i, cmd := range b.cfg.Layer.Actions.Commands {
		// Resolve environment variables for this command
		envVars, err := b.resolveEnv(b.cfg.Layer.Env, cmd.Env)
		if err != nil {
			return fmt.Errorf("resolve env for command %d: %w", i, err)
		}

		// Create run options with environment variables
		var opts []container.RunOption
		if len(envVars) > 0 {
			log.Debug("setting environment variables", "index", i, "count", len(envVars))
			opts = append(opts, container.WithEnv(envVars...))
		}

		switch cmd.Type() {
		case config.CommandRun:
			log.Debug("executing run command", "index", i, "run", cmd.Run)
			// Parse the command string into parts (handles quoting properly)
			parts, err := shellwords.Parse(cmd.Run)
			if err != nil {
				return fmt.Errorf("parse command %q: %w", cmd.Run, err)
			}
			if err := container.RunCmd(ctx, c, "builder", parts, container.RunModeContainer, opts...); err != nil {
				return fmt.Errorf("run %s: %w", cmd.Run, err)
			}

		case config.CommandScript:
			log.Debug("executing script", "index", i, "script", cmd.Script)
			if err := container.RunScriptCmd(ctx, c, "builder", cmd.Script, opts...); err != nil {
				return fmt.Errorf("run script: %w", err)
			}

		case config.CommandAnsible:
			log.Debug("executing ansible playbook", "index", i, "playbook", cmd.Ansible.Playbook)
			if err := b.runAnsibleCommand(ctx, c, cmd.Ansible, opts...); err != nil {
				return fmt.Errorf("run ansible: %w", err)
			}

		default:
			return fmt.Errorf("command has no run, script, or ansible")
		}
	}

	if len(b.cfg.Layer.Actions.Commands) > 0 {
		log.Info("commands complete", "count", len(b.cfg.Layer.Actions.Commands))
	}
	return nil
}

// removePackages removes packages from the container if specified in the configuration.
// Uses rpm -e --nodeps for RPM-based systems or dpkg --remove for Debian-based systems.
// This is useful for minimizing image size by removing unnecessary packages.
//
// For scratch builds, the command runs on the host against the mounted root
// (e.g. rpm --root <path> -e --nodeps ...) because a freshly-bootstrapped
// scratch root may not be able to exec the package manager itself.
// For parent builds, the command runs inside the container.
func (b *Builder) removePackages(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")

	packages := b.cfg.Layer.Actions.Install.RemovePackages
	if len(packages) == 0 {
		return nil
	}

	log.Info("removing packages", "count", len(packages), "packages", packages)

	rootPath := ""
	runMode := container.RunModeContainer
	if b.cfg.Meta.From == "scratch" {
		rootPath = c.MountPath()
		runMode = container.RunModeHost
	}

	cmd := b.backend.RemovePackagesCommand(packages, rootPath)
	if cmd == nil {
		return fmt.Errorf("backend %s does not support package removal", b.cfg.Layer.Manager.Name)
	}

	if err := container.RunCmd(ctx, c, "builder", cmd, runMode); err != nil {
		return fmt.Errorf("remove packages %v: %w", packages, err)
	}

	log.Info("removed packages")
	return nil
}

// runOpenSCAP executes OpenSCAP security scanning if configured.
// This performs security compliance checking and vulnerability assessment.
//
// Supports:
//   - Installing OpenSCAP tools (install_scap)
//   - Running XCCDF security benchmark scans (scap_benchmark)
//   - Running OVAL vulnerability evaluations (oval_eval)
//
// Results are saved in the container at the configured paths (default: /root/)
func (b *Builder) runOpenSCAP(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")

	// Skip if OpenSCAP is not configured
	if b.cfg.Layer.OpenSCAP == nil {
		return nil
	}

	oscapCfg := b.cfg.Layer.OpenSCAP

	// Skip if no OpenSCAP operations are requested
	if !oscapCfg.InstallSCAP && !oscapCfg.SCAPBenchmark && !oscapCfg.OVALEval {
		return nil
	}

	log.Info("starting openscap security scanning")

	scanner := oscap.New(oscapCfg)
	if err := scanner.Run(ctx, c, b.cfg.Layer.Manager.Name); err != nil {
		return fmt.Errorf("OpenSCAP failed: %w", err)
	}

	log.Info("openscap security scanning complete")
	return nil
}

// extractExitCode returns the exit code from an error produced by running a
// command (e.g. via exec.Cmd.Run). It unwraps the error chain to find an
// *exec.ExitError. If no exit code can be determined, it returns -1.
func extractExitCode(err error) int {
	if err == nil {
		return 0
	}
	// First try to extract from *exec.ExitError in the error chain
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	// Fallback: parse "exit status N" from error message
	// This handles cases where buildah or other wrappers convert
	// the ExitError to a string before wrapping it
	errMsg := err.Error()
	if strings.Contains(errMsg, "exit status ") {
		// Find "exit status N" pattern
		parts := strings.Split(errMsg, "exit status ")
		if len(parts) >= 2 {
			// Extract the number after "exit status "
			codeStr := strings.Fields(parts[1])[0]
			if code, parseErr := strconv.Atoi(codeStr); parseErr == nil {
				return code
			}
		}
	}
	return -1
}

// publisherNames returns a short label for each publisher (e.g. "local",
// "registry", "s3", "squashfs"), derived from the concrete type's package
// name. Used in the startup banner so operators can see at a glance where
// the build will publish without us having to add a Name() method to the
// Publisher interface.
func publisherNames(ps []publisher.Publisher) []string {
	names := make([]string, len(ps))
	for i, p := range ps {
		// %T yields "*local.LocalPublisher"; the package segment is the
		// stable, human-readable label.
		t := fmt.Sprintf("%T", p)
		t = strings.TrimPrefix(t, "*")
		if dot := strings.Index(t, "."); dot >= 0 {
			t = t[:dot]
		}
		names[i] = t
	}
	return names
}

// allExist reports whether the given (name, tags) pair already exists at
// every configured publish destination. Used to short-circuit a build when
// the target image is already published.
func (b *Builder) allExist(ctx context.Context, name string, tags []string) (bool, error) {
	for _, p := range b.publishers {
		exists, err := p.Exists(ctx, name, tags)
		if err != nil {
			return false, fmt.Errorf("check exists %T: %w", p, err)
		}
		if !exists {
			return false, nil
		}
	}
	return true, nil
}
