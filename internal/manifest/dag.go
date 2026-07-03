// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package manifest

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

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

// ComputeTag returns a deterministic md5 hash for the named layer.
//
// globalVarFiles is the set of var files that apply to *every* layer in the
// computation (typically the CLI-supplied --var-file). Each layer's own
// VarFiles are appended internally — callers must NOT pre-mix layer-specific
// var files into globalVarFiles, otherwise those files would be hashed twice
// for the layer they belong to and would leak into the hash of every ancestor.
//
// varOverrides are CLI-level "key=value" overrides (--var). Like
// globalVarFiles they apply to every layer, and they are folded into the
// hash so that builds differing only in --var get distinct tags.
func (d *DAG) ComputeTag(name string, globalVarFiles []string, varOverrides ...string) (string, error) {
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
			ConfigPath:   a.Config,
			VarFiles:     combineVarFiles(globalVarFiles, a.VarFiles),
			VarOverrides: varOverrides,
		}
	}

	layerInput := tag.LayerInput{
		ConfigPath:   layer.Config,
		VarFiles:     combineVarFiles(globalVarFiles, layer.VarFiles),
		VarOverrides: varOverrides,
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
//   - "<parent>_tag" — that parent's computed hash, keyed by the parent's
//     logical name (not its arch-suffixed concrete name). So a template
//     can reference `{{ .rocky_base_tag }}` and it resolves to the tag of
//     the same-arch parent whether the current build is x86_64 or aarch64.
//     Hyphens in the logical name are replaced by underscores.
//
// Injected when the layer has a single direct parent:
//   - "parent_tag" — alias for that one parent's hash. Not injected for
//     forks (>1 parent, ambiguous) or roots (0 parents, nothing to alias).
//
// Injected when the layer targets a specific architecture (i.e. was
// produced by manifest expansion):
//   - "arch" — the architecture name (e.g. "x86_64", "aarch64"). Not
//     injected for single-arch manifests where Arch is empty, so arch var
//     files remain the source of truth in that case.
//
// Note: transitive ancestor tags are intentionally NOT injected. A
// grandchild that needs its grandparent's tag must list the grandparent
// directly in depends_on, or have an intermediate layer forward the
// value.
//
// globalVarFiles must be only the CLI-level globals; layer var files are
// applied inside ComputeTag. varOverrides are CLI-level --var "key=value"
// overrides, forwarded to ComputeTag so they participate in every hash.
func ComputeBuildVars(dag *DAG, layerName string, globalVarFiles []string, varOverrides ...string) (map[string]interface{}, error) {
	log := slog.With("component", "manifest")
	layer, err := dag.Get(layerName)
	if err != nil {
		return nil, fmt.Errorf("get layer: %w", err)
	}

	vars := make(map[string]interface{})

	layerTag, err := dag.ComputeTag(layerName, globalVarFiles, varOverrides...)
	if err != nil {
		return nil, fmt.Errorf("compute tag for %s: %w", layerName, err)
	}
	vars["tag"] = layerTag
	log.Info("computed tag", "layer", layerName, "tag", layerTag)

	if layer.Arch != "" {
		vars["arch"] = layer.Arch
	}

	for _, depName := range layer.DependsOn {
		depTag, err := dag.ComputeTag(depName, globalVarFiles, varOverrides...)
		if err != nil {
			return nil, fmt.Errorf("compute tag for parent %s: %w", depName, err)
		}
		varName := parentVarName(dag, depName) + "_tag"
		vars[varName] = depTag
		log.Debug("computed parent tag", "layer", depName, "var", varName, "tag", depTag)
	}

	if len(layer.DependsOn) == 1 {
		vars["parent_tag"] = vars[parentVarName(dag, layer.DependsOn[0])+"_tag"]
	}

	return vars, nil
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
