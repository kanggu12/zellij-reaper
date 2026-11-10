#!/usr/bin/env bash
# Real zellij sessions on the machine this runs on, checked: made for the
# macOS and Windows runners of the platforms workflow, where no container can
# stand in for the system. It starts sessions, renames them and runs passes,
# so never run it on a machine whose sessions matter: it deletes sessions.
#
# On Windows it must run in a console: started without one, as a CI step is,
# zellij gives a shell pane a console with no input, so the shell ends at once
# and its session with it. The platforms workflow starts it in a hidden one.
set -uo pipefail
R=${R:-$PWD/zellij-reaper$(go env GOEXE)}
Z=${Z:-zellij}
pass=0 fail=0
step() { printf '\n==== %s\n' "$*"; }
show() { printf '$ %s\n' "$*"; "$@" 2>&1 | head -60; }
check() {
  local what=$1
  shift
  if "$@" >/dev/null 2>&1; then
    pass=$((pass + 1))
    echo "  ok   $what"
  else
    fail=$((fail + 1))
    echo "  FAIL $what"
  fi
}
# macOS has no timeout(1); perl, which macOS and Git for Windows both have,
# does the same.
tmo() { local s=$1; shift; perl -e 'alarm shift; exec @ARGV or die "$!"' "$s" "$@"; }
listed() { "$Z" list-sessions --no-formatting 2>/dev/null | tr -d '\r' | grep -q "^$1 "; }
gone() { ! listed "$1"; }
# A pass's verdict on one session, from plan --json.
verdict() { "$R" plan --json | tr -d '\r' | awk -v n="\"$1\"" '$1 == "\"name\":" && $2 == n "," { on = 1 } on && $1 == "\"code\":" { gsub(/[",]/, "", $2); print $2; exit }'; }
is() { [ "$(verdict "$1")" = "$2" ]; }
# A pass answers and has data for every session it lists.
has_data() {
  local out
  out=$("$R" plan --json 2>/dev/null | tr -d '\r') && [ -n "$out" ] && ! grep -q '"code": "no-info"' <<<"$out"
}

windows=false
tmp=${TMPDIR:-/tmp}
case "$(uname -s)" in
  MINGW* | MSYS* | CYGWIN*)
    windows=true
    tmp=$(cygpath "$TEMP")
    # Git Bash hands zellij its own SHELL, a Unix path a Windows zellij
    # cannot start; and it turns "/c" into "C:/" on the way to cmd.
    unset SHELL
    export MSYS2_ARG_CONV_EXCL='*'
    ;;
esac
native() { if $windows; then cygpath -w "$1"; else printf '%s' "$1"; fi; }

# A layout whose only pane runs a long command, titled $1 when given.
long_layout() {
  local name=${1:+ name=\"$1\"}
  if $windows; then
    printf 'layout {\n    pane%s command="ping" {\n        args "-n" "900" "127.0.0.1"\n    }\n}\n' "$name"
  else
    printf 'layout {\n    pane%s command="sleep" {\n        args "900"\n    }\n}\n' "$name"
  fi
}
long_layout >"$tmp/long.kdl"
long_layout "Plan the launch" >"$tmp/launch.kdl"
start() { # name [layout]
  tmo 30 "$Z" --layout "$(native "${2:-$tmp/long.kdl}")" attach --create-background "$1" >/dev/null 2>&1
}

step "versions"
show "$Z" --version
show "$R" version

step "sessions"
start busy-one
start to-rename
start wise-panda "$tmp/launch.kdl"
tmo 30 "$Z" attach --create-background idle-one >/dev/null 2>&1
sleep 3
# zellij writes a session's data a moment after the session starts, and on a
# busy runner later than that: once on Windows the first session of the run
# had none after 4 s, and the pass rightly kept it as "no session data yet".
# Wait, up to a minute, until a pass has data for every session.
for _ in $(seq 60); do has_data && break; sleep 1; done
show "$Z" list-sessions --no-formatting
check "the sessions are up" listed busy-one
check "the idle one is up" listed idle-one

step "what a pass sees"
show "$R" plan
check "a session running a command is kept" is busy-one busy
check "a shell at its prompt waits its turn" is idle-one idle-recent
check "doctor names how renamed sessions are found" sh -c "'$R' doctor | grep -q 'renamed sessions are matched'"

step "here, from inside a pane"
rm -f "$tmp/here.out"
if $windows; then
  # Each word its own argument: zellij quotes an argument with spaces in it,
  # which cmd cannot read back.
  tmo 20 "$Z" --session busy-one run -- cmd /c "$(native "$R")" here '>' "$(native "$tmp/here.out")" >/dev/null 2>&1
else
  tmo 20 "$Z" --session busy-one run -- sh -c "'$R' here >'$tmp/here.out' 2>&1" >/dev/null 2>&1
fi
for _ in $(seq 120); do [ -s "$tmp/here.out" ] && break; sleep 0.25; done
show cat "$tmp/here.out"
check "here names the session" sh -c "tr -d '\r' <'$tmp/here.out' | grep -qx busy-one"

step "a rename"
show tmo 20 "$Z" --session to-rename action rename-session renamed-one
sleep 2
show "$Z" list-sessions --no-formatting
if $windows; then
  # zellij on Windows lists the new name but cannot reach it.
  check "zellij cannot reach a renamed session" sh -c "! perl -e 'alarm 10; exec @ARGV' '$Z' --session renamed-one action list-clients"
  check "the pass keeps it without asking zellij" is renamed-one attach-unknown
else
  check "zellij reaches the renamed session" tmo 20 "$Z" --session renamed-one action list-clients
  check "its server is found by its socket file" is renamed-one busy
fi

step "install"
show "$R" install
show "$R" doctor
check "doctor finds nothing wrong" sh -c "'$R' doctor | grep -q 'everything looks fine'"
if $windows; then
  check "the task is registered" powershell.exe -NoProfile -Command "Get-ScheduledTask -TaskName 'zellij-reaper' | Out-Null"
  check "and no names task, since nothing is renamed" powershell.exe -NoProfile -Command "if (Get-ScheduledTask -TaskName 'zellij-reaper-names' -ErrorAction SilentlyContinue) { exit 1 }"
  check "doctor says why names stay" sh -c "ZELLIJ_REAPER_AUTO_RENAME=true '$R' doctor | grep -q 'cannot reach a session after a rename'"
else
  check "the agent is loaded" launchctl print "gui/$(id -u)/io.github.kanggu12.zellij-reaper"
  check "the names agent is loaded" launchctl print "gui/$(id -u)/io.github.kanggu12.zellij-reaper-names"
fi

step "passes"
printf 'idle_after = "1m"\nauto_rename = true\n' >>"$("$R" config path | tr -d '\r')"
show "$R" run --names-only
if $windows; then
  check "a names pass renames nothing" listed wise-panda
  check "and says why" sh -c "'$R' run --names-only | grep -q 'cannot reach a session after a rename'"
else
  check "a names pass names a session after its title" listed plan-the-launch
fi
show "$R" run --force
show "$Z" list-sessions --no-formatting
check "a session running a command stays" listed busy-one
check "the idle session is reaped" gone idle-one

step "an idle session waits out idle_after"
# Without --force: the first pass finds it too fresh, one past the wait reaps
# it. On Windows, where no terminal can be read, idle time counts from that
# first pass, since zellij rewrites its session files every minute.
tmo 30 "$Z" attach --create-background idle-two >/dev/null 2>&1
sleep 3
show "$R" run
check "a fresh idle session stays" listed idle-two
if $windows; then
  info="$(cygpath "$LOCALAPPDATA")/Zellij/cache/contract_version_1/session_info"
else
  info="$HOME/Library/Caches/org.Zellij-Contributors.Zellij/contract_version_1/session_info"
fi
sleep 65
# How old zellij's files for an idle session are after a minute: a heartbeat
# keeps them young.
show perl -e 'printf "idle-two session files: %ds old\n", time - (stat shift)[9]' "$info/idle-two/session-metadata.kdl"
show "$R" run
check "past idle_after it is reaped" gone idle-two
show "$R" logs -n 30
show "$R" tui --snapshot 120x30 --theme light

step "uninstall"
show "$R" uninstall --purge
if $windows; then
  check "the tasks are gone" powershell.exe -NoProfile -Command "if (Get-ScheduledTask -TaskName 'zellij-reaper*' -ErrorAction SilentlyContinue) { exit 1 }"
else
  check "the agents are gone" sh -c "! ls '$HOME/Library/LaunchAgents/' | grep -q zellij-reaper"
fi
for s in $("$Z" list-sessions --no-formatting 2>/dev/null | tr -d '\r' | awk '{print $1}'); do "$Z" kill-session "$s" >/dev/null 2>&1; done

echo
echo "passed $pass, failed $fail"
[ "$fail" = 0 ]
