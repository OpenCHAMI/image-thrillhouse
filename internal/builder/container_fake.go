// internal/builder/container_fake.go
package builder

import (
	"context"
	"fmt"

	"github.com/travisbcotton/image-build/internal/config"
)

type fakeContainer struct {
	fromScratch bool
	mountPath   string
}

func newFakeContainer(from string) (container, error) {
	return &fakeContainer{
		fromScratch: from == "scratch",
		mountPath:   "/fake/mountpath",
	}, nil
}

func (c *fakeContainer) Run(ctx context.Context, cmd []string) error {
	if c.fromScratch {
		cmd = append(cmd, "--installroot", c.mountPath)
	}
	fmt.Printf("run: %v\n", cmd)
	return nil
}

func (c *fakeContainer) RunScript(ctx context.Context, script string) error {
	fmt.Printf("run: %v\n", script)
	return nil
}

func (c *fakeContainer) WriteFile(file config.File) error {
	fmt.Printf("write: %s\n", file.Path)
	return nil
}

func (c *fakeContainer) Commit(ctx context.Context, name, tag string) error {
	fmt.Printf("commit: %s:%s\n", name, tag)
	return nil
}

func (c *fakeContainer) Delete() {
	fmt.Println("delete container")
}

func (c *fakeContainer) MountPath() string {
	return c.mountPath
}
