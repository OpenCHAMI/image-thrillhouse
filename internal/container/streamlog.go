package container

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
)

var logFormat atomic.Value // string: "json", "text", or "textblock"

// SetLogFormat records the active --log-format value so stream helpers can
// render captured command output in a way that matches the user's choice.
// Called once from main.setupLogger after the default slog logger is set.
// If never called, helpers default to "json" (the CLI default).
func SetLogFormat(format string) {
	logFormat.Store(format)
}

func currentLogFormat() string {
	if v := logFormat.Load(); v != nil {
		return v.(string)
	}
	return "json"
}

// LogFormat returns the active log format ("json", "text", or "textblock") so
// callers outside this package can branch on it — useful for streaming writers
// that want to bypass the textblock per-record handler and emit one atomic
// line per piece of output instead of one 4-line box.
func LogFormat() string {
	return currentLogFormat()
}

// ansiRe matches CSI ANSI escape sequences (color, cursor moves) that DNF
// and user scripts occasionally emit even when not on a TTY.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

// FlushRawDebug is the per-backend classifier's "log the full raw output at
// DEBUG, tagged with the backend name" call. Centralised so each backend
// classifier doesn't need to repeat the same LogStreamBlock incantation —
// and so the policy of NOT re-logging raw output on the error path lives in
// one place. (Each classifier already emits parsed warnings/errors on the
// error path; re-emitting the raw block there would just duplicate them.)
func FlushRawDebug(backend, raw string) {
	LogStreamBlock(slog.LevelDebug, backend+" raw output", raw, "backend", backend)
}

// LogStreamBlock emits a captured command-output buffer in a format-appropriate
// way, suitable for the bulky multi-KB output produced by package managers.
//
// json mode: one structured record at the given level with the cleaned lines
// in a "lines" array attribute. No \n-escaped soup; downstream consumers can
// iterate with jq.
//
// text mode: one structured header record at the given level, followed by a
// verbatim block written to stderr with a "│ " prefix and bracketed by box
// rules so the block is visually delimited from surrounding log lines.
//
// textblock mode: same as text mode - uses block formatting for stream output.
//
// ANSI escapes are stripped and carriage-return progress redraws are folded
// (only the final segment of each line is kept) so progress bars don't leave
// breadcrumbs in either mode. Nothing is emitted when raw is empty, when the
// cleaned output has no content, or when the level is below the active slog
// threshold.
func LogStreamBlock(level slog.Level, msg, raw string, attrs ...any) {
	if !slog.Default().Enabled(context.Background(), level) {
		return
	}
	lines := normalizeStreamLines(raw)
	if len(lines) == 0 {
		return
	}
	format := currentLogFormat()
	if format == "text" || format == "textblock" {
		slog.Log(nil, level, msg, attrs...)
		writeTextBlock(lines)
		return
	}
	attrs = append(attrs, slog.Any("lines", lines))
	slog.Log(nil, level, msg, attrs...)
}

// normalizeStreamLines splits raw into lines, folds CR-progress redraws,
// strips ANSI escapes, and trims trailing blank lines. Mid-buffer blank
// lines are preserved because they carry visual structure for human
// readers (e.g. the gap between DNF's "Dependencies resolved." and the
// transaction table).
func normalizeStreamLines(raw string) []string {
	if raw == "" {
		return nil
	}
	out := make([]string, 0, 64)
	sc := bufio.NewScanner(strings.NewReader(raw))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if idx := strings.LastIndex(line, "\r"); idx >= 0 {
			line = line[idx+1:]
		}
		line = ansiRe.ReplaceAllString(line, "")
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

func writeTextBlock(lines []string) {
	// Line prefix is ASCII "| " (not the box-drawing │) so that streamed
	// command-output blocks match TextBlockHandler.Handle and
	// ansibleStreamWriter, both of which already use "| ". The outer ┌─/└─
	// box rules stay box-drawing — they're visually distinct from content.
	w := bufio.NewWriter(os.Stderr)
	fmt.Fprintln(w, "┌──── output ────")
	for _, l := range lines {
		fmt.Fprint(w, "| ")
		fmt.Fprintln(w, l)
	}
	fmt.Fprintln(w, "└────────────────")
	_ = w.Flush()
}

// TextBlockHandler is a custom slog.Handler that formats logs in a clean,
// human-readable format without timestamps, suitable for textblock mode.
type TextBlockHandler struct {
	opts  slog.HandlerOptions
	w     io.Writer
	attrs []slog.Attr
	group string
}

// NewTextBlockHandler creates a new TextBlockHandler.
func NewTextBlockHandler(w io.Writer, opts *slog.HandlerOptions) *TextBlockHandler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	return &TextBlockHandler{
		opts: *opts,
		w:    w,
	}
}

// Enabled reports whether the handler handles records at the given level.
func (h *TextBlockHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return level >= minLevel
}

// Handle formats and writes a log record.
func (h *TextBlockHandler) Handle(_ context.Context, r slog.Record) error {
	buf := bufio.NewWriter(h.w)

	// Format: level=LEVEL key=value key=value...
	// followed by textblock containing the message
	fmt.Fprintf(buf, "level=%s", r.Level.String())

	// Collect handler-level attributes first (like component=)
	for _, a := range h.attrs {
		if a.Key != "" {
			fmt.Fprintf(buf, " %s=", a.Key)
			h.appendValue(buf, a.Value)
		}
	}

	fmt.Fprintln(buf)

	// Now emit the textblock with message and attributes
	fmt.Fprintln(buf, "┌──── output ────")
	
	// Start with the message
	if r.Message != "" {
		msgLines := strings.Split(r.Message, "\n")
		for _, line := range msgLines {
			fmt.Fprintf(buf, "| %s\n", line)
		}
	}
	
	// Add record attributes in the textblock
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != "" {
			h.appendAttrInBlock(buf, a)
		}
		return true
	})
	
	fmt.Fprintln(buf, "└────────────────")

	return buf.Flush()
}

// appendAttrInBlock formats an attribute inside the textblock
func (h *TextBlockHandler) appendAttrInBlock(buf *bufio.Writer, a slog.Attr) {
	// Handle different value types
	switch val := a.Value.Any().(type) {
	case string:
		// Check if this is a multiline string (contains \n)
		if strings.Contains(val, "\n") {
			h.formatMultilineString(buf, a.Key, val)
		} else {
			// Single-line string
			fmt.Fprintf(buf, "| %s=%q\n", a.Key, val)
		}
	case []string:
		// Array of strings - list each on its own line
		fmt.Fprintf(buf, "| %s=\n", a.Key)
		for _, item := range val {
			fmt.Fprintf(buf, "| %s\n", item)
		}
	case []int:
		fmt.Fprintf(buf, "| %s=\n", a.Key)
		for _, item := range val {
			fmt.Fprintf(buf, "| %d\n", item)
		}
	case []interface{}:
		fmt.Fprintf(buf, "| %s=\n", a.Key)
		for _, item := range val {
			fmt.Fprintf(buf, "| %v\n", item)
		}
	default:
		// For structs and other types, format them inline with reflection
		// Check if it's a struct with fields we can extract
		valStr := fmt.Sprintf("%v", val)
		
		// Check if this looks like a struct (starts with {)
		if strings.HasPrefix(valStr, "{") {
			// Parse struct fields and pretty-print them
			h.formatStructInBlock(buf, a.Key, valStr)
		} else {
			// Simple value - write on one line
			fmt.Fprintf(buf, "| %s=", a.Key)
			h.appendValue(buf, a.Value)
			fmt.Fprintln(buf)
		}
	}
}

// formatStructInBlock parses a struct string representation and formats it nicely
func (h *TextBlockHandler) formatStructInBlock(buf *bufio.Writer, key, valStr string) {
	// For a struct like {[bash systemd kernel] [] [] []}, extract the arrays
	fmt.Fprintf(buf, "| %s=\n", key)
	
	// Simple parser for struct format: {[item1 item2] [item3]}
	// Remove outer braces
	valStr = strings.TrimPrefix(valStr, "{")
	valStr = strings.TrimSuffix(valStr, "}")
	
	// Split by brackets to find arrays
	inBracket := false
	currentArray := strings.Builder{}
	
	for _, ch := range valStr {
		switch ch {
		case '[':
			inBracket = true
			currentArray.Reset()
		case ']':
			if inBracket {
				// Process the array content
				content := strings.TrimSpace(currentArray.String())
				if content != "" {
					// Split by spaces and print each item
					items := strings.Fields(content)
					for _, item := range items {
						fmt.Fprintf(buf, "| %s\n", item)
					}
				}
				inBracket = false
			}
		default:
			if inBracket {
				currentArray.WriteRune(ch)
			}
		}
	}
}

// formatMultilineString formats a multiline string attribute as a sub-block
func (h *TextBlockHandler) formatMultilineString(buf *bufio.Writer, key, val string) {
	// Write the key with an indented sub-block
	fmt.Fprintf(buf, "| %s=\n", key)
	fmt.Fprintln(buf, "| ┌─────")
	
	// Split the string by newlines and write each line with extra indentation
	lines := strings.Split(val, "\n")
	for _, line := range lines {
		fmt.Fprintf(buf, "| │ %s\n", line)
	}
	
	fmt.Fprintln(buf, "| └─────")
}

// appendValue writes a slog.Value to the buffer.
func (h *TextBlockHandler) appendValue(buf *bufio.Writer, v slog.Value) {
	switch v.Kind() {
	case slog.KindString:
		fmt.Fprintf(buf, "%q", v.String())
	case slog.KindInt64:
		fmt.Fprintf(buf, "%d", v.Int64())
	case slog.KindUint64:
		fmt.Fprintf(buf, "%d", v.Uint64())
	case slog.KindFloat64:
		fmt.Fprintf(buf, "%g", v.Float64())
	case slog.KindBool:
		fmt.Fprintf(buf, "%t", v.Bool())
	case slog.KindDuration:
		fmt.Fprintf(buf, "%s", v.Duration())
	case slog.KindTime:
		fmt.Fprintf(buf, "%s", v.Time())
	case slog.KindAny:
		fmt.Fprintf(buf, "%v", v.Any())
	default:
		fmt.Fprintf(buf, "%v", v.Any())
	}
}

// WithAttrs returns a new handler with additional attributes.
func (h *TextBlockHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)
	return &TextBlockHandler{
		opts:  h.opts,
		w:     h.w,
		attrs: newAttrs,
		group: h.group,
	}
}

// WithGroup returns a new handler with the given group name.
func (h *TextBlockHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	newGroup := name
	if h.group != "" {
		newGroup = h.group + "." + name
	}
	return &TextBlockHandler{
		opts:  h.opts,
		w:     h.w,
		attrs: h.attrs,
		group: newGroup,
	}
}
