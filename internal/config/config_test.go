// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.podman.io/storage/pkg/fileutils"
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
// TestParseAndValidate_OmittedFromDefaultsToScratch pins the documented
// contract that an absent meta.from means a scratch build. The rest of the
// codebase branches on Meta.From == "scratch", so an empty value here used to
// send a scratch build down the parent-image path.
func TestParseAndValidate_OmittedFromDefaultsToScratch(t *testing.T) {
	rendered := `
meta:
  name: test-image
  tags: ["1.0"]
layer:
  manager:
    name: dnf
`
	cfg, err := ParseAndValidate(rendered, "test.yaml")
	if err != nil {
		t.Fatalf("ParseAndValidate failed: %v", err)
	}
	if cfg.Meta.From != "scratch" {
		t.Errorf("Expected omitted meta.from to default to 'scratch', got %q", cfg.Meta.From)
	}
}

// TestParseAndValidate_ExplicitFromPreserved guards against the normalisation
// above clobbering a real base image.
func TestParseAndValidate_ExplicitFromPreserved(t *testing.T) {
	rendered := `
meta:
  name: test-image
  tags: ["1.0"]
  from: registry.example.com/rocky:9
layer:
  manager:
    name: dnf
`
	cfg, err := ParseAndValidate(rendered, "test.yaml")
	if err != nil {
		t.Fatalf("ParseAndValidate failed: %v", err)
	}
	if cfg.Meta.From != "registry.example.com/rocky:9" {
		t.Errorf("Expected explicit meta.from to be preserved, got %q", cfg.Meta.From)
	}
}

// TestValidatePublish covers the publish-block checks that the `validate`
// subcommand advertises. Before these existed, a typo'd type or an s3 block
// missing its bucket passed validate cleanly and only failed once a build had
// already started.
func TestValidatePublish(t *testing.T) {
	tests := []struct {
		name    string
		publish Publish
		wantErr string
	}{
		{"local needs nothing", Publish{Type: "local"}, ""},
		{"squashfs with path", Publish{Type: "squashfs", Path: "/out"}, ""},
		{"registry with url", Publish{Type: "registry", URL: "reg.io/repo"}, ""},
		{"s3 with url and bucket", Publish{Type: "s3", URL: "https://s3.example", Bucket: "b"}, ""},
		{"missing type", Publish{}, "type is required"},
		{"unknown type", Publish{Type: "s4"}, "not supported"},
		{"squashfs without path", Publish{Type: "squashfs"}, "requires path"},
		{"registry without url", Publish{Type: "registry"}, "requires url"},
		{"s3 without url", Publish{Type: "s3", Bucket: "b"}, "requires url"},
		{"s3 without bucket", Publish{Type: "s3", URL: "https://s3.example"}, "requires bucket"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.publish.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("Expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Expected error containing %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

// TestParseAndValidate_RejectsBadPublisher confirms publish validation is
// reachable from the top-level config path the validate subcommand uses, and
// that the error names which block is at fault.
func TestParseAndValidate_RejectsBadPublisher(t *testing.T) {
	rendered := `
meta:
  name: test-image
  tags: ["1.0"]
  from: scratch
layer:
  manager:
    name: dnf
publish:
  - type: local
  - type: squashfs
`
	_, err := ParseAndValidate(rendered, "test.yaml")
	if err == nil {
		t.Fatal("Expected an error for a squashfs publish block with no path")
	}
	if !strings.Contains(err.Error(), "publish 1") {
		t.Errorf("Expected the error to identify publish block 1, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "requires path") {
		t.Errorf("Expected a 'requires path' error, got %q", err.Error())
	}
}

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

// TestRenderConfig_MissingKeyErrors verifies that referencing a key absent
// from vars is a hard error (missingkey=error): a missing variable silently
// rendering to nothing would ship a broken config, so render must fail loudly.
func TestRenderConfig_MissingKeyErrors(t *testing.T) {
	tmpl := filepath.Join(t.TempDir(), "tmpl.yaml")
	if err := os.WriteFile(tmpl, []byte(`meta:
  name: {{ .name }}
  optional: {{ .missing }}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := RenderConfig(tmpl, map[string]interface{}{
		"name": "test",
		// .missing is not provided
	})
	if err == nil {
		t.Fatal("expected an error for a missing template key, got nil")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should name the missing key; got: %v", err)
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
		{
			// container_images and other_files are both optional: a layer
			// that never sets them must validate exactly like one from
			// before the feature existed.
			name: "no container_images or other_files is valid",
			layer: Layer{
				Manager: Manager{Name: "dnf"},
			},
			wantErr: false,
		},
		{
			name: "valid container_images and other_files",
			layer: Layer{
				Manager: Manager{Name: "dnf"},
				ContainerImages: []ContainerImage{
					{Image: "docker.io/library/hello-world:latest"},
				},
				OtherFiles: []OtherFile{
					{TarFile: "https://example.com/archive.tar.gz"},
				},
			},
			wantErr: false,
		},
		{
			name: "container_images entry missing image",
			layer: Layer{
				Manager:         Manager{Name: "dnf"},
				ContainerImages: []ContainerImage{{}},
			},
			wantErr: true,
		},
		{
			name: "other_files entry missing tar_file",
			layer: Layer{
				Manager:    Manager{Name: "dnf"},
				OtherFiles: []OtherFile{{}},
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

// TestContainerImageValidate tests ContainerImage validation in isolation.
func TestContainerImageValidate(t *testing.T) {
	tests := []struct {
		name    string
		img     ContainerImage
		wantErr bool
	}{
		{name: "valid image", img: ContainerImage{Image: "docker.io/library/mysql:9.3.0"}, wantErr: false},
		{name: "missing image", img: ContainerImage{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.img.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("ContainerImage.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestOtherFileValidate tests OtherFile validation in isolation.
func TestOtherFileValidate(t *testing.T) {
	tests := []struct {
		name    string
		of      OtherFile
		wantErr bool
	}{
		{name: "valid local tar_file", of: OtherFile{TarFile: "/opt/vendor-bundle.tar.gz"}, wantErr: false},
		{name: "valid http(s) tar_file", of: OtherFile{TarFile: "https://example.com/archive.tar.gz"}, wantErr: false},
		{name: "missing tar_file", of: OtherFile{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.of.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("OtherFile.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestOtherFileToTarFile verifies the field mapping used to bridge the
// user-facing tar_file/path names to the internal TarFile representation.
func TestOtherFileToTarFile(t *testing.T) {
	extract := true
	tlsVerify := false
	of := OtherFile{
		TarFile:   "https://example.com/archive.tar.gz",
		Path:      "/opt/artifacts/example",
		Extract:   &extract,
		TLSVerify: &tlsVerify,
	}
	tf := of.ToTarFile()
	if tf.Src != of.TarFile {
		t.Errorf("Src = %q, want %q", tf.Src, of.TarFile)
	}
	if tf.Dest != of.Path {
		t.Errorf("Dest = %q, want %q", tf.Dest, of.Path)
	}
	if tf.Extract != of.Extract {
		t.Errorf("Extract pointer not carried through")
	}
	if tf.TLSVerify != of.TLSVerify {
		t.Errorf("TLSVerify pointer not carried through")
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

// TestDirectoryEffectiveExcludes covers the list both the tag hasher and the
// copy step build from: git bookkeeping dropped, user patterns preserved
// after it.
func TestDirectoryEffectiveExcludes(t *testing.T) {
	tests := []struct {
		name     string
		dir      Directory
		expected []string
	}{
		{
			name:     "no excludes still drops git",
			dir:      Directory{Path: "/opt/app", Src: "./tree"},
			expected: []string{"**/.git"},
		},
		{
			name:     "user patterns come after the git default",
			dir:      Directory{Path: "/opt/app", Src: "./tree", Excludes: []string{"*.tmp", "cache"}},
			expected: []string{"**/.git", "*.tmp", "cache"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.dir.EffectiveExcludes()
			if len(got) != len(tt.expected) {
				t.Fatalf("EffectiveExcludes() = %v, want %v", got, tt.expected)
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("EffectiveExcludes()[%d] = %q, want %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

// TestDirectoryEffectiveExcludesDoesNotAliasConfig guards against the helper
// appending into the caller's Excludes backing array — writeDirectories and
// the hasher both call it on the same Directory, and a shared array would let
// one call's result bleed into the other's.
func TestDirectoryEffectiveExcludesDoesNotAliasConfig(t *testing.T) {
	d := Directory{Path: "/opt/app", Src: "./tree", Excludes: []string{"*.tmp"}}

	first := d.EffectiveExcludes()
	first[len(first)-1] = "mutated"

	if d.Excludes[0] != "*.tmp" {
		t.Errorf("EffectiveExcludes must not alias d.Excludes, got %q", d.Excludes[0])
	}
	if second := d.EffectiveExcludes(); second[len(second)-1] != "*.tmp" {
		t.Errorf("second call returned mutated data: %v", second)
	}
}

// TestDirectoryGitExcludeMatchesOnlyGitDir pins what the default pattern is
// allowed to catch. ".gitignore", ".gitkeep", ".gitmodules" and friends are
// source content that has to reach the image, and they sit one character away
// from the pattern — a future tweak to it would drop them silently.
func TestDirectoryGitExcludeMatchesOnlyGitDir(t *testing.T) {
	pm, err := fileutils.NewPatternMatcher((&Directory{Src: "tree"}).EffectiveExcludes())
	if err != nil {
		t.Fatalf("compile default excludes: %v", err)
	}

	excluded := []string{".git", ".git/config", ".git/objects/pack/p.pack", "sub/.git", "sub/.git/index"}
	kept := []string{".gitignore", ".gitkeep", ".gitmodules", ".gitattributes", "sub/.gitignore", "a.git", "dotgit"}

	for _, p := range excluded {
		got, err := pm.Matches(p)
		if err != nil {
			t.Fatalf("match %s: %v", p, err)
		}
		if !got {
			t.Errorf("%s should be excluded by default", p)
		}
	}
	for _, p := range kept {
		got, err := pm.Matches(p)
		if err != nil {
			t.Fatalf("match %s: %v", p, err)
		}
		if got {
			t.Errorf("%s is source content and must not be excluded", p)
		}
	}
}
