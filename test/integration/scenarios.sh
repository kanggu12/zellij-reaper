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
    echo "       zellij lists: $($Z list-sessions -n 2>&1 | tr '\n' ';')"
    echo "       the last pass in the log:"
    awk '/=== reaper start/ { pass = "" } { pass = pass "         " $0 "\n" } END { printf "%s", pass }' "$LOG" 2>/dev/null
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

# Wait until zellij can reach a session. On a slow machine a session whose first
# pane has started can still answer "Session not found", which 0.43.1 does with
# exit status 0, so a command sent then is lost without a word and the scenario
# goes on with a session that is not what it thinks. This looks at the list, the
# same check that message comes from. It must not ask the session anything: on
# 0.43.1 an early list-clients stops the session from saving its layout, and it
# never shows as exited.
wait_reachable() {
  for _ in $(seq 100); do
    $Z list-sessions -n -s 2>/dev/null | grep -qxF "$1" && return 0
    sleep 0.1
  done
  echo "  (zellij does not list $1)"
  return 1
}

# Start a background session, optionally from a layout file.
start() {
  # zellij's client waits for the new server's socket with no end: a server
  # that dies starting would hold the run forever.
  if [ -n "${2:-}" ]; then
    timeout 20 $Z --layout "$2" attach --create-background "$1" >/dev/null 2>&1
  else
    timeout 20 $Z attach --create-background "$1" >/dev/null 2>&1
  fi
  wait_pane "$1" && wait_reachable "$1"
}

# How many clients the plan counts on one session.
clients_are() { [ "$($R plan --json | jq -r --arg n "$1" '.sessions[] | select(.name==$n) | .clients')" = "$2" ]; }

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

# Delete a session and wait for it to leave the list, as a pass does. A server caught
# saving its layout writes that data back as it stops, and the session lists as exited.
delete_it() {
  $Z delete-session "$1" --force >/dev/null 2>&1 || return 1
  for _ in $(seq 50); do
    case "$($Z list-sessions -n 2>/dev/null | awk -v n="$1" '$1 == n { print (/EXITED/ ? "exited" : "running") }')" in
      "") return 0 ;;
      exited)
        $Z delete-session "$1" >/dev/null 2>&1
        return
        ;;
    esac
    sleep 0.1
  done
  return 1
}

# Rename a session and wait for zellij to list the new name. The command returns
# before the server has acted, and on a slow machine a session that has only just
# started can answer "not found" for a moment, so it is asked again until the name
# is listed. A pass does the same: a rename that fails is tried on the next pass.
rename_to() {
  for _ in $(seq 30); do
    $Z --session "$1" action rename-session "$2" >/dev/null 2>&1
    for _ in 1 2 3 4 5; do
      listed "$2" && return 0
      sleep 0.1
    done
  done
  echo "  (zellij does not list $2 after renaming $1)"
  return 1
}

# Wait for a pane's title to reach zellij's metadata file, where a pass reads it.
wait_title() {
  for _ in $(seq 90); do
    grep -q "title \"$2\"" "$INFO/$1/session-metadata.kdl" 2>/dev/null && return 0
    sleep 1
  done
  return 1
}

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
# A name zellij picks itself on this release, which a pass further down replaces:
# the whole way from zellij making a name up to a better one. The picked name is only
# one word pair of thousands, so run.sh compares the full word lists. zellij picks one
# only when no session is running, so this comes first, and it runs something so the
# forced pass keeps it. The scenarios below start sessions under a few such names of
# their own, so a pick among those is made again.
reserved=' quiet-mouse adept-magpie kind-viola rusty-pepper brave-peach glowing-duck lucky-duck kind-panda wise-yak '
mkdir -p /srv/own-pick
own=""
for _ in 1 2 3 4 5; do
  (cd /srv/own-pick && $Z attach --create-background >/dev/null 2>&1)
  own=""
  for _ in $(seq 100); do
    own=$($Z list-sessions -n -s 2>/dev/null | head -1)
    [ -n "$own" ] && break
    sleep 0.1
  done
  [[ "$reserved" == *" $own "* ]] || break
  $Z delete-session "$own" --force >/dev/null 2>&1
done
check "zellij picks a name" test -n "$own"
wait_pane "$own" && wait_reachable "$own" && $Z --session "$own" run -- sleep 99996 >/dev/null
# Started first: the auto_rename scenario needs its metadata settled.
start named /tmp/named.kdl
start idle-old && age_ptys idle-old '-3 hours'
start idle-new
start shell-busy && $Z --session shell-busy run -- bash -c 'sleep 100000; true' >/dev/null 2>&1
start cmd-pane && $Z --session cmd-pane run -- sleep 99999 >/dev/null 2>&1
start agent && $Z --session agent run -- claude >/dev/null 2>&1
# Jobs whose input is not the terminal: still panes, and busy.
start nohup-job && $Z --session nohup-job run -- nohup sleep 99998 >/dev/null 2>&1
start redirected && $Z --session redirected run -- sh -c 'exec sleep 99997 </dev/null' >/dev/null 2>&1
start attached && { script -qfec "$Z attach attached" /dev/null >/dev/null 2>&1 & }
start keepme && age_ptys keepme '-3 hours'
start first && rename_to first second
# Age it under its first name: a rename leaves the server's command line
# alone, so that is the only name server_pid can match. (The reaper itself
# finds the server through the socket file, which the rename does follow.)
age_ptys first '-3 hours'
for s in exited-old exited-new; do
  # A second pane makes zellij save the layout, which resurrection needs.
  start "$s" && $Z --session "$s" run -- sleep 5000 >/dev/null 2>&1
done
# zellij lists an exited session only once it has saved a layout, which takes
# longer on a slow machine than any fixed wait here would allow for.
wait_saved() {
  for _ in $(seq 300); do
    grep -q 'sleep' "$INFO/$1/session-layout.kdl" 2>/dev/null && return 0
    sleep 0.1
  done
  echo "  (zellij saved no layout for $1)"
  return 1
}
wait_exited() {
  for _ in $(seq 300); do
    $Z list-sessions -n 2>/dev/null | grep -q "^$1 .*EXITED" && return 0
    sleep 0.1
  done
  echo "  (zellij does not list $1 as exited)"
  return 1
}
for s in exited-old exited-new; do wait_saved "$s"; done
for s in exited-old exited-new; do kill -9 "$(server_pid "$s")"; done
for s in exited-old exited-new; do wait_exited "$s"; done
touch -d '-4 days' "$INFO"/exited-old/*
# Busy panes need their programs started before the terminals are aged.
age_ptys shell-busy '-3 hours'
age_ptys cmd-pane '-3 hours'
age_ptys agent '-3 hours'
age_ptys nohup-job '-3 hours'
age_ptys redirected '-3 hours'
$R protect keepme >/dev/null

echo "-- the plan"
check "idle for 3h is reaped" is_code idle-old idle-old
check "idle for a moment waits" is_code idle-new idle-recent
check "a command under the shell keeps it" is_code shell-busy busy
check "a command pane keeps it" is_code cmd-pane busy
check "a protected program keeps it" is_code agent protected-command
check "a job under nohup keeps it" is_code nohup-job busy
check "a job with its input redirected keeps it" is_code redirected busy
check "an attached client keeps it" is_code attached attached
check "it counts one client" clients_are attached 1
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
for s in idle-new shell-busy cmd-pane agent nohup-job redirected attached keepme exited-new; do
  check "$s is kept" listed "$s"
done
check "deletions are logged" grep -q '  -> deleted: idle-old' "$LOG"

echo "-- a second terminal on a session"
{ script -qfec "$Z attach attached" /dev/null >/dev/null 2>&1 & }
sleep 2
check "the plan counts both clients" clients_are attached 2
check "the session is still kept" is_code attached attached

echo "-- a forced pass skips the waits only"
check "a forced pass succeeds" $R run --force --quiet
check "the recent idle session is gone" gone idle-new
check "the recent exited session is gone" gone exited-new
for s in shell-busy cmd-pane agent attached keepme named; do
  check "$s is still kept" listed "$s"
done

echo "-- auto_rename"
# Sessions zellij could have named, showing something to be named after: a title,
# one with a dot in it, the same title twice, a long title, one with words in other
# scripts, and a shell whose title says nothing, working in a directory that does.
# And one a person named.
pane_layout() { printf 'layout {\n    pane name="%s" command="sleep" {\n        args "100000"\n    }\n}\n' "$1" >"$2"; }
pane_layout 'build watcher' /tmp/watcher.kdl
pane_layout 'same title' /tmp/same.kdl
pane_layout 'notes v1.2.md' /tmp/dotted.kdl
pane_layout 'notes from the long planning meeting about the next release' /tmp/long.kdl
pane_layout 'Déploiement prod 本番' /tmp/scripts.kdl
mkdir -p /srv/api
start quiet-mouse /tmp/watcher.kdl
start adept-magpie /tmp/same.kdl
start kind-viola /tmp/same.kdl
start rusty-pepper /tmp/dotted.kdl
start brave-peach /tmp/long.kdl
start wise-yak /tmp/scripts.kdl
(cd /srv/api && start glowing-duck)
# A stamped name from an earlier version, which gave those by renaming. And names
# people chose, by starting a session or by renaming one: plain, two words from
# zellij's lists, and two in the shape of a stamp.
start old-title /tmp/watcher.kdl && rename_to old-title old-title_1104-0931
start my-project /tmp/watcher.kdl
start hand-picked /tmp/watcher.kdl && rename_to hand-picked lucky-duck
start yearly /tmp/watcher.kdl && rename_to yearly report_2025-2026
start started_1104-0931 /tmp/watcher.kdl
# A name zellij could have made up, which someone kept with r and Enter.
start kind-panda /tmp/watcher.kdl && printf 'kind-panda\n' >>~/.local/state/zellij-reaper/kept
check "the titles reach the metadata" sh -c "$(declare -f wait_title); INFO='$INFO'; wait_title quiet-mouse 'build watcher' && wait_title adept-magpie 'same title' && wait_title kind-viola 'same title' && wait_title rusty-pepper 'notes v1.2.md' && wait_title brave-peach 'notes from the long planning meeting about the next release' && wait_title wise-yak 'Déploiement prod 本番' && wait_title my-project 'build watcher' && wait_title old-title_1104-0931 'build watcher' && wait_title lucky-duck 'build watcher' && wait_title report_2025-2026 'build watcher' && wait_title started_1104-0931 'build watcher' && wait_title kind-panda 'build watcher'"
check "a pass with auto_rename succeeds" env ZELLIJ_REAPER_AUTO_RENAME=1 $R run --quiet
check "a made-up name goes" gone quiet-mouse
check "a session is named after its pane" listed build-watcher
check "a name someone chose stays" listed my-project
check "zellij's words given by hand stay" listed lucky-duck
check "a name in the shape of a stamp stays" listed report_2025-2026
check "a stamped name a session started under stays" listed started_1104-0931
check "a name someone kept stays" listed kind-panda
check "the stamp of an earlier version goes" gone old-title_1104-0931
check "a dot stays in a word" listed notes-v1.2.md
check "a name with a dot still reaches its server" $Z --session notes-v1.2.md action list-clients
check "a shell is named after its directory" listed api
check "the same title takes a tail" sh -c "$Z list-sessions -n -s | grep -qxF same-title && $Z list-sessions -n -s | grep -qxF same-title-2"
long_name='notes-from-the-long-planning'
check "a long title is cut at a word" listed "$long_name"
check "a cut name still reaches its server" $Z --session "$long_name" action list-clients
check "words in other scripts are left out" listed prod
check "a name zellij picked itself goes" gone "$own"
check "and the session is named after what it runs, where" listed own-pick-sleep-99996
check "the names given are remembered" grep -qxF build-watcher ~/.local/state/zellij-reaper/named

renames() { grep -c -- '-> renamed:' "$LOG"; }
before=$(renames)
check "a second pass succeeds" env ZELLIJ_REAPER_AUTO_RENAME=1 $R run --quiet
check "and renames nothing" test "$(renames)" = "$before"
check "the names stay" sh -c "$Z list-sessions -n -s | grep -qxF same-title-2 && $Z list-sessions -n -s | grep -qxF my-project"

# The list of given names holds names, not sessions. Someone starts a session under
# a name a pass gave to a session now gone, before any pass has cleared it.
check "a session a pass named can be deleted" delete_it build-watcher
check "and that name is free" gone build-watcher
pane_layout 'my own work' /tmp/own.kdl
start build-watcher /tmp/own.kdl
check "the new session's title reaches the metadata" sh -c "$(declare -f wait_title); INFO='$INFO'; wait_title build-watcher 'my own work'"
check "its name is on the list from the one before" grep -qxF build-watcher ~/.local/state/zellij-reaper/named
check "a pass after that succeeds" env ZELLIJ_REAPER_AUTO_RENAME=1 $R run --quiet
check "a session started under a name a pass gave keeps it" listed build-watcher

check "zellij deletes a name with a dot" delete_it notes-v1.2.md
check "and it is gone" gone notes-v1.2.md

# zellij remembers the names a session has had and refuses them, so a session
# that goes back to a name it had before takes a tail. Its name then stays.
check "a stamp can be asked for" env ZELLIJ_REAPER_AUTO_RENAME=1 ZELLIJ_REAPER_AUTO_RENAME_STAMP=date $R run --quiet
check "the name ends in the day of last use" sh -c "$Z list-sessions -n -s | grep -Eq '^api_[0-9]{4}\$'"
check "a stamp leaves a chosen name alone" listed my-project
check "the minute can be asked for" env ZELLIJ_REAPER_AUTO_RENAME=1 ZELLIJ_REAPER_AUTO_RENAME_STAMP=time $R run --quiet
check "the name ends in its minute" sh -c "$Z list-sessions -n -s | grep -Eq '^api_[0-9]{4}-[0-9]{4}\$'"
check "without a stamp the name comes back" env ZELLIJ_REAPER_AUTO_RENAME=1 $R run --quiet
check "and the stamp is gone" sh -c "$Z list-sessions -n -s | grep -Eq '^api(-[0-9]+)?\$'"
before=$(renames)
check "one more pass succeeds" env ZELLIJ_REAPER_AUTO_RENAME=1 $R run --quiet
check "and leaves that name alone" test "$(renames)" = "$before"

echo "-- auto_rename_attached"
# A session someone works in: a pass renames it too, unless told to wait until
# nobody is attached, and its client stays through the rename.
pane_layout 'review the draft' /tmp/review.kdl
start quiet-magpie /tmp/review.kdl
{ script -qfec "$Z attach quiet-magpie" /dev/null >/dev/null 2>&1 & }
sleep 2
check "its title reaches the metadata" sh -c "$(declare -f wait_title); INFO='$INFO'; wait_title quiet-magpie 'review the draft'"
check "a client is attached to it" clients_are quiet-magpie 1
check "a pass that waits for nobody attached succeeds" env ZELLIJ_REAPER_AUTO_RENAME=1 ZELLIJ_REAPER_AUTO_RENAME_ATTACHED=0 $R run --quiet
check "and the attached session keeps its name" listed quiet-magpie
check "a pass with auto_rename succeeds" env ZELLIJ_REAPER_AUTO_RENAME=1 $R run --quiet
check "an attached session is named after its pane" listed review-the-draft
check "and its client is still attached" clients_are review-the-draft 1

echo "-- the names pass, keep-name, here and the hook"
pane_layout 'plan the launch' /tmp/launch.kdl
start wise-panda /tmp/launch.kdl
check "its title reaches the metadata" sh -c "$(declare -f wait_title); INFO='$INFO'; wait_title wise-panda 'plan the launch'"
names_lines() { grep -c -- '=== names start' "$LOG"; }
before=$(names_lines)
check "a names pass without auto_rename succeeds" $R run --names-only --quiet
check "and writes nothing" test "$(names_lines)" = "$before"
check "keep-name keeps a running session's name" $R keep-name wise-panda
check "keep-name lists it" sh -c "$R keep-name | grep -qxF wise-panda"
check "a names pass succeeds" env ZELLIJ_REAPER_AUTO_RENAME=1 $R run --names-only --quiet
check "and leaves a kept name alone" listed wise-panda
check "forget-name lets it go" $R forget-name wise-panda
check "a names pass after that succeeds" env ZELLIJ_REAPER_AUTO_RENAME=1 $R run --names-only --quiet
check "the session is named after its pane" listed plan-the-launch
check "the names pass says so in the log" sh -c "grep -q -- '-> renamed: wise-panda → plan-the-launch' '$LOG' && grep -q '=== names end (1 renamed) ===' '$LOG'"

# A pane opened before a rename: here gives the new name, and with it a zellij
# command returns, where the pane's own ZELLIJ_SESSION_NAME would wait.
start adept-peach
# shellcheck disable=SC2016 # expanded in the pane, not here
$Z --session adept-peach run -- bash -c 'while [ ! -e /tmp/go-here ]; do sleep 0.1; done
  zellij-reaper here >/tmp/here.out 2>&1; echo "rc=$?" >>/tmp/here.out
  ZELLIJ_SESSION_NAME=$(zellij-reaper here) timeout 6 zellij action list-clients >/dev/null 2>&1; echo "cmd=$?" >>/tmp/here.out
  eval "$(zellij-reaper shell-hook bash)"; ZELLIJ_SESSION_NAME=adept-peach; false; __zellij_reaper_here
  echo "hook=$? $ZELLIJ_SESSION_NAME" >>/tmp/here.out' >/dev/null 2>&1
sleep 1
rename_to adept-peach peach-renamed
touch /tmp/go-here
for _ in $(seq 60); do grep -q '^hook=' /tmp/here.out 2>/dev/null && break; sleep 0.25; done
check "here gives the name the session has now" grep -qx 'peach-renamed' /tmp/here.out
check "a zellij command under that name returns" grep -qx 'cmd=0' /tmp/here.out
check "the bash hook sets it and keeps the last status" grep -qx 'hook=1 peach-renamed' /tmp/here.out

echo "-- Enter in the dashboard"
# The dashboard runs in the only pane of a session someone is attached to;
# that client presses the keys, so it is the client switch-session moves.
printf 'layout {\n    pane command="zellij-reaper" {\n        args "tui"\n    }\n}\n' >/tmp/dash.kdl
attach_typing() { # session fifo
  mkfifo "$2"
  { script -qfec "stty cols 120 rows 40; $Z attach $1" /dev/null <"$2" >/dev/null 2>&1 & }
}
clients_on() { $Z --session "$1" action list-clients 2>/dev/null | tail -n +2 | grep -c .; }
# screen dumps what a session's focused pane shows to /tmp/screen.txt
# (0.43 takes the path as an argument, later releases as --path).
screen() { $Z --session "$1" action dump-screen --path /tmp/screen.txt >/dev/null 2>&1 || $Z --session "$1" action dump-screen /tmp/screen.txt >/dev/null 2>&1; }
# shows waits until a session's pane shows a text.
shows() {
  for _ in $(seq 60); do
    screen "$1" && grep -q "$2" /tmp/screen.txt && return 0
    sleep 0.25
  done
  return 1
}
v=$($Z --version | awk '{print $2}')
for round in plain renamed; do
  from=dash-$round to=goal-$round
  start "$from" /tmp/dash.kdl && start "$to"
  attach_typing "$from" "/tmp/keys-$round"
  exec 7>"/tmp/keys-$round"
  sleep 3
  # The dashboard's own session renamed after it started: its pane still
  # has the old name in ZELLIJ_SESSION_NAME.
  if [ $round = renamed ]; then rename_to "$from" "$from-x"; from=$from-x; sleep 1; fi
  # A zellij with no settings asks which keys to use on an attach; that
  # question would take the keys meant for the dashboard, so it is answered.
  if screen "$from" && grep -q 'Which do you prefer' /tmp/screen.txt; then printf '\r' >&7; fi
  check "the dashboard shows ($round)" shows "$from" 'NEXT PASS'
  printf '/%s\r' "$to" >&7
  check "the filter leaves the target selected ($round)" shows "$from" "● $to"
  sleep 1
  printf '\r' >&7
  for _ in $(seq 40); do [ "$(clients_on "$to")" = 1 ] && break; sleep 0.25; done
  case $v in
    0.43.*)
      check "zellij $v cannot switch, and the client stays ($round)" test "$(clients_on "$from")" = 1
      check "the dashboard says what to do instead ($round)" shows "$from" 'cannot switch sessions' ;;
    *) check "Enter moves the client to the session ($round)" test "$(clients_on "$to")" = 1 ;;
  esac
  exec 7>&-
  # The dashboard reads the sessions every few seconds. zellij 0.45.1 can die
  # starting when a client connects and leaves before it is up, so a dashboard
  # left running here can stall a later start.
  $Z kill-session "$from" >/dev/null 2>&1
done

echo "-- recover"
start first2 && rename_to first2 second2
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
start orph-busy && $Z --session orph-busy run -- sleep 99999 >/dev/null
start orph-idle
sleep 3
for s in orph-client orph-busy orph-idle; do rm -f "$(sock_of "$s")"; done
check "the dry run says why a server is kept" sh -c "$R recover --aggressive --dry-run | grep -q 'first name: orph-client), kept: client attached'"
check "recover --aggressive succeeds" $R recover --aggressive
check "the server with a client is kept" running orph-client
check "the server with a busy pane is kept" running orph-busy
check "the idle orphan is stopped" stopped orph-idle
check "a healthy session is untouched" listed keepme

echo "-- the other commands"
check "doctor reports zellij" sh -c "$R doctor | grep -q 'zellij $($Z --version | awk '{print $2}')'"
# The name is drawn in two colors: look for it with the colors taken out.
check "the dashboard renders a frame" sh -c "$R tui --snapshot 120x30 | sed 's/\x1b\[[0-9;]*m//g' | grep -q zellij-reaper"
check "logs prints the log" sh -c "$R logs -n 50 | grep -q 'reaper end'"
check "config show lists the file" sh -c "$R config show | grep -Eq 'idle_after += \"1h\"'"

echo "passed $pass, failed $fail"
[ "$fail" = 0 ]
