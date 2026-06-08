// Package dnf provides a backend for DNF package manager.
// DNF (Dandified Yum) is used by Red Hat, Rocky Linux, AlmaLinux, Fedora, and related distributions.
// It supports both scratch builds (--installroot) and parent image builds.
package dnf

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/travisbcotton/image-build/internal/backend/cmdutil"
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
//   - macro.*: string (optional) - Custom RPM macros (e.g., macro._dbpath: "/var/lib/rpm")
type DnfBackend struct {
	installWeakDeps bool
	best            bool
	skipBroken      bool
	allowErasing    bool
	noBest          bool
	releaseVer      string
	customMacros    map[string]string
}

// New creates a new DNF backend instance.
// The options parameter can configure DNF behavior:
//   - install-weak-deps: Whether to install weak dependencies (default: true)
//   - best: Use best package versions (default: true)
//   - skip-broken: Skip packages with unsolvable dependencies (default: false)
//   - allowerasing: Allow erasing packages to resolve dependencies (default: false)
//   - nobest: Do not limit to best candidates (default: false)
//   - releasever: Override the distro release version (e.g., "9", "10")
//   - macro.*: Custom RPM macros (e.g., macro._dbpath: "/var/lib/rpm")
func New(options map[string]string) *DnfBackend {
	backend := &DnfBackend{
		installWeakDeps: true, // DNF default
		best:            true, // DNF default
		skipBroken:      false,
		allowErasing:    false,
		noBest:          false,
		releaseVer:      "", // Empty = use system default
		customMacros:    cmdutil.ExtractMacroOptions(options),
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
		cmd = append(cmd, "module", "-y", mod.Action, moduleSpec(mod))
		cmds = append(cmds, cmd)
	}

	return cmds
}

// moduleSpec renders a module reference for `dnf module`. A stream is
// optional: omitting it lets DNF use the module's default stream. An empty
// stream must not produce a trailing colon (e.g. "nodejs:") because DNF
// treats that differently than the bare module name.
func moduleSpec(mod config.Module) string {
	if mod.Stream == "" {
		return mod.Name
	}
	return fmt.Sprintf("%s:%s", mod.Name, mod.Stream)
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
		// Add RPM options to work around overlay filesystem issues
		cmd = append(cmd, "--setopt=tsflags=nodocs")
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
		cmd = append(cmd, "--installroot", rootPath, "module", "-y", mod.Action, moduleSpec(mod))
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
//   - macro.*: string (any RPM macro definition)
//
// Returns an error if an unknown option is provided or if a value is invalid.
func (d *DnfBackend) ValidateOptions(options map[string]string) error {
	schema := map[string]cmdutil.OptionKind{
		"install-weak-deps": cmdutil.OptionBool,
		"best":              cmdutil.OptionBool,
		"skip-broken":       cmdutil.OptionBool,
		"allowerasing":      cmdutil.OptionBool,
		"nobest":            cmdutil.OptionBool,
		"releasever":        cmdutil.OptionAny,
	}
	if err := cmdutil.ValidateOptionSchema("dnf", options, schema); err != nil {
		return err
	}
	// Cross-option conflict
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

// RemovePackagesCommand delegates to the shared RPM removal helper. See
// cmdutil.RPMRemove for details (also used by the zypper backend).
func (d *DnfBackend) RemovePackagesCommand(packages []string, rootPath string) []string {
	return cmdutil.RPMRemove(rootPath, packages)
}

// ImportGPGKeyCommand delegates to the shared RPM key-import helper. See
// cmdutil.RPMImportKey (also used by the zypper backend).
func (d *DnfBackend) ImportGPGKeyCommand(keyPath string, rootPath string) []string {
	return cmdutil.RPMImportKey(rootPath, keyPath)
}

// Bootstrap prepares a fresh scratch root for DNF. It pre-creates the
// canonical filesystem skeleton (since the `filesystem` rpm needs many of
// these to already exist before unpacking), writes the shared RPM macros
// that disable file capabilities + shebang mangling + ldconfig, and
// initializes the RPM database. Directory-creation failures are logged
// but not fatal — the install step will fail with a clearer error if any
// of these are truly required.
func (d *DnfBackend) Bootstrap(ctx context.Context, c container.Container, rootPath string) error {
	log := slog.With("component", "backend.dnf")

	// Pre-create the base directory skeleton — DNF's filesystem package
	// will otherwise stumble trying to unpack on top of nothing.
	baseDirs := []string{
		"/dev", "/proc", "/sys", "/tmp", "/run", "/var", "/var/lib", "/var/lib/rpm",
		"/etc", "/etc/rpm", "/etc/yum.repos.d", "/usr", "/usr/bin", "/usr/lib", "/usr/lib64",
		"/usr/sbin", "/usr/share", "/boot", "/home", "/root", "/opt", "/srv", "/media", "/mnt",
	}
	for _, dir := range baseDirs {
		if err := os.MkdirAll(rootPath+dir, 0755); err != nil {
			log.Warn("Failed to create base directory", "dir", dir, "error", err)
		}
	}

	// Write the shared RPM macros into the scratch root.
	cmdutil.WriteRPMMacros(ctx, c, log, d.customMacros)

	// Initialize the RPM database.
	log.Debug("Initializing RPM database")
	out := d.OutputWriter()
	if err := c.Run(ctx, []string{"rpm", "--root", rootPath, "--initdb"}, container.RunModeHost, out); err != nil {
		log.Warn("Failed to initialize RPM database", "error", err)
	}
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
// The writer parses DNF's output to extract package information and errors.
func (d *DnfBackend) OutputWriter() container.OutputWriter {
	return newDnfWriter()
}

// IsAcceptableExitCode checks if a DNF exit code should be tolerated.
// DNF generally has reliable exit codes, so we don't tolerate non-zero exits.
func (d *DnfBackend) IsAcceptableExitCode(exitCode int, output string) bool {
	return false
}
