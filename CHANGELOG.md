# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
versions follow [semantic versioning](https://semver.org/). Until 1.0, every release raises the last number, whether it
adds features or fixes them: 0.6.0 is followed by 0.6.1, 0.6.2 and so on.

## [Unreleased]

## [0.6.1] - 2026-11-07

### Fixed

- `install` on Windows failed with `register the tasks: exit status 1` when zellij was not on the `PATH`: PowerShell
  exits 1 when its last command failed, even one whose error was silenced on purpose, and the last command removes a
  names task that is usually not there. Every script zellij-reaper hands PowerShell now ends in `exit 0`; an error
  that matters still stops it before that.
- The release workflow runs `installers.yml` once the release is out: a release made by a workflow starts no other
  workflow, so it never ran for 0.6.0 on its own, which is how this went out.

## [0.6.0] - 2026-11-07

### Added

- macOS. The process table comes from sysctl and lsof, and two launch agents run the passes. A renamed session's
  socket file is matched to its server by when it was made, which a rename keeps.
- Windows. The process table comes from Toolhelp, and one scheduled task runs the passes through
  `conhost.exe --headless`, so no window flashes. A renamed session's socket file holds its server's process ID. The
  settings live in `%APPDATA%\zellij-reaper`, the log and the lists in `%LOCALAPPDATA%\zellij-reaper`. Releases carry
  a zip and `install.ps1`.
- zellij on Windows cannot reach a session after a rename: its named pipe keeps the first name, and every command for
  the new name waits forever (measured on 0.45.1). zellij-reaper renames nothing there, and `r` in the dashboard and
  `doctor` say why; a session renamed by hand is kept without asking zellij.
- Where no terminal can be read, as on Windows, a running session is idle from the later of the first pass that found
  it quiet, kept in the state directory, and the last time a client left it, which zellij's session files record:
  the files alone do not change when a pane prints, so a job that just ended would not count.
- The platforms workflow runs the unit tests and real sessions on macOS and Windows runners; installers.yml installs
  each release with `install.sh` and `install.ps1`.

### Changed

- The `systemd` package is now `timers`, behind one interface for the three systems.
- A name keeps room only for the stamp in use, and a name cut short ends at a word: under macOS's long socket
  directory, `plan-the-launch` used to become `plan-the-la`.
- When the timer does not say when it runs next (launchd), the header shows its interval. `config edit` falls back on
  Notepad on Windows.
- A server that started less than a second ago is not asked for its clients, which zellij issue 5632 could stop; its
  session is kept for that pass.
- `cmd` and `powershell` count as shells on Windows only: elsewhere a program called `cmd` is more likely a build of
  someone's `./cmd`.

## [0.5.0] - 2026-11-07

### Added

- A names timer: `zellij-reaper-names.timer` runs `run --names-only` every `rename_interval` (5 minutes, `"0"` turns
  it off), so a name follows its session within minutes instead of at the hourly pass. A names pass renames what the
  hourly pass would, deletes nothing, and writes to the log only when it renames. `install` writes it, or removes it with
  `rename_interval = "0"`, `uninstall` removes it, and `doctor` says when it is not running.
- `zellij-reaper here` prints the current name of the shell's session, from `/proc`, the kernel's socket table
  and the socket directory, in about a millisecond, without starting zellij. `shell-hook bash|zsh|fish`
  prints a prompt hook that keeps `ZELLIJ_SESSION_NAME` at that name, so zellij commands in a pane opened before a
  rename keep working; it keeps the status of the command before.
- `keep-name NAME` and `forget-name NAME` keep a session's name, or let passes rename it again, from a script, as `r`,
  Tab and Enter do in the dashboard; `keep-name` alone lists the kept names.
- `auto_rename_drop_tail = true` gives a session back the name without its `-2` once no other session has it.
- The session list in the dashboard and in `plan` shows the name the next pass gives a session (`→ fix-the-login-bug`).
- Fuzz tests for the names and for zellij's output (`make fuzz`, 10 seconds each in CI), integration scenarios for all
  of the above and for Enter in the dashboard, zellij 0.45.1 on arm64 in CI, and a weekly canary on zellij's newest
  release and its main branch.

### Changed

- A pass reads less of `/proc`, only what zellij's processes and the ones under them need, and reads it while zellij
  lists the sessions: `plan` on a machine with 166 processes went from 40.2 ms to 31.0 ms.
- On zellij 0.43, which has no `switch-session`, Enter inside zellij says to detach and attach instead of showing
  zellij's usage error.
- The demo has a thirteenth session, `brave-peach`, and `auto_rename` on, so it shows a rename coming. The README opens
  with a Quick start and a recording of the dashboard.

### Fixed

- A pass or the dashboard could stop a session that was starting: zellij 0.44 and 0.45 die when a connection to a new
  server closes in its first tenth of a second (zellij issue 5632), and listing the sessions makes one. zellij-reaper
  now waits until every server is a second old before it lists them. Measured on 0.45.1, a `plan` run at that moment
  stopped 12 of 25 new sessions before, none after.
- On a narrow terminal the dashboard keeps the verdict and drops the client count and the state, and keeps attach,
  rename and protect in the key bar before the arrows and the run-a-pass key.
- A line of `list-sessions` output with a blank name no longer becomes a session with no name, and a `-N` tail is added only to the
  name it belongs to (both found by fuzzing).

## [0.4.0] - 2026-11-06

### Changed

- `auto_rename` renames sessions with a client attached too, as you work in them. It renamed only sessions nobody was
  attached to, so on a machine whose sessions stay attached it never renamed one. `auto_rename_attached = false` brings
  that back. Panes opened before a rename keep the old name in `ZELLIJ_SESSION_NAME`, and a `zellij` command run there
  without `--session` waits for a session by that name while another one is running;
  `export ZELLIJ_SESSION_NAME=NEW-NAME` in such a pane fixes it.
- `config show` lines its values up after the longest key.

### Fixed

- Inside zellij, Enter in the dashboard switches to the selected session even when it, or the session the dashboard
  runs in, has been renamed since. `zellij action switch-session` went by the old names: it waited for a session by
  the old name, or made a new, empty session under it. The switch also gives up after ten seconds, and the screen no
  longer stops while it looks.
- The dashboard keeps its selection on a session a pass renamed. The selection used to move to whatever took its row,
  where `p` could protect, or stop protecting, another session. `p` also says so when no session has the name any
  more.

## [0.3.5] - 2026-11-06

### Added

- In the dashboard's rename field, Tab swaps the suggested name with the name the session has, and the keys under the
  field say what Enter will do: keeping a running session's name takes `r`, Tab and Enter, with no typing. Before, the
  field held only the suggested name, and keeping the current one meant typing it over. On a narrow screen the keys
  under the field drop to what fits on one line.
- The recent decisions show a name kept from the dashboard.

### Changed

- The `uninstall --purge` help names the lists the way the documents do.

## [0.3.4] - 2026-11-06

### Fixed

- 0.3.2 and 0.3.3 could replace a name someone chose. For a name on no list, they read the decision log, but a log
  line says which name a pass gave, not to which session, and zellij gives a name out again once its session is gone.
  So two kinds of chosen names read as given by a pass: a name kept from the dashboard in 0.3.1, and a name set with
  `zellij action rename-session` that a pass had given before, to an earlier session started under the same name.
  The log is no longer read for names. As in 0.3.1, a renamed session keeps its name unless the list of given names
  holds it or it has the old stamp, so a name 0.2.0 gave stays.
- Keeping a name in the dashboard now takes the pass lock. Before, a pass already under way could rename the session
  right after its name was kept. The dashboard also refuses to keep a name no session has any more, as when the dialog
  stayed open while a pass renamed the session. A kept name goes in the decision log as
  `NAME :: name kept from the dashboard`.
- When zellij leaves a session out of its list for a moment, its socket file still counts, so its entries on the lists
  of given and kept names stay. `plan`, the dashboard and dry runs now also avoid a name whose socket file is there, as
  a pass does, so they show the names a pass would give.

## [0.3.3] - 2026-11-06

### Changed

- Names hold no letters but English ones, so they type on any keyboard and need no quotes in a shell. A word in another
  script, or with an accent, is left out whole. A title with nothing left says nothing, so the name comes from another
  pane, or from the directory and the program. A name a pass gave in another script can be replaced at the next pass,
  like any name on the list of given names.

### Fixed

- A pass and the dashboard refuse a new name whose socket file is already there, whether zellij lists that session at
  that moment or not. Since zellij 0.44, a rename onto it cuts the other session off. In the integration scenarios,
  zellij once left a session out of its list for a moment right after renaming it, while a pass checked only the list.

## [0.3.2] - 2026-11-06

### Added

- In the dashboard, Enter in the `r` field with the session's current name in it keeps that name, whoever gave it, a
  name zellij made up included. zellij keeps nothing that tells a name it made up from the same words typed with
  `zellij -s`, so a person who typed one says so once for each session. The names go in
  `~/.local/state/zellij-reaper/kept`.

### Fixed

- A name 0.2.0 gave follows what the session shows again. For a name the list of given names does not hold, the renames
  in the decision log tell whether a pass gave it, for as long as the log holds them. That also covers a list that was
  lost. (0.3.4 took this back out.)

## [0.3.1] - 2026-11-06

### Added

- In the dashboard, Enter in the `r` field with the session's current name in it keeps a name a pass gave: passes leave
  it alone from then on.
- [Setup](docs/setup.md#without-systemd) says what a crontab needs to run the passes without systemd.
- `make integration` compares zellij's word lists with the source of each release it tests, and a test fails if the
  code imports a networking package.

### Fixed

- `auto_rename` tells who chose a name from the name the session started under, which zellij keeps on the server's
  command line. These used to lose their name at the next pass:
  - a session someone started under a name a pass had given to a session now gone
  - two words from zellij's lists given by hand (`lucky-duck`)
  - a name in the shape of the old stamp that is no real date (`report_2025-2026`), or that a session started under
  - with `auto_rename_stamp = "time"`, a stamped name someone took from `r`

  A session started under two words from zellij's lists still reads as made up, since nothing tells them apart.
- A pass that finds no sessions clears the list of given names.
- A deleted session stays deleted. A server caught saving its layout as it stopped could write the session's data back,
  and the session came back as exited until `exited_after` had passed. A delete now checks what zellij still lists: it
  deletes a session that came back as exited once more, and reports one still running five seconds later as `FAILED`.
- A rename that timed out, or that zellij accepted without listing the new name in time, no longer counts as refused.
  The new name stays on the list of given names, so it can still follow what the session shows, and the dashboard keeps
  a protected session's protection on both names.
- A list of given names that cannot be read no longer stops every command, the timer's passes included. Its file now says
  what it holds.
- The steps for machines with no internet access: a failed checksum stops the install, the closed machine sets
  `version` itself, since the variable does not carry across with the files, the demo image is pulled before it is
  saved, `rpm -U` also upgrades, and building from source names the Go it needs. The install script's advice for a
  network that cannot reach GitHub works when followed.
- The third-party notices carry zellij's license, since its word lists are copied. The help for `uninstall --purge` and
  `run`, and the settings file, now say what they do.

## [0.3.0] - 2026-11-05

### Added

- Setup has a section on [machines with no internet access](docs/setup.md#without-internet-access).
- The integration scenarios run with no network, and let zellij pick a name on each release.

### Changed

- `auto_rename` leaves a name someone chose alone. It replaces only a name zellij made up (`brave-peach`), a name with the
  stamp earlier versions added, and a name it gave itself, which it now keeps in `~/.local/state/zellij-reaper/named`.
  Keeping a name used to take the protected list, which keeps the session from being reaped as well. A name 0.2.0 gave
  is on no list and has no stamp, so it stays until someone changes it.

### Fixed

- The scenarios wait for zellij to list a session before using it: on 0.43.1 a session that has only just started can
  answer "not found" with exit status 0, and a command sent then is lost.

## [0.2.0] - 2026-11-05

### Changed

- `auto_rename` no longer adds the time of last use to a name, which renamed a session in use on every pass: 20,536 of
  the 20,839 renames in the author's log moved nothing but the time. A name now changes when what the session shows
  changes. `auto_rename_stamp = "date"` or `"time"` adds a stamp back. A name from an earlier version loses its stamp at
  the next pass.
- Names read better. Titles lose flags, the directories of paths, URL schemes and ssh users. A shell's own title gives its
  directory, or the machine when it is another one. A title that only names a program (`Claude Code`) goes after the
  directory, read from where the first pane works now. The focused pane comes first. A command line gives only its plain
  words up to the first flag, since arguments can hold a password. Names stop at 32 columns, at the end of a word, and a
  `.` stays inside a word (`vim-notes.md`).
- Who is attached now comes from the kernel's socket table, which matches `list-clients` on every tested release. `plan`
  over ten sessions takes about 26 ms (100 to 123 ms before). On zellij 0.44.2 it took 1125 ms, up to 5 s, and 19% of
  verdicts came out `attach-unknown`, because that release answers overlapping `list-clients` calls with nothing.
- Commands start faster: a text field of its own replaces a widget that searched `PATH` for clipboard tools at every
  start (`plan` on WSL from 79 ms to 45 ms). A paste into the filter now filters.
- `r` in the dashboard offers the name a session's panes point to, with `auto_rename` on or off.
- The installer's address is now `releases/latest/download/install.sh`, and it travels with each release. The old
  `raw.githubusercontent.com/.../main/install.sh` address stops working.
- The repository's layout: packages sit in layers under `internal/`, which a test enforces, and the root holds less.

### Fixed

- A line added at the end of the settings file landed in its `[log]` table and did nothing, with `protect` among the keys
  it could silently lose. The file no longer has that table, and a key nothing reads now stops the command with an error,
  with a word on where to move it when it sits under an older file's `[log]`.
- `config edit` takes an `$EDITOR` with arguments (`code --wait`).
- A rename by hand to a name that is taken is refused. zellij 0.44 and later accept it, and cut the other session off.
- A rename is reported as done only once zellij lists the new name.
- zellij's own `ps`, which it runs to save its layouts, no longer makes an idle session look busy for that pass.
- A session whose `list-clients` failed no longer comes out `attach-unknown` when the socket table can tell.
- Two sessions showing the same title no longer swap between `-2` and `-3` on every pass.
- `plan` and dry runs name two sessions showing the same title as a pass would.
- `zellij-reaper version` in the demo image reports the release instead of `dev`.

## [0.1.1] - 2026-11-05

### Added

- A demo image, `ghcr.io/kanggu12/zellij-reaper-demo`: the dashboard on twelve demo sessions in a throwaway
  container, without a clone or Go. `--shell` opens a shell in the same sandbox.
- `THIRD_PARTY_NOTICES.md`, listing every module the binary is built from and the font the logo is drawn from, in
  the release archives and packages.
- Pictures in the documentation: the checks for two sessions side by side, the dashboard, a sequence diagram of one
  pass, and what a pass reads and changes.

### Fixed

- A session with a client attached is no longer renamed by `auto_rename` when a protected program runs in it.
- The protected list is re-read before each delete and shared under a lock, so a protection added from another
  terminal is never overwritten or missed.
- A rename from the dashboard keeps the protection, and refuses a name too long for the session's socket path.
- `recover --aggressive` keeps an orphan server when a client is connected, a pane is busy, a protected program runs
  in it, or a protected session has lost its socket file.
- `install` and `doctor` warn about settings that only the shell sees, which the timer never reads.
- A migrated custom log path is honoured, `logs -n` rejects a negative count, and unit files quote their paths.
- Release notes are taken from this file again (they were empty in 0.1.0).

## [0.1.0] - 2026-11-04

The first release of zellij-reaper as one Go binary. It replaces the earlier shell scripts
(`zellij-reaper.sh` and `zellij-reap`); `zellij-reaper install` moves an install of them over.

### Added

- A dashboard (`zellij-reaper`): every session, sorted by what the next pass does to it, the checks that decide it,
  its panes, the latest reaps, and keys to attach, reap, protect, rename and run a pass. English and Korean.
- The first pass waits ten minutes after `install`, and `install` says how many sessions it would delete.
- `make demo`: the dashboard on demo sessions in a throwaway container.
- `plan` (with `--json`), `logs -f`, `doctor`, `protect` / `unprotect`, `config show|path|init|edit`, shell completion.
- A settings file, `~/.config/zellij-reaper/config.toml`, with separate waits for idle and exited sessions
  (`idle_after`, `exited_after`), `protect_commands` (coding agents keep their session while they run), `shells`,
  and `interval`. `ZELLIJ_REAPER_*` variables override it.
- Integration tests on real zellij 0.43.1, 0.44.3 and 0.45.1, and release archives, checksums and `.deb`, `.rpm`,
  `.apk` and Arch packages for amd64 and arm64.

### Changed

- Clients are counted with `zellij action list-clients`, which asks the server; the count in zellij's metadata file
  lags and is used only when the server does not answer.
- The log moved to `~/.local/state/zellij-reaper/reaper.log`; its lines keep their words.
- Defaults for a new install: 12 hours idle and 3 days exited before a session is reaped, and `auto_rename` off. A
  moved-over install keeps the shell version's values.

### Fixed

These were wrong in the shell version:

- A renamed session was never matched to its server, so it was kept forever as `server PID not found`. Servers are
  now found through their socket file in the kernel's socket table.
- A pane that runs a program directly (`zellij run`, a command pane from a layout) looked idle. Only a shell at its
  prompt is idle now.
- zellij 0.43's version-named directories were not found.
- `recover --aggressive` stopped the servers of renamed sessions as if they were orphans.
- A pass that found no sessions was logged as `zellij list-sessions failed`.

[Unreleased]: https://github.com/kanggu12/zellij-reaper/compare/v0.6.1...HEAD
[0.6.1]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.6.1
[0.6.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.6.0
[0.5.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.5.0
[0.4.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.4.0
[0.3.5]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.3.5
[0.3.4]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.3.4
[0.3.3]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.3.3
[0.3.2]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.3.2
[0.3.1]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.3.1
[0.3.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.3.0
[0.2.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.2.0
[0.1.1]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.1.1
[0.1.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.1.0
