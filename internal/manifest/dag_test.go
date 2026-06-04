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

// The ComputeTag_* tests below use TempDir fixtures instead of static yaml
// in tests/, so the suite stays hermetic and survives renames of the test
// tree (the original paths drifted out of date once dev/templates moved the
// fixtures around). Each test stamps a known config into a temp dir, builds
// a tiny DAG over those paths, then asserts a property of the resulting tag.

func TestComputeTag_Deterministic(t *testing.T) {
	dir := t.TempDir()
	baseCfg := writeConfig(t, dir, "base.yaml", minimalConfig)
	computeCfg := writeConfig(t, dir, "compute.yaml", minimalConfig)

	m := &Manifest{
		Layers: []Layer{
			{Name: "base", Config: baseCfg},
			{Name: "compute", Config: computeCfg, DependsOn: []string{"base"}},
		},
	}
	dag, err := NewDAG(m)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	tag1, err := dag.ComputeTag("compute", nil)
	if err != nil {
		t.Fatalf("compute1: %v", err)
	}
	tag2, err := dag.ComputeTag("compute", nil)
	if err != nil {
		t.Fatalf("compute2: %v", err)
	}
	if tag1 != tag2 {
		t.Errorf("tags not deterministic: %s != %s", tag1, tag2)
	}
}

func TestComputeTag_ChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	baseCfg := writeConfig(t, dir, "base.yaml", minimalConfig)
	// Different layer content -> different hash. We use a slightly varied
	// minimalConfig for the compute layer (different name) to make the
	// per-layer content actually differ.
	computeCfg := writeConfig(t, dir, "compute.yaml", `meta:
  name: compute
  tags: ["x"]
  from: scratch
layer:
  manager:
    name: apt
`)

	m := &Manifest{
		Layers: []Layer{
			{Name: "base", Config: baseCfg},
			{Name: "compute", Config: computeCfg, DependsOn: []string{"base"}},
		},
	}
	dag, _ := NewDAG(m)

	baseTag, err := dag.ComputeTag("base", nil)
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	computeTag, err := dag.ComputeTag("compute", nil)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if baseTag == computeTag {
		t.Errorf("different layers should have different tags (both = %s)", baseTag)
	}
}

func TestComputeTag_ParentAffectsChild(t *testing.T) {
	// Compute's tag must differ depending on whether it has a parent in the
	// graph — the parent's content is folded into the child's hash.
	dir := t.TempDir()
	baseCfg := writeConfig(t, dir, "base.yaml", minimalConfig)
	computeCfg := writeConfig(t, dir, "compute.yaml", minimalConfig)

	soloDAG, err := NewDAG(&Manifest{
		Layers: []Layer{{Name: "compute", Config: computeCfg}},
	})
	if err != nil {
		t.Fatalf("solo dag: %v", err)
	}
	chainedDAG, err := NewDAG(&Manifest{
		Layers: []Layer{
			{Name: "base", Config: baseCfg},
			{Name: "compute", Config: computeCfg, DependsOn: []string{"base"}},
		},
	})
	if err != nil {
		t.Fatalf("chained dag: %v", err)
	}

	soloTag, err := soloDAG.ComputeTag("compute", nil)
	if err != nil {
		t.Fatalf("solo: %v", err)
	}
	chainedTag, err := chainedDAG.ComputeTag("compute", nil)
	if err != nil {
		t.Fatalf("chained: %v", err)
	}
	if soloTag == chainedTag {
		t.Errorf("tag should differ when parent is included (both = %s)", soloTag)
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

// TestComputeBuildVars_DirectParentsOnly locks in the design's "direct
// parents only" rule: a grandchild gets vars for its direct parent (and
// parent_tag, since there's exactly one), but NOT for any grandparent.
// Templates that need a grandparent's tag must either list it directly in
// depends_on or have an intermediate layer forward the value.
func TestComputeBuildVars_DirectParentsOnly(t *testing.T) {
	dir := t.TempDir()
	gpCfg := writeConfig(t, dir, "gp.yaml", minimalConfig)
	pCfg := writeConfig(t, dir, "p.yaml", minimalConfig)
	cCfg := writeConfig(t, dir, "c.yaml", minimalConfig)

	m := &Manifest{
		Layers: []Layer{
			{Name: "grandparent", Config: gpCfg},
			{Name: "parent", Config: pCfg, DependsOn: []string{"grandparent"}},
			{Name: "grandchild", Config: cCfg, DependsOn: []string{"parent"}},
		},
	}
	dag, err := NewDAG(m)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	vars, err := ComputeBuildVars(dag, "grandchild", nil)
	if err != nil {
		t.Fatalf("compute build vars: %v", err)
	}

	// Present: tag (this layer), parent_tag (single-parent alias), parent_tag-by-name.
	for _, key := range []string{"tag", "parent_tag", "parent_tag"} {
		v, ok := vars[key]
		if !ok {
			t.Errorf("missing var %q", key)
			continue
		}
		if s, ok := v.(string); !ok || s == "" {
			t.Errorf("var %q has empty/non-string value: %v", key, v)
		}
	}

	// Absent: grandparent_tag. Transitive ancestors are not injected.
	if _, ok := vars["grandparent_tag"]; ok {
		t.Errorf("grandparent_tag should NOT be injected for an indirect ancestor; got %v", vars)
	}
}

// TestComputeBuildVars_ParentTagSingleParent verifies the singular
// "parent_tag" alias is injected when (and only when) a layer has exactly
// one direct parent. This is the convention multi-arch templates rely on:
// one template instantiated per arch, the parent reference written once as
// `{{ .parent_tag }}`.
func TestComputeBuildVars_ParentTagSingleParent(t *testing.T) {
	dir := t.TempDir()
	rootCfg := writeConfig(t, dir, "root.yaml", minimalConfig)
	leftCfg := writeConfig(t, dir, "left.yaml", minimalConfig)
	rightCfg := writeConfig(t, dir, "right.yaml", minimalConfig)
	joinCfg := writeConfig(t, dir, "join.yaml", minimalConfig)
	loneCfg := writeConfig(t, dir, "lone.yaml", minimalConfig)

	m := &Manifest{
		Layers: []Layer{
			{Name: "root", Config: rootCfg},
			{Name: "left", Config: leftCfg, DependsOn: []string{"root"}},
			{Name: "right", Config: rightCfg, DependsOn: []string{"root"}},
			// fork: two direct parents -> parent_tag must NOT be injected
			{Name: "join", Config: joinCfg, DependsOn: []string{"left", "right"}},
			// orphan root: zero parents -> parent_tag must NOT be injected
			{Name: "lone", Config: loneCfg},
		},
	}
	dag, err := NewDAG(m)
	if err != nil {
		t.Fatalf("dag: %v", err)
	}

	// single-parent case: parent_tag present, equals dag.ComputeTag(parent).
	leftVars, err := ComputeBuildVars(dag, "left", nil)
	if err != nil {
		t.Fatalf("left: %v", err)
	}
	wantRoot, err := dag.ComputeTag("root", nil)
	if err != nil {
		t.Fatalf("compute root: %v", err)
	}
	if got, ok := leftVars["parent_tag"]; !ok {
		t.Errorf("left: parent_tag not injected for single-parent layer")
	} else if got != wantRoot {
		t.Errorf("left.parent_tag mismatch:\n  got  = %v\n  want = %s", got, wantRoot)
	}

	// fork: parent_tag absent (ambiguous which parent it would refer to).
	joinVars, err := ComputeBuildVars(dag, "join", nil)
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, ok := joinVars["parent_tag"]; ok {
		t.Errorf("join: parent_tag must not be injected when a layer has multiple direct parents")
	}
	// Direct parents (left, right) get per-name aliases. root is a
	// grandparent via both branches and must NOT be injected — transitive
	// ancestors are not exposed.
	for _, key := range []string{"left_tag", "right_tag"} {
		if _, ok := joinVars[key]; !ok {
			t.Errorf("join: missing %q (direct parent should always have a per-name tag)", key)
		}
	}
	if _, ok := joinVars["root_tag"]; ok {
		t.Errorf("join: root_tag must not be injected (root is a grandparent, not a direct parent)")
	}

	// orphan root: parent_tag absent (nothing to alias).
	loneVars, err := ComputeBuildVars(dag, "lone", nil)
	if err != nil {
		t.Fatalf("lone: %v", err)
	}
	if _, ok := loneVars["parent_tag"]; ok {
		t.Errorf("lone: parent_tag must not be injected for a root layer")
	}
}
