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
