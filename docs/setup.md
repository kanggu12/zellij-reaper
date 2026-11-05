# Setting up zellij-reaper

1. [Install](#install)
2. [Coming from the shell scripts](#coming-from-the-shell-scripts)
3. [Without internet access](#without-internet-access)
4. [Upgrade and uninstall](#upgrade-and-uninstall)
5. [Settings](#settings)
6. [Protecting sessions](#protecting-sessions)
7. [Naming sessions after what they show](#naming-sessions-after-what-they-show)
8. [Files](#files)
9. [zellij versions](#zellij-versions)
10. [WSL 2](#wsl-2)

## Install

zellij-reaper is one static binary for Linux on x86_64 or arm64. It needs zellij and a systemd user instance for its
timer. Without systemd, `ZELLIJ_REAPER_NO_TIMER=1` installs the binary only, and `zellij-reaper run` can go in cron.

```sh
curl -fsSL https://github.com/kanggu12/zellij-reaper/releases/latest/download/install.sh | sh
```

To see the dashboard before installing anything, `docker run --rm -it --init ghcr.io/kanggu12/zellij-reaper-demo`
opens it on demo sessions in a throwaway container.

The script needs `curl`, `tar` and `sha256sum` (or `shasum`). It downloads the latest release for your architecture,
checks it against the release's `checksums.txt`, installs it to `~/.local/bin` and runs `zellij-reaper install`.
These variables change what it does, set on `sh` (`curl -fsSL URL | ZELLIJ_REAPER_BIN_DIR=~/bin sh`):

| Variable | Default | Effect |
|:---|:---:|:---|
| `ZELLIJ_REAPER_VERSION` | the latest release | install a given release, like `v0.3.0` |
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

## Without internet access

zellij-reaper needs no network to run: it makes no connection, and its integration tests run in a container with none.
Only the install script downloads anything, so on a closed network, skip it. On a machine with internet access, fetch the
archive for the target's architecture and the checksums, and carry both across:

```sh
base=https://github.com/kanggu12/zellij-reaper/releases/latest/download
version=$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/kanggu12/zellij-reaper/releases/latest | sed 's|.*/v||')
curl -fsSLO "$base/zellij-reaper_${version}_linux_amd64.tar.gz"
curl -fsSLO "$base/checksums.txt"
```

Then on the closed machine:

```sh
grep " zellij-reaper_${version}_linux_amd64.tar.gz\$" checksums.txt | sha256sum -c -
tar -xzf "zellij-reaper_${version}_linux_amd64.tar.gz" zellij-reaper
install -Dm0755 zellij-reaper ~/.local/bin/zellij-reaper
~/.local/bin/zellij-reaper install
```

The rest works the same way:

- **A package** instead of the archive: copy the `.deb`, `.rpm`, `.apk` or `.pkg.tar.zst`, install it with `dpkg -i`, `rpm -i`,
  `apk add --allow-untrusted` or `pacman -U`, then run `zellij-reaper install` as each user who wants the timer.
- **Many machines:** put the release's files on an internal web server as `<base>/v<version>/<file>`, and the install script
  works from there: `ZELLIJ_REAPER_BASE_URL=<base> ZELLIJ_REAPER_VERSION=v<version> sh install.sh`. It needs the version,
  because finding the latest one takes GitHub.
- **zellij itself** is one binary too, from [its releases](https://github.com/zellij-org/zellij/releases):
  `zellij-<machine>-unknown-linux-musl.tar.gz`.
- **The demo image:** `docker save ghcr.io/kanggu12/zellij-reaper-demo | gzip >demo.tar.gz` where there is internet, and
  `docker load <demo.tar.gz` where there is not. It runs with `--network none`.
- **From source**, with Go already on the closed machine: run `go mod vendor` in a clone where there is internet and carry
  the folder across, then `GOFLAGS=-mod=vendor GOTOOLCHAIN=local make build`.

A closed network also keeps you from checking for new releases: the steps above are how to upgrade as well.

## Upgrade and uninstall

**Upgrade** by running the install script again, or with your package manager. The settings stay; `zellij-reaper
install` rewrites the units. After moving the binary by hand, run `zellij-reaper install` again: `doctor` notices a
timer that still runs an old path.

**Uninstall** with `zellij-reaper uninstall`, which stops and removes the timer and the service. `--purge` also removes
the settings, the log and the protected list. Lingering stays on; `loginctl disable-linger` turns it off. Then remove the
binary (`rm ~/.local/bin/zellij-reaper`) or the package. Run `uninstall` before removing a package, or the timer stays and fails every hour.

## Settings

The file is `~/.config/zellij-reaper/config.toml` (`$XDG_CONFIG_HOME/zellij-reaper/config.toml`, or the file named by
`ZELLIJ_REAPER_CONFIG` or `--config`). Every key is optional. Durations take `s`, `m`, `h`, `d` and `w`, combined as
in `1d12h`; `"0"` means no wait.

| Key | Default | Meaning |
|:---|:---:|:---|
| `dry_run` | `false` | log decisions, delete and rename nothing |
| `idle_after` | `"12h"` | how long a running session must be idle |
| `exited_after` | `"3d"` | how long an exited session is kept for resurrection |
| `protect` | `""` | a regular expression ([Go syntax](https://pkg.go.dev/regexp/syntax)); matching names are never reaped or renamed |
| `protect_commands` | `["claude", "codex", "gemini", "aider", "opencode"]` | programs that keep a running session |
| `shells` | `[]` | extra programs that count as a prompt |
| `auto_rename` | `false` | name the sessions nobody named after what they show |
| `auto_rename_stamp` | `"none"` | what those names end in: `"none"`, `"date"` or `"time"` |
| `interval` | `"1h"` | how often the timer runs, up to five minutes late; at least `1m`; run `install` again after changing it |
| `zellij` | `""` | the zellij binary; empty means the first one in `PATH` |
| `log.path` | `~/.local/state/zellij-reaper/reaper.log` | the decision log |
| `log.max_bytes` | `1048576` | the size at which the log moves to `.1`; `0` never |

Most keys also take a variable: `ZELLIJ_REAPER_` and the key in capitals (`ZELLIJ_REAPER_IDLE_AFTER`), except
`ZELLIJ_REAPER_LOG` for `log.path` and `ZELLIJ_REAPER_LOG_MAX_BYTES` for `log.max_bytes`. `protect_commands`, `shells` and
`interval` are set in the file only.

A variable wins over the file, and `run --dry-run` and `run --force` win over both. Variables set in your shell apply
to the commands you run there, not to the timer: `install` and `doctor` warn about each one, so put lasting settings
in the file. A value that cannot be read (a duration without a unit, a pattern that does not compile) stops the
command with an error instead of falling back to a default, and so does a key nothing reads: a typo in `protect` must not
leave a session unprotected. A file with a `[log]` table, as earlier versions wrote it, takes new lines above that table.

`zellij-reaper config show` prints each setting and where its value comes from:

```
# ~/.config/zellij-reaper/config.toml
dry_run           = false                                               # default
idle_after        = "4h"                                                # file
exited_after      = "1d"                                                # file
protect           = ""                                                  # default
protect_commands  = ["claude", "codex", "gemini", "aider", "opencode"]  # default
...
```

## Protecting sessions

Three ways. The first two also stop renaming; a session kept only for a running program is still renamed while no client
is attached:

- **A name pattern** in the settings: `protect = "^(keep-|work-)"`.
- **One session at a time:** `zellij-reaper protect NAME`, or `p` in the dashboard. `protect` with no name lists them;
  `unprotect NAME` removes one. A session renamed from the dashboard keeps its place on the list.
- **A running program:** while a program from `protect_commands` runs anywhere in a running session, or its layout
  starts one, the session is kept, even when the pane running it looks idle.

## Naming sessions after what they show

With `auto_rename = true`, every pass names the sessions it keeps after what they show, if nobody chose their name: a name
zellij made up (`brave-peach`), or one zellij-reaper gave earlier. A name you gave a session stays, and so does a session
that is attached, exited, protected by name or uncertain. Press `r` in the dashboard for the suggested name by hand, with
`auto_rename` on or off.

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/names-dark.svg">
  <img alt="Examples of session names. A session zellij named brave-peach that shows Fix the login bug becomes fix-the-login-bug. tail -f /var/log/app.log running in ~/web-app becomes web-app-tail-app.log. A shell titled me@laptop: ~/projects/api becomes api. Claude Code working in web-app becomes web-app-claude-code. A shell on another machine, me@prod-db:~, becomes prod-db. Pane #1 in ~/notes becomes notes." src="assets/names-light.svg">
</picture>

The name comes from the first of these that says something:

1. **A pane's title**, the focused pane first, then the other panes still running. With nobody attached zellij marks no
   tab active, so that is the first tab's focused pane.
2. **The directory and the program**, when the titles only name a program (`Claude Code`, `nvim`) or say nothing:
   `web-app-claude-code`, `api-npm-run-dev`. The directory is where the first pane works now, so it follows a `cd`. From
   a command line it takes only the plain words up to the first flag, since arguments can hold a password.
3. **The directory alone**, for a shell with nothing running.

Your home directory and `/` say nothing, so a shell at home with a title like `Pane #1` keeps its name.

These parts of a title are dropped or shortened:

| In the title | Becomes | Example |
|:---|:---|:---|
| Spinners, stars, emoji | nothing | `✳ Fix the login bug` is `fix-the-login-bug` |
| A command's flags | nothing | `docker compose up -d --build` is `docker-compose-up` |
| A path | its last part | `vim ~/notes/today.md` is `vim-today.md` |
| A URL | no scheme, no password | `https://github.com/foo/bar` is `github.com-foo-bar` |
| An ssh target | no user | `ssh me@prod-db` is `ssh-prod-db` |
| A shell's own title | its directory, and the machine if it is another one | `me@laptop: ~/api` is `api`, `me@buildbox:/srv/app$` is `buildbox-app` |
| `Pane #1`, `Tab #2`, a bare `bash` | nothing | |

These rows show the title alone. A title that repeats what the pane runs falls under rule 2: `tail -f /var/log/app.log`
run in `~/web-app` gives `web-app-tail-app.log`.

Letters of every script stay, so two Korean titles make two names. A `.` stays inside a word, so `notes.md` still reads as
a file. Names stop at 32 columns at the end of a word, and within what the session's socket path leaves of Linux's
107-byte limit.

A name changes only when what the session shows changes, so a script that attaches by name keeps working until then.
When a name is taken, the session gets a `-2`, `-3`, and keeps that tail. zellij refuses a name a session has had before,
so a title that comes back gives a tail too.

`auto_rename_stamp` adds when a session was last used, in local time: `"date"` gives `web-app_1104`, which renames a
session in use once more a day, and `"time"` gives `web-app_1104-0931`, which renames it on every pass.

## Files

| Path | What |
|:---|:---|
| `~/.config/zellij-reaper/config.toml` | the settings |
| `~/.config/systemd/user/zellij-reaper.{service,timer}` | the timer, written by `install` |
| `~/.local/state/zellij-reaper/reaper.log` and `.1` | the decision log |
| `~/.local/state/zellij-reaper/protected` | the protected list, one name per line |
| `~/.local/state/zellij-reaper/protected.lock` | held while the protected list is changed |
| `~/.local/state/zellij-reaper/named`, `named.lock` | the names `auto_rename` gave, so it knows them from a name someone chose |
| `~/.local/state/zellij-reaper/lock` | the pass lock |

`$XDG_CONFIG_HOME` and `$XDG_STATE_HOME` move them. zellij-reaper reads zellij's files under `$XDG_CACHE_HOME/zellij`
(`~/.cache/zellij`) and never writes there, except `recover` clearing a broken session.

## zellij versions

CI runs real sessions on zellij 0.43.1, 0.44.3 and 0.45.1. The differences it handles:

| | 0.43 | 0.44, 0.45 |
|:---|:---|:---|
| Data and socket directories | named after the version: `~/.cache/zellij/0.43.1/` | `contract_version_1` |
| A session made with `attach --create-background` | the first `list-clients` names the client that made it, so it is asked twice | counts none |
| The client count in the metadata file | can keep counting that client until something happens | rewritten about once a minute |
| Renaming to a name that is taken | refused | accepted, cutting the other session off, so zellij-reaper looks first |

zellij 0.44.2 prints nothing when `list-clients` calls overlap, so zellij-reaper does not overlap them. The version comes
from `zellij --version`. Older releases may work; they are not tested.

## WSL 2

The user instance of systemd needs `systemd=true` under `[boot]` in `/etc/wsl.conf`, then `wsl --shutdown` from
Windows. WSL stops its VM a while after the last terminal closes, and the timer with it. Missed passes are not
replayed: when the VM starts again, so does the timer, and its first pass runs ten minutes later.
`zellij-reaper doctor` checks the whole setup.
