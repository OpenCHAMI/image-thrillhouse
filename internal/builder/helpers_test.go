// Tests for the pure helpers in the builder package — extractExitCode,
// firstNonEmpty, absPath, resolveConfigPath. These don't need any container/
// backend mocks; they're string/error/path massagers exercising specific
// branches that have bitten us before (the "exit status N" string-parse
// fallback in extractExitCode in particular).
package builder

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeExitErr produces an *exec.ExitError by running a small command that
// fails. We don't try to fabricate one by hand because *exec.ExitError holds
// a *os.ProcessState that's awkward to construct manually, and the actual
// code path uses errors.As anyway — so the test wants a real one.
func fakeExitErr(t *testing.T, code int) error {
	t.Helper()
	cmd := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code))
	err := cmd.Run()
	if err == nil {
		t.Fatalf("sh -c 'exit %d' unexpectedly succeeded", code)
	}
	return err
}

func TestExtractExitCode_NilError(t *testing.T) {
	if got := extractExitCode(nil); got != 0 {
		t.Errorf("extractExitCode(nil) = %d, want 0", got)
	}
}

func TestExtractExitCode_RealExitError(t *testing.T) {
	for _, code := range []int{1, 8, 100, 107} {
		if got := extractExitCode(fakeExitErr(t, code)); got != code {
			t.Errorf("extractExitCode of `exit %d` = %d, want %d", code, got, code)
		}
	}
}

func TestExtractExitCode_WrappedExitError(t *testing.T) {
	// errors.As must walk the chain, so a wrapped *ExitError still resolves.
	wrapped := fmt.Errorf("buildah: %w", fakeExitErr(t, 42))
	if got := extractExitCode(wrapped); got != 42 {
		t.Errorf("extractExitCode of wrapped ExitError = %d, want 42", got)
	}
}

func TestExtractExitCode_StringFallback(t *testing.T) {
	// Buildah sometimes stringifies the ExitError before wrapping. The
	// fallback parser must pull the number out of "... exit status N ..."
	// payloads.
	tests := []struct {
		msg  string
		want int
	}{
		{"run [rpm --root /mnt -e foo]: exit status 1", 1},
		{"exit status 107 from zypper", 107},
		{"exit status 42: something else", 42},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			got := extractExitCode(errors.New(tt.msg))
			if got != tt.want {
				t.Errorf("extractExitCode(%q) = %d, want %d", tt.msg, got, tt.want)
			}
		})
	}
}

func TestExtractExitCode_Unparseable(t *testing.T) {
	// When neither ExitError nor "exit status N" is present, the contract
	// is to return -1 so the caller can surface the "could not determine
	// exit code" diagnostic. Test the contract directly.
	cases := []error{
		errors.New("dial tcp: connection refused"),
		errors.New("permission denied"),
		errors.New("exit status notanumber"),
	}
	for _, e := range cases {
		if got := extractExitCode(e); got != -1 {
			t.Errorf("extractExitCode(%q) = %d, want -1", e, got)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		a, b string
		want string
	}{
		{"hello", "world", "hello"},
		{"", "world", "world"},
		{"", "", ""},
		{"foo", "", "foo"},
	}
	for _, tt := range tests {
		if got := firstNonEmpty(tt.a, tt.b); got != tt.want {
			t.Errorf("firstNonEmpty(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestAbsPath_AlreadyAbsolute(t *testing.T) {
	got, err := absPath("/etc/passwd")
	if err != nil {
		t.Fatalf("absPath: %v", err)
	}
	if got != "/etc/passwd" {
		t.Errorf("absPath of absolute path should round-trip, got %q", got)
	}
}

func TestAbsPath_Relative(t *testing.T) {
	got, err := absPath("./somepath")
	if err != nil {
		t.Fatalf("absPath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("absPath did not produce absolute path: %q", got)
	}
	if !strings.HasSuffix(got, "somepath") {
		t.Errorf("absPath should preserve basename, got %q", got)
	}
}

func TestResolveConfigPath_AbsoluteUnchanged(t *testing.T) {
	b := &Builder{cfgPath: "/configs/foo.yaml"}
	got := b.resolveConfigPath("/etc/ansible/playbook.yaml")
	if got != "/etc/ansible/playbook.yaml" {
		t.Errorf("absolute paths must not be rewritten, got %q", got)
	}
}

func TestResolveConfigPath_RelativeUnchanged(t *testing.T) {
	// Relative paths are now returned as-is and resolve relative to CWD
	b := &Builder{cfgPath: "/configs/sub/foo.yaml"}
	got := b.resolveConfigPath("roles")
	want := "roles"
	if got != want {
		t.Errorf("resolveConfigPath = %q, want %q", got, want)
	}
}

func TestResolveConfigPath_AlwaysUsesCWD(t *testing.T) {
	// All relative paths resolve relative to CWD, regardless of cfgPath
	b := &Builder{cfgPath: ""}
	got := b.resolveConfigPath("playbook.yaml")
	if got != "playbook.yaml" {
		t.Errorf("relative path should be unchanged, got %q", got)
	}
}
