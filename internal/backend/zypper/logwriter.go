// Package zypper implements the Zypper package manager backend for openSUSE and SLES.
package zypper

import (
	"log/slog"
	"strings"

	"github.com/travisbcotton/image-build/internal/container"
)

// zypperClassifier parses Zypper output. Two state flags track whether we
// are currently inside the "NEW packages" section (whose lines we collect)
// or one of the recommended/suggested sections (which we silently skip).
//
// The classifier needs both the raw line (to detect indentation) and the
// trimmed value (for prefix matching); the LineWriter passes the raw line
// and we trim once here.
type zypperClassifier struct {
	newPackages   []string
	errors        []string
	inNewPackages bool
	inOther       bool
}

func newZypperWriter() *container.LineWriter {
	return container.NewLineWriter(&zypperClassifier{})
}

// Line classifies one line of Zypper output.
func (c *zypperClassifier) Line(line string, hadErr bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		// Blank line resets section state — zypper uses indentation +
		// blank lines to mark group boundaries.
		c.inNewPackages = false
		c.inOther = false
		return
	}

	switch {
	case strings.Contains(trimmed, "NEW packages are going to be installed"):
		c.inNewPackages = true
		c.inOther = false
	case strings.Contains(trimmed, "recommended packages were automatically selected"),
		strings.Contains(trimmed, "packages are suggested"):
		c.inNewPackages = false
		c.inOther = true
	case c.inNewPackages && strings.HasPrefix(line, " "):
		c.newPackages = append(c.newPackages, strings.Fields(trimmed)...)
	case c.inOther && strings.HasPrefix(line, " "):
		// drop indented continuation lines from recommended/suggested sections
	case strings.HasPrefix(trimmed, "No provider of"):
		c.errors = append(c.errors, trimmed)
	case strings.HasPrefix(trimmed, "Overall download size:"):
		slog.Info("zypper", "msg", trimmed)
	case strings.HasPrefix(trimmed, "Continue?"):
		// suppress
	}
}

// Done emits the summary logs after classification.
func (c *zypperClassifier) Done(raw string, err error) {
	container.LogStreamBlock(slog.LevelDebug, "Zypper raw output", raw, "backend", "zypper")

	if len(c.newPackages) > 0 {
		slog.Info("packages installed", "packages", c.newPackages)
	}
	if err != nil {
		for _, e := range c.errors {
			slog.Error("zypper error", "msg", e)
		}
		// Note: We don't log the raw output block here on error because:
		// 1. The parsed errors above already show the key information
		// 2. The full raw output is available at DEBUG level (line 63)
		// 3. This prevents duplicate error messages in the output
		// 4. Matches the behavior of other backends (dnf, apt)
	}
}
