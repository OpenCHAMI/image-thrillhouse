package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func LoadVars(varFile string, cliVars []string) (map[string]interface{}, error) {
	merged := make(map[string]interface{})

	if varFile != "" {
		fileVars, err := loadVarFile(varFile)
		if err != nil {
			return nil, fmt.Errorf("load var file: %w", err)
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
