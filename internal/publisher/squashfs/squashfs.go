package squashfs

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/travisbcotton/image-build/internal/container"
)

type SquashfsPublisher struct {
	path string
}

func New(path string) *SquashfsPublisher {
	return &SquashfsPublisher{path: path}
}

func (s *SquashfsPublisher) Publish(ctx context.Context, c container.Container, name, tag string) error {
	output := fmt.Sprintf("%s/%s-%s.squashfs", s.path, name, tag)
	cmd := exec.CommandContext(ctx, "mksquashfs", c.MountPath(), output, "-noappend")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mksquashfs: %w", err)
	}
	fmt.Printf("published squashfs: %s\n", output)
	return nil
}
