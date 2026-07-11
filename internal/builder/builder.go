// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

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
	"time"

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
	newContainer func(context.Context, string, bool) (container.Container, error)
	publishers   []publisher.Publisher
	skipIfExists bool
}

// New constructs a Builder. cfgPath is the path to the config file that
// produced cfg. Note that relative paths inside the config (e.g. ansible.playbook,
// ansible.inventory) are resolved relative to the current working directory,
// not relative to the config file's location. Pass "" if there is no source
// path (e.g. an in-memory config).
func New(cfg *config.Config, cfgPath string, b backend.Backend, p []publisher.Publisher) *Builder {
	return &Builder{
		cfg:     cfg,
		cfgPath: cfgPath,
		backend: b,
		newContainer: func(ctx context.Context, from string, tlsverify bool) (container.Container, error) {
			return ibuildah.NewContainer(ctx, from, tlsverify)
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
//  7. Apply image labels and publish to all configured destinations
//  8. Clean up the container
//
// One ordering exception: backends whose scratch bootstrap refuses a
// non-empty root (Backend.RequiresEmptyRoot, today mmdebstrap) run the
// install step FIRST — writing repos/files/keys beforehand would make the
// bootstrap fail. The write steps then run after the root exists.
//
// Returns an error if any step fails. The container is automatically
// cleaned up via defer, even if the build fails.
func (b *Builder) Build(ctx context.Context) error {
	log := slog.With("component", "builder")
	buildStart := time.Now()

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

	c, err := b.newContainer(ctx, b.cfg.Meta.From, b.cfg.Meta.TLSVerify())
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	defer c.Delete() // Always clean up the container when done

	log.Debug("created container", "id", c.GetID(), "name", c.GetName(), "from", c.GetParent())

	isScratch := b.cfg.Meta.From == "scratch"

	// Backends like mmdebstrap refuse to bootstrap into a non-empty root,
	// so every write step must wait until after the install for them.
	installFirst := isScratch && b.backend.RequiresEmptyRoot()
	if installFirst {
		if err := b.backend.Bootstrap(ctx, c, c.MountPath()); err != nil {
			return fmt.Errorf("bootstrap %s: %w", b.cfg.Layer.Manager.Name, err)
		}
		if err := b.runInstall(ctx, c); err != nil {
			return fmt.Errorf("install: %w", err)
		}
	}

	// Apply package manager configuration (e.g., dnf.conf)
	if err := b.applyManagerConfig(ctx, c); err != nil {
		return fmt.Errorf("write manager config: %w", err)
	}

	// Backend-specific scratch preparation (creating /etc/rpm, writing
	// macros, pre-creating the filesystem skeleton, rpm --initdb, etc.)
	// lives in each backend's Bootstrap; the builder only decides whether
	// (and when) to call it.
	if isScratch && !installFirst {
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

	// Install packages, groups, and modules (unless the backend already
	// installed up front to satisfy its empty-root requirement)
	if !installFirst {
		if err := b.runInstall(ctx, c); err != nil {
			return fmt.Errorf("install: %w", err)
		}
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

	// Generate image labels and apply them to the container BEFORE the
	// publish loop, so every destination (local commit, direct registry
	// push, …) carries them. Publishers previously depended on the local
	// publisher having run first to set labels — a registry-only publish
	// silently produced unlabeled images.
	log.Debug("generating image labels")
	labelGen := labels.New(b.cfg)
	imageLabels := labelGen.Generate()
	c.SetLabels(imageLabels)
	log.Debug("applied labels", "count", len(imageLabels))

	// Publish to all configured destinations
	for _, p := range b.publishers {
		if err := p.Publish(ctx, c, b.cfg.Meta.Name, b.cfg.Meta.Tags, imageLabels); err != nil {
			return fmt.Errorf("publish %T: %w", p, err)
		}
	}

	log.Info("build complete",
		"name", b.cfg.Meta.Name,
		"tags", b.cfg.Meta.Tags,
		"duration", time.Since(buildStart).Round(time.Millisecond),
	)
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
//     import command runs inside the container. cleanup removes the key file
//     from the container so it doesn't get committed into the image layer —
//     removal is best-effort (logged at WARN on failure) because a leftover
//     public key is cosmetic, not a build failure.
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
	cleanup := func() {
		if err := container.RunCmd(ctx, c, "builder", []string{"rm", "-f", inContainer}, container.RunModeContainer); err != nil {
			slog.With("component", "builder").Warn("failed to remove gpg key from container (continuing)",
				"path", inContainer, "error", err)
		}
	}
	return inContainer, cleanup, nil
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
	log.Info("starting install", installAttrs(b.cfg.Layer.Actions.Install)...)
	start := time.Now()

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

	log.Info("install complete", "duration", time.Since(start).Round(time.Millisecond))
	return nil
}

// installAttrs renders a config.Install as slog attrs, one per non-empty
// field. Logging the fields separately (instead of dumping the struct) keeps
// multi-word group names like "Minimal Install" intact — the struct fallback
// formatter splits on whitespace, which made one group read as two packages.
func installAttrs(inst config.Install) []any {
	attrs := make([]any, 0, 6)
	if len(inst.Packages) > 0 {
		attrs = append(attrs, "packages", inst.Packages)
	}
	if len(inst.Groups) > 0 {
		attrs = append(attrs, "groups", inst.Groups)
	}
	if len(inst.Modules) > 0 {
		mods := make([]string, len(inst.Modules))
		for i, m := range inst.Modules {
			mods[i] = fmt.Sprintf("%s:%s (%s)", m.Name, m.Stream, m.Action)
		}
		attrs = append(attrs, "modules", mods)
	}
	return attrs
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
		// Joined, not the raw slice: a command line reads (and copy-pastes)
		// as one line, whereas the slice renders one argument per line.
		log.Debug("running install command", "cmd", strings.Join(cmd, " "))
		// Capture output so the backend can decide whether to tolerate a
		// non-zero exit (e.g. zypper post-install scriptlet noise).
		out := container.NewCapturingWriter(b.backend.OutputWriter())
		err := c.Run(ctx, cmd, mode, out)
		if err == nil {
			continue
		}

		// A Go runtime crash in buildah's reexec'd chroot helper (which is
		// this very binary) writes its panic dump into the command's output
		// stream and surfaces as a plain exit code — historically then
		// misread as the package manager's own exit status. Classify it as
		// an infrastructure failure instead: the exit code is meaningless
		// for the acceptable-exit-code check (the package manager may never
		// have run), and treating it as acceptable could commit a broken
		// image.
		if marker := crashMarker(out.String()); marker != "" {
			return fmt.Errorf("%s %v: build helper or command crashed (%q in output), not a package manager failure; "+
				"the runner is likely out of processes or threads — check for leftover build processes "+
				"and the RLIMIT_NPROC / cgroup pids.max limits on the runner: %w", errVerb, cmd, marker, err)
		}

		exitCode := extractExitCode(err)
		log.Debug("install command exited non-zero",
			"exit_code", exitCode, "backend", b.cfg.Layer.Manager.Name, "cmd", strings.Join(cmd, " "))
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

	// Layer-level env first, then command-level so the latter overrides.
	for _, e := range []*config.EnvConfig{layerEnv, cmdEnv} {
		if err := applyEnvConfig(envMap, e); err != nil {
			return nil, err
		}
	}

	// Convert to []string format
	result := make([]string, 0, len(envMap))
	for key, value := range envMap {
		result = append(result, fmt.Sprintf("%s=%s", key, value))
	}

	return result, nil
}

// applyEnvConfig folds one EnvConfig into envMap: "pass" keys are read from
// the host environment (and must exist there), then "set" keys apply their
// explicit values. A nil config is a no-op.
func applyEnvConfig(envMap map[string]string, e *config.EnvConfig) error {
	if e == nil {
		return nil
	}
	for _, key := range e.Pass {
		value, exists := os.LookupEnv(key)
		if !exists {
			return fmt.Errorf("required environment variable %q not found on host", key)
		}
		envMap[key] = value
	}
	for key, value := range e.Set {
		envMap[key] = value
	}
	return nil
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
	start := time.Now()

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
		log.Info("commands complete",
			"count", len(b.cfg.Layer.Actions.Commands),
			"duration", time.Since(start).Round(time.Millisecond),
		)
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
	start := time.Now()

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

	log.Info("removed packages", "count", len(packages), "duration", time.Since(start).Round(time.Millisecond))
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

// crashMarker scans captured command output for signatures of a Go runtime
// crash and returns the first marker found ("" if none). With chroot
// isolation, the process that runs the command is a reexec'd copy of this
// binary; if the runner is out of processes/threads it dies with a runtime
// abort whose stderr lands in the command's captured output, while the
// error itself is just "exit status 2". These markers are how we tell that
// failure apart from the package manager exiting non-zero.
//
// The patterns are deliberately narrow — verbatim strings the Go runtime
// prints on a fatal error — so ordinary package-manager output can't match.
func crashMarker(output string) string {
	for _, marker := range []string{
		"pthread_create failed",  // runtime/cgo thread exhaustion (EAGAIN)
		"SIGABRT: abort",         // Go runtime fatal-error banner
		"runtime: g 0",           // runtime traceback of the scheduler goroutine
		"fatal error: runtime: ", // e.g. "fatal error: runtime: out of memory"
	} {
		if strings.Contains(output, marker) {
			return marker
		}
	}
	return ""
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
	if parts := strings.Split(errMsg, "exit status "); len(parts) >= 2 {
		// Take the leading digit run after "exit status " — the code may be
		// followed by punctuation ("exit status 42: …") or nothing at all,
		// so neither Fields-splitting nor a bare Atoi of the remainder works.
		rest := parts[1]
		end := 0
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		if end > 0 {
			if code, parseErr := strconv.Atoi(rest[:end]); parseErr == nil {
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
