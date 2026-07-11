// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

//go:build !linux

package buildah

import "os/exec"

// hardenHostCmd is a no-op off Linux: Pdeathsig and process-group kill are
// Linux-specific, and host-mode builds only run on Linux anyway. The stub
// exists so the package still compiles (and its tests run) on developer
// machines.
func hardenHostCmd(_ *exec.Cmd) {}
