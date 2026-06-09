package mmdebstrap

import (
	"strings"
	"testing"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
)

func TestNew(t *testing.T) {
	options := map[string]string{
		"suite":  "bookworm",
		"mirror": "http://deb.debian.org/debian",
	}
	backend := New(options)
	if backend == nil {
		t.Fatal("New() returned nil")
	}
}

func TestNewWithDefaults(t *testing.T) {
	options := map[string]string{
		"suite":  "bookworm",
		"mirror": "http://deb.debian.org/debian",
	}
	backend := New(options)

	if backend.suite != "bookworm" {
		t.Errorf("suite = %v, want bookworm", backend.suite)
	}
	if backend.mirror != "http://deb.debian.org/debian" {
		t.Errorf("mirror = %v, want http://deb.debian.org/debian", backend.mirror)
	}
	if backend.variant != "minbase" {
		t.Errorf("variant = %v, want minbase (default)", backend.variant)
	}
	if backend.mode != "fakechroot" {
		t.Errorf("mode = %v, want fakechroot (default)", backend.mode)
	}
}

func TestNewWithCustomOptions(t *testing.T) {
	options := map[string]string{
		"suite":   "bullseye",
		"mirror":  "http://ftp.debian.org/debian",
		"variant": "buildd",
		"mode":    "fakeroot",
	}
	backend := New(options)

	if backend.suite != "bullseye" {
		t.Errorf("suite = %v, want bullseye", backend.suite)
	}
	if backend.mirror != "http://ftp.debian.org/debian" {
		t.Errorf("mirror = %v, want http://ftp.debian.org/debian", backend.mirror)
	}
	if backend.variant != "buildd" {
		t.Errorf("variant = %v, want buildd", backend.variant)
	}
	if backend.mode != "fakeroot" {
		t.Errorf("mode = %v, want fakeroot", backend.mode)
	}
}

func TestConfigFilePath(t *testing.T) {
	// mmdebstrap has no persistent config file; ConfigFilePath returns "" so
	// the builder can refuse layer.manager.config rather than writing to a
	// bogus path.
	backend := New(map[string]string{
		"suite":  "bookworm",
		"mirror": "http://deb.debian.org/debian",
	})
	if got := backend.ConfigFilePath(); got != "" {
		t.Errorf("ConfigFilePath() = %q, want \"\"", got)
	}
}

func TestSupportsInstallRoot(t *testing.T) {
	backend := New(map[string]string{
		"suite":  "bookworm",
		"mirror": "http://deb.debian.org/debian",
	})
	if !backend.SupportsInstallRoot() {
		t.Error("mmdebstrap should support install root (scratch builds)")
	}
}

func TestSupportsParentInstall(t *testing.T) {
	backend := New(map[string]string{
		"suite":  "bookworm",
		"mirror": "http://deb.debian.org/debian",
	})
	// mmdebstrap can only bootstrap from scratch; use apt for parent builds.
	if backend.SupportsParentInstall() {
		t.Error("mmdebstrap SupportsParentInstall() should return false")
	}
}

func TestInstallCommands(t *testing.T) {
	backend := New(map[string]string{
		"suite":  "bookworm",
		"mirror": "http://deb.debian.org/debian",
	})

	install := config.Install{
		Packages: []string{"vim"},
	}

	cmds := backend.InstallCommands(install)

	// mmdebstrap does not support parent installs, should return nil
	if cmds != nil {
		t.Error("mmdebstrap should not support InstallCommands (use apt for parent builds)")
	}
}

func TestInstallRootCommands(t *testing.T) {
	tests := []struct {
		name     string
		options  map[string]string
		install  config.Install
		rootPath string
		wantCmds int
	}{
		{
			name: "basic packages",
			options: map[string]string{
				"suite":  "bookworm",
				"mirror": "http://deb.debian.org/debian",
			},
			install: config.Install{
				Packages: []string{"vim", "curl"},
			},
			rootPath: "/mnt/container",
			wantCmds: 1,
		},
		{
			name: "no packages",
			options: map[string]string{
				"suite":  "bookworm",
				"mirror": "http://deb.debian.org/debian",
			},
			install: config.Install{
				Packages: []string{},
			},
			rootPath: "/mnt/container",
			wantCmds: 1, // Still creates base system
		},
		{
			name: "custom variant",
			options: map[string]string{
				"suite":   "bookworm",
				"mirror":  "http://deb.debian.org/debian",
				"variant": "buildd",
			},
			install: config.Install{
				Packages: []string{"build-essential"},
			},
			rootPath: "/mnt/container",
			wantCmds: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := New(tt.options)
			cmds := backend.InstallRootCommands(tt.install, tt.rootPath)
			if len(cmds) != tt.wantCmds {
				t.Errorf("InstallRootCommands() returned %d commands, want %d", len(cmds), tt.wantCmds)
			}
		})
	}
}

func TestInstallRootCommandsStructure(t *testing.T) {
	options := map[string]string{
		"suite":   "bookworm",
		"mirror":  "http://deb.debian.org/debian",
		"variant": "minbase",
		"mode":    "fakechroot",
	}
	backend := New(options)
	rootPath := "/var/lib/containers/storage/overlay/merged"

	install := config.Install{
		Packages: []string{"vim", "curl", "git"},
	}

	cmds := backend.InstallRootCommands(install, rootPath)

	if len(cmds) != 1 {
		t.Fatalf("Expected 1 command, got %d", len(cmds))
	}

	cmd := cmds[0]

	// Check command starts with mmdebstrap
	if cmd[0] != "mmdebstrap" {
		t.Errorf("Expected command to start with 'mmdebstrap', got '%s'", cmd[0])
	}

	// Check for --mode flag
	hasMode := false
	for _, arg := range cmd {
		if strings.HasPrefix(arg, "--mode=") {
			hasMode = true
			if arg != "--mode=fakechroot" {
				t.Errorf("Expected --mode=fakechroot, got %s", arg)
			}
			break
		}
	}
	if !hasMode {
		t.Error("Expected --mode flag")
	}

	// Check for --variant flag
	hasVariant := false
	for _, arg := range cmd {
		if strings.HasPrefix(arg, "--variant=") {
			hasVariant = true
			if arg != "--variant=minbase" {
				t.Errorf("Expected --variant=minbase, got %s", arg)
			}
			break
		}
	}
	if !hasVariant {
		t.Error("Expected --variant flag")
	}

	// Check for --include flag with packages
	hasInclude := false
	for _, arg := range cmd {
		if strings.HasPrefix(arg, "--include=") {
			hasInclude = true
			// Should contain all packages
			if !strings.Contains(arg, "vim") {
				t.Error("Expected vim in --include")
			}
			if !strings.Contains(arg, "curl") {
				t.Error("Expected curl in --include")
			}
			if !strings.Contains(arg, "git") {
				t.Error("Expected git in --include")
			}
			break
		}
	}
	if !hasInclude {
		t.Error("Expected --include flag with packages")
	}

	// Check suite is present
	hasSuite := false
	for _, arg := range cmd {
		if arg == "bookworm" {
			hasSuite = true
			break
		}
	}
	if !hasSuite {
		t.Error("Expected suite (bookworm) in command")
	}

	// Check rootPath is present
	hasRootPath := false
	for _, arg := range cmd {
		if arg == rootPath {
			hasRootPath = true
			break
		}
	}
	if !hasRootPath {
		t.Error("Expected root path in command")
	}

	// Check mirror is present
	hasMirror := false
	for _, arg := range cmd {
		if arg == "http://deb.debian.org/debian" {
			hasMirror = true
			break
		}
	}
	if !hasMirror {
		t.Error("Expected mirror URL in command")
	}
}

func TestValidateOptions(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]string
		wantErr bool
	}{
		{
			name: "valid options",
			options: map[string]string{
				"suite":  "bookworm",
				"mirror": "http://deb.debian.org/debian",
			},
			wantErr: false,
		},
		{
			name: "valid with optional",
			options: map[string]string{
				"suite":   "bookworm",
				"mirror":  "http://deb.debian.org/debian",
				"variant": "buildd",
				"mode":    "fakeroot",
			},
			wantErr: false,
		},
		{
			name: "missing suite",
			options: map[string]string{
				"mirror": "http://deb.debian.org/debian",
			},
			wantErr: true,
		},
		{
			name: "missing mirror",
			options: map[string]string{
				"suite": "bookworm",
			},
			wantErr: true,
		},
		{
			name:    "missing both required",
			options: map[string]string{},
			wantErr: true,
		},
		{
			name:    "nil options",
			options: nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := New(tt.options)
			err := backend.ValidateOptions(tt.options)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateOptions() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestOutputWriter(t *testing.T) {
	backend := New(map[string]string{
		"suite":  "bookworm",
		"mirror": "http://deb.debian.org/debian",
	})

	writer := backend.OutputWriter()
	if writer == nil {
		t.Error("OutputWriter() returned nil")
	}
}

func TestPackageListFormatting(t *testing.T) {
	backend := New(map[string]string{
		"suite":  "bookworm",
		"mirror": "http://deb.debian.org/debian",
	})

	install := config.Install{
		Packages: []string{"vim", "curl", "git", "build-essential"},
	}

	cmds := backend.InstallRootCommands(install, "/mnt")

	if len(cmds) != 1 {
		t.Fatalf("Expected 1 command, got %d", len(cmds))
	}

	cmd := cmds[0]

	// Find the --include flag
	var includeArg string
	for _, arg := range cmd {
		if strings.HasPrefix(arg, "--include=") {
			includeArg = arg
			break
		}
	}

	if includeArg == "" {
		t.Fatal("No --include flag found")
	}

	// Extract package list
	packageList := strings.TrimPrefix(includeArg, "--include=")
	packages := strings.Split(packageList, ",")

	if len(packages) != 4 {
		t.Errorf("Expected 4 packages in include list, got %d", len(packages))
	}

	// Verify all packages are present
	packageMap := make(map[string]bool)
	for _, pkg := range packages {
		packageMap[pkg] = true
	}

	for _, expected := range []string{"vim", "curl", "git", "build-essential"} {
		if !packageMap[expected] {
			t.Errorf("Expected package %s in --include list", expected)
		}
	}
}

func TestCommandOrder(t *testing.T) {
	backend := New(map[string]string{
		"suite":   "bookworm",
		"mirror":  "http://deb.debian.org/debian",
		"variant": "minbase",
		"mode":    "fakechroot",
	})

	install := config.Install{
		Packages: []string{"vim"},
	}

	cmds := backend.InstallRootCommands(install, "/mnt")
	cmd := cmds[0]

	// Expected order: mmdebstrap --mode=... --variant=... --include=... suite rootpath mirror
	// Verify mmdebstrap is first
	if cmd[0] != "mmdebstrap" {
		t.Errorf("Expected mmdebstrap to be first, got %s", cmd[0])
	}

	// Find positions of key elements
	suitePos := -1
	rootPathPos := -1
	mirrorPos := -1

	for i, arg := range cmd {
		if arg == "bookworm" {
			suitePos = i
		}
		if arg == "/mnt" {
			rootPathPos = i
		}
		if arg == "http://deb.debian.org/debian" {
			mirrorPos = i
		}
	}

	// Verify order: suite before rootpath before mirror
	if suitePos == -1 || rootPathPos == -1 || mirrorPos == -1 {
		t.Fatal("Missing required positional arguments")
	}

	if suitePos >= rootPathPos {
		t.Error("Suite should come before root path")
	}
	if rootPathPos >= mirrorPos {
		t.Error("Root path should come before mirror")
	}
}
