package dnf

import (
	"bytes"
	"log/slog"
	"strings"
)

type dnfLogWriter struct {
	buf bytes.Buffer
}

func (w *dnfLogWriter) Write(p []byte) (n int, err error) {
	return w.buf.Write(p)
}

func (w *dnfLogWriter) Flush(err error) {
	output := w.buf.String()
	w.buf.Reset()

	var installed []string
	var warnings []string
	var errors []string

	inInstalled := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case line == "Installed:":
			inInstalled = true
		case line == "Upgraded:" || line == "Removed:" || line == "Failed:":
			inInstalled = false
		case inInstalled:
			installed = append(installed, line)
		case strings.HasPrefix(line, "Unable to detect release version"):
			warnings = append(warnings, line)
		case strings.HasPrefix(line, "Error:"):
			errors = append(errors, line)
			inInstalled = false
		}
	}

	if len(installed) > 0 {
		slog.Info("packages installed", "packages", installed)
	}
	for _, w := range warnings {
		slog.Warn("dnf warning", "msg", w)
	}
	if err != nil {
		for _, e := range errors {
			slog.Error("dnf error", "msg", e)
		}
	}
}
