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
		line = strings.TrimSpace(line)
		if line == "" {
			inNewPackages = false
			inAdditional = false
			continue
		}

		switch {
		case line == "The following NEW packages will be installed:":
			inNewPackages = true
			inAdditional = false
		case line == "The following additional packages will be installed:":
			inAdditional = true
			inNewPackages = false
		case inNewPackages:
			newPackages = append(newPackages, strings.Fields(line)...)
		case inAdditional:
			additionalPackages = append(additionalPackages, strings.Fields(line)...)
		case strings.HasPrefix(line, "invoke-rc.d: WARNING"):
			warnings = append(warnings, line)
		case strings.HasPrefix(line, "W:"):
			warnings = append(warnings, strings.TrimPrefix(line, "W: "))
		case strings.HasPrefix(line, "E:"):
			errors = append(errors, strings.TrimPrefix(line, "E: "))
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
