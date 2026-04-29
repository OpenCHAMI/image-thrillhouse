// Package squashfs provides a publisher that creates SquashFS filesystem images.
// SquashFS is a compressed read-only filesystem commonly used for bootable images,
// live CDs, and network boot scenarios.
package squashfs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"github.com/travisbcotton/image-build/internal/container"
)

// SquashfsPublisher creates SquashFS images from container filesystems.
// The resulting .squashfs files can be used for network booting, diskless nodes,
// or as read-only root filesystems.
type SquashfsPublisher struct {
	path string // Output directory for SquashFS images
}

// New creates a new SquashfsPublisher with the specified output directory.
// The directory will be created if it doesn't exist.
func New(path string) *SquashfsPublisher {
	return &SquashfsPublisher{path: path}
}

// Publish creates a SquashFS image from the container filesystem.
// The output file will be named: <path>/<name>-<tag>.squashfs
//
// For example, if path="/output", name="rocky-base", tag="9.5",
// the output will be: /output/rocky-base-9.5.squashfs
//
// Note: Labels are not embedded in SquashFS files as they are filesystem images,
// not OCI container images. Labels are only relevant for container registries.
//
// Requirements:
//   - mksquashfs command must be available (install squashfs-tools)
//   - Output directory must be writable
func (s *SquashfsPublisher) Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error {
	log := slog.With("component", "publisher")
	output := fmt.Sprintf("%s/%s-%s.squashfs", s.path, name, tags[0])
	log.Info("Creating squashfs", "squashfs", output, "source", c.MountPath())
	
	// Create output directory if it doesn't exist
	if err := os.MkdirAll(s.path, 0755); err != nil {
		return fmt.Errorf("create output directory %s: %w", s.path, err)
	}

	// Check if mksquashfs is installed
	if _, err := exec.LookPath("mksquashfs"); err != nil {
		return fmt.Errorf("mksquashfs not found: install squashfs-tools")
	}

	// Create the SquashFS image
	// Options:
	//   -noappend: Always create a new image (don't append)
	//   -no-progress: Disable progress bar (for cleaner logs)
	cmd := exec.CommandContext(ctx, "mksquashfs", c.MountPath(), output, "-noappend", "-no-progress")
	cmd.Stdout = nil        // Suppress stdout
	cmd.Stderr = os.Stderr  // Show errors
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("mksquashfs: %w", err)
	}
	
	log.Info("Published squashfs", "squash", output)
	return nil
}
