#!/usr/bin/env bash
# Runs zellij-reaper against real zellij sessions on the machine it runs on:
# made for CI runners of macOS and Windows, where no container can stand in.
# It starts sessions, renames one, and prints what zellij and zellij-reaper
# report, so a run shows how zellij behaves on that system. Never run it on a
# machine whose sessions matter: it deletes sessions.
set -uo pipefail
R=${R:-./zellij-reaper}
Z=${Z:-zellij}
step() { printf '\n==== %s\n' "$*"; }
# macOS has no timeout(1); perl, which macOS and Git for Windows both have,
# does the same.
tmo() { local s=$1; shift; perl -e 'alarm shift; exec @ARGV or die "$!"' "$s" "$@"; }
show() { printf '$ %s\n' "$*"; "$@" 2>&1 | head -60; printf '(exit %s)\n' "${PIPESTATUS[0]}"; }

# Git Bash hands zellij its own SHELL, a Unix path a Windows zellij cannot
# start; without it zellij picks the system's shell.
case "$(uname -s)" in
  MINGW* | MSYS* | CYGWIN*) unset SHELL ;;
esac

step "versions"
show "$Z" --version
show "$R" version
uname -a

step "start sessions"
for s in idle-one busy-one; do
  tmo 30 "$Z" attach --create-background "$s" >/dev/null 2>&1
  echo "started $s: $?"
done
sleep 3
tmo 20 "$Z" --session busy-one run -- sleep 600 >/dev/null 2>&1
sleep 3
show "$Z" list-sessions --no-formatting

step "where zellij keeps things"
case "$(uname -s)" in
  Darwin)
    show ls -la "${TMPDIR:-/tmp}"/zellij-"$(id -u)"/contract_version_1/
    show ls -la "$HOME/Library/Caches/org.Zellij-Contributors.Zellij/contract_version_1/session_info/"
    show ps -axo pid,ppid,tty,lstart,command
    ;;
  MINGW* | MSYS* | CYGWIN*)
    show ls -la "$(cygpath "$TEMP")/zellij/contract_version_1/"
    for f in "$(cygpath "$TEMP")"/zellij/contract_version_1/*; do echo "$f: $(head -c 40 "$f" 2>/dev/null)"; done
    show ls -la "$(cygpath "$LOCALAPPDATA")/Zellij/cache/contract_version_1/session_info/"
    show ls -laR "$(cygpath "$TEMP")/zellij/"
    for f in "$(cygpath "$TEMP")"/zellij/zellij-log/*.log; do echo "== $f"; tail -40 "$f"; done
    show powershell.exe -NoProfile -Command "Get-CimInstance Win32_Process | Where-Object { \$_.Name -match 'zellij|conhost|OpenConsole|sleep|bash|pwsh|cmd' } | Select-Object ProcessId,ParentProcessId,Name,CommandLine | Format-Table -AutoSize -Wrap | Out-String -Width 300"
    ;;
esac

step "zellij-reaper sees"
show "$R" doctor
show "$R" plan
show "$R" plan --json
show "$R" tui --snapshot 120x30

step "list-clients"
show tmo 20 "$Z" --session idle-one action list-clients

if [ "$(uname -s)" = Darwin ]; then
  step "socket files and servers by time"
  show go run ./test/native/probe
fi

step "rename idle-one to renamed-one, then reach it"
show tmo 20 "$Z" --session idle-one action rename-session renamed-one
sleep 2
show "$Z" list-sessions --no-formatting
show tmo 20 "$Z" --session renamed-one action list-clients
show "$R" plan --json
if [ "$(uname -s)" = Darwin ]; then show go run ./test/native/probe; fi

step "install the timers"
show "$R" install
show "$R" doctor
case "$(uname -s)" in
  MINGW* | MSYS* | CYGWIN*)
    # The status script zellij-reaper runs, as it runs it.
    cat >"$TEMP/status.ps1" <<'PS'
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [Text.Encoding]::UTF8
$t = Get-ScheduledTask -TaskName 'zellij-reaper'
$i = $t | Get-ScheduledTaskInfo
$a = $t.Actions[0]
$when = { param($d) if ($d -and $d.Year -gt 2000) { $d.ToUniversalTime().ToString('o') } else { '' } }
[pscustomobject]@{ State = [string]$t.State; Last = & $when $i.LastRunTime; Next = & $when $i.NextRunTime;
  Exec = [string]$a.Execute; Args = [string]$a.Arguments; Interval = [string]$t.Triggers[0].Repetition.Interval } | ConvertTo-Json -Compress
PS
    show powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$(cygpath -w "$TEMP/status.ps1")"
    show powershell.exe -NoProfile -Command "Get-ScheduledTask -TaskName zellij-reaper | Select-Object TaskName,State | Format-List; (Get-ScheduledTask -TaskName zellij-reaper).Triggers[0].Repetition | Format-List; Get-ScheduledTask -TaskName zellij-reaper | Get-ScheduledTaskInfo | Format-List; (Get-ScheduledTask -TaskName zellij-reaper).Actions[0] | Format-List"
    ;;
  Darwin)
    show ls -la "$HOME/Library/LaunchAgents/"
    show launchctl print "gui/$(id -u)/io.github.kanggu12.zellij-reaper"
    ;;
esac

step "a pass that reaps idle sessions now"
"$R" config init >/dev/null 2>&1
printf 'idle_after = "1m"\nauto_rename = true\n' >>"$("$R" config path)"
show "$R" config show
show "$R" run --dry-run
show "$R" run --force
show "$Z" list-sessions --no-formatting
show "$R" logs -n 40

step "uninstall"
show "$R" uninstall
