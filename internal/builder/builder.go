package builder

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/travisbcotton/image-build/internal/backend"
	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
	"github.com/travisbcotton/image-build/internal/publisher"
)

type Builder struct {
	cfg          *config.Config
	backend      backend.Backend
	newContainer func(context.Context, string, string) (container.Container, error)
	publishers   []publisher.Publisher
}

func New(ctx context.Context, cfg *config.Config, b backend.Backend, p []publisher.Publisher) *Builder {
	return &Builder{
		cfg:     cfg,
		backend: b,
		newContainer: func(ctx context.Context, name string, from string) (container.Container, error) {
			return newContainer(ctx, name, from)
		},
		publishers: p,
	}
}

func (b *Builder) Build(ctx context.Context) error {
	c, err := b.newContainer(ctx, b.cfg.Meta.Name, b.cfg.Meta.From)
	log := slog.With("component", "builder")
	log.Info("Creating container", "id", c.GetID(), "name", c.GetName())
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	defer c.Delete()

	if err := b.applyManagerConfig(c); err != nil {
		return fmt.Errorf("write manager config: %w", err)
	}

	if err := b.writeRepos(c); err != nil {
		return fmt.Errorf("write repos: %w", err)
	}

	if err := b.writeFiles(c); err != nil {
		return fmt.Errorf("write files: %w", err)
	}

	if err := b.runInstall(ctx, c); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	if err := b.runCommands(ctx, c); err != nil {
		return fmt.Errorf("run commands: %w", err)
	}

	for _, p := range b.publishers {
		if err := p.Publish(ctx, c, b.cfg.Meta.Name, b.cfg.Meta.Tag); err != nil {
			return fmt.Errorf("publish %T: %w", p, err)
		}
	}

	return nil
}

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

func (b *Builder) runInstall(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")
	log.Info("Starting install commands:", "install", b.cfg.Layer.Actions.Install)
	if b.cfg.Meta.From == "scratch" {
		cmds := b.backend.InstallRootCommands(b.cfg.Layer.Actions.Install, c.MountPath())
		for _, cmd := range cmds {
			log.Debug("Install", "action", cmd)
			if err := c.Run(ctx, cmd, container.RunModeHost); err != nil {
				return fmt.Errorf("run root %v: %w", cmd, err)
			}
		}
	} else {
		cmds := b.backend.InstallCommands(b.cfg.Layer.Actions.Install)
		for _, cmd := range cmds {
			log.Debug("Install", "action", cmd)
			if err := c.Run(ctx, cmd, container.RunModeContainer); err != nil {
				return fmt.Errorf("run %v: %w", cmd, err)
			}
		}
	}
	log.Info("Done installing commands:", "install", b.cfg.Layer.Actions.Install)
	return nil
}

func (b *Builder) runCommands(ctx context.Context, c container.Container) error {
	log := slog.With("component", "builder")
	log.Info("Starting Run Commands:", "commands", b.cfg.Layer.Actions.Commands)
	for _, cmd := range b.cfg.Layer.Actions.Commands {
		log.Debug("Executing", "command", cmd)
		switch cmd.Type() {
		case config.CommandRun:
			parts := strings.Fields(cmd.Run)
			if err := c.Run(ctx, parts, container.RunModeContainer); err != nil {
				return fmt.Errorf("run %s: %w", cmd.Run, err)
			}
		case config.CommandScript:
			if err := c.RunScript(ctx, cmd.Script); err != nil {
				return fmt.Errorf("run script: %w", err)
			}
		default:
			return fmt.Errorf("command has no run or script")
		}
	}
	log.Info("Done Run Commands:", "commands", b.cfg.Layer.Actions.Commands)
	return nil
}
