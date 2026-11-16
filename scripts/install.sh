#!/bin/sh
# Install zellij-reaper on Linux or macOS from a GitHub release and start its
# timers. Windows takes the release's install.ps1. Each release carries this
# script under its own name, with the release written in, and installs that
# release:
#
#   curl -fsSL https://github.com/kanggu12/zellij-reaper/releases/download/v0.15.0/zellij-reaper-v0.15.0-install.sh | sh
#
# Settings, all optional:
#   ZELLIJ_REAPER_VERSION   release to install, like v0.15.0 (default: the release
#                           this script came with; this copy in the repository
#                           installs the latest)
#   ZELLIJ_REAPER_BIN_DIR   where the binary goes (default: ~/.local/bin)
#   ZELLIJ_REAPER_NO_TIMER  set to 1 to install the binary only
#   ZELLIJ_REAPER_NO_SETUP  set to 1 to start the timers with the defaults, without
#                           the setup screen a first install in a terminal gets
#   ZELLIJ_REAPER_BASE_URL  where releases are downloaded from (mirrors, tests)
set -eu

# The release this script installs. scripts/installers.sh writes it into the
# copy each release carries; empty here, so a run from the repository installs
# the latest.
release=""

repo=kanggu12/zellij-reaper
base=${ZELLIJ_REAPER_BASE_URL:-https://github.com/$repo/releases/download}
# From GitHub, HTTPS only, redirects too, and TLS 1.2 or newer: a downgrade
# on the way cannot hand over another binary with its own checksums. A mirror
# named in ZELLIJ_REAPER_BASE_URL is taken as given.
secure="--proto =https --proto-redir =https --tlsv1.2"
[ -n "${ZELLIJ_REAPER_BASE_URL:-}" ] && from="" || from=$secure
bin_dir=${ZELLIJ_REAPER_BIN_DIR:-$HOME/.local/bin}

say() { printf '%s\n' "$*"; }
fail() {
  printf 'zellij-reaper install: %s\n' "$*" >&2
  exit 1
}

# Linux and macOS take this script; Windows takes install.ps1. The words are
# those of the release's file names: macos, and x86_64 or arm64 as uname -m
# says them (aarch64 on Linux is arm64).
case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=macos ;;
  *) fail "no release for $(uname -s): on Windows, use the release's install.ps1" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=x86_64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) fail "no release for $(uname -m)" ;;
esac
command -v curl >/dev/null 2>&1 || fail "curl is needed"
command -v tar >/dev/null 2>&1 || fail "tar is needed"

# The release asked for, else the one this script came with, else the latest:
# where github.com/<repo>/releases/latest redirects.
version=${ZELLIJ_REAPER_VERSION:-$release}
if [ -z "$version" ]; then
  # shellcheck disable=SC2086 # the options are words of their own
  url=$(curl $secure -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
    fail "could not find the latest release; on a network that cannot reach GitHub, set ZELLIJ_REAPER_BASE_URL and ZELLIJ_REAPER_VERSION, or follow docs/setup.md#without-internet-access"
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
# Releases before 0.15.0 named their files otherwise: each installs with the
# install.sh it carries.
case "$version" in
  v0.[0-9].* | v0.1[0-4].*) fail "$version names its files otherwise: install it with the install.sh of that release" ;;
esac

# Every file of a release starts with the name and the release, and the archive
# holds a folder of the same name: zellij-reaper-v0.15.0-linux-x86_64.
name="zellij-reaper-$version-$os-$arch"
archive="$name.tar.gz"
sums="zellij-reaper-$version-checksums.txt"
tmp=$(mktemp -d)
# The download goes away however the script ends. A signal also stops the
# install there, with the status a shell gives for it, instead of going on
# with whatever step was interrupted.
trap 'rm -rf "$tmp"' EXIT
trap 'rm -rf "$tmp"; exit 130' INT
trap 'rm -rf "$tmp"; exit 143' TERM

say "downloading $archive"
# shellcheck disable=SC2086 # the options are words of their own
curl $from -fsSL "$base/$version/$archive" -o "$tmp/$archive" || fail "could not download $archive"
# shellcheck disable=SC2086
curl $from -fsSL "$base/$version/$sums" -o "$tmp/$sums" || fail "could not download $sums"

# Refuse a download whose checksum does not match the release's list.
want=$(grep " $archive\$" "$tmp/$sums" | cut -d' ' -f1)
[ -n "$want" ] || fail "$archive is not in $sums"
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$archive" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$archive" | cut -d' ' -f1)
fi
[ "$got" = "$want" ] || fail "checksum mismatch for $archive"

# Only the binary, out of the archive's folder.
tar -xzf "$tmp/$archive" -C "$tmp" "$name/zellij-reaper"
mkdir -p "$bin_dir"
install -m 0755 "$tmp/$name/zellij-reaper" "$bin_dir/zellij-reaper"
say "installed $bin_dir/zellij-reaper"

# The binary is in place whatever comes of the timers, so their failing (no
# scheduler answers on this machine, say) does not end the script on the spot:
# the status is kept, and what follows still applies.
status=0
if [ "${ZELLIJ_REAPER_NO_TIMER:-}" = 1 ]; then
  say "run 'zellij-reaper install' to start the timers"
elif [ "${ZELLIJ_REAPER_NO_SETUP:-}" != 1 ] && [ -t 1 ] && (: </dev/tty) 2>/dev/null; then
  # In a terminal: on a first install the setup screen, which shows what the
  # first pass would reap; on an upgrade only the timers are written again.
  # Piped through sh, this script's own input is the script, so the screen
  # reads the terminal. The check runs in a subshell: a redirection that
  # fails on `:` ends a POSIX shell, not only the test.
  "$bin_dir/zellij-reaper" setup --installer </dev/tty || status=$?
else
  "$bin_dir/zellij-reaper" install || status=$?
fi

# Tell people when the binary is not on their PATH yet.
case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) say "add $bin_dir to your PATH to run zellij-reaper by name" ;;
esac

# The timers did not start, for the reason the binary gave above. The script
# ends with the binary's status, so whatever ran it sees an install that is
# not finished.
if [ "$status" -ne 0 ]; then
  printf "zellij-reaper install: the binary is in place, the timers are not: run '%s install' to try again\n" "$bin_dir/zellij-reaper" >&2
  exit "$status"
fi
