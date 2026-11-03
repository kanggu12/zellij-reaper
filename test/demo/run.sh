#!/usr/bin/env bash
# Try the dashboard on demo sessions, in a throwaway container: reap, protect,
# rename and run passes freely; your own zellij sessions are not touched.
#
#   test/demo/run.sh            # the dashboard
#   test/demo/run.sh --shell    # a shell in the same sandbox, for the commands
#
# ZELLIJ_REAPER_LANG=ko shows it in Korean. Needs Docker.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
root=$(cd "$here/../.." && pwd)
cache="$root/test/integration/.cache"
version=0.45.1
mkdir -p "$cache"
case "$(uname -m)" in
  x86_64) arch=x86_64 ;;
  aarch64 | arm64) arch=aarch64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 2 ;;
esac
# The demo's own binaries: an integration run may be using the ones in $cache.
bin="$cache/demo"
mkdir -p "$bin"
(cd "$root" && CGO_ENABLED=0 go build -o "$bin/zellij-reaper" ./cmd/zellij-reaper && CGO_ENABLED=0 go build -o "$bin/sleeper" ./test/demo/sleeper)
docker build -q -t zellij-reaper-it "$root/test/integration" >/dev/null
if [ ! -x "$cache/zellij-$version" ]; then
  curl -fsSL "https://github.com/zellij-org/zellij/releases/download/v$version/zellij-$arch-unknown-linux-musl.tar.gz" | tar -xz -C "$cache" zellij
  mv "$cache/zellij" "$cache/zellij-$version"
fi
tty_flags=-i
# A terminal gets the dashboard; piped input (scripts, tests) still works.
[ -t 0 ] && tty_flags=-it
# --init: a signal (ctrl+c, a timeout) reaches the staging script too.
exec docker run --rm --init $tty_flags \
  -e TERM="${TERM:-xterm-256color}" -e ZELLIJ_REAPER_LANG="${ZELLIJ_REAPER_LANG:-}" -e LANG="${LANG:-C.UTF-8}" \
  -v "$cache/zellij-$version:/usr/local/bin/zellij:ro" \
  -v "$bin/zellij-reaper:/usr/local/bin/zellij-reaper:ro" \
  -v "$bin/sleeper:/sleeper:ro" \
  -v "$here/stage.sh:/stage.sh:ro" \
  zellij-reaper-it /stage.sh "$@"
