package manifest

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/travisbcotton/image-build/internal/tag"
)

type DAG struct {
	layers map[string]*Layer
}

func NewDAG(m *Manifest) (*DAG, error) {
	d := &DAG{layers: make(map[string]*Layer)}
	for i := range m.Layers {
		d.layers[m.Layers[i].Name] = &m.Layers[i]
	}
	// check for cycles
	for name := range d.layers {
		if err := d.checkCycle(name, make(map[string]bool), make(map[string]bool)); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (d *DAG) checkCycle(name string, visiting, visited map[string]bool) error {
	if visiting[name] {
		return fmt.Errorf("cycle detected involving layer %s", name)
	}
	if visited[name] {
		return nil
	}
	visiting[name] = true
	layer := d.layers[name]
	for _, dep := range layer.DependsOn {
		if err := d.checkCycle(dep, visiting, visited); err != nil {
			return err
		}
	}
	delete(visiting, name)
	visited[name] = true
	return nil
}

// Ancestors returns all ancestor layers in dependency order (root first)
// does not include the layer itself
func (d *DAG) Ancestors(name string) ([]*Layer, error) {
	layer, ok := d.layers[name]
	if !ok {
		return nil, fmt.Errorf("unknown layer: %s", name)
	}

	visited := make(map[string]bool)
	var result []*Layer

	for _, dep := range layer.DependsOn {
		if err := d.walk(dep, visited, &result); err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (d *DAG) walk(name string, visited map[string]bool, result *[]*Layer) error {
	if visited[name] {
		return nil
	}

	layer, ok := d.layers[name]
	if !ok {
		return fmt.Errorf("unknown layer: %s", name)
	}

	// visit dependencies first
	for _, dep := range layer.DependsOn {
		if err := d.walk(dep, visited, result); err != nil {
			return err
		}
	}

	visited[name] = true
	*result = append(*result, layer)
	return nil
}

// TopologicalSort returns all layers in build order
func (d *DAG) TopologicalSort() ([]*Layer, error) {
	visited := make(map[string]bool)
	var result []*Layer

	for name := range d.layers {
		if err := d.walk(name, visited, &result); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// Get returns a layer by name
func (d *DAG) Get(name string) (*Layer, error) {
	layer, ok := d.layers[name]
	if !ok {
		return nil, fmt.Errorf("unknown layer: %s", name)
	}
	return layer, nil
}

// ComputeTag returns a deterministic md5 hash for the named layer.
//
// globalVarFiles is the set of var files that apply to *every* layer in the
// computation (typically the CLI-supplied --var-file). Each layer's own
// VarFiles are appended internally — callers must NOT pre-mix layer-specific
// var files into globalVarFiles, otherwise those files would be hashed twice
// for the layer they belong to and would leak into the hash of every ancestor.
func (d *DAG) ComputeTag(name string, globalVarFiles []string) (string, error) {
	ancestors, err := d.Ancestors(name)
	if err != nil {
		return "", err
	}

	layer, err := d.Get(name)
	if err != nil {
		return "", err
	}

	ancestorInputs := make([]tag.LayerInput, len(ancestors))
	for i, a := range ancestors {
		ancestorInputs[i] = tag.LayerInput{
			ConfigPath: a.Config,
			VarFiles:   combineVarFiles(globalVarFiles, a.VarFiles),
		}
	}

	layerInput := tag.LayerInput{
		ConfigPath: layer.Config,
		VarFiles:   combineVarFiles(globalVarFiles, layer.VarFiles),
	}

	return tag.Compute(layerInput, ancestorInputs)
}

// combineVarFiles returns a fresh slice with globals followed by layer-specific
// var files. A fresh slice avoids aliasing the caller's globalVarFiles backing
// array (a naive `append(globals, ...)` can mutate it when there's spare cap).
func combineVarFiles(globals, layerSpecific []string) []string {
	out := make([]string, 0, len(globals)+len(layerSpecific))
	out = append(out, globals...)
	out = append(out, layerSpecific...)
	return out
}

// ComputeBuildVars returns the template variables that should be injected
// when rendering layerName's config in a manifest build.
//
// Always injected:
//   - "tag" — this layer's computed hash.
//
// Injected for every DIRECT parent (entries in layer.DependsOn):
//   - "<parent>_tag" — that parent's computed hash, with hyphens replaced
//     by underscores (so "rocky-base" becomes "rocky_base_tag"). A child
//     references its parents by name.
//
// Additionally injected when the layer has a single direct parent:
//   - "parent_tag" — alias for that one parent's hash. Lets a multi-arch
//     template stay layer-name-agnostic: a single rocky-compute.yaml can
//     be reused as rocky-compute-x86_64 and rocky-compute-aarch64 with
//     `from: localhost/rocky-base:{{ .parent_tag }}` instead of having to
//     hard-code which arch-specific parent name applies. Not injected for
//     forks (>1 parent, ambiguous) or roots (0 parents, nothing to alias).
//
// Note: transitive ancestor tags are intentionally NOT injected. A
// grandchild that needs its grandparent's tag must list the grandparent
// directly in depends_on, or have an intermediate layer forward the
// value. This matches the original design — "computes the current layer's
// tag and all parent tags" — and keeps the template-visible var surface
// proportional to what the manifest declares.
//
// globalVarFiles must be only the CLI-level globals; layer var files are
// applied inside ComputeTag.
func ComputeBuildVars(dag *DAG, layerName string, globalVarFiles []string) (map[string]interface{}, error) {
	layer, err := dag.Get(layerName)
	if err != nil {
		return nil, fmt.Errorf("get layer: %w", err)
	}

	vars := make(map[string]interface{})

	layerTag, err := dag.ComputeTag(layerName, globalVarFiles)
	if err != nil {
		return nil, fmt.Errorf("compute tag for %s: %w", layerName, err)
	}
	vars["tag"] = layerTag
	slog.Info("computed tag", "layer", layerName, "tag", layerTag)

	for _, depName := range layer.DependsOn {
		depTag, err := dag.ComputeTag(depName, globalVarFiles)
		if err != nil {
			return nil, fmt.Errorf("compute tag for parent %s: %w", depName, err)
		}
		varName := strings.ReplaceAll(depName, "-", "_") + "_tag"
		vars[varName] = depTag
		slog.Debug("computed parent tag", "layer", depName, "var", varName, "tag", depTag)
	}

	// Singular parent_tag alias when the layer has exactly one direct
	// parent. Skipped for forks (ambiguous) and roots (nothing to alias).
	if len(layer.DependsOn) == 1 {
		vars["parent_tag"] = vars[strings.ReplaceAll(layer.DependsOn[0], "-", "_")+"_tag"]
	}

	return vars, nil
}
