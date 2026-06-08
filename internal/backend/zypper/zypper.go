// Package zypper implements the Zypper package manager backend for openSUSE and SLES.
// Zypper is the command-line package manager for SUSE-based distributions.
// It supports both scratch builds (--installroot) and parent image builds.
package zypper

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/travisbcotton/image-build/internal/backend/cmdutil"
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
//   - macro.*: string (optional) - Custom RPM macros (e.g., macro._dbpath: "/var/lib/rpm")
type ZypperBackend struct {
	repoPath        string // Path to the repository directory (default: /etc/zypp/repos.d)
	noRecommends    bool   // Do not install recommended packages
	noGpgChecks     bool   // Skip GPG signature checks
	forceResolution bool   // Force automatic resolution of conflicts
	customMacros    map[string]string
}

// New creates a new Zypper backend instance with the provided options.
//
// Supported options:
//   - repopath: Path to repository directory (default: /etc/zypp/repos.d)
//   - no-recommends: Do not install recommended packages (default: false)
//   - no-gpg-checks: Skip GPG signature checks (default: false)
//   - force-resolution: Force automatic resolution of conflicts (default: false)
//   - macro.*: Custom RPM macros (e.g., macro._dbpath: "/var/lib/rpm")
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
		customMacros:    cmdutil.ExtractMacroOptions(options),
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
//
//	-q: Quiet mode for cleaner output
//	--gpg-auto-import-keys: Automatically import repository GPG keys (unless no-gpg-checks)
//	-y: Assume "yes" to all prompts (non-interactive)
//	-t pattern: Specifies package type for groups/patterns
//	--no-recommends: Skip recommended packages (if configured)
//	--no-gpg-checks: Skip GPG verification (if configured)
//	--force-resolution: Force automatic conflict resolution (if configured)
func (z *ZypperBackend) InstallCommands(install config.Install) [][]string {
	var cmds [][]string

	if len(install.Modules) > 0 {
		slog.With("component", "backend.zypper").Warn("Zypper backend does not support modules, ignoring", "modules", install.Modules)
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

// InstallRootCommands generates zypper commands for scratch builds using --root.
// This runs zypper on the host system, installing into the specified root directory.
// This is how we bootstrap a new openSUSE/SLES filesystem from nothing.
//
// Process:
//  1. Runs 'zypper refresh' to update repository metadata
//  2. Installs packages with --root flag
//  3. Installs groups (patterns) if specified
//
// The commands use --root instead of --installroot because:
//
//	--root: Operates on a different root directory (for scratch builds)
//	--installroot: Shares repos with host (not suitable for scratch builds)
func (z *ZypperBackend) InstallRootCommands(install config.Install, rootPath string) [][]string {
	var cmds [][]string

	if len(install.Modules) > 0 {
		slog.With("component", "backend.zypper").Warn("Zypper backend does not support modules, ignoring", "modules", install.Modules)
	}

	// Always refresh repository metadata first for scratch builds
	// Don't use -q flag for refresh to see potential errors
	// Note: refresh doesn't accept --no-recommends flag, only --no-gpg-checks
	refreshCmd := make([]string, 0, 8)
	refreshCmd = append(refreshCmd, "zypper", "--root", rootPath)
	// Only add GPG check flags for refresh
	if z.noGpgChecks {
		refreshCmd = append(refreshCmd, "--no-gpg-checks")
	} else {
		refreshCmd = append(refreshCmd, "--gpg-auto-import-keys")
	}
	refreshCmd = append(refreshCmd, "refresh")
	cmds = append(cmds, refreshCmd)

	if len(install.Packages) > 0 {
		cmd := make([]string, 0, 12+len(install.Packages))
		cmd = append(cmd, "zypper", "--root", rootPath)
		// Add global options before subcommand
		if z.noGpgChecks {
			cmd = append(cmd, "--no-gpg-checks")
		} else {
			cmd = append(cmd, "--gpg-auto-import-keys")
		}
		cmd = append(cmd, "install", "-y")
		// Add subcommand-specific options
		if z.noRecommends {
			cmd = append(cmd, "--no-recommends")
		}
		if z.forceResolution {
			cmd = append(cmd, "--force-resolution")
		}
		cmd = append(cmd, install.Packages...)
		cmds = append(cmds, cmd)
	}

	if len(install.Groups) > 0 {
		cmd := make([]string, 0, 12+len(install.Groups))
		cmd = append(cmd, "zypper", "--root", rootPath)
		// Add global options before subcommand
		if z.noGpgChecks {
			cmd = append(cmd, "--no-gpg-checks")
		} else {
			cmd = append(cmd, "--gpg-auto-import-keys")
		}
		cmd = append(cmd, "install", "-y", "-t", "pattern")
		// Add subcommand-specific options
		if z.noRecommends {
			cmd = append(cmd, "--no-recommends")
		}
		if z.forceResolution {
			cmd = append(cmd, "--force-resolution")
		}
		cmd = append(cmd, install.Groups...)
		cmds = append(cmds, cmd)
	}

	return cmds
}

// ValidateOptions checks if the provided options are valid for the Zypper backend.
// Valid options:
//   - repopath: Any non-empty string path
//   - no-recommends: "true" or "false"
//   - no-gpg-checks: "true" or "false"
//   - force-resolution: "true" or "false"
//   - macro.*: string (any RPM macro definition)
//
// Returns an error if an unknown option is provided or if a value is invalid.
func (z *ZypperBackend) ValidateOptions(options map[string]string) error {
	schema := map[string]cmdutil.OptionKind{
		"repopath":         cmdutil.OptionString,
		"no-recommends":    cmdutil.OptionBool,
		"no-gpg-checks":    cmdutil.OptionBool,
		"force-resolution": cmdutil.OptionBool,
	}
	return cmdutil.ValidateOptionSchema("zypper", options, schema)
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

// RemovePackagesCommand delegates to the shared RPM removal helper (also
// used by the dnf backend). See cmdutil.RPMRemove.
func (z *ZypperBackend) RemovePackagesCommand(packages []string, rootPath string) []string {
	return cmdutil.RPMRemove(rootPath, packages)
}

// ImportGPGKeyCommand delegates to the shared RPM key-import helper (also
// used by the dnf backend). See cmdutil.RPMImportKey.
func (z *ZypperBackend) ImportGPGKeyCommand(keyPath string, rootPath string) []string {
	return cmdutil.RPMImportKey(rootPath, keyPath)
}

// Bootstrap prepares a fresh scratch root for Zypper. It pre-creates the
// pseudo-fs mount points that Zypper's `filesystem` package expects to find,
// and writes the shared RPM macros (same set the dnf backend uses) — both
// backends share the RPM scriptlet ecosystem so the same workarounds apply.
//
// We intentionally avoid creating /dev here: doing so left a host-owned dir
// in the scratch root which broke UID/GID checks during commit.
func (z *ZypperBackend) Bootstrap(ctx context.Context, c container.Container, rootPath string) error {
	log := slog.With("component", "backend.zypper")

	for _, dir := range []string{"/proc", "/sys", "/run", "/etc/rpm"} {
		if err := os.MkdirAll(rootPath+dir, 0755); err != nil {
			log.Warn("Failed to create essential directory", "dir", dir, "error", err)
		}
	}

	cmdutil.WriteRPMMacros(ctx, c, log, z.customMacros)
	return nil
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
	return newZypperWriter()
}

// IsAcceptableExitCode checks if a zypper exit code should be tolerated.
//
// Zypper distinguishes "error" exit codes (1..99) from "informational" exit
// codes (100..149). The informational codes mean the requested operation
// succeeded — they only carry an extra signal back to the caller. In a
// container/image-build context where there is no init, no D-Bus, and the
// image is about to be committed and never "run" in the chroot, several of
// these signals are pure noise:
//
//   - 102 ZYPPER_EXIT_INF_REBOOT_NEEDED: the image will be rebooted by
//     whoever boots it; nothing to do here.
//   - 103 ZYPPER_EXIT_INF_RESTART_NEEDED: same; no zypper running to restart.
//   - 107 ZYPPER_EXIT_INF_RPM_SCRIPT_FAILED: packages were installed but one
//     or more RPM scriptlets (typically systemd's post-install talking to
//     dbus) failed in the --root chroot. The on-disk state is correct; the
//     scriptlets will re-run at first boot.
//
// Exit code 8 (ZYPPER_EXIT_ERR_COMMIT) is a real error in general, but in
// older zypper versions it was also returned for post-install scriptlet
// failures. We keep the existing output-sniffing heuristic for that case
// for backward compatibility.
func (z *ZypperBackend) IsAcceptableExitCode(exitCode int, output string) bool {
	switch exitCode {
	case 102, 103, 107:
		// Informational codes after a successful install. Safe to treat as
		// success for an image build.
		return true
	case 8:
		// Legacy: ZYPPER_EXIT_ERR_COMMIT can mean "post-install scriptlet
		// failed but packages installed." Confirm via output sniffing.
		return strings.Contains(output, "Installing:") ||
			strings.Contains(output, "NEW packages are going to be installed") ||
			strings.Contains(output, "done]")
	}
	return false
}
