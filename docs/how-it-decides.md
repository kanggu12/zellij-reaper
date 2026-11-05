# How zellij-reaper decides

A pass deletes a session only when every check below has passed on a fact it could read. A fact it cannot read keeps
the session: keeping a session one more hour costs nothing, deleting one in use cannot be undone.

## In short

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/goes-dark.svg">
  <img alt="A running session is deleted only if it is not protected, has no client attached, every pane is at a shell prompt and it has been idle long enough. Each check it fails keeps it, with the words the dashboard shows. An exited session is deleted once it has been gone long enough, unless protected. A fact it cannot read keeps the session too." src="assets/goes-light.svg">
</picture>

Idle time counts from the last input or output on any of the session's terminals, so coming back to a session starts
its wait over:

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/when-dark.svg">
  <img alt="A timeline over 16 hours with a pass every hour. A session you detach from after 2 hours is deleted by the first pass once 12 hours have gone by with nothing happening in it. A session you come back to starts its wait over. A session running a program is kept for as long as it runs." src="assets/when-light.svg">
</picture>

## A pass, step by step

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/pass-dark.svg">
  <img alt="A pass takes the lock and rotates the log, lists the sessions, reads one snapshot of the kernel's tables and zellij's files, and decides every session from it. Then it goes through them one at a time, writing each verdict to the log. Before it deletes or renames a session it looks again, and a session that changed is kept." src="assets/pass-light.svg">
</picture>

1. **Takes the pass lock.** The timer, `zellij-reaper run` and the dashboard's actions share one lock, so two of them
   never act at once. A pass waits five seconds for it and a dashboard action two seconds, then leaves the work to
   whoever holds it. The protected list is read again once the lock is held.
2. **Rotates the log** to `reaper.log.1` if it has grown past `log.max_bytes`, so a pass never spans two files.
3. **Lists the sessions** with `zellij list-sessions --no-formatting`. zellij answers "No active zellij sessions found."
   with the same exit status as an error; the pass tells the two apart and logs `no sessions`.
4. **Reads the machine once:** one snapshot of `/proc`, one of the kernel's Unix socket table, and each session's files
   in zellij's cache directory. Every session in the pass is judged against the same moment.
5. **Tells who is attached** from that socket table. Only a session the table cannot answer for is asked, with
   `zellij action list-clients`, one at a time.
6. **Decides every session** with the checks below, all from that one snapshot.
7. **Goes through the sessions one at a time** and logs each one's line. Just before it deletes a session, it asks
   again: a running session for its clients, an exited one whether it is still listed as exited, and it reads the
   protected list once more. Anything that changed keeps the session, logged as `-> kept: NAME :: reason`. Otherwise
   it runs `zellij delete-session NAME --force`.
8. **Renames a session it kept** when `auto_rename` is on, its name is not one someone chose, and its panes point to
   another name. It asks the session for its clients first.

A dry run (`dry_run = true`, or `run --dry-run`) does steps 1 to 6, and in steps 7 and 8 only logs what it would do.
`plan` and the dashboard do steps 3 to 6 only: no lock, no log, no change.

## The checks

They run in this order, and the first check that matches decides. Its code is what `plan --json` reports and what the
dashboard's detail panel marks with ✗.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/checks-dark.png">
  <img alt="The checks in the dashboard for two sessions: scratch passes every check and goes at the next pass; api-server stops at 'every pane at a prompt' because a pane runs npm run dev, so it stays" src="assets/checks-light.png">
</picture>

| Code | Matches when | Log line |
|:---:|:---|:---|
| `protected-name` | the name matches `protect`, or is on the protected list | `SKIP protected by protect pattern`, `SKIP protected by protected list` |
| `no-info` | zellij has written nothing about the session yet | `SKIP no session_info dir (unknown state)` |
| `exited-live` | listed as exited, but a server still listens on its socket | `SKIP EXITED, but a server still serves it (pid 123)` |
| `protected-command` | a program from `protect_commands` runs in it, or its layout starts one | `SKIP protected command running (claude)` |
| `no-activity` | the time of its last use cannot be read | `SKIP cannot read activity time` |
| `exited-recent` | exited less than `exited_after` ago | `SKIP EXITED 5h ago, reaped after 3d` |
| `exited-old` | exited `exited_after` ago or more: **deleted** | `REAP EXITED, 4d 2h since last activity` |
| `no-socket` | a running session has no socket file | `SKIP socket file missing for running session` |
| `no-server` | no server process serves its socket | `SKIP server PID not found` |
| `attached` | a client is attached | `SKIP client attached (1 client(s) attached)` |
| `attach-unknown` | whether one is attached cannot be told | `SKIP attach-check uncertain (...)` |
| `no-panes` | the server has no pane processes: it is starting or stopping | `SKIP server has no pane processes (transient)` |
| `busy` | a pane runs anything but a shell at its prompt | `SKIP running in a pane: htop` |
| `idle-recent` | idle for less than `idle_after` | `SKIP IDLE for 12m, reaped after 1h` |
| `idle-old` | idle `idle_after` or more: **deleted** | `REAP IDLE, 2h 14m since last activity` |

`plan` and the dashboard's NEXT PASS column put each code in fewer words:

| Code | In `plan` |
|:---:|:---|
| `protected-name` | `protected (list)` or `protected (pattern)` |
| `no-info` | `no session data yet` |
| `exited-live` | `exited, server still up` |
| `protected-command` | `protected: claude` |
| `no-activity` | `last use unknown` |
| `exited-recent`, `idle-recent` | `reap in 34m` |
| `exited-old`, `idle-old` | `reap` |
| `no-socket` | `socket missing` |
| `no-server` | `server not found` |
| `attached` | `attached` |
| `attach-unknown` | `attach state unknown` |
| `no-panes` | `starting or stopping` |
| `busy` | `running htop` |

`exited-live` applies only to exited sessions, `protected-command` only to running ones. An exited session stops at
`exited-recent` or `exited-old`: it has no socket, server, clients or panes left to check.

`run --force` (`F` in the dashboard) sets both waits to zero for one pass. Every other check still applies.

## Where each fact comes from

- **Last use** of a running session is the newest read or write time of its panes' terminals (`/dev/pts/N`). The kernel
  moves it on input and output only, so a redraw or zellij's own bookkeeping does not count. Exited sessions use their files
  in zellij's cache, which stopped changing when the server ended.
- **Attached clients** come from the kernel's socket table: each connection a server accepted, with its other end still
  open, is one client. When the table cannot tell, `zellij action list-clients` asks the server itself, and when the
  server does not answer, the count zellij writes to its metadata file can add a client. Browser clients (zellij's web
  client) always count.
- **Busy panes.** Each pane is a child process of the session's server with a controlling terminal; a server's other
  children, like the `ps` zellij runs to save its layouts, are not panes. A pane is at a prompt when it is a known shell
  (`bash`, `zsh`, `fish`, `nu` and others, plus `shells`), started without a script, with no child process. A command in
  the foreground or the background, or a program the pane runs itself (`zellij run`, a command pane from a layout), makes
  it busy, and so does a job with its input redirected (`nohup`).
- **Protected programs** are looked for in every process under the server and in the commands of the saved layout. A
  program matches by its process name, its executable's name or the script an interpreter runs, as a whole word:
  `node .../claude-code/cli.js` matches `claude`, `claudette` does not.
- **The server** is found from the socket file: its inode leads, through the kernel's socket table, to the process
  listening on it. zellij keeps a server's command line when its session is renamed, so this is what finds renamed
  sessions. Without the socket table (a kernel without `unix_diag`) renamed sessions are kept as `no-server`.

## The log

`~/.local/state/zellij-reaper/reaper.log`: a start line with the settings of the pass, one line per session with its
state and pane titles, an arrow line for each thing done, and an end line with the counts.

```
[2026-11-04 09:31:00] === reaper start (dry_run=false idle_after=12h exited_after=3d) ===
[2026-11-04 09:31:00] api-server [RUNNING] panes={npm run dev} :: SKIP running in a pane: npm run dev
[2026-11-04 09:31:00] scratch [RUNNING] panes={Pane #1} :: REAP IDLE, 13h 2m since last activity
[2026-11-04 09:31:00]   -> deleted: scratch
[2026-11-04 09:31:00] === reaper end (1 reaped, 1 kept, 0 renamed) ===
```

Every line the log can hold:

| Line | Written when |
|:---|:---|
| `=== reaper start (dry_run=... idle_after=... exited_after=...) ===` | a pass starts; ` force` follows `exited_after` for `run --force` |
| `could not rotate PATH (N bytes) to PATH.1: ERROR` | the log could not move aside; the pass goes on |
| `NAME [RUNNING] panes={TITLES} :: VERDICT` | a session is decided; `[EXITED]` for an exited one |
| `  -> deleted: NAME` | the session was deleted |
| `  -> FAILED: NAME :: ERROR` | zellij refused the delete, or the session was still running five seconds later |
| `  -> kept: NAME :: REASON` | something changed just before the delete |
| `  -> renamed: OLD → NEW` | `auto_rename` renamed a survivor |
| `  -> kept name: NAME :: REASON` | a client attached before the rename |
| `could not read the list of kept names: ERROR, so no session is renamed` | the pass goes on, without renaming |
| `  -> could not record the name NAME: ERROR` | the list of given names could not be written; the rename goes on, and that name stays once it is given |
| `  -> rename FAILED: OLD → NEW :: ERROR` | zellij refused the rename |
| `  -> rename gave up: OLD → NEW (too many collisions)` | every numbered name was taken |
| `  -> DRY_RUN: would run: zellij delete-session NAME --force`, `  -> DRY_RUN: would rename 'OLD' → 'NEW'` | a dry run |
| `no sessions` | zellij lists none |
| `zellij not installed, nothing to do` | zellij is not on the service's `PATH` |
| `zellij list-sessions failed: ...`, `could not inspect the sessions: ...`, `could not read the protected list: ...` | the pass stops without acting |
| `NAME :: deleted from the dashboard`, `NAME :: renamed from the dashboard to NEW`, `NAME :: name kept from the dashboard` | done by hand in the dashboard |
| `=== reaper end (N reaped, N kept, N renamed) ===` | a pass ends; just `=== reaper end ===` after a pass that stopped early |

The words stay the same across releases, so months of the log can be grepped. New kinds of lines may be added.
`zellij-reaper logs -f` follows it across rotations; with `tail`, use `tail -F`.
