package builder

import (
	"context"
	"fmt"
	"os"

	"github.com/containers/buildah"
	"github.com/containers/buildah/define"
	"go.podman.io/storage"

	"github.com/travisbcotton/image-build/internal/config"
)

type container interface {
	Run(cmd []string) error
	WriteFile(file config.File) error
	Commit(ctx context.Context, name, tag string) error
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

	fmt.Printf("container id: %s\n", builder.ContainerID)

	return &Container{
		Name:        name,
		fromScratch: from == "scratch",
		mountPath:   mountPath,
		Builder:     builder,
		Store:       store,
	}, nil
}

func (c *Container) Run(cmd []string) error {
	fmt.Printf("run: %v\n", cmd)
	if c.fromScratch {
		cmd = append(cmd, "--installroot", c.mountPath)
	} else {
		err := c.Builder.Run(cmd, buildah.RunOptions{
			ConfigureNetwork: define.NetworkEnabled,
			Isolation:        define.IsolationOCIRootless,
			Stdout:           os.Stdout,
			Stderr:           os.Stderr,
			AddCapabilities: []string{
				"CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_FOWNER", "CAP_FSETID", "CAP_KILL",
				"CAP_NET_BIND_SERVICE", "CAP_SETFCAP", "CAP_SETGID", "CAP_SETPCAP", "CAP_SETUID", "CAP_SYS_CHROOT",
			},
		})
		if err != nil {
			return fmt.Errorf("run %v: %w", cmd, err)
		}
	}
	return nil
}

func (c *Container) WriteFile(file config.File) error {
	fmt.Printf("write: %s\n", file.Path)
	var content []byte
	var err error

	if file.Content != "" && file.Src != "" {
		return fmt.Errorf("file %s: content and src are mutually exclusive", file.Path)
	}
	if file.Content == "" && file.Src == "" {
		return fmt.Errorf("file %s: content and src are mutually exclusive", file.Path)
	}

	if file.Content != "" {
		content = []byte(file.Content)
	} else if file.Src != "" {
		content, err = os.ReadFile(file.Src)
		if err != nil {
			return fmt.Errorf("read src %s: %w", file.Src, err)
		}
	}

	// write to temp file
	tmp, err := os.CreateTemp("", "image-build-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if _, err := tmp.Write(content); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	tmp.Close()

	if err := c.Builder.Add(file.Path, false, buildah.AddAndCopyOptions{}, tmp.Name()); err != nil {
		return fmt.Errorf("add file %s: %w", file.Path, err)
	}

	return nil
}

func (c *Container) Commit(ctx context.Context, name, tag string) error {
	fmt.Printf("commit: %s:%s\n", name, tag)
	options := buildah.CommitOptions{
		AdditionalTags: []string{fmt.Sprintf("localhost/%s:%s", name, tag)},
	}
	_, _, _, err := c.Builder.Commit(ctx, nil, options)
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	fmt.Printf("committed image localhost/%s:%s\n", name, tag)
	return nil
}

func (c *Container) Delete() {
	fmt.Println("delete container")
	c.Builder.Delete()
	c.Store.Shutdown(false)
}

func openStore() (storage.Store, error) {
	opts, err := storage.DefaultStoreOptions()
	if err != nil {
		return nil, fmt.Errorf("default store opts: %w", err)
	}
	return storage.GetStore(opts)
}
