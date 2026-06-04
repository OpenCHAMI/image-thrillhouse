// Package dnf provides a backend for DNF package manager.
package dnf

import (
	"log/slog"
	"strings"

	"github.com/travisbcotton/image-build/internal/container"
)

// dnfClassifier parses DNF command output. It walks lines, tracks an
// "Installed:" section state, and collects warnings and errors. Summary
// logging happens in Done. The buffering/Flush plumbing is provided by
// container.LineWriter.
type dnfClassifier struct {
	installed   []string
	warnings    []string
	errors      []string
	inInstalled bool
}

func newDnfWriter() *container.LineWriter {
	return container.NewLineWriter(&dnfClassifier{})
}

// Line classifies one line of DNF output.
func (c *dnfClassifier) Line(line string, hadErr bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}
	switch {
	case trimmed == "Installed:":
		c.inInstalled = true
	case trimmed == "Upgraded:", trimmed == "Removed:", trimmed == "Failed:":
		c.inInstalled = false
	case c.inInstalled:
		c.installed = append(c.installed, trimmed)
	case strings.HasPrefix(trimmed, "Unable to detect release version"):
		c.warnings = append(c.warnings, trimmed)
	case strings.HasPrefix(trimmed, "Error:"):
		c.errors = append(c.errors, trimmed)
		c.inInstalled = false
	}
}

// Done emits summary logs after the buffer has been fully classified.
func (c *dnfClassifier) Done(raw string, err error) {
	container.LogStreamBlock(slog.LevelDebug, "DNF raw output", raw, "backend", "dnf")

	if len(c.installed) > 0 {
		slog.Info("packages installed", "packages", c.installed)
	}
	for _, w := range c.warnings {
		slog.Warn("dnf warning", "msg", w)
	}
	if err != nil {
		for _, e := range c.errors {
			slog.Error("dnf error", "msg", e)
		}
		// Note: We don't log the raw output block here on error because:
		// 1. The parsed errors above already show the key information
		// 2. The full raw output is available at DEBUG level (line 49)
		// 3. This prevents duplicate error messages in the output
		// 4. Matches the behavior of other backends (apt, zypper)
	}
}
