// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

//go:build linux

package buildah

import (
	"os/exec"
	"syscall"
	"time"
)

// hardenHostCmd confines a host-mode (RunModeHost) command so it cannot
// outlive the build:
//
//   - Setpgid puts the command and everything it spawns in a fresh process
//     group, so cancellation can kill the whole tree, not just the direct
//     child. Package managers fork freely (rpm scriptlets, gpg, …) and a
//     bare Process.Kill only reaches the top process.
//   - Pdeathsig has the kernel SIGKILL the child if this process dies first
//     (e.g. a CI runner SIGKILLs the job). Orphaned package-manager trees
//     from killed jobs were accumulating on shared runners until the
//     runner's process limit was exhausted.
//   - Cancel kills the process group (negative pid) on context
//     cancellation instead of the default single-process SIGKILL.
//   - WaitDelay unblocks Wait even if an orphaned grandchild inherited the
//     command's stdout/stderr pipes and keeps them open after the kill.
func hardenHostCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGKILL,
	}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 10 * time.Second
}
