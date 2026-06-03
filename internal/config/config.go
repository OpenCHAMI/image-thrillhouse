// Package config provides structures and functions for parsing and validating
// image-build YAML configuration files.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level structure for an image-build configuration file.
// It contains three main sections:
//   - Meta: Image metadata (name, tag, base image)
//   - Layer: Build instructions (package manager, repos, files, actions)
//   - Publish: Publishing destinations (local, squashfs, registry, s3)
type Config struct {
	Meta    Meta      `yaml:"meta"`    // Image metadata and base configuration
	Layer   Layer     `yaml:"layer"`   // Layer build instructions
	Publish []Publish `yaml:"publish"` // Publishing destinations (optional)
}

// Meta contains metadata about the image being built.
// This includes the image name, tag, and the base image to build from.
type Meta struct {
	Name          string   `yaml:"name"`
	From          string   `yaml:"from"`
	FromTLSVerify *bool    `yaml:"from-tls-verify"`
	Tags          []string `yaml:"tags"`
}

// Layer defines how to build the image layer.
// It specifies the package manager, repositories, files, and actions to perform.
type Layer struct {
	Manager Manager  `yaml:"manager"`  // Package manager configuration
	Repos   []Repo   `yaml:"repos"`    // Repository configurations
	Files   []File   `yaml:"files"`    // Files to add to the image
	Actions Actions  `yaml:"actions"`  // Installation and command actions
	OpenSCAP *OpenSCAP `yaml:"openscap"` // Optional: OpenSCAP security scanning configuration
}

// Manager specifies the package manager to use and its configuration.
type Manager struct {
	Name    string            `yaml:"name"`    // Package manager: dnf, zypper, apt, mmdebstrap
	Config  string            `yaml:"config"`  // Optional: package manager config file content (e.g., dnf.conf)
	Options map[string]string `yaml:"options"` // Optional: backend-specific options
}

// File represents a file to add to the image.
// Exactly one of Content, Src, or URL must be specified.
type File struct {
	Path    string `yaml:"path"`    // Destination path in the image (required)
	Content string `yaml:"content"` // Inline file content
	Src     string `yaml:"src"`     // Source file path on host
	URL     string `yaml:"url"`     // URL to download file from
	Mode    string `yaml:"mode"`    // Optional: File permissions mode (e.g., "0755", "0644")
}

// Repo represents a package repository configuration.
// Exactly one of Content, Src, or URL must be specified.
type Repo struct {
	Path    string `yaml:"path"`    // Destination path in the image (required)
	Content string `yaml:"content"` // Inline repo file content
	URL     string `yaml:"url"`     // URL to download repo file from
	Src     string `yaml:"src"`     // Source repo file path on host
	GPGKey  string `yaml:"gpg"`     // Optional: URL to GPG key for repository signing verification
}

// Actions defines what to install and what commands to run during the build.
type Actions struct {
	Install  Install   `yaml:"install"`  // Package installation configuration
	Commands []Command `yaml:"commands"` // Commands to run in the container
}

// Install specifies packages, groups, and modules to install.
// Not all package managers support all options (e.g., zypper doesn't support groups).
type Install struct {
	Packages       []string `yaml:"packages"`        // Individual packages to install
	Groups         []string `yaml:"groups"`          // Package groups to install (DNF only)
	Modules        []Module `yaml:"modules"`         // DNF modules to enable/install (DNF only)
	RemovePackages []string `yaml:"remove_packages"` // Packages to remove after installation
}

// Module represents a DNF module operation.
// DNF modules allow installing specific versions of software stacks.
type Module struct {
	Name   string `yaml:"name"`   // Module name (e.g., "nodejs")
	Stream string `yaml:"stream"` // Module stream/version (e.g., "18")
	Action string `yaml:"action"` // Action: "enable", "install", "disable"
}

// Command represents a command to run in the container.
// Exactly one of Run or Script must be specified.
type Command struct {
	Run    string `yaml:"run"`    // Simple command to run (e.g., "systemctl enable service")
	Script string `yaml:"script"` // Multi-line shell script to run
}

// Publish defines where to publish the built image.
// Multiple publishers can be specified to publish to multiple destinations.
type Publish struct {
	Type      string `yaml:"type"`
	URL       string `yaml:"url"`
	Bucket    string `yaml:"bucket"`
	Prefix    string `yaml:"prefix"`
	Path      string `yaml:"path"`
	TLSVerify *bool  `yaml:"tls-verify"`
	Endpoint  string `yaml:"endpoint"`
	Format    string `yaml:"format"`
}

// Used for switch-case so I can make things easier add in the future
type CommandType int

const (
	CommandRun    CommandType = iota // Simple command (Run field)
	CommandScript                    // Multi-line script (Script field)
)

// Type returns the CommandType for this command.
// It determines whether to execute the Run field or the Script field.
func (c *Command) Type() CommandType {
	if c.Script != "" {
		return CommandScript
	}
	return CommandRun
}

// LoadConfig reads and parses a YAML configuration file from the given path.
// It also validates the configuration structure and required fields.
//
// Returns an error if:
//   - The file cannot be read
//   - The YAML is invalid
//   - Validation fails (missing required fields, etc.)
func LoadConfig(path string) (*Config, error) {
	c, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(c, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// OpenSCAP defines security scanning configuration using OpenSCAP tools.
// OpenSCAP provides security compliance checking and vulnerability assessment.
type OpenSCAP struct {
	InstallSCAP    bool   `yaml:"install_scap"`     // Install openscap-utils, scap-security-guide, bzip2
	SCAPBenchmark  bool   `yaml:"scap_benchmark"`   // Run XCCDF security benchmark scan
	OVALEval       bool   `yaml:"oval_eval"`        // Run OVAL vulnerability evaluation
	Profile        string `yaml:"profile"`          // SCAP profile (e.g., xccdf_org.ssgproject.content_profile_stig)
	BenchmarkPath  string `yaml:"benchmark_path"`   // Path to XCCDF XML file (e.g., /usr/share/xml/scap/ssg/content/ssg-rl9-ds.xml)
	OVALUrl        string `yaml:"oval_url"`         // URL to download OVAL definitions (usually .bz2 compressed)
	ResultsPath    string `yaml:"results_path"`     // Path to save scan results (default: /root/scan.xml)
	RemediatePath  string `yaml:"remediate_path"`   // Path to save remediation script (default: /root/remediate.sh)
	OVALResultPath string `yaml:"oval_result_path"` // Path to save OVAL results (default: /root/vulnerabilities.xml)
}

// TLSVerify returns whether to verify TLS certificates when pulling base images.
// Defaults to true (verify) if not explicitly set.
func (m *Meta) TLSVerify() bool {
	if m.FromTLSVerify != nil {
		return *m.FromTLSVerify
	}
	return true // default to verify
}
