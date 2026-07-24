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
	"regexp"
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

// APTKeyringsDir is the directory apt-style backends place per-repository GPG
// keyrings in. It deliberately is NOT /etc/apt/trusted.gpg.d: a key dropped
// there is trusted to sign *every* repository (a global-trust downgrade that
// modern apt warns about), whereas a key under /etc/apt/keyrings is only used
// by a source entry that names it via `signed-by=`. See APTKeyringPath and
// APTWireRepoContent, which together place the key here and point the repo at
// it.
const APTKeyringsDir = "/etc/apt/keyrings"

// APTKeyringPath returns the in-image path of the dedicated keyring for the
// repository identified by keyName: /etc/apt/keyrings/<safe>.gpg. keyName is
// reduced to a safe filename component (see safeKeyName), so an empty or junk
// value can never escape the keyrings directory.
//
// Both the key-import command (APTImportKey) and the source-entry wiring
// (APTWireRepoContent) derive the path from this one function, so the
// `signed-by=` reference in a repo file always matches where the key is
// actually placed.
func APTKeyringPath(keyName string) string {
	return APTKeyringsDir + "/" + safeKeyName(keyName) + ".gpg"
}

// APTImportKey returns a `sh -c` command that installs a previously fetched
// key into /etc/apt/keyrings/<keyName>.gpg (see APTKeyringPath). The key is
// dearmored when possible and copied verbatim otherwise, and the keyrings
// directory is created first because — unlike trusted.gpg.d — it does not
// exist on a stock Debian/Ubuntu image.
//
// keyName makes the destination filename unique per repository. It was
// previously a hardcoded "image-thrillhouse-repo.gpg", which meant a build
// with two apt repos each carrying a `gpg:` key silently clobbered the first
// key with the second — leaving one repo unverifiable. Callers derive keyName
// from the repo (see internal/builder).
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
	final := APTKeyringPath(keyName)
	if rootPath != "" {
		final = rootPath + final
	}
	// $0 is the script's "name" slot (we pass "apt-import-key" so error
	// messages from sh stay readable), $1 is the destination, $2 is the key.
	// "${1%/*}" strips the filename to give the keyrings directory to mkdir.
	const script = `mkdir -p "${1%/*}" && { gpg --dearmor -o "$1" "$2" 2>/dev/null || cp "$2" "$1"; }`
	return []string{"sh", "-c", script, "apt-import-key", final, keyPath}
}

// safeKeyName reduces an arbitrary, repo-derived name to a single safe
// filename component for use under /etc/apt/keyrings/. It strips any
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

// APTWireRepoContent links an apt source file's content to the dedicated
// keyring the builder imports for it (APTKeyringPath(keyName)) by ensuring the
// source entry references that keyring via `signed-by=`. This is required for
// correctness under modern apt: a key placed in /etc/apt/keyrings is NOT
// trusted unless a source explicitly names it, so moving keys out of the old
// global-trust directory (/etc/apt/trusted.gpg.d) without this wiring would
// leave repos unverifiable.
//
// It handles both apt source formats:
//   - one-line (.list / sources.list): `deb [opts] URI suite comps` — the
//     `signed-by=<keyring>` option is added to each deb/deb-src line.
//   - deb822 (.sources): a `Signed-By: <keyring>` field is added to each
//     source stanza.
//
// Entries (lines or stanzas) that already specify a keyring — the user pinned
// their own `signed-by=`/`Signed-By:` — are left untouched, so an explicit
// choice always wins. Comments and blank lines are preserved verbatim.
//
// keyName == "" means the repo has no builder-managed key; the content is
// returned unchanged. Content whose format isn't recognized as apt one-line or
// deb822 is also returned unchanged rather than mangled.
func APTWireRepoContent(content, keyName string) string {
	if keyName == "" {
		return content
	}
	keyring := APTKeyringPath(keyName)
	switch {
	case aptIsOneLine(content):
		return aptInjectOneLine(content, keyring)
	case aptIsDeb822(content):
		return aptInjectDeb822(content, keyring)
	default:
		return content
	}
}

var (
	// aptOneLineRe matches the start of a one-line source entry: a `deb` or
	// `deb-src` token followed by whitespace (optionally indented).
	aptOneLineRe = regexp.MustCompile(`(?m)^[ \t]*deb(-src)?[ \t]`)
	// aptDeb822FieldRe matches the deb822 fields that mark a real source
	// stanza (as opposed to a comment-only block).
	aptDeb822FieldRe = regexp.MustCompile(`(?mi)^[ \t]*(Types|URIs):`)
	// aptDeb822SignedByRe matches an existing Signed-By field in a stanza.
	aptDeb822SignedByRe = regexp.MustCompile(`(?mi)^[ \t]*Signed-By:`)
)

func aptIsOneLine(content string) bool { return aptOneLineRe.MatchString(content) }
func aptIsDeb822(content string) bool  { return aptDeb822FieldRe.MatchString(content) }

// aptInjectOneLine adds `signed-by=<keyring>` to every deb/deb-src line that
// doesn't already carry a signed-by option, merging into an existing `[...]`
// option group when present.
func aptInjectOneLine(content, keyring string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		indent := line[:len(line)-len(trimmed)]

		var kw string
		switch {
		case strings.HasPrefix(trimmed, "deb-src") && len(trimmed) > 7 && isSpace(trimmed[7]):
			kw = "deb-src"
		case strings.HasPrefix(trimmed, "deb") && len(trimmed) > 3 && isSpace(trimmed[3]):
			kw = "deb"
		default:
			continue // comment, blank, or not a source line
		}
		if strings.Contains(trimmed, "signed-by=") {
			continue // user already pinned a keyring
		}

		rest := strings.TrimLeft(trimmed[len(kw):], " \t")
		opt := "signed-by=" + keyring
		if strings.HasPrefix(rest, "[") {
			end := strings.Index(rest, "]")
			if end == -1 {
				continue // malformed option group, leave untouched
			}
			inside := strings.TrimSpace(rest[1:end])
			after := rest[end+1:]
			lines[i] = indent + kw + " [" + inside + " " + opt + "]" + after
		} else {
			lines[i] = indent + kw + " [" + opt + "] " + rest
		}
	}
	return strings.Join(lines, "\n")
}

// aptInjectDeb822 appends a `Signed-By: <keyring>` field to every source
// stanza that lacks one. Stanzas are separated by blank lines; a stanza is a
// "source" stanza if it carries a Types: or URIs: field.
func aptInjectDeb822(content, keyring string) string {
	lines := strings.Split(content, "\n")
	var out []string
	var stanza []string

	flush := func() {
		if len(stanza) == 0 {
			return
		}
		block := strings.Join(stanza, "\n")
		if aptDeb822FieldRe.MatchString(block) && !aptDeb822SignedByRe.MatchString(block) {
			stanza = append(stanza, "Signed-By: "+keyring)
		}
		out = append(out, stanza...)
		stanza = nil
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			out = append(out, line)
			continue
		}
		stanza = append(stanza, line)
	}
	flush()
	return strings.Join(out, "\n")
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }
