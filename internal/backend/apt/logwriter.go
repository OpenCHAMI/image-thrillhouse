// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package apt implements the APT package manager backend for Debian and Ubuntu systems.
package apt

import (
	"log/slog"
	"strings"

	"github.com/travisbcotton/image-thrillhouse/internal/container"
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
	log                *slog.Logger
}

func newAptWriter() *container.LineWriter {
	return container.NewLineWriter(&aptClassifier{
		log: slog.With("component", "backend.apt"),
	})
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

// Done emits the summary logs. See FlushRawDebug for the shared raw-output +
// error-path policy.
func (c *aptClassifier) Done(raw string, err error) {
	container.FlushRawDebug("apt", raw)

	if len(c.newPackages) > 0 {
		c.log.Info("packages installed", "packages", c.newPackages)
	}
	if len(c.additionalPackages) > 0 {
		c.log.Info("additional packages installed", "packages", c.additionalPackages)
	}
	for _, w := range c.warnings {
		c.log.Warn("apt warning", "line", w)
	}
	if err != nil {
		for _, e := range c.errors {
			c.log.Error("apt error", "line", e)
		}
	}
}
