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
	"path/filepath"

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

// Publish creates a SquashFS image for each configured tag.
//
// Each tag produces a file named "<name>-<tag>.squashfs" inside s.path —
// previously the publisher hard-coded "rootfs", which silently clobbered
// the file on every build and ignored both name and tag.
//
// The source filesystem (the buildah mount path) does not change between
// tags, so the image content is identical; we still write one file per tag
// so callers can refer to a specific version without renaming.
//
// Note: Labels are not embedded in SquashFS files as they are filesystem
// images, not OCI container images. Labels are only relevant for container
// registries.
//
// Requirements:
//   - mksquashfs command must be available (install squashfs-tools)
//   - Output directory must be writable
func (s *SquashfsPublisher) Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error {
	log := slog.With("component", "publisher")

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(s.path, 0755); err != nil {
		return fmt.Errorf("create output directory %s: %w", s.path, err)
	}

	// Check if mksquashfs is installed
	if _, err := exec.LookPath("mksquashfs"); err != nil {
		return fmt.Errorf("mksquashfs not found: install squashfs-tools")
	}

	if len(tags) == 0 {
		// Defensive — config validation already requires non-empty tags, but
		// in case a publisher is invoked directly we still want a stable
		// filename rather than overwriting "rootfs".
		tags = []string{"latest"}
	}

	for _, tag := range tags {
		output := filepath.Join(s.path, fmt.Sprintf("%s-%s.squashfs", name, tag))
		log.Info("Creating squashfs", "squashfs", output, "source", c.MountPath())

		// `-e <patterns…>` must come last; excludes transient host mounts
		// that can show through the buildah overlay while the container is
		// still mounted.
		cmd := exec.CommandContext(ctx, "mksquashfs", c.MountPath(), output,
			"-noappend", "-no-progress",
			"-e", "proc", "sys", "dev", "run")
		cmd.Stdout = nil       // Suppress stdout
		cmd.Stderr = os.Stderr // Show errors

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("mksquashfs %s: %w", output, err)
		}

		log.Info("Published squashfs", "squashfs", output)
	}
	return nil
}
