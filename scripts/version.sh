#!/usr/bin/env bash
# Print the version this build should report, derived from git.
#
# The base version comes from the source tree (the fallback constant in
# internal/app/app.go), so bumping the base version stays a one-line edit in one
# place rather than a change to this script. Git then supplies the revision
# detail on top:
#
#   exact tag          -> 0.4.0
#   N commits past tag -> 0.4.0-dev.N.g<sha>
#   no reachable tag   -> 0.4.0-dev.g<sha>
#   dirty tree         -> adds "-dirty"
#   not a git repo     -> 0.4.0  (the base version alone)
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The base version is the single source of truth for the major.minor.patch part.
base="$(
  sed -n 's/^var version = "\(.*\)"$/\1/p' "$repo_root/internal/app/app.go" | head -1
)"
if [ -z "$base" ]; then
  echo "version.sh: could not read the base version from internal/app/app.go" >&2
  exit 1
fi

# Strip any suffix already present, so a base like "0.4.0-dev" cannot compound.
base="${base%%-*}"

# Outside a git checkout (a tarball, a vendored copy) the base version stands
# alone rather than failing the build.
if ! git -C "$repo_root" rev-parse --git-dir >/dev/null 2>&1; then
  echo "$base"
  exit 0
fi

sha="$(git -C "$repo_root" rev-parse --short=7 HEAD 2>/dev/null || echo unknown)"
dirty=""
if ! git -C "$repo_root" diff --quiet 2>/dev/null \
  || ! git -C "$repo_root" diff --cached --quiet 2>/dev/null; then
  dirty="-dirty"
fi

# Prefer the newest tag reachable from HEAD. A tag that does not match the base
# version in the tree is ignored: the tree constant is authoritative, and a stale
# tag should not silently rename the build.
tag="$(git -C "$repo_root" describe --tags --abbrev=0 2>/dev/null || true)"
tag_version="${tag#v}"

if [ -n "$tag_version" ] && [ "$tag_version" = "$base" ]; then
  commits="$(git -C "$repo_root" rev-list --count "${tag}..HEAD" 2>/dev/null || echo 0)"
  if [ "$commits" -eq 0 ] && [ -z "$dirty" ]; then
    # Exactly at the tagged release.
    echo "$base"
  elif [ "$commits" -eq 0 ]; then
    echo "$base+$sha$dirty"
  else
    echo "$base-dev.$commits.g$sha$dirty"
  fi
else
  # No usable tag: report the version plus the revision it was cut from.
  echo "$base-dev.g$sha$dirty"
fi
