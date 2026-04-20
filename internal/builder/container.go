package builder

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/containers/buildah"
	"github.com/containers/buildah/define"
	"go.podman.io/storage"

	"github.com/travisbcotton/image-build/internal/config"
)

type RunMode int

const (
	RunModeAuto      RunMode = iota // builder decides based on context
	RunModeHost                     // always exec on host (for package managers)
	RunModeContainer                // always run in container/chroot
)

type container interface {
	Run(ctx context.Context, cmd []string, mode RunMode) error
	RunScript(ctx context.Context, script string) error
	WriteFile(file config.File) error
	Commit(ctx context.Context, name, tag string) error
	Delete()
	MountPath() string
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

func (c *Container) Run(ctx context.Context, cmd []string, mode RunMode) error {
	if c.fromScratch {
		switch mode {
		case RunModeHost:
			// exec directly, used for dnf --installroot
			command := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
			command.Stdout = os.Stdout
			command.Stderr = os.Stderr
			return command.Run()
		case RunModeContainer:
			// chroot into mountpath, rootfs must have a shell
			return c.Builder.Run(cmd, buildah.RunOptions{
				Isolation: define.IsolationOCIRootless,
				Stdout:    os.Stdout,
				Stderr:    os.Stderr,
				AddCapabilities: []string{
					"CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_FOWNER", "CAP_FSETID", "CAP_KILL",
					"CAP_NET_BIND_SERVICE", "CAP_SETFCAP", "CAP_SETGID", "CAP_SETPCAP", "CAP_SETUID", "CAP_SYS_CHROOT",
				},
			})
		}
	} else {
		return c.Builder.Run(cmd, buildah.RunOptions{
			Isolation: define.IsolationOCIRootless,
			Stdout:    os.Stdout,
			Stderr:    os.Stderr,
			AddCapabilities: []string{
				"CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_FOWNER", "CAP_FSETID", "CAP_KILL",
				"CAP_NET_BIND_SERVICE", "CAP_SETFCAP", "CAP_SETGID", "CAP_SETPCAP", "CAP_SETUID", "CAP_SYS_CHROOT",
			},
		})
	}
	return nil
}

func (c *Container) RunScript(ctx context.Context, script string) error {
	// write script to temp file in container
	tmpPath := fmt.Sprintf("/tmp/image-build-script-%d.sh", time.Now().UnixNano())

	if err := c.WriteFile(config.File{
		Path:    tmpPath,
		Content: script,
	}); err != nil {
		return fmt.Errorf("write script: %w", err)
	}

	// make executable and run
	if err := c.Run(ctx, []string{"chmod", "+x", tmpPath}, RunModeContainer); err != nil {
		return fmt.Errorf("chmod script: %w", err)
	}

	if err := c.Run(ctx, []string{tmpPath}, RunModeContainer); err != nil {
		return fmt.Errorf("exec script: %w", err)
	}

	// cleanup
	if err := c.Run(ctx, []string{"rm", tmpPath}, RunModeContainer); err != nil {
		return fmt.Errorf("cleanup script: %w", err)
	}

	return nil
}

func (c *Container) WriteFile(file config.File) error {
	fmt.Printf("write: %s\n", file.Path)
	var content []byte
	var err error

	// content is a yaml scalar block or string
	if file.Content != "" {
		content = []byte(file.Content)
		// content is a file on the host
	} else if file.Src != "" {
		content, err = os.ReadFile(file.Src)
		if err != nil {
			return fmt.Errorf("read src %s: %w", file.Src, err)
		}
		// content is at a remote url
	} else if file.URL != "" {
		resp, err := http.Get(file.URL)
		if err != nil {
			return fmt.Errorf("fetch %s: %w", file.URL, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("fetch %s: status %d", file.URL, resp.StatusCode)
		}

		content, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read %s: %w", file.URL, err)
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

func (c *Container) MountPath() string {
	return c.mountPath
}

func openStore() (storage.Store, error) {
	opts, err := storage.DefaultStoreOptions()
	if err != nil {
		return nil, fmt.Errorf("default store opts: %w", err)
	}
	return storage.GetStore(opts)
}
