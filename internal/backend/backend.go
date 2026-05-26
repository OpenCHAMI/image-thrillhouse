// Package backend defines the interface for package manager backends.
// Each backend implements package manager-specific logic for installing
// packages, groups, and modules.
package backend

import (
	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
)

// Backend is the interface that all package manager backends must implement.
// It provides methods for checking capabilities and generating installation commands.
//
// Implementations exist for:
//   - DNF (Red Hat, Rocky, AlmaLinux, Fedora)
//   - Zypper (openSUSE, SLES)
//   - APT (Debian, Ubuntu - parent builds only)
//   - mmdebstrap (Debian, Ubuntu - scratch builds only)
type Backend interface {
	// SupportsInstallRoot indicates if this backend can bootstrap a scratch filesystem.
	// This is used for building images from scratch using --installroot or equivalent.
	// Returns true for: dnf, zypper, mmdebstrap
	// Returns false for: apt (use mmdebstrap for scratch builds)
	SupportsInstallRoot() bool

	// SupportsParentInstall indicates if this backend can install into an existing image.
	// This is used for layering on top of existing base images.
	// Returns true for: dnf, zypper, apt
	// Returns false for: mmdebstrap (use apt for parent builds)
	SupportsParentInstall() bool

	// ValidateOptions checks if the provided backend-specific options are valid.
	// Returns an error if any options are invalid or unknown.
	ValidateOptions(options map[string]string) error

	// ConfigFilePath returns the path where the package manager config should be written.
	// Examples:
	//   - DNF: /etc/dnf/dnf.conf
	//   - Zypper: /etc/zypp/zypp.conf
	//   - APT: /etc/apt/apt.conf
	ConfigFilePath() string

	// InstallCommands generates commands to install packages inside a running container.
	// Used for parent image builds (from != "scratch").
	// Returns a list of commands, where each command is a slice of arguments.
	InstallCommands(install config.Install) [][]string

	// InstallRootCommands generates commands to bootstrap a new filesystem from scratch.
	// Used for scratch builds (from == "scratch").
	// The commands run on the host and target the specified rootPath.
	// Returns a list of commands, where each command is a slice of arguments.
	InstallRootCommands(install config.Install, rootPath string) [][]string

	// RemovePackagesCommand generates a command to remove packages from the container.
	// Uses rpm -e --nodeps for RPM-based systems (dnf, zypper).
	// Returns a command as a slice of arguments, or nil if no packages to remove.
	RemovePackagesCommand(packages []string) []string

	// ImportGPGKeyCommand generates a command to import a GPG key for repository signing.
	// The keyURL is the URL to download the GPG key from.
	// For scratch builds, rootPath is the target root filesystem.
	// Returns a command as a slice of arguments, or nil if not supported.
	ImportGPGKeyCommand(keyURL string, rootPath string) []string

	// OutputWriter returns a writer for capturing package manager output.
	// This allows backends to format and filter package manager output.
	OutputWriter() container.OutputWriter

	// IsAcceptableExitCode checks if a non-zero exit code should be tolerated.
	// Some package managers return non-zero exit codes for non-fatal issues
	// (e.g., post-installation script failures when packages installed successfully).
	// The output parameter contains the command output which may be used to determine
	// if the operation actually succeeded despite the non-zero exit code.
	// Returns true if the exit code should be treated as success.
	IsAcceptableExitCode(exitCode int, output string) bool
}
