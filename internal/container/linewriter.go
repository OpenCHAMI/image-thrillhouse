// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package container

import (
	"bytes"
	"strings"
)

// LineClassifier is the per-backend hook for a LineWriter. Each implementation
// owns whatever state it needs (e.g. "are we inside a NEW packages section?"
// for zypper, or "did we see Installed: yet?" for dnf) and decides what to
// log for each line.
//
// LineWriter feeds raw lines (no trimming) so classifiers can distinguish
// indented continuation lines from headers. After all lines are processed,
// Done is called once with the entire buffer contents and the command
// error, so classifiers can emit summary logs.
//
// A LineClassifier instance is single-use — LineWriter resets its own buffer
// but does not touch the classifier. Backends should construct a fresh
// classifier for each new LineWriter (which is what they already do when
// returning from Backend.OutputWriter()).
type LineClassifier interface {
	// Line is called for each line in the buffer, in order. The line is
	// passed verbatim (no trailing newline). hadErr indicates whether the
	// command that produced the output errored — useful for downgrading
	// "Error: ..." chatter to debug when the command succeeded anyway.
	Line(line string, hadErr bool)

	// Done is called once after all lines have been classified. raw is the
	// full pre-split buffer contents (handy for "raw output" debug logs),
	// and err is the command error (nil on success). Backends typically
	// emit summary logs here (e.g. "packages installed", "N warnings").
	Done(raw string, err error)
}

// LineWriter is a reusable scaffold for the package-manager output parsers.
// Each backend used to maintain its own tiny `bytes.Buffer + Write + Flush`
// trio with subtle differences (trailing-CR handling, where the empty-line
// filter lived, etc.). Putting that boilerplate here once means each backend
// only owns its classification logic.
type LineWriter struct {
	buf bytes.Buffer
	cls LineClassifier
}

// NewLineWriter wraps the given classifier with the shared buffer + Flush
// scaffolding. The result satisfies the OutputWriter interface.
func NewLineWriter(cls LineClassifier) *LineWriter {
	return &LineWriter{cls: cls}
}

// Write appends data to the internal buffer.
func (w *LineWriter) Write(p []byte) (int, error) {
	return w.buf.Write(p)
}

// Flush splits the buffered output into lines and feeds them to the
// classifier, then calls Done with the raw buffer contents and err. The
// internal buffer is reset before returning.
func (w *LineWriter) Flush(err error) {
	raw := w.buf.String()
	w.buf.Reset()
	for _, line := range strings.Split(raw, "\n") {
		// Strip a trailing CR for Windows-style line endings — the previous
		// dnf/zypper implementations did this on a per-line basis.
		line = strings.TrimRight(line, "\r")
		w.cls.Line(line, err != nil)
	}
	w.cls.Done(raw, err)
}
