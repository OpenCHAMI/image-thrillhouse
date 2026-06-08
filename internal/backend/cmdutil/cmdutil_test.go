package cmdutil

import (
	"reflect"
	"strings"
	"testing"
)

func TestRPMRemove(t *testing.T) {
	tests := []struct {
		name     string
		rootPath string
		packages []string
		want     []string
	}{
		{
			name:     "no packages returns nil",
			rootPath: "",
			packages: nil,
			want:     nil,
		},
		{
			name:     "empty slice returns nil",
			rootPath: "/mnt",
			packages: []string{},
			want:     nil,
		},
		{
			name:     "container-side removal omits --root",
			rootPath: "",
			packages: []string{"vim", "nano"},
			want:     []string{"rpm", "-e", "--nodeps", "vim", "nano"},
		},
		{
			name:     "scratch-root removal injects --root",
			rootPath: "/mnt/scratch",
			packages: []string{"vim"},
			want:     []string{"rpm", "--root", "/mnt/scratch", "-e", "--nodeps", "vim"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RPMRemove(tt.rootPath, tt.packages)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RPMRemove(%q, %v) = %v, want %v", tt.rootPath, tt.packages, got, tt.want)
			}
		})
	}
}

func TestRPMImportKey(t *testing.T) {
	tests := []struct {
		name     string
		rootPath string
		keyPath  string
		want     []string
	}{
		{
			name:     "empty keyPath returns nil",
			rootPath: "",
			keyPath:  "",
			want:     nil,
		},
		{
			name:     "container-side import",
			rootPath: "",
			keyPath:  "/tmp/key.bin",
			want:     []string{"rpm", "--import", "/tmp/key.bin"},
		},
		{
			name:     "scratch-root import",
			rootPath: "/mnt/scratch",
			keyPath:  "/host/tmp/key.bin",
			want:     []string{"rpm", "--root", "/mnt/scratch", "--import", "/host/tmp/key.bin"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RPMImportKey(tt.rootPath, tt.keyPath)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("RPMImportKey(%q, %q) = %v, want %v", tt.rootPath, tt.keyPath, got, tt.want)
			}
		})
	}
}

func TestDPKGRemove(t *testing.T) {
	tests := []struct {
		name     string
		rootPath string
		packages []string
		want     []string
	}{
		{
			name:     "no packages returns nil",
			packages: nil,
			want:     nil,
		},
		{
			name:     "container-side removal",
			rootPath: "",
			packages: []string{"nano"},
			want:     []string{"dpkg", "--remove", "--force-depends", "nano"},
		},
		{
			name:     "scratch-root removal injects --root",
			rootPath: "/mnt/scratch",
			packages: []string{"nano", "vim-tiny"},
			want:     []string{"dpkg", "--root", "/mnt/scratch", "--remove", "--force-depends", "nano", "vim-tiny"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DPKGRemove(tt.rootPath, tt.packages)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DPKGRemove(%q, %v) = %v, want %v", tt.rootPath, tt.packages, got, tt.want)
			}
		})
	}
}

func TestAPTImportKey(t *testing.T) {
	tests := []struct {
		name     string
		rootPath string
		keyPath  string
		wantNil  bool
		// substrings the rendered script must contain
		mustContain []string
	}{
		{
			name:    "empty keyPath returns nil",
			keyPath: "",
			wantNil: true,
		},
		{
			name:        "container-side",
			rootPath:    "",
			keyPath:     "/tmp/key.bin",
			mustContain: []string{"sh", "-c", "/tmp/key.bin", "/etc/apt/trusted.gpg.d/image-build-repo.gpg", "gpg --dearmor"},
		},
		{
			name:        "scratch-root prefixes destination path",
			rootPath:    "/mnt/scratch",
			keyPath:     "/tmp/key.bin",
			mustContain: []string{"/mnt/scratch/etc/apt/trusted.gpg.d/image-build-repo.gpg"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := APTImportKey(tt.rootPath, tt.keyPath)
			if tt.wantNil {
				if got != nil {
					t.Errorf("APTImportKey expected nil, got %v", got)
				}
				return
			}
			if len(got) < 3 || got[0] != "sh" || got[1] != "-c" {
				t.Fatalf("APTImportKey expected sh -c <script>, got %v", got)
			}
			script := got[2]
			for _, want := range tt.mustContain {
				if !strings.Contains(strings.Join(got, " "), want) && !strings.Contains(script, want) {
					t.Errorf("script missing %q\nfull cmd: %v\nscript: %s", want, got, script)
				}
			}
		})
	}
}

// TestAPTImportKey_PositionalArgs locks in the post-fix shape: paths must be
// passed as argv to sh (not interpolated into the script body) so that any
// future widening of input sources can't introduce a shell-injection vector.
func TestAPTImportKey_PositionalArgs(t *testing.T) {
	got := APTImportKey("/mnt/root", "/host/tmp/key.bin")
	if len(got) != 6 {
		t.Fatalf("expected 6 argv elements (sh -c <script> $0 $1 $2), got %d: %v", len(got), got)
	}
	if got[0] != "sh" || got[1] != "-c" {
		t.Fatalf("expected sh -c prefix, got %v", got[:2])
	}
	// The script must reference positional params, NOT contain the actual paths.
	script := got[2]
	if !strings.Contains(script, `"$1"`) || !strings.Contains(script, `"$2"`) {
		t.Errorf("script does not use $1/$2 positional params: %q", script)
	}
	// And the paths themselves must NOT appear in the script text.
	if strings.Contains(script, "/mnt/root") || strings.Contains(script, "/host/tmp/key.bin") {
		t.Errorf("paths leaked into script body (injection surface): %q", script)
	}
	// Argv positions: $0 first, then $1 (dest), then $2 (key).
	if got[4] != "/mnt/root/etc/apt/trusted.gpg.d/image-build-repo.gpg" {
		t.Errorf("argv[4] (destination) = %q, want scratch-rooted path", got[4])
	}
	if got[5] != "/host/tmp/key.bin" {
		t.Errorf("argv[5] (key path) = %q, want /host/tmp/key.bin", got[5])
	}
}

// TestAPTImportKey_NoShellInjection feeds a malicious-looking path with shell
// metacharacters; the script must remain a fixed string and the dangerous
// bytes must only appear as argv values.
func TestAPTImportKey_NoShellInjection(t *testing.T) {
	malicious := `/tmp/key.bin"; rm -rf / #`
	got := APTImportKey("", malicious)
	script := got[2]
	if strings.Contains(script, "rm -rf") {
		t.Errorf("malicious bytes interpolated into script: %q", script)
	}
	// The malicious string still has to round-trip as the keyPath argv slot
	// so the dearmor/cp can find the file.
	if got[5] != malicious {
		t.Errorf("argv[5] = %q, want verbatim malicious path %q", got[5], malicious)
	}
}

func TestValidateOptionSchema_Unknown(t *testing.T) {
	schema := map[string]OptionKind{"foo": OptionBool}
	err := ValidateOptionSchema("test", map[string]string{"bar": "true"}, schema)
	if err == nil {
		t.Fatal("expected error for unknown option")
	}
	if !strings.Contains(err.Error(), "unknown option") {
		t.Errorf("expected 'unknown option' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "test") {
		t.Errorf("expected backend name 'test' in error, got: %v", err)
	}
}

func TestValidateOptionSchema_Bool(t *testing.T) {
	schema := map[string]OptionKind{"flag": OptionBool}

	for _, v := range []string{"", "true", "false"} {
		if err := ValidateOptionSchema("test", map[string]string{"flag": v}, schema); err != nil {
			t.Errorf("OptionBool value %q should be accepted, got: %v", v, err)
		}
	}

	for _, v := range []string{"yes", "1", "TRUE", "0"} {
		if err := ValidateOptionSchema("test", map[string]string{"flag": v}, schema); err == nil {
			t.Errorf("OptionBool value %q should be rejected", v)
		}
	}
}

func TestValidateOptionSchema_String(t *testing.T) {
	schema := map[string]OptionKind{"path": OptionString}

	if err := ValidateOptionSchema("test", map[string]string{"path": "/etc"}, schema); err != nil {
		t.Errorf("non-empty string should be accepted, got: %v", err)
	}
	if err := ValidateOptionSchema("test", map[string]string{"path": ""}, schema); err == nil {
		t.Error("empty string should be rejected for OptionString")
	}
}

func TestValidateOptionSchema_Any(t *testing.T) {
	schema := map[string]OptionKind{"raw": OptionAny}
	for _, v := range []string{"", "true", "anything goes"} {
		if err := ValidateOptionSchema("test", map[string]string{"raw": v}, schema); err != nil {
			t.Errorf("OptionAny value %q should be accepted, got: %v", v, err)
		}
	}
}

func TestValidateOptionSchema_EmptyOptions(t *testing.T) {
	schema := map[string]OptionKind{"foo": OptionBool}
	if err := ValidateOptionSchema("test", nil, schema); err != nil {
		t.Errorf("nil options map should be accepted, got: %v", err)
	}
	if err := ValidateOptionSchema("test", map[string]string{}, schema); err != nil {
		t.Errorf("empty options map should be accepted, got: %v", err)
	}
}

func TestExtractMacroOptions(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]string
		want    map[string]string
	}{
		{
			name:    "no macro options",
			options: map[string]string{"releasever": "8", "install-weak-deps": "false"},
			want:    map[string]string{},
		},
		{
			name:    "empty options",
			options: map[string]string{},
			want:    map[string]string{},
		},
		{
			name:    "nil options",
			options: nil,
			want:    map[string]string{},
		},
		{
			name: "single macro option",
			options: map[string]string{
				"macro._dbpath": "/var/lib/rpm",
				"releasever":    "8",
			},
			want: map[string]string{
				"_dbpath": "/var/lib/rpm",
			},
		},
		{
			name: "multiple macro options",
			options: map[string]string{
				"macro._dbpath":         "/var/lib/rpm",
				"macro._dbpath_trans":   "/var/lib/rpm",
				"macro._netsharedpath":  "/sys:/proc",
				"install-weak-deps":     "false",
				"releasever":            "8",
			},
			want: map[string]string{
				"_dbpath":        "/var/lib/rpm",
				"_dbpath_trans":  "/var/lib/rpm",
				"_netsharedpath": "/sys:/proc",
			},
		},
		{
			name: "macro option with empty value",
			options: map[string]string{
				"macro._test": "",
			},
			want: map[string]string{
				"_test": "",
			},
		},
		{
			name: "ignore malformed macro prefix",
			options: map[string]string{
				"macro.": "ignored",
				"macro":  "not-a-macro",
			},
			want: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractMacroOptions(tt.options)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExtractMacroOptions() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildRPMMacros(t *testing.T) {
	tests := []struct {
		name         string
		customMacros map[string]string
		mustContain  []string
		mustNotContain []string
	}{
		{
			name:         "no custom macros uses defaults",
			customMacros: nil,
			mustContain: []string{
				"%_netsharedpath /sys:/proc:/dev",
				"%_install_langs C:en:en_US:en_US.UTF-8",
				"%__brp_mangle_shebangs %{nil}",
				"%_missing_build_ids_terminate_build 0",
				"%_file_context_file %{nil}",
				"%__brp_ldconfig %{nil}",
			},
		},
		{
			name:         "empty custom macros",
			customMacros: map[string]string{},
			mustContain: []string{
				"%_netsharedpath /sys:/proc:/dev",
				"%_install_langs",
			},
		},
		{
			name: "custom macros are added",
			customMacros: map[string]string{
				"_dbpath":       "/var/lib/rpm",
				"_dbpath_trans": "/var/lib/rpm",
			},
			mustContain: []string{
				"%_dbpath /var/lib/rpm",
				"%_dbpath_trans /var/lib/rpm",
				"%_netsharedpath /sys:/proc:/dev",
			},
		},
		{
			name: "custom macros override defaults",
			customMacros: map[string]string{
				"_netsharedpath": "/sys:/proc",
			},
			mustContain: []string{
				"%_netsharedpath /sys:/proc",
			},
			mustNotContain: []string{
				"%_netsharedpath /sys:/proc:/dev",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildRPMMacros(tt.customMacros)
			for _, want := range tt.mustContain {
				if !strings.Contains(got, want) {
					t.Errorf("BuildRPMMacros() missing %q\nGot:\n%s", want, got)
				}
			}
			for _, dontWant := range tt.mustNotContain {
				if strings.Contains(got, dontWant) {
					t.Errorf("BuildRPMMacros() should not contain %q\nGot:\n%s", dontWant, got)
				}
			}
		})
	}
}

func TestValidateOptionSchema_MacroPrefix(t *testing.T) {
	schema := map[string]OptionKind{"releasever": OptionAny}
	
	// Test that dnf backend accepts macro.* options
	options := map[string]string{
		"releasever":      "8",
		"macro._dbpath":   "/var/lib/rpm",
		"macro._custom":   "value",
	}
	if err := ValidateOptionSchema("dnf", options, schema); err != nil {
		t.Errorf("dnf backend should accept macro.* options, got: %v", err)
	}

	// Test that zypper backend accepts macro.* options
	if err := ValidateOptionSchema("zypper", options, schema); err != nil {
		t.Errorf("zypper backend should accept macro.* options, got: %v", err)
	}

	// Test that non-RPM backends reject macro.* options
	if err := ValidateOptionSchema("apt", options, schema); err == nil {
		t.Error("apt backend should reject macro.* options")
	}
}
