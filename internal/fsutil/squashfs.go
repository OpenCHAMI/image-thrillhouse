// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package fsutil holds small filesystem helpers shared by the publishers.
package fsutil

import (
	"context"
	"fmt"
	"os/exec"
)

// virtualMounts are the kernel/runtime pseudo-filesystem mountpoints that must
// exist as empty directories in the root filesystem. Booting an image (e.g. via
// the dmsquash-live dracut module) mounts proc, sysfs, devtmpfs and tmpfs over
// these; if the directories are missing, the mounts — and boot — fail.
//
// Each entry is a mksquashfs pseudo-file directory definition of the form
// "<name> d <mode> <uid> <gid>". The modes mirror a conventional Linux root:
// proc and sys are 0555 (mounted read-only), dev and run are 0755.
var virtualMounts = []string{
	"proc d 555 0 0",
	"sys d 555 0 0",
	"dev d 755 0 0",
	"run d 755 0 0",
}

// MakeSquashFS runs `mksquashfs srcDir dstPath` with the project's standard
// flags. It was duplicated between the squashfs and S3 publishers — the only
// observable difference was how they handled stderr — so the shared form
// always returns the combined output on failure for diagnostics and discards
// it on success.
//
// The kernel/runtime mountpoints (proc, sys, dev, run) are handled specially:
// we exclude them from the source so the transient host mounts that show
// through the buildah overlay while the container is still mounted don't get
// captured, then recreate them as empty directories via pseudo-file
// definitions. This leaves the mountpoints present (so dmsquash-live and other
// init paths can mount over them) without injecting any of the build host's
// files.
//
// Flags:
//   - -noappend: always create a new image (don't append)
//   - -no-progress: disable the progress bar so logs stay clean
//   - -p "<name> d ...": create the empty mountpoint directories
//   - -e proc sys dev run: exclude the host mount contents. The `-e` option
//     must come last; everything after it is treated as an exclude pattern.
func MakeSquashFS(ctx context.Context, srcDir, dstPath string) error {
	args := []string{srcDir, dstPath, "-noappend", "-no-progress"}
	for _, m := range virtualMounts {
		args = append(args, "-p", m)
	}
	// -e must be last: mksquashfs treats every argument after it as an
	// exclude pattern.
	args = append(args, "-e", "proc", "sys", "dev", "run")

	cmd := exec.CommandContext(ctx, "mksquashfs", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mksquashfs %s: %w (output: %s)", dstPath, err, string(out))
	}
	return nil
}
