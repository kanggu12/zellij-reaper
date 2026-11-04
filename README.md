<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
    <img alt="zellij-reaper" src="docs/assets/logo-light.svg" width="520">
  </picture>
</p>

<p align="center">
  Deletes the zellij sessions you stopped using. Keeps the ones you use.
</p>

<p align="center">
  <a href="https://github.com/kanggu12/zellij-reaper/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/kanggu12/zellij-reaper/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/kanggu12/zellij-reaper/releases"><img alt="Release" src="https://img.shields.io/github/v/release/kanggu12/zellij-reaper?sort=semver"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-2a78d6.svg"></a>
</p>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/screenshot-dark.png">
  <img alt="The zellij-reaper dashboard: twelve sessions sorted by what the next pass does to them, the checks for the selected one, and the latest reaps" src="docs/assets/screenshot-light.png">
</picture>

zellij keeps every session until you delete it: the ones you detached from last week, and the exited ones it keeps
for resurrection. zellij-reaper runs once an hour from a systemd user timer and deletes the sessions nobody uses.
Every check has to pass before a session goes; when a fact is unclear, the session stays.

## What goes and what stays

| Session | Next pass |
|:---|:---|
| Running, no client attached, every pane at a shell prompt | **deleted** once idle for 12 hours (`idle_after`) |
| Exited (`EXITED - attach to resurrect`) | **deleted** 3 days after it exited (`exited_after`) |
| A client attached, in any terminal or a browser | kept |
| A pane running anything: a build, `npm run dev`, `tail -f`, an editor, a background job | kept |
| A coding agent, even waiting at its prompt: Claude Code, Codex, Gemini CLI, aider, opencode | kept |
| A name you protect | kept |
| Anything it cannot read for sure | kept |

To reap a session is to delete it (`zellij delete-session NAME --force`): it is gone with its scrollback and cannot be
resurrected. [How it decides](docs/how-it-decides.md) lists every check in order.

## Install

Linux or WSL 2, with zellij 0.43 to 0.45 and a systemd user instance (`systemctl --user` works):

```sh
curl -fsSL https://raw.githubusercontent.com/kanggu12/zellij-reaper/main/install.sh | sh
```

The script checks the download against the release's checksums, puts `zellij-reaper` in `~/.local/bin` and starts the
timer, with lingering so it also runs after you log out. The first pass runs ten minutes later. Look before it does
(this machine waits one hour, not twelve, before reaping an idle session):

```
$ zellij-reaper plan
zellij-reaper · zellij 0.45.1 · reaping on · reaps after 1h idle, 3d exited · next pass 23:06

  SESSION         STATE    IDLE  CLIENTS  NEXT PASS
● old-experiment  exited   5d    –        reap
● scratch         running  3h    0        reap
● release-v2      running  50m   0        reap in 9m
● docs-review     running  25m   0        reap in 34m
● k8s-debug       exited   1d    –        reap in 1d 23h
● notes           running  1s    1        attached
● api-server      running  3h    0        running npm run dev
● db-shell        running  3h    0        running psql app_dev
● logs            running  3h    0        running tail -f app.log
● monitoring      running  3h    0        running watch df -h
● agent-refactor  running  3h    0        protected: claude
● dotfiles        running  3h    0        protected (list)

  12 sessions · 2 to reap at the next pass · run `zellij-reaper` for the dashboard
```

`zellij-reaper protect NAME` keeps a session out of every pass. To start with passes that only log, run
`zellij-reaper config edit` and set `dry_run = true`. Packages (`.deb`, `.rpm`, `.apk`, Arch), release
archives and `go install` are in [setup](docs/setup.md#install), with upgrading, uninstalling and WSL 2.

## Try it in a sandbox

```sh
docker run --rm -it ghcr.io/kanggu12/zellij-reaper-demo
```

The dashboard opens on twelve sessions in a throwaway container, one of every kind above. Reap, protect, rename and
run passes there as you like; your own sessions are not touched. Staging the sessions takes about a minute. Append
`--shell` for a shell in the same sandbox, and `-e ZELLIJ_REAPER_LANG=ko` for Korean.

From a clone, `make demo` builds the same sandbox from the working tree (it needs Go), and `make demo DEMO_SHELL=1`
opens its shell.

## The dashboard

`zellij-reaper` opens it. Sessions are sorted by what the next pass does to them; the panel shows the checks for the
selected one with their evidence, its panes and the settings that apply.

| Key | Does | Key | Does |
|:---:|:---|:---:|:---|
| `↑` `↓` | select a session | `f` | run a pass now (asks first when it would reap) |
| `⏎` | attach (inside zellij: switch to it) | `F` | run a pass without the idle and exited waits, after asking |
| `d` | reap it now, after asking | `l` | the decision log |
| `p` | protect it, or lift the protection | `/` | filter by name |
| `r` | rename it | `?` | every key and what each color means |

It speaks English, and Korean when the locale is Korean (`ZELLIJ_REAPER_LANG=en` or `ko` picks one).

## Commands

| Command | Does |
|:---|:---|
| `zellij-reaper` | the dashboard; the plan as a table when the output is not a terminal |
| `plan [--json]` | what the next pass would do, changing nothing |
| `run [--dry-run] [--force]` | one pass now, as the timer runs it |
| `logs [-n 50] [-f]` | the decision log |
| `protect NAME` / `unprotect NAME` | keep a session out of every pass, or let it go again |
| `install` / `uninstall [--purge]` | the systemd user timer |
| `config [show\|path\|init\|edit]` | the settings and where each value comes from |
| `doctor` | check zellij, systemd, lingering, the settings and the log |
| `recover [--dry-run] [--aggressive]` | repair sessions whose name is too long for their socket |
| `resize [--dry-run] [--shallow]` | make zellij and the programs in its panes redraw at the right size |

[Commands](docs/commands.md) has every flag and the `plan --json` shape.

## Settings

`install` writes `~/.config/zellij-reaper/config.toml` with every key at its default, commented out. Set what you
change:

```toml
idle_after = "4h"           # reap idle sessions after 4 hours (default 12h)
exited_after = "1d"         # and exited ones after a day (default 3d)
protect = "^(keep-|work-)"  # names that are never reaped or renamed
auto_rename = true          # name the sessions it keeps after what they show
```

`zellij-reaper config show` prints the values in effect and where each one comes from. [Setup](docs/setup.md#settings)
lists every key.

## The log

Every pass writes one line per session, in words that stay the same across releases:

```
$ zellij-reaper logs
11-03 22:24:28  === reaper start (dry_run=false idle_after=1h exited_after=3d) ===
11-03 22:24:28  old-experiment [EXITED] panes={Pane #1|sleep 5000} :: REAP EXITED, 5d since last activity
11-03 22:24:28    -> deleted: old-experiment
11-03 22:24:28  k8s-debug [EXITED] panes={Pane #1|sleep 5000} :: SKIP EXITED 1d ago, reaped after 3d
11-03 22:24:28  api-server [RUNNING] panes={Pane #1|npm run dev} :: SKIP running in a pane: npm run dev
11-03 22:24:28  notes [RUNNING] panes={Pane #1} :: SKIP client attached (1 client(s) attached)
11-03 22:24:28  agent-refactor [RUNNING] panes={Pane #1|claude} :: SKIP protected command running (claude)
11-03 22:24:28  db-shell [RUNNING] panes={Pane #1|psql app_dev} :: SKIP running in a pane: psql app_dev
11-03 22:24:28  monitoring [RUNNING] panes={Pane #1|watch df -h} :: SKIP running in a pane: watch df -h
11-03 22:24:28  logs [RUNNING] panes={Pane #1|tail -f app.log} :: SKIP running in a pane: tail -f app.log
11-03 22:24:28  scratch [RUNNING] panes={Pane #1} :: REAP IDLE, 3h since last activity
11-03 22:24:28    -> deleted: scratch
11-03 22:24:28  docs-review [RUNNING] panes={Pane #1} :: SKIP IDLE for 25m, reaped after 1h
11-03 22:24:28  release-v2 [RUNNING] panes={Pane #1} :: SKIP IDLE for 50m, reaped after 1h
11-03 22:24:28  dotfiles [RUNNING] panes={Pane #1} :: SKIP protected by protected list
11-03 22:24:28  === reaper end (2 reaped, 10 kept, 0 renamed) ===
```

It lives in `~/.local/state/zellij-reaper/` and moves to `reaper.log.1` past `log.max_bytes` (1 MiB).

## Questions

**Will it delete the session I am in?** No. A session with a client attached is kept, and the pass asks zellij again
just before each delete: a client that attached in between keeps the session.

**My coding agent waits at its prompt for hours. Is its session safe?** Yes, while the agent runs; once it exits, the
normal wait applies. The programs in `protect_commands` keep their session even when the pane looks idle, and they are
found under any name they run as (`node .../claude-code/cli.js` counts as `claude`).

**Can I get a deleted session back?** No: zellij removes its resurrection data too. To watch first, set
`dry_run = true`; every pass then only writes what it would do to the log.

**What does it touch?** It runs as you, without root. It reads `/proc`, the kernel's socket table and zellij's files,
runs `zellij` and `systemctl --user`, and writes only its own settings, state and unit files. It makes no network
connection.

**Why not `zellij delete-all-sessions`?** That deletes every exited session at once (with `--force`, every session),
used or not. zellij-reaper looks at each session and keeps the ones in use.

**How do I pause it?** Set `dry_run = true` to keep it logging without deleting, or stop the timer (`enable --now`
brings it back):

```sh
systemctl --user disable --now zellij-reaper.timer
```

**Does it run on macOS?** No. It reads Linux's `/proc` and socket table and runs from a systemd timer.

**I used the shell scripts (`zellij-reaper.sh`, `zellij-reap`). What happens to them?** `zellij-reaper install` moves
them over: their settings, their log, and their dry-run setting. See
[setup](docs/setup.md#coming-from-the-shell-scripts).

## Documentation

- [How it decides](docs/how-it-decides.md): a pass step by step, every check, the log's words.
- [Setup](docs/setup.md): installing, every setting, protecting and naming sessions, zellij versions.
- [Commands](docs/commands.md): every command and flag, the dashboard's keys and colors.
- [Troubleshooting](docs/troubleshooting.md): what `doctor` says, sessions kept longer than you expect, WSL 2.
- [Internals](docs/internals.md): how the facts are read, the design decisions, the tests.

## Contributing

Bug reports with `zellij-reaper plan --json` and the log lines for the session help most.
[CONTRIBUTING.md](CONTRIBUTING.md) covers building, the tests (fake machines for the rules, real zellij in
containers) and the bar for a change.

## License

MIT ([LICENSE](LICENSE)). The libraries zellij-reaper is built from and the font its logo is drawn from are listed
with their licenses in [THIRD_PARTY_NOTICES.md](docs/THIRD_PARTY_NOTICES.md).
