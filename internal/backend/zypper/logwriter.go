// Package zypper implements the Zypper package manager backend for openSUSE and SLES.
package zypper

import (
	"bytes"
	"log/slog"
	"strings"
)

// zypperLogWriter buffers and parses Zypper command output.
// It extracts package information, download sizes, and errors from Zypper's output.
type zypperLogWriter struct {
	buf bytes.Buffer
}

// Write buffers the output data for later processing by Flush.
// Implements io.Writer interface.
func (w *zypperLogWriter) Write(p []byte) (n int, err error) {
	return w.buf.Write(p)
}

// Flush processes the buffered Zypper output and logs relevant information.
// It parses Zypper's output format to extract:
//   - New packages being installed (after "NEW packages are going to be installed")
//   - Overall download size
//   - Errors (e.g., "No provider of" messages)
//
// The parser uses state machine logic to track which section of output it's in:
//   - inNewPackages: Collecting package names from the install list
//   - inOther: Skipping recommended/suggested package sections
//
// Empty lines reset the state. Indented lines contain package names or additional info.
// The buffer is reset after processing.
func (w *zypperLogWriter) Flush(err error) {
	output := w.buf.String()
	w.buf.Reset()

	var newPackages []string
	var errors []string

	inNewPackages := false
	inOther := false

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			inNewPackages = false
			inOther = false
			continue
		}

		switch {
		case strings.Contains(trimmed, "NEW packages are going to be installed"):
			inNewPackages = true
			inOther = false
			continue
		case strings.Contains(trimmed, "recommended packages were automatically selected"):
			inNewPackages = false
			inOther = true
			continue
		case strings.Contains(trimmed, "packages are suggested"):
			inNewPackages = false
			inOther = true
			continue
		case inNewPackages && strings.HasPrefix(line, " "):
			newPackages = append(newPackages, strings.Fields(trimmed)...)
			continue
		case inOther && strings.HasPrefix(line, " "):
			continue
		case strings.HasPrefix(trimmed, "No provider of"):
			errors = append(errors, trimmed)
		case strings.HasPrefix(trimmed, "Overall download size:"):
			slog.Info("zypper", "msg", trimmed)
		case strings.HasPrefix(trimmed, "Continue?"):
			// suppress
			continue
		}
	}

	if len(newPackages) > 0 {
		slog.Info("packages installed", "packages", newPackages)
	}
	if err != nil {
		for _, e := range errors {
			slog.Error("zypper error", "msg", e)
		}
	}
}
