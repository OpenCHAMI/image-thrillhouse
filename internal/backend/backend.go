// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package backend defines the interface for package manager backends.
// Each backend implements package manager-specific logic for installing
// packages, groups, and modules.
package backend

import (
	"context"

	"github.com/openchami/image-thrillhouse/internal/config"
	"github.com/openchami/image-thrillhouse/internal/container"
)

// Backend generates the package-manager commands for one distro family.
// Implementations: dnf, zypper, apt (parent builds only), mmdebstrap (scratch
// only). Each declares its capabilities via the Supports* methods.
//
// Backends never touch the network or a shell. They return argv slices that the
// builder runs; anything that must be fetched is fetched by the builder first
// and handed over as a local path. That is what makes it impossible for a
// user-supplied URL to reach a shell string.
//
// rootPath is the convention throughout: non-empty means a scratch build, so the
// command targets that host path (e.g. rpm --root <path>) and runs on the host.
// Empty means the command runs inside the container.
type Backend interface {
	// Bootstrap prepares a fresh scratch root before InstallRootCommands —
	// pre-creating directories, writing RPM macros, running rpm --initdb. Only
	// called for scratch builds; backends without scratch support return nil.
	Bootstrap(ctx context.Context, c container.Container, rootPath string) error

	// RequiresEmptyRoot reports that InstallRootCommands refuses to run against
	// a non-empty root (today: mmdebstrap). The builder defers repo/file/GPG-key
	// writes until after the install step for these, inverting its usual order.
	RequiresEmptyRoot() bool

	// SupportsInstallRoot reports whether this backend can bootstrap a scratch
	// filesystem. False for apt — use mmdebstrap.
	SupportsInstallRoot() bool

	// SupportsParentInstall reports whether this backend can install into an
	// existing image. False for mmdebstrap — use apt.
	SupportsParentInstall() bool

	ValidateOptions(options map[string]string) error

	// ConfigFilePath is where layer.manager.config gets written, or "" for
	// backends with no persistent config file (the builder rejects the setting
	// rather than writing to a bogus path).
	ConfigFilePath() string

	// InstallCommands builds the install argv for a parent build.
	InstallCommands(install config.Install) [][]string

	// InstallRootCommands builds the install argv for a scratch build.
	InstallRootCommands(install config.Install, rootPath string) [][]string

	// RemovePackagesCommand returns nil when there is nothing to remove.
	RemovePackagesCommand(packages []string, rootPath string) []string

	// ImportGPGKeyCommand installs a key the builder has already fetched to
	// keyPath. keyName is a per-repo identifier used by deb-based backends to
	// give each key its own file under /etc/apt/trusted.gpg.d/ — without it,
	// two repos supplying keys overwrote each other. RPM backends import into
	// the rpm keyring and ignore it. Returns nil if unsupported.
	ImportGPGKeyCommand(keyName string, keyPath string, rootPath string) []string

	// OutputWriter returns the writer that parses this package manager's output.
	OutputWriter() container.OutputWriter

	// IsAcceptableExitCode reports whether a non-zero exit should be treated as
	// success — some package managers signal non-fatal conditions that way (see
	// zypper's informational 1xx codes). output is available for classifiers
	// that need to inspect what actually happened.
	IsAcceptableExitCode(exitCode int, output string) bool
}
