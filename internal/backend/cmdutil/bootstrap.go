package cmdutil

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
