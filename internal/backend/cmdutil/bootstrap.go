// SPDX-FileCopyrightText: © 2025 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

package cmdutil

import (
	"context"
	"log/slog"
	"strings"

	"github.com/travisbcotton/image-thrillhouse/internal/config"
	"github.com/travisbcotton/image-thrillhouse/internal/container"
)

// RPMMacrosPath is the canonical destination for the shared image-thrillhouse RPM
// macros under /etc/rpm. Both dnf and zypper scratch bootstraps write here.
const RPMMacrosPath = "/etc/rpm/macros.image-thrillhouse"

// WriteRPMMacros installs the shared RPMMacros content into the container at
// RPMMacrosPath. It centralises the boilerplate the dnf and zypper backends
// both ran in their Bootstrap methods — including the WARN-on-failure
// posture: scratch installs occasionally race the filesystem-layer rpm and
// having a clean error message here is more useful than a failed build.
//
// Custom macros can be provided via the customMacros map, where keys are macro
// names (without the leading %) and values are the macro definitions. Custom
// macros append to the default macros, and can override defaults if they use
// the same macro name.
func WriteRPMMacros(ctx context.Context, c container.Container, log *slog.Logger, customMacros map[string]string) {
	content := BuildRPMMacros(customMacros)
	if err := c.WriteFile(ctx, config.File{
		Path:    RPMMacrosPath,
		Content: content,
	}); err != nil {
		log.Warn("failed to write rpm macros", "error", err)
	}
}

// BuildRPMMacros constructs the RPM macros file content by merging default
// macros with custom ones. Custom macros can override defaults by using the
// same macro name. The macro names in customMacros should not include the
// leading % character.
//
// Example:
//   customMacros := map[string]string{
//       "_dbpath": "/var/lib/rpm",
//       "_dbpath_trans": "/var/lib/rpm",
//   }
//   content := BuildRPMMacros(customMacros)
func BuildRPMMacros(customMacros map[string]string) string {
	// Default macros as a map for easy override
	defaults := map[string]string{
		"_netsharedpath":                  "/sys:/proc:/dev",
		"_install_langs":                  "C:en:en_US:en_US.UTF-8",
		"__brp_mangle_shebangs":           "%{nil}",
		"_missing_build_ids_terminate_build": "0",
		"_file_context_file":              "%{nil}",
		"__brp_ldconfig":                  "%{nil}",
	}

	// Merge custom macros into defaults (custom macros override defaults)
	for key, value := range customMacros {
		defaults[key] = value
	}

	// Build the macro file content
	var content string
	for key, value := range defaults {
		content += "%" + key + " " + value + "\n"
	}

	return content
}

// RPMMacros is the macros.image-thrillhouse file content that both the dnf and
// zypper scratch builds need under /etc/rpm. It works around a cluster of
// overlay-filesystem and container-isolation issues that bite RPM during
// scriptlet execution:
//
//   - %_netsharedpath: tells rpm not to install into shared kernel pseudo-fs
//     mounts that may not exist or be writable in the chroot.
//   - %_install_langs: trims down installed locales (smaller image).
//   - %__brp_mangle_shebangs / %__brp_ldconfig: disables build-root policies
//     that try to rewrite shebangs or run ldconfig — both fail or are
//     undesirable in a fresh chroot without a working runtime.
//   - %_missing_build_ids_terminate_build: don't fail the build when stripped
//     binaries lack build-ids.
//   - %_file_context_file: suppress SELinux file-context lookups since the
//     scratch root has none yet.
//
// Kept in one place so a fix to the macros doesn't need parallel edits in
// every RPM-based backend.
//
// Deprecated: Use BuildRPMMacros instead. This constant is kept for
// documentation purposes.
const RPMMacros = `%_netsharedpath /sys:/proc:/dev
%_install_langs C:en:en_US:en_US.UTF-8
%__brp_mangle_shebangs %{nil}
%_missing_build_ids_terminate_build 0
%_file_context_file %{nil}
%__brp_ldconfig %{nil}
`

// ExtractMacroOptions extracts RPM macro definitions from the options map.
// Options with the "macro." prefix are treated as RPM macros, where the key
// format is "macro.<macro_name>" and the value is the macro definition.
//
// Example options:
//   "macro._dbpath": "/var/lib/rpm"
//   "macro._dbpath_trans": "/var/lib/rpm"
//   "macro._netsharedpath": "/sys:/proc"  // Override default
//
// Returns a map where keys are macro names (without "macro." prefix or leading %)
// and values are macro definitions.
func ExtractMacroOptions(options map[string]string) map[string]string {
	macros := make(map[string]string)
	const prefix = "macro."
	
	for key, value := range options {
		if strings.HasPrefix(key, prefix) {
			macroName := strings.TrimPrefix(key, prefix)
			if macroName != "" {
				macros[macroName] = value
			}
		}
	}
	
	return macros
}
