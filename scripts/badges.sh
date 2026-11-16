#!/bin/sh
# Write the README's badges into docs/assets/badges/. GitHub shows an image from
# another site through a proxy that gives up after about four seconds, and
# shields.io often takes longer, so badges linked from there showed as broken
# images. A file in the repository is served without that proxy. It also puts
# the newest release in the install commands and file names of the README and
# docs/setup.md. Run it once a release's section is in CHANGELOG.md, and after
# the tested zellij releases change:
#
#   make badges
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
dir=docs/assets/badges
mkdir -p "$dir"

# The newest release is the first "## [x.y.z]" heading: "## [Unreleased]" has no
# digit after its bracket.
version=$(awk '/^## \[[0-9]/ { gsub(/[][]/, "", $2); print $2; exit }' CHANGELOG.md)
[ -n "$version" ] || { echo "CHANGELOG.md names no release" >&2; exit 1; }

# The zellij releases the integration tests run, oldest first. The badge gives
# the minor version of the first and of the last.
zellijs=$(awk -F'[()]' '/^default_versions=/ { print $2 }' test/integration/run.sh)
first=$(echo "$zellijs" | awk '{ split($1, v, "."); print v[1] "." v[2] }')
last=$(echo "$zellijs" | awk '{ split($NF, v, "."); print v[1] "." v[2] }')
if [ -z "$first" ] || [ -z "$last" ]; then
  echo "test/integration/run.sh names no zellij release" >&2
  exit 1
fi

# badge NAME SPEC writes shields.io's static badge SPEC (label-message-color,
# spaces as %20, "|" as %7C) to NAME.svg, whole or not at all.
badge() {
  tmp="$dir/$1.svg.tmp"
  curl -fsS --retry 3 --max-time 30 -o "$tmp" "https://img.shields.io/badge/$2.svg"
  # An error page in place of a badge must not replace the one there is.
  grep -q '<svg' "$tmp" || { echo "shields.io sent no SVG for $1" >&2; rm -f "$tmp"; exit 1; }
  mv "$tmp" "$dir/$1.svg"
}

badge release "release-v$version-ea7233"
badge license "license-MIT-2a78d6"
badge demo "demo-ghcr.io-2a78d6"
badge runs-on "runs%20on-Linux%20%7C%20macOS%20%7C%20Windows-555"
badge zellij "zellij-$first%20to%20$last-555"

# The README's alt texts say the same, for a reader without images.
sed -i \
  -e "s/alt=\"Release v[0-9][0-9.]*\"/alt=\"Release v$version\"/" \
  -e "s/alt=\"zellij [0-9.]* to [0-9.]*\"/alt=\"zellij $first to $last\"/" \
  README.md

# The install commands and the release's file names in the README and the setup
# guide name the newest release too: each release's installer installs that
# release, so a command naming an older one would install the older one. The
# words "from 0.15.0 on" and the like have no v and stay.
sed -i -E \
  -e "s|zellij-reaper/releases/download/v[0-9]+\.[0-9]+\.[0-9]+/|zellij-reaper/releases/download/v$version/|g" \
  -e "s|zellij-reaper-v[0-9]+\.[0-9]+\.[0-9]+-|zellij-reaper-v$version-|g" \
  -e "s|^version=v[0-9]+\.[0-9]+\.[0-9]+\$|version=v$version|" \
  -e "s|^(# 1\. Install )v[0-9]+\.[0-9]+\.[0-9]+|\1v$version|" \
  README.md docs/setup.md
echo "wrote $dir: release v$version, zellij $first to $last; the docs' install commands name v$version"
