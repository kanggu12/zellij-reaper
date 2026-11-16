#!/bin/sh
# Write the installers a release carries: scripts/install.sh and install.ps1
# with the release they install written in, so each installs the release it
# came with, says so in its usage line, and its name can say which. goreleaser
# runs it before a build (.github/goreleaser.yaml), with the release and the
# folder to write to:
#
#   scripts/installers.sh v0.15.0 build/installers
set -eu

version=$1
out=$2
# Run from anywhere: the scripts are read from the repository this one is in.
root=$(cd "$(dirname "$0")/.." && pwd)

# A release is vX.Y.Z, maybe with a suffix (a snapshot's); anything else
# would end up inside the scripts' quotes.
case "$version" in
  v[0-9]*) ;;
  *) echo "installers: not a release: $version" >&2; exit 1 ;;
esac
case "$version" in
  *[!A-Za-z0-9.+-]*) echo "installers: not a release: $version" >&2; exit 1 ;;
esac

mkdir -p "$out"
# The line each script reads its release from, empty in the repository,
# where a script installs the latest release; and vX.Y.Z in the usage line
# at the top, so a copy shows the command that fetches it.
sed -e "s/^release=\"\"\$/release=\"$version\"/" -e "s/vX\.Y\.Z/$version/g" \
  "$root/scripts/install.sh" >"$out/install.sh"
sed -e "s/^  \$release = ''\$/  \$release = '$version'/" -e "s/vX\.Y\.Z/$version/g" \
  "$root/scripts/install.ps1" >"$out/install.ps1"

# Each line must have been there to be written: a script that still installs
# "the latest" would go out under a name that says otherwise, and one whose
# usage line still says vX.Y.Z would give a command that fetches nothing.
grep -qx "release=\"$version\"" "$out/install.sh" || { echo "installers: install.sh has no release line" >&2; exit 1; }
grep -qx "  \$release = '$version'" "$out/install.ps1" || { echo "installers: install.ps1 has no release line" >&2; exit 1; }
grep -qF "releases/download/$version/zellij-reaper-$version-install.sh | sh" "$out/install.sh" ||
  { echo "installers: install.sh has no usage line" >&2; exit 1; }
grep -qF "releases/download/$version/zellij-reaper-$version-install.ps1 | iex" "$out/install.ps1" ||
  { echo "installers: install.ps1 has no usage line" >&2; exit 1; }
! grep -q 'vX\.Y\.Z' "$out/install.sh" "$out/install.ps1" || { echo "installers: vX.Y.Z left in a script" >&2; exit 1; }
echo "wrote $out/install.sh and $out/install.ps1 for $version"
