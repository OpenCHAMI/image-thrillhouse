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
// exposes the expected layer/dep structure. Relative paths get resolved
// against the manifest's directory; we check the var_files entry ends
// with the original basename rather than asserting the full resolved
// path, which would couple this test to TempDir's layout.
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
	if len(m.Layers[1].VarFiles) != 1 || filepath.Base(m.Layers[1].VarFiles[0]) != "vars.yaml" {
		t.Errorf("var_files lost or not resolved sensibly: %+v", m.Layers[1].VarFiles)
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

// TestLoad_EmptyLayers: validate() rejects a manifest with no layers.
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

// TestLoad_ResolvesRelativePaths: paths in the manifest are resolved
// against the manifest's directory, so a manifest authored with paths
// like "../rocky/templates/foo.yaml" works regardless of the process's
// current working directory. Absolute paths pass through untouched —
// that's the escape hatch when the user really does want a fixed
// container-mount path like /tests/...
func TestLoad_ResolvesRelativePaths(t *testing.T) {
	// Layout:
	//   <root>/manifests/m.yaml
	//   <root>/configs/base.yaml
	//   <root>/configs/vars.yaml
	root := t.TempDir()
	manifestDir := filepath.Join(root, "manifests")
	configsDir := filepath.Join(root, "configs")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Real files at the target paths (Load doesn't read them, but a
	// downstream caller would, and making them exist lets us also assert
	// that the resolved path points at a real file).
	for _, name := range []string{"base.yaml", "vars.yaml"} {
		if err := os.WriteFile(filepath.Join(configsDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	manifestPath := filepath.Join(manifestDir, "m.yaml")
	body := `layers:
  - name: base
    config: ../configs/base.yaml
    var_files:
      - ../configs/vars.yaml
      - /absolute/passes/through.yaml
`
	if err := os.WriteFile(manifestPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Load(manifestPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	wantConfig := filepath.Join(configsDir, "base.yaml")
	if got := m.Layers[0].Config; got != wantConfig {
		t.Errorf("config path not resolved: got %q, want %q", got, wantConfig)
	}
	if _, err := os.Stat(m.Layers[0].Config); err != nil {
		t.Errorf("resolved config path is not readable: %v", err)
	}

	wantVarFile0 := filepath.Join(configsDir, "vars.yaml")
	if got := m.Layers[0].VarFiles[0]; got != wantVarFile0 {
		t.Errorf("var_file 0 not resolved: got %q, want %q", got, wantVarFile0)
	}
	if got := m.Layers[0].VarFiles[1]; got != "/absolute/passes/through.yaml" {
		t.Errorf("absolute var_file should pass through unchanged: got %q", got)
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
