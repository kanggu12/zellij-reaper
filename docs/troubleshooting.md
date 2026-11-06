# Troubleshooting zellij-reaper

Start with `zellij-reaper doctor`: one line per thing it needs, and what to do about each one that is off. Then
`zellij-reaper plan`, where every session's row says why it is kept.

## The timer does not run

- `systemctl --user status zellij-reaper.timer` says whether systemd knows it. If not, run `zellij-reaper install`.
- Without lingering (`loginctl show-user $USER -p Linger`) the timer stops when you log out. `install` turns it on;
  `doctor` says when it is off.
- On WSL 2, systemd has to be turned on, and the timer stops with the VM: see [setup](setup.md#wsl-2).
- On macOS, `launchctl print gui/$(id -u)/io.github.kanggu12.zellij-reaper` shows the launch agent; the first pass
  comes an hour after install.
- On Windows, the Task Scheduler lists `zellij-reaper` (`Get-ScheduledTask zellij-reaper` in PowerShell). It runs while
  you are logged in.

## The log says `zellij not installed`, but zellij works in your shell

The service has its own `PATH`. Run `zellij-reaper install` again from a shell where `zellij` resolves (its directory
goes first in the service's `PATH`), or set `zellij = "/path/to/zellij"` in the settings.

## A session you left hours ago is kept

Its row in `plan` says why, and the dashboard's panel shows which check stopped it.

- `running ...` (`busy`): something still runs in a pane. Often it is a background job started from the shell, or a
  pane zellij started with a command.
- `attached`: a client is still connected, maybe in another terminal, over SSH or in a browser.
- `reap in ...` (`idle-recent`): a pane's terminal was used more recently than you think, often a program printing to
  it.
- `server not found` (`no-server`) after a rename: the kernel has no `unix_diag` (`doctor` says so), so a renamed
  session cannot be matched to its server. `modprobe unix_diag` where that is possible.

## A session's name keeps a `-2`

Two sessions showed the same title, or the session had that name before, which zellij refuses to give back. The tail
stays, because a name changes only when what the session shows changes; with `auto_rename_drop_tail = true` it goes
once no other session has the name. With `r` you can give it the name without the tail, unless this session had that
name before. A name given by hand stays as it is from then on.

## A session is named after the wrong thing

A pass names a session after the focused pane of the tab a client looks at, or of the first tab when nobody is attached,
since zellij then marks no tab active. When that title says nothing, the other running panes come next. Its row in
`plan --json` lists `would_rename`, and `r` in the dashboard shows the name its panes point to. Give it a name with `r`
or `zellij action rename-session`, and passes leave that name alone. To keep the name a running session has now, press
`r`, then Tab and Enter. `auto_rename = false` turns renaming off.

## A `zellij` command hangs in a renamed session

A pane keeps the session name it was opened under, in `ZELLIJ_SESSION_NAME`, so after a rename a `zellij` command run
there without `--session` waits for a session by the old name while any other session is running. Press Ctrl+C, then
`export ZELLIJ_SESSION_NAME=$(zellij-reaper here)`. The prompt hook does that at every prompt:
`command -v zellij-reaper >/dev/null && eval "$(zellij-reaper shell-hook bash)"` in `~/.bashrc` (also `zsh` and `fish`).

## A name is slow to change

The names timer renames every `rename_interval` (5 minutes); without it, only the hourly pass does. `doctor` says when
it is not running: run `zellij-reaper install` again, which also picks up a changed `rename_interval`.
`zellij-reaper run --names-only` renames now.

## `plan` and the timer disagree

Settings from variables in your shell (`ZELLIJ_REAPER_PROTECT=...`) apply to the commands you run there, not to the
timer. `doctor` lists each one. Put lasting settings in the settings file.

## A program you leave running on purpose

A program running in a pane makes the pane busy, so its session is kept. A program started with `nohup` or `setsid`
that left the pane no longer counts: the pane looks idle. Protect that session by name.

## A session nothing can delete

A session renamed to a name longer than its socket path allows keeps a server no zellij command can reach, and its
files come back after every delete. `zellij-reaper recover --dry-run` shows what it would repair; without `--dry-run`
it repairs it.

## Reporting a problem

[CONTRIBUTING.md](../.github/CONTRIBUTING.md#report-a-problem) lists what to put in an issue. A security problem goes
through [SECURITY.md](../.github/SECURITY.md) instead.
