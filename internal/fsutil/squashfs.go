// SPDX-FileCopyrightText: © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package fsutil holds small filesystem helpers shared by the publishers.
package fsutil

import (
	"context"
	"fmt"
	"os/exec"
)

// MakeSquashFS runs `mksquashfs srcDir dstPath` with the project's standard
// flags. It was duplicated between the squashfs and S3 publishers — the only
// observable difference was how they handled stderr — so the shared form
// always returns the combined output on failure for diagnostics and discards
// it on success.
//
// Flags:
//   - -noappend: always create a new image (don't append)
//   - -no-progress: disable the progress bar so logs stay clean
//   - -e proc sys dev run: exclude transient host mounts that show through
//     the buildah overlay while the container is still mounted. The `-e`
//     option must come last; everything after it is treated as an exclude
//     pattern.
func MakeSquashFS(ctx context.Context, srcDir, dstPath string) error {
	cmd := exec.CommandContext(ctx, "mksquashfs", srcDir, dstPath,
		"-noappend", "-no-progress",
		"-e", "proc", "sys", "dev", "run")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mksquashfs %s: %w (output: %s)", dstPath, err, string(out))
	}
	return nil
}
