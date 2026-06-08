// Package mmdebstrap implements a backend for creating Debian/Ubuntu scratch builds.
// mmdebstrap is a Debian debootstrap alternative that creates minimal base systems.
// This backend only supports scratch builds (from = "scratch").
// For parent image builds on Debian/Ubuntu, use the apt backend instead.
package mmdebstrap

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/travisbcotton/image-build/internal/backend/cmdutil"
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

// ConfigFilePath returns "" because mmdebstrap has no persistent config file
// to write into. The previous implementation returned a human-readable
// sentence, which would have been interpreted as a literal path the moment
// any user actually set layer.manager.config on a mmdebstrap build —
// applyManagerConfig now treats "" as a hard error rather than writing to
// nonsense paths.
func (m *MmdebstrapBackend) ConfigFilePath() string {
	return ""
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

// ValidateOptions checks that the required mmdebstrap options are provided
// and that only known options are present.
//
// Required options:
//   - suite (e.g., bookworm, bullseye, jammy)
//   - mirror (e.g., http://deb.debian.org/debian)
//
// Optional options:
//   - variant (e.g., minbase, buildd)
//   - mode (e.g., fakechroot, unshare)
func (m *MmdebstrapBackend) ValidateOptions(options map[string]string) error {
	if options["suite"] == "" {
		return fmt.Errorf("mmdebstrap requires options.suite (e.g. bookworm)")
	}
	if options["mirror"] == "" {
		return fmt.Errorf("mmdebstrap requires options.mirror (e.g. http://deb.debian.org/debian)")
	}
	schema := map[string]cmdutil.OptionKind{
		"suite":   cmdutil.OptionString,
		"mirror":  cmdutil.OptionString,
		"variant": cmdutil.OptionAny,
		"mode":    cmdutil.OptionAny,
	}
	return cmdutil.ValidateOptionSchema("mmdebstrap", options, schema)
}

// Bootstrap is a no-op for mmdebstrap. mmdebstrap itself bootstraps the
// scratch root in its single InstallRootCommands invocation, so the builder
// has no pre-creation work to do beforehand.
func (d *MmdebstrapBackend) Bootstrap(ctx context.Context, c container.Container, rootPath string) error {
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

// RemovePackagesCommand delegates to the shared dpkg removal helper (also
// used by the apt backend). See cmdutil.DPKGRemove.
func (m *MmdebstrapBackend) RemovePackagesCommand(packages []string, rootPath string) []string {
	return cmdutil.DPKGRemove(rootPath, packages)
}

// ImportGPGKeyCommand delegates to the shared apt key-import helper (also
// used by the apt backend). mmdebstrap normally derives its trust from the
// mirror's keyring; this path exists for custom third-party keys.
// See cmdutil.APTImportKey.
func (m *MmdebstrapBackend) ImportGPGKeyCommand(keyPath string, rootPath string) []string {
	return cmdutil.APTImportKey(rootPath, keyPath)
}

// OutputWriter returns a writer that parses and formats mmdebstrap output.
// The writer filters mmdebstrap's verbose output and logs relevant information.
func (d *MmdebstrapBackend) OutputWriter() container.OutputWriter {
	return newMmdebstrapWriter()
}

// IsAcceptableExitCode checks if an mmdebstrap exit code should be tolerated.
// mmdebstrap generally has reliable exit codes, so we don't tolerate non-zero exits.
func (d *MmdebstrapBackend) IsAcceptableExitCode(exitCode int, output string) bool {
	return false
}
