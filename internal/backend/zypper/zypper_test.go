// SPDX-FileCopyrightText: © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package zypper

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
	expected := "/etc/zypp/zypp.conf"

	if got := backend.ConfigFilePath(); got != expected {
		t.Errorf("ConfigFilePath() = %v, want %v", got, expected)
	}
}

func TestSupportsInstallRoot(t *testing.T) {
	backend := New(nil)
	if !backend.SupportsInstallRoot() {
		t.Error("Zypper should support install root (scratch builds)")
	}
}

func TestSupportsParentInstall(t *testing.T) {
	backend := New(nil)
	if !backend.SupportsParentInstall() {
		t.Error("Zypper should support parent install")
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
				Packages: []string{"kernel-default"},
			},
			wantCmds: 1,
		},
		{
			name: "multiple packages",
			install: config.Install{
				Packages: []string{"kernel-default", "systemd", "vim"},
			},
			wantCmds: 1,
		},
		{
			name: "groups (patterns) supported",
			install: config.Install{
				Groups: []string{"Development"},
			},
			wantCmds: 1, // Zypper supports groups as patterns
		},
		{
			name: "packages and groups",
			install: config.Install{
				Packages: []string{"vim"},
				Groups:   []string{"Development"},
			},
			wantCmds: 2, // One for packages, one for groups
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
		Packages: []string{"vim", "curl"},
	}

	cmds := backend.InstallCommands(install)

	if len(cmds) != 1 {
		t.Fatalf("Expected 1 command, got %d", len(cmds))
	}

	cmd := cmds[0]

	// Check command starts with zypper
	if cmd[0] != "zypper" {
		t.Errorf("Expected command to start with 'zypper', got '%s'", cmd[0])
	}

	// Check for install subcommand
	hasInstall := false
	for _, arg := range cmd {
		if arg == "install" {
			hasInstall = true
			break
		}
	}
	if !hasInstall {
		t.Error("Expected 'install' subcommand")
	}

	// Check for -y flag (auto-confirm)
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
}

func TestInstallRootCommands(t *testing.T) {
	backend := New(nil)
	rootPath := "/mnt/container"

	install := config.Install{
		Packages: []string{"vim"},
	}

	cmds := backend.InstallRootCommands(install, rootPath)

	if len(cmds) != 1 {
		t.Fatalf("Expected 1 command, got %d", len(cmds))
	}

	cmd := cmds[0]

	// Check for --installroot flag (not --root)
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

func TestValidateOptions(t *testing.T) {
	backend := New(nil)

	err := backend.ValidateOptions(nil)
	if err != nil {
		t.Errorf("ValidateOptions() error = %v, want nil", err)
	}
}

func TestOutputWriter(t *testing.T) {
	backend := New(nil)

	writer := backend.OutputWriter()
	if writer == nil {
		t.Error("OutputWriter() returned nil")
	}
}

func TestIsAcceptableExitCode(t *testing.T) {
	backend := New(nil)

	tests := []struct {
		name     string
		exitCode int
		output   string
		want     bool
	}{
		{"zero exit not consulted but should not be acceptable here", 0, "", false},
		{"unrelated failure", 1, "Installing: bash", false},
		{"err_commit with install evidence", 8, "Installing: bash-5.1", true},
		{"err_commit with NEW packages evidence", 8, "The following NEW packages are going to be installed:\n  bash", true},
		{"err_commit without evidence", 8, "some unrelated zypper error", false},
		{"reboot needed (102) is informational", 102, "", true},
		{"restart needed (103) is informational", 103, "", true},
		{"rpm script failed (107) is informational", 107, "warning: scriptlet failed", true},
		{"unknown high code stays an error", 199, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backend.IsAcceptableExitCode(tt.exitCode, tt.output); got != tt.want {
				t.Errorf("IsAcceptableExitCode(%d, %q) = %v, want %v",
					tt.exitCode, tt.output, got, tt.want)
			}
		})
	}
}
