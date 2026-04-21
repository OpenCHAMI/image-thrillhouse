package builder

import (
	"bytes"
	"log/slog"
	"strings"
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
		case strings.HasPrefix(line, "Error"):
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
