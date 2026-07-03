// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func LoadVars(varFiles []string, cliVars []string) (map[string]interface{}, error) {
	merged := make(map[string]interface{})

	for _, vf := range varFiles {
		if vf == "" {
			continue // skip empty strings
		}
		fileVars, err := loadVarFile(vf)
		if err != nil {
			return nil, fmt.Errorf("load var file %s: %w", vf, err)
		}
		merged = deepMerge(merged, fileVars)
	}

	for _, v := range cliVars {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid var %q: must be key=value", v)
		}
		setNestedVar(merged, parts[0], parts[1])
	}

	return merged, nil
}

// MergeVars returns a new map holding base with override merged on top
// (override wins on conflicts; nested maps merge recursively). Neither
// argument is mutated — both are deep-copied first, so callers can safely
// reuse the inputs (e.g. the same CLI vars map across multiple layers).
func MergeVars(base, override map[string]interface{}) map[string]interface{} {
	return deepMerge(deepCopyMap(base), deepCopyMap(override))
}

// deepCopyMap returns a copy of m where every nested map[string]interface{}
// is also copied. Non-map values (scalars, slices) are shared — deepMerge
// never mutates those, only map entries.
func deepCopyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		if vm, ok := v.(map[string]interface{}); ok {
			out[k] = deepCopyMap(vm)
		} else {
			out[k] = v
		}
	}
	return out
}

func loadVarFile(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read var file: %w", err)
	}

	vars := make(map[string]interface{})

	switch {
	case strings.HasSuffix(path, ".json"):
		if err := json.Unmarshal(data, &vars); err != nil {
			return nil, fmt.Errorf("parse json var file: %w", err)
		}
	case strings.HasSuffix(path, ".yaml"), strings.HasSuffix(path, ".yml"):
		if err := yaml.Unmarshal(data, &vars); err != nil {
			return nil, fmt.Errorf("parse yaml var file: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported var file format, use .yaml, .yml, or .json")
	}

	return vars, nil
}

// deepMerge merges src into dst IN PLACE and returns dst. Internal helper:
// callers that must not mutate their inputs go through MergeVars, which
// copies first.
func deepMerge(dst, src map[string]interface{}) map[string]interface{} {
	for k, v := range src {
		if existing, ok := dst[k]; ok {
			if dstMap, ok := existing.(map[string]interface{}); ok {
				if srcMap, ok := v.(map[string]interface{}); ok {
					dst[k] = deepMerge(dstMap, srcMap)
					continue
				}
			}
		}
		dst[k] = v
	}
	return dst
}

func setNestedVar(vars map[string]interface{}, key, value string) {
	parts := strings.SplitN(key, ".", 2)
	if len(parts) == 1 {
		vars[key] = value
		return
	}
	nested, ok := vars[parts[0]].(map[string]interface{})
	if !ok {
		nested = make(map[string]interface{})
		vars[parts[0]] = nested
	}
	setNestedVar(nested, parts[1], value)
}
