package builder

import (
	"context"
	"fmt"
	"strings"

	"github.com/travisbcotton/image-build/internal/backend"
	"github.com/travisbcotton/image-build/internal/config"
)

type Builder struct {
	cfg          *config.Config
	backend      backend.Backend
	newContainer func(context.Context, string, string) (container, error)
}

func New(ctx context.Context, cfg *config.Config, b backend.Backend) *Builder {
	return &Builder{
		cfg:     cfg,
		backend: b,
		newContainer: func(ctx context.Context, name string, from string) (container, error) {
			return newContainer(ctx, name, from)
		},
	}
}

func (b *Builder) Build(ctx context.Context) error {
	c, err := b.newContainer(ctx, b.cfg.Meta.Name, b.cfg.Meta.From)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	defer c.Delete()

	if err := b.writeFiles(c); err != nil {
		return fmt.Errorf("write files: %w", err)
	}

	if err := b.runInstall(ctx, c); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	if err := b.runCommands(ctx, c); err != nil {
		return fmt.Errorf("run commands: %w", err)
	}

	return c.Commit(ctx, b.cfg.Meta.Name, b.cfg.Meta.Tag)
}

func (b *Builder) writeFiles(c container) error {
	for _, file := range b.cfg.Layer.Files {
		if err := c.WriteFile(file); err != nil {
			return fmt.Errorf("write file %s: %w", file.Path, err)
		}
	}
	return nil
}

func (b *Builder) runInstall(ctx context.Context, c container) error {
	if b.cfg.Meta.From == "scratch" {
		cmds := b.backend.InstallRootCommands(b.cfg.Layer.Actions.Install, c.MountPath())
		for _, cmd := range cmds {
			if err := c.Run(ctx, cmd, RunModeHost); err != nil {
				return fmt.Errorf("run root %v: %w", cmd, err)
			}
		}
	} else {
		cmds := b.backend.InstallCommands(b.cfg.Layer.Actions.Install)
		for _, cmd := range cmds {
			if err := c.Run(ctx, cmd, RunModeContainer); err != nil {
				return fmt.Errorf("run %v: %w", cmd, err)
			}
		}
	}
	return nil
}

func (b *Builder) runCommands(ctx context.Context, c container) error {
	for _, cmd := range b.cfg.Layer.Actions.Commands {
		switch cmd.Type() {
		case config.CommandRun:
			parts := strings.Fields(cmd.Run)
			if err := c.Run(ctx, parts, RunModeContainer); err != nil {
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
	return nil
}
