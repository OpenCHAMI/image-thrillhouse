// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package tag

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openchami/image-thrillhouse/internal/config"
)

// input parses rendered config YAML into a LayerInput ready for Compute.
func input(t *testing.T, rendered string) LayerInput {
	t.Helper()
	cfg, err := config.ParseAndValidate(rendered, "test.yaml")
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	return LayerInput{ConfigPath: "test.yaml", Rendered: rendered, Cfg: cfg}
}

// dirCfg returns a rendered config whose layer.directories entry points at
// srcRoot, optionally with extra option lines.
func dirCfg(srcRoot, optionLines string) string {
	return `meta:
  name: test
  tags: ["1"]
layer:
  manager:
    name: dnf
  directories:
    - path: /opt/app
      src: ` + srcRoot + `
` + optionLines
}

const minimalConfig = `meta:
  name: test
  tags: ["1"]
layer:
  manager:
    name: dnf
`

// writeFile writes content to path, creating parent directories as needed,
// and returns path.
func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestCompute_Deterministic(t *testing.T) {
	layer := input(t, minimalConfig)
	h1, err := Compute(layer, nil)
	if err != nil {
		t.Fatalf("Compute 1: %v", err)
	}
	h2, err := Compute(layer, nil)
	if err != nil {
		t.Fatalf("Compute 2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash not deterministic: %s vs %s", h1, h2)
	}
	if len(h1) != TagHexLen {
		t.Errorf("expected %d-char hex tag, got %d-char %q", TagHexLen, len(h1), h1)
	}
}

func TestCompute_RenderedChangeChangesHash(t *testing.T) {
	hA, _ := Compute(input(t, minimalConfig), nil)
	hB, _ := Compute(input(t, minimalConfig+"# trailing comment\n"), nil)
	if hA == hB {
		t.Errorf("expected different hashes for different rendered configs, both = %s", hA)
	}
}

func TestCompute_ParentTagsChangeHash(t *testing.T) {
	layer := input(t, minimalConfig)

	solo, _ := Compute(layer, nil)
	withParent, _ := Compute(layer, []string{"aaaa"})
	if solo == withParent {
		t.Error("adding a parent tag should change the hash")
	}

	otherParent, _ := Compute(layer, []string{"bbbb"})
	if withParent == otherParent {
		t.Error("different parent tags should produce different hashes")
	}
}

func TestCompute_ParentTagOrderMatters(t *testing.T) {
	// Parent tags are folded in DependsOn order — the tag represents a
	// fully-ordered lineage, so reversing them must change the hash.
	layer := input(t, minimalConfig)
	forward, _ := Compute(layer, []string{"aaaa", "bbbb"})
	reverse, _ := Compute(layer, []string{"bbbb", "aaaa"})
	if forward == reverse {
		t.Errorf("parent tag order should affect hash: %s == %s", forward, reverse)
	}
}

func TestCompute_MissingSrcFile(t *testing.T) {
	rendered := `meta:
  name: test
  tags: ["1"]
layer:
  manager:
    name: dnf
  files:
    - path: /etc/foo
      src: /nonexistent/payload.txt
`
	_, err := Compute(input(t, rendered), nil)
	if err == nil {
		t.Fatal("expected error for missing src file")
	}
}

// TestCompute_DirectoryContentChange: editing a file under a layer.directories
// src must change the layer hash. This is the core cache-correctness contract
// — without it, a stale image would happily be reused after a host-side edit.
func TestCompute_DirectoryContentChange(t *testing.T) {
	dir := t.TempDir()
	srcRoot := filepath.Join(dir, "tree")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(srcRoot, "config.txt")
	if err := os.WriteFile(payload, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	layer := input(t, dirCfg(srcRoot, ""))

	h1, err := Compute(layer, nil)
	if err != nil {
		t.Fatalf("Compute 1: %v", err)
	}

	if err := os.WriteFile(payload, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, err := Compute(layer, nil)
	if err != nil {
		t.Fatalf("Compute 2: %v", err)
	}
	if h1 == h2 {
		t.Error("edit to a file under directories.src must change the hash")
	}
}

// TestCompute_DirectoryAddRemoveFile: adding or removing a file under src must
// change the hash. Two configs that differ only by the presence of an extra
// file should not share a layer tag.
func TestCompute_DirectoryAddRemoveFile(t *testing.T) {
	dir := t.TempDir()
	srcRoot := filepath.Join(dir, "tree")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcRoot, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	layer := input(t, dirCfg(srcRoot, ""))

	h1, _ := Compute(layer, nil)

	if err := os.WriteFile(filepath.Join(srcRoot, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, _ := Compute(layer, nil)
	if h1 == h2 {
		t.Error("adding a file under directories.src must change the hash")
	}

	if err := os.Remove(filepath.Join(srcRoot, "b.txt")); err != nil {
		t.Fatal(err)
	}
	h3, _ := Compute(layer, nil)
	if h1 != h3 {
		t.Errorf("removing a previously-added file should restore the hash: %s vs %s", h1, h3)
	}
}

// TestCompute_DirectoryExcludesDropContent: an excluded file must NOT
// contribute to the hash. Verify by writing junk under an excluded subdir
// and confirming the hash matches an otherwise-identical tree without that
// file.
func TestCompute_DirectoryExcludesDropContent(t *testing.T) {
	dir := t.TempDir()
	srcRoot := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(srcRoot, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcRoot, "keep.txt"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	layer := input(t, dirCfg(srcRoot, "      excludes:\n        - cache\n"))

	h1, _ := Compute(layer, nil)

	// Drop a file under the excluded subdir; hash must not move.
	if err := os.WriteFile(filepath.Join(srcRoot, "cache", "garbage.bin"), []byte("noise\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, _ := Compute(layer, nil)
	if h1 != h2 {
		t.Errorf("excluded file must not change the hash: %s vs %s", h1, h2)
	}

	// Sanity check: a non-excluded edit DOES move the hash.
	if err := os.WriteFile(filepath.Join(srcRoot, "keep.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h3, _ := Compute(layer, nil)
	if h1 == h3 {
		t.Error("edit to a non-excluded file should move the hash")
	}
}

// TestCompute_DirectoryGitMetadataIgnored: a src that is a git working tree
// must not have its tag moved by git bookkeeping. The index and reflogs are
// rewritten by every clone, fetch, and checkout, so hashing them would mean a
// fresh CI clone never hits the cache.
func TestCompute_DirectoryGitMetadataIgnored(t *testing.T) {
	dir := t.TempDir()
	srcRoot := filepath.Join(dir, "tree")
	payload := writeFile(t, filepath.Join(srcRoot, "app.conf"), "v1\n")

	// A vendored repo nested under src, so the later nested-.git check adds
	// only the .git — its parent directories are entries in their own right
	// and would legitimately move the tag.
	writeFile(t, filepath.Join(srcRoot, "vendor", "lib", "lib.conf"), "lib\n")

	index := writeFile(t, filepath.Join(srcRoot, ".git", "index"), "stat cache A\n")
	reflog := writeFile(t, filepath.Join(srcRoot, ".git", "logs", "HEAD"), "0000 aaaa clone\n")
	writeFile(t, filepath.Join(srcRoot, ".git", "config"), "[remote \"origin\"]\n")

	layer := input(t, dirCfg(srcRoot, ""))
	h1 := mustCompute(t, layer)

	// Same content, second clone: index and reflogs differ, source does not.
	writeFile(t, index, "stat cache B\n")
	writeFile(t, reflog, "0000 aaaa clone\naaaa bbbb checkout: moving to main\n")

	if h2 := mustCompute(t, layer); h1 != h2 {
		t.Errorf("git bookkeeping churn must not change the tag: %s -> %s", h1, h2)
	}

	// The vendored repo's own .git is dropped at depth too.
	writeFile(t, filepath.Join(srcRoot, "vendor", "lib", ".git", "index"), "nested stat cache\n")
	if h3 := mustCompute(t, layer); h1 != h3 {
		t.Errorf("nested .git must not change the tag: %s -> %s", h1, h3)
	}

	// The tracked content itself still moves the tag.
	writeFile(t, payload, "v2\n")
	if h4 := mustCompute(t, layer); h1 == h4 {
		t.Errorf("content change under src must still change the tag, both = %s", h1)
	}
}

// TestCompute_DirectoryCheckoutShapeAgnostic is the submodule case: identical
// content must produce one tag no matter how the tree was obtained. .git takes
// three different shapes across those routes — a pointer file naming the
// submodule's path inside the superproject, a real directory for a standalone
// clone, and absent entirely for a release tarball — and hashing any of them
// would split one tree into three tags.
func TestCompute_DirectoryCheckoutShapeAgnostic(t *testing.T) {
	dir := t.TempDir()
	srcRoot := filepath.Join(dir, "tree")
	gitPath := filepath.Join(srcRoot, ".git")

	// The config text (and so srcRoot) is identical across all three shapes;
	// only the .git entry changes, which is the whole point of the test.
	layer := input(t, dirCfg(srcRoot, ""))

	// Shape 1: release tarball — no .git at all.
	writeFile(t, filepath.Join(srcRoot, "app.conf"), "v1\n")
	tarball := mustCompute(t, layer)

	// Shape 2: checked out as a submodule — .git is a pointer file whose
	// contents encode where the submodule sits in the superproject.
	writeFile(t, gitPath, "gitdir: ../../.git/modules/vendor/content\n")
	submodule := mustCompute(t, layer)
	if submodule != tarball {
		t.Errorf("submodule checkout tag %s != tarball tag %s", submodule, tarball)
	}

	// Moving the submodule within the superproject rewrites that pointer, and
	// must not move the tag either.
	writeFile(t, gitPath, "gitdir: ../.git/modules/content\n")
	if moved := mustCompute(t, layer); moved != tarball {
		t.Errorf("relocated submodule tag %s != tarball tag %s", moved, tarball)
	}

	// Shape 3: standalone clone — .git is a real directory.
	if err := os.Remove(gitPath); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(gitPath, "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(gitPath, "index"), "stat cache\n")
	writeFile(t, filepath.Join(gitPath, "objects", "pack", "pack-abc.pack"), "PACK\n")

	if clone := mustCompute(t, layer); clone != tarball {
		t.Errorf("standalone clone tag %s != tarball tag %s", clone, tarball)
	}
}

// TestCompute_DirectoryRealGitCheckoutsAgree drives the same contract as the
// two tests above against real git rather than hand-written fixtures, because
// what .git actually contains after a clone, a gc, or a submodule add is git's
// business and not something a fixture can promise to keep matching.
//
// src is relative here, so every shape below is hashed from an identical
// config text — only the checkout underneath it differs.
func TestCompute_DirectoryRealGitCheckoutsAgree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	commitAll := func(dir, msg string) {
		t.Helper()
		git(dir, "add", ".")
		git(dir, "-c", "user.email=test@example.invalid", "-c", "user.name=test", "commit", "-qm", msg)
	}

	origin := t.TempDir()
	git(origin, "init", "-q", ".")
	writeFile(t, filepath.Join(origin, "app.conf"), "v1\n")
	writeFile(t, filepath.Join(origin, ".gitignore"), "*.tmp\n") // content, not bookkeeping
	commitAll(origin, "init")

	cfg := dirCfg("./tree", "")

	// tagFrom computes the layer tag with parent as the working directory,
	// where the tree being hashed is checked out at ./tree.
	tagFrom := func(parent string) string {
		t.Helper()
		t.Chdir(parent)
		return computeOrFail(t, cfg)
	}

	cloneA, cloneB := t.TempDir(), t.TempDir()
	git(cloneA, "clone", "-q", origin, "tree")
	git(cloneB, "clone", "-q", origin, "tree")

	tagA := tagFrom(cloneA)
	if tagB := tagFrom(cloneB); tagA != tagB {
		t.Errorf("two clones of one commit produced different tags: %s vs %s", tagA, tagB)
	}

	// Everything that rewrites .git in place, with the source untouched.
	git(filepath.Join(cloneA, "tree"), "fetch", "-q", "origin")
	git(filepath.Join(cloneA, "tree"), "checkout", "-q", "-b", "scratch")
	git(filepath.Join(cloneA, "tree"), "gc", "-q")
	if after := tagFrom(cloneA); after != tagA {
		t.Errorf("fetch/checkout/gc moved the tag: %s -> %s", tagA, after)
	}

	// The submodule case: same content, .git is a pointer file instead.
	super := t.TempDir()
	git(super, "init", "-q", ".")
	writeFile(t, filepath.Join(super, "top.txt"), "top\n")
	commitAll(super, "init")
	git(super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", origin, "tree")
	if sub := tagFrom(super); sub != tagA {
		t.Errorf("submodule checkout tag %s != standalone clone tag %s", sub, tagA)
	}

	// A real source edit is still a rebuild.
	writeFile(t, filepath.Join(cloneA, "tree", "app.conf"), "v2\n")
	if edited := tagFrom(cloneA); edited == tagA {
		t.Errorf("source edit must still move the tag, both = %s", tagA)
	}
}

// TestCompute_DirectoryHostModeChange: when dir.mode is unset, buildah
// preserves host modes — so a host chmod must invalidate the cache. When
// dir.mode is set, all entries get that mode regardless of host, so a host
// chmod must NOT invalidate the cache.
func TestCompute_DirectoryHostModeChange(t *testing.T) {
	dir := t.TempDir()
	srcRoot := filepath.Join(dir, "tree")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(srcRoot, "script.sh")
	if err := os.WriteFile(file, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Case A: no mode set in YAML → host modes preserved → chmod must move hash.
	layerA := input(t, dirCfg(srcRoot, ""))
	hA1, _ := Compute(layerA, nil)
	if err := os.Chmod(file, 0o755); err != nil {
		t.Fatal(err)
	}
	hA2, _ := Compute(layerA, nil)
	if hA1 == hA2 {
		t.Error("with no mode set, host chmod must move the hash (modes flow into the layer)")
	}

	// Case B: mode forced in YAML → host modes ignored → chmod must NOT move hash.
	layerB := input(t, dirCfg(srcRoot, "      mode: \"0644\"\n"))
	hB1, _ := Compute(layerB, nil)
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	hB2, _ := Compute(layerB, nil)
	if hB1 != hB2 {
		t.Errorf("with forced mode, host chmod must NOT move the hash: %s vs %s", hB1, hB2)
	}
}

// TestCompute_DirectoryConfigOptionsAffectHash: the config-level option fields
// (mode, owner, preserve_ownership, contents_only, excludes) live in the
// rendered YAML, so flipping them must change the hash via the rendered-bytes
// path. This isn't a feature of hashDirectory itself — it's an end-to-end
// guarantee — but it matters enough to lock down with a test.
func TestCompute_DirectoryConfigOptionsAffectHash(t *testing.T) {
	dir := t.TempDir()
	srcRoot := filepath.Join(dir, "tree")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcRoot, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	hPlain, _ := Compute(input(t, dirCfg(srcRoot, "")), nil)
	hMode, _ := Compute(input(t, dirCfg(srcRoot, "      mode: \"0644\"\n")), nil)
	hOwner, _ := Compute(input(t, dirCfg(srcRoot, "      owner: \"1000:1000\"\n")), nil)
	hSubdir, _ := Compute(input(t, dirCfg(srcRoot, "      contents_only: false\n")), nil)

	hashes := map[string]string{
		"plain":  hPlain,
		"mode":   hMode,
		"owner":  hOwner,
		"subdir": hSubdir,
	}
	for n1, h1 := range hashes {
		for n2, h2 := range hashes {
			if n1 < n2 && h1 == h2 {
				t.Errorf("%s and %s configs should hash differently, both = %s", n1, n2, h1)
			}
		}
	}
}

func TestCompute_HashesSrcFilesAndURLs(t *testing.T) {
	// Configs with Files/Repos that reference src paths must include those
	// src bytes in the hash, and URLs must be included as strings.
	dir := t.TempDir()
	src := writeFile(t, filepath.Join(dir, "payload.txt"), "hello\n")
	layerSrc := input(t, `meta:
  name: test
  tags: ["1"]
layer:
  manager:
    name: dnf
  files:
    - path: /etc/foo
      src: `+src+`
`)
	layerURL := input(t, `meta:
  name: test
  tags: ["1"]
layer:
  manager:
    name: dnf
  files:
    - path: /etc/foo
      url: https://example.com/foo
`)

	hSrc, err := Compute(layerSrc, nil)
	if err != nil {
		t.Fatalf("Compute src: %v", err)
	}
	hURL, err := Compute(layerURL, nil)
	if err != nil {
		t.Fatalf("Compute url: %v", err)
	}
	if hSrc == hURL {
		t.Error("src-file and url-file configs should hash differently")
	}

	// Mutate the src file — hash should change.
	if err := os.WriteFile(src, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hSrc2, _ := Compute(layerSrc, nil)
	if hSrc == hSrc2 {
		t.Error("src file content change should change hash")
	}
}

// ansibleCfg returns a rendered config with a single ansible command running
// playbook, plus any extra ansible option lines (indented to sit under the
// ansible: mapping).
func ansibleCfg(playbook, optionLines string) string {
	return `meta:
  name: test
  tags: ["1"]
layer:
  manager:
    name: dnf
  actions:
    commands:
      - ansible:
          playbook: ` + playbook + `
          groups: [compute]
` + optionLines
}

// computeOrFail renders cfg and returns its tag.
func computeOrFail(t *testing.T, cfg string) string {
	t.Helper()
	return mustCompute(t, input(t, cfg))
}

// mustCompute is computeOrFail for tests that hold a LayerInput — those that
// recompute the same config against a mutating tree on disk.
func mustCompute(t *testing.T, layer LayerInput) string {
	t.Helper()
	h, err := Compute(layer, nil)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return h
}

// ansiblePlaybook writes a minimal valid playbook and returns its path.
func ansiblePlaybook(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "playbook.yaml")
	writeFile(t, path, "---\n- name: test\n  hosts: all\n  tasks:\n    - debug:\n        msg: hello\n")
	return path
}

// TestCompute_AnsiblePlaybookHashed verifies that the playbook file itself
// is hashed.
func TestCompute_AnsiblePlaybookHashed(t *testing.T) {
	dir := t.TempDir()
	playbook := ansiblePlaybook(t, dir)
	cfg := ansibleCfg(playbook, "")

	h1 := computeOrFail(t, cfg)

	writeFile(t, playbook, "---\n- name: test updated\n  hosts: all\n")

	if h2 := computeOrFail(t, cfg); h1 == h2 {
		t.Errorf("playbook content change should change hash, both = %s", h1)
	}
}

// TestCompute_AnsibleRolesHashed verifies that Ansible roles directory
// content is hashed, so changes to any role affect the layer tag.
func TestCompute_AnsibleRolesHashed(t *testing.T) {
	dir := t.TempDir()
	rolesDir := filepath.Join(dir, "roles")
	mainYaml := filepath.Join(rolesDir, "chrony", "tasks", "main.yaml")
	writeFile(t, mainYaml, "# chrony role v1\n")
	cfg := ansibleCfg(ansiblePlaybook(t, dir), "          roles: "+rolesDir+"\n")

	h1 := computeOrFail(t, cfg)

	writeFile(t, mainYaml, "# chrony role v2\n")

	if h2 := computeOrFail(t, cfg); h1 == h2 {
		t.Errorf("ansible roles content change should change hash, both = %s", h1)
	}
}

// TestCompute_AnsibleAllRolesHashed verifies that ALL roles in the roles
// directory are hashed, even ones the playbook never references — the
// deliberate over-hash that avoids reimplementing ansible's role resolution.
func TestCompute_AnsibleAllRolesHashed(t *testing.T) {
	dir := t.TempDir()
	rolesDir := filepath.Join(dir, "roles")
	writeFile(t, filepath.Join(rolesDir, "chrony", "tasks", "main.yaml"), "# chrony\n")
	ntpMain := filepath.Join(rolesDir, "ntp", "tasks", "main.yaml")
	writeFile(t, ntpMain, "# ntp\n")

	// The playbook references neither role.
	cfg := ansibleCfg(ansiblePlaybook(t, dir), "          roles: "+rolesDir+"\n")

	h1 := computeOrFail(t, cfg)

	writeFile(t, ntpMain, "# ntp modified\n")

	if h2 := computeOrFail(t, cfg); h1 == h2 {
		t.Errorf("any role change should change hash (entire roles dir is hashed), both = %s", h1)
	}
}

// TestCompute_AnsibleRolesVCSMetadataIgnored verifies that git bookkeeping
// inside a roles tree does not move the tag. A roles tree that is a plain git
// clone rewrites .git on every fetch, checkout, and gc; hashing that would
// invalidate the cache without any role content having changed.
func TestCompute_AnsibleRolesVCSMetadataIgnored(t *testing.T) {
	dir := t.TempDir()
	rolesDir := filepath.Join(dir, "roles")
	writeFile(t, filepath.Join(rolesDir, "chrony", "tasks", "main.yaml"), "# chrony\n")

	// .git at the root of the roles tree (a clone of a roles repo) and inside
	// a single role (a clone of one role).
	rootLog := filepath.Join(rolesDir, ".git", "logs", "HEAD")
	writeFile(t, rootLog, "0000 aaaa checkout: moving to main\n")
	roleLog := filepath.Join(rolesDir, "chrony", ".git", "logs", "HEAD")
	writeFile(t, roleLog, "0000 aaaa checkout: moving to main\n")

	cfg := ansibleCfg(ansiblePlaybook(t, dir), "          roles: "+rolesDir+"\n")

	h1 := computeOrFail(t, cfg)

	writeFile(t, rootLog, "0000 aaaa checkout: moving to main\naaaa bbbb checkout: moving to main\n")
	writeFile(t, roleLog, "0000 aaaa checkout: moving to main\naaaa bbbb checkout: moving to main\n")

	if h2 := computeOrFail(t, cfg); h1 != h2 {
		t.Errorf("git bookkeeping churn should not change hash: %s -> %s", h1, h2)
	}

	// Role content still moves the tag with the exclusion in place.
	writeFile(t, filepath.Join(rolesDir, "chrony", "tasks", "main.yaml"), "# chrony v2\n")

	if h3 := computeOrFail(t, cfg); h1 == h3 {
		t.Errorf("role content change should still change hash, both = %s", h1)
	}
}

// TestCompute_AnsibleSubmoduleRolesHashed verifies that a role vendored as a
// git submodule participates in the tag: its .git is a pointer file that the
// VCS exclusion drops, but its working tree is hashed like any other role.
func TestCompute_AnsibleSubmoduleRolesHashed(t *testing.T) {
	dir := t.TempDir()
	rolesDir := filepath.Join(dir, "roles")
	writeFile(t, filepath.Join(rolesDir, "chrony", ".git"), "gitdir: ../../.git/modules/roles/chrony\n")
	roleMain := filepath.Join(rolesDir, "chrony", "tasks", "main.yaml")
	writeFile(t, roleMain, "# chrony from submodule\n")

	cfg := ansibleCfg(ansiblePlaybook(t, dir), "          roles: "+rolesDir+"\n")

	h1 := computeOrFail(t, cfg)

	// A submodule bump changes the checked-out working tree.
	writeFile(t, roleMain, "# chrony from submodule, newer pin\n")

	if h2 := computeOrFail(t, cfg); h1 == h2 {
		t.Errorf("submodule role content change should change hash, both = %s", h1)
	}
}

// TestCompute_AnsibleDefaultRolesDir verifies that "roles" is used as the
// default directory when no roles path is specified.
func TestCompute_AnsibleDefaultRolesDir(t *testing.T) {
	dir := t.TempDir()
	roleMain := filepath.Join(dir, "roles", "chrony", "tasks", "main.yaml")
	writeFile(t, roleMain, "# chrony v1\n")
	cfg := ansibleCfg(ansiblePlaybook(t, dir), "")

	// Relative "roles" resolves against the working directory, so run from dir.
	t.Chdir(dir)

	h1 := computeOrFail(t, cfg)

	writeFile(t, roleMain, "# chrony v2\n")

	if h2 := computeOrFail(t, cfg); h1 == h2 {
		t.Errorf("role in default roles dir should affect hash, both = %s", h1)
	}
}

// TestCompute_AnsibleMissingRolesDir verifies that a missing roles directory
// is not an error — it's optional, and the builder skips the mount too.
func TestCompute_AnsibleMissingRolesDir(t *testing.T) {
	dir := t.TempDir()
	cfg := ansibleCfg(ansiblePlaybook(t, dir), "          roles: "+filepath.Join(dir, "nonexistent-roles")+"\n")

	if _, err := Compute(input(t, cfg), nil); err != nil {
		t.Errorf("missing roles directory should not cause error: %v", err)
	}
}

// TestCompute_AnsibleRolesNotADirectory verifies that a roles path pointing at
// a file is skipped rather than failing. The builder skips the bind mount in
// exactly this case, so the tag has to agree.
func TestCompute_AnsibleRolesNotADirectory(t *testing.T) {
	dir := t.TempDir()
	rolesFile := filepath.Join(dir, "roles")
	writeFile(t, rolesFile, "not a directory\n")
	cfg := ansibleCfg(ansiblePlaybook(t, dir), "          roles: "+rolesFile+"\n")

	if _, err := Compute(input(t, cfg), nil); err != nil {
		t.Errorf("roles path that is not a directory should not cause error: %v", err)
	}
}

// TestCompute_AnsibleInventoryHashed verifies that Ansible inventory
// directory content is hashed.
func TestCompute_AnsibleInventoryHashed(t *testing.T) {
	dir := t.TempDir()
	hostsFile := filepath.Join(dir, "inventory", "hosts")
	writeFile(t, hostsFile, "[compute]\nlocalhost\n")
	cfg := ansibleCfg(ansiblePlaybook(t, dir), "          inventory: "+filepath.Join(dir, "inventory")+"\n")

	h1 := computeOrFail(t, cfg)

	writeFile(t, hostsFile, "[compute]\nlocalhost\nnode1\n")

	if h2 := computeOrFail(t, cfg); h1 == h2 {
		t.Errorf("ansible inventory content change should change hash, both = %s", h1)
	}
}

// TestCompute_AnsibleInventoryFileHashed covers the single-file inventory
// branch, which takes a different code path from an inventory directory.
func TestCompute_AnsibleInventoryFileHashed(t *testing.T) {
	dir := t.TempDir()
	hostsFile := filepath.Join(dir, "hosts.ini")
	writeFile(t, hostsFile, "[compute]\nlocalhost\n")
	cfg := ansibleCfg(ansiblePlaybook(t, dir), "          inventory: "+hostsFile+"\n")

	h1 := computeOrFail(t, cfg)

	writeFile(t, hostsFile, "[compute]\nlocalhost\nnode1\n")

	if h2 := computeOrFail(t, cfg); h1 == h2 {
		t.Errorf("inventory file content change should change hash, both = %s", h1)
	}
}

// TestCompute_AnsibleMissingInventory verifies that a declared-but-absent
// inventory fails tag computation rather than hashing nothing. The builder
// refuses to run in this case, so a tag would describe a build that can't
// happen.
func TestCompute_AnsibleMissingInventory(t *testing.T) {
	dir := t.TempDir()
	cfg := ansibleCfg(ansiblePlaybook(t, dir), "          inventory: "+filepath.Join(dir, "nonexistent-inventory")+"\n")

	if _, err := Compute(input(t, cfg), nil); err == nil {
		t.Error("missing inventory should cause an error")
	}
}

// TestCompute_AnsibleMissingPlaybook verifies that an absent playbook fails
// tag computation with a message that explains path resolution.
func TestCompute_AnsibleMissingPlaybook(t *testing.T) {
	dir := t.TempDir()
	cfg := ansibleCfg(filepath.Join(dir, "nonexistent.yaml"), "")

	_, err := Compute(input(t, cfg), nil)
	if err == nil {
		t.Fatal("missing playbook should cause an error")
	}
	if !strings.Contains(err.Error(), "playbook file not found") {
		t.Errorf("error should name the missing playbook, got: %v", err)
	}
}

// TestCompute_AnsibleRelativeAndAbsolutePathsAgree verifies that referring to
// the same roles tree by relative and absolute path hashes the same content.
func TestCompute_AnsibleRelativeAndAbsolutePathsAgree(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "roles", "chrony", "tasks", "main.yaml"), "# chrony\n")
	playbook := ansiblePlaybook(t, dir)

	t.Chdir(dir)

	// Same tree, two spellings. The rendered config differs (the paths are in
	// the YAML), so the tags differ — but both must hash the tree, meaning a
	// role edit has to move both.
	relCfg := ansibleCfg(playbook, "          roles: roles\n")
	absCfg := ansibleCfg(playbook, "          roles: "+filepath.Join(dir, "roles")+"\n")

	relBefore, absBefore := computeOrFail(t, relCfg), computeOrFail(t, absCfg)

	writeFile(t, filepath.Join(dir, "roles", "chrony", "tasks", "main.yaml"), "# chrony v2\n")

	if relAfter := computeOrFail(t, relCfg); relBefore == relAfter {
		t.Error("role edit should change the relative-path tag")
	}
	if absAfter := computeOrFail(t, absCfg); absBefore == absAfter {
		t.Error("role edit should change the absolute-path tag")
	}
}

// TestCompute_AnsibleSharedRolesTreeHashedOnce verifies that several ansible
// commands sharing a roles tree still track its content. The tree is walked
// once per layer as an optimisation; that dedupe must not drop it from the
// hash.
func TestCompute_AnsibleSharedRolesTreeHashedOnce(t *testing.T) {
	dir := t.TempDir()
	roleMain := filepath.Join(dir, "roles", "chrony", "tasks", "main.yaml")
	writeFile(t, roleMain, "# chrony v1\n")
	first := ansiblePlaybook(t, dir)
	second := filepath.Join(dir, "second.yaml")
	writeFile(t, second, "---\n- name: second\n  hosts: all\n")

	rolesDir := filepath.Join(dir, "roles")
	cfg := `meta:
  name: test
  tags: ["1"]
layer:
  manager:
    name: dnf
  actions:
    commands:
      - ansible:
          playbook: ` + first + `
          roles: ` + rolesDir + `
          groups: [compute]
      - ansible:
          playbook: ` + second + `
          roles: ` + rolesDir + `
          groups: [compute]
`
	h1 := computeOrFail(t, cfg)

	writeFile(t, roleMain, "# chrony v2\n")

	if h2 := computeOrFail(t, cfg); h1 == h2 {
		t.Errorf("shared roles tree content change should change hash, both = %s", h1)
	}
}
