package builder

import (
	"fmt"

	"github.com/travisbcotton/image-build/internal/backend"
	"github.com/travisbcotton/image-build/internal/config"
)

type Builder struct {
	cfg     *config.Config
	backend backend.Backend
}

func New(cfg *config.Config, b backend.Backend) *Builder {
	return &Builder{
		cfg:     cfg,
		backend: b,
	}
}

func (b *Builder) Build() error {
	// placeholder for now, real buildah calls will go here
	var installRoot bool
	mountPath := "/var/lib/containers/storage/overlay/XXXYYY"

	if b.cfg.Meta.From == "scratch" {
		// mount the container, get installRoot path
		installRoot = true
	} else {
		// pull/reuse the parent image as a working container
		installRoot = false
	}

	for _, file := range b.cfg.Layer.Files {
		filePath := mountPath + file.Path
		fmt.Printf("%s\n", filePath)
	}

	cmds := b.backend.InstallCommands(b.cfg.Layer.Actions.Install)
	if installRoot {
		for _, cmd := range cmds {
			cmd = append(cmd, "--installroot", mountPath)
			fmt.Printf("%s\n", cmd)
		}
	} else {
		for _, cmd := range cmds {
			fmt.Printf("%s\n", cmd)
		}
	}

	return nil
}
