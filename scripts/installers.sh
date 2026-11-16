#!/bin/sh
# Write the installers a release carries: scripts/install.sh and install.ps1
# with the release they install written in, so each installs the release it
# came with and says so in its usage lines. install.sh goes out twice, as
# linux-installer.sh and macos-installer.sh, the same script under each system's
# name; install.ps1 as windows-installer.ps1. goreleaser runs it before a build
# (.github/goreleaser.yaml), with the release and the folder to write to, and
# puts the name and the version before each name:
#
#   scripts/installers.sh v0.16.1 build/installers
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
# The release's address has the v; the files' names have the version without it.
plain=${version#v}

mkdir -p "$out"
# The line each script reads its release from, empty in the repository,
# where a script installs the latest release; and the usage lines at the top,
# vX.Y.Z in the address and X.Y.Z in the file's name, so a copy shows the
# commands that fetch it.
sed -e "s/^release=\"\"\$/release=\"$version\"/" -e "s/vX\.Y\.Z/$version/g" -e "s/X\.Y\.Z/$plain/g" \
  "$root/scripts/install.sh" >"$out/linux-installer.sh"
sed -e "s/^  \$release = ''\$/  \$release = '$version'/" -e "s/vX\.Y\.Z/$version/g" -e "s/X\.Y\.Z/$plain/g" \
  "$root/scripts/install.ps1" >"$out/windows-installer.ps1"
# macOS takes the same script: it installs for the system it runs on.
cp "$out/linux-installer.sh" "$out/macos-installer.sh"

# Each line must have been there to be written: a script that still installs
# "the latest" would go out under a name that says otherwise, and one whose
# usage lines still say X.Y.Z would give commands that fetch nothing.
grep -qx "release=\"$version\"" "$out/linux-installer.sh" || { echo "installers: install.sh has no release line" >&2; exit 1; }
grep -qx "  \$release = '$version'" "$out/windows-installer.ps1" || { echo "installers: install.ps1 has no release line" >&2; exit 1; }
for system in linux macos; do
  grep -qF "releases/download/$version/zellij-reaper-$plain-$system-installer.sh | sh" "$out/$system-installer.sh" ||
    { echo "installers: install.sh has no usage line for $system" >&2; exit 1; }
done
grep -qF "releases/download/$version/zellij-reaper-$plain-windows-installer.ps1 | iex" "$out/windows-installer.ps1" ||
  { echo "installers: install.ps1 has no usage line" >&2; exit 1; }
! grep -q 'X\.Y\.Z' "$out/linux-installer.sh" "$out/macos-installer.sh" "$out/windows-installer.ps1" || { echo "installers: X.Y.Z left in a script" >&2; exit 1; }
echo "wrote $out/linux-installer.sh, macos-installer.sh and windows-installer.ps1 for $version"
