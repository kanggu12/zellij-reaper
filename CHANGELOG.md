# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
the project uses [semantic versioning](https://semver.org/).

## [Unreleased]

## [0.3.3] - 2026-11-06

### Changed

- Names hold English letters and digits only, so they type on any keyboard and need no quotes in a shell. A word in
  another script, or with an accent, is left out whole, and a title left with nothing gives way to the directory and the
  program. A name a pass gave in another script before changes to its English one at the next pass.

### Fixed

- A rename refuses a name whose socket file is already there, whether zellij lists that session at that moment or not.
  Since zellij 0.44 a rename onto it cuts the other session off. In the integration scenarios, zellij once left a session
  it had just renamed out of its list for a moment, and a pass checked the list alone.

## [0.3.2] - 2026-11-06

### Added

- In the dashboard, `r` and Enter on the name a session has keeps that name, whoever gave it, a name zellij made up
  included. zellij keeps nothing that tells a name it made up from the same words typed with `zellij -s`, so a person who
  typed one says so once. The names go in `~/.local/state/zellij-reaper/kept`.

### Fixed

- A name 0.2.0 gave follows what the session shows again. Where the list of given names has no word on a name, the
  renames in the decision log tell whether a pass gave it, for as long as the log holds them. That also covers a list
  that was lost.

## [0.3.1] - 2026-11-06

### Added

- In the dashboard, `r` and Enter on the name a session has keeps a name a pass gave: passes leave it alone from then on.
- [Setup](docs/setup.md#without-systemd) says what a crontab needs to run the passes without systemd.
- `make integration` compares zellij's word lists with the source of each release it tests, and a test holds the code to
  importing no networking package.

### Fixed

- `auto_rename` tells who chose a name from what the session started under, which zellij keeps on the server's command
  line. Each of these used to lose its name at the next pass: a session someone started under a name a pass gave to a session
  now gone, two words from zellij's lists given by hand (`lucky-duck`), a name in the shape of the old stamp that is no date
  (`report_2025-2026`) or that a session started under, and, with `auto_rename_stamp = "time"`, a stamped name taken from `r`.
  A session started under two words from zellij's lists still reads as made up, since nothing tells them apart.
- A pass that finds no sessions clears the list of given names.
- A deleted session stays deleted. A server caught saving its layout as it stopped could write the session's data back, so
  it came back as exited until `exited_after` had passed. A delete now looks at what is left, and reports a session still
  running five seconds later as `FAILED`.
- A rename that timed out, or that zellij took without it showing in time, no longer counts as refused. Its name stays on the
  list of given names, so it can still follow what the session shows, and the dashboard keeps such a session's protection
  on both names.
- A list of given names that cannot be read no longer stops every command, the timer's passes included. Its file now says
  what it holds.
- The steps for machines with no internet access: a failed checksum stops the install, the demo image is pulled before it
  is saved, `rpm -U` also upgrades, and building from source names the Go it needs. The install script's advice for a network
  that cannot reach GitHub works when followed.
- The third-party notices carry zellij's license, since its word lists are copied. `uninstall --purge`, the `run` help and the
  settings file say what they do now.

## [0.3.0] - 2026-11-05

### Changed

- `auto_rename` leaves a name someone chose alone. It replaces only a name zellij made up (`brave-peach`), a name with the
  stamp earlier versions added, and a name it gave itself, which it now keeps in `~/.local/state/zellij-reaper/named`.
  Keeping a name used to take the protected list, which keeps the session from being reaped as well. A name 0.2.0 gave
  is on no list and has no stamp, so it stays until someone changes it.

### Added

- Setup has a section on [machines with no internet access](docs/setup.md#without-internet-access).
- The integration scenarios run with no network, and let zellij pick a name on each release.

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

[Unreleased]: https://github.com/kanggu12/zellij-reaper/compare/v0.3.3...HEAD
[0.3.3]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.3.3
[0.3.2]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.3.2
[0.3.1]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.3.1
[0.3.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.3.0
[0.2.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.2.0
[0.1.1]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.1.1
[0.1.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.1.0
