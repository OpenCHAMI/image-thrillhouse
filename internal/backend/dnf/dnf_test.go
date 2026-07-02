// SPDX-FileCopyrightText: © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package dnf

import (
	"testing"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
)

func TestNew(t *testing.T) {
	backend := New(nil)
	if backend == nil {
		t.Fatal("New() returned nil")
	}
}

func TestConfigFilePath(t *testing.T) {
	backend := New(nil)
	expected := "/etc/dnf/dnf.conf"

	if got := backend.ConfigFilePath(); got != expected {
		t.Errorf("ConfigFilePath() = %v, want %v", got, expected)
	}
}

func TestSupportsInstallRoot(t *testing.T) {
	backend := New(nil)
	if !backend.SupportsInstallRoot() {
		t.Error("DNF should support install root (scratch builds)")
	}
}

func TestSupportsParentInstall(t *testing.T) {
	backend := New(nil)
	if !backend.SupportsParentInstall() {
		t.Error("DNF should support parent install")
	}
}

func TestInstallCommands(t *testing.T) {
	backend := New(nil)

	tests := []struct {
		name     string
		install  config.Install
		wantCmds int
	}{
		{
			name: "single package",
			install: config.Install{
				Packages: []string{"kernel"},
			},
			wantCmds: 1,
		},
		{
			name: "multiple packages",
			install: config.Install{
				Packages: []string{"kernel", "systemd", "vim"},
			},
			wantCmds: 1,
		},
		{
			name: "package group",
			install: config.Install{
				Groups: []string{"Development Tools"},
			},
			wantCmds: 1,
		},
		{
			name: "packages and groups",
			install: config.Install{
				Packages: []string{"kernel"},
				Groups:   []string{"Development Tools"},
			},
			wantCmds: 2,
		},
		{
			name: "module",
			install: config.Install{
				Modules: []config.Module{
					{Name: "nodejs", Stream: "18", Action: "install"},
				},
			},
			wantCmds: 1,
		},
		{
			name: "packages, groups, and modules",
			install: config.Install{
				Packages: []string{"kernel"},
				Groups:   []string{"Development Tools"},
				Modules: []config.Module{
					{Name: "nodejs", Stream: "18", Action: "install"},
				},
			},
			wantCmds: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmds := backend.InstallCommands(tt.install)
			if len(cmds) != tt.wantCmds {
				t.Errorf("InstallCommands() returned %d commands, want %d", len(cmds), tt.wantCmds)
			}
		})
	}
}

func TestInstallCommandsStructure(t *testing.T) {
	backend := New(nil)

	install := config.Install{
		Packages: []string{"kernel", "systemd"},
	}

	cmds := backend.InstallCommands(install)

	if len(cmds) != 1 {
		t.Fatalf("Expected 1 command, got %d", len(cmds))
	}

	cmd := cmds[0]

	// Check command starts with dnf
	if cmd[0] != "dnf" {
		t.Errorf("Expected command to start with 'dnf', got '%s'", cmd[0])
	}

	// Check for -q flag
	hasQuiet := false
	for _, arg := range cmd {
		if arg == "-q" {
			hasQuiet = true
			break
		}
	}
	if !hasQuiet {
		t.Error("Expected -q flag for quiet output")
	}

	// Check for -y flag
	hasYes := false
	for _, arg := range cmd {
		if arg == "-y" {
			hasYes = true
			break
		}
	}
	if !hasYes {
		t.Error("Expected -y flag for automatic yes")
	}

	// Check packages are included
	hasKernel := false
	hasSystemd := false
	for _, arg := range cmd {
		if arg == "kernel" {
			hasKernel = true
		}
		if arg == "systemd" {
			hasSystemd = true
		}
	}
	if !hasKernel || !hasSystemd {
		t.Error("Expected both kernel and systemd packages in command")
	}
}

func TestInstallRootCommands(t *testing.T) {
	backend := New(nil)
	rootPath := "/mnt/container"

	tests := []struct {
		name     string
		install  config.Install
		wantCmds int
	}{
		{
			name: "single package",
			install: config.Install{
				Packages: []string{"kernel"},
			},
			wantCmds: 1,
		},
		{
			name: "multiple packages",
			install: config.Install{
				Packages: []string{"kernel", "systemd", "vim"},
			},
			wantCmds: 1,
		},
		{
			name: "package group",
			install: config.Install{
				Groups: []string{"Minimal Install"},
			},
			wantCmds: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmds := backend.InstallRootCommands(tt.install, rootPath)
			if len(cmds) != tt.wantCmds {
				t.Errorf("InstallRootCommands() returned %d commands, want %d", len(cmds), tt.wantCmds)
			}
		})
	}
}

func TestInstallRootCommandsStructure(t *testing.T) {
	backend := New(nil)
	rootPath := "/var/lib/containers/storage/overlay/merged"

	install := config.Install{
		Packages: []string{"kernel"},
	}

	cmds := backend.InstallRootCommands(install, rootPath)

	if len(cmds) != 1 {
		t.Fatalf("Expected 1 command, got %d", len(cmds))
	}

	cmd := cmds[0]

	// Check for --installroot flag
	hasInstallRoot := false
	for i, arg := range cmd {
		if arg == "--installroot" {
			hasInstallRoot = true
			if i+1 < len(cmd) && cmd[i+1] == rootPath {
				// Path is correct
			} else {
				t.Errorf("Expected --installroot to be followed by %s", rootPath)
			}
			break
		}
	}
	if !hasInstallRoot {
		t.Error("Expected --installroot flag for scratch builds")
	}
}

func TestInstallCommandsWithModules(t *testing.T) {
	backend := New(nil)

	install := config.Install{
		Modules: []config.Module{
			{Name: "nodejs", Stream: "18", Action: "install"},
			{Name: "ruby", Stream: "3.0", Action: "enable"},
		},
	}

	cmds := backend.InstallCommands(install)

	if len(cmds) != 2 {
		t.Fatalf("Expected 2 commands (one per module), got %d", len(cmds))
	}

	// Check first module command
	cmd1 := cmds[0]
	hasModule := false
	for _, arg := range cmd1 {
		if arg == "module" {
			hasModule = true
			break
		}
	}
	if !hasModule {
		t.Error("Expected 'module' in DNF module command")
	}

	// Check for nodejs:18
	hasNodeJS := false
	for _, arg := range cmd1 {
		if arg == "nodejs:18" {
			hasNodeJS = true
			break
		}
	}
	if !hasNodeJS {
		t.Error("Expected 'nodejs:18' in module command")
	}
}

func TestValidateOptions(t *testing.T) {
	backend := New(nil)

	// Test nil options
	err := backend.ValidateOptions(nil)
	if err != nil {
		t.Errorf("ValidateOptions(nil) error = %v, want nil", err)
	}

	// Test valid options
	err = backend.ValidateOptions(map[string]string{
		"install-weak-deps": "false",
		"best":              "true",
		"skip-broken":       "true",
		"releasever":        "9",
	})
	if err != nil {
		t.Errorf("ValidateOptions() with valid options error = %v, want nil", err)
	}

	// Test invalid option name
	err = backend.ValidateOptions(map[string]string{"invalid-key": "value"})
	if err == nil {
		t.Error("ValidateOptions() with invalid option should return error")
	}

	// Test invalid option value
	err = backend.ValidateOptions(map[string]string{"best": "maybe"})
	if err == nil {
		t.Error("ValidateOptions() with invalid value should return error")
	}

	// Test conflicting options
	err = backend.ValidateOptions(map[string]string{
		"best":   "true",
		"nobest": "true",
	})
	if err == nil {
		t.Error("ValidateOptions() with conflicting options should return error")
	}
}

func TestOutputWriter(t *testing.T) {
	backend := New(nil)

	writer := backend.OutputWriter()
	if writer == nil {
		t.Error("OutputWriter() returned nil")
	}
}
