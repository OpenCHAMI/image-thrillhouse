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
//
// The classifier carries its own *slog.Logger so summary logs go out with
// component=backend.dnf — matches the canonical component scheme documented
// in container/streamlog.go.
type dnfClassifier struct {
	installed   []string
	warnings    []string
	errors      []string
	inInstalled bool
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
// FlushRawDebug handles the raw-output dump + the "don't re-log on error"
// policy; this method only emits the parsed summary.
func (c *dnfClassifier) Done(raw string, err error) {
	container.FlushRawDebug("dnf", raw)

	if len(c.installed) > 0 {
		c.log.Info("packages installed", "packages", c.installed)
	}
	for _, w := range c.warnings {
		c.log.Warn("dnf warning", "msg", w)
	}
	if err != nil {
		for _, e := range c.errors {
			c.log.Error("dnf error", "msg", e)
		}
	}
}
