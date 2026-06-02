// Package apt implements the APT package manager backend for Debian and Ubuntu systems.
package apt

import (
	"log/slog"
	"strings"

	"github.com/travisbcotton/image-build/internal/container"
)

// aptClassifier parses apt-get output. It tracks two collection sections —
// "The following NEW packages will be installed:" and "The following
// additional packages will be installed:" — by watching for indented
// continuation lines. It also collects W:/E: prefixed warnings and errors.
type aptClassifier struct {
	newPackages        []string
	additionalPackages []string
	warnings           []string
	errors             []string
	inNewPackages      bool
	inAdditional       bool
}

func newAptWriter() *container.LineWriter {
	return container.NewLineWriter(&aptClassifier{})
}

// Line classifies a single line of apt-get output.
func (c *aptClassifier) Line(line string, hadErr bool) {
	trimmed := strings.TrimSpace(line)

	// Continuing-collection bail-out: a blank line or a non-indented
	// line ends the current section. Has to happen before the switch so
	// the next iteration sees fresh state.
	if c.inNewPackages || c.inAdditional {
		if trimmed == "" || !strings.HasPrefix(line, " ") {
			c.inNewPackages = false
			c.inAdditional = false
		}
	}

	switch {
	case trimmed == "The following NEW packages will be installed:":
		c.inNewPackages = true
		c.inAdditional = false
	case trimmed == "The following additional packages will be installed:":
		c.inAdditional = true
		c.inNewPackages = false
	case c.inNewPackages:
		c.newPackages = append(c.newPackages, strings.Fields(trimmed)...)
	case c.inAdditional:
		c.additionalPackages = append(c.additionalPackages, strings.Fields(trimmed)...)
	case strings.Contains(trimmed, "invoke-rc.d: WARNING"):
		c.warnings = append(c.warnings, trimmed)
	case strings.HasPrefix(trimmed, "W:"):
		c.warnings = append(c.warnings, strings.TrimPrefix(trimmed, "W: "))
	case strings.HasPrefix(trimmed, "E:"):
		c.errors = append(c.errors, strings.TrimPrefix(trimmed, "E: "))
	}
}

// Done emits the summary logs.
func (c *aptClassifier) Done(raw string, err error) {
	if len(c.newPackages) > 0 {
		slog.Info("packages installed", "packages", c.newPackages)
	}
	if len(c.additionalPackages) > 0 {
		slog.Info("additional packages installed", "packages", c.additionalPackages)
	}
	for _, w := range c.warnings {
		slog.Warn("apt warning", "msg", w)
	}
	if err != nil {
		for _, e := range c.errors {
			slog.Error("apt error", "msg", e)
		}
	}
}
