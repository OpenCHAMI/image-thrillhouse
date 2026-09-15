// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package squashfs provides a publisher that creates SquashFS filesystem images.
// SquashFS is a compressed read-only filesystem commonly used for bootable images,
// live CDs, and network boot scenarios.
package squashfs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/openchami/image-thrillhouse/internal/container"
	"github.com/openchami/image-thrillhouse/internal/fsutil"
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

// Publish writes one SquashFS image to <path>/<name>-<tags[0]>.squashfs, plus
// the image's kernel and initramfs beside it so the output is bootable on its
// own — the same three artifacts the S3 publisher uploads:
//
//	<path>/<name>-<tags[0]>.squashfs
//	<path>/<name>-<tags[0]>.vmlinuz
//	<path>/<name>-<tags[0]>.initramfs.img
//
// An image with no kernel installed (empty /lib/modules) still gets its
// squashfs, with a warning in place of the boot files. A kernel that is present
// but missing its vmlinuz or initramfs is an error.
//
// The bytes derive purely from the container mount, so one file per tag would be
// N identical files differing only in filename. tags[0] is the identifier, same
// convention as the S3 publisher.
//
// labels is ignored: a SquashFS image is a filesystem, not an OCI image.
// Requires mksquashfs (squashfs-tools) on PATH.
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

	// Locate boot files before the slow mksquashfs so a kernel with a missing
	// vmlinuz or initramfs fails without leaving a .squashfs behind that
	// Exists would report as already published.
	boot, err := fsutil.FindBootFiles(c.MountPath())
	hasKernel := !errors.Is(err, fsutil.ErrNoKernel)
	if hasKernel && err != nil {
		return fmt.Errorf("find boot files: %w", err)
	}

	log.Info("creating squashfs", "squashfs", output, "source", c.MountPath())
	if err := fsutil.MakeSquashFS(ctx, c.MountPath(), output); err != nil {
		return err
	}

	if !hasKernel {
		// Not every squashfs is a bootable rootfs; a kernel-less image still
		// publishes, it just has no boot files to accompany it.
		log.Warn("no kernel installed in image; skipping vmlinuz and initramfs")
		log.Info("published squashfs", "squashfs", output)
		return nil
	}

	vmlinuz, initramfs := s.bootPaths(name, primary)
	if err := fsutil.CopyFile(boot.Vmlinuz, vmlinuz); err != nil {
		return fmt.Errorf("copy vmlinuz: %w", err)
	}
	if err := fsutil.CopyFile(boot.Initramfs, initramfs); err != nil {
		return fmt.Errorf("copy initramfs: %w", err)
	}

	log.Info("published squashfs", "squashfs", output, "vmlinuz", vmlinuz, "initramfs", initramfs,
		"kernel_version", boot.KernelVersion)
	return nil
}

// bootPaths returns the output paths for the kernel and initramfs written
// alongside <name>-<tag>.squashfs.
func (s *SquashfsPublisher) bootPaths(name, tag string) (vmlinuz, initramfs string) {
	base := filepath.Join(s.path, fmt.Sprintf("%s-%s", name, tag))
	return base + ".vmlinuz", base + ".initramfs.img"
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
