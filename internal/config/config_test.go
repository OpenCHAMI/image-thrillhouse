// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadConfigWithVars tests loading a valid configuration file
func TestLoadConfigWithVars(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test-config.yaml")

	configContent := `
meta:
  name: test-image
  tags: ["1.0"]
  from: scratch

layer:
  manager:
    name: dnf
  repos:
    - path: /etc/yum.repos.d/test.repo
      content: |
        [test]
        baseurl=http://example.com
  actions:
    install:
      packages:
        - kernel

publish:
  - type: local
`

	err := os.WriteFile(configPath, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	// Test loading the config
	cfg, err := LoadConfigWithVars(configPath, nil)
	if err != nil {
		t.Fatalf("LoadConfigWithVars failed: %v", err)
	}

	// Verify config was parsed correctly
	if cfg.Meta.Name != "test-image" {
		t.Errorf("Expected name 'test-image', got '%s'", cfg.Meta.Name)
	}

	if len(cfg.Meta.Tags) != 1 || cfg.Meta.Tags[0] != "1.0" {
		t.Errorf("Expected tags ['1.0'], got '%v'", cfg.Meta.Tags)
	}

	if cfg.Layer.Manager.Name != "dnf" {
		t.Errorf("Expected manager 'dnf', got '%s'", cfg.Layer.Manager.Name)
	}

	if len(cfg.Layer.Actions.Install.Packages) != 1 {
		t.Errorf("Expected 1 package, got %d", len(cfg.Layer.Actions.Install.Packages))
	}
}

// TestLoadConfigFileNotFound tests error handling for missing config
func TestLoadConfigFileNotFound(t *testing.T) {
	_, err := LoadConfigWithVars("/nonexistent/config.yaml", nil)
	if err == nil {
		t.Error("Expected error for nonexistent file, got nil")
	}
}

// TestLoadConfigInvalidYAML tests error handling for invalid YAML
func TestLoadConfigInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")

	invalidYAML := `
meta:
  name: test
  invalid yaml syntax here: [[[
`

	err := os.WriteFile(configPath, []byte(invalidYAML), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	_, err = LoadConfigWithVars(configPath, nil)
	if err == nil {
		t.Error("Expected error for invalid YAML, got nil")
	}
}

// TestRenderConfig_SubstitutesVars exercises the happy path of the
// templating engine: every {{ .var }} reference must be replaced and no
// markers remain in the output.
func TestRenderConfig_SubstitutesVars(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl.yaml")
	if err := os.WriteFile(tmpl, []byte(`meta:
  name: {{ .name }}
  tags: ["{{ .version }}"]
  from: scratch
layer:
  manager:
    name: {{ .mgr }}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := RenderConfig(tmpl, map[string]interface{}{
		"name":    "demo",
		"version": "1.2.3",
		"mgr":     "dnf",
	})
	if err != nil {
		t.Fatalf("RenderConfig: %v", err)
	}

	for _, want := range []string{"name: demo", `tags: ["1.2.3"]`, "name: dnf"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in rendered output, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "{{") || strings.Contains(out, "}}") {
		t.Errorf("rendered output still contains template markers:\n%s", out)
	}
}

// TestRenderConfig_MissingKeyZero verifies that missing keys are treated as
// zero values (empty string, nil, etc.) to support optional variables and
// conditional rendering with {{ if }} or {{ range }} ... {{ else }}.
func TestRenderConfig_MissingKeyZero(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl.yaml")
	if err := os.WriteFile(tmpl, []byte(`meta:
  name: {{ .name }}
  optional: {{ .missing }}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := RenderConfig(tmpl, map[string]interface{}{
		"name": "test",
		// .missing is not provided
	})
	if err != nil {
		t.Fatalf("RenderConfig should not error on missing keys: %v", err)
	}
	
	// Missing key should render as empty string (zero value)
	if !strings.Contains(out, "name: test") {
		t.Errorf("expected 'name: test' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "optional: ") {
		t.Errorf("expected 'optional: ' (empty) in output, got:\n%s", out)
	}
	// Should NOT contain the template marker
	if strings.Contains(out, "{{ .missing }}") {
		t.Errorf("template marker should be replaced, got:\n%s", out)
	}
}

// TestRenderConfig_FileNotFound: a non-existent template path must error,
// not silently render to empty.
func TestRenderConfig_FileNotFound(t *testing.T) {
	_, err := RenderConfig("/does/not/exist.yaml", nil)
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

// TestLoadConfigRaw_AcceptsRawTemplate verifies that LoadConfigRaw can
// parse a template file (with {{ }} markers) without expanding it. This is
// the contract internal/tag relies on for deterministic hashing of the
// unrendered config.
func TestLoadConfigRaw_AcceptsRawTemplate(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl.yaml")
	body := `meta:
  name: {{ .name }}
  tags: ["{{ .version }}"]
  from: scratch
layer:
  manager:
    name: dnf
`
	if err := os.WriteFile(tmpl, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfigRaw(tmpl)
	if err != nil {
		t.Fatalf("LoadConfigRaw: %v", err)
	}
	// Placeholder substitution should leave us with a parseable Config
	// whose Manager.Name is the static value from the template (the
	// template markers in meta.name don't break YAML parsing — they're
	// replaced by the placeholder string).
	if cfg.Layer.Manager.Name != "dnf" {
		t.Errorf("expected manager dnf, got %q", cfg.Layer.Manager.Name)
	}
}

// TestLoadConfigRaw_RangeBlocks verifies that LoadConfigRaw can parse
// template files containing {{ range }} blocks without breaking YAML structure.
// This is critical for tag computation which must hash the unrendered template.
func TestLoadConfigRaw_RangeBlocks(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl.yaml")
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
        - {{ .kernel_package }}
        # Shared packages
        {{- range .base_shared_packages }}
        - {{ . }}
        {{- end }}
        # x86_64-only packages
        {{- range .base_x86_64_only_packages }}
        - {{ . }}
        {{- end }}
        - static-package
`
	if err := os.WriteFile(tmpl, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfigRaw(tmpl)
	if err != nil {
		t.Fatalf("LoadConfigRaw with range blocks failed: %v", err)
	}

	// The range blocks should be replaced with placeholder list items,
	// leaving valid YAML structure that can be parsed
	if cfg.Layer.Manager.Name != "dnf" {
		t.Errorf("expected manager dnf, got %q", cfg.Layer.Manager.Name)
	}

	// The packages list should contain placeholders for the template vars
	// and range blocks, plus the static package
	if len(cfg.Layer.Actions.Install.Packages) < 1 {
		t.Errorf("expected at least 1 package after placeholder replacement, got %d", 
			len(cfg.Layer.Actions.Install.Packages))
	}
}

// TestLoadVars_CLIWinsOverFile verifies the documented precedence: --var
// key=value on the command line overrides the same key in --var-file.
// This is the property templates rely on for per-build pin-tweaks.
func TestLoadVars_CLIWinsOverFile(t *testing.T) {
	dir := t.TempDir()
	vf := filepath.Join(dir, "vars.yaml")
	if err := os.WriteFile(vf, []byte("arch: aarch64\nreleasever: '9'\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	merged, err := LoadVars([]string{vf}, []string{"arch=x86_64"})
	if err != nil {
		t.Fatalf("LoadVars: %v", err)
	}

	if got := merged["arch"]; got != "x86_64" {
		t.Errorf("CLI --var arch should win: got %v, want x86_64", got)
	}
	if got := merged["releasever"]; got != "9" {
		t.Errorf("file-only key should pass through: got %v, want 9", got)
	}
}

// TestLoadVars_DeepMerge: a nested map in a var file must merge key-wise
// rather than the second file's map clobbering the first's. Without
// deep-merge, layering two var files (e.g. arch + per-env overrides)
// silently loses keys.
func TestLoadVars_DeepMerge(t *testing.T) {
	dir := t.TempDir()
	vf1 := filepath.Join(dir, "base.yaml")
	vf2 := filepath.Join(dir, "override.yaml")
	if err := os.WriteFile(vf1, []byte("repo:\n  base: rocky\n  arch: x86_64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vf2, []byte("repo:\n  arch: aarch64\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	merged, err := LoadVars([]string{vf1, vf2}, nil)
	if err != nil {
		t.Fatalf("LoadVars: %v", err)
	}

	repo, ok := merged["repo"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected repo to be a map, got %T", merged["repo"])
	}
	if repo["base"] != "rocky" {
		t.Errorf("repo.base from first file should survive: got %v, want rocky", repo["base"])
	}
	if repo["arch"] != "aarch64" {
		t.Errorf("repo.arch from second file should win: got %v, want aarch64", repo["arch"])
	}
}

// TestLoadVars_CLIDottedKey: dotted CLI keys (--var repo.arch=...) should
// create a nested map, matching the var-file layout. Without this, users
// can't override a nested key from the command line.
func TestLoadVars_CLIDottedKey(t *testing.T) {
	merged, err := LoadVars(nil, []string{"repo.arch=x86_64", "repo.base=rocky"})
	if err != nil {
		t.Fatalf("LoadVars: %v", err)
	}
	repo, ok := merged["repo"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected repo to be a map, got %T", merged["repo"])
	}
	if repo["arch"] != "x86_64" {
		t.Errorf("repo.arch: got %v, want x86_64", repo["arch"])
	}
	if repo["base"] != "rocky" {
		t.Errorf("repo.base: got %v, want rocky", repo["base"])
	}
}

// TestLoadVars_EmptyVarFileSkipped: passing an empty string in the
// varFiles slice (which happens when --var-file wasn't given but
// the global slice still carries one "" entry) must be a no-op rather
// than an open("") error.
func TestLoadVars_EmptyVarFileSkipped(t *testing.T) {
	merged, err := LoadVars([]string{""}, []string{"k=v"})
	if err != nil {
		t.Fatalf("LoadVars: %v", err)
	}
	if merged["k"] != "v" {
		t.Errorf("CLI var lost: got %v, want v", merged["k"])
	}
}

// TestLoadVars_BadCLIVar: a CLI var without "=" must fail loudly. Silent
// acceptance would let typos like `--var arch x86_64` become no-ops.
func TestLoadVars_BadCLIVar(t *testing.T) {
	_, err := LoadVars(nil, []string{"no-equals-sign"})
	if err == nil {
		t.Error("expected error from malformed --var, got nil")
	}
}

// TestValidateMeta tests Meta validation
func TestValidateMeta(t *testing.T) {
	tests := []struct {
		name    string
		meta    Meta
		wantErr bool
	}{
		{
			name: "valid meta",
			meta: Meta{
				Name: "test-image",
				Tags: []string{"1.0"},
				From: "scratch",
			},
			wantErr: false,
		},
		{
			name: "missing name",
			meta: Meta{
				Tags: []string{"1.0"},
				From: "scratch",
			},
			wantErr: true,
		},
		{
			name: "missing tags",
			meta: Meta{
				Name: "test-image",
				From: "scratch",
			},
			wantErr: true,
		},
		{
			name: "valid without from",
			meta: Meta{
				Name: "test-image",
				Tags: []string{"1.0"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.meta.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Meta.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateLayer tests Layer validation
func TestValidateLayer(t *testing.T) {
	tests := []struct {
		name    string
		layer   Layer
		wantErr bool
	}{
		{
			name: "valid dnf manager",
			layer: Layer{
				Manager: Manager{Name: "dnf"},
			},
			wantErr: false,
		},
		{
			name: "valid zypper manager",
			layer: Layer{
				Manager: Manager{Name: "zypper"},
			},
			wantErr: false,
		},
		{
			name: "invalid manager",
			layer: Layer{
				Manager: Manager{Name: "invalid"},
			},
			wantErr: true,
		},
		{
			name: "missing manager",
			layer: Layer{
				Manager: Manager{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.layer.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Layer.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateFile tests File validation
func TestValidateFile(t *testing.T) {
	tests := []struct {
		name    string
		file    File
		wantErr bool
	}{
		{
			name: "valid with content",
			file: File{
				Path:    "/etc/test.conf",
				Content: "test content",
			},
			wantErr: false,
		},
		{
			name: "valid with src",
			file: File{
				Path: "/etc/test.conf",
				Src:  "/local/file",
			},
			wantErr: false,
		},
		{
			name: "valid with url",
			file: File{
				Path: "/etc/test.conf",
				URL:  "https://example.com/file",
			},
			wantErr: false,
		},
		{
			name: "missing path",
			file: File{
				Content: "test",
			},
			wantErr: true,
		},
		{
			name: "no source specified",
			file: File{
				Path: "/etc/test.conf",
			},
			wantErr: true,
		},
		{
			name: "multiple sources",
			file: File{
				Path:    "/etc/test.conf",
				Content: "test",
				Src:     "/local/file",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.file.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("File.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestValidateDirectory exercises Directory.Validate's required-fields and
// mutual-exclusion contracts.
func TestValidateDirectory(t *testing.T) {
	tru := true
	tests := []struct {
		name    string
		dir     Directory
		wantErr bool
	}{
		{
			name:    "valid minimal",
			dir:     Directory{Path: "/opt/app", Src: "./build"},
			wantErr: false,
		},
		{
			name: "valid with all options",
			dir: Directory{
				Path:         "/opt/app",
				Src:          "./build",
				Mode:         "0755",
				Owner:        "1000:1000",
				Excludes:     []string{"*.tmp"},
				ContentsOnly: &tru,
			},
			wantErr: false,
		},
		{
			name:    "missing path",
			dir:     Directory{Src: "./build"},
			wantErr: true,
		},
		{
			name:    "missing src",
			dir:     Directory{Path: "/opt/app"},
			wantErr: true,
		},
		{
			name: "owner + preserve_ownership conflict",
			dir: Directory{
				Path:              "/opt/app",
				Src:               "./build",
				Owner:             "1000:1000",
				PreserveOwnership: true,
			},
			wantErr: true,
		},
		{
			name: "preserve_ownership alone is fine",
			dir: Directory{
				Path:              "/opt/app",
				Src:               "./build",
				PreserveOwnership: true,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.dir.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Directory.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestLoadConfigWithDirectories ensures the new layer.directories block round-trips
// through LoadConfigWithVars, including the contents_only pointer default
// (unset → nil, the builder applies the true default).
func TestLoadConfigWithDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	body := `meta:
  name: dir-test
  tags: ["1"]
  from: scratch
layer:
  manager:
    name: dnf
  directories:
    - path: /opt/app
      src: ./build/app
      mode: "0755"
      owner: "1000:1000"
      excludes:
        - "*.tmp"
        - "cache/"
    - path: /opt/other
      src: ./other
      preserve_ownership: true
      contents_only: false
`
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfigWithVars(configPath, nil)
	if err != nil {
		t.Fatalf("LoadConfigWithVars: %v", err)
	}

	if got := len(cfg.Layer.Directories); got != 2 {
		t.Fatalf("expected 2 directories, got %d", got)
	}

	first := cfg.Layer.Directories[0]
	if first.Path != "/opt/app" || first.Src != "./build/app" {
		t.Errorf("first directory path/src wrong: %+v", first)
	}
	if first.Mode != "0755" || first.Owner != "1000:1000" {
		t.Errorf("first directory mode/owner wrong: %+v", first)
	}
	if len(first.Excludes) != 2 || first.Excludes[0] != "*.tmp" {
		t.Errorf("first directory excludes wrong: %+v", first.Excludes)
	}
	// Pointer left nil when key absent — builder applies the true default.
	if first.ContentsOnly != nil {
		t.Errorf("expected ContentsOnly nil when unset, got %v", *first.ContentsOnly)
	}

	second := cfg.Layer.Directories[1]
	if !second.PreserveOwnership {
		t.Errorf("preserve_ownership not parsed: %+v", second)
	}
	if second.ContentsOnly == nil || *second.ContentsOnly {
		t.Errorf("expected ContentsOnly to be a pointer to false, got %v", second.ContentsOnly)
	}
}

// TestValidateModule tests Module validation
func TestValidateModule(t *testing.T) {
	tests := []struct {
		name    string
		module  Module
		wantErr bool
	}{
		{
			name: "valid install",
			module: Module{
				Name:   "nodejs",
				Stream: "18",
				Action: "install",
			},
			wantErr: false,
		},
		{
			name: "valid enable",
			module: Module{
				Name:   "nodejs",
				Stream: "18",
				Action: "enable",
			},
			wantErr: false,
		},
		{
			name: "missing name",
			module: Module{
				Stream: "18",
				Action: "install",
			},
			wantErr: true,
		},
		{
			name: "missing action",
			module: Module{
				Name:   "nodejs",
				Stream: "18",
			},
			wantErr: true,
		},
		{
			name: "invalid action",
			module: Module{
				Name:   "nodejs",
				Stream: "18",
				Action: "invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.module.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Module.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestCommandType tests Command.Type() method
func TestCommandType(t *testing.T) {
	tests := []struct {
		name     string
		command  Command
		expected CommandType
	}{
		{
			name: "run command",
			command: Command{
				Run: "echo test",
			},
			expected: CommandRun,
		},
		{
			name: "script command",
			command: Command{
				Script: "#!/bin/bash\necho test",
			},
			expected: CommandScript,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.command.Type(); got != tt.expected {
				t.Errorf("Command.Type() = %v, want %v", got, tt.expected)
			}
		})
	}
}
