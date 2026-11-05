<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/logo-dark.svg">
    <img alt="zellij-reaper" src="docs/assets/logo-light.svg" width="520">
  </picture>
</p>

<p align="center">
  <b>Deletes the zellij sessions you stopped using. Keeps the ones you use.</b><br>
  Once an hour it looks at every session: who is attached, what runs in its panes, when it was last used.
</p>

<p align="center">
  <a href="https://github.com/kanggu12/zellij-reaper/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/kanggu12/zellij-reaper/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://github.com/kanggu12/zellij-reaper/releases"><img alt="Release" src="https://img.shields.io/github/v/release/kanggu12/zellij-reaper?sort=semver"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-2a78d6.svg"></a>
  <a href="https://github.com/kanggu12/zellij-reaper/pkgs/container/zellij-reaper-demo"><img alt="Demo image" src="https://img.shields.io/badge/demo-ghcr.io-2a78d6.svg"></a>
</p>

<p align="center">
  <a href="#install">Install</a> |
  <a href="#what-goes-and-what-stays">What goes, what stays</a> |
  <a href="#names-that-say-what-a-session-is">Names</a> |
  <a href="#the-dashboard">Dashboard</a> |
  <a href="docs/README.md">Documentation</a> |
  <a href=".github/CONTRIBUTING.md">Contributing</a>
</p>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/screenshot-dark.png">
  <img alt="The zellij-reaper dashboard: twelve sessions sorted by what the next pass does to them, the checks for the selected one, and the latest reaps" src="docs/assets/screenshot-light.png">
</picture>

zellij keeps every session until you delete it: the ones you detached from last week, and the exited ones it keeps
for resurrection. zellij-reaper runs from a systemd user timer and deletes the sessions nobody uses. Every check has
to pass before a session goes; when a fact is unclear, the session stays.

## What goes and what stays

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/goes-dark.svg">
  <img alt="A running session is deleted only if it is not protected, has no client attached, every pane is at a shell prompt and it has been idle long enough. Each check it fails keeps it, with the words the dashboard shows: protected: claude, attached, running npm run dev, reap in 9m. An exited session is deleted once it has been gone long enough, unless protected. A fact it cannot read keeps the session too." src="docs/assets/goes-light.svg">
</picture>

The waits are yours to set: 12 hours idle (`idle_after`) and 3 days exited (`exited_after`) by default.

- **Attached** means any client, in any terminal or a browser.
- **Running** means anything but a shell at its prompt: a build, `npm run dev`, `tail -f`, an editor, a background job.
- **Protected** means a name you protect, or a coding agent at work, even one waiting at its prompt: Claude Code,
  Codex, Gemini CLI, aider, opencode.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/when-dark.svg">
  <img alt="A timeline over 16 hours with a pass every hour. A session you detach from after 2 hours is deleted by the first pass once 12 hours have gone by with nothing happening in it. A session you come back to starts its wait over. A session running a program is kept for as long as it runs." src="docs/assets/when-light.svg">
</picture>

To reap a session is to delete it (`zellij delete-session NAME --force`): it is gone with its scrollback and cannot be
resurrected. [How it decides](docs/how-it-decides.md) lists every check in order.

## Why zellij-reaper

- **It knows who is attached.** It reads the kernel's socket table, which agrees with `zellij action list-clients` on
  every tested zellij, and asks zellij again right before each delete.
- **It knows what is running.** A pane counts as idle only when its process is a shell at its prompt with no child.
  A command pane, `zellij run` and a job in the background all count as running.
- **It finds renamed sessions.** zellij keeps a server's first name after a rename, so scripts that match servers by
  name lose them. The socket file's inode leads to the right server.
- **It says why.** The dashboard shows every check with its evidence, and each decision goes to a log whose words stay
  the same across releases.
- **It is light.** A look at ten sessions takes about 25 ms and starts no process per session. No root, no network.

## Install

Linux or WSL 2, with zellij 0.43 to 0.45 and a systemd user instance (`systemctl --user` works):

```sh
curl -fsSL https://github.com/kanggu12/zellij-reaper/releases/latest/download/install.sh | sh
```

The script checks the download against the release's checksums, puts `zellij-reaper` in `~/.local/bin` and starts the
timer, with lingering so it also runs after you log out. The first pass runs ten minutes later. Look before it does.
The sample below comes from a machine set to `idle_after = "1h"`:

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

`zellij-reaper protect NAME` keeps a session out of every pass. To start with passes that only log, run `zellij-reaper
config edit` and set `dry_run = true`. Packages (`.deb`, `.rpm`, `.apk`, Arch), release archives and `go install` are
in [setup](docs/setup.md), with upgrading, uninstalling and WSL 2.

## Try it in a sandbox

```sh
docker run --rm -it --init ghcr.io/kanggu12/zellij-reaper-demo
```

The dashboard opens on twelve sessions in a throwaway container, one of every kind above. Reap, protect, rename and
run passes there as you like; your own sessions are not touched. Staging the sessions takes about a minute, and
`--init` lets ctrl+c out of it. Append `--shell` to the command for a shell in the same sandbox; put
`-e ZELLIJ_REAPER_LANG=ko` before the image name for Korean.

From a clone, `make demo` stages the same sessions from the working tree (it needs Go and Docker), and
`make demo DEMO_SHELL=1` opens its shell.

## Names that say what a session is

zellij names a session `brave-peach`. With `auto_rename = true`, every pass names the sessions it keeps after what they
show, so the list of sessions reads like a list of what you were doing:

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/names-dark.svg">
  <img alt="Examples of session names. A session zellij named brave-peach that shows Fix the login bug becomes fix-the-login-bug. tail -f /var/log/app.log becomes tail-app.log. A shell titled me@laptop: ~/projects/api becomes api. Claude Code working in web-app becomes web-app-claude-code. A shell on another machine, me@prod-db:~, becomes prod-db. Pane #1 in ~/notes becomes notes." src="docs/assets/names-light.svg">
</picture>

A name stays until what the session shows changes. An attached session is never renamed, and a session you protect
keeps its name. [Setup](docs/setup.md#naming-sessions-after-what-they-show) has the rules. In the dashboard, `r` offers
the same name for any session, with `auto_rename` on or off.

## The dashboard

`zellij-reaper` opens it. Sessions are sorted by what the next pass does to them; the panel shows the checks for the
selected one with their evidence, its panes and the settings that apply.

| Key | Does | Key | Does |
|:---:|:---|:---:|:---|
| <kbd>↑</kbd> <kbd>↓</kbd> | select a session | <kbd>f</kbd> | run a pass now (asks first when it would reap) |
| <kbd>⏎</kbd> | attach (inside zellij: switch to it) | <kbd>F</kbd> | run a pass without the idle and exited waits, after asking |
| <kbd>d</kbd> | reap it now, after asking | <kbd>l</kbd> | the decision log |
| <kbd>p</kbd> | protect it, or lift the protection | <kbd>/</kbd> | filter by name |
| <kbd>r</kbd> | rename it | <kbd>?</kbd> | every key and what each color means |

It speaks English, and Korean when the locale is Korean (`ZELLIJ_REAPER_LANG=en` or `ko` picks one).

## Commands

| Command | Does |
|:---|:---|
| `zellij-reaper`, `tui` | the dashboard; the plan as a table when the output is not a terminal |
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
idle_after = "4h"            # reap idle sessions after 4 hours (default 12h)
exited_after = "1d"          # and exited ones after a day (default 3d)
protect = "^(keep-|work-)"   # names that are never reaped or renamed
auto_rename = true           # name the sessions it keeps after what they show
```

`zellij-reaper config show` prints the values in effect and where each one comes from. [Setup](docs/setup.md#settings)
lists every key.

## The log

Every pass writes one line per session, in words that stay the same across releases:

```
$ zellij-reaper logs
11-05 02:49:17  === reaper start (dry_run=false idle_after=1h exited_after=3d) ===
11-05 02:49:17  old-experiment [EXITED] panes={Pane #1|sleep 5000} :: REAP EXITED, 5d since last activity
11-05 02:49:17    -> deleted: old-experiment
11-05 02:49:17  k8s-debug [EXITED] panes={Pane #1|sleep 5000} :: SKIP EXITED 1d ago, reaped after 3d
11-05 02:49:17  api-server [RUNNING] panes={Pane #1|npm run dev} :: SKIP running in a pane: npm run dev
11-05 02:49:17    -> renamed: api-server → npm-run-dev
11-05 02:49:17  notes [RUNNING] panes={Pane #1} :: SKIP client attached (1 client(s) attached)
11-05 02:49:17  agent-refactor [RUNNING] panes={Pane #1|claude} :: SKIP protected command running (claude)
11-05 02:49:17    -> renamed: agent-refactor → claude
11-05 02:49:17  db-shell [RUNNING] panes={Pane #1|psql app_dev} :: SKIP running in a pane: psql app_dev
11-05 02:49:17    -> renamed: db-shell → psql-app_dev
11-05 02:49:17  monitoring [RUNNING] panes={Pane #1|watch df -h} :: SKIP running in a pane: watch df -h
11-05 02:49:17    -> renamed: monitoring → watch-df
11-05 02:49:17  logs [RUNNING] panes={Pane #1|tail -f app.log} :: SKIP running in a pane: tail -f app.log
11-05 02:49:17    -> renamed: logs → tail-app.log
11-05 02:49:17  scratch [RUNNING] panes={Pane #1} :: REAP IDLE, 3h since last activity
11-05 02:49:17    -> deleted: scratch
11-05 02:49:17  docs-review [RUNNING] panes={Pane #1} :: SKIP IDLE for 25m, reaped after 1h
11-05 02:49:17  release-v2 [RUNNING] panes={Pane #1} :: SKIP IDLE for 50m, reaped after 1h
11-05 02:49:17  dotfiles [RUNNING] panes={Pane #1} :: SKIP protected by protected list
11-05 02:49:17  === reaper end (2 reaped, 10 kept, 5 renamed) ===
```

It lives in `~/.local/state/zellij-reaper/` and moves to `reaper.log.1` past `log.max_bytes` (1 MiB).

## How it compares

| | zellij-reaper | `zellij delete-all-sessions` | `zellij kill-all-sessions` |
|:---|:---:|:---:|:---:|
| Exited sessions | deleted after a wait you set | deleted, all at once | left |
| Running sessions | deleted when idle, after a wait you set | with `--force`, all of them | stopped, all of them |
| A session with a client attached | kept | with `--force`, deleted | stopped |
| A session running a program | kept | with `--force`, deleted | stopped |
| Runs on its own | yes, from a user timer | no | no |
| Shows what it would do first | `plan`, the dashboard | no | no |

## Questions

**Will it delete the session I am in?** No. A session with a client attached is kept, and the pass asks zellij again
just before each delete: a client that attached in between keeps the session.

**My coding agent waits at its prompt for hours. Is its session safe?** Yes, while the agent runs; once it exits, the
normal wait applies. The programs in `protect_commands` keep their session even when the pane looks idle, and they are
found under any name they run as (`node .../claude-code/cli.js` counts as `claude`).

**Can I get a deleted session back?** No: zellij removes its resurrection data too. To watch first, set
`dry_run = true`; every pass then only writes what it would do to the log.

**What does it touch?** It runs as you, without root, and makes no network connection:

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/touches-dark.svg">
  <img alt="zellij-reaper reads the kernel's process and socket tables, zellij's list and session files, and your settings. It changes sessions only through zellij's own commands, and otherwise writes only its log, its lock and protected list, and its systemd units when you run install." src="docs/assets/touches-light.svg">
</picture>

**I used the earlier shell scripts.** `zellij-reaper install` moves them over: their settings, their log and their
dry-run setting. See [setup](docs/setup.md#coming-from-the-shell-scripts).

## Documentation

- [How it decides](docs/how-it-decides.md): a pass step by step, every check, the log's words.
- [Setup](docs/setup.md): installing, every setting, protecting and naming sessions, zellij versions.
- [Commands](docs/commands.md): every command and flag, the dashboard's keys and colors.
- [Troubleshooting](docs/troubleshooting.md): what `doctor` says, sessions kept longer than you expect, WSL 2.
- [Internals](docs/internals.md): the layers, how the facts are read, the design decisions, the tests.
- [Changelog](CHANGELOG.md): what changed in each release.

## Contributing

Bug reports with `zellij-reaper plan --json` and the log lines for the session help most.
[CONTRIBUTING.md](.github/CONTRIBUTING.md) covers how the code fits together, the tests (fake machines for the rules,
real zellij in containers) and what a change needs.

```sh
git clone https://github.com/kanggu12/zellij-reaper && cd zellij-reaper
make check        # gofmt, go vet, staticcheck, shellcheck, the unit tests
make integration  # real zellij releases in throwaway containers
```

## License

MIT ([LICENSE](LICENSE)). The libraries zellij-reaper is built from and the font its logo and figures are drawn from
are listed with their licenses in [THIRD_PARTY_NOTICES.md](docs/THIRD_PARTY_NOTICES.md).
