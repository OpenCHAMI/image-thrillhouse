package manifest

import (
	"fmt"

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

func (d *DAG) ComputeTag(name string, globalVarFiles []string) (string, error) {
	ancestors, err := d.Ancestors(name)
	if err != nil {
		return "", err
	}

	layer, err := d.Get(name)
	if err != nil {
		return "", err
	}

	// convert ancestors to tag.LayerInput
	ancestorInputs := make([]tag.LayerInput, len(ancestors))
	for i, a := range ancestors {
		ancestorInputs[i] = tag.LayerInput{
			ConfigPath: a.Config,
			VarFiles:   append(globalVarFiles, a.VarFiles...),
		}
	}

	layerInput := tag.LayerInput{
		ConfigPath: layer.Config,
		VarFiles:   append(globalVarFiles, layer.VarFiles...),
	}

	return tag.Compute(layerInput, ancestorInputs)
}
