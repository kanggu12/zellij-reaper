# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
the project uses [semantic versioning](https://semver.org/).

## [Unreleased]

## [0.3.0] - 2026-11-05

### Changed

- `auto_rename` leaves a name someone chose alone. It replaces only a name zellij made up (`brave-peach`), a name with the
  stamp earlier versions added, and a name it gave itself, which it now keeps in `~/.local/state/zellij-reaper/named`.
  Keeping a name used to take the protected list, which keeps the session from being reaped as well.

### Added

- Setup has a section on [machines with no internet access](docs/setup.md#without-internet-access).
- The integration scenarios run with no network, which holds zellij-reaper to making no connection, and let zellij pick a
  name on each release, so new words in its lists are caught.

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

[Unreleased]: https://github.com/kanggu12/zellij-reaper/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.3.0
[0.2.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.2.0
[0.1.1]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.1.1
[0.1.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.1.0
