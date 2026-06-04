package manifest

import (
	"fmt"
	"os"
	"path/filepath"

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

// Load reads, parses, and validates a manifest file. Relative Config and
// VarFiles paths inside the manifest are resolved against the directory
// containing the manifest file, so a manifest authored as
//
//	layers:
//	  - name: base
//	    config: ../rocky/templates/rocky-base.yaml
//
// works the same whether image-build is invoked from the repo root, from a
// subdirectory, or from a container mount — no cwd assumption needed.
// Absolute paths are left as-is.
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

	resolveLayerPaths(&m, filepath.Dir(path))

	return &m, nil
}

// resolveLayerPaths rewrites every relative Config / VarFiles entry to be
// relative to manifestDir (the directory holding the manifest). Absolute
// paths pass through untouched. Done in-place because the parsed Manifest
// has no other consumers between Load and downstream callers.
func resolveLayerPaths(m *Manifest, manifestDir string) {
	for i := range m.Layers {
		l := &m.Layers[i]
		if l.Config != "" && !filepath.IsAbs(l.Config) {
			l.Config = filepath.Join(manifestDir, l.Config)
		}
		for j, vf := range l.VarFiles {
			if vf != "" && !filepath.IsAbs(vf) {
				l.VarFiles[j] = filepath.Join(manifestDir, vf)
			}
		}
	}
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
