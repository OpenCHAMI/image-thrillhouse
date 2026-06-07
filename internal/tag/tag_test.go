package tag

import (
	"os"
	"path/filepath"
	"testing"
)

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
