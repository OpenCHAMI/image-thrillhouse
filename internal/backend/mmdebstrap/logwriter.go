// internal/backend/mmdebstrap/logwriter.go
package mmdebstrap

import (
	"bytes"
	"log/slog"
	"strings"
)

type mmdebstrapLogWriter struct {
	buf bytes.Buffer
}

func (w *mmdebstrapLogWriter) Write(p []byte) (n int, err error) {
	return w.buf.Write(p)
}

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
