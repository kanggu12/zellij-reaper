#!/bin/sh
# Write the installers a release carries: scripts/install.sh and install.ps1
# with the release they install written in, so each installs the release it
# came with and its name can say which. goreleaser runs it before a build
# (.github/goreleaser.yaml), with the release and the folder to write to:
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
# The line each script reads its release from; empty in the repository,
# where a script installs the latest release.
sed "s/^release=\"\"\$/release=\"$version\"/" "$root/scripts/install.sh" >"$out/install.sh"
sed "s/^  \$release = ''\$/  \$release = '$version'/" "$root/scripts/install.ps1" >"$out/install.ps1"

# Each line must have been there to be written: a script that still installs
# "the latest" would go out under a name that says otherwise.
grep -qx "release=\"$version\"" "$out/install.sh" || { echo "installers: install.sh has no release line" >&2; exit 1; }
grep -qx "  \$release = '$version'" "$out/install.ps1" || { echo "installers: install.ps1 has no release line" >&2; exit 1; }
echo "wrote $out/install.sh and $out/install.ps1 for $version"
