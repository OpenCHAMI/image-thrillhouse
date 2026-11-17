#!/bin/sh
# SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
#
# SPDX-License-Identifier: MIT
# Print the version to stamp into image-thrillhouse builds, derived from git:
#
#   HEAD is a release tag     -> that tag, e.g. v0.0.27
#   HEAD is past a release tag -> the most recent tag + "-debug", e.g. v0.0.27-debug
#   no release tags at all    -> v0.0.0-debug
#
# Exits non-zero (printing nothing) outside a git work tree, so callers that
# can build from a release tarball (debian/rules) can fall back to another
# source of truth. POSIX sh: CI runs this under sh in minimal containers.
set -eu

cd "$(dirname "$0")/.."
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 1

match='v[0-9]*.[0-9]*.[0-9]*'
if tag=$(git describe --tags --exact-match --match "$match" HEAD 2>/dev/null); then
  echo "$tag"
elif tag=$(git describe --tags --abbrev=0 --match "$match" HEAD 2>/dev/null); then
  echo "${tag}-debug"
else
  echo "v0.0.0-debug"
fi
