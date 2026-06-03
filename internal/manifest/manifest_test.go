package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeManifestYAML stamps a manifest yaml into a temp dir and returns the
// path. Used by all Load tests below.
func writeManifestYAML(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return p
}

// TestLoad_Happy: a well-formed manifest round-trips through Load and
// exposes the expected layer/dep structure.
func TestLoad_Happy(t *testing.T) {
	p := writeManifestYAML(t, `layers:
  - name: base
    config: base.yaml
    depends_on: []
  - name: compute
    config: compute.yaml
    var_files:
      - vars.yaml
    depends_on: [base]
`)
	m, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(m.Layers))
	}
	if m.Layers[1].Name != "compute" || len(m.Layers[1].DependsOn) != 1 {
		t.Errorf("compute layer wiring wrong: %+v", m.Layers[1])
	}
	if len(m.Layers[1].VarFiles) != 1 || m.Layers[1].VarFiles[0] != "vars.yaml" {
		t.Errorf("var_files lost: %+v", m.Layers[1].VarFiles)
	}
}

// TestLoad_FileNotFound: an unreadable manifest must surface the read
// error rather than crash or return a zero-value Manifest.
func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/does/not/exist.yaml")
	if err == nil {
		t.Error("expected error from missing manifest, got nil")
	}
}

// TestLoad_InvalidYAML: malformed YAML surfaces a parse error.
func TestLoad_InvalidYAML(t *testing.T) {
	p := writeManifestYAML(t, "not: [valid: yaml")
	_, err := Load(p)
	if err == nil {
		t.Error("expected parse error, got nil")
	}
}

// TestLoad_EmptyLayers: validate() rejects a manifest with no layers — a
// build-all run with zero layers would silently do nothing.
func TestLoad_EmptyLayers(t *testing.T) {
	p := writeManifestYAML(t, "layers: []\n")
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for empty layers, got nil")
	}
	if !strings.Contains(err.Error(), "at least one layer") {
		t.Errorf("wrong error: %v", err)
	}
}

// TestLoad_LayerMissingName: every layer must have a name. Without this
// validate() check, a typo'd manifest could be loaded into a DAG that
// would crash later in unrelated code paths.
func TestLoad_LayerMissingName(t *testing.T) {
	p := writeManifestYAML(t, `layers:
  - config: base.yaml
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for unnamed layer, got nil")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("wrong error: %v", err)
	}
}

// TestLoad_LayerMissingConfig: every layer must have a config path.
func TestLoad_LayerMissingConfig(t *testing.T) {
	p := writeManifestYAML(t, `layers:
  - name: base
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for layer with no config, got nil")
	}
	if !strings.Contains(err.Error(), "config") {
		t.Errorf("wrong error: %v", err)
	}
}

// TestLoad_DuplicateNames: layer names are the DAG's keys, so duplicates
// must be rejected at parse time before NewDAG silently picks one.
func TestLoad_DuplicateNames(t *testing.T) {
	p := writeManifestYAML(t, `layers:
  - name: dup
    config: a.yaml
  - name: dup
    config: b.yaml
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for duplicate layer names, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("wrong error: %v", err)
	}
}

// TestLoad_UnknownDependency: depends_on must reference layers that exist
// in the same manifest. Otherwise NewDAG's Get(dep) would surface this
// later as a confusing runtime error.
func TestLoad_UnknownDependency(t *testing.T) {
	p := writeManifestYAML(t, `layers:
  - name: base
    config: base.yaml
    depends_on: [ghost]
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for unknown dependency, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should mention the missing dependency name: %v", err)
	}
}
