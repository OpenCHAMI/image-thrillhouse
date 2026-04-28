// Package builder orchestrates the image building process.
// It coordinates between the backend (package manager), container operations,
// and publishers to create and distribute container images.
package builder

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mattn/go-shellwords"
	"github.com/travisbcotton/image-build/internal/backend"
	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
	"github.com/travisbcotton/image-build/internal/labels"
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

	// Write repository configurations (e.g., yum repos)
	if err := b.writeRepos(c); err != nil {
		return fmt.Errorf("write repos: %w", err)
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

	// Generate image labels
	log.Debug("Generating image labels")
	labelGen := labels.New(b.cfg)
	imageLabels := labelGen.Generate()
	log.Debug("Generated labels", "count", len(imageLabels))

	// Publish to all configured destinations
	for _, p := range b.publishers {
		if err := p.Publish(ctx, c, b.cfg.Meta.Name, b.cfg.Meta.Tags); err != nil {
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
		// Get commands to run on the host targeting the container mount
		cmds := b.backend.InstallRootCommands(b.cfg.Layer.Actions.Install, c.MountPath())
		for _, cmd := range cmds {
			log.Debug("Install", "action", cmd)
			out := b.backend.OutputWriter()
			if err := c.Run(ctx, cmd, container.RunModeHost, out); err != nil {
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
