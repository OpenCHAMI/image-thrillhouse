// Package dnf provides a backend for DNF package manager.
// DNF (Dandified Yum) is used by Red Hat, Rocky Linux, AlmaLinux, Fedora, and related distributions.
// It supports both scratch builds (--installroot) and parent image builds.
package dnf

import (
	"fmt"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
)

// DnfBackend implements the Backend interface for DNF package manager.
//
// Supported options:
//   - install-weak-deps: "true" or "false" (default: "true") - Install weak dependencies
//   - best: "true" or "false" (default: "true") - Use best package versions
//   - skip-broken: "true" or "false" (default: "false") - Skip uninstallable packages
//   - allowerasing: "true" or "false" (default: "false") - Allow erasing of installed packages to resolve dependencies
//   - nobest: "true" or "false" (default: "false") - Do not limit packages to best candidates
//   - releasever: string (optional) - Override the RHEL/distro release version (e.g., "9", "10")
type DnfBackend struct {
	installWeakDeps bool
	best            bool
	skipBroken      bool
	allowErasing    bool
	noBest          bool
	releaseVer      string
}

// New creates a new DNF backend instance.
// The options parameter can configure DNF behavior:
//   - install-weak-deps: Whether to install weak dependencies (default: true)
//   - best: Use best package versions (default: true)
//   - skip-broken: Skip packages with unsolvable dependencies (default: false)
//   - allowerasing: Allow erasing packages to resolve dependencies (default: false)
//   - nobest: Do not limit to best candidates (default: false)
//   - releasever: Override the distro release version (e.g., "9", "10")
func New(options map[string]string) *DnfBackend {
	backend := &DnfBackend{
		installWeakDeps: true, // DNF default
		best:            true, // DNF default
		skipBroken:      false,
		allowErasing:    false,
		noBest:          false,
		releaseVer:      "", // Empty = use system default
	}

	// Parse options
	if options["install-weak-deps"] == "false" {
		backend.installWeakDeps = false
	}
	if options["best"] == "false" {
		backend.best = false
	}
	if options["skip-broken"] == "true" {
		backend.skipBroken = true
	}
	if options["allowerasing"] == "true" {
		backend.allowErasing = true
	}
	if options["nobest"] == "true" {
		backend.noBest = true
	}
	if rv, ok := options["releasever"]; ok && rv != "" {
		backend.releaseVer = rv
	}

	return backend
}

// ConfigFilePath returns the path to the DNF configuration file.
func (d *DnfBackend) ConfigFilePath() string {
	return "/etc/dnf/dnf.conf"
}

// InstallCommands generates DNF commands to run inside a container.
// This is used for parent image builds where DNF is already installed.
//
// Generates commands for:
//   - Installing individual packages: dnf install -y <packages>
//   - Installing package groups: dnf groupinstall -y <groups>
//   - Module operations: dnf module <action> <module:stream>
//
// All commands use -q for quiet output and -y for automatic yes.
// Additional flags are added based on configured options.
func (d *DnfBackend) InstallCommands(install config.Install) [][]string {
	var cmds [][]string

	// Install individual packages
	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 10+len(install.Packages))
		cmd = append(cmd, "dnf", "-q")
		cmd = d.addOptionFlags(cmd)
		cmd = append(cmd, "install", "-y")
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	// Install package groups (e.g., "Development Tools")
	if len(install.Groups) > 0 {
		cmd := make([]string, 0, 10+len(install.Groups))
		cmd = append(cmd, "dnf", "-q")
		cmd = d.addOptionFlags(cmd)
		cmd = append(cmd, "groupinstall", "-y")
		cmd = append(cmd, install.Groups...)
		cmds = append(cmds, cmd)
	}

	// Handle module operations (enable, install, disable, reset)
	for _, mod := range install.Modules {
		cmd := make([]string, 0, 12)
		cmd = append(cmd, "dnf", "-q")
		cmd = d.addOptionFlags(cmd)
		cmd = append(cmd, "module", "-y", mod.Action, fmt.Sprintf("%s:%s", mod.Name, mod.Stream))
		cmds = append(cmds, cmd)
	}

	return cmds
}

// InstallRootCommands generates DNF commands for scratch builds using --installroot.
// This runs DNF on the host system, installing into the specified root directory.
// This is how we bootstrap a new filesystem from nothing.
//
// The commands are similar to InstallCommands but include --installroot flag.
func (d *DnfBackend) InstallRootCommands(install config.Install, rootPath string) [][]string {
	var cmds [][]string

	// Install individual packages into the root path
	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 12+len(install.Packages))
		cmd = append(cmd, "dnf", "-q")
		cmd = d.addOptionFlags(cmd)
		cmd = append(cmd, "--installroot", rootPath, "install", "-y")
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	// Install package groups into the root path
	if len(install.Groups) > 0 {
		cmd := make([]string, 0, 12+len(install.Groups))
		cmd = append(cmd, "dnf", "-q")
		cmd = d.addOptionFlags(cmd)
		cmd = append(cmd, "--installroot", rootPath, "groupinstall", "-y")
		cmd = append(cmd, install.Groups...)
		cmds = append(cmds, cmd)
	}

	// Handle module operations for the root path
	for _, mod := range install.Modules {
		cmd := make([]string, 0, 14)
		cmd = append(cmd, "dnf", "-q")
		cmd = d.addOptionFlags(cmd)
		cmd = append(cmd, "--installroot", rootPath, "module", "-y", mod.Action, fmt.Sprintf("%s:%s", mod.Name, mod.Stream))
		cmds = append(cmds, cmd)
	}

	return cmds
}

// ValidateOptions validates DNF-specific options.
// Valid options:
//   - install-weak-deps: "true" or "false"
//   - best: "true" or "false"
//   - skip-broken: "true" or "false"
//   - allowerasing: "true" or "false"
//   - nobest: "true" or "false"
//   - releasever: string (any value, e.g., "9", "10", "40")
//
// Returns an error if an unknown option is provided or if a value is invalid.
func (d *DnfBackend) ValidateOptions(options map[string]string) error {
	validOptions := map[string]bool{
		"install-weak-deps": true,
		"best":              true,
		"skip-broken":       true,
		"allowerasing":      true,
		"nobest":            true,
		"releasever":        true,
	}

	validValues := map[string]bool{
		"true":  true,
		"false": true,
	}

	for key, value := range options {
		if !validOptions[key] {
			return fmt.Errorf("unknown option %q for dnf backend", key)
		}
		// Skip validation for releasever - it can be any string
		if key == "releasever" {
			continue
		}
		if value != "" && !validValues[value] {
			return fmt.Errorf("option %q must be 'true' or 'false', got %q", key, value)
		}
	}

	// Validate conflicting options
	if options["best"] == "true" && options["nobest"] == "true" {
		return fmt.Errorf("options 'best' and 'nobest' cannot both be true")
	}

	return nil
}

// addOptionFlags adds DNF option flags to a command based on configured options.
// This is a helper method to avoid duplicating flag logic.
func (d *DnfBackend) addOptionFlags(cmd []string) []string {
	if d.releaseVer != "" {
		cmd = append(cmd, "--releasever", d.releaseVer)
	}
	if !d.installWeakDeps {
		cmd = append(cmd, "--setopt=install_weak_deps=False")
	}
	if !d.best {
		cmd = append(cmd, "--setopt=best=False")
	}
	if d.skipBroken {
		cmd = append(cmd, "--skip-broken")
	}
	if d.allowErasing {
		cmd = append(cmd, "--allowerasing")
	}
	if d.noBest {
		cmd = append(cmd, "--nobest")
	}
	return cmd
}

// SupportsInstallRoot indicates that DNF supports scratch builds using --installroot.
func (d *DnfBackend) SupportsInstallRoot() bool {
	return true
}

// SupportsParentInstall indicates that DNF can install into existing containers.
func (d *DnfBackend) SupportsParentInstall() bool {
	return true
}

// OutputWriter returns a custom log writer for DNF output.
// This writer filters and formats DNF's output for better readability.
func (d *DnfBackend) OutputWriter() container.OutputWriter {
	return &dnfLogWriter{}
}
