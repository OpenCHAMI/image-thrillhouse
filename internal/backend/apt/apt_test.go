// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package apt

import (
	"strings"
	"testing"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
)

func TestNew(t *testing.T) {
	backend := New(nil)
	if backend == nil {
		t.Fatal("New() returned nil")
	}
}

func TestNewWithOptions(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]string
		want    *AptBackend
	}{
		{
			name:    "nil options",
			options: nil,
			want: &AptBackend{
				installRecommends:    false,
				installSuggests:      false,
				allowUnauthenticated: false,
			},
		},
		{
			name:    "empty options",
			options: map[string]string{},
			want: &AptBackend{
				installRecommends:    false,
				installSuggests:      false,
				allowUnauthenticated: false,
			},
		},
		{
			name: "install-recommends true",
			options: map[string]string{
				"install-recommends": "true",
			},
			want: &AptBackend{
				installRecommends:    true,
				installSuggests:      false,
				allowUnauthenticated: false,
			},
		},
		{
			name: "all options true",
			options: map[string]string{
				"install-recommends":    "true",
				"install-suggests":      "true",
				"allow-unauthenticated": "true",
			},
			want: &AptBackend{
				installRecommends:    true,
				installSuggests:      true,
				allowUnauthenticated: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(tt.options)
			if got.installRecommends != tt.want.installRecommends {
				t.Errorf("installRecommends = %v, want %v", got.installRecommends, tt.want.installRecommends)
			}
			if got.installSuggests != tt.want.installSuggests {
				t.Errorf("installSuggests = %v, want %v", got.installSuggests, tt.want.installSuggests)
			}
			if got.allowUnauthenticated != tt.want.allowUnauthenticated {
				t.Errorf("allowUnauthenticated = %v, want %v", got.allowUnauthenticated, tt.want.allowUnauthenticated)
			}
		})
	}
}

func TestConfigFilePath(t *testing.T) {
	backend := New(nil)
	expected := "/etc/apt/apt.conf.d/99-image-thrillhouse.conf"

	if got := backend.ConfigFilePath(); got != expected {
		t.Errorf("ConfigFilePath() = %v, want %v", got, expected)
	}
}

func TestSupportsInstallRoot(t *testing.T) {
	backend := New(nil)
	if backend.SupportsInstallRoot() {
		t.Error("APT should not support install root (use mmdebstrap for scratch builds)")
	}
}

func TestSupportsParentInstall(t *testing.T) {
	backend := New(nil)
	if !backend.SupportsParentInstall() {
		t.Error("APT should support parent install")
	}
}

func TestInstallCommands(t *testing.T) {
	tests := []struct {
		name     string
		options  map[string]string
		install  config.Install
		wantCmds int
	}{
		{
			name:    "single package",
			options: nil,
			install: config.Install{
				Packages: []string{"vim"},
			},
			wantCmds: 2, // update + install
		},
		{
			name:    "multiple packages",
			options: nil,
			install: config.Install{
				Packages: []string{"vim", "curl", "git"},
			},
			wantCmds: 2, // update + install
		},
		{
			name:    "no packages",
			options: nil,
			install: config.Install{
				Packages: []string{},
			},
			wantCmds: 1, // update only
		},
		{
			name:    "packages with groups (ignored)",
			options: nil,
			install: config.Install{
				Packages: []string{"vim"},
				Groups:   []string{"build-essential"}, // Groups are ignored with warning
			},
			wantCmds: 2, // update + install (groups ignored)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := New(tt.options)
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

	if len(cmds) < 2 {
		t.Fatalf("Expected at least 2 commands (update + install), got %d", len(cmds))
	}

	// Check first command is update
	updateCmd := cmds[0]
	if updateCmd[0] != "apt-get" || updateCmd[1] != "update" {
		t.Errorf("Expected first command to be 'apt-get update', got '%s %s'", updateCmd[0], updateCmd[1])
	}

	// Check install command
	installCmd := cmds[1]

	// Check command starts with apt-get
	if installCmd[0] != "apt-get" {
		t.Errorf("Expected command to start with 'apt-get', got '%s'", installCmd[0])
	}

	// Check for -y flag
	hasYes := false
	for _, arg := range installCmd {
		if arg == "-y" {
			hasYes = true
			break
		}
	}
	if !hasYes {
		t.Error("Expected -y flag for automatic yes")
	}

	// Check for -q flag
	hasQuiet := false
	for _, arg := range installCmd {
		if arg == "-q" {
			hasQuiet = true
			break
		}
	}
	if !hasQuiet {
		t.Error("Expected -q flag for quiet output")
	}

	// Check packages are included
	hasVim := false
	hasCurl := false
	for _, arg := range installCmd {
		if arg == "vim" {
			hasVim = true
		}
		if arg == "curl" {
			hasCurl = true
		}
	}
	if !hasVim || !hasCurl {
		t.Error("Expected both vim and curl packages in command")
	}
}

func TestInstallCommandsWithOptions(t *testing.T) {
	tests := []struct {
		name           string
		options        map[string]string
		wantRecommends bool
		wantSuggests   bool
		wantUnauth     bool
	}{
		{
			name:           "default options",
			options:        nil,
			wantRecommends: false,
			wantSuggests:   false,
			wantUnauth:     false,
		},
		{
			name: "install-recommends true",
			options: map[string]string{
				"install-recommends": "true",
			},
			wantRecommends: true,
			wantSuggests:   false,
			wantUnauth:     false,
		},
		{
			name: "all options enabled",
			options: map[string]string{
				"install-recommends":    "true",
				"install-suggests":      "true",
				"allow-unauthenticated": "true",
			},
			wantRecommends: true,
			wantSuggests:   true,
			wantUnauth:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := New(tt.options)
			install := config.Install{
				Packages: []string{"vim"},
			}

			cmds := backend.InstallCommands(install)
			if len(cmds) < 2 {
				t.Fatalf("Expected at least 2 commands, got %d", len(cmds))
			}

			installCmd := cmds[1]

			// Check for --no-install-recommends
			hasNoRecommends := false
			for _, arg := range installCmd {
				if arg == "--no-install-recommends" {
					hasNoRecommends = true
					break
				}
			}
			if tt.wantRecommends && hasNoRecommends {
				t.Error("Should not have --no-install-recommends when install-recommends is true")
			}
			if !tt.wantRecommends && !hasNoRecommends {
				t.Error("Should have --no-install-recommends when install-recommends is false")
			}

			// Check for --no-install-suggests
			hasNoSuggests := false
			for _, arg := range installCmd {
				if arg == "--no-install-suggests" {
					hasNoSuggests = true
					break
				}
			}
			if tt.wantSuggests && hasNoSuggests {
				t.Error("Should not have --no-install-suggests when install-suggests is true")
			}
			if !tt.wantSuggests && !hasNoSuggests {
				t.Error("Should have --no-install-suggests when install-suggests is false")
			}

			// Check for --allow-unauthenticated
			hasUnauth := false
			for _, arg := range installCmd {
				if arg == "--allow-unauthenticated" {
					hasUnauth = true
					break
				}
			}
			if tt.wantUnauth != hasUnauth {
				t.Errorf("allow-unauthenticated flag presence = %v, want %v", hasUnauth, tt.wantUnauth)
			}
		})
	}
}

func TestInstallRootCommands(t *testing.T) {
	backend := New(nil)
	rootPath := "/mnt/container"

	install := config.Install{
		Packages: []string{"vim"},
	}

	cmds := backend.InstallRootCommands(install, rootPath)

	// APT does not support installroot, should return nil
	if cmds != nil {
		t.Error("APT should not support InstallRootCommands (use mmdebstrap for scratch builds)")
	}
}

func TestValidateOptions(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]string
		wantErr bool
	}{
		{
			name:    "nil options",
			options: nil,
			wantErr: false,
		},
		{
			name:    "empty options",
			options: map[string]string{},
			wantErr: false,
		},
		{
			name: "valid options",
			options: map[string]string{
				"install-recommends": "true",
				"install-suggests":   "false",
			},
			wantErr: false,
		},
		{
			name: "invalid option name",
			options: map[string]string{
				"invalid-option": "true",
			},
			wantErr: true,
		},
		{
			name: "invalid option value",
			options: map[string]string{
				"install-recommends": "yes",
			},
			wantErr: true,
		},
		{
			name: "mixed valid and invalid",
			options: map[string]string{
				"install-recommends": "true",
				"invalid-option":     "true",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := New(nil)
			err := backend.ValidateOptions(tt.options)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateOptions() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOutputWriter(t *testing.T) {
	backend := New(nil)

	writer := backend.OutputWriter()
	if writer == nil {
		t.Error("OutputWriter() returned nil")
	}
}

func TestWireRepoContent(t *testing.T) {
	backend := New(nil)

	// With no key name, content is untouched.
	const oneLine = "deb http://deb.debian.org/debian bookworm main\n"
	if got := backend.WireRepoContent(oneLine, ""); got != oneLine {
		t.Errorf("empty keyName should leave content unchanged, got %q", got)
	}

	// With a key name, apt injects a signed-by pointing at the keyring for
	// that name. We don't hardcode the full path (that's cmdutil's contract,
	// tested there) — just that the linkage is added and references keyrings.
	got := backend.WireRepoContent(oneLine, "toolchain")
	if !strings.Contains(got, "signed-by=/etc/apt/keyrings/toolchain.gpg") {
		t.Errorf("expected signed-by wired to the keyring, got %q", got)
	}

	// A user-pinned keyring must win.
	pinned := "deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] http://x bookworm main\n"
	if got := backend.WireRepoContent(pinned, "toolchain"); got != pinned {
		t.Errorf("user-pinned signed-by should be preserved, got %q", got)
	}
}

func TestInstallCommandsIgnoresGroupsAndModules(t *testing.T) {
	backend := New(nil)

	install := config.Install{
		Packages: []string{"vim"},
		Groups:   []string{"build-essential"},
		Modules: []config.Module{
			{Name: "nodejs", Stream: "18", Action: "install"},
		},
	}

	cmds := backend.InstallCommands(install)

	// Should only have update + install commands (groups and modules ignored)
	if len(cmds) != 2 {
		t.Errorf("Expected 2 commands (update + install), got %d", len(cmds))
	}

	// Verify no group-related commands were added
	for _, cmd := range cmds {
		for _, arg := range cmd {
			if arg == "build-essential" {
				t.Error("Groups should be ignored by APT backend")
			}
			if arg == "nodejs" {
				t.Error("Modules should be ignored by APT backend")
			}
		}
	}
}
