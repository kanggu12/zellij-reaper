#!/bin/sh
# Install zellij-reaper from a GitHub release and start its timer.
#
#   curl -fsSL https://github.com/kanggu12/zellij-reaper/releases/latest/download/install.sh | sh
#
# Settings, all optional:
#   ZELLIJ_REAPER_VERSION   release to install, like v0.2.0 (default: the latest)
#   ZELLIJ_REAPER_BIN_DIR   where the binary goes (default: ~/.local/bin)
#   ZELLIJ_REAPER_NO_TIMER  set to 1 to install the binary only
#   ZELLIJ_REAPER_BASE_URL  where releases are downloaded from (mirrors, tests)
set -eu

repo=kanggu12/zellij-reaper
base=${ZELLIJ_REAPER_BASE_URL:-https://github.com/$repo/releases/download}
bin_dir=${ZELLIJ_REAPER_BIN_DIR:-$HOME/.local/bin}

say() { printf '%s\n' "$*"; }
fail() {
  printf 'zellij-reaper install: %s\n' "$*" >&2
  exit 1
}

# zellij-reaper reads /proc and talks to systemd: Linux only.
[ "$(uname -s)" = Linux ] || fail "zellij-reaper runs on Linux"
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "no release for $(uname -m)" ;;
esac
command -v curl >/dev/null 2>&1 || fail "curl is needed"
command -v tar >/dev/null 2>&1 || fail "tar is needed"

# The latest release is where github.com/<repo>/releases/latest redirects.
version=${ZELLIJ_REAPER_VERSION:-}
if [ -z "$version" ]; then
  url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
    fail "could not find the latest release"
  # A release redirects to its tag. With none published yet, GitHub lands on
  # the list of releases instead, whose last word is not a version.
  case "$url" in
    */releases/tag/?*) version=${url##*/} ;;
    *) fail "no release published yet; set ZELLIJ_REAPER_VERSION or install from source" ;;
  esac
fi
case "$version" in
  v*) ;;
  *) version=v$version ;;
esac

archive="zellij-reaper_${version#v}_linux_${arch}.tar.gz"
tmp=$(mktemp -d)
# The download goes away however the script ends. A signal also stops the
# install there, with the status a shell gives for it, instead of going on
# with whatever step was interrupted.
trap 'rm -rf "$tmp"' EXIT
trap 'rm -rf "$tmp"; exit 130' INT
trap 'rm -rf "$tmp"; exit 143' TERM

say "downloading zellij-reaper $version for linux/$arch"
curl -fsSL "$base/$version/$archive" -o "$tmp/$archive" || fail "could not download $archive"
curl -fsSL "$base/$version/checksums.txt" -o "$tmp/checksums.txt" || fail "could not download checksums.txt"

# Refuse a download whose checksum does not match the release's list.
want=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)
[ -n "$want" ] || fail "$archive is not in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)
fi
[ "$got" = "$want" ] || fail "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp" zellij-reaper
mkdir -p "$bin_dir"
install -m 0755 "$tmp/zellij-reaper" "$bin_dir/zellij-reaper"
say "installed $bin_dir/zellij-reaper"

if [ "${ZELLIJ_REAPER_NO_TIMER:-}" = 1 ]; then
  say "run 'zellij-reaper install' to start the timer"
else
  "$bin_dir/zellij-reaper" install
fi

# Tell people when the binary is not on their PATH yet.
case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) say "add $bin_dir to your PATH to run zellij-reaper by name" ;;
esac
