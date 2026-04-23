package container

import (
	"bytes"
	"log/slog"
	"sync"
)

type BufLogWriter struct {
	mu  sync.Mutex
	buf []byte
	key string
}

func NewBufLogWriter(key string) *BufLogWriter {
	return &BufLogWriter{key: key}
}

func (w *BufLogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func (w *BufLogWriter) Flush(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return
	}
	level := slog.LevelDebug
	if err != nil {
		level = slog.LevelError
	}
	for _, line := range bytes.Split(w.buf, []byte("\n")) {
		if msg := string(bytes.TrimRight(line, "\r\n")); msg != "" {
			slog.Log(nil, level, msg, "stream", w.key)
		}
	}
	w.buf = nil
}

type NopWriter struct{}

func (n *NopWriter) Write(p []byte) (int, error) { return len(p), nil }
func (n *NopWriter) Flush(err error)             {}
