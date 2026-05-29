package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfig tests loading a valid configuration file
func TestLoadConfig(t *testing.T) {
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
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
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
	_, err := LoadConfig("/nonexistent/config.yaml")
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

	_, err = LoadConfig(configPath)
	if err == nil {
		t.Error("Expected error for invalid YAML, got nil")
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
