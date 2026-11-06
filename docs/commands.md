# zellij-reaper commands

`zellij-reaper --help` and `zellij-reaper COMMAND --help` say the same in short. Every command takes `--config FILE`.

## Looking

**`zellij-reaper`** opens [the dashboard](#the-dashboard) in a terminal. When its input or output is not a terminal, it
prints the plan as a table instead. **`zellij-reaper tui`** always opens the dashboard, and needs a terminal.

**`plan`** (also `status`, `ls`) shows what the next pass would do, changing nothing: no lock, no log, no deletes.
The table is sorted by what happens to each session; `sessions` in the JSON keeps zellij's order. `--json` prints a shape
that stays stable for scripts; `reason` is the log's wording:

```json
{
  "generated": "2026-11-04T09:31:00+09:00",
  "dry_run": false,
  "idle_after": "12h",
  "exited_after": "3d",
  "sessions": [
    {
      "name": "scratch",
      "state": "running",
      "last_active": "2026-11-04T06:31:00+09:00",
      "idle_seconds": 10800,
      "clients": 0,
      "action": "reap",
      "code": "idle-old",
      "reason": "REAP IDLE, 3h since last activity",
      "server_pid": 4242,
      "panes": [{"pid": 4243, "command": "/bin/bash", "prompt": true}]
    }
  ]
}
```

`wait_seconds`, `server_pid`, `last_active`, `panes`, `would_rename` (with `auto_rename` on) and a pane's `busy` appear
only when they apply. In the table, a session the next pass renames shows `→ NEW-NAME` after its verdict, as room
allows. `clients` is -1 when it is not known, as on every exited session.

**`here`** prints the current name of this shell's zellij session, read from `/proc`, the kernel's socket table and the
socket directory, with no zellij process; outside zellij, or when the name cannot be told, it prints nothing and exits
with status 1. A pane keeps the name its session had when the pane opened, in `ZELLIJ_SESSION_NAME`, and after a rename
a zellij command run there waits for a session by that name. **`shell-hook bash|zsh|fish`** prints a prompt hook that sets
`ZELLIJ_SESSION_NAME` from `here` at every prompt and keeps the status of the command before; see
[setup](setup.md#naming-sessions-after-what-they-show).

**`logs`** prints the last 50 lines of the decision log (`-n` for another number), colored by kind. `-f` keeps
printing new lines and follows the log across rotations.

**`doctor`** checks zellij and its version, the timers' service (systemd, launchd or the Task Scheduler), the timer
(installed, enabled, running this binary, the interval it was installed with), the names timer when `auto_rename` is
on, lingering on Linux, the settings, settings that only your shell sees, zellij's session directory, how renamed
sessions are matched to their servers and the log. It exits with status 1
when something is broken; a warning alone still exits 0.

## Acting

**`run`** runs one pass now, as the timer does, and prints a table of what it did, with the totals. `--dry-run` only
logs; `--force` sets both waits to zero for this pass; `--names-only` renames what a pass would and deletes nothing,
writing to the log only when it renames (the names timer's mode); `--quiet` prints nothing (the timers' mode). The exit
status is 0 when the pass ran, when another pass holds the lock, or when zellij is missing; 1 on any other error, such
as zellij failing to list its sessions.

**`protect NAME...`** adds sessions to the protected list; with no name it prints the list. **`unprotect NAME...`**
takes them off. A name the `protect` pattern matches stays protected either way, and `unprotect` says so.

**`keep-name NAME...`** keeps running sessions' names as they are, as `r`, Tab and Enter do in the dashboard: no pass
renames them. With no name it prints the kept names. **`forget-name NAME...`** takes names off that list, so passes may
rename those sessions again. Both wait for a pass that holds the lock, and log what they did.

**`recover [--dry-run] [--aggressive]`** repairs sessions that no zellij command can reach. A session's name is part
of its socket path, which Linux caps at 107 bytes and macOS at 103; a session renamed past that keeps a server nobody can talk to, and
its files come back after every delete. `recover` deletes such sessions and clears their files. `--aggressive` also
stops servers that serve no socket file at all, unless a client is still connected to one, a pane in it is busy, a
protected program runs in it, its name is protected, or any of that cannot be read; it looks once more just before
each stop. A renamed session's server still serves its socket and is left alone. A real run takes the pass lock.

**`resize [--dry-run] [--shallow]`** (also `refresh`) sends `SIGWINCH` to every zellij client, every server and every
program in a pane, so each one reads its terminal size again. Use it when a program is stuck at an old size after the
window changed. `--shallow` signals the clients only.

## Setting up

**`install`** writes a settings file and the timers, the systemd units with lingering on Linux, the launch agents on
macOS and the scheduled tasks on Windows, and says when the first pass runs. **`uninstall`** removes both timers and their services; `--purge` also removes the settings,
the log, the protected list and the lists of given and kept names. See [setup](setup.md#install).

**`config`** has four subcommands: `show` (the default) prints every setting with where its value comes from, `path`
prints the file's path, `init` writes the file with every key at its default, and `edit` opens it in `$VISUAL` or
`$EDITOR` (or `vi`) and refuses a value it cannot read or a key it does not know.

**`version`** prints the version; **`completion bash|zsh|fish|powershell`** prints a completion script.

## The dashboard

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/screenshot-dark.png">
  <img alt="The dashboard: the sessions with what the next pass does to each, the checks for the selected one, and the latest decisions" src="assets/screenshot-light.png">
</picture>

The top bar shows zellij's version, whether passes delete (`reaping on`) or only log (`dry run`), both waits
(`reaps after 12h idle, 3d exited`) and when the timer runs next. The table has a row per session: its state, how long it
has been idle, its clients, and what the next pass does to it. Rows are sorted by outcome: deleted, waiting, unclear,
attached, busy, protected. The panel beside it (below it on terminals under 132 columns) explains the selected session:
the verdict in a sentence, every check with its evidence and where it came from, its panes, and the settings that apply.
Under a short table, the latest reaps, renames and kept names fill the column.

The color of a row says what will happen to it:

| Color | Means |
|:---:|:---|
| yellow | deleted at the next pass |
| grey | idle, waiting out `idle_after` or `exited_after` |
| blue | a program runs in it |
| green | a client is attached |
| violet | protected, by name or by a running program |
| red | a fact could not be read, so it is kept |

| Key | Does |
|:---:|:---|
| `↑` `↓`, `k` `j` | select a session |
| `g` `G`, `PgUp` `PgDn` | first, last, a page up or down |
| `⏎` | attach to it; inside zellij, switch this client to it (zellij 0.44 and later: 0.43 has no command for that) |
| `d` | reap it now, after asking (the question names what still runs in it); refused while a client is attached or zellij cannot say |
| `p` | protect it, or lift the protection |
| `r` | rename it; the field starts with [the name its panes point to](setup.md#naming-sessions-after-what-they-show), and `⇥` swaps it with the name the session has. A name that is taken is refused, and an exited session cannot be renamed |
| `f` | run a pass now; it asks first when the pass would delete something |
| `F` | run a pass without the idle and exited waits, after asking |
| `l` | the decision log: `↑` `↓` scroll, `f` follows new lines, `esc` returns |
| `/` | filter by name as you type: `⏎` keeps the filter, `esc` puts back the old one |
| `esc` | clear the filter; it never quits |
| `R` | read the sessions again now |
| `?` | every key and what each color means |
| `q`, `ctrl+c` | quit |

To keep a running session's current name, press `r`, `⇥` and `⏎`: passes leave it alone from then on.

The dashboard reads the sessions every five seconds. `d`, `r`, `f` and `F` take the pass lock and go to the decision
log, like a pass's work; `p` changes the protected list under that list's own lock and is not logged. It speaks English,
and Korean when `LC_ALL`, `LC_MESSAGES` or `LANG` starts with `ko`; `ZELLIJ_REAPER_LANG=en` or `ko` picks one. The
colors follow the terminal's background, and `NO_COLOR` turns them off.
