// Package mmdebstrap implements a backend for creating Debian/Ubuntu scratch builds.
package mmdebstrap

import (
	"bytes"
	"log/slog"
	"strings"
)

// mmdebstrapLogWriter buffers and parses mmdebstrap command output.
// It filters and categorizes mmdebstrap's output based on message prefixes.
type mmdebstrapLogWriter struct {
	buf bytes.Buffer
}

// Write buffers the output data for later processing by Flush.
// Implements io.Writer interface.
func (w *mmdebstrapLogWriter) Write(p []byte) (n int, err error) {
	return w.buf.Write(p)
}

// Flush processes the buffered mmdebstrap output and logs categorized messages.
// It parses mmdebstrap's output format to extract:
//   - Info messages (I: prefix) - logged at debug level unless they contain "success"
//   - Warnings (W: prefix) - logged as warnings
//   - Errors (E: prefix) - logged as errors
//   - Other messages - logged as debug (or error if err is non-nil)
//
// Empty lines and "done" messages are filtered out.
// The buffer is reset after processing.
func (w *mmdebstrapLogWriter) Flush(err error) {
	output := w.buf.String()
	w.buf.Reset()

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "done" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "I: "):
			msg := strings.TrimPrefix(line, "I: ")
			if strings.HasPrefix(msg, "success") {
				slog.Info("mmdebstrap", "msg", msg)
			} else {
				slog.Debug("mmdebstrap", "msg", msg)
			}
		case strings.HasPrefix(line, "W: "):
			slog.Warn("mmdebstrap", "msg", strings.TrimPrefix(line, "W: "))
		case strings.HasPrefix(line, "E: "):
			slog.Error("mmdebstrap", "msg", strings.TrimPrefix(line, "E: "))
		default:
			if err != nil {
				slog.Error("mmdebstrap", "msg", line)
			} else {
				slog.Debug("mmdebstrap", "msg", line)
			}
		}
	}
}
