#!/usr/bin/env bash
# Inside the demo container: start sessions in every state the dashboard shows,
# with a few earlier passes in the log, then open the dashboard (or a shell with
# --shell). Nothing here touches the machine running the container.
set -u
export HOME=/root SHELL=/bin/bash TERM=${TERM:-xterm-256color}
Z=zellij
LOG=~/.local/state/zellij-reaper/reaper.log
INFO=~/.cache/zellij/contract_version_1/session_info
mkdir -p ~/.config/zellij ~/.config/zellij-reaper ~/.config/systemd/user /srv
: >/srv/app.log
printf 'serialization_interval 1\nshow_startup_tips false\nshow_release_notes false\n' >~/.config/zellij/config.kdl
printf 'idle_after = "1h"\nexited_after = "3d"\nauto_rename = false\n' >~/.config/zellij-reaper/config.toml
# Stand-ins for programs people leave running.
for p in npm claude psql; do cp /sleeper /usr/local/sbin/$p; done
# A stand-in for systemd, so the dashboard has a timer to show: it reports one
# that last fired 18 minutes ago. Passes still run only when you start them.
cat >/usr/local/sbin/systemctl <<'SH'
#!/bin/sh
case "$*" in
  *show-environment*) exit 0 ;;
  *"show zellij-reaper.timer"*) printf 'LoadState=loaded\nActiveState=active\nUnitFileState=enabled\nLastTriggerUSec=%s\nNextElapseUSecRealtime=\n' "$(date -d '-18 min' '+%a %Y-%m-%d %H:%M:%S %Z')" ;;
esac
SH
chmod +x /usr/local/sbin/systemctl
printf '[Timer]\nOnUnitActiveSec=1h\n' >~/.config/systemd/user/zellij-reaper.timer

# server prints the pid of a session's server.
server() { pgrep -f -- "--server .*/$1\$" | head -1; }
# pane_wait waits until a session's server has started its first pane.
pane_wait() { for _ in $(seq 100); do p=$(server "$1"); [ -n "$p" ] && [ -n "$(pgrep -P "$p")" ] && return 0; sleep 0.1; done; }
# age moves the last use of a session's panes back, as if nobody had typed
# in them since.
age() { p=$(server "$1"); for c in $(pgrep -P "$p"); do t=$(readlink "/proc/$c/fd/0") && touch -d "$2" "$t"; done; }
# mk starts a session in the background, with nobody attached, and waits
# until zellij has written its session data (a pass keeps a session without).
mk() {
  # zellij can stall starting a session; a second try gets it going.
  timeout 15 $Z attach --create-background "$1" >/dev/null 2>&1 ||
    timeout 15 $Z attach --create-background "$1" >/dev/null 2>&1
  pane_wait "$1"
  for _ in $(seq 100); do [ -s "$INFO/$1/session-metadata.kdl" ] && return 0; sleep 0.1; done
}
# exited leaves a session exited, as zellij shows one after a crash or a
# reboot, last used $2 ago.
exited() {
  mk "$1" && $Z --session "$1" run -- sleep 5000 >/dev/null 2>&1
  sleep 3
  kill -9 "$(server "$1")"
  sleep 2
  touch -d "$2" "$INFO/$1"/*
}
# earlier runs a pass now and moves its lines in the log back by $1, as if
# the timer had run it then.
earlier() {
  local from=1 tmp
  [ -f "$LOG" ] && from=$(($(wc -l <"$LOG") + 1))
  zellij-reaper run --quiet
  tmp=$(mktemp)
  head -n "$((from - 1))" "$LOG" >"$tmp"
  tail -n "+$from" "$LOG" | while IFS= read -r line; do
    printf '[%s]%s\n' "$(date -d "${line:1:19} $1 ago" '+%Y-%m-%d %H:%M:%S')" "${line:21}"
  done >>"$tmp"
  mv "$tmp" "$LOG"
}

echo "starting the demo sessions (about a minute)..."
# Earlier passes, so the log and the recent decisions have something to show.
mk tmp && age tmp '-5 hours'
exited build-cache '-4 days'
zellij-reaper plan --json >/dev/null
earlier '26 hours'
mk review-pr-41 && age review-pr-41 '-14 hours'
earlier '9 hours'
exited old-logs '-5 days'
earlier '3 hours'

# The sessions on screen, one or more of every kind.
mk api-server && $Z --session api-server run -- npm run dev >/dev/null 2>&1
mk notes
mk agent-refactor && $Z --session agent-refactor run -- claude >/dev/null 2>&1
mk db-shell && $Z --session db-shell run -- psql app_dev >/dev/null 2>&1
mk monitoring && $Z --session monitoring run -- watch df -h >/dev/null 2>&1
mk logs && $Z --session logs run --cwd /srv -- tail -f app.log >/dev/null 2>&1
mk scratch
mk docs-review
mk release-v2
mk dotfiles && zellij-reaper protect dotfiles >/dev/null
exited old-experiment '-5 days'
exited k8s-debug '-1 day'
for s in api-server agent-refactor db-shell monitoring logs scratch dotfiles; do age "$s" '-3 hours'; done
age docs-review '-25 min'
age release-v2 '-50 min'
# A client attached to notes, as a terminal left open on it would be. It
# comes last: zellij can stall starting a session while a client attaches.
script -qfec "$Z attach notes" /dev/null >/dev/null 2>&1 &
for _ in $(seq 100); do
  $Z --session notes action list-clients 2>/dev/null | grep -q '^[0-9]' && break
  sleep 0.1
done
zellij-reaper plan --json >/dev/null

if [ "${1:-}" = --shell ]; then
  echo "demo sessions are up. try: zellij-reaper plan, zellij-reaper, zellij-reaper run --dry-run, zellij-reaper logs"
  exec bash
fi
exec zellij-reaper
