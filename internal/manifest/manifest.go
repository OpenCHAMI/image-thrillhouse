// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package manifest

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Architecture declares one build target dimension that layers can be
// expanded across. When a manifest has one or more architectures declared,
// each layer is a *logical* template that expands into one concrete build
// node per arch it's opted in to.
type Architecture struct {
	Name     string   `yaml:"name"`
	VarFiles []string `yaml:"var_files"`
}

type Layer struct {
	Name      string   `yaml:"name"`
	Config    string   `yaml:"config"`
	VarFiles  []string `yaml:"var_files"`
	DependsOn []string `yaml:"depends_on"`

	// Arches, when non-empty, restricts a logical layer to a subset of the
	// manifest's declared architectures. Only meaningful when the manifest
	// has an architectures block. Default (empty) means "build for every
	// declared arch".
	Arches []string `yaml:"arches,omitempty"`

	// LogicalName and Arch are set by the expansion pass on concrete
	// (post-expansion) layers so downstream code can recover which logical
	// template a concrete layer came from and which arch it targets.
	// LogicalName == Name and Arch == "" for manifests without an
	// architectures block.
	LogicalName string `yaml:"-"`
	Arch        string `yaml:"-"`
}

type Manifest struct {
	Architectures []Architecture `yaml:"architectures"`
	Layers        []Layer        `yaml:"layers"`
}

// Load reads, parses, and validates a manifest file. Relative Config and
// VarFiles paths inside the manifest are resolved against the directory
// containing the manifest file, so a manifest authored as
//
//	layers:
//	  - name: base
//	    config: ../rocky/templates/rocky-base.yaml
//
// works the same whether image-thrillhouse is invoked from the repo root, from a
// subdirectory, or from a container mount — no cwd assumption needed.
// Absolute paths are left as-is.
//
// When the manifest declares an architectures block, Load also expands
// each logical layer into one concrete layer per arch it targets, with
// deps rewired to the same-arch parent expansion. Downstream code only
// ever sees concrete layers.
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

	if err := m.expand(); err != nil {
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

// validate performs structural checks that apply regardless of whether
// the manifest uses the architectures block. Runs on the parsed (pre-
// expansion) layer list, so depends_on entries here reference logical
// names when architectures is set.
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
	for _, l := range m.Layers {
		for _, dep := range l.DependsOn {
			if !names[dep] {
				return fmt.Errorf("layer %s depends on unknown layer %s", l.Name, dep)
			}
		}
	}
	return nil
}

// expand rewrites m.Layers into concrete (arch-suffixed) form when the
// manifest declares an architectures block. When no architectures are
// declared, expand is a no-op except for defaulting LogicalName = Name on
// each layer so downstream code can treat every layer uniformly.
//
// Rules enforced here:
//   - architecture names are non-empty and unique
//   - a layer's arches (if set) must be a subset of the declared archs
//   - a layer with `arches:` set is illegal when no architectures block is present
//   - for every child arch A, every parent listed in depends_on must also
//     build for A; otherwise the child's A-expansion would have no parent
//     to wire to. We error at load time with a message pointing at both
//     the child and parent arches so the fix is obvious.
func (m *Manifest) expand() error {
	if len(m.Architectures) == 0 {
		for i := range m.Layers {
			if len(m.Layers[i].Arches) > 0 {
				return fmt.Errorf("layer %s declares arches but manifest has no architectures block",
					m.Layers[i].Name)
			}
			m.Layers[i].LogicalName = m.Layers[i].Name
		}
		return nil
	}

	archByName := make(map[string]*Architecture, len(m.Architectures))
	declared := make([]string, 0, len(m.Architectures))
	for i := range m.Architectures {
		a := &m.Architectures[i]
		if a.Name == "" {
			return fmt.Errorf("architecture is missing a name")
		}
		if _, dup := archByName[a.Name]; dup {
			return fmt.Errorf("duplicate architecture: %s", a.Name)
		}
		archByName[a.Name] = a
		declared = append(declared, a.Name)
	}

	layerArches := make(map[string][]string, len(m.Layers))
	for _, l := range m.Layers {
		archesForLayer := l.Arches
		if len(archesForLayer) == 0 {
			archesForLayer = declared
		} else {
			for _, name := range archesForLayer {
				if _, ok := archByName[name]; !ok {
					return fmt.Errorf("layer %s: unknown arch %q (declared: %v)",
						l.Name, name, declared)
				}
			}
		}
		layerArches[l.Name] = archesForLayer
	}

	for _, l := range m.Layers {
		child := layerArches[l.Name]
		for _, dep := range l.DependsOn {
			parentSet := make(map[string]bool, len(layerArches[dep]))
			for _, a := range layerArches[dep] {
				parentSet[a] = true
			}
			for _, a := range child {
				if !parentSet[a] {
					return fmt.Errorf(
						"layer %s builds for arch %q but its parent %s does not; "+
							"add %q to %s.arches or remove it from %s.arches",
						l.Name, a, dep, a, dep, l.Name,
					)
				}
			}
		}
	}

	concrete := make([]Layer, 0, len(m.Layers)*len(m.Architectures))
	for _, l := range m.Layers {
		for _, archName := range layerArches[l.Name] {
			arch := archByName[archName]

			varFiles := make([]string, 0, len(arch.VarFiles)+len(l.VarFiles))
			varFiles = append(varFiles, arch.VarFiles...)
			varFiles = append(varFiles, l.VarFiles...)

			deps := make([]string, 0, len(l.DependsOn))
			for _, p := range l.DependsOn {
				deps = append(deps, p+"-"+archName)
			}

			concrete = append(concrete, Layer{
				Name:        l.Name + "-" + archName,
				Config:      l.Config,
				VarFiles:    varFiles,
				DependsOn:   deps,
				LogicalName: l.Name,
				Arch:        archName,
			})
		}
	}
	m.Layers = concrete
	return nil
}
