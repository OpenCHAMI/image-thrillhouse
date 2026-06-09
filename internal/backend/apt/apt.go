// Package apt implements the APT package manager backend for Debian and Ubuntu systems.
// This backend only supports parent image builds (from != "scratch").
// For scratch builds, use the mmdebstrap backend instead.
package apt

import (
	"context"
	"log/slog"

	"github.com/travisbcotton/image-thrillhouse/internal/backend/cmdutil"
	"github.com/travisbcotton/image-thrillhouse/internal/config"
	"github.com/travisbcotton/image-thrillhouse/internal/container"
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
	schema := map[string]cmdutil.OptionKind{
		"install-recommends":    cmdutil.OptionBool,
		"install-suggests":      cmdutil.OptionBool,
		"allow-unauthenticated": cmdutil.OptionBool,
	}
	return cmdutil.ValidateOptionSchema("apt", options, schema)
}

// Bootstrap is a no-op for apt — it does not support scratch builds, so
// this method should never be invoked by the builder. Kept to satisfy the
// Backend interface.
func (a *AptBackend) Bootstrap(ctx context.Context, c container.Container, rootPath string) error {
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
	return "/etc/apt/apt.conf.d/99-image-thrillhouse.conf"
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

	log := slog.With("component", "backend.apt")
	if len(install.Groups) > 0 {
		log.Warn("apt backend does not support package groups, ignoring", "groups", install.Groups)
	}
	if len(install.Modules) > 0 {
		log.Warn("apt backend does not support modules, ignoring", "modules", install.Modules)
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

// RemovePackagesCommand delegates to the shared dpkg removal helper (also
// used by the mmdebstrap backend). See cmdutil.DPKGRemove.
func (a *AptBackend) RemovePackagesCommand(packages []string, rootPath string) []string {
	return cmdutil.DPKGRemove(rootPath, packages)
}

// ImportGPGKeyCommand delegates to the shared apt key-import helper (also
// used by the mmdebstrap backend). See cmdutil.APTImportKey.
func (a *AptBackend) ImportGPGKeyCommand(keyPath string, rootPath string) []string {
	return cmdutil.APTImportKey(rootPath, keyPath)
}

// OutputWriter returns a writer that parses and formats APT command output.
// The writer extracts useful information like installed packages and warnings.
func (a *AptBackend) OutputWriter() container.OutputWriter {
	return newAptWriter()
}

// IsAcceptableExitCode checks if an APT exit code should be tolerated.
// APT generally has reliable exit codes, so we don't tolerate non-zero exits.
func (a *AptBackend) IsAcceptableExitCode(exitCode int, output string) bool {
	return false
}
