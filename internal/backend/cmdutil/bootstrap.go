package cmdutil

import (
	"context"
	"log/slog"

	"github.com/travisbcotton/image-build/internal/config"
	"github.com/travisbcotton/image-build/internal/container"
)

// RPMMacrosPath is the canonical destination for the shared image-build RPM
// macros under /etc/rpm. Both dnf and zypper scratch bootstraps write here.
const RPMMacrosPath = "/etc/rpm/macros.image-build"

// WriteRPMMacros installs the shared RPMMacros content into the container at
// RPMMacrosPath. It centralises the boilerplate the dnf and zypper backends
// both ran in their Bootstrap methods — including the WARN-on-failure
// posture: scratch installs occasionally race the filesystem-layer rpm and
// having a clean error message here is more useful than a failed build.
func WriteRPMMacros(ctx context.Context, c container.Container, log *slog.Logger) {
	if err := c.WriteFile(ctx, config.File{
		Path:    RPMMacrosPath,
		Content: RPMMacros,
	}); err != nil {
		log.Warn("Failed to write RPM macros", "error", err)
	}
}

// RPMMacros is the macros.image-build file content that both the dnf and
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
const RPMMacros = `%_netsharedpath /sys:/proc:/dev
%_install_langs C:en:en_US:en_US.UTF-8
%__brp_mangle_shebangs %{nil}
%_missing_build_ids_terminate_build 0
%_file_context_file %{nil}
%__brp_ldconfig %{nil}
`
