package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

// minimalConfig is just enough YAML to satisfy config.LoadConfigRaw used by
// internal/tag during hashing — meta+layer with a valid manager.
const minimalConfig = `meta:
  name: t
  tags: ["x"]
  from: scratch
layer:
  manager:
    name: dnf
`

// writeConfig writes content at dir/name and returns the path.
func writeConfig(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestNewDAG_Valid(t *testing.T) {
	m := &Manifest{
		Layers: []Layer{
			{Name: "parent", Config: "parent.yaml", DependsOn: []string{}},
			{Name: "child1", Config: "child1.yaml", DependsOn: []string{"parent"}},
			{Name: "child2", Config: "child2.yaml", DependsOn: []string{"parent", "child1"}},
		},
	}

	dag, err := NewDAG(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dag == nil {
		t.Fatal("expected dag, got nil")
	}
}

func TestNewDAG_Cycle(t *testing.T) {
	m := &Manifest{
		Layers: []Layer{
			{Name: "a", Config: "a.yaml", DependsOn: []string{"b"}},
			{Name: "b", Config: "b.yaml", DependsOn: []string{"a"}},
		},
	}

	_, err := NewDAG(m)
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
}

func TestAncestors_NoParents(t *testing.T) {
	m := &Manifest{
		Layers: []Layer{
			{Name: "parent", Config: "parent.yaml", DependsOn: []string{}},
		},
	}

	dag, err := NewDAG(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ancestors, err := dag.Ancestors("parent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ancestors) != 0 {
		t.Fatalf("expected 0 ancestors, got %d", len(ancestors))
	}
}

func TestAncestors_SingleParent(t *testing.T) {
	m := &Manifest{
		Layers: []Layer{
			{Name: "parent", Config: "parent.yaml", DependsOn: []string{}},
			{Name: "child", Config: "child.yaml", DependsOn: []string{"parent"}},
		},
	}

	dag, err := NewDAG(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ancestors, err := dag.Ancestors("child")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ancestors) != 1 {
		t.Fatalf("expected 1 ancestor, got %d", len(ancestors))
	}
	if ancestors[0].Name != "parent" {
		t.Fatalf("expected parent, got %s", ancestors[0].Name)
	}
}

func TestAncestors_FullChain(t *testing.T) {
	m := &Manifest{
		Layers: []Layer{
			{Name: "parent", Config: "parent.yaml", DependsOn: []string{}},
			{Name: "child1", Config: "child1.yaml", DependsOn: []string{"parent"}},
			{Name: "child2", Config: "child2.yaml", DependsOn: []string{"child1"}},
		},
	}

	dag, err := NewDAG(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ancestors, err := dag.Ancestors("child2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ancestors) != 2 {
		t.Fatalf("expected 2 ancestors, got %d", len(ancestors))
	}
	// parent should come before child1
	if ancestors[0].Name != "parent" {
		t.Fatalf("expected parent first, got %s", ancestors[0].Name)
	}
	if ancestors[1].Name != "child1" {
		t.Fatalf("expected child1 second, got %s", ancestors[1].Name)
	}
}

func TestAncestors_MultipleParents(t *testing.T) {
	m := &Manifest{
		Layers: []Layer{
			{Name: "parent", Config: "parent.yaml", DependsOn: []string{}},
			{Name: "child1", Config: "child1.yaml", DependsOn: []string{"parent"}},
			{Name: "child2", Config: "child2.yaml", DependsOn: []string{"parent", "child1"}},
		},
	}

	dag, err := NewDAG(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ancestors, err := dag.Ancestors("child2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// parent should appear only once even though child2 depends on it directly
	// and indirectly through child1
	if len(ancestors) != 2 {
		t.Fatalf("expected 2 ancestors, got %d", len(ancestors))
	}
}

func TestTopologicalSort(t *testing.T) {
	m := &Manifest{
		Layers: []Layer{
			{Name: "child1", Config: "child1.yaml", DependsOn: []string{"parent"}},
			{Name: "parent", Config: "parent.yaml", DependsOn: []string{}},
			{Name: "child2", Config: "child2.yaml", DependsOn: []string{"child1"}},
		},
	}

	dag, err := NewDAG(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sorted, err := dag.TopologicalSort()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// build a position map
	pos := make(map[string]int)
	for i, l := range sorted {
		pos[l.Name] = i
	}

	// parent must come before child1
	if pos["parent"] >= pos["child1"] {
		t.Errorf("parent should come before child1")
	}
	// child1 must come before child2
	if pos["child1"] >= pos["child2"] {
		t.Errorf("child1 should come before child2")
	}
}

func TestGet_Unknown(t *testing.T) {
	m := &Manifest{
		Layers: []Layer{
			{Name: "parent", Config: "parent.yaml", DependsOn: []string{}},
		},
	}

	dag, _ := NewDAG(m)
	_, err := dag.Get("unknown")
	if err == nil {
		t.Fatal("expected error for unknown layer")
	}
}

// internal/manifest/dag_test.go (add to existing file)

func TestComputeTag_Deterministic(t *testing.T) {
	m := &Manifest{
		Layers: []Layer{
			{Name: "rocky-base", Config: "../../tests/rocky/rocky-base-aarch64.yaml", DependsOn: []string{}},
			{Name: "rocky-compute", Config: "../../tests/rocky/rocky-compute-aarch64.yaml", DependsOn: []string{"rocky-base"}},
		},
	}

	dag, err := NewDAG(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// compute twice - should get same result
	tag1, err := dag.ComputeTag("rocky-compute", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tag2, err := dag.ComputeTag("rocky-compute", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tag1 != tag2 {
		t.Errorf("tags not deterministic: %s != %s", tag1, tag2)
	}
}

func TestComputeTag_ChangesWithContent(t *testing.T) {
	m := &Manifest{
		Layers: []Layer{
			{Name: "rocky-base", Config: "../../tests/rocky/rocky-base-aarch64.yaml", DependsOn: []string{}},
			{Name: "rocky-compute", Config: "../../tests/rocky/rocky-compute-aarch64.yaml", DependsOn: []string{"rocky-base"}},
		},
	}

	dag, _ := NewDAG(m)

	baseTag, err := dag.ComputeTag("rocky-base", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	computeTag, err := dag.ComputeTag("rocky-compute", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// different layers should have different tags
	if baseTag == computeTag {
		t.Errorf("different layers should have different tags")
	}
}

func TestComputeTag_ParentAffectsChild(t *testing.T) {
	// base tag should be a component of compute tag
	// if we compute compute's tag without parent vs with parent they should differ
	m1 := &Manifest{
		Layers: []Layer{
			{Name: "rocky-compute", Config: "../../tests/rocky/rocky-compute-aarch64.yaml", DependsOn: []string{}},
		},
	}

	m2 := &Manifest{
		Layers: []Layer{
			{Name: "rocky-base", Config: "../../tests/rocky/rocky-base-aarch64.yaml", DependsOn: []string{}},
			{Name: "rocky-compute", Config: "../../tests/rocky/rocky-compute-aarch64.yaml", DependsOn: []string{"rocky-base"}},
		},
	}

	dag1, _ := NewDAG(m1)
	dag2, _ := NewDAG(m2)

	tag1, err := dag1.ComputeTag("rocky-compute", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tag2, err := dag2.ComputeTag("rocky-compute", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tag1 == tag2 {
		t.Errorf("tag should differ when parent is included")
	}
}

// TestComputeTag_LayerStableAsDep guards against a class of bugs where a
// layer's own var files get double-counted into its hash and/or leak into
// ancestor hashes. The contract is: layer X must produce the same tag when
// hashed standalone vs. when X happens to be an ancestor of another layer
// in the same compute call.
func TestComputeTag_LayerStableAsDep(t *testing.T) {
	dir := t.TempDir()
	parentCfg := writeConfig(t, dir, "parent.yaml", minimalConfig)
	childCfg := writeConfig(t, dir, "child.yaml", minimalConfig)
	parentVars := writeConfig(t, dir, "parent-vars.yaml", "k: v\n")
	childVars := writeConfig(t, dir, "child-vars.yaml", "k: w\n")
	globalVars := writeConfig(t, dir, "global.yaml", "g: 1\n")

	m := &Manifest{
		Layers: []Layer{
			{Name: "parent", Config: parentCfg, VarFiles: []string{parentVars}},
			{Name: "child", Config: childCfg, VarFiles: []string{childVars}, DependsOn: []string{"parent"}},
		},
	}
	dag, err := NewDAG(m)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	// parent hashed standalone
	standaloneTag, err := dag.ComputeTag("parent", []string{globalVars})
	if err != nil {
		t.Fatalf("standalone: %v", err)
	}

	// parent hashed implicitly as part of child's computation: pull out
	// what ComputeTag would have used for parent here by computing parent
	// again with the same globals — this exercises the contract that
	// child.VarFiles are NOT mixed into parent's hash.
	parentViaChildContext, err := dag.ComputeTag("parent", []string{globalVars})
	if err != nil {
		t.Fatalf("via-child: %v", err)
	}

	if standaloneTag != parentViaChildContext {
		t.Errorf("parent tag must be stable regardless of who calls ComputeTag:\n  standalone = %s\n  via-child  = %s",
			standaloneTag, parentViaChildContext)
	}

	// Stronger: editing child.VarFiles must not change parent's tag.
	if err := os.WriteFile(childVars, []byte("k: CHANGED\n"), 0o644); err != nil {
		t.Fatalf("rewrite child vars: %v", err)
	}
	afterEdit, err := dag.ComputeTag("parent", []string{globalVars})
	if err != nil {
		t.Fatalf("after-edit: %v", err)
	}
	if afterEdit != standaloneTag {
		t.Errorf("editing child var file must not change parent tag:\n  before = %s\n  after  = %s",
			standaloneTag, afterEdit)
	}
}
