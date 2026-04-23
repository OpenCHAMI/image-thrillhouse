package buildah

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/containers/buildah"
	"github.com/containers/buildah/define"
	"go.podman.io/storage"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
)

type Container struct {
	Name        string
	fromScratch bool
	mountPath   string
	Builder     *buildah.Builder
	Store       storage.Store
}

func NewContainer(ctx context.Context, name string, from string) (container.Container, error) {
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

	mountPath, err := builder.Mount("")
	if err != nil {
		return nil, fmt.Errorf("mount: %w", err)
	}

	return &Container{
		Name:        name,
		fromScratch: from == "scratch",
		mountPath:   mountPath,
		Builder:     builder,
		Store:       store,
	}, nil
}

func (c *Container) Run(ctx context.Context, cmd []string, mode container.RunMode, out container.OutputWriter) error {
	if c.fromScratch {
		switch mode {
		case container.RunModeHost:
			// exec directly, used for dnf --installroot
			command := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
			command.Stdout = out
			command.Stderr = out
			err := command.Run()
			out.Flush(err)
			//dnfOut.Flush(err)
			if err != nil {
				return fmt.Errorf("run %v: %w", cmd, err)
			}
			return nil
		case container.RunModeContainer:
			// chroot into mountpath, rootfs must have a shell
			err := c.Builder.Run(cmd, buildah.RunOptions{
				Isolation: c.GetIsolation(),
				Stdout:    out,
				Stderr:    out,
				AddCapabilities: []string{
					"CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_FOWNER", "CAP_FSETID", "CAP_KILL",
					"CAP_NET_BIND_SERVICE", "CAP_SETFCAP", "CAP_SETGID", "CAP_SETPCAP", "CAP_SETUID", "CAP_SYS_CHROOT",
				},
			})
			out.Flush(err)
			if err != nil {
				return fmt.Errorf("run %v: %w", cmd, err)
			}
			return nil
		}
	} else {
		err := c.Builder.Run(cmd, buildah.RunOptions{
			Isolation: c.GetIsolation(),
			Stdout:    out,
			Stderr:    out,
			AddCapabilities: []string{
				"CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_FOWNER", "CAP_FSETID", "CAP_KILL",
				"CAP_NET_BIND_SERVICE", "CAP_SETFCAP", "CAP_SETGID", "CAP_SETPCAP", "CAP_SETUID", "CAP_SYS_CHROOT",
			},
		})
		out.Flush(err)
		if err != nil {
			return fmt.Errorf("run %v: %w", cmd, err)
		}
	}
	return nil
}

func (c *Container) RunScript(ctx context.Context, script string, out container.OutputWriter) error {
	// write script to temp file in container
	tmpPath := fmt.Sprintf("/tmp/image-build-script-%d.sh", time.Now().UnixNano())

	if err := c.WriteFile(config.File{
		Path:    tmpPath,
		Content: script,
	}); err != nil {
		return fmt.Errorf("write script: %w", err)
	}

	// make executable and run
	if err := c.Run(ctx, []string{"chmod", "+x", tmpPath}, container.RunModeContainer, out); err != nil {
		return fmt.Errorf("chmod script: %w", err)
	}

	if err := c.Run(ctx, []string{tmpPath}, container.RunModeContainer, out); err != nil {
		return fmt.Errorf("exec script: %w", err)
	}

	// cleanup
	if err := c.Run(ctx, []string{"rm", tmpPath}, container.RunModeContainer, out); err != nil {
		return fmt.Errorf("cleanup script: %w", err)
	}

	return nil
}

func (c *Container) WriteFile(file config.File) error {
	var content []byte
	var err error
	log := slog.With("component", "container")
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
	log.Debug("Wrtie File", "path", file.Path)

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

func (c *Container) Commit(ctx context.Context, name, tag string) (string, error) {
	log := slog.With("component", "container")
	log.Debug("Commit Container", "ID", c.GetID(), "Name", c.GetName(), "as", name, ":", tag)
	options := buildah.CommitOptions{
		AdditionalTags: []string{fmt.Sprintf("localhost/%s:%s", name, tag)},
	}
	_, _, _, err := c.Builder.Commit(ctx, nil, options)
	if err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return c.GetID(), nil
}

func (c *Container) Delete() {
	log := slog.With("component", "container")
	log.Debug("Deleting Container", "ID", c.GetID(), "Name", c.GetName())
	c.Builder.Unmount()
	c.Builder.Delete()
	c.Store.Shutdown(false)
}

func (c *Container) MountPath() string {
	return c.mountPath
}

func (c *Container) GetID() string {

	return c.Builder.ContainerID
}

func (c *Container) GetName() string {

	return c.Builder.Container
}

func (c *Container) GetIsolation() define.Isolation {
	if iso := os.Getenv("BUILDAH_ISOLATION"); iso != "" {
		switch iso {
		case "chroot":
			return define.IsolationChroot
		case "rootless":
			return define.IsolationOCIRootless
		case "oci":
			return define.IsolationOCI
		}
	}
	return define.IsolationDefault
}
