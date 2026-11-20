#!/bin/sh
# SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
#
# SPDX-License-Identifier: MIT
# Print the version to stamp into image-thrillhouse builds, derived from git:
#
#   HEAD is a release tag      -> that tag, e.g. v0.0.27
#   HEAD is past a release tag -> `git describe` output, e.g. v0.0.27-3-gabc1234
#   no release tags at all     -> v0.0.0-0-g<sha>, e.g. v0.0.0-0-gabc1234,
#                                 with a warning on stderr that it is invalid
#
# With --package, print the package (deb/rpm) version instead, derived from
# the version above or from the VERSION argument if given:
#
#   v0.0.27            -> 0.0.27
#   v0.0.27-3-gabc1234 -> 0.0.27+3.gabc1234
#
# RPM forbids '-' in Version, and '+' sorts after the bare tag in both dpkg
# and rpm, so an untagged build upgrades the release it is based on and is
# itself superseded by the next release. Any other '-' becomes '~'.
#
# Exits non-zero (printing nothing) outside a git work tree, so callers that
# can build from a release tarball (debian/rules) can fall back to another
# source of truth. POSIX sh: CI runs this under sh in minimal containers.
set -eu

package=
if [ "${1:-}" = "--package" ]; then
  package=1
  shift
fi

git_version() {
  cd "$(dirname "$0")/.."
  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 1

  match='v[0-9]*.[0-9]*.[0-9]*'
  if ! git describe --tags --match "$match" HEAD 2>/dev/null; then
    msg="no release tags ($match) found; version v0.0.0 is a placeholder, not a real release version (fetch tags, e.g. 'git fetch --tags')"
    # Under GitHub Actions, emit it as an annotation so it shows on the run.
    if [ -n "${GITHUB_ACTIONS:-}" ]; then
      echo "::warning::version.sh: $msg" >&2
    else
      echo "version.sh: warning: $msg" >&2
    fi
    echo "v0.0.0-0-g$(git rev-parse --short HEAD)"
  fi
}

if [ -z "$package" ]; then
  git_version
  exit 0
fi

if [ $# -gt 0 ]; then
  version=$1
else
  version=$(git_version)
fi
echo "$version" | sed -e 's/^v//' \
  -e 's/-\([0-9][0-9]*\)-g\([0-9a-f][0-9a-f]*\)$/+\1.g\2/' \
  -e 's/-/~/g'
