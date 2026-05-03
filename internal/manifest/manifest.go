package manifest

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Layer struct {
	Name      string   `yaml:"name"`
	Config    string   `yaml:"config"`
	VarFiles  []string `yaml:"var_files"`
	DependsOn []string `yaml:"depends_on"`
}

type Manifest struct {
	Layers []Layer `yaml:"layers"`
}

func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	return &m, nil
}

func (m *Manifest) validate() error {
	if len(m.Layers) == 0 {
		return fmt.Errorf("manifest must have at least one layer")
	}
	names := make(map[string]bool)
	for _, l := range m.Layers {
		if l.Name == "" {
			return fmt.Errorf("all layers must have a name")
		}
		if l.Config == "" {
			return fmt.Errorf("layer %s must have a config", l.Name)
		}
		if names[l.Name] {
			return fmt.Errorf("duplicate layer name: %s", l.Name)
		}
		names[l.Name] = true
	}
	// validate depends_on references exist
	for _, l := range m.Layers {
		for _, dep := range l.DependsOn {
			if !names[dep] {
				return fmt.Errorf("layer %s depends on unknown layer %s", l.Name, dep)
			}
		}
	}
	return nil
}
