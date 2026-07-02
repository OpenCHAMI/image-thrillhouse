#!/usr/bin/env bash
# SPDX-FileCopyrightText: © 2026 OpenCHAMI a Series of LF Projects, LLC
#
# SPDX-License-Identifier: MIT
# Regenerate debian/changelog and the %changelog section in the RPM spec
# from git tag history. Annotated-tag metadata is preferred; lightweight
# tags fall back to commit author + GITHUB_ACTOR + auto-generated body
# from git log between adjacent release tags.
#
# Configurable via env:
#   PKG            (default: image-thrillhouse)
#   DEB_CHANGELOG  (default: debian/changelog)
#   SPEC           (default: image-thrillhouse.spec)
#   GITHUB_ACTOR   (set automatically inside GitHub Actions)
set -euo pipefail

PKG="${PKG:-image-thrillhouse}"
DEB_CHANGELOG="${DEB_CHANGELOG:-debian/changelog}"
SPEC="${SPEC:-image-thrillhouse.spec}"

# Fallback identity used when a tag has no tagger info (lightweight tag).
fallback_name="${GITHUB_ACTOR:-$(git config --get user.name 2>/dev/null || echo "Release Bot")}"
if [ -n "${GITHUB_ACTOR:-}" ]; then
  fallback_email="${GITHUB_ACTOR}@users.noreply.github.com"
else
  fallback_email="$(git config --get user.email 2>/dev/null || echo "noreply@localhost")"
fi

if ! git tag --list 'v[0-9]*.[0-9]*.[0-9]*' | grep -q .; then
  echo "gen-changelog: no release tags (v[0-9]+.[0-9]+.[0-9]+) found" >&2
  exit 1
fi

deb_out=$(mktemp)
rpm_out=$(mktemp)
trap 'rm -f "$deb_out" "$rpm_out"' EXIT

emit_bullet() {
  # $1 = bullet prefix ("  * " for deb, "- " for rpm)
  # stdin = body text (may already contain bullets we should strip)
  local prefix="$1" line
  while IFS= read -r line; do
    [ -z "${line//[[:space:]]/}" ] && continue
    line="${line#"${line%%[![:space:]]*}"}"     # ltrim
    case "$line" in [-*•]\ *) line="${line:2}" ;; esac
    printf '%s%s\n' "$prefix" "$line"
  done
}

while IFS= read -r TAG; do
  VERSION="${TAG#v}"

  name=$(git for-each-ref --format='%(taggername)' "refs/tags/${TAG}")
  email=$(git for-each-ref --format='%(taggeremail)' "refs/tags/${TAG}")
  date_rfc=$(git for-each-ref --format='%(taggerdate:rfc2822)' "refs/tags/${TAG}")
  date_rpm=$(git for-each-ref --format='%(taggerdate:format:%a %b %d %Y)' "refs/tags/${TAG}")
  msg=$(git for-each-ref --format='%(contents:body)' "refs/tags/${TAG}")

  if [ -z "$name" ]; then
    # Lightweight tag: fall back to commit metadata + fallback identity
    name="$fallback_name"
    email="<${fallback_email}>"
    date_rfc=$(git log -1 --format='%aD' "${TAG}")
    date_rpm=$(git log -1 --format='%ad' --date='format:%a %b %d %Y' "${TAG}")
    msg=""
  fi

  if [ -n "$msg" ]; then
    body="$msg"
  else
    prev=$(git describe --tags --abbrev=0 --match='v[0-9]*.[0-9]*.[0-9]*' "${TAG}^" 2>/dev/null || true)
    if [ -n "$prev" ]; then
      body=$(git log --reverse --pretty='%s' "${prev}..${TAG}")
    else
      body="Initial release"
    fi
  fi

  {
    echo "${PKG} (${VERSION}-1) unstable; urgency=medium"
    echo
    emit_bullet "  * " <<< "$body"
    echo
    printf ' -- %s %s  %s\n\n' "$name" "$email" "$date_rfc"
  } >> "$deb_out"

  {
    printf '* %s %s %s - %s-1\n' "$date_rpm" "$name" "$email" "$VERSION"
    emit_bullet "- " <<< "$body"
    echo
  } >> "$rpm_out"
done < <(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-version:refname)

mkdir -p "$(dirname "$DEB_CHANGELOG")"
mv "$deb_out" "$DEB_CHANGELOG"
echo "gen-changelog: wrote $DEB_CHANGELOG"

awk -v rpm_out="$rpm_out" '
  { print }
  /^%changelog/ {
    while ((getline line < rpm_out) > 0) print line
    close(rpm_out)
    found = 1
    exit
  }
  END {
    if (!found) {
      print "%changelog"
      while ((getline line < rpm_out) > 0) print line
      close(rpm_out)
    }
  }
' "$SPEC" > "${SPEC}.new" && mv "${SPEC}.new" "$SPEC"
echo "gen-changelog: updated %changelog in $SPEC"
