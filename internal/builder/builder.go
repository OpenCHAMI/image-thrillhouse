// Package builder orchestrates the image building process.
// It coordinates between the backend (package manager), container operations,
// and publishers to create and distribute container images.
package builder

import (
	"context"
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
	c, err := b.newContainer(ctx, b.cfg.Meta.Name, b.cfg.Meta.From, b.cfg.Meta.TLSVerify())
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	defer c.Delete() // Always clean up the container when done

	log := slog.With("component", "builder")
	log.Debug("Created container", "id", c.GetID(), "name", c.GetName(), "mountPath", c.MountPath())

	// Apply package manager configuration (e.g., dnf.conf)
	if err := b.applyManagerConfig(c); err != nil {
		return fmt.Errorf("write manager config: %w", err)
	}

	// Write RPM macros to disable file capabilities for scratch builds with DNF or Zypper
	// This works around issues with overlay filesystems that don't support extended attributes
	// and prevents post-installation script failures in containerized environments
	if b.cfg.Meta.From == "scratch" && (b.cfg.Layer.Manager.Name == "dnf" || b.cfg.Layer.Manager.Name == "zypper") {
		log.Debug("Writing RPM macros to disable file capabilities")
		rpmMacros := `%_netsharedpath /sys:/proc:/dev
%_install_langs C:en:en_US:en_US.UTF-8
%__brp_mangle_shebangs %{nil}
%_missing_build_ids_terminate_build 0
%_file_context_file %{nil}
%__brp_ldconfig %{nil}
`
		// Create /etc directory structure first
		etcPath := c.MountPath() + "/etc"
		rpmPath := etcPath + "/rpm"
		if err := os.MkdirAll(rpmPath, 0755); err != nil {
			log.Warn("Failed to create /etc/rpm directory", "error", err)
		}

		if err := c.WriteFile(config.File{
			Path:    "/etc/rpm/macros.image-build",
			Content: rpmMacros,
		}); err != nil {
			log.Warn("Failed to write RPM macros", "error", err)
		}
	}

	// Create essential directories for zypper scratch builds
	// The filesystem package tries to create /dev but fails if it doesn't exist
	// Note: We don't create /dev here because it causes UID/GID issues during commit
	if b.cfg.Meta.From == "scratch" && b.cfg.Layer.Manager.Name == "zypper" {
		log.Debug("Creating essential directories for zypper scratch build")
		essentialDirs := []string{"/proc", "/sys", "/run"}
		for _, dir := range essentialDirs {
			dirPath := c.MountPath() + dir
			if err := os.MkdirAll(dirPath, 0755); err != nil {
				log.Warn("Failed to create essential directory", "dir", dir, "error", err)
			}
		}
	}

	// Write repository configurations (e.g., yum repos)
	if err := b.writeRepos(c); err != nil {
		return fmt.Errorf("write repos: %w", err)
	}

	// Import GPG keys for repositories
	if err := b.importGPGKeys(ctx, c); err != nil {
		return fmt.Errorf("import GPG keys: %w", err)
	}

	// Write custom files to the container
	if err := b.writeFiles(c); err != nil {
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
func (b *Builder) applyManagerConfig(c container.Container) error {
	log := slog.With("component", "builder")
	if b.cfg.Layer.Manager.Config == "" {
		return nil
	}
	log.Info("Writing configfile", "config", b.cfg.Layer.Manager.Config)
	return c.WriteFile(config.File{
		Path:    b.backend.ConfigFilePath(),
		Content: b.cfg.Layer.Manager.Config,
	})
}

// writeRepos writes all repository configuration files to the container.
// Repositories can be specified as inline content, local files, or URLs.
// The actual path where repos are written depends on the package manager.
func (b *Builder) writeRepos(c container.Container) error {
	log := slog.With("component", "builder")
	for _, repo := range b.cfg.Layer.Repos {
		log.Info("writing repos:", "repo", repo.Path)
		file := config.File{
			Path:    repo.Path,
			Content: repo.Content,
			URL:     repo.URL,
			Src:     repo.Src,
		}
		if err := c.WriteFile(file); err != nil {
			return fmt.Errorf("write repo %s: %w", repo.Path, err)
		}
	}
	return nil
}

// importGPGKeys imports GPG keys for repositories that specify them.
// This allows automatic verification of package signatures.
// Keys are imported using backend-specific commands:
//   - RPM-based (dnf, zypper): rpm --import
//   - APT-based (apt, mmdebstrap): gpg --dearmor to /etc/apt/trusted.gpg.d/
//
// If no GPG key is specified for a repo, it's skipped (user must handle GPG in repo config).
func (b *Builder) importGPGKeys(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")
	
	// Determine if this is a scratch build
	isScratch := b.cfg.Meta.From == "scratch"
	rootPath := ""
	if isScratch {
		rootPath = c.MountPath()
	}
	
	for _, repo := range b.cfg.Layer.Repos {
		// Skip repos without GPG keys
		if repo.GPGKey == "" {
			continue
		}
		
		log.Info("Importing GPG key for repository", "repo", repo.Path, "key", repo.GPGKey)
		
		cmd := b.backend.ImportGPGKeyCommand(repo.GPGKey, rootPath)
		if cmd == nil {
			log.Warn("Backend does not support GPG key import", "backend", b.cfg.Layer.Manager.Name)
			continue
		}
		
		// For scratch builds, run on host; for parent builds, run in container
		runMode := container.RunModeContainer
		if isScratch {
			runMode = container.RunModeHost
		}
		
		out := container.NewBufLogWriter("stdout")
		if err := c.Run(ctx, cmd, runMode, out); err != nil {
			log.Warn("Failed to import GPG key (continuing)", "repo", repo.Path, "error", err)
			// Don't fail the build if GPG import fails - the repo might work without it
			// or the user might have configured GPG checking differently
		} else {
			log.Info("Successfully imported GPG key", "repo", repo.Path)
		}
	}
	
	return nil
}

// writeFiles writes all custom files to the container.
// Files can be specified as inline content, local files, or URLs.
// This is useful for adding configuration files, scripts, etc.
func (b *Builder) writeFiles(c container.Container) error {
	log := slog.With("component", "builder")
	for _, file := range b.cfg.Layer.Files {
		log.Info("Writing Files:", "file", file.Path)
		if err := c.WriteFile(file); err != nil {
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

	// Scratch build: bootstrap a new filesystem from nothing
	if b.cfg.Meta.From == "scratch" {
		if !b.backend.SupportsInstallRoot() {
			return fmt.Errorf("backend %s does not support scratch builds", b.cfg.Layer.Manager.Name)
		}

		// For DNF scratch builds, initialize the base directory structure first
		// This works around issues with the filesystem package failing to unpack
		if b.cfg.Layer.Manager.Name == "dnf" {
			log.Debug("Pre-creating base directory structure for DNF scratch build")
			mountPath := c.MountPath()
			baseDirs := []string{
				"/dev", "/proc", "/sys", "/tmp", "/run", "/var", "/var/lib", "/var/lib/rpm",
				"/etc", "/etc/yum.repos.d", "/usr", "/usr/bin", "/usr/lib", "/usr/lib64",
				"/usr/sbin", "/usr/share", "/boot", "/home", "/root", "/opt", "/srv", "/media", "/mnt",
			}
			for _, dir := range baseDirs {
				fullPath := mountPath + dir
				if err := os.MkdirAll(fullPath, 0755); err != nil {
					log.Warn("Failed to create base directory", "dir", dir, "error", err)
				}
			}

			// Initialize RPM database
			log.Debug("Initializing RPM database")
			rpmdbCmd := []string{"rpm", "--root", mountPath, "--initdb"}
			out := b.backend.OutputWriter()
			if err := c.Run(ctx, rpmdbCmd, container.RunModeHost, out); err != nil {
				log.Warn("Failed to initialize RPM database", "error", err)
			}
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
				if exitCode > 0 && b.backend.IsAcceptableExitCode(exitCode, out.String()) {
					log.Warn("Command returned non-zero exit code but packages were installed successfully",
						"exitCode", exitCode, "cmd", cmd)
					// Treat as success
					continue
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
			out := b.backend.OutputWriter()
			if err := c.Run(ctx, cmd, container.RunModeContainer, out); err != nil {
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
// Two command types are supported:
//   - run: Simple command (e.g., "systemctl enable myservice")
//   - script: Multi-line bash script
//
// All commands run inside the container using "buildah run".
func (b *Builder) runCommands(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")
	log.Info("Starting Run Commands:", "commands", b.cfg.Layer.Actions.Commands)

	for _, cmd := range b.cfg.Layer.Actions.Commands {
		log.Debug("Executing", "command", cmd)

		switch cmd.Type() {
		case config.CommandRun:
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
			// Execute a multi-line script
			out := container.NewBufLogWriter("stdout")
			if err := c.RunScript(ctx, cmd.Script, out); err != nil {
				return fmt.Errorf("run script: %w", err)
			}

		default:
			return fmt.Errorf("command has no run or script")
		}
	}

	log.Info("Done Run Commands:", "commands", b.cfg.Layer.Actions.Commands)
	return nil
}

// removePackages removes packages from the container if specified in the configuration.
// Uses rpm -e --nodeps for RPM-based systems or dpkg --remove for Debian-based systems.
// This is useful for minimizing image size by removing unnecessary packages.
func (b *Builder) removePackages(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")
	
	packages := b.cfg.Layer.Actions.Install.RemovePackages
	if len(packages) == 0 {
		return nil
	}
	
	log.Info("Removing packages", "count", len(packages), "packages", packages)
	
	cmd := b.backend.RemovePackagesCommand(packages)
	if cmd == nil {
		log.Warn("Backend does not support package removal")
		return nil
	}
	
	out := container.NewBufLogWriter("stdout")
	if err := c.Run(ctx, cmd, container.RunModeContainer, out); err != nil {
		log.Warn("Failed to remove some packages (may be expected)", "error", err)
		// Don't fail the build if package removal fails - some packages may not exist
		return nil
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
