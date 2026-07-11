// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

//go:build !linux

package buildah

import "log/slog"

// killStrayProcesses is a no-op off Linux: the sweep works by matching
// /proc/<pid>/root against the container mount path, which only exists on
// Linux — where builds actually run. The stub keeps the package compiling
// on developer machines.
func killStrayProcesses(_ string, _ *slog.Logger) int { return 0 }
