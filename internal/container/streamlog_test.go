package container

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestTextBlockHandler_SimpleMessage(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler).With("component", "builder")

	logger.Info("Simple test message")

	output := buf.String()
	if !strings.Contains(output, "level=INFO") {
		t.Errorf("Expected level=INFO in output, got: %s", output)
	}
	if !strings.Contains(output, `component="builder"`) {
		t.Errorf("Expected component=\"builder\" in output, got: %s", output)
	}
	if !strings.Contains(output, "┌──── output ────") {
		t.Errorf("Expected textblock header in output, got: %s", output)
	}
	if !strings.Contains(output, "| Simple test message") {
		t.Errorf("Expected message in textblock, got: %s", output)
	}
	if !strings.Contains(output, "└────────────────") {
		t.Errorf("Expected textblock footer in output, got: %s", output)
	}
}

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

	// Check header line
	if !strings.Contains(output, "level=INFO") {
		t.Errorf("Expected level=INFO in output")
	}
	if !strings.Contains(output, `component="builder"`) {
		t.Errorf("Expected component=\"builder\" in output")
	}

	// Check textblock
	if !strings.Contains(output, "┌──── output ────") {
		t.Errorf("Expected textblock header")
	}
	if !strings.Contains(output, "| Done installing commands:") {
		t.Errorf("Expected message in textblock")
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

func TestTextBlockHandler_MultipleAttributes(t *testing.T) {
	var buf bytes.Buffer
	handler := NewTextBlockHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler).With("component", "builder")

	logger.Info("Test with attributes", "name", "myimage", "count", 42)

	output := buf.String()
	t.Logf("Output:\n%s", output)

	if !strings.Contains(output, "level=INFO") {
		t.Errorf("Expected level=INFO")
	}
	if !strings.Contains(output, "| Test with attributes") {
		t.Errorf("Expected message in textblock")
	}
	if !strings.Contains(output, `| name="myimage"`) {
		t.Errorf("Expected name attribute in textblock")
	}
	if !strings.Contains(output, "| count=42") {
		t.Errorf("Expected count attribute in textblock")
	}
}

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
	if !strings.Contains(output, "level=INFO") {
		t.Errorf("Expected level=INFO")
	}
	if !strings.Contains(output, `component="builder"`) {
		t.Errorf("Expected component=\"builder\"")
	}

	// Check textblock
	if !strings.Contains(output, "| Writing configfile") {
		t.Errorf("Expected message in textblock")
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

	// Check simple attributes
	if !strings.Contains(output, `| path="/tmp/script.sh"`) {
		t.Errorf("Expected path attribute")
	}
	if !strings.Contains(output, "| timeout=30") {
		t.Errorf("Expected timeout attribute")
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
