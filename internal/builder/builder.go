package builder

import (
	"fmt"

	"github.com/travisbcotton/image-build/internal/backend"
	"github.com/travisbcotton/image-build/internal/config"
)

type Builder struct {
	cfg          *config.Config
	backend      backend.Backend
	newContainer func(string, string) (container, error)
}

func New(cfg *config.Config, b backend.Backend) *Builder {
	return &Builder{
		cfg:     cfg,
		backend: b,
		newContainer: func(name string, from string) (container, error) {
			return newContainer(name, from)
		},
	}
}

func (b *Builder) Build() error {
	c, err := b.newContainer(b.cfg.Meta.Name, b.cfg.Meta.From)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	defer c.Delete()

	if err := b.writeFiles(c); err != nil {
		return fmt.Errorf("write files: %w", err)
	}

	if err := b.runInstall(c); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	if err := b.runCommands(c); err != nil {
		return fmt.Errorf("run commands: %w", err)
	}

	return c.Commit(b.cfg.Meta.Name, b.cfg.Meta.Tag)
}

func (b *Builder) writeFiles(c container) error {
	for _, file := range b.cfg.Layer.Files {
		if err := c.WriteFile(file); err != nil {
			return fmt.Errorf("write file %s: %w", file.Path, err)
		}
	}
	return nil
}

func (b *Builder) runInstall(c container) error {
	cmds := b.backend.InstallCommands(b.cfg.Layer.Actions.Install)
	for _, cmd := range cmds {
		if err := c.Run(cmd); err != nil {
			return fmt.Errorf("run %v: %w", cmd, err)
		}
	}
	return nil
}

func (b *Builder) runCommands(c container) error {
	for _, cmd := range b.cfg.Layer.Actions.Commands {
		if err := c.Run([]string{cmd}); err != nil {
			return fmt.Errorf("run command %s: %w", cmd, err)
		}
	}
	return nil
}
