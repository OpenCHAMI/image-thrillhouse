// Package mmdebstrap implements a backend for creating Debian/Ubuntu scratch builds.
package mmdebstrap

import (
	"log/slog"
	"strings"

	"github.com/travisbcotton/image-build/internal/container"
)

// mmdebstrapClassifier is stateless — mmdebstrap output is line-oriented
// with `I:`, `W:`, `E:` prefixes, so each line is classified independently.
type mmdebstrapClassifier struct{}

func newMmdebstrapWriter() *container.LineWriter {
	return container.NewLineWriter(&mmdebstrapClassifier{})
}

// Line classifies a single line of mmdebstrap output.
func (mmdebstrapClassifier) Line(line string, hadErr bool) {
	line = strings.TrimSpace(line)
	if line == "" || line == "done" {
		return
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
		if hadErr {
			slog.Error("mmdebstrap", "msg", line)
		} else {
			slog.Debug("mmdebstrap", "msg", line)
		}
	}
}

// Done logs raw output for consistency with other backends. mmdebstrap
// already logs line-by-line in Line(), but FlushRawDebug ensures the full
// buffer is available at DEBUG for post-mortem.
func (mmdebstrapClassifier) Done(raw string, err error) {
	container.FlushRawDebug("mmdebstrap", raw)
}
