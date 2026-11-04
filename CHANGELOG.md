# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
the project uses [semantic versioning](https://semver.org/).

## [Unreleased]

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

[Unreleased]: https://github.com/kanggu12/zellij-reaper/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.1.1
[0.1.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.1.0
