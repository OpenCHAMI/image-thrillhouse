// internal/backend/zypper/logwriter.go
package zypper

import (
	"bytes"
	"log/slog"
	"strings"
)

type zypperLogWriter struct {
	buf bytes.Buffer
}

func (w *zypperLogWriter) Write(p []byte) (n int, err error) {
	return w.buf.Write(p)
}

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
