// SPDX-FileCopyrightText: © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

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

	"github.com/travisbcotton/image-thrillhouse/internal/container"
	"github.com/travisbcotton/image-thrillhouse/internal/fsutil"
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

// Publish creates a single SquashFS image from the container filesystem.
//
// The output file is named "<name>-<tags[0]>.squashfs" inside s.path. The
// SquashFS bytes are derived purely from the container mount, so running
// mksquashfs once per tag would produce N identical files differing only
// in filename — wasteful disk and IO with no observable benefit. We
// instead write a single file named after the first ("primary") tag,
// matching the S3 publisher's convention of using tags[0] as the rootfs
// identifier.
//
// Previously the publisher hard-coded "rootfs", which silently clobbered
// the file on every build and ignored both name and tag entirely.
//
// Note: Labels are not embedded in SquashFS files as they are filesystem
// images, not OCI container images. Labels are only relevant for container
// registries.
//
// Requirements:
//   - mksquashfs command must be available (install squashfs-tools)
//   - Output directory must be writable
func (s *SquashfsPublisher) Publish(ctx context.Context, c container.Container, name string, tags []string, labels map[string]string) error {
	log := slog.With("component", "publisher.squashfs")

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(s.path, 0755); err != nil {
		return fmt.Errorf("create output directory %s: %w", s.path, err)
	}

	// Check if mksquashfs is installed
	if _, err := exec.LookPath("mksquashfs"); err != nil {
		return fmt.Errorf("mksquashfs not found: install squashfs-tools")
	}

	// Config validation requires at least one tag, but be defensive in case
	// this publisher is invoked directly.
	if len(tags) == 0 {
		return fmt.Errorf("squashfs publisher requires at least one tag")
	}

	primary := tags[0]
	output := filepath.Join(s.path, fmt.Sprintf("%s-%s.squashfs", name, primary))
	if len(tags) > 1 {
		log.Info("multiple tags configured; using the first for the filename",
			"primary", primary, "ignored_tags", tags[1:])
	}

	log.Info("creating squashfs", "squashfs", output, "source", c.MountPath())
	if err := fsutil.MakeSquashFS(ctx, c.MountPath(), output); err != nil {
		return err
	}

	log.Info("published squashfs", "squashfs", output)
	return nil
}

// Exists reports whether the squashfs output file for this (name, tags) pair
// is already present on disk. Mirrors Publish's naming: the file is named
// after the primary (first) tag, so a single stat of <path>/<name>-<tags[0]>
// .squashfs is sufficient.
func (s *SquashfsPublisher) Exists(ctx context.Context, name string, tags []string) (bool, error) {
	if len(tags) == 0 {
		return false, fmt.Errorf("squashfs publisher requires at least one tag")
	}
	output := filepath.Join(s.path, fmt.Sprintf("%s-%s.squashfs", name, tags[0]))
	_, err := os.Stat(output)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("stat %s: %w", output, err)
}
