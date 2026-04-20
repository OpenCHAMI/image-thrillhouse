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

	if err := os.MkdirAll(s.path, 0755); err != nil {
		return fmt.Errorf("create output directory %s: %w", s.path, err)
	}

	if _, err := exec.LookPath("mksquashfs"); err != nil {
		return fmt.Errorf("mksquashfs not found: install squashfs-tools")
	}

	cmd := exec.CommandContext(ctx, "mksquashfs", c.MountPath(), output, "-noappend")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mksquashfs: %w", err)
	}
	fmt.Printf("published squashfs: %s\n", output)
	return nil
}
