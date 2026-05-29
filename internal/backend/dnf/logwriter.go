// Package dnf provides a backend for DNF package manager.
package dnf

import (
	"bytes"
	"log/slog"
	"strings"
)

// dnfLogWriter buffers and parses DNF command output.
// It extracts installed packages, warnings, and errors from DNF's output.
type dnfLogWriter struct {
	buf bytes.Buffer
}

// Write buffers the output data for later processing by Flush.
// Implements io.Writer interface.
func (w *dnfLogWriter) Write(p []byte) (n int, err error) {
	return w.buf.Write(p)
}

// Flush processes the buffered DNF output and logs relevant information.
// It parses DNF's output format to extract:
//   - Installed packages (lines after "Installed:" section)
//   - Warnings (e.g., unable to detect release version)
//   - Errors (lines starting with "Error:")
//
// The buffer is reset after processing.
func (w *dnfLogWriter) Flush(err error) {
	output := w.buf.String()

	// Log full raw output for debugging
	slog.Debug("DNF raw output", "output", output)

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
		// Log all errors
		for _, e := range errors {
			slog.Error("dnf error", "msg", e)
		}
		// Always log the full output when there's an error for debugging
		slog.Error("DNF command failed", "full_output", output, "exit_error", err)
	}
}
