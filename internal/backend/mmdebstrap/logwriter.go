// Package mmdebstrap implements a backend for creating Debian/Ubuntu scratch builds.
package mmdebstrap

import (
	"log/slog"
	"strings"

	"github.com/travisbcotton/image-thrillhouse/internal/container"
)

// mmdebstrapClassifier holds the per-instance slog logger so every line ends
// up tagged with component=backend.mmdebstrap. Logic is otherwise stateless —
// mmdebstrap output is line-oriented with `I:`, `W:`, `E:` prefixes, so each
// line is classified independently.
type mmdebstrapClassifier struct {
	log *slog.Logger
}

func newMmdebstrapWriter() *container.LineWriter {
	return container.NewLineWriter(&mmdebstrapClassifier{
		log: slog.With("component", "backend.mmdebstrap"),
	})
}

// Line classifies a single line of mmdebstrap output.
func (c *mmdebstrapClassifier) Line(line string, hadErr bool) {
	line = strings.TrimSpace(line)
	if line == "" || line == "done" {
		return
	}
	switch {
	case strings.HasPrefix(line, "I: "):
		msg := strings.TrimPrefix(line, "I: ")
		if strings.HasPrefix(msg, "success") {
			c.log.Info("mmdebstrap", "msg", msg)
		} else {
			c.log.Debug("mmdebstrap", "msg", msg)
		}
	case strings.HasPrefix(line, "W: "):
		c.log.Warn("mmdebstrap", "msg", strings.TrimPrefix(line, "W: "))
	case strings.HasPrefix(line, "E: "):
		c.log.Error("mmdebstrap", "msg", strings.TrimPrefix(line, "E: "))
	default:
		if hadErr {
			c.log.Error("mmdebstrap", "msg", line)
		} else {
			c.log.Debug("mmdebstrap", "msg", line)
		}
	}
}

// Done logs raw output for consistency with other backends. mmdebstrap
// already logs line-by-line in Line(), but FlushRawDebug ensures the full
// buffer is available at DEBUG for post-mortem.
func (c *mmdebstrapClassifier) Done(raw string, err error) {
	container.FlushRawDebug("mmdebstrap", raw)
}
