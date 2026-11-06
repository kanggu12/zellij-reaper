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
show() { printf '$ %s\n' "$*"; "$@" 2>&1 | head -60; printf '(exit %s)\n' "${PIPESTATUS[0]}"; }

step "versions"
show "$Z" --version
show "$R" version
uname -a

step "start sessions"
for s in idle-one busy-one; do
  timeout 30 "$Z" attach --create-background "$s" >/dev/null 2>&1
  echo "started $s: $?"
done
sleep 3
timeout 20 "$Z" --session busy-one run -- sleep 600 >/dev/null 2>&1
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
    for f in "$(cygpath "$TEMP")"/zellij/contract_version_1/*; do echo "$f: $(cat "$f" 2>/dev/null | head -c 40)"; done
    show ls -la "$(cygpath "$LOCALAPPDATA")/Zellij/cache/contract_version_1/session_info/"
    show powershell.exe -NoProfile -Command "Get-CimInstance Win32_Process | Where-Object { \$_.Name -match 'zellij|conhost|OpenConsole|sleep|bash|pwsh|cmd' } | Select-Object ProcessId,ParentProcessId,Name,CommandLine | Format-Table -AutoSize -Wrap | Out-String -Width 300"
    ;;
esac

step "zellij-reaper sees"
show "$R" doctor
show "$R" plan
show "$R" plan --json
show "$R" tui --snapshot 120x30

step "list-clients"
show timeout 20 "$Z" --session idle-one action list-clients

step "rename idle-one to renamed-one, then reach it"
show timeout 20 "$Z" --session idle-one action rename-session renamed-one
sleep 2
show "$Z" list-sessions --no-formatting
show timeout 20 "$Z" --session renamed-one action list-clients
show "$R" plan --json

step "install the timers"
show "$R" install
show "$R" doctor

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
