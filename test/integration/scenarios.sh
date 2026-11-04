#!/usr/bin/env bash
# Integration scenarios, run inside a throwaway container by run.sh: real zellij
# sessions in every state the reaper judges, then plans and passes against them.
set -uo pipefail

Z=zellij
R=zellij-reaper
LOG="$HOME/.local/state/zellij-reaper/reaper.log"
pass=0
fail=0

# ok/no record one check; check runs a command as a check.
ok() { pass=$((pass + 1)); echo "  ok   $1"; }
no() { fail=$((fail + 1)); echo "  FAIL $1${2:+: $2}"; }
check() {
  local name=$1
  shift
  if "$@"; then
    ok "$name"
  else
    no "$name"
    # What the reaper saw, so a failure that only happens now and then can be
    # read afterwards instead of guessed at.
    echo "       plan: $($R plan --json 2>&1 | jq -c '[.sessions[] | {name, code}]' 2>/dev/null)"
    echo "       last log lines:"
    tail -n 5 "$LOG" 2>/dev/null | sed 's/^/         /'
  fi
}

# The server of a session, found by the name it was started with.
server_pid() { pgrep -f -- "--server .*/$1\$" | head -1; }

# Wait until a session's server has a pane process.
wait_pane() {
  local p
  for _ in $(seq 100); do
    p=$(server_pid "$1")
    [ -n "$p" ] && [ -n "$(pgrep -P "$p")" ] && return 0
    sleep 0.1
  done
  echo "  (no pane appeared for $1)"
  return 1
}

# Move every pane terminal's last use of a session back in time.
age_ptys() {
  local p c t
  p=$(server_pid "$1")
  for c in $(pgrep -P "$p"); do
    t=$(readlink "/proc/$c/fd/0") && touch -d "$2" "$t"
  done
}

# Start a background session, optionally from a layout file.
start() {
  if [ -n "${2:-}" ]; then
    $Z --layout "$2" attach --create-background "$1" >/dev/null 2>&1
  else
    $Z attach --create-background "$1" >/dev/null 2>&1
  fi
  wait_pane "$1"
}

# Wait until the plan no longer reports a client for a session that was just
# created in the background (zellij 0.43 reports the creating client once).
settle() {
  for _ in $(seq 90); do
    [ "$(code "$1")" != attached ] && return 0
    sleep 1
  done
  return 1
}

# What the plan says about one session: its code, or "absent".
code() { $R plan --json | jq -r --arg n "$1" '(.sessions[] | select(.name==$n) | .code) // "absent"'; }
# is_code compares one reading of the plan, and shows that reading on failure.
is_code() {
  local entry absent='{"code":"absent"}'
  entry=$($R plan --json | jq -c --arg n "$1" '.sessions[] | select(.name==$n) | {code, reason}')
  # The default sits in a variable: a "}" written inside ${entry:-...} ends
  # the expansion early and leaves a stray "}" that jq fails on.
  [ "$(jq -r .code <<<"${entry:-$absent}")" = "$2" ] || {
    echo "       $1: ${entry:-absent}, want $2"
    return 1
  }
}
listed() { $Z list-sessions -n -s 2>/dev/null | grep -qxF "$1"; }
gone() { ! listed "$1"; }

ver=$($Z --version | awk '{print $2}')
echo "zellij $ver"
# zellij 0.43 and earlier name their data directory after the version.
case "$ver" in
  0.[0-3][0-9].* | 0.4[0-3].*) data=$ver ;;
  *) data=contract_version_1 ;;
esac
INFO=~/.cache/zellij/$data/session_info

# Settings for the scenarios: serialize every second so an exited session can
# be resurrected (and so listed as EXITED) soon after it starts.
mkdir -p ~/.config/zellij ~/.config/zellij-reaper
printf 'serialization_interval 1\nshow_startup_tips false\nshow_release_notes false\n' >~/.config/zellij/config.kdl
cat >~/.config/zellij-reaper/config.toml <<'EOF'
idle_after = "1h"
exited_after = "3d"
protect_commands = ["claude"]
EOF
# A stand-in coding agent: a program named claude that stays running.
printf '#!/bin/sh\nsleep 100000\n' >/usr/local/sbin/claude
chmod +x /usr/local/sbin/claude
# A named command pane: busy (so the forced pass keeps it) and informative.
printf 'layout {\n    cwd "/srv/web-app"\n    pane name="build watcher" command="sleep" {\n        args "100000"\n    }\n}\n' >/tmp/named.kdl
mkdir -p /srv/web-app

echo "-- nothing to do"
check "a pass with no sessions succeeds" $R run --quiet
check "and logs 'no sessions'" grep -q '] no sessions$' "$LOG"
check "plan --json lists nothing" test "$($R plan --json | jq '.sessions | length')" = 0

echo "-- sessions in every state"
# Started first: the auto_rename scenario needs its metadata settled.
start named /tmp/named.kdl
start idle-old && age_ptys idle-old '-3 hours'
start idle-new
start shell-busy && $Z --session shell-busy run -- bash -c 'sleep 100000; true' >/dev/null 2>&1
start cmd-pane && $Z --session cmd-pane run -- sleep 99999 >/dev/null 2>&1
start agent && $Z --session agent run -- claude >/dev/null 2>&1
start attached && { script -qfec "$Z attach attached" /dev/null >/dev/null 2>&1 & }
start keepme && age_ptys keepme '-3 hours'
start first && $Z --session first action rename-session second
# Age it under its first name: a rename leaves the server's command line
# alone, so that is the only name server_pid can match. (The reaper itself
# finds the server through the socket file, which the rename does follow.)
age_ptys first '-3 hours'
for s in exited-old exited-new; do
  # A second pane makes zellij save the layout, which resurrection needs.
  start "$s" && $Z --session "$s" run -- sleep 5000 >/dev/null 2>&1
done
sleep 4
for s in exited-old exited-new; do kill -9 "$(server_pid "$s")"; done
sleep 2
touch -d '-4 days' "$INFO"/exited-old/*
# Busy panes need their programs started before the terminals are aged.
age_ptys shell-busy '-3 hours'
age_ptys cmd-pane '-3 hours'
age_ptys agent '-3 hours'
$R protect keepme >/dev/null

echo "-- the plan"
# zellij 0.43 answers the first `list-clients` for a background session with
# the client that created it, so the reaper keeps that session for one pass.
# One plan settles one answer per session, and the sessions do not all settle
# in the same plan: ask until none is reported attached, or give up and let
# the checks below say what is wrong.
for _ in $(seq 10); do
  $R plan --json >/tmp/plan.json 2>/dev/null || break
  jq -e '[.sessions[] | select(.code == "attached" and .name != "attached")] | length == 0' /tmp/plan.json >/dev/null && break
  sleep 1
done
check "idle for 3h is reaped" is_code idle-old idle-old
check "idle for a moment waits" is_code idle-new idle-recent
check "a command under the shell keeps it" is_code shell-busy busy
check "a command pane keeps it" is_code cmd-pane busy
check "a protected program keeps it" is_code agent protected-command
check "an attached client keeps it" is_code attached attached
check "the protected list keeps it" is_code keepme protected-name
check "a renamed session is matched to its server" is_code second idle-old
check "exited 4 days ago is reaped" is_code exited-old exited-old
check "exited just now waits" is_code exited-new exited-recent
check "a busy pane names its command" sh -c "$R plan --json | jq -e '.sessions[] | select(.name==\"cmd-pane\") | .reason | contains(\"sleep 99999\")' >/dev/null"

echo "-- a dry run changes nothing"
check "dry run succeeds" env ZELLIJ_REAPER_DRY_RUN=1 $R run --quiet
check "every session is still there" sh -c "for s in idle-old second exited-old; do $Z list-sessions -n -s | grep -qxF \$s || exit 1; done"
check "the log says what it would do" grep -q 'DRY_RUN: would run: zellij delete-session idle-old --force' "$LOG"

echo "-- a pass"
check "a pass succeeds" $R run --quiet
check "the idle session is gone" gone idle-old
check "the renamed idle session is gone" gone second
check "the old exited session is gone" gone exited-old
for s in idle-new shell-busy cmd-pane agent attached keepme exited-new; do
  check "$s is kept" listed "$s"
done
check "deletions are logged" grep -q '  -> deleted: idle-old' "$LOG"

echo "-- a forced pass skips the waits only"
check "a forced pass succeeds" $R run --force --quiet
check "the recent idle session is gone" gone idle-new
check "the recent exited session is gone" gone exited-new
for s in shell-busy cmd-pane agent attached keepme named; do
  check "$s is still kept" listed "$s"
done

echo "-- auto_rename"
check "the session's metadata settles" settle named
check "a pass with auto_rename succeeds" env ZELLIJ_REAPER_AUTO_RENAME=1 $R run --quiet
new=$($Z list-sessions -n -s | grep '^build-watcher_' || true)
check "the session is named after its pane" test -n "$new"
check "the old name is gone" gone named
check "a second pass keeps the name" env ZELLIJ_REAPER_AUTO_RENAME=1 $R run --quiet
check "renamed once only" test "$(grep -c -- '-> renamed: named' "$LOG")" = 1
check "and still listed" listed "$new"

echo "-- recover"
start first2 && $Z --session first2 action rename-session second2
# A session whose name is too long for a socket: its saved layout makes zellij
# keep it and list it, but it can never be reached.
info=$INFO
long=$(printf 'x%.0s' $(seq 1 120))
mkdir -p "$info/$long"
printf 'layout {\n    pane\n}\n' >"$info/$long/session-layout.kdl"
printf 'name "%s"\n' "$long" >"$info/$long/session-metadata.kdl"
sleep 1
check "zellij lists the broken session" listed "$long"
check "recover --aggressive --dry-run finds no orphan" sh -c "$R recover --aggressive --dry-run | grep -q 'every server serves a socket file'"
check "recover repairs the long name" $R recover
check "its directory is gone" test ! -d "$INFO/$long"
check "the renamed session survives" listed second2

echo "-- recover --aggressive keeps servers somebody uses"
# Servers whose socket file is gone: zellij can no longer reach them, but a
# client attached before stays connected, and a pane can still be busy.
sock_of() { tr '\0' '\n' <"/proc/$(server_pid "$1")/cmdline" | sed -n '/^--server$/{n;p;}'; }
running() { [ -n "$(server_pid "$1")" ]; }
stopped() { sleep 1; [ -z "$(server_pid "$1")" ]; }
start orph-client && { script -qfec "$Z attach orph-client" /dev/null >/dev/null 2>&1 & }
start orph-busy && $Z --session orph-busy run -- sleep 99999 >/dev/null 2>&1
start orph-idle
sleep 3
for s in orph-client orph-busy orph-idle; do rm -f "$(sock_of "$s")"; done
check "the dry run says why a server is kept" sh -c "$R recover --aggressive --dry-run | grep -q 'first name: orph-client), kept: client attached'"
check "recover --aggressive succeeds" $R recover --aggressive
check "the server with a client is kept" running orph-client
check "the server with a busy pane is kept" running orph-busy
check "the idle orphan is stopped" stopped orph-idle
check "a healthy session is untouched" listed shell-busy

echo "-- the other commands"
check "doctor reports zellij" sh -c "$R doctor | grep -q 'zellij $($Z --version | awk '{print $2}')'"
check "the dashboard renders a frame" sh -c "$R tui --snapshot 120x30 | grep -q zellij-reaper"
check "logs prints the log" sh -c "$R logs -n 5 | grep -q 'reaper end'"
check "config show lists the file" sh -c "$R config show | grep -q 'idle_after        = \"1h\"'"

echo "passed $pass, failed $fail"
[ "$fail" = 0 ]
