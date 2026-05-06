// Package zypper implements the Zypper package manager backend for openSUSE and SLES.
// Zypper is the command-line package manager for SUSE-based distributions.
// It supports both scratch builds (--installroot) and parent image builds.
package zypper

import (
	"fmt"
	"log/slog"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
)

// ZypperBackend implements the Backend interface for Zypper-based distributions.
// It supports openSUSE, SLES, and their derivatives.
//
// Supported options:
//   - repopath: Path to repository directory (default: /etc/zypp/repos.d)
//   - no-recommends: "true" or "false" (default: "false") - Do not install recommended packages
//   - no-gpg-checks: "true" or "false" (default: "false") - Skip GPG signature checks
//   - force-resolution: "true" or "false" (default: "false") - Force automatic resolution of conflicts
type ZypperBackend struct {
	repoPath        string // Path to the repository directory (default: /etc/zypp/repos.d)
	noRecommends    bool   // Do not install recommended packages
	noGpgChecks     bool   // Skip GPG signature checks
	forceResolution bool   // Force automatic resolution of conflicts
}

// New creates a new Zypper backend instance with the provided options.
//
// Supported options:
//   - repopath: Path to repository directory (default: /etc/zypp/repos.d)
//   - no-recommends: Do not install recommended packages (default: false)
//   - no-gpg-checks: Skip GPG signature checks (default: false)
//   - force-resolution: Force automatic resolution of conflicts (default: false)
func New(options map[string]string) *ZypperBackend {
	repoPath := options["repopath"]
	if repoPath == "" {
		repoPath = "/etc/zypp/repos.d"
	}
	
	backend := &ZypperBackend{
		repoPath:        repoPath,
		noRecommends:    false,
		noGpgChecks:     false,
		forceResolution: false,
	}
	
	// Parse options
	if options["no-recommends"] == "true" {
		backend.noRecommends = true
	}
	if options["no-gpg-checks"] == "true" {
		backend.noGpgChecks = true
	}
	if options["force-resolution"] == "true" {
		backend.forceResolution = true
	}
	
	return backend
}

// ConfigFilePath returns the path where Zypper configuration should be written.
func (z *ZypperBackend) ConfigFilePath() string {
	return "/etc/zypp/zypp.conf"
}

// InstallCommands generates zypper commands to install packages in a running container.
// This method is used for parent image builds (from != "scratch").
//
// Process:
//  1. Warns if modules are specified (Zypper doesn't support modules)
//  2. Installs packages with 'zypper install -y'
//  3. Installs groups (patterns) with 'zypper install -y -t pattern'
//
// Flags used:
//   -q: Quiet mode for cleaner output
//   --gpg-auto-import-keys: Automatically import repository GPG keys (unless no-gpg-checks)
//   -y: Assume "yes" to all prompts (non-interactive)
//   -t pattern: Specifies package type for groups/patterns
//   --no-recommends: Skip recommended packages (if configured)
//   --no-gpg-checks: Skip GPG verification (if configured)
//   --force-resolution: Force automatic conflict resolution (if configured)
func (z *ZypperBackend) InstallCommands(install config.Install) [][]string {
	var cmds [][]string

	if len(install.Modules) > 0 {
		slog.Warn("Zypper backend does not support modules, ignoring", "modules", install.Modules)
	}

	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 10+len(install.Packages))
		cmd = append(cmd, "zypper", "-q")
		cmd = z.addOptionFlags(cmd)
		cmd = append(cmd, "install", "-y")
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	if len(install.Groups) > 0 {
		cmd := make([]string, 0, 10+len(install.Groups))
		cmd = append(cmd, "zypper", "-q")
		cmd = z.addOptionFlags(cmd)
		cmd = append(cmd, "install", "-y", "-t", "pattern")
		cmd = append(cmd, install.Groups...)
		cmds = append(cmds, cmd)
	}

	return cmds
}

// InstallRootCommands generates zypper commands for scratch builds using --installroot.
// This runs zypper on the host system, installing into the specified root directory.
// This is how we bootstrap a new openSUSE/SLES filesystem from nothing.
//
// The commands are similar to InstallCommands but include:
//   --installroot: Target directory for the installation
//   --reposd-dir: Path to repository definitions within the new root
func (z *ZypperBackend) InstallRootCommands(install config.Install, rootPath string) [][]string {
	var cmds [][]string

	if len(install.Modules) > 0 {
		slog.Warn("Zypper backend does not support modules, ignoring", "modules", install.Modules)
	}

	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 12+len(install.Packages))
		cmd = append(cmd, "zypper", "-q", "--installroot", rootPath, "--reposd-dir", rootPath+z.repoPath)
		cmd = z.addOptionFlags(cmd)
		cmd = append(cmd, "install", "-y")
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	if len(install.Groups) > 0 {
		cmd := make([]string, 0, 12+len(install.Groups))
		cmd = append(cmd, "zypper", "-q", "--installroot", rootPath, "--reposd-dir", rootPath+z.repoPath)
		cmd = z.addOptionFlags(cmd)
		cmd = append(cmd, "install", "-y", "-t", "pattern")
		cmd = append(cmd, install.Groups...)
		cmds = append(cmds, cmd)
	}

	return cmds
}

// ValidateOptions checks if the provided options are valid for the Zypper backend.
// Valid options:
//   - repopath: Any string path
//   - no-recommends: "true" or "false"
//   - no-gpg-checks: "true" or "false"
//   - force-resolution: "true" or "false"
//
// Returns an error if an unknown option is provided or if a value is invalid.
func (z *ZypperBackend) ValidateOptions(options map[string]string) error {
	validOptions := map[string]bool{
		"repopath":         true,
		"no-recommends":    true,
		"no-gpg-checks":    true,
		"force-resolution": true,
	}
	
	boolOptions := map[string]bool{
		"no-recommends":    true,
		"no-gpg-checks":    true,
		"force-resolution": true,
	}
	
	validValues := map[string]bool{
		"true":  true,
		"false": true,
	}
	
	for key, value := range options {
		if !validOptions[key] {
			return fmt.Errorf("unknown option %q for zypper backend", key)
		}
		
		// Validate boolean options
		if boolOptions[key] && value != "" && !validValues[value] {
			return fmt.Errorf("option %q must be 'true' or 'false', got %q", key, value)
		}
		
		// repopath can be any non-empty string
		if key == "repopath" && value == "" {
			return fmt.Errorf("option 'repopath' cannot be empty")
		}
	}
	
	return nil
}

// addOptionFlags adds Zypper option flags to a command based on configured options.
// This is a helper method to avoid duplicating flag logic.
func (z *ZypperBackend) addOptionFlags(cmd []string) []string {
	if z.noRecommends {
		cmd = append(cmd, "--no-recommends")
	}
	if z.noGpgChecks {
		cmd = append(cmd, "--no-gpg-checks")
	} else {
		cmd = append(cmd, "--gpg-auto-import-keys")
	}
	if z.forceResolution {
		cmd = append(cmd, "--force-resolution")
	}
	return cmd
}

// RemovePackagesCommand generates a command to remove packages using rpm.
// Uses rpm -e --nodeps to remove packages without checking dependencies.
// This is useful for removing unnecessary packages to minimize image size.
//
// Returns nil if no packages to remove.
func (z *ZypperBackend) RemovePackagesCommand(packages []string) []string {
	if len(packages) == 0 {
		return nil
	}
	
	cmd := make([]string, 0, 3+len(packages))
	cmd = append(cmd, "rpm", "-e", "--nodeps")
	cmd = append(cmd, packages...)
	return cmd
}

// ImportGPGKeyCommand generates a command to import a GPG key for repository signing.
// For Zypper (RPM-based), this uses rpm --import to import the GPG key.
// For scratch builds, the --root flag targets the specified root path.
//
// Returns nil if keyURL is empty.
func (z *ZypperBackend) ImportGPGKeyCommand(keyURL string, rootPath string) []string {
	if keyURL == "" {
		return nil
	}
	
	if rootPath != "" {
		// Scratch build: use --root flag
		return []string{"rpm", "--root", rootPath, "--import", keyURL}
	}
	
	// Parent build: import directly
	return []string{"rpm", "--import", keyURL}
}

// SupportsInstallRoot returns true because Zypper can bootstrap a scratch filesystem.
func (z *ZypperBackend) SupportsInstallRoot() bool {
	return true
}

// SupportsParentInstall returns true because Zypper can install into existing containers.
func (z *ZypperBackend) SupportsParentInstall() bool {
	return true
}

// OutputWriter returns a writer that parses and formats Zypper output.
// The writer extracts useful information like installed packages and errors.
func (z *ZypperBackend) OutputWriter() container.OutputWriter {
	return &zypperLogWriter{}
}
