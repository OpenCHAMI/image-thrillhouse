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

// ansiRe matches CSI ANSI escape sequences (color, cursor moves) that DNF
// and user scripts occasionally emit even when not on a TTY.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

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
	w := bufio.NewWriter(os.Stderr)
	fmt.Fprintln(w, "┌──── output ────")
	for _, l := range lines {
		fmt.Fprint(w, "│ ")
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

	// Format: level=LEVEL msg="message" key=value key=value...
	// Similar to text format but without timestamp
	fmt.Fprintf(buf, "level=%s", r.Level.String())

	if r.Message != "" {
		fmt.Fprintf(buf, " msg=%q", r.Message)
	}

	// Add attributes
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != "" {
			fmt.Fprintf(buf, " %s=", a.Key)
			h.appendValue(buf, a.Value)
		}
		return true
	})

	// Add handler-level attributes
	for _, a := range h.attrs {
		if a.Key != "" {
			fmt.Fprintf(buf, " %s=", a.Key)
			h.appendValue(buf, a.Value)
		}
	}

	fmt.Fprintln(buf)
	return buf.Flush()
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
