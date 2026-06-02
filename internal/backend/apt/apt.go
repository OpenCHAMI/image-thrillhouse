// Package apt implements the APT package manager backend for Debian and Ubuntu systems.
// This backend only supports parent image builds (from != "scratch").
// For scratch builds, use the mmdebstrap backend instead.
package apt

import (
	"fmt"
	"log/slog"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
)

// AptBackend implements the Backend interface for APT-based distributions.
// It supports Debian, Ubuntu, and their derivatives for parent image builds.
//
// Supported options:
//   - install-recommends: "true" or "false" (default: "false") - Install recommended packages
//   - install-suggests: "true" or "false" (default: "false") - Install suggested packages
//   - allow-unauthenticated: "true" or "false" (default: "false") - Allow unauthenticated packages
type AptBackend struct {
	installRecommends    bool
	installSuggests      bool
	allowUnauthenticated bool
}

// New creates a new APT backend instance.
// The options parameter can configure APT behavior:
//   - install-recommends: Whether to install recommended packages (default: false)
//   - install-suggests: Whether to install suggested packages (default: false)
//   - allow-unauthenticated: Whether to allow unauthenticated packages (default: false)
func New(options map[string]string) *AptBackend {
	backend := &AptBackend{
		installRecommends:    false,
		installSuggests:      false,
		allowUnauthenticated: false,
	}

	// Parse options
	if options["install-recommends"] == "true" {
		backend.installRecommends = true
	}
	if options["install-suggests"] == "true" {
		backend.installSuggests = true
	}
	if options["allow-unauthenticated"] == "true" {
		backend.allowUnauthenticated = true
	}

	return backend
}

// ValidateOptions checks if the provided options are valid for the APT backend.
// Valid options:
//   - install-recommends: "true" or "false"
//   - install-suggests: "true" or "false"
//   - allow-unauthenticated: "true" or "false"
//
// Returns an error if an unknown option is provided or if a value is invalid.
func (a *AptBackend) ValidateOptions(options map[string]string) error {
	validOptions := map[string]bool{
		"install-recommends":    true,
		"install-suggests":      true,
		"allow-unauthenticated": true,
	}

	validValues := map[string]bool{
		"true":  true,
		"false": true,
	}

	for key, value := range options {
		if !validOptions[key] {
			return fmt.Errorf("unknown option %q for apt backend", key)
		}
		if value != "" && !validValues[value] {
			return fmt.Errorf("option %q must be 'true' or 'false', got %q", key, value)
		}
	}

	return nil
}

// SupportsInstallRoot returns false because APT cannot bootstrap a scratch filesystem.
// Use the mmdebstrap backend for scratch builds on Debian/Ubuntu systems.
func (a *AptBackend) SupportsInstallRoot() bool { return false }

// SupportsParentInstall returns true because APT can install packages into existing images.
func (a *AptBackend) SupportsParentInstall() bool { return true }

// ConfigFilePath returns the path where APT configuration should be written.
// Returns the standard apt.conf.d drop-in directory path with a high priority number
// to ensure our configuration overrides other settings.
func (a *AptBackend) ConfigFilePath() string {
	return "/etc/apt/apt.conf.d/99-image-build.conf"
}

// InstallCommands generates apt-get commands to install packages in a running container.
// This method is used for parent image builds (from != "scratch").
//
// Process:
//  1. Always runs 'apt-get update' first to refresh package indexes
//  2. Warns if groups or modules are specified (APT doesn't support these)
//  3. Installs all packages with 'apt-get install' and configured options
//
// Flags used:
//
//	-y: Assume "yes" to all prompts (non-interactive)
//	-q: Quiet mode for cleaner output
//	--no-install-recommends: Don't install recommended packages (unless enabled)
//	--no-install-suggests: Don't install suggested packages (unless enabled)
//	--allow-unauthenticated: Allow packages without GPG verification (if enabled)
func (a *AptBackend) InstallCommands(install config.Install) [][]string {
	var cmds [][]string

	// always update first
	cmds = append(cmds, []string{"apt-get", "update", "-q"})

	if len(install.Groups) > 0 {
		slog.Warn("apt backend does not support package groups, ignoring", "groups", install.Groups)
	}
	if len(install.Modules) > 0 {
		slog.Warn("apt backend does not support modules, ignoring", "modules", install.Modules)
	}

	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 8+len(install.Packages))
		cmd = append(cmd, "apt-get", "install", "-y", "-q")

		// Add option flags
		if !a.installRecommends {
			cmd = append(cmd, "--no-install-recommends")
		}
		if !a.installSuggests {
			cmd = append(cmd, "--no-install-suggests")
		}
		if a.allowUnauthenticated {
			cmd = append(cmd, "--allow-unauthenticated")
		}

		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	return cmds
}

// InstallRootCommands is not supported by APT backend and returns nil.
// For scratch builds on Debian/Ubuntu, use the mmdebstrap backend instead.
func (a *AptBackend) InstallRootCommands(install config.Install, rootPath string) [][]string {
	return nil
}

// RemovePackagesCommand generates a command to remove packages using dpkg.
// Uses dpkg --remove --force-depends to remove packages without checking dependencies.
// This is useful for removing unnecessary packages to minimize image size.
//
// If rootPath is non-empty, the command runs on the host targeting that
// filesystem (dpkg --root <path> ...) so that scratch builds can remove
// packages from the bootstrapped root before commit.
//
// Returns nil if no packages to remove.
func (a *AptBackend) RemovePackagesCommand(packages []string, rootPath string) []string {
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

// ImportGPGKeyCommand returns a command that installs an already-fetched
// GPG key from keyPath into /etc/apt/trusted.gpg.d/. It dearmors the key
// when needed and falls back to copying it as-is if it is already binary.
//
// The builder fetches the key in Go (see internal/builder.importGPGKeys)
// and writes it to keyPath. Only that path and our own hardcoded
// destination appear in the shell string, so a user-supplied URL can
// never reach a shell — closing the prior injection vector.
//
// For scratch builds, rootPath is non-empty and the destination lives
// under that root; the command runs on the host.
//
// Returns nil if keyPath is empty.
func (a *AptBackend) ImportGPGKeyCommand(keyPath string, rootPath string) []string {
	if keyPath == "" {
		return nil
	}

	final := "/etc/apt/trusted.gpg.d/image-build-repo.gpg"
	if rootPath != "" {
		final = rootPath + "/etc/apt/trusted.gpg.d/image-build-repo.gpg"
	}

	// keyPath and final are both produced by this codebase; neither is
	// user-supplied. Try gpg --dearmor first (works on ASCII-armored input)
	// and fall back to a plain copy for already-binary keys.
	script := fmt.Sprintf("gpg --dearmor -o %q %q 2>/dev/null || cp %q %q",
		final, keyPath, keyPath, final)

	return []string{"sh", "-c", script}
}

// OutputWriter returns a writer that parses and formats APT command output.
// The writer extracts useful information like installed packages and warnings.
func (a *AptBackend) OutputWriter() container.OutputWriter {
	return &aptLogWriter{}
}

// IsAcceptableExitCode checks if an APT exit code should be tolerated.
// APT generally has reliable exit codes, so we don't tolerate non-zero exits.
func (a *AptBackend) IsAcceptableExitCode(exitCode int, output string) bool {
	return false
}
