// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestReplacePlaceholders_IfBlock tests that {{- if }} conditionals with list
// items are replaced with valid YAML placeholder list items. This is critical
// for LoadConfigRaw which needs to parse unrendered templates for tag hashing.
func TestReplacePlaceholders_IfBlock(t *testing.T) {
	input := `packages:
- pkg1
{{- if .include_falcon_sensor }}
- falcon-sensor
{{- end }}
- pkg3`

	result := replaceTemplatePlaceholders([]byte(input))
	resultStr := string(result)

	// Should replace the entire if block with a placeholder list item
	if !strings.Contains(resultStr, "- __placeholder__") {
		t.Errorf("expected placeholder list item, got:\n%s", resultStr)
	}

	// Should produce valid YAML
	var obj interface{}
	if err := yaml.Unmarshal(result, &obj); err != nil {
		t.Errorf("result is not valid YAML: %v\nGot:\n%s", err, resultStr)
	}

	// Should not contain template syntax
	if strings.Contains(resultStr, "{{") || strings.Contains(resultStr, "}}") {
		t.Errorf("result still contains template syntax:\n%s", resultStr)
	}
}

// TestReplacePlaceholders_RangeBlock tests that {{- range }} loops are handled
func TestReplacePlaceholders_RangeBlock(t *testing.T) {
	input := `items:
{{- range .things }}
- {{ . }}
{{- end }}`

	result := replaceTemplatePlaceholders([]byte(input))
	resultStr := string(result)

	// Should replace the entire range block with a placeholder list item
	if !strings.Contains(resultStr, "- __placeholder__") {
		t.Errorf("expected placeholder list item, got:\n%s", resultStr)
	}

	// Should produce valid YAML
	var obj interface{}
	if err := yaml.Unmarshal(result, &obj); err != nil {
		t.Errorf("result is not valid YAML: %v\nGot:\n%s", err, resultStr)
	}
}

// TestReplacePlaceholders_StructuredList tests that if blocks containing
// structured list items (- key: value) are replaced correctly
func TestReplacePlaceholders_StructuredList(t *testing.T) {
	input := `files:
- path: /usr/bin/alt_sss
  src: files/alt_sss
{{- if .include_falcon }}
- path: /usr/lib/systemd/system/falcon.service
  src: files/falcon.service
{{- end }}
- path: /etc/config
  src: files/config`

	result := replaceTemplatePlaceholders([]byte(input))
	resultStr := string(result)

	// Should produce valid YAML
	var obj interface{}
	if err := yaml.Unmarshal(result, &obj); err != nil {
		t.Errorf("result is not valid YAML: %v\nGot:\n%s", err, resultStr)
	}

	// Should preserve structure of non-template items
	if !strings.Contains(resultStr, "path: /usr/bin/alt_sss") {
		t.Errorf("expected first file entry preserved, got:\n%s", resultStr)
	}
	if !strings.Contains(resultStr, "path: /etc/config") {
		t.Errorf("expected last file entry preserved, got:\n%s", resultStr)
	}
}

// TestReplacePlaceholders_InlineTemplates tests that inline {{ .var }} expressions
// are replaced with placeholders
func TestReplacePlaceholders_InlineTemplates(t *testing.T) {
	input := `meta:
  name: test-{{ .arch }}
  from: docker://base:{{ .tag }}`

	result := replaceTemplatePlaceholders([]byte(input))
	resultStr := string(result)

	// Should replace inline templates with placeholders
	if !strings.Contains(resultStr, "test-__placeholder__") {
		t.Errorf("expected placeholder in name, got:\n%s", resultStr)
	}
	if !strings.Contains(resultStr, "base:__placeholder__") {
		t.Errorf("expected placeholder in from, got:\n%s", resultStr)
	}

	// Should produce valid YAML
	var obj interface{}
	if err := yaml.Unmarshal(result, &obj); err != nil {
		t.Errorf("result is not valid YAML: %v\nGot:\n%s", err, resultStr)
	}
}

// TestReplacePlaceholders_ComplexRealWorld tests a complex real-world config
// with multiple if blocks in different contexts
func TestReplacePlaceholders_ComplexRealWorld(t *testing.T) {
	input := `meta:
  name: sles-fe-gpu-{{ .arch }}
  from: docker://registry.example.com/base:{{ .parent_tag }}
  tags:
    - "{{ .tag }}"

layer:
  manager:
    name: zypper
    arch: "{{ .arch }}"

  actions:
    install:
      packages:
      - pam_radius
      - krb5
      {{- if .include_falcon_sensor }}
      - falcon-sensor
      {{- end }}
    commands:
    {{- if .include_falcon_sensor }}
    - run: systemctl enable falcon-config
    {{- end }}
    - run: systemctl enable nftables

  files:
  - path: /usr/bin/alt_sss
    src: image-configs/files/alt_sss
  {{- if .include_falcon_sensor }}
  - path: /usr/lib/systemd/system/falcon-config.service
    src: image-configs/files/systemd/falcon-config.service
  {{- end }}
  - path: /etc/systemd/system/gitlab-runner.service
    src: image-configs/files/systemd/gitlab-runner.service`

	result := replaceTemplatePlaceholders([]byte(input))
	resultStr := string(result)

	// Should produce valid YAML
	var obj interface{}
	if err := yaml.Unmarshal(result, &obj); err != nil {
		t.Errorf("result is not valid YAML: %v\nGot:\n%s", err, resultStr)
	}

	// Should not contain template syntax
	if strings.Contains(resultStr, "{{") || strings.Contains(resultStr, "}}") {
		t.Errorf("result still contains template syntax:\n%s", resultStr)
	}

	// Verify key structures are preserved
	if !strings.Contains(resultStr, "name: sles-fe-gpu-__placeholder__") {
		t.Errorf("expected name with placeholder, got:\n%s", resultStr)
	}
	if !strings.Contains(resultStr, "- pam_radius") {
		t.Errorf("expected base package preserved, got:\n%s", resultStr)
	}
	if !strings.Contains(resultStr, "- run: systemctl enable nftables") {
		t.Errorf("expected base command preserved, got:\n%s", resultStr)
	}
}

// TestReplacePlaceholders_WithBlock tests that {{- with }} blocks are handled
// (though they're rarely used in practice)
func TestReplacePlaceholders_WithBlock(t *testing.T) {
	input := `config:
  base: value
{{- with .settings }}
  custom: {{ .value }}
{{- end }}`

	result := replaceTemplatePlaceholders([]byte(input))
	resultStr := string(result)

	// Should not contain template syntax
	if strings.Contains(resultStr, "{{") || strings.Contains(resultStr, "}}") {
		t.Errorf("result still contains template syntax:\n%s", resultStr)
	}

	// Note: with blocks that don't wrap lists may not always produce valid YAML,
	// but that's acceptable since they're not commonly used in image configs.
	// The important thing is that all template syntax is removed.
}

// TestReplacePlaceholders_GenericKeywords tests that the generic regex approach
// handles current and potential future Go template keywords
func TestReplacePlaceholders_GenericKeywords(t *testing.T) {
	testCases := []struct {
		name  string
		input string
	}{
		{
			name: "if block",
			input: `items:
{{- if .test }}
- item
{{- end }}`,
		},
		{
			name: "range block",
			input: `items:
{{- range .list }}
- {{ . }}
{{- end }}`,
		},
		{
			name: "with block",
			input: `items:
{{- with .data }}
- item
{{- end }}`,
		},
		{
			name: "define block",
			input: `{{- define "tmpl" }}
content
{{- end }}
main: value`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := replaceTemplatePlaceholders([]byte(tc.input))
			resultStr := string(result)

			// Should not contain template syntax
			if strings.Contains(resultStr, "{{") || strings.Contains(resultStr, "}}") {
				t.Errorf("result still contains template syntax:\n%s", resultStr)
			}
		})
	}
}

// TestLoadConfigRaw_WithIfBlocks is an end-to-end test verifying that
// LoadConfigRaw can successfully parse a config with if blocks
func TestLoadConfigRaw_WithIfBlocks(t *testing.T) {
	input := `meta:
  name: test-{{ .arch }}
  from: docker://base:{{ .tag }}
  tags: ["{{ .tag }}"]

layer:
  manager:
    name: zypper
  actions:
    install:
      packages:
      - base-pkg
      {{- if .include_extra }}
      - extra-pkg
      {{- end }}

publish:
  - type: local`

	// Write to temp file
	tmpFile := t.TempDir() + "/config.yaml"
	if err := writeFile(tmpFile, []byte(input)); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	// LoadConfigRaw should parse successfully
	cfg, err := LoadConfigRaw(tmpFile)
	if err != nil {
		t.Fatalf("LoadConfigRaw failed with if blocks: %v", err)
	}

	// Verify basic structure was parsed
	if cfg.Meta.Name != "test-__placeholder__" {
		t.Errorf("expected name with placeholder, got %q", cfg.Meta.Name)
	}

	// Verify packages list contains placeholders
	if len(cfg.Layer.Actions.Install.Packages) < 2 {
		t.Errorf("expected at least 2 packages (base + placeholder), got %d", len(cfg.Layer.Actions.Install.Packages))
	}
	if cfg.Layer.Actions.Install.Packages[0] != "base-pkg" {
		t.Errorf("expected first package to be 'base-pkg', got %q", cfg.Layer.Actions.Install.Packages[0])
	}
	if cfg.Layer.Actions.Install.Packages[1] != TemplatePlaceholder {
		t.Errorf("expected second package to be placeholder, got %q", cfg.Layer.Actions.Install.Packages[1])
	}
}

func writeFile(path string, data []byte) error {
	// Use os.WriteFile for the actual write
	return os.WriteFile(path, data, 0o644)
}
