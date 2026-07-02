// SPDX-FileCopyrightText: © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package dnf provides a backend for DNF package manager.
package dnf

import (
	"log/slog"
	"strings"

	"github.com/travisbcotton/image-thrillhouse/internal/container"
)

// dnfClassifier parses DNF command output. It walks lines, tracks an
// "Installed:" section state, and collects warnings and errors. Summary
// logging happens in Done. The buffering/Flush plumbing is provided by
// container.LineWriter.
//
// The classifier carries its own *slog.Logger so summary logs go out with
// component=backend.dnf — matches the canonical component scheme documented
// in container/streamlog.go.
type dnfClassifier struct {
	installed   []string
	warnings    []string
	errors      []string
	inInstalled bool
	inError     bool
	log         *slog.Logger
}

func newDnfWriter() *container.LineWriter {
	return container.NewLineWriter(&dnfClassifier{
		log: slog.With("component", "backend.dnf"),
	})
}

// Line classifies one line of DNF output.
func (c *dnfClassifier) Line(line string, hadErr bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		// Blank line ends error section but not installed section
		c.inError = false
		return
	}
	switch {
	case trimmed == "Installed:":
		c.inInstalled = true
		c.inError = false
	case trimmed == "Upgraded:", trimmed == "Removed:", trimmed == "Failed:":
		c.inInstalled = false
		c.inError = false
	case c.inInstalled:
		c.installed = append(c.installed, trimmed)
	case strings.HasPrefix(trimmed, "Unable to detect release version"):
		c.warnings = append(c.warnings, trimmed)
		c.inError = false
	case strings.HasPrefix(trimmed, "Error:"):
		// Start of error section - capture this line and subsequent lines
		c.errors = append(c.errors, trimmed)
		c.inInstalled = false
		c.inError = true
	case c.inError:
		// Continuation of error section - capture all non-empty lines
		// DNF error details can be indented or not, so capture everything
		c.errors = append(c.errors, trimmed)
	}
}

// Done emits summary logs after the buffer has been fully classified.
// FlushRawDebug handles the raw-output dump + the "don't re-log on error"
// policy; this method only emits the parsed summary.
func (c *dnfClassifier) Done(raw string, err error) {
	container.FlushRawDebug("dnf", raw)

	if len(c.installed) > 0 {
		c.log.Info("packages installed", "packages", c.installed)
	}
	for _, w := range c.warnings {
		c.log.Warn("dnf warning", "line", w)
	}
	if err != nil && len(c.errors) > 0 {
		// Combine all error lines into a single message for better readability
		errorMsg := strings.Join(c.errors, "\n")
		c.log.Error("dnf error", "detail", errorMsg)
	}
}
