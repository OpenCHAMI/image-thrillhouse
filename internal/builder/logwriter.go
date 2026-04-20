package builder

import (
	"bytes"
	"log/slog"
)

type logWriter struct {
	level slog.Level
	key   string
}

func newLogWriter(level slog.Level, key string) *logWriter {
	return &logWriter{level: level, key: key}
}

func (w *logWriter) Write(p []byte) (n int, err error) {
	// trim trailing newline since slog adds its own
	msg := string(bytes.TrimRight(p, "\n"))
	if msg == "" {
		return len(p), nil
	}
	slog.Log(nil, w.level, msg, "stream", w.key)
	return len(p), nil
}

type bufLogWriter struct {
	level  slog.Level
	key    string
	buf    bytes.Buffer
	always bool // if true, always log at level; if false, buffer and only log on flush
}

func (w *bufLogWriter) Write(p []byte) (n int, err error) {
	if w.always {
		msg := string(bytes.TrimRight(p, "\n"))
		if msg != "" {
			slog.Log(nil, w.level, msg, "stream", w.key)
		}
		return len(p), nil
	}
	return w.buf.Write(p)
}

func (w *bufLogWriter) Flush(level slog.Level) {
	if w.buf.Len() == 0 {
		return
	}
	for _, line := range bytes.Split(w.buf.Bytes(), []byte("\n")) {
		msg := string(bytes.TrimRight(line, "\n"))
		if msg != "" {
			slog.Log(nil, level, msg, "stream", w.key)
		}
	}
	w.buf.Reset()
}
