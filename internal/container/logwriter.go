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

// String returns the captured output as a string.
// This allows checking the output content before clearing the buffer.
func (w *BufLogWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}

// CapturingWriter wraps an OutputWriter and captures all output.
// This allows backends to check the output content when determining
// if an exit code should be tolerated.
type CapturingWriter struct {
	delegate OutputWriter
	buf      bytes.Buffer
	mu       sync.Mutex
}

// NewCapturingWriter creates a writer that captures output while delegating to another writer.
func NewCapturingWriter(delegate OutputWriter) *CapturingWriter {
	return &CapturingWriter{delegate: delegate}
}

// Write writes to both the buffer and the delegate writer.
func (w *CapturingWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Write to capture buffer
	w.buf.Write(p)
	// Write to delegate
	return w.delegate.Write(p)
}

// Flush flushes the delegate writer.
func (w *CapturingWriter) Flush(err error) {
	w.delegate.Flush(err)
}

// String returns the captured output.
func (w *CapturingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// NopWriter is a no-op implementation of OutputWriter.
// It discards all writes and does nothing on flush.
// Useful for silencing command output.
type NopWriter struct{}

// Write discards the data and returns success.
func (n *NopWriter) Write(p []byte) (int, error) { return len(p), nil }

// Flush is a no-op.
func (n *NopWriter) Flush(err error) {}
