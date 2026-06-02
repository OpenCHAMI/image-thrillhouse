// Package cmdutil holds small command-builders shared between package-manager
// backends. The goal is to keep "rpm does X" and "dpkg does X" knowledge in
// one place instead of duplicated across dnf/zypper and apt/mmdebstrap.
//
// All helpers return commands as []string (argv form) so callers pass them
// to exec.Command / buildah.Run without any shell interpretation — the
// inputs may include user-supplied paths and package names.
package cmdutil

import "fmt"

// RPMRemove returns the `rpm -e --nodeps [--root rootPath] <packages…>`
// command shared by the dnf and zypper backends. When rootPath is empty
// the command runs against the live container's RPM database; otherwise
// it targets the bootstrapped scratch root on the host.
//
// Returns nil if there are no packages — the caller should treat that as a
// no-op rather than running rpm with no arguments.
func RPMRemove(rootPath string, packages []string) []string {
	if len(packages) == 0 {
		return nil
	}
	cmd := make([]string, 0, 5+len(packages))
	cmd = append(cmd, "rpm")
	if rootPath != "" {
		cmd = append(cmd, "--root", rootPath)
	}
	cmd = append(cmd, "-e", "--nodeps")
	cmd = append(cmd, packages...)
	return cmd
}

// RPMImportKey returns `rpm --import [--root rootPath] <keyPath>`. rpm
// accepts armored and binary keys, so no pre-processing is required. The
// caller is expected to have placed the key bytes at keyPath beforehand.
//
// Returns nil if keyPath is empty.
func RPMImportKey(rootPath, keyPath string) []string {
	if keyPath == "" {
		return nil
	}
	if rootPath != "" {
		return []string{"rpm", "--root", rootPath, "--import", keyPath}
	}
	return []string{"rpm", "--import", keyPath}
}

// DPKGRemove returns the `dpkg [--root rootPath] --remove --force-depends
// <packages…>` command shared by the apt and mmdebstrap backends.
//
// Returns nil if there are no packages.
func DPKGRemove(rootPath string, packages []string) []string {
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

// APTImportKey returns a single `sh -c` command that installs a previously
// fetched key into /etc/apt/trusted.gpg.d/. The key is dearmored when
// possible and copied verbatim otherwise. Only paths controlled by this
// codebase are interpolated into the script — never a user-supplied URL —
// so there is no shell-injection surface even though sh is used.
//
// When rootPath is non-empty (scratch build) the destination lives under
// that root and the command is meant to run on the host.
//
// Returns nil if keyPath is empty.
func APTImportKey(rootPath, keyPath string) []string {
	if keyPath == "" {
		return nil
	}
	final := "/etc/apt/trusted.gpg.d/image-build-repo.gpg"
	if rootPath != "" {
		final = rootPath + "/etc/apt/trusted.gpg.d/image-build-repo.gpg"
	}
	script := fmt.Sprintf("gpg --dearmor -o %q %q 2>/dev/null || cp %q %q",
		final, keyPath, keyPath, final)
	return []string{"sh", "-c", script}
}
