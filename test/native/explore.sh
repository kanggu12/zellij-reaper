#!/usr/bin/env bash
# Runs zellij-reaper against real zellij sessions on the machine it runs on:
# made for CI runners of macOS and Windows, where no container can stand in.
# It starts sessions, renames them, lets passes run, and prints what zellij
# and zellij-reaper report, so a run shows how zellij behaves on that system.
# Never run it on a machine whose sessions matter: it deletes sessions.
set -uo pipefail
R=${R:-$PWD/zellij-reaper$(go env GOEXE)}
Z=${Z:-zellij}
step() { printf '\n==== %s\n' "$*"; }
show() { printf '$ %s\n' "$*"; "$@" 2>&1 | head -80; printf '(exit %s)\n' "${PIPESTATUS[0]}"; }
# macOS has no timeout(1); perl, which macOS and Git for Windows both have,
# does the same.
tmo() { local s=$1; shift; perl -e 'alarm shift; exec @ARGV or die "$!"' "$s" "$@"; }
# A pass's verdict on one session, from plan --json.
verdict() { "$R" plan --json | tr -d '\r' | awk -v n="\"$1\"" '$1 == "\"name\":" && $2 == n "," { on = 1 } on && $1 == "\"code\":" { gsub(/[",]/, "", $2); print $2; exit }'; }

windows=false
case "$(uname -s)" in
  MINGW* | MSYS* | CYGWIN*)
    windows=true
    # Git Bash hands zellij its own SHELL, a Unix path a Windows zellij
    # cannot start.
    unset SHELL
    ;;
esac

step "versions"
show "$Z" --version
show "$R" version
uname -a

# A session that stays up on every runner: a pane running a long command. On
# a Windows runner a shell pane ends at once (its console has no input), so a
# session there has to run a command.
long_layout() {
  if $windows; then
    printf 'layout {\n    pane command="ping" {\n        args "-n" "900" "127.0.0.1"\n    }\n}\n'
  else
    printf 'layout {\n    pane command="sleep" {\n        args "900"\n    }\n}\n'
  fi
}
long_layout >"${TMPDIR:-/tmp}/long.kdl"
layout=${TMPDIR:-/tmp}/long.kdl
$windows && layout=$(cygpath -w "$layout")
start_long() { tmo 30 "$Z" --layout "$layout" attach --create-background "$1" >/dev/null 2>&1; echo "started $1: $?"; }

step "start sessions"
start_long busy-one
start_long to-rename
start_long to-exit
if ! $windows; then
  # A shell at its prompt, which a pass may reap.
  tmo 30 "$Z" attach --create-background idle-one >/dev/null 2>&1
  echo "started idle-one: $?"
fi
# A session zellij names itself, which auto_rename renames.
tmo 30 "$Z" --layout "$layout" attach --create-background >/dev/null 2>&1
echo "started one zellij names: $?"
sleep 3
show "$Z" list-sessions --no-formatting

step "where zellij keeps things"
if $windows; then
  show ls -la "$(cygpath "$TEMP")/zellij/contract_version_1/"
  for f in "$(cygpath "$TEMP")"/zellij/contract_version_1/*; do echo "$f: $(head -c 40 "$f" 2>/dev/null)"; done
  show ls -la "$(cygpath "$LOCALAPPDATA")/Zellij/cache/contract_version_1/session_info/"
else
  show ls -la "${TMPDIR:-/tmp}/zellij-$(id -u)/contract_version_1/"
  show ls -la "$HOME/Library/Caches/org.Zellij-Contributors.Zellij/contract_version_1/session_info/"
fi

step "zellij-reaper sees"
show "$R" doctor
show "$R" plan
show "$R" plan --json

step "here, from inside a pane"
out=${TMPDIR:-/tmp}/here.out
rm -f "$out"
if $windows; then
  wout=$(cygpath -w "$out")
  tmo 20 "$Z" --session busy-one run -- cmd /c "\"$(cygpath -w "$R")\" here > \"$wout\" 2>&1" >/dev/null 2>&1
else
  tmo 20 "$Z" --session busy-one run -- sh -c "'$R' here >'$out' 2>&1" >/dev/null 2>&1
fi
sleep 3
show cat "$out"

step "rename to-rename to renamed-one, then reach it"
show tmo 20 "$Z" --session to-rename action rename-session renamed-one
sleep 2
show "$Z" list-sessions --no-formatting
show tmo 20 "$Z" --session renamed-one action list-clients
show tmo 20 "$Z" --session to-rename action list-clients
if $windows; then
  show ls -la "$(cygpath "$TEMP")/zellij/contract_version_1/"
  show powershell.exe -NoProfile -Command "[System.IO.Directory]::GetFiles('\\\\.\\pipe\\') | Where-Object { \$_ -match 'zellij' }"
else
  show go run ./test/native/probe
fi
echo "verdict on renamed-one: $(verdict renamed-one)"

step "end to-exit, so it is EXITED"
show tmo 20 "$Z" kill-session to-exit
sleep 3
show "$Z" list-sessions --no-formatting
echo "verdict on to-exit: $(verdict to-exit)"

step "install the timers"
show "$R" install
show "$R" doctor
if $windows; then
  show powershell.exe -NoProfile -Command "Get-ScheduledTask -TaskName zellij-reaper* | Format-Table TaskName,State; Get-ScheduledTask -TaskName zellij-reaper | Get-ScheduledTaskInfo | Format-List LastRunTime,NextRunTime"
else
  show launchctl print "gui/$(id -u)/io.github.kanggu12.zellij-reaper"
fi

step "passes: names only, then one that reaps"
"$R" config init >/dev/null 2>&1
printf 'idle_after = "1m"\nexited_after = "1m"\nauto_rename = true\n' >>"$("$R" config path)"
show "$R" run --names-only
show "$Z" list-sessions --no-formatting
show "$R" run --dry-run
show "$R" run --force
show "$Z" list-sessions --no-formatting
show "$R" logs -n 40

step "the dashboard"
show "$R" tui --snapshot 120x30 --theme light

step "uninstall"
show "$R" uninstall --purge
for s in $("$Z" list-sessions --no-formatting 2>/dev/null | awk '{print $1}'); do "$Z" kill-session "$s" >/dev/null 2>&1; done
