// Package labels provides functionality for generating and managing container image labels.
// It automatically creates standard OpenCHAMI labels based on the build configuration
// and allows for custom labels to be added.
package labels

import (
	"fmt"
	"strings"
	"time"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
)

// Generator creates image labels based on configuration.
type Generator struct {
	cfg *config.Config
}

// New creates a new label generator with the provided configuration.
func New(cfg *config.Config) *Generator {
	return &Generator{cfg: cfg}
}

// Generate creates a map of all labels for the image.
// This includes both automatically generated labels and custom labels from the config.
//
// Automatic labels follow the org.openchami.image.* naming convention:
//   - org.openchami.image.name: Image name
//   - org.openchami.image.type: Layer type (currently always "base")
//   - org.openchami.image.package-manager: Package manager used
//   - org.openchami.image.parent: Parent image
//   - org.openchami.image.tags: Image tag(s)
//   - org.openchami.image.build-date: ISO 8601 timestamp
//   - org.openchami.image.repositories: Comma-separated repo aliases
//   - org.openchami.image.packages: Comma-separated package list
//   - org.openchami.image.package-groups: Comma-separated group list
//
// Custom labels from meta.labels override automatic labels if there are conflicts.
func (g *Generator) Generate() map[string]string {
	labels := make(map[string]string)

	// Basic metadata
	labels["org.openchami.image.name"] = g.cfg.Meta.Name
	labels["org.openchami.image.type"] = "base" // Currently only base layer type supported
	labels["org.openchami.image.package-manager"] = g.cfg.Layer.Manager.Name
	labels["org.openchami.image.parent"] = g.cfg.Meta.From
	if len(g.cfg.Meta.Tags) > 0 {
		labels["org.openchami.image.tags"] = strings.Join(g.cfg.Meta.Tags, ",")
	}

	// Build timestamp in ISO 8601 format
	labels["org.openchami.image.build-date"] = time.Now().UTC().Format(time.RFC3339)

	// Repository information
	if len(g.cfg.Layer.Repos) > 0 {
		var repoNames []string
		for _, repo := range g.cfg.Layer.Repos {
			// Extract repo name from path (e.g., /etc/yum.repos.d/baseos.repo -> baseos)
			parts := strings.Split(repo.Path, "/")
			if len(parts) > 0 {
				repoName := parts[len(parts)-1]
				repoName = strings.TrimSuffix(repoName, ".repo")
				repoNames = append(repoNames, repoName)
			}
		}
		if len(repoNames) > 0 {
			labels["org.openchami.image.repositories"] = strings.Join(repoNames, ",")
		}
	}

	// Package information
	if len(g.cfg.Layer.Actions.Install.Packages) > 0 {
		labels["org.openchami.image.packages"] = strings.Join(g.cfg.Layer.Actions.Install.Packages, ",")
	}

	// Package group information
	if len(g.cfg.Layer.Actions.Install.Groups) > 0 {
		labels["org.openchami.image.package-groups"] = strings.Join(g.cfg.Layer.Actions.Install.Groups, ",")
	}

	// Module information (DNF-specific)
	if len(g.cfg.Layer.Actions.Install.Modules) > 0 {
		var moduleStrings []string
		for _, mod := range g.cfg.Layer.Actions.Install.Modules {
			moduleStrings = append(moduleStrings, fmt.Sprintf("%s:%s", mod.Name, mod.Stream))
		}
		labels["org.openchami.image.modules"] = strings.Join(moduleStrings, ",")
	}

	// Custom labels from meta.labels are applied last so they override any
	// auto-generated label with the same key. This is the contract the
	// docstring promises and the one consumers expect — an operator setting
	// org.openchami.image.name in YAML wants that value, not the auto one.
	for k, v := range g.cfg.Meta.Labels {
		labels[k] = v
	}

	return labels
}
