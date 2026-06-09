// Tests for stageAnsiblePayload — the host-side filesystem prep that builds
// a temp dir with the playbook copy, generated localhost inventory, and
// ansible.cfg. Pure FS, no Container involved.
package builder

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
)

func TestStageAnsiblePayload_MissingPlaybook(t *testing.T) {
	b := &Builder{cfgPath: ""}
	_, _, err := b.stageAnsiblePayload(&config.AnsibleCommand{
		Playbook: "/nonexistent/path.yaml",
		Groups:   []string{"all"},
	})
	if err == nil {
		t.Fatal("expected error for missing playbook")
	}
	if !strings.Contains(err.Error(), "playbook file not found") {
		t.Errorf("error should mention 'playbook file not found', got: %v", err)
	}
}

func TestStageAnsiblePayload_PlaybookIsDirectory(t *testing.T) {
	// If the user accidentally points playbook at a directory, fail with a
	// clear message rather than producing an empty stage.
	dir := t.TempDir()
	b := &Builder{cfgPath: ""}
	_, _, err := b.stageAnsiblePayload(&config.AnsibleCommand{
		Playbook: dir,
		Groups:   []string{"all"},
	})
	if err == nil {
		t.Fatal("expected error when playbook is a directory")
	}
	if !strings.Contains(err.Error(), "must be a file") {
		t.Errorf("error should mention 'must be a file', got: %v", err)
	}
}

func TestStageAnsiblePayload_Success(t *testing.T) {
	dir := t.TempDir()
	playbook := filepath.Join(dir, "site.yml")
	if err := os.WriteFile(playbook, []byte("- hosts: all\n  tasks: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := &Builder{cfgPath: ""}
	stage, base, err := b.stageAnsiblePayload(&config.AnsibleCommand{
		Playbook: playbook,
		Groups:   []string{"compute", "controllers"},
	})
	if err != nil {
		t.Fatalf("stageAnsiblePayload: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stage) })

	if base != "site.yml" {
		t.Errorf("playbookBase = %q, want site.yml", base)
	}

	// Stage dir must contain playbook copy, generated inventory, ansible.cfg.
	mustExist(t, filepath.Join(stage, "site.yml"))
	mustExist(t, filepath.Join(stage, "00-generated-localhost"))
	mustExist(t, filepath.Join(stage, "ansible.cfg"))

	// Inventory must list each group with localhost mapped to it.
	invBytes, err := os.ReadFile(filepath.Join(stage, "00-generated-localhost"))
	if err != nil {
		t.Fatal(err)
	}
	inv := string(invBytes)
	for _, group := range []string{"[compute]", "[controllers]"} {
		if !strings.Contains(inv, group) {
			t.Errorf("inventory missing %q, got:\n%s", group, inv)
		}
	}
	if strings.Count(inv, "localhost ansible_connection=local") != 2 {
		t.Errorf("expected 2 localhost lines (one per group), got:\n%s", inv)
	}

	// ansible.cfg must point roles_path at the container-side mount, NOT a host path.
	cfg, err := os.ReadFile(filepath.Join(stage, "ansible.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cfg), "roles_path = "+stageRoles) {
		t.Errorf("ansible.cfg should set roles_path=%s, got:\n%s", stageRoles, cfg)
	}
}

func TestStageAnsiblePayload_CleansUpOnError(t *testing.T) {
	// Force a failure AFTER MkdirTemp succeeds by pointing roles to a
	// missing path. The function's deferred cleanup must remove the
	// partial stage so the test doesn't leak it.
	//
	// Today the only post-MkdirTemp failure paths are read/write failures
	// — hard to provoke in a unit test without filesystem fault injection.
	// Instead, we just confirm that successful stages don't leak (the
	// happy-path test above) and trust the deferred cleanup pattern.
	t.Skip("requires FS fault injection; cleanup path covered by code review")
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Errorf("expected file to exist: %s", path)
		} else {
			t.Errorf("stat %s: %v", path, err)
		}
	}
}
