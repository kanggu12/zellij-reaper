# Setting up zellij-reaper

1. [Install](#install)
2. [Coming from the shell scripts](#coming-from-the-shell-scripts)
3. [Upgrade and uninstall](#upgrade-and-uninstall)
4. [Settings](#settings)
5. [Protecting sessions](#protecting-sessions)
6. [Naming sessions after what they show](#naming-sessions-after-what-they-show)
7. [Files](#files)
8. [zellij versions](#zellij-versions)
9. [WSL 2](#wsl-2)

## Install

zellij-reaper is one static binary for Linux on x86_64 or arm64. It needs zellij and a systemd user instance for its
timer.

```sh
curl -fsSL https://raw.githubusercontent.com/kanggu12/zellij-reaper/main/install.sh | sh
```

To see the dashboard before installing anything, `docker run --rm -it --init ghcr.io/kanggu12/zellij-reaper-demo`
opens it on demo sessions in a throwaway container.

The script needs `curl`, `tar` and `sha256sum` (or `shasum`). It downloads the latest release for your architecture,
checks it against the release's `checksums.txt`, installs it to `~/.local/bin` and runs `zellij-reaper install`.
These variables change what it does:

| Variable | Default | Effect |
|:---|:---:|:---|
| `ZELLIJ_REAPER_VERSION` | the latest release | install a given release, like `v0.1.1` |
| `ZELLIJ_REAPER_BIN_DIR` | `~/.local/bin` | where the binary goes |
| `ZELLIJ_REAPER_NO_TIMER` | | `1` installs the binary only |
| `ZELLIJ_REAPER_BASE_URL` | GitHub releases | download from a mirror |

Other ways:

| From | Then |
|:---|:---|
| a package (`.deb`, `.rpm`, `.apk`, Arch) from the [releases page](https://github.com/kanggu12/zellij-reaper/releases) | the binary is in `/usr/bin`; each user who wants a timer runs `zellij-reaper install` |
| a release archive, `zellij-reaper_<version>_linux_<arch>.tar.gz` | put the binary on your `PATH`, run `zellij-reaper install` |
| source: `go install github.com/kanggu12/zellij-reaper/cmd/zellij-reaper@latest` | `zellij-reaper install` |
| a clone: `make install` | builds, copies to `~/.local/bin` and installs the timer |

### What `zellij-reaper install` does

- Writes `zellij-reaper.service` and `zellij-reaper.timer` to `~/.config/systemd/user/`. The service runs this binary
  (its absolute path at install time) with `run --quiet`, with zellij's directory first in its `PATH`.
- Writes `~/.config/zellij-reaper/config.toml` if there is none, every key commented out at its default.
- Enables the timer and turns on lingering (`loginctl enable-linger`), so the timer runs without a login.
- Says when the first pass runs and how many sessions it would delete. The first pass waits ten minutes after the timer
  starts; `zellij-reaper plan` shows what it will do, and `zellij-reaper protect NAME` keeps a session out of it.

Run it again after moving the binary or changing `interval`. It never overwrites a settings file. With
`--config FILE` (or `ZELLIJ_REAPER_CONFIG`), the timer is set up to read that file too.

## Coming from the shell scripts

The earlier version was two shell scripts, `~/.local/bin/zellij-reaper.sh` and `~/.local/bin/zellij-reap`, set up
with `Environment=` lines in the same service file. `zellij-reaper install` finds them and moves them over:

| The shell version | Becomes |
|:---|:---|
| `DRY_RUN` (its default: on) | `dry_run` |
| `MAX_AGE_HOURS` or `MAX_AGE_DAYS` (its default: 3 days) | `idle_after` and `exited_after` |
| `PROTECT_REGEX` | `protect` |
| `AUTO_RENAME` (its default: on) | `auto_rename` |
| `LOG_MAX_BYTES` | `log.max_bytes` |
| `LOG` | `log.path` |
| `~/.cache/zellij-reaper.log` and `.log.1` | moved to `~/.local/state/zellij-reaper/reaper.log` and `.1` |
| the two scripts and `~/.cache/zellij-reaper.lock` | removed |

The settings file is written only when there is none; if one exists, `install` keeps it and says the shell version's
settings were not moved. The scripts are removed only when they are recognizably the shell version's. The shell
version ran as a dry run unless its service set `DRY_RUN=0`, and the moved-over settings keep that: check `dry_run`
with `zellij-reaper config show`.

## Upgrade and uninstall

**Upgrade** by running the install script again, or with your package manager. The settings stay; `zellij-reaper
install` rewrites the units. After moving the binary by hand, run `zellij-reaper install` again: `doctor` notices a
timer that still runs an old path.

**Uninstall** with `zellij-reaper uninstall`, which stops and removes the timer and the service. `--purge` also removes
the settings, the log and the protected list. Then remove the binary (`rm ~/.local/bin/zellij-reaper`) or the
package. Run `uninstall` before removing a package, or the timer stays and fails every hour.

## Settings

The file is `~/.config/zellij-reaper/config.toml` (`$XDG_CONFIG_HOME/zellij-reaper/config.toml`, or the file named by
`ZELLIJ_REAPER_CONFIG` or `--config`). Every key is optional. Durations take `s`, `m`, `h`, `d` and `w`, combined as
in `1d12h`; `"0"` means no wait.

| Key | Default | Variable | Meaning |
|:---|:---:|:---:|:---|
| `dry_run` | `false` | `ZELLIJ_REAPER_DRY_RUN` | log decisions, delete and rename nothing |
| `idle_after` | `"12h"` | `ZELLIJ_REAPER_IDLE_AFTER` | how long a running session must be idle |
| `exited_after` | `"3d"` | `ZELLIJ_REAPER_EXITED_AFTER` | how long an exited session is kept for resurrection |
| `protect` | `""` | `ZELLIJ_REAPER_PROTECT` | a regular expression ([Go syntax](https://pkg.go.dev/regexp/syntax)); matching names are never reaped or renamed |
| `protect_commands` | `["claude", "codex", "gemini", "aider", "opencode"]` | | programs that keep a running session |
| `shells` | `[]` | | extra programs that count as a prompt |
| `auto_rename` | `false` | `ZELLIJ_REAPER_AUTO_RENAME` | rename survivors after what they show |
| `interval` | `"1h"` | | how often the timer runs; run `install` again after changing it |
| `zellij` | `""` | `ZELLIJ_REAPER_ZELLIJ` | the zellij binary; empty means the first one in `PATH` |
| `log.path` | `~/.local/state/zellij-reaper/reaper.log` | `ZELLIJ_REAPER_LOG` | the decision log |
| `log.max_bytes` | `1048576` | `ZELLIJ_REAPER_LOG_MAX_BYTES` | the size at which the log moves to `.1`; `0` never |

A variable wins over the file, and `run --dry-run` and `run --force` win over both. Variables set in your shell apply
to the commands you run there, not to the timer: `install` and `doctor` warn about each one, so put lasting settings
in the file. A value that cannot be read (a duration without a unit, a pattern that does not compile) stops the
command with an error instead of falling back to a default. An unknown key is reported by `config show` and `doctor`.

`zellij-reaper config show` prints each setting and where its value comes from:

```
# ~/.config/zellij-reaper/config.toml
dry_run           = false                                               # default
idle_after        = "4h"                                                # file
exited_after      = "1d"                                                # file
protect_commands  = ["claude", "codex", "gemini", "aider", "opencode"]  # default
...
```

## Protecting sessions

Three ways, and each one also stops renaming:

- **A name pattern** in the settings: `protect = "^(keep-|work-)"`.
- **One session at a time:** `zellij-reaper protect NAME`, or `p` in the dashboard. `protect` with no name lists them;
  `unprotect NAME` removes one. A session renamed from the dashboard keeps its place on the list.
- **A running program:** while a program from `protect_commands` runs anywhere in a running session, or its layout
  starts one, the session is kept, even when the pane running it looks idle.

## Naming sessions after what they show

With `auto_rename = true`, every pass renames the sessions it keeps (except attached, protected and uncertain ones)
after their first terminal pane's title, or the directory they were started in, plus when they were last used:
`vim notes.md` last used on Nov 4 at 09:31 becomes `vim-notes-md_1104-0931`. The stamp follows the last use, so a name
changes only when the session was used.

- Spinners and status marks that programs put in titles (dingbats, braille) are dropped. Letters of every script stay,
  so two Korean titles make two names.
- Titles that say nothing (`Pane #1`, `Tab #2`, a bare `bash`) are skipped.
- A name is cut so the session's socket path stays within Linux's 107-byte limit, with room for the stamp and a `-2`,
  `-3` when the name is taken.

Names change as sessions are used, so a script that attaches to a session by name stops finding it once it is renamed.

## Files

| Path | What |
|:---|:---|
| `~/.config/zellij-reaper/config.toml` | the settings |
| `~/.config/systemd/user/zellij-reaper.{service,timer}` | the timer, written by `install` |
| `~/.local/state/zellij-reaper/reaper.log` and `.1` | the decision log |
| `~/.local/state/zellij-reaper/protected` | the protected list, one name per line |
| `~/.local/state/zellij-reaper/protected.lock` | held while the protected list is changed |
| `~/.local/state/zellij-reaper/lock` | the pass lock |

`$XDG_CONFIG_HOME` and `$XDG_STATE_HOME` move them. zellij-reaper reads zellij's files under `$XDG_CACHE_HOME/zellij`
(`~/.cache/zellij`) and never writes there, except `recover` clearing a broken session.

## zellij versions

CI runs real sessions on zellij 0.43.1, 0.44.3 and 0.45.1. The differences it handles:

| | 0.43 | 0.44, 0.45 |
|:---|:---|:---|
| Data and socket directories | named after the version: `~/.cache/zellij/0.43.1/` | `contract_version_1` |
| A session made with `attach --create-background` | the first `list-clients` counts the client that made it, so the first pass keeps it | counts none |
| The client count in the metadata file | can keep counting that client until something happens | rewritten about once a minute |

The version comes from `zellij --version`. Older releases may work; they are not tested.

## WSL 2

The user instance of systemd needs `systemd=true` under `[boot]` in `/etc/wsl.conf`, then `wsl --shutdown` from
Windows. WSL stops its VM a while after the last terminal closes, and the timer with it. Missed passes are not
replayed: when the VM starts again, so does the timer, and its first pass runs ten minutes later.
`zellij-reaper doctor` checks the whole setup.
