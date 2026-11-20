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
# A "-dirty" suffix is appended when tracked files have uncommitted changes,
# e.g. v0.0.27-3-gabc1234-dirty. debian/changelog and image-thrillhouse.spec
# are ignored for this: the packaging steps rewrite them (gen-changelog.sh,
# the RPM Version line) before building.
#
# With --package, print the package (deb/rpm) version instead, derived from
# the version above or from the VERSION argument if given:
#
#   v0.0.27                  -> 0.0.27
#   v0.0.27-3-gabc1234       -> 0.0.27+3.gabc1234
#   v0.0.27-3-gabc1234-dirty -> 0.0.27+3.gabc1234~dirty
#   v0.0.28-rc1              -> 0.0.28~rc1
#
# RPM forbids '-' in Version, and '+' sorts after the bare tag in both dpkg
# and rpm, so an untagged build upgrades the release it is based on and is
# itself superseded by the next release. Any other '-' becomes '~', which
# sorts before the version it follows, so a pre-release or a dirty build
# never outranks the corresponding clean one.
#
# --from-package VERSION reverses that, for builds from a source package with
# no .git (debian/rules, the RPM spec), so the binary reports the same string
# a git checkout of that commit would: 0.0.27+3.gabc1234 -> v0.0.27-3-gabc1234.
#
# Exits non-zero (printing nothing) unless the source tree is itself the top
# level of a git work tree, so callers that can build from a release tarball
# (debian/rules) can fall back to another source of truth. POSIX sh: CI runs
# this under sh in minimal containers.
set -eu

mode=
case "${1:-}" in
  --package|--from-package)
    mode=$1
    shift
    ;;
esac

git_version() {
  cd "$(dirname "$0")/.."
  # Require this checkout to be the work tree's top level, not merely inside
  # one: a release tarball unpacked within some other repo (a packaging repo,
  # a git-managed home directory) must fall back too, not stamp that repo's
  # tags.
  [ "$(git rev-parse --show-toplevel 2>/dev/null)" = "$(pwd -P)" ] || exit 1

  match='v[0-9]*.[0-9]*.[0-9]*'
  if ! version=$(git describe --tags --match "$match" HEAD 2>/dev/null); then
    msg="no release tags ($match) found; version v0.0.0 is a placeholder, not a real release version (fetch tags, e.g. 'git fetch --tags')"
    # Under GitHub Actions, emit it as an annotation so it shows on the run.
    if [ -n "${GITHUB_ACTIONS:-}" ]; then
      echo "::warning::version.sh: $msg" >&2
    else
      echo "version.sh: warning: $msg" >&2
    fi
    version="v0.0.0-0-g$(git rev-parse --short HEAD)"
  fi

  # Refresh stat info first (as `git describe --dirty` does), so files that
  # were merely copied or touched, e.g. by Docker's COPY, don't count.
  git update-index -q --refresh >/dev/null 2>&1 || true
  if ! git diff-index --quiet HEAD -- . \
      ':(exclude)debian/changelog' ':(exclude)image-thrillhouse.spec'; then
    version="${version}-dirty"
  fi
  echo "$version"
}

case "$mode" in
  "")
    git_version
    ;;
  --package)
    if [ $# -gt 0 ]; then
      version=$1
    else
      version=$(git_version)
    fi
    echo "$version" | sed -e 's/^v//' \
      -e 's/-\([0-9][0-9]*\)-g\([0-9a-f][0-9a-f]*\)\(-dirty\)\{0,1\}$/+\1.g\2\3/' \
      -e 's/-/~/g'
    ;;
  --from-package)
    echo "${1:?--from-package needs a VERSION}" | sed -e 's/^/v/' \
      -e 's/+\([0-9][0-9]*\)\.g\([0-9a-f][0-9a-f]*\)/-\1-g\2/' \
      -e 's/~/-/g'
    ;;
esac
