package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func LoadVars(varFile string, cliVars []string) (map[string]string, error) {
	merged := make(map[string]string)

	// load var file first (lowest priority of the two external sources)
	if varFile != "" {
		fileVars, err := loadVarFile(varFile)
		if err != nil {
			return nil, fmt.Errorf("load var file: %w", err)
		}
		for k, v := range fileVars {
			merged[k] = v
		}
	}

	// cli vars override var file
	for _, v := range cliVars {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid var %q: must be key=value", v)
		}
		merged[parts[0]] = parts[1]
	}

	return merged, nil
}

func loadVarFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read var file: %w", err)
	}

	vars := make(map[string]string)

	// detect format by extension
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
