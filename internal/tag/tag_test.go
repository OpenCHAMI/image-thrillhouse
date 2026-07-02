// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package tag

import (
	"os"
	"path/filepath"
	"testing"
)

// dirCfg writes a config file that references a layer.directories entry
// pointing at root, optionally with excludes and option fields.
func dirCfg(t *testing.T, cfgPath, srcRoot, optionLines string) {
	t.Helper()
	body := `meta:
  name: test
  tags: ["1"]
layer:
  manager:
    name: dnf
  directories:
    - path: /opt/app
      src: ` + srcRoot + `
` + optionLines
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
}

// minimalConfig is the smallest yaml that LoadConfigRaw will parse without
// complaint. We don't care about its contents — only that the file exists
// and parses — because hashing reads the bytes raw and then walks the
// (parsed) Files/Repos lists.
const minimalConfig = `meta:
  name: test
  tags: ["1"]
layer:
  manager:
    name: dnf
`

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestCompute_Deterministic(t *testing.T) {
	dir := t.TempDir()
	cfg := writeFile(t, dir, "layer.yaml", minimalConfig)

	layer := LayerInput{ConfigPath: cfg}
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
	if len(h1) != 32 {
		t.Errorf("expected 32-char md5 hex, got %d-char %q", len(h1), h1)
	}
}

func TestCompute_ConfigChangeChangesHash(t *testing.T) {
	dir := t.TempDir()
	cfgA := writeFile(t, dir, "a.yaml", minimalConfig)
	cfgB := writeFile(t, dir, "b.yaml", minimalConfig+"# trailing comment\n")

	hA, _ := Compute(LayerInput{ConfigPath: cfgA}, nil)
	hB, _ := Compute(LayerInput{ConfigPath: cfgB}, nil)
	if hA == hB {
		t.Errorf("expected different hashes for different configs, both = %s", hA)
	}
}

func TestCompute_VarFilesChangeHash(t *testing.T) {
	dir := t.TempDir()
	cfg := writeFile(t, dir, "layer.yaml", minimalConfig)
	vfA := writeFile(t, dir, "vars-a.yaml", "version: 1\n")
	vfB := writeFile(t, dir, "vars-b.yaml", "version: 2\n")

	noVars, _ := Compute(LayerInput{ConfigPath: cfg}, nil)
	withA, _ := Compute(LayerInput{ConfigPath: cfg, VarFiles: []string{vfA}}, nil)
	withB, _ := Compute(LayerInput{ConfigPath: cfg, VarFiles: []string{vfB}}, nil)

	if noVars == withA {
		t.Error("adding a var file should change the hash")
	}
	if withA == withB {
		t.Error("different var file contents should produce different hashes")
	}
}

func TestCompute_VarFileOrderIndependent(t *testing.T) {
	// hashLayer sorts var files internally to keep the hash stable regardless
	// of declaration order — confirm that contract holds.
	dir := t.TempDir()
	cfg := writeFile(t, dir, "layer.yaml", minimalConfig)
	vf1 := writeFile(t, dir, "1.yaml", "a: 1\n")
	vf2 := writeFile(t, dir, "2.yaml", "b: 2\n")

	hAsc, _ := Compute(LayerInput{ConfigPath: cfg, VarFiles: []string{vf1, vf2}}, nil)
	hDesc, _ := Compute(LayerInput{ConfigPath: cfg, VarFiles: []string{vf2, vf1}}, nil)
	if hAsc != hDesc {
		t.Errorf("var file order must not affect hash: %s vs %s", hAsc, hDesc)
	}
}

func TestCompute_AncestorOrderMatters(t *testing.T) {
	// Ancestors are hashed in slice order (parent-of-parent first), so
	// reversing them must change the hash. This is intentional: the tag
	// represents a fully-ordered lineage.
	dir := t.TempDir()
	leaf := writeFile(t, dir, "leaf.yaml", minimalConfig)
	a1 := writeFile(t, dir, "a1.yaml", minimalConfig+"# a1\n")
	a2 := writeFile(t, dir, "a2.yaml", minimalConfig+"# a2\n")

	leafIn := LayerInput{ConfigPath: leaf}
	forward, _ := Compute(leafIn, []LayerInput{{ConfigPath: a1}, {ConfigPath: a2}})
	reverse, _ := Compute(leafIn, []LayerInput{{ConfigPath: a2}, {ConfigPath: a1}})
	if forward == reverse {
		t.Errorf("ancestor order should affect hash: %s == %s", forward, reverse)
	}
}

func TestCompute_MissingConfigFile(t *testing.T) {
	_, err := Compute(LayerInput{ConfigPath: "/nonexistent/path.yaml"}, nil)
	if err == nil {
		t.Fatal("expected error for missing config file")
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

	cfg := filepath.Join(dir, "layer.yaml")
	dirCfg(t, cfg, srcRoot, "")

	h1, err := Compute(LayerInput{ConfigPath: cfg}, nil)
	if err != nil {
		t.Fatalf("Compute 1: %v", err)
	}

	if err := os.WriteFile(payload, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, err := Compute(LayerInput{ConfigPath: cfg}, nil)
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
	cfg := filepath.Join(dir, "layer.yaml")
	dirCfg(t, cfg, srcRoot, "")

	h1, _ := Compute(LayerInput{ConfigPath: cfg}, nil)

	if err := os.WriteFile(filepath.Join(srcRoot, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, _ := Compute(LayerInput{ConfigPath: cfg}, nil)
	if h1 == h2 {
		t.Error("adding a file under directories.src must change the hash")
	}

	if err := os.Remove(filepath.Join(srcRoot, "b.txt")); err != nil {
		t.Fatal(err)
	}
	h3, _ := Compute(LayerInput{ConfigPath: cfg}, nil)
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

	cfg := filepath.Join(dir, "layer.yaml")
	dirCfg(t, cfg, srcRoot, "      excludes:\n        - cache\n")

	h1, _ := Compute(LayerInput{ConfigPath: cfg}, nil)

	// Drop a file under the excluded subdir; hash must not move.
	if err := os.WriteFile(filepath.Join(srcRoot, "cache", "garbage.bin"), []byte("noise\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, _ := Compute(LayerInput{ConfigPath: cfg}, nil)
	if h1 != h2 {
		t.Errorf("excluded file must not change the hash: %s vs %s", h1, h2)
	}

	// Sanity check: a non-excluded edit DOES move the hash.
	if err := os.WriteFile(filepath.Join(srcRoot, "keep.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h3, _ := Compute(LayerInput{ConfigPath: cfg}, nil)
	if h1 == h3 {
		t.Error("edit to a non-excluded file should move the hash")
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
	cfgA := filepath.Join(dir, "preserve.yaml")
	dirCfg(t, cfgA, srcRoot, "")
	hA1, _ := Compute(LayerInput{ConfigPath: cfgA}, nil)
	if err := os.Chmod(file, 0o755); err != nil {
		t.Fatal(err)
	}
	hA2, _ := Compute(LayerInput{ConfigPath: cfgA}, nil)
	if hA1 == hA2 {
		t.Error("with no mode set, host chmod must move the hash (modes flow into the layer)")
	}

	// Case B: mode forced in YAML → host modes ignored → chmod must NOT move hash.
	cfgB := filepath.Join(dir, "forced.yaml")
	dirCfg(t, cfgB, srcRoot, "      mode: \"0644\"\n")
	hB1, _ := Compute(LayerInput{ConfigPath: cfgB}, nil)
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	hB2, _ := Compute(LayerInput{ConfigPath: cfgB}, nil)
	if hB1 != hB2 {
		t.Errorf("with forced mode, host chmod must NOT move the hash: %s vs %s", hB1, hB2)
	}
}

// TestCompute_DirectoryConfigOptionsAffectHash: the config-level option fields
// (mode, owner, preserve_ownership, contents_only, excludes) live in the YAML,
// so flipping them must change the hash via the config-bytes path. This isn't
// a feature of hashDirectory itself — it's an end-to-end guarantee — but it
// matters enough to lock down with a test.
func TestCompute_DirectoryConfigOptionsAffectHash(t *testing.T) {
	dir := t.TempDir()
	srcRoot := filepath.Join(dir, "tree")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcRoot, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgPlain := filepath.Join(dir, "plain.yaml")
	dirCfg(t, cfgPlain, srcRoot, "")
	cfgMode := filepath.Join(dir, "mode.yaml")
	dirCfg(t, cfgMode, srcRoot, "      mode: \"0644\"\n")
	cfgOwner := filepath.Join(dir, "owner.yaml")
	dirCfg(t, cfgOwner, srcRoot, "      owner: \"1000:1000\"\n")
	cfgSubdir := filepath.Join(dir, "subdir.yaml")
	dirCfg(t, cfgSubdir, srcRoot, "      contents_only: false\n")

	hPlain, _ := Compute(LayerInput{ConfigPath: cfgPlain}, nil)
	hMode, _ := Compute(LayerInput{ConfigPath: cfgMode}, nil)
	hOwner, _ := Compute(LayerInput{ConfigPath: cfgOwner}, nil)
	hSubdir, _ := Compute(LayerInput{ConfigPath: cfgSubdir}, nil)

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
	src := writeFile(t, dir, "payload.txt", "hello\n")
	cfgSrc := writeFile(t, dir, "with-src.yaml", `meta:
  name: test
  tags: ["1"]
layer:
  manager:
    name: dnf
  files:
    - path: /etc/foo
      src: `+src+`
`)
	cfgURL := writeFile(t, dir, "with-url.yaml", `meta:
  name: test
  tags: ["1"]
layer:
  manager:
    name: dnf
  files:
    - path: /etc/foo
      url: https://example.com/foo
`)

	hSrc, err := Compute(LayerInput{ConfigPath: cfgSrc}, nil)
	if err != nil {
		t.Fatalf("Compute src: %v", err)
	}
	hURL, err := Compute(LayerInput{ConfigPath: cfgURL}, nil)
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
	hSrc2, _ := Compute(LayerInput{ConfigPath: cfgSrc}, nil)
	if hSrc == hSrc2 {
		t.Error("src file content change should change hash")
	}
}
