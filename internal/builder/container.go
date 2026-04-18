package builder

import (
	"context"
	"fmt"

	"github.com/containers/buildah"
	"go.podman.io/storage"

	"github.com/travisbcotton/image-build/internal/config"
)

type container interface {
	Run(cmd []string) error
	WriteFile(file config.File) error
	Commit(name, tag string) error
	Delete()
}

type Container struct {
	Name        string
	fromScratch bool
	mountPath   string
	Builder     *buildah.Builder
	Store       storage.Store
}

func newContainer(ctx context.Context, name string, from string) (container, error) {
	// get container store
	store, err := openStore()
	if err != nil {
		return nil, fmt.Errorf("Container Store: %w", err)
	}

	// create new builder
	builder, err := buildah.NewBuilder(ctx, store, buildah.BuilderOptions{
		FromImage: from,
	})
	if err != nil {
		return nil, fmt.Errorf("new builder: %w", err)
	}

	// if from == scratch, mount container and assign
	var mountPath string
	if from == "scratch" {
		mountPath, err = builder.Mount("")
		if err != nil {
			return nil, fmt.Errorf("mount: %w", err)
		}
	} else {
		mountPath = ""
	}

	return &Container{
		Name:        name,
		fromScratch: from == "scratch",
		mountPath:   mountPath,
		Builder:     builder,
		Store:       store,
	}, nil
}

func (c *Container) Run(cmd []string) error {
	if c.fromScratch {
		cmd = append(cmd, "--installroot", c.mountPath)
	}
	fmt.Printf("run: %v\n", cmd)
	return nil
}

func (c *Container) WriteFile(file config.File) error {
	fmt.Printf("write: %s\n", file.Path)
	return nil
}

func (c *Container) Commit(name, tag string) error {
	fmt.Printf("commit: %s:%s\n", name, tag)
	return nil
}

func (c *Container) Delete() {
	fmt.Println("delete container")
}

func openStore() (storage.Store, error) {
	opts, err := storage.DefaultStoreOptions()
	if err != nil {
		return nil, fmt.Errorf("default store opts: %w", err)
	}
	return storage.GetStore(opts)
}
