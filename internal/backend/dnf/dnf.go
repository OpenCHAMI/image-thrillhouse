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
type DnfBackend struct{}

// New creates a new DNF backend instance.
// Options are currently unused but reserved for future DNF-specific configuration.
func New(options map[string]string) *DnfBackend {
	return &DnfBackend{}
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
func (d *DnfBackend) InstallCommands(install config.Install) [][]string {
	var cmds [][]string

	// Install individual packages
	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 4+len(install.Packages))
		cmd = append(cmd, "dnf", "-q", "install", "-y")
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	// Install package groups (e.g., "Development Tools")
	if len(install.Groups) > 0 {
		cmd := make([]string, 0, 4+len(install.Groups))
		cmd = append(cmd, "dnf", "-q", "groupinstall", "-y")
		cmd = append(cmd, install.Groups...)
		cmds = append(cmds, cmd)
	}

	// Handle module operations (enable, install, disable, reset)
	for _, mod := range install.Modules {
		cmd := make([]string, 0, 6)
		cmd = append(cmd, "dnf", "-q", "module", "-y", mod.Action, fmt.Sprintf("%s:%s", mod.Name, mod.Stream))
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
		cmd := make([]string, 0, 4+len(install.Packages))
		cmd = append(cmd, "dnf", "-q", "--installroot", rootPath, "install", "-y")
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	// Install package groups into the root path
	if len(install.Groups) > 0 {
		cmd := make([]string, 0, 4+len(install.Groups))
		cmd = append(cmd, "dnf", "-q", "--installroot", rootPath, "groupinstall", "-y")
		cmd = append(cmd, install.Groups...)
		cmds = append(cmds, cmd)
	}

	// Handle module operations for the root path
	for _, mod := range install.Modules {
		cmd := make([]string, 0, 6)
		cmd = append(cmd, "dnf", "-q", "--installroot", rootPath, "module", "-y", mod.Action, fmt.Sprintf("%s:%s", mod.Name, mod.Stream))
		cmds = append(cmds, cmd)
	}

	return cmds
}

// ValidateOptions validates DNF-specific options.
// Currently no options are defined, so this always returns nil.
func (d *DnfBackend) ValidateOptions(options map[string]string) error {
	return nil
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
