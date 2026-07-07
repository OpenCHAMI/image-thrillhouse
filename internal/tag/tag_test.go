// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package tag

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
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

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
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
	src := writeFile(t, dir, "payload.txt", "hello\n")
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
