package container

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
)

var logFormat atomic.Value // string: "json" or "text"

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
	if currentLogFormat() == "text" {
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
