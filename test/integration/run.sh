#!/usr/bin/env bash
# Run the integration scenarios against real zellij releases.
#
#   test/integration/run.sh                  # the releases CI covers
#   test/integration/run.sh 0.45.1 0.44.3    # chosen releases
#   test/integration/run.sh main             # zellij's main branch, built first
#                                            # to test/integration/.cache/zellij-main
#
# Every release runs in its own throwaway container, so the sessions the
# scenarios create, reap and kill never touch the machine running the tests.
set -euo pipefail

# The releases CI covers: the newest patch of each supported minor.
default_versions=(0.43.1 0.44.3 0.45.1)
versions=("$@")
[ "${#versions[@]}" -gt 0 ] || versions=("${default_versions[@]}")

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(cd "$here/../.." && pwd)
cache="$here/.cache"
mkdir -p "$cache"

# zellij publishes musl builds, which run on any Linux image.
case "$(uname -m)" in
  x86_64) arch=x86_64 ;;
  aarch64 | arm64) arch=aarch64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 2 ;;
esac

# A static binary, so it runs in the container whatever its libc.
echo "building zellij-reaper"
(cd "$root" && CGO_ENABLED=0 go build -o "$cache/zellij-reaper" ./cmd/zellij-reaper)

echo "building the test image"
docker build -q -t zellij-reaper-it "$here" >/dev/null

failed=0
for v in "${versions[@]}"; do
  bin="$cache/zellij-$v"
  # main is no release: the canary workflow builds it, and its word lists
  # come from the branch.
  ref="v$v"
  if [ "$v" = main ]; then
    ref=main
    if [ ! -x "$bin" ]; then
      echo "zellij main: build it to $bin first" >&2
      failed=1
      continue
    fi
  fi
  # Download each release once; later runs reuse it.
  if [ ! -x "$bin" ]; then
    echo "downloading zellij $v"
    curl -fsSL "https://github.com/zellij-org/zellij/releases/download/v$v/zellij-$arch-unknown-linux-musl.tar.gz" | tar -xz -C "$cache" zellij
    mv "$cache/zellij" "$bin"
  fi
  echo "== zellij $v"
  # The release's own word lists: a name zellij can make up has to read as made up.
  words="$cache/sessions-$v.rs"
  [ -s "$words" ] || curl -fsSL "https://raw.githubusercontent.com/zellij-org/zellij/$ref/zellij-utils/src/sessions.rs" -o "$words"
  if ! (cd "$root" && ZELLIJ_SESSIONS_RS="$words" go test -count=1 -run '^TestKnowsTheWordsOfARelease$' ./internal/core/rename); then
    failed=1
  fi
  # No network: nothing here may need one.
  if ! docker run --rm --network none \
      -v "$bin:/usr/local/bin/zellij:ro" \
      -v "$cache/zellij-reaper:/usr/local/bin/zellij-reaper:ro" \
      -v "$here/scenarios.sh:/scenarios.sh:ro" \
      zellij-reaper-it /scenarios.sh; then
    failed=1
  fi
done
exit "$failed"
