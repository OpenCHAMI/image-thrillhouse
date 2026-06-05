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
	"github.com/travisbcotton/image-build/internal/backend"
	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
	"github.com/travisbcotton/image-build/internal/fetch"
	"github.com/travisbcotton/image-build/internal/labels"
	"github.com/travisbcotton/image-build/internal/oscap"
	"github.com/travisbcotton/image-build/internal/publisher"

	ibuildah "github.com/travisbcotton/image-build/internal/buildah"
)

// Builder orchestrates the image building process.
// It manages the lifecycle of creating a container, running installations,
// executing commands, and publishing the result.
type Builder struct {
	cfg          *config.Config
	backend      backend.Backend
	newContainer func(context.Context, string, string, bool) (container.Container, error)
	publishers   []publisher.Publisher
	skipIfExists bool
}

func New(ctx context.Context, cfg *config.Config, b backend.Backend, p []publisher.Publisher) *Builder {
	return &Builder{
		cfg:     cfg,
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

	if b.skipIfExists {
		exists, err := b.allExist(ctx, b.cfg.Meta.Name, b.cfg.Meta.Tags)
		if err != nil {
			return fmt.Errorf("check exists: %w", err)
		}
		if exists {
			slog.Info("skipping build, image already exists",
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

	log := slog.With("component", "builder")
	log.Debug("Created container", "id", c.GetID(), "name", c.GetName(), "from", c.GetParent())

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
	log.Debug("Generating image labels")
	labelGen := labels.New(b.cfg)
	imageLabels := labelGen.Generate()
	log.Debug("Generated labels", "count", len(imageLabels))

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
func (b *Builder) applyManagerConfig(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")
	if b.cfg.Layer.Manager.Config == "" {
		return nil
	}
	log.Info("Writing configfile", "config", b.cfg.Layer.Manager.Config)
	return c.WriteFile(ctx, config.File{
		Path:    b.backend.ConfigFilePath(),
		Content: b.cfg.Layer.Manager.Config,
	})
}

// writeRepos writes all repository configuration files to the container.
// Repositories can be specified as inline content, local files, or URLs.
// The actual path where repos are written depends on the package manager.
func (b *Builder) writeRepos(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")
	for _, repo := range b.cfg.Layer.Repos {
		log.Info("writing repos:", "repo", repo.Path)
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

		log.Info("Importing GPG key for repository", "repo", repo.Path, "key", repo.GPGKey)

		keyBytes, err := fetch.Get(ctx, repo.GPGKey)
		if err != nil {
			log.Warn("Failed to fetch GPG key (continuing)", "repo", repo.Path, "error", err)
			continue
		}

		keyPath, cleanup, err := b.placeGPGKey(ctx, c, isScratch, rootPath, i, keyBytes)
		if err != nil {
			log.Warn("Failed to place GPG key (continuing)", "repo", repo.Path, "error", err)
			continue
		}

		cmd := b.backend.ImportGPGKeyCommand(keyPath, rootPath)
		if cmd == nil {
			log.Warn("Backend does not support GPG key import", "backend", b.cfg.Layer.Manager.Name)
			cleanup()
			continue
		}

		runMode := container.RunModeContainer
		if isScratch {
			runMode = container.RunModeHost
		}

		out := container.NewBufLogWriter("stdout")
		if err := c.Run(ctx, cmd, runMode, out); err != nil {
			log.Warn("Failed to import GPG key (continuing)", "repo", repo.Path, "error", err)
		} else {
			log.Info("Successfully imported GPG key", "repo", repo.Path)
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
		f, err := os.CreateTemp("", "image-build-gpg-key-*.bin")
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
	inContainer := fmt.Sprintf("/tmp/image-build-gpg-key-%d.bin", idx)
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
		log.Info("Writing Files:", "file", file.Path)
		if err := c.WriteFile(ctx, file); err != nil {
			return fmt.Errorf("write file %s: %w", file.Path, err)
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
	log.Info("Starting install commands:", "install", b.cfg.Layer.Actions.Install)

	// Scratch build: bootstrap a new filesystem from nothing.
	// Backend.Bootstrap (called earlier in Build) has already created the
	// directory skeleton, written any RPM macros, and initialized the RPM
	// database for backends that need it.
	if b.cfg.Meta.From == "scratch" {
		if !b.backend.SupportsInstallRoot() {
			return fmt.Errorf("backend %s does not support scratch builds", b.cfg.Layer.Manager.Name)
		}

		// Get commands to run on the host targeting the container mount
		cmds := b.backend.InstallRootCommands(b.cfg.Layer.Actions.Install, c.MountPath())
		for _, cmd := range cmds {
			log.Debug("Install", "action", cmd)
			// Wrap the backend's output writer to capture output for exit code checking
			out := container.NewCapturingWriter(b.backend.OutputWriter())
			err := c.Run(ctx, cmd, container.RunModeHost, out)

			// Check if the error is an acceptable exit code
			if err != nil {
				// Try to extract exit code from error
				exitCode := extractExitCode(err)
				log.Debug("install command exited non-zero",
					"exitCode", exitCode, "backend", b.cfg.Layer.Manager.Name, "cmd", cmd)
				if exitCode > 0 && b.backend.IsAcceptableExitCode(exitCode, out.String()) {
					log.Warn("Command returned non-zero exit code but packages were installed successfully",
						"exitCode", exitCode, "cmd", cmd)
					// Treat as success
					continue
				}
				if exitCode == -1 {
					// Diagnostic for the case where the error chain doesn't
					// contain an *exec.ExitError — the acceptable-exit-code
					// check can't even run, so a backend that *would* tolerate
					// this code silently won't get the chance.
					log.Warn("could not determine exit code from error; acceptable-exit-code checks skipped",
						"err", err)
				}
				return fmt.Errorf("run root %v: %w", cmd, err)
			}
		}
	} else {
		// Parent build: run commands inside the existing container
		if !b.backend.SupportsParentInstall() {
			return fmt.Errorf("backend %s does not support parent image builds, use apt instead", b.cfg.Layer.Manager.Name)
		}
		// Get commands to run inside the container
		cmds := b.backend.InstallCommands(b.cfg.Layer.Actions.Install)
		for _, cmd := range cmds {
			log.Debug("Install", "action", cmd)
			// Wrap the backend's output writer to capture output for exit code checking
			out := container.NewCapturingWriter(b.backend.OutputWriter())
			err := c.Run(ctx, cmd, container.RunModeContainer, out)

			// Check if the error is an acceptable exit code
			if err != nil {
				// Try to extract exit code from error
				exitCode := extractExitCode(err)
				log.Debug("install command exited non-zero",
					"exitCode", exitCode, "backend", b.cfg.Layer.Manager.Name, "cmd", cmd)
				if exitCode > 0 && b.backend.IsAcceptableExitCode(exitCode, out.String()) {
					log.Warn("Command returned non-zero exit code but packages were installed successfully",
						"exitCode", exitCode, "cmd", cmd)
					// Treat as success
					continue
				}
				if exitCode == -1 {
					// Diagnostic for the case where the error chain doesn't
					// contain an *exec.ExitError — the acceptable-exit-code
					// check can't even run, so a backend that *would* tolerate
					// this code silently won't get the chance.
					log.Warn("could not determine exit code from error; acceptable-exit-code checks skipped",
						"err", err)
				}
				return fmt.Errorf("run %v: %w", cmd, err)
			}
		}
	}
	log.Info("Done installing commands:", "install", b.cfg.Layer.Actions.Install)
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

	// Log start with count
	if len(b.cfg.Layer.Actions.Commands) > 0 {
		log.Info("Starting Run Commands", "count", len(b.cfg.Layer.Actions.Commands))
	}

	for i, cmd := range b.cfg.Layer.Actions.Commands {
		// Log the command being executed with proper formatting
		switch cmd.Type() {
		case config.CommandRun:
			log.Debug("Executing run command", "index", i, "run", cmd.Run)
			// Parse the command string into parts (handles quoting properly)
			parts, err := shellwords.Parse(cmd.Run)
			if err != nil {
				return fmt.Errorf("parse command %q: %w", cmd.Run, err)
			}
			out := container.NewBufLogWriter("stdout")
			if err := c.Run(ctx, parts, container.RunModeContainer, out); err != nil {
				return fmt.Errorf("run %s: %w", cmd.Run, err)
			}

		case config.CommandScript:
			log.Debug("Executing script", "index", i, "script", cmd.Script)
			// Execute a multi-line script
			out := container.NewBufLogWriter("stdout")
			if err := c.RunScript(ctx, cmd.Script, out); err != nil {
				return fmt.Errorf("run script: %w", err)
			}

		case config.CommandAnsible:
			log.Debug("Executing ansible playbook", "index", i, "playbook", cmd.Ansible.Playbook)
			// Execute Ansible playbook
			if err := b.runAnsibleCommand(ctx, c, cmd.Ansible); err != nil {
				return fmt.Errorf("run ansible: %w", err)
			}

		default:
			return fmt.Errorf("command has no run, script, or ansible")
		}
	}

	// Log completion with each command formatted nicely
	if len(b.cfg.Layer.Actions.Commands) > 0 {
		log.Info("Done Run Commands", "count", len(b.cfg.Layer.Actions.Commands))
		for i, cmd := range b.cfg.Layer.Actions.Commands {
			switch cmd.Type() {
			case config.CommandRun:
				log.Info("command", "index", i, "type", "run", "run", cmd.Run)
			case config.CommandScript:
				log.Info("command", "index", i, "type", "script", "script", cmd.Script)
			case config.CommandAnsible:
				log.Info("command", "index", i, "type", "ansible", "playbook", cmd.Ansible.Playbook)
			}
		}
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

	log.Info("Removing packages", "count", len(packages), "packages", packages)

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

	out := container.NewBufLogWriter("stdout")
	if err := c.Run(ctx, cmd, runMode, out); err != nil {
		return fmt.Errorf("remove packages %v: %w", packages, err)
	}

	log.Info("Successfully removed packages")
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

	log.Info("Starting OpenSCAP security scanning")

	scanner := oscap.New(oscapCfg)
	if err := scanner.Run(ctx, c, b.cfg.Layer.Manager.Name); err != nil {
		return fmt.Errorf("OpenSCAP failed: %w", err)
	}

	log.Info("OpenSCAP security scanning complete")
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
