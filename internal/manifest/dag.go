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

// ComputeBuildVars returns the template variables that should be injected when
// rendering layerName's config in a manifest build. It always injects a "tag"
// key (the layer's computed hash) and a "<ancestor>_tag" key for every
// TRANSITIVE ancestor, so child templates can reference any layer up-chain by
// its deterministic tag — e.g. a grandchild can use
// `from: localhost/grandparent:{{ .grandparent_tag }}` without needing the
// intermediate layer to forward it.
//
// Ancestor variable names are derived by replacing "-" with "_" in the layer
// name and appending "_tag" (so "rocky-base" becomes "rocky_base_tag").
//
// globalVarFiles must be only the CLI-level globals; layer var files are
// applied inside ComputeTag.
func ComputeBuildVars(dag *DAG, layerName string, globalVarFiles []string) (map[string]interface{}, error) {
	vars := make(map[string]interface{})

	layerTag, err := dag.ComputeTag(layerName, globalVarFiles)
	if err != nil {
		return nil, fmt.Errorf("compute tag for %s: %w", layerName, err)
	}
	vars["tag"] = layerTag
	slog.Info("computed tag", "layer", layerName, "tag", layerTag)

	ancestors, err := dag.Ancestors(layerName)
	if err != nil {
		return nil, fmt.Errorf("ancestors: %w", err)
	}
	for _, ancestor := range ancestors {
		ancestorTag, err := dag.ComputeTag(ancestor.Name, globalVarFiles)
		if err != nil {
			return nil, fmt.Errorf("compute tag for ancestor %s: %w", ancestor.Name, err)
		}

		varName := strings.ReplaceAll(ancestor.Name, "-", "_") + "_tag"
		vars[varName] = ancestorTag
		slog.Debug("computed ancestor tag", "layer", ancestor.Name, "var", varName, "tag", ancestorTag)
	}

	return vars, nil
}
