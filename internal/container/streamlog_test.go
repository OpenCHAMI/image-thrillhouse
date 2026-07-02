// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package container

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestTextBlockHandler_SimpleMessage verifies that a record with no bulky
// attributes collapses to a single header line with the [component] prefix
// and no box.
func TestTextBlockHandler_SimpleMessage(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler).With("component", "builder")

	logger.Info("Simple test message")

	output := buf.String()
	if !strings.Contains(output, "INFO") {
		t.Errorf("Expected INFO in output, got: %s", output)
	}
	if !strings.Contains(output, "[builder]") {
		t.Errorf("Expected [builder] component prefix in output, got: %s", output)
	}
	if !strings.Contains(output, "Simple test message") {
		t.Errorf("Expected message in output, got: %s", output)
	}
	if strings.Contains(output, "┌──── output ────") {
		t.Errorf("Scalar-only record must not emit a box, got: %s", output)
	}
	if lines := strings.Count(strings.TrimRight(output, "\n"), "\n"); lines != 0 {
		t.Errorf("Scalar-only record must be a single line, got: %s", output)
	}
}

// TestTextBlockHandler_StructAttribute verifies that struct-valued attributes
// still get the box treatment with their fields broken out.
func TestTextBlockHandler_StructAttribute(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler).With("component", "builder")

	// Simulate the Install struct format
	type Install struct {
		Packages []string
		Groups   []string
		Modules  []interface{}
	}
	install := Install{
		Packages: []string{"bash", "systemd", "kernel"},
		Groups:   []string{},
		Modules:  []interface{}{},
	}

	logger.Info("Done installing commands:", "install", install)

	output := buf.String()
	t.Logf("Output:\n%s", output)

	// Header line carries level, component, and message
	if !strings.Contains(output, "INFO") {
		t.Errorf("Expected INFO in output")
	}
	if !strings.Contains(output, "[builder]") {
		t.Errorf("Expected [builder] in output")
	}
	if !strings.Contains(output, "Done installing commands:") {
		t.Errorf("Expected message on header line")
	}

	// The struct attr goes in the box
	if !strings.Contains(output, "┌──── output ────") {
		t.Errorf("Expected textblock header")
	}
	if !strings.Contains(output, "| bash") {
		t.Errorf("Expected 'bash' in textblock")
	}
	if !strings.Contains(output, "| systemd") {
		t.Errorf("Expected 'systemd' in textblock")
	}
	if !strings.Contains(output, "| kernel") {
		t.Errorf("Expected 'kernel' in textblock")
	}
	if !strings.Contains(output, "└────────────────") {
		t.Errorf("Expected textblock footer")
	}
}

// TestTextBlockHandler_MultipleAttributes verifies scalar attributes render
// inline on the header line.
func TestTextBlockHandler_MultipleAttributes(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler).With("component", "builder")

	logger.Info("Test with attributes", "name", "myimage", "count", 42)

	output := buf.String()
	t.Logf("Output:\n%s", output)

	if !strings.Contains(output, "INFO") {
		t.Errorf("Expected INFO")
	}
	if !strings.Contains(output, "Test with attributes") {
		t.Errorf("Expected message on header line")
	}
	if !strings.Contains(output, `name="myimage"`) {
		t.Errorf("Expected inline name attribute")
	}
	if !strings.Contains(output, "count=42") {
		t.Errorf("Expected inline count attribute")
	}
	if strings.Contains(output, "┌──── output ────") {
		t.Errorf("Scalar-only record must not emit a box, got: %s", output)
	}
}

// TestTextBlockHandler_ShortListInline verifies that lists with at most
// inlineListMax elements render on the header line as key=[a b c].
func TestTextBlockHandler_ShortListInline(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler).With("component", "builder")

	logger.Info("computed tags", "tags", []string{"abc123", "latest"})

	output := buf.String()
	t.Logf("Output:\n%s", output)

	if !strings.Contains(output, "tags=[abc123 latest]") {
		t.Errorf("Expected inline short list 'tags=[abc123 latest]', got: %s", output)
	}
	if strings.Contains(output, "┌──── output ────") {
		t.Errorf("Short list must not emit a box, got: %s", output)
	}
}

// TestTextBlockHandler_LongListBoxed verifies that lists longer than
// inlineListMax get one item per line inside the box.
func TestTextBlockHandler_LongListBoxed(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler).With("component", "backend.dnf")

	pkgs := []string{"bash", "systemd", "kernel", "coreutils", "glibc"}
	logger.Info("packages installed", "packages", pkgs)

	output := buf.String()
	t.Logf("Output:\n%s", output)

	if !strings.Contains(output, "packages installed") {
		t.Errorf("Expected message on header line")
	}
	if !strings.Contains(output, "┌──── output ────") {
		t.Errorf("Expected box for long list")
	}
	if !strings.Contains(output, "| packages=") {
		t.Errorf("Expected packages= key in box")
	}
	for _, p := range pkgs {
		if !strings.Contains(output, "| "+p) {
			t.Errorf("Expected %q on its own line in box", p)
		}
	}
}

// TestTextBlockHandler_RecordLevelComponent verifies that a component passed
// as a record-level attr (the LogStreamBlock pattern) is hoisted into the
// [name] header prefix rather than rendered as a key=value pair.
func TestTextBlockHandler_RecordLevelComponent(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)

	logger.Debug("dnf raw output", "component", "backend.dnf", "stream", "stdout")

	output := buf.String()
	t.Logf("Output:\n%s", output)

	if !strings.Contains(output, "[backend.dnf]") {
		t.Errorf("Expected [backend.dnf] prefix, got: %s", output)
	}
	if strings.Contains(output, `component=`) {
		t.Errorf("component must not render as key=value, got: %s", output)
	}
	if !strings.Contains(output, `stream="stdout"`) {
		t.Errorf("Expected inline stream attr, got: %s", output)
	}
}

// TestTextBlockHandler_MultilineString verifies multi-line string attrs are
// boxed with the sub-block framing while the message stays on the header.
func TestTextBlockHandler_MultilineString(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler).With("component", "builder")

	configContent := "[main]\ngpgcheck=1\ninstallonly_limit=3"
	logger.Info("Writing configfile", "config", configContent)

	output := buf.String()
	t.Logf("Output:\n%s", output)

	// Check header
	if !strings.Contains(output, "INFO") {
		t.Errorf("Expected INFO")
	}
	if !strings.Contains(output, "[builder]") {
		t.Errorf("Expected [builder]")
	}
	if !strings.Contains(output, "Writing configfile") {
		t.Errorf("Expected message on header line")
	}

	// Check multiline string sub-block
	if !strings.Contains(output, "| config=") {
		t.Errorf("Expected config= in textblock")
	}
	if !strings.Contains(output, "| ┌─────") {
		t.Errorf("Expected sub-block header")
	}
	if !strings.Contains(output, "| │ [main]") {
		t.Errorf("Expected [main] in sub-block")
	}
	if !strings.Contains(output, "| │ gpgcheck=1") {
		t.Errorf("Expected gpgcheck=1 in sub-block")
	}
	if !strings.Contains(output, "| │ installonly_limit=3") {
		t.Errorf("Expected installonly_limit=3 in sub-block")
	}
	if !strings.Contains(output, "| └─────") {
		t.Errorf("Expected sub-block footer")
	}

	// Make sure we're NOT seeing the escaped \n in the output
	if strings.Contains(output, `\n`) {
		t.Errorf("Should not contain escaped newlines, output should render actual lines")
	}
}

// TestTextBlockHandler_MixedAttributes verifies scalar attrs go inline while
// the multi-line script attr is boxed.
func TestTextBlockHandler_MixedAttributes(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler).With("component", "container")

	script := "#!/bin/bash\nset -e\necho 'Done'"
	logger.Info("Running script", "path", "/tmp/script.sh", "script", script, "timeout", 30)

	output := buf.String()
	t.Logf("Output:\n%s", output)

	// Scalar attributes stay on the header line
	if !strings.Contains(output, `path="/tmp/script.sh"`) {
		t.Errorf("Expected inline path attribute")
	}
	if !strings.Contains(output, "timeout=30") {
		t.Errorf("Expected inline timeout attribute")
	}

	// Check multiline script
	if !strings.Contains(output, "| script=") {
		t.Errorf("Expected script= in textblock")
	}
	if !strings.Contains(output, "| │ #!/bin/bash") {
		t.Errorf("Expected script line 1")
	}
	if !strings.Contains(output, "| │ set -e") {
		t.Errorf("Expected script line 2")
	}
	if !strings.Contains(output, "| │ echo 'Done'") {
		t.Errorf("Expected script line 3")
	}
}

// TestTextBlockHandler_WithGroup_PrefixesRecordAttrs verifies that
// WithGroup("g") makes subsequent record-level attrs render as g.<key>.
func TestTextBlockHandler_WithGroup_PrefixesRecordAttrs(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler).WithGroup("svc")

	logger.Info("hello", "name", "alice", "count", 3)

	output := buf.String()
	if !strings.Contains(output, `svc.name="alice"`) {
		t.Errorf("expected 'svc.name=\"alice\"' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "svc.count=3") {
		t.Errorf("expected 'svc.count=3' in output, got:\n%s", output)
	}
}

// TestTextBlockHandler_WithGroup_PrefixesHandlerAttrs verifies that attrs
// added AFTER WithGroup are baked-in with the group prefix, and that the
// pre-group component attr is still hoisted into the [name] prefix.
func TestTextBlockHandler_WithGroup_PrefixesHandlerAttrs(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	// component is added FIRST (no group), then WithGroup, then svc-level
	// attrs. The component attribute must render as the [test] prefix;
	// svc-level attrs must be prefixed with the group name.
	logger := slog.New(handler).
		With("component", "test").
		WithGroup("svc").
		With("region", "us-east-1")

	logger.Info("hello")

	output := buf.String()
	if !strings.Contains(output, "[test]") {
		t.Errorf("pre-group component attr must render as [test] prefix, got:\n%s", output)
	}
	if !strings.Contains(output, `svc.region="us-east-1"`) {
		t.Errorf("post-group attr must be prefixed, got:\n%s", output)
	}
}

// TestTextBlockHandler_NestedGroups verifies that chained WithGroup calls
// produce dotted nested prefixes (a.b.key).
func TestTextBlockHandler_NestedGroups(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler).WithGroup("a").WithGroup("b")

	logger.Info("hello", "k", 1)

	output := buf.String()
	if !strings.Contains(output, "a.b.k=1") {
		t.Errorf("expected nested prefix 'a.b.k=1', got:\n%s", output)
	}
}

// TestTextBlockHandler_EmptyGroupNoop verifies WithGroup("") returns the
// receiver unchanged (per slog interface contract).
func TestTextBlockHandler_EmptyGroupNoop(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(handler).WithGroup("")

	logger.Info("hello", "k", 1)

	output := buf.String()
	if !strings.Contains(output, "k=1") {
		t.Errorf("empty WithGroup should not prefix, got:\n%s", output)
	}
	if strings.Contains(output, ".k=") {
		t.Errorf("empty WithGroup should not produce a prefix, got:\n%s", output)
	}
}
