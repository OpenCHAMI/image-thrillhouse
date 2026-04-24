// internal/backend/apt/logwriter.go
package apt

import (
	"bytes"
	"log/slog"
	"strings"
)

type aptLogWriter struct {
	buf bytes.Buffer
}

func (w *aptLogWriter) Write(p []byte) (n int, err error) {
	return w.buf.Write(p)
}

func (w *aptLogWriter) Flush(err error) {
	output := w.buf.String()
	w.buf.Reset()

	var newPackages []string
	var additionalPackages []string
	var warnings []string
	var errors []string

	inNewPackages := false
	inAdditional := false

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)

		// stop collecting package names when we hit a non-indented line
		// or a line that doesn't look like package names
		if inNewPackages || inAdditional {
			if trimmed == "" || !strings.HasPrefix(line, " ") {
				inNewPackages = false
				inAdditional = false
			}
		}

		switch {
		case trimmed == "The following NEW packages will be installed:":
			inNewPackages = true
			inAdditional = false
			continue
		case trimmed == "The following additional packages will be installed:":
			inAdditional = true
			inNewPackages = false
			continue
		case inNewPackages:
			newPackages = append(newPackages, strings.Fields(trimmed)...)
			continue
		case inAdditional:
			additionalPackages = append(additionalPackages, strings.Fields(trimmed)...)
			continue
		case strings.Contains(trimmed, "invoke-rc.d: WARNING"):
			warnings = append(warnings, trimmed)
		case strings.HasPrefix(trimmed, "W:"):
			warnings = append(warnings, strings.TrimPrefix(trimmed, "W: "))
		case strings.HasPrefix(trimmed, "E:"):
			errors = append(errors, strings.TrimPrefix(trimmed, "E: "))
		}
	}

	if len(newPackages) > 0 {
		slog.Info("packages installed", "packages", newPackages)
	}
	if len(additionalPackages) > 0 {
		slog.Info("additional packages installed", "packages", additionalPackages)
	}
	for _, w := range warnings {
		slog.Warn("apt warning", "msg", w)
	}
	if err != nil {
		for _, e := range errors {
			slog.Error("apt error", "msg", e)
		}
	}
}
