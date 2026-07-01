package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderConfig_RangeLoop tests that Go template range loops work correctly
// for iterating over slices in the config. This is critical for dynamic package
// lists and architecture-specific configurations.
func TestRenderConfig_RangeLoop(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl.yaml")
	body := `meta:
  name: sles-base-{{ .arch }}
  tags: ["{{ .tag }}"]
  from: scratch

layer:
  manager:
    name: zypper
    arch: "{{ .arch }}"

  actions:
    install:
      packages:
        # Architecture-specific kernel
        - {{ .kernel_package }}
        # Shared packages (both architectures)
{{- range .base_shared_packages }}
        - {{ . }}
{{- end }}
        # x86_64-only packages
{{- range .base_x86_64_only_packages }}
        - {{ . }}
{{- end }}
`
	if err := os.WriteFile(tmpl, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := map[string]interface{}{
		"arch":                      "x86_64",
		"tag":                       "test123",
		"kernel_package":            "kernel-default",
		"base_shared_packages":      []interface{}{"systemd", "vim", "openssh"},
		"base_x86_64_only_packages": []interface{}{"grub2-x86_64-efi", "microcode_ctl"},
	}

	out, err := RenderConfig(tmpl, vars)
	if err != nil {
		t.Fatalf("RenderConfig with range loops failed: %v", err)
	}

	// Verify basic substitutions
	if !strings.Contains(out, "name: sles-base-x86_64") {
		t.Errorf("expected name substitution in output, got:\n%s", out)
	}

	if !strings.Contains(out, `tags: ["test123"]`) {
		t.Errorf("expected tag substitution in output, got:\n%s", out)
	}

	// Verify range loop expansions
	expectedPackages := []string{
		"- kernel-default",
		"- systemd",
		"- vim",
		"- openssh",
		"- grub2-x86_64-efi",
		"- microcode_ctl",
	}

	for _, pkg := range expectedPackages {
		if !strings.Contains(out, pkg) {
			t.Errorf("expected package %q in output, got:\n%s", pkg, out)
		}
	}

	// Verify no template markers remain
	if strings.Contains(out, "{{") || strings.Contains(out, "}}") {
		t.Errorf("rendered output still contains template markers:\n%s", out)
	}
}

// TestRenderConfig_EmptyRange tests that empty slices in range loops don't break
// the rendering or produce invalid YAML
func TestRenderConfig_EmptyRange(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl.yaml")
	body := `meta:
  name: test
  tags: ["1.0"]
  from: scratch

layer:
  manager:
    name: dnf
  actions:
    install:
      packages:
        - base-package
{{- range .extra_packages }}
        - {{ . }}
{{- end }}
`
	if err := os.WriteFile(tmpl, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := map[string]interface{}{
		"extra_packages": []interface{}{}, // empty slice
	}

	out, err := RenderConfig(tmpl, vars)
	if err != nil {
		t.Fatalf("RenderConfig with empty range failed: %v", err)
	}

	// Should still have the base package
	if !strings.Contains(out, "- base-package") {
		t.Errorf("expected base package in output, got:\n%s", out)
	}

	// Should not break YAML structure
	if strings.Contains(out, "{{") || strings.Contains(out, "}}") {
		t.Errorf("rendered output still contains template markers:\n%s", out)
	}
}

// TestRenderConfig_NestedRangeAndConditions tests more complex template logic
// combining range loops with conditionals
func TestRenderConfig_NestedTemplateLogic(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl.yaml")
	body := `meta:
  name: test-{{ .arch }}
  tags: ["1.0"]
  from: scratch

layer:
  manager:
    name: dnf
  repos:
{{- range .repos }}
    - path: {{ .path }}
      content: |-
        [{{ .name }}]
        baseurl={{ .url }}/{{ $.arch }}
        enabled=1
{{- end }}
`
	if err := os.WriteFile(tmpl, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	vars := map[string]interface{}{
		"arch": "x86_64",
		"repos": []interface{}{
			map[string]interface{}{
				"name": "baseos",
				"path": "/etc/yum.repos.d/baseos.repo",
				"url":  "https://example.com/baseos",
			},
			map[string]interface{}{
				"name": "appstream",
				"path": "/etc/yum.repos.d/appstream.repo",
				"url":  "https://example.com/appstream",
			},
		},
	}

	out, err := RenderConfig(tmpl, vars)
	if err != nil {
		t.Fatalf("RenderConfig with nested template logic failed: %v", err)
	}

	// Verify both repos were rendered
	if !strings.Contains(out, "[baseos]") {
		t.Errorf("expected baseos repo in output, got:\n%s", out)
	}
	if !strings.Contains(out, "[appstream]") {
		t.Errorf("expected appstream repo in output, got:\n%s", out)
	}

	// Verify parent scope access with $
	if !strings.Contains(out, "baseurl=https://example.com/baseos/x86_64") {
		t.Errorf("expected arch from parent scope in baseurl, got:\n%s", out)
	}

	// Verify no template markers remain
	if strings.Contains(out, "{{") || strings.Contains(out, "}}") {
		t.Errorf("rendered output still contains template markers:\n%s", out)
	}
}

// TestLoadConfigWithVars_RangeLoop is an end-to-end test that verifies
// a config with range loops can be loaded, rendered, and parsed successfully
func TestLoadConfigWithVars_RangeLoop(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	body := `meta:
  name: test-{{ .arch }}
  tags: ["{{ .tag }}"]
  from: scratch

layer:
  manager:
    name: dnf
  actions:
    install:
      packages:
{{- range .packages }}
        - {{ . }}
{{- end }}

publish:
  - type: local
`
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	vars := map[string]interface{}{
		"arch":     "x86_64",
		"tag":      "v1.0.0",
		"packages": []interface{}{"kernel", "systemd", "openssh"},
	}

	cfg, err := LoadConfigWithVars(configPath, vars)
	if err != nil {
		t.Fatalf("LoadConfigWithVars with range loop failed: %v", err)
	}

	// Verify the config was parsed correctly after template rendering
	if cfg.Meta.Name != "test-x86_64" {
		t.Errorf("expected name 'test-x86_64', got '%s'", cfg.Meta.Name)
	}

	if len(cfg.Meta.Tags) != 1 || cfg.Meta.Tags[0] != "v1.0.0" {
		t.Errorf("expected tags ['v1.0.0'], got '%v'", cfg.Meta.Tags)
	}

	if len(cfg.Layer.Actions.Install.Packages) != 3 {
		t.Errorf("expected 3 packages, got %d", len(cfg.Layer.Actions.Install.Packages))
	}

	expectedPackages := []string{"kernel", "systemd", "openssh"}
	for i, expected := range expectedPackages {
		if cfg.Layer.Actions.Install.Packages[i] != expected {
			t.Errorf("package[%d]: expected %s, got %s", i, expected, cfg.Layer.Actions.Install.Packages[i])
		}
	}
}
