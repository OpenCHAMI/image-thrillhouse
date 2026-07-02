// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package container defines interfaces for container operations.
package container

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
)

// RunCmd wraps c.Run with a default BufLogWriter for callers that just need
// "run this, log output at DEBUG (or ERROR on failure), surface errors".
// Eliminates the boilerplate of allocating a writer at every site —
// previously a 2-line pattern repeated a dozen+ times across builder/oscap/
// ansible_incontainer.
//
// component is the canonical slog "component" name (see streamlog.go) that
// will be attached to the captured-output records. Required so command output
// groups with the rest of each caller's logs instead of being unattributed.
//
// Callers that need to inspect the captured output (e.g. acceptable-exit-code
// checks) should keep calling c.Run directly with a CapturingWriter.
func RunCmd(ctx context.Context, c Container, component string, cmd []string, mode RunMode, opts ...RunOption) error {
	return c.Run(ctx, cmd, mode, NewBufLogWriter(component, "stdout"), opts...)
}

// RunScriptCmd is the RunScript analogue of RunCmd — same boilerplate-saver
// for callers that only care about pass/fail and don't need the writer.
// See RunCmd for the component parameter.
func RunScriptCmd(ctx context.Context, c Container, component string, script string, opts ...RunOption) error {
	return c.RunScript(ctx, script, NewBufLogWriter(component, "stdout"), opts...)
}

// BufLogWriter is a thread-safe buffered writer that logs output line-by-line.
// It buffers all writes and logs them when Flush is called.
// Used for capturing and logging command output from containers.
type BufLogWriter struct {
	mu        sync.Mutex // Protects concurrent writes
	buf       []byte     // Buffered output
	component string     // canonical slog component (e.g. "oscap", "builder")
	key       string     // Log attribute key (e.g., "stdout", "stderr")
}

// NewBufLogWriter creates a new buffered log writer tagged with the given
// component (see streamlog.go for the canonical names). key is used as a
// log attribute when output is flushed (e.g., "stdout").
func NewBufLogWriter(component, key string) *BufLogWriter {
	return &BufLogWriter{component: component, key: key}
}

// Write appends data to the buffer in a thread-safe manner.
// Implements io.Writer interface.
func (w *BufLogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	return len(p), nil
}

// Flush logs the buffered output as a single format-aware block and clears
// the buffer. Output is logged at DEBUG when err is nil, ERROR otherwise.
// See LogStreamBlock for how the block is rendered per --log-format.
func (w *BufLogWriter) Flush(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) == 0 {
		return
	}
	level := slog.LevelDebug
	msg := "command output"
	if err != nil {
		level = slog.LevelError
		msg = "command output (failed)"
	}
	LogStreamBlock(level, msg, string(w.buf), "component", w.component, "stream", w.key)
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
//
// The captured buffer is owned by CapturingWriter, not the delegate, so
// String() remains valid after Flush — by design, since callers typically
// inspect output for acceptable-exit-code checks after the underlying
// command has already returned and the delegate has been flushed.
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
