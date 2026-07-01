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

// TestLoad_ExpandsArchitectures: with an architectures block, each logical
// layer expands into one concrete layer per declared arch. Concrete names
// are "<logical>-<arch>", VarFiles are arch-first then layer-specific, and
// depends_on gets rewritten to point at the same-arch parent.
func TestLoad_ExpandsArchitectures(t *testing.T) {
	p := writeManifestYAML(t, `architectures:
  - name: x86_64
    var_files: [x86.yaml]
  - name: aarch64
    var_files: [arm.yaml]

layers:
  - name: rocky-base
    config: base.yaml
  - name: rocky-compute
    config: compute.yaml
    var_files: [common.yaml]
    depends_on: [rocky-base]
`)
	m, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Layers) != 4 {
		t.Fatalf("expected 4 concrete layers (2 logical × 2 arches), got %d", len(m.Layers))
	}

	byName := make(map[string]Layer)
	for _, l := range m.Layers {
		byName[l.Name] = l
	}
	for _, want := range []string{"rocky-base-x86_64", "rocky-base-aarch64",
		"rocky-compute-x86_64", "rocky-compute-aarch64"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("missing concrete layer %q; got %v", want, m.Layers)
		}
	}

	// LogicalName + Arch propagate.
	got := byName["rocky-compute-aarch64"]
	if got.LogicalName != "rocky-compute" || got.Arch != "aarch64" {
		t.Errorf("logical/arch metadata wrong: %+v", got)
	}

	// depends_on rewritten to arch-suffixed parent.
	if len(got.DependsOn) != 1 || got.DependsOn[0] != "rocky-base-aarch64" {
		t.Errorf("depends_on not rewritten to same-arch parent: %+v", got.DependsOn)
	}

	// Arch var_files come first, then layer-specific var_files.
	if len(got.VarFiles) != 2 ||
		filepath.Base(got.VarFiles[0]) != "arm.yaml" ||
		filepath.Base(got.VarFiles[1]) != "common.yaml" {
		t.Errorf("var_files order wrong: %+v", got.VarFiles)
	}
}

// TestLoad_ArchesOptOut: a layer with `arches:` builds only for the listed
// subset. Concrete layers for other arches must NOT be produced.
func TestLoad_ArchesOptOut(t *testing.T) {
	p := writeManifestYAML(t, `architectures:
  - name: x86_64
  - name: aarch64

layers:
  - name: base
    config: base.yaml
  - name: x86only
    config: x.yaml
    arches: [x86_64]
    depends_on: [base]
`)
	m, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	names := make(map[string]bool)
	for _, l := range m.Layers {
		names[l.Name] = true
	}
	if !names["x86only-x86_64"] {
		t.Error("missing x86only-x86_64")
	}
	if names["x86only-aarch64"] {
		t.Error("x86only should not have an aarch64 expansion")
	}
}

// TestLoad_UnknownArch: `arches:` entries must be a subset of the declared
// architectures — a typo like `arch64` should error out at load time
// listing the valid options, not silently drop the layer.
func TestLoad_UnknownArch(t *testing.T) {
	p := writeManifestYAML(t, `architectures:
  - name: x86_64
  - name: aarch64

layers:
  - name: base
    config: base.yaml
    arches: [arch64]
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for unknown arch")
	}
	if !strings.Contains(err.Error(), "arch64") {
		t.Errorf("error should mention the offending arch name: %v", err)
	}
}

// TestLoad_ArchOnlyParent: a child that builds for arch A but whose parent
// does not is a load-time error — silent inference would surprise users.
// The message must name both layers and the offending arch so the fix is
// obvious.
func TestLoad_ArchOnlyParent(t *testing.T) {
	p := writeManifestYAML(t, `architectures:
  - name: x86_64
  - name: aarch64

layers:
  - name: base
    config: base.yaml
    arches: [x86_64]
  - name: compute
    config: compute.yaml
    depends_on: [base]
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for arch-only parent")
	}
	for _, want := range []string{"compute", "base", "aarch64"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

// TestLoad_ArchesWithoutArchitectures: `arches:` is only meaningful when
// the manifest has an architectures block. Using it in a plain (non-multi-
// arch) manifest is almost certainly a typo/misunderstanding — error out.
func TestLoad_ArchesWithoutArchitectures(t *testing.T) {
	p := writeManifestYAML(t, `layers:
  - name: base
    config: base.yaml
    arches: [x86_64]
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for arches without architectures block")
	}
}

// TestLoad_DuplicateArch: two architectures with the same name would
// silently overwrite one another during expansion — reject at load time.
func TestLoad_DuplicateArch(t *testing.T) {
	p := writeManifestYAML(t, `architectures:
  - name: x86_64
  - name: x86_64

layers:
  - name: base
    config: base.yaml
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for duplicate architecture name")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("wrong error: %v", err)
	}
}

// TestLoad_NoArchitectures_LogicalNameDefaults: when no architectures
// block is present, expansion is a no-op but LogicalName is still
// populated (defaulting to Name) so downstream code can uniformly read
// LogicalName without a nil-check dance.
func TestLoad_NoArchitectures_LogicalNameDefaults(t *testing.T) {
	p := writeManifestYAML(t, `layers:
  - name: base
    config: base.yaml
`)
	m, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Layers[0].LogicalName != "base" {
		t.Errorf("LogicalName should default to Name; got %q", m.Layers[0].LogicalName)
	}
	if m.Layers[0].Arch != "" {
		t.Errorf("Arch should be empty for non-expanded layer; got %q", m.Layers[0].Arch)
	}
}

// TestLoad_ArchVarFilesResolved: relative paths under architectures[].var_files
// must be resolved against the manifest directory too — expansion folds
// them into layer VarFiles which resolveLayerPaths then rewrites.
func TestLoad_ArchVarFilesResolved(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, "manifests")
	configsDir := filepath.Join(root, "configs")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"base.yaml", "x86.yaml"} {
		if err := os.WriteFile(filepath.Join(configsDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	manifestPath := filepath.Join(manifestDir, "m.yaml")
	body := `architectures:
  - name: x86_64
    var_files: [../configs/x86.yaml]

layers:
  - name: base
    config: ../configs/base.yaml
`
	if err := os.WriteFile(manifestPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(manifestPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := filepath.Join(configsDir, "x86.yaml")
	if got := m.Layers[0].VarFiles[0]; got != want {
		t.Errorf("arch var_file not resolved: got %q, want %q", got, want)
	}
}
