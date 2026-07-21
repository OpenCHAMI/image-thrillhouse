// SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
//
// SPDX-License-Identifier: MIT

// Package cmdutil holds small command-builders shared between package-manager
// backends. The goal is to keep "rpm does X" and "dpkg does X" knowledge in
// one place instead of duplicated across dnf/zypper and apt/mmdebstrap.
//
// All helpers return commands as []string (argv form) so callers pass them
// to exec.Command / buildah.Run without any shell interpretation — the
// inputs may include user-supplied paths and package names.
package cmdutil

import (
	"path/filepath"
	"strings"
)

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

// APTImportKey returns a `sh -c` command that installs a previously fetched
// key into /etc/apt/trusted.gpg.d/<keyName>.gpg. The key is dearmored when
// possible and copied verbatim otherwise.
//
// keyName makes the destination filename unique per repository. It was
// previously a hardcoded "image-thrillhouse-repo.gpg", which meant a build
// with two apt repos each carrying a `gpg:` key silently clobbered the first
// key with the second — leaving one repo unverifiable. Callers derive keyName
// from the repo (see internal/builder). It is reduced to a safe filename
// component here (see safeKeyName), so an empty or junk value can never
// escape the trusted.gpg.d directory.
//
// The destination and key paths are passed as POSITIONAL arguments to sh
// (referenced inside the script as $1 and $2) rather than interpolated into
// the script text. That removes any quoting surface — even if a future
// refactor pipes user-controlled bytes into rootPath or keyPath, the shell
// only ever sees them as opaque argv strings, never as parseable script
// fragments. The previous implementation used fmt.Sprintf with Go's %q
// verb, which is NOT shell-safe (e.g. \xNN sequences mean different things
// in Go and sh) and was a latent injection vector waiting for the inputs
// to widen.
//
// When rootPath is non-empty (scratch build) the destination lives under
// that root and the command is meant to run on the host.
//
// Returns nil if keyPath is empty.
func APTImportKey(rootPath, keyName, keyPath string) []string {
	if keyPath == "" {
		return nil
	}
	final := "/etc/apt/trusted.gpg.d/" + safeKeyName(keyName) + ".gpg"
	if rootPath != "" {
		final = rootPath + final
	}
	// $0 is the script's "name" slot (we pass "apt-import-key" so error
	// messages from sh stay readable), $1 is the destination, $2 is the key.
	const script = `gpg --dearmor -o "$1" "$2" 2>/dev/null || cp "$2" "$1"`
	return []string{"sh", "-c", script, "apt-import-key", final, keyPath}
}

// safeKeyName reduces an arbitrary, repo-derived name to a single safe
// filename component for use under /etc/apt/trusted.gpg.d/. It strips any
// directory part (so "../../etc/passwd" can't traverse out), restricts the
// charset to [A-Za-z0-9._-], and trims leading/trailing dots and dashes.
// If nothing usable survives it falls back to a fixed name so the caller
// still gets a valid, if non-unique, destination.
func safeKeyName(name string) string {
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), ".-")
	if s == "" {
		return "image-thrillhouse-repo"
	}
	return s
}
