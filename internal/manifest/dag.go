// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package manifest

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
	"github.com/travisbcotton/image-thrillhouse/internal/tag"
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
	layer, ok := d.layers[name]
	if !ok {
		// Manifest.Load validates depends_on targets, but NewDAG is public —
		// a hand-built Manifest can reference a layer that doesn't exist.
		// Error instead of dereferencing nil.
		return fmt.Errorf("layer depends on unknown layer %s", name)
	}
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

// IsMultiArch returns true when the DAG holds any concrete arch-suffixed
// layer produced by manifest expansion. Used by the CLI to decide whether
// --arch is required.
func (d *DAG) IsMultiArch() bool {
	for _, l := range d.layers {
		if l.Arch != "" {
			return true
		}
	}
	return false
}

// LogicalNames returns every logical layer name in the manifest, sorted and
// de-duplicated across arch expansions — so a multi-arch manifest yields one
// entry per logical layer, not one per (layer, arch). Used by promote to walk
// the whole manifest when no single layer was named.
func (d *DAG) LogicalNames() []string {
	seen := make(map[string]bool, len(d.layers))
	out := make([]string, 0, len(d.layers))
	for _, l := range d.layers {
		name := l.LogicalName
		if name == "" {
			name = l.Name
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// ArchesFor returns the sorted list of arches a logical layer builds for,
// or empty when the DAG has no expansion. Used to produce helpful error
// messages when a user asks for an unsupported arch.
func (d *DAG) ArchesFor(logicalName string) []string {
	var out []string
	for _, l := range d.layers {
		if l.LogicalName == logicalName && l.Arch != "" {
			out = append(out, l.Arch)
		}
	}
	sort.Strings(out)
	return out
}

// Resolve maps a user-facing (logical layer, arch) pair to the concrete
// DAG layer name to build. The CLI calls this exactly once per invocation.
//
// Behaviour by mode:
//   - Single-arch manifest (no architectures block): `arch` must be empty
//     and layerName must be one of the concrete layers in the DAG.
//   - Multi-arch manifest: `arch` must be non-empty (CLI defaults it to
//     host arch before calling), layerName must be a logical name that
//     builds for `arch`. Passing an arch-suffixed name is rejected with
//     a message pointing at the correct --layer/--arch pair.
func (d *DAG) Resolve(layerName, arch string) (string, error) {
	if !d.IsMultiArch() {
		if arch != "" {
			return "", fmt.Errorf(
				"--arch %q given but manifest has no architectures block", arch,
			)
		}
		if _, err := d.Get(layerName); err != nil {
			return "", err
		}
		return layerName, nil
	}

	if arch == "" {
		return "", fmt.Errorf(
			"--arch is required: manifest declares an architectures block",
		)
	}

	// Reject arch-suffixed names: in multi-arch manifests every DAG entry
	// is a concrete expansion, so a Get hit here means the user passed one
	// of those concrete names as --layer. Redirect them to the
	// logical-name + --arch spelling.
	if l, err := d.Get(layerName); err == nil {
		return "", fmt.Errorf(
			"--layer %q is a concrete (arch-suffixed) name; "+
				"use --layer %q --arch %q instead",
			layerName, l.LogicalName, l.Arch,
		)
	}

	concrete := layerName + "-" + arch
	if _, err := d.Get(concrete); err != nil {
		arches := d.ArchesFor(layerName)
		if len(arches) == 0 {
			return "", fmt.Errorf("unknown layer %q in manifest", layerName)
		}
		return "", fmt.Errorf(
			"layer %q does not build for arch %q; available arches: %v",
			layerName, arch, arches,
		)
	}
	return concrete, nil
}

// ComputeTags renders and hashes layerName and every ancestor, bottom-up,
// and returns each computed tag keyed by concrete layer name.
//
// Each layer's hash covers its *rendered* config (so a var change only
// invalidates layers whose rendered output actually changes), the host-side
// content the rendered config references (src files, URLs, directories), and
// the tags of its direct parents — which chain the full ancestry Merkle-style.
// The render binds {{ .tag }} to tag.SelfTagSentinel (a layer's tag cannot
// feed its own hash); every other variable gets its real value.
//
// cliVars is the merged --var-file + --var map; it applies to every layer,
// layered on top of each layer's own var files.
func (d *DAG) ComputeTags(layerName string, cliVars map[string]interface{}) (map[string]string, error) {
	log := slog.With("component", "manifest")
	tags := make(map[string]string)

	var visit func(name string) error
	visit = func(name string) error {
		if _, done := tags[name]; done {
			return nil
		}
		layer, err := d.Get(name)
		if err != nil {
			return err
		}
		for _, dep := range layer.DependsOn {
			if err := visit(dep); err != nil {
				return err
			}
		}

		vars, err := d.renderVars(layer, cliVars, tags, tag.SelfTagSentinel)
		if err != nil {
			return err
		}
		rendered, err := config.RenderConfig(layer.Config, vars)
		if err != nil {
			return fmt.Errorf("render %s: %w", layer.Config, err)
		}
		cfg, err := config.ParseAndValidate(rendered, layer.Config)
		if err != nil {
			return fmt.Errorf("layer %s: %w", name, err)
		}

		parentTags := make([]string, len(layer.DependsOn))
		for i, dep := range layer.DependsOn {
			parentTags[i] = tags[dep]
		}

		t, err := tag.Compute(tag.LayerInput{
			ConfigPath: layer.Config,
			Rendered:   rendered,
			Cfg:        cfg,
		}, parentTags)
		if err != nil {
			return err
		}
		tags[name] = t
		log.Debug("computed tag", "layer", name, "tag", t)
		return nil
	}

	if err := visit(layerName); err != nil {
		return nil, err
	}
	return tags, nil
}

// RenderVars returns the complete variable map for rendering layerName's
// config in a manifest build: the layer's var files, cliVars on top, then the
// computed vars on top of both. It is identical to the map used for hashing
// except {{ .tag }} is bound to the layer's real computed tag instead of the
// sentinel — keeping the hashed render and the build render provably in sync.
//
// Computed vars:
//   - "tag" — this layer's computed hash.
//   - "<parent>_tag" — each DIRECT parent's hash, keyed by the parent's
//     logical name (hyphens → underscores), so templates stay arch-agnostic.
//   - "parent_tag" — alias for the one parent's hash when the layer has
//     exactly one direct parent. Not injected for forks or roots.
//   - "arch" — the architecture name, only for layers produced by multi-arch
//     expansion, so arch var files stay the source of truth otherwise.
//
// Transitive ancestor tags are intentionally NOT injected. A grandchild that
// needs its grandparent's tag must list the grandparent in depends_on, or
// have an intermediate layer forward the value.
func (d *DAG) RenderVars(layerName string, cliVars map[string]interface{}) (map[string]interface{}, error) {
	tags, err := d.ComputeTags(layerName, cliVars)
	if err != nil {
		return nil, err
	}
	layer, err := d.Get(layerName)
	if err != nil {
		return nil, err
	}
	slog.With("component", "manifest").Info("computed tag", "layer", layerName, "tag", tags[layerName])
	return d.renderVars(layer, cliVars, tags, tags[layerName])
}

// renderVars merges the full variable map for one layer: var files, then
// cliVars, then computed vars. selfTag is what {{ .tag }} binds to —
// tag.SelfTagSentinel on the hash path, the real tag on the build path.
// tags must already hold every direct parent of layer.
func (d *DAG) renderVars(layer *Layer, cliVars map[string]interface{}, tags map[string]string, selfTag string) (map[string]interface{}, error) {
	layerVars, err := config.LoadVars(layer.VarFiles, nil)
	if err != nil {
		return nil, fmt.Errorf("load var files for %s: %w", layer.Name, err)
	}
	merged := config.MergeVars(layerVars, cliVars)

	computed := map[string]interface{}{"tag": selfTag}
	if layer.Arch != "" {
		computed["arch"] = layer.Arch
	}
	for _, dep := range layer.DependsOn {
		computed[parentVarName(d, dep)+"_tag"] = tags[dep]
	}
	if len(layer.DependsOn) == 1 {
		computed["parent_tag"] = tags[layer.DependsOn[0]]
	}

	return config.MergeVars(merged, computed), nil
}

// parentVarName returns the underscore-normalised key used to build the
// "<parent>_tag" template variable. For manifests that went through arch
// expansion, this is the parent's logical (un-arch-suffixed) name so
// templates stay arch-agnostic. For manifests without an architectures
// block, LogicalName defaults to Name (populated in Manifest.expand) and
// the behaviour matches the pre-multi-arch design.
//
// If the DAG was constructed by hand (e.g. in tests) without ever going
// through Load, LogicalName may be empty; we fall back to the depName the
// caller passed in so callers don't have to know about the field.
func parentVarName(dag *DAG, depName string) string {
	logical := depName
	if p, err := dag.Get(depName); err == nil && p.LogicalName != "" {
		logical = p.LogicalName
	}
	return strings.ReplaceAll(logical, "-", "_")
}
