package labels

import (
	"strings"
	"testing"
	"time"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
)

// TestGenerate tests basic label generation
func TestGenerate(t *testing.T) {
	cfg := &config.Config{
		Meta: config.Meta{
			Name: "test-image",
			Tags: []string{"1.0"},
			From: "scratch",
		},
		Layer: config.Layer{
			Manager: config.Manager{
				Name: "dnf",
			},
		},
	}

	gen := New(cfg)
	labels := gen.Generate()

	// Check required labels exist
	requiredLabels := []string{
		"org.openchami.image.name",
		"org.openchami.image.type",
		"org.openchami.image.package-manager",
		"org.openchami.image.parent",
		"org.openchami.image.tags",
		"org.openchami.image.build-date",
	}

	for _, key := range requiredLabels {
		if _, ok := labels[key]; !ok {
			t.Errorf("Missing required label: %s", key)
		}
	}
}

// TestGenerateWithPackages tests label generation with packages
func TestGenerateWithPackages(t *testing.T) {
	cfg := &config.Config{
		Meta: config.Meta{
			Name: "test-image",
			Tags: []string{"1.0"},
			From: "scratch",
		},
		Layer: config.Layer{
			Manager: config.Manager{
				Name: "dnf",
			},
			Actions: config.Actions{
				Install: config.Install{
					Packages: []string{"kernel", "systemd", "vim"},
				},
			},
		},
	}

	gen := New(cfg)
	labels := gen.Generate()

	packagesLabel := labels["org.openchami.image.packages"]
	if packagesLabel != "kernel,systemd,vim" {
		t.Errorf("Expected 'kernel,systemd,vim', got '%s'", packagesLabel)
	}
}

// TestGenerateWithGroups tests label generation with package groups
func TestGenerateWithGroups(t *testing.T) {
	cfg := &config.Config{
		Meta: config.Meta{
			Name: "test-image",
			Tags: []string{"1.0"},
			From: "scratch",
		},
		Layer: config.Layer{
			Manager: config.Manager{
				Name: "dnf",
			},
			Actions: config.Actions{
				Install: config.Install{
					Groups: []string{"Minimal Install", "Development Tools"},
				},
			},
		},
	}

	gen := New(cfg)
	labels := gen.Generate()

	groupsLabel := labels["org.openchami.image.package-groups"]
	if groupsLabel != "Minimal Install,Development Tools" {
		t.Errorf("Expected 'Minimal Install,Development Tools', got '%s'", groupsLabel)
	}
}

// TestGenerateWithModules tests label generation with DNF modules
func TestGenerateWithModules(t *testing.T) {
	cfg := &config.Config{
		Meta: config.Meta{
			Name: "test-image",
			Tags: []string{"1.0"},
			From: "scratch",
		},
		Layer: config.Layer{
			Manager: config.Manager{
				Name: "dnf",
			},
			Actions: config.Actions{
				Install: config.Install{
					Modules: []config.Module{
						{Name: "nodejs", Stream: "18"},
						{Name: "ruby", Stream: "3.0"},
					},
				},
			},
		},
	}

	gen := New(cfg)
	labels := gen.Generate()

	modulesLabel := labels["org.openchami.image.modules"]
	if modulesLabel != "nodejs:18,ruby:3.0" {
		t.Errorf("Expected 'nodejs:18,ruby:3.0', got '%s'", modulesLabel)
	}
}

// TestGenerateWithRepos tests label generation with repositories
func TestGenerateWithRepos(t *testing.T) {
	cfg := &config.Config{
		Meta: config.Meta{
			Name: "test-image",
			Tags: []string{"1.0"},
			From: "scratch",
		},
		Layer: config.Layer{
			Manager: config.Manager{
				Name: "dnf",
			},
			Repos: []config.Repo{
				{Path: "/etc/yum.repos.d/baseos.repo"},
				{Path: "/etc/yum.repos.d/appstream.repo"},
				{Path: "/etc/yum.repos.d/epel.repo"},
			},
		},
	}

	gen := New(cfg)
	labels := gen.Generate()

	reposLabel := labels["org.openchami.image.repositories"]
	if reposLabel != "baseos,appstream,epel" {
		t.Errorf("Expected 'baseos,appstream,epel', got '%s'", reposLabel)
	}
}

// TestGenerateWithCustomLabels verifies that Meta.Labels values are applied
// on top of auto-generated labels and override colliding keys.
func TestGenerateWithCustomLabels(t *testing.T) {
	cfg := &config.Config{
		Meta: config.Meta{
			Name: "test-image",
			Tags: []string{"1.0"},
			From: "scratch",
			Labels: map[string]string{
				"maintainer":               "ops@example.com",
				"version":                  "2.0",
				"org.openchami.image.name": "custom-override", // collides with auto
			},
		},
		Layer: config.Layer{
			Manager: config.Manager{
				Name: "dnf",
			},
		},
	}

	gen := New(cfg)
	labels := gen.Generate()

	if labels["maintainer"] != "ops@example.com" {
		t.Errorf("Expected custom maintainer label, got %q", labels["maintainer"])
	}
	if labels["version"] != "2.0" {
		t.Errorf("Expected custom version label, got %q", labels["version"])
	}
	if labels["org.openchami.image.name"] != "custom-override" {
		t.Errorf("Expected custom label to override auto-generated label, got %q",
			labels["org.openchami.image.name"])
	}
}

// TestGenerateCustomLabelsEmpty confirms an absent Meta.Labels map leaves
// the auto-generated labels untouched (i.e. no nil-map panic on iteration).
func TestGenerateCustomLabelsEmpty(t *testing.T) {
	cfg := &config.Config{
		Meta: config.Meta{
			Name: "test-image",
			Tags: []string{"1.0"},
			From: "scratch",
		},
		Layer: config.Layer{
			Manager: config.Manager{Name: "dnf"},
		},
	}
	gen := New(cfg)
	labels := gen.Generate()
	if labels["org.openchami.image.name"] != "test-image" {
		t.Errorf("auto label clobbered: name = %q", labels["org.openchami.image.name"])
	}
}

// TestGenerateBuildDate tests build-date label format
func TestGenerateBuildDate(t *testing.T) {
	cfg := &config.Config{
		Meta: config.Meta{
			Name: "test-image",
			Tags: []string{"1.0"},
			From: "scratch",
		},
		Layer: config.Layer{
			Manager: config.Manager{
				Name: "dnf",
			},
		},
	}

	gen := New(cfg)
	labels := gen.Generate()

	buildDate := labels["org.openchami.image.build-date"]

	// Check it's a valid RFC3339 timestamp
	_, err := time.Parse(time.RFC3339, buildDate)
	if err != nil {
		t.Errorf("build-date is not valid RFC3339: %v", err)
	}
}

// TestGenerateLabelValues tests all automatic label values
func TestGenerateLabelValues(t *testing.T) {
	cfg := &config.Config{
		Meta: config.Meta{
			Name: "rocky-base",
			Tags: []string{"9.5"},
			From: "scratch",
		},
		Layer: config.Layer{
			Manager: config.Manager{
				Name: "dnf",
			},
		},
	}

	gen := New(cfg)
	labels := gen.Generate()

	tests := []struct {
		key      string
		expected string
	}{
		{"org.openchami.image.name", "rocky-base"},
		{"org.openchami.image.type", "base"},
		{"org.openchami.image.package-manager", "dnf"},
		{"org.openchami.image.parent", "scratch"},
		{"org.openchami.image.tags", "9.5"},
	}

	for _, tt := range tests {
		if got := labels[tt.key]; got != tt.expected {
			t.Errorf("Label %s = %v, want %v", tt.key, got, tt.expected)
		}
	}
}

// TestGenerateEmptyConfig tests label generation with minimal config
func TestGenerateEmptyConfig(t *testing.T) {
	cfg := &config.Config{
		Meta: config.Meta{
			Name: "minimal",
			Tags: []string{"1.0"},
			From: "scratch",
		},
		Layer: config.Layer{
			Manager: config.Manager{
				Name: "dnf",
			},
		},
	}

	gen := New(cfg)
	labels := gen.Generate()

	// Should still have basic labels
	if len(labels) < 6 {
		t.Errorf("Expected at least 6 basic labels, got %d", len(labels))
	}

	// Should NOT have optional labels
	optionalLabels := []string{
		"org.openchami.image.repositories",
		"org.openchami.image.packages",
		"org.openchami.image.package-groups",
		"org.openchami.image.modules",
	}

	for _, key := range optionalLabels {
		if _, ok := labels[key]; ok {
			t.Errorf("Should not have optional label %s with empty config", key)
		}
	}
}

// TestRepoNameExtraction tests repository name extraction from path
func TestRepoNameExtraction(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/etc/yum.repos.d/baseos.repo", "baseos"},
		{"/etc/yum.repos.d/rocky-appstream.repo", "rocky-appstream"},
		{"/etc/zypp/repos.d/opensuse.repo", "opensuse"},
		{"baseos.repo", "baseos"},
	}

	for _, tt := range tests {
		cfg := &config.Config{
			Meta: config.Meta{
				Name: "test",
				Tags: []string{"1.0"},
				From: "scratch",
			},
			Layer: config.Layer{
				Manager: config.Manager{Name: "dnf"},
				Repos:   []config.Repo{{Path: tt.path}},
			},
		}

		gen := New(cfg)
		labels := gen.Generate()
		reposLabel := labels["org.openchami.image.repositories"]

		if !strings.Contains(reposLabel, tt.expected) {
			t.Errorf("Expected repo name '%s' from path '%s', got '%s'",
				tt.expected, tt.path, reposLabel)
		}
	}
}
