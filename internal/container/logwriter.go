// Package container defines interfaces for container operations.
package container

import (
	"bytes"
	"log/slog"
	"sync"
)

// BufLogWriter is a thread-safe buffered writer that logs output line-by-line.
// It buffers all writes and logs them when Flush is called.
// Used for capturing and logging command output from containers.
type BufLogWriter struct {
	mu  sync.Mutex // Protects concurrent writes
	buf []byte     // Buffered output
	key string     // Log attribute key (e.g., "stdout", "stderr")
}

// NewBufLogWriter creates a new buffered log writer with the specified key.
// The key is used as a log attribute when output is flushed (e.g., "stdout").
func NewBufLogWriter(key string) *BufLogWriter {
	return &BufLogWriter{key: key}
}

// Write appends data to the buffer in a thread-safe manner.
// Implements io.Writer interface.
func (w *BufLogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// Flush logs all buffered output line-by-line and clears the buffer.
// Output is logged at DEBUG level if err is nil, or ERROR level if err is set.
// Empty lines are filtered out. The buffer is cleared after flushing.
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

// NopWriter is a no-op implementation of OutputWriter.
// It discards all writes and does nothing on flush.
// Useful for silencing command output.
type NopWriter struct{}

// Write discards the data and returns success.
func (n *NopWriter) Write(p []byte) (int, error) { return len(p), nil }

// Flush is a no-op.
func (n *NopWriter) Flush(err error) {}
