package tag

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/travisbcotton/image-build/internal/config"
)

type LayerInput struct {
	ConfigPath string
	VarFiles   []string
}

func Compute(layer LayerInput, ancestors []LayerInput) (string, error) {
	h := md5.New()

	// hash ancestors first in order
	for _, ancestor := range ancestors {
		if err := hashLayer(h, ancestor); err != nil {
			return "", fmt.Errorf("hash ancestor %s: %w", ancestor.ConfigPath, err)
		}
	}

	// hash this layer
	if err := hashLayer(h, layer); err != nil {
		return "", fmt.Errorf("hash layer %s: %w", layer.ConfigPath, err)
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func hashLayer(h io.Writer, layer LayerInput) error {
	// config file
	if err := hashFile(h, layer.ConfigPath); err != nil {
		return fmt.Errorf("hash config: %w", err)
	}

	// var files - sorted for determinism
	sorted := make([]string, len(layer.VarFiles))
	copy(sorted, layer.VarFiles)
	sort.Strings(sorted)
	for _, vf := range sorted {
		if err := hashFile(h, vf); err != nil {
			return fmt.Errorf("hash var file %s: %w", vf, err)
		}
	}

	// src files and URLs from config
	cfg, err := config.LoadConfig(layer.ConfigPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	for _, f := range cfg.Layer.Files {
		if f.Src != "" {
			if err := hashFile(h, f.Src); err != nil {
				return fmt.Errorf("hash src %s: %w", f.Src, err)
			}
		}
		if f.URL != "" {
			io.WriteString(h, f.URL)
		}
	}

	for _, r := range cfg.Layer.Repos {
		if r.Src != "" {
			if err := hashFile(h, r.Src); err != nil {
				return fmt.Errorf("hash repo src %s: %w", r.Src, err)
			}
		}
		if r.URL != "" {
			io.WriteString(h, r.URL)
		}
	}

	return nil
}

func hashFile(h io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(h, f)
	return err
}
