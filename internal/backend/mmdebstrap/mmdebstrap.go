// Package mmdebstrap implements a backend for creating Debian/Ubuntu scratch builds.
// mmdebstrap is a Debian debootstrap alternative that creates minimal base systems.
// This backend only supports scratch builds (from = "scratch").
// For parent image builds on Debian/Ubuntu, use the apt backend instead.
package mmdebstrap

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
)

// MmdebstrapBackend implements the Backend interface for mmdebstrap.
// It creates minimal Debian/Ubuntu base systems from scratch.
type MmdebstrapBackend struct {
	suite   string // Debian/Ubuntu suite (e.g., bookworm, jammy)
	mirror  string // Package mirror URL (e.g., http://deb.debian.org/debian)
	variant string // Bootstrap variant: minbase, buildd, etc. (default: minbase)
	mode    string // Execution mode: fakechroot, fakeroot, etc. (default: fakechroot)
}

// New creates a new mmdebstrap backend instance with the provided options.
//
// Required options:
//   - suite: Debian/Ubuntu release codename (e.g., bookworm, jammy)
//   - mirror: Package mirror URL
//
// Optional options:
//   - variant: Bootstrap variant (default: minbase)
//   - mode: Execution mode (default: fakechroot)
func New(options map[string]string) *MmdebstrapBackend {
	variant := options["variant"]
	if variant == "" {
		variant = "minbase"
	}
	mode := options["mode"]
	if mode == "" {
		mode = "fakechroot"
	}
	return &MmdebstrapBackend{
		suite:   options["suite"],
		mirror:  options["mirror"],
		variant: variant,
		mode:    mode,
	}
}

// ConfigFilePath returns the path where configuration should be written.
// mmdebstrap doesn't use a persistent config file like DNF.
func (d *MmdebstrapBackend) ConfigFilePath() string {
	return "mmdebstrap doesn't use a persistent config file"
}

// InstallCommands is not supported by mmdebstrap backend and returns nil.
// mmdebstrap is only for scratch builds, not for installing into existing containers.
// For parent image builds on Debian/Ubuntu, use the apt backend instead.
func (m *MmdebstrapBackend) InstallCommands(install config.Install) [][]string {
	slog.Warn("mmdebstrap does not support parent image installs, use apt backend instead")
	return nil
}

// InstallRootCommands generates the mmdebstrap command to bootstrap a Debian/Ubuntu system.
// This creates a minimal base system from scratch using the configured suite and mirror.
//
// Command format:
//
//	mmdebstrap --mode=<mode> --variant=<variant> --include=<packages> <suite> <rootPath> <mirror>
//
// Example:
//
//	mmdebstrap --mode=fakechroot --variant=minbase --include=bash,coreutils bookworm /root http://deb.debian.org/debian
func (m *MmdebstrapBackend) InstallRootCommands(install config.Install, rootPath string) [][]string {
	cmd := make([]string, 0)
	cmd = append(cmd, "mmdebstrap")
	cmd = append(cmd, "--mode="+m.mode)
	cmd = append(cmd, "--variant="+m.variant)

	// add packages as --include
	if len(install.Packages) > 0 {
		cmd = append(cmd, "--include="+strings.Join(install.Packages, ","))
	}

	cmd = append(cmd, m.suite)
	cmd = append(cmd, rootPath)
	cmd = append(cmd, m.mirror)

	return [][]string{cmd}
}

// ValidateOptions checks if the required mmdebstrap options are provided.
// Required options:
//   - suite: Must be specified (e.g., bookworm, bullseye, jammy)
//   - mirror: Must be specified (e.g., http://deb.debian.org/debian)
//
// Optional options:
//   - variant: Bootstrap variant (e.g., minbase, buildd)
//   - mode: Execution mode (e.g., fakechroot, unshare)
//
// Returns an error if either required option is missing or if an unknown option is provided.
func (m *MmdebstrapBackend) ValidateOptions(options map[string]string) error {
	// Check required options
	if options["suite"] == "" {
		return fmt.Errorf("mmdebstrap requires options.suite (e.g. bookworm)")
	}
	if options["mirror"] == "" {
		return fmt.Errorf("mmdebstrap requires options.mirror (e.g. http://deb.debian.org/debian)")
	}

	// Validate that only known options are provided
	validOptions := map[string]bool{
		"suite":   true,
		"mirror":  true,
		"variant": true,
		"mode":    true,
	}

	for key := range options {
		if !validOptions[key] {
			return fmt.Errorf("unknown option %q for mmdebstrap backend", key)
		}
	}

	return nil
}

// SupportsInstallRoot returns true because mmdebstrap can bootstrap a scratch filesystem.
func (d *MmdebstrapBackend) SupportsInstallRoot() bool {
	return true
}

// SupportsParentInstall returns false because mmdebstrap can only bootstrap
// a new filesystem; it cannot install into an existing image.
// Use the apt backend for parent image builds.
func (d *MmdebstrapBackend) SupportsParentInstall() bool {
	return false
}

// RemovePackagesCommand generates a command to remove packages using dpkg.
// Uses dpkg --remove --force-depends to remove packages without checking dependencies.
// This is useful for removing unnecessary packages to minimize image size.
//
// If rootPath is non-empty, the command runs on the host targeting that
// filesystem (dpkg --root <path> ...). For mmdebstrap this is the common
// case since the backend only supports scratch builds.
//
// Returns nil if no packages to remove.
func (m *MmdebstrapBackend) RemovePackagesCommand(packages []string, rootPath string) []string {
	if len(packages) == 0 {
		return nil
	}

	cmd := make([]string, 0, 5+len(packages))
	cmd = append(cmd, "dpkg")
	if rootPath != "" {
		cmd = append(cmd, "--root", rootPath)
	}
	cmd = append(cmd, "--remove", "--force-depends")
	cmd = append(cmd, packages...)
	return cmd
}

// ImportGPGKeyCommand generates a command to import a GPG key for APT repository signing.
// For mmdebstrap, GPG keys should typically be handled through the mirror's keyring.
// This implementation provides basic support for custom keys.
//
// Returns nil if keyURL is empty.
func (m *MmdebstrapBackend) ImportGPGKeyCommand(keyURL string, rootPath string) []string {
	if keyURL == "" {
		return nil
	}
	
	keyName := "image-build-repo.gpg"
	if rootPath != "" {
		keyName = rootPath + "/etc/apt/trusted.gpg.d/image-build-repo.gpg"
	} else {
		keyName = "/etc/apt/trusted.gpg.d/image-build-repo.gpg"
	}
	
	// Use curl to download and gpg to dearmor (if ASCII-armored)
	script := fmt.Sprintf("curl -fsSL %s | gpg --dearmor -o %s 2>/dev/null || curl -fsSL %s -o %s", 
		keyURL, keyName, keyURL, keyName)
	
	return []string{"sh", "-c", script}
}

// OutputWriter returns a writer that parses and formats mmdebstrap output.
// The writer filters mmdebstrap's verbose output and logs relevant information.
func (d *MmdebstrapBackend) OutputWriter() container.OutputWriter {
	return &mmdebstrapLogWriter{}
}

// IsAcceptableExitCode checks if an mmdebstrap exit code should be tolerated.
// mmdebstrap generally has reliable exit codes, so we don't tolerate non-zero exits.
func (d *MmdebstrapBackend) IsAcceptableExitCode(exitCode int, output string) bool {
	return false
}
