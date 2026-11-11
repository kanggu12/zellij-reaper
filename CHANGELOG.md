# Changelog

All notable changes are listed here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
versions follow [semantic versioning](https://semver.org/): a release that adds something raises the minor number, one
that only fixes raises the patch number. Each release's section here is its release notes.

## [Unreleased]

## [0.14.1] - 2026-11-11

### Fixed

- A session zellij brought back from exited, after a restart say, kept the name a pass had given it for good: it starts
  under that name, which read as one someone typed, so passes left it alone, and neither its titles nor its agent's
  project renamed it again. zellij keeps the panes it saved for a session it brings back, so a pass now tells such a
  session by that file, older than the new server's socket, and renames it again when the name was a pass's. A session
  someone starts anew under a name a pass once gave keeps it: the next pass that sees it takes the name off the list of
  given names.

## [0.14.0] - 2026-11-11

### Changed

- A session where a coding agent runs (`claude`, `codex`, `gemini`, `aider`, `opencode`) is named after the git
  repository the agent works in, and follows it to the next one. Such sessions were named after the agent's title, the
  topic its conversation began with, and kept that name long after the work moved on. A pass looks at the commands the
  agent is running at that moment and counts the newest, if it started after the last one counted; once two counted in
  a row ran in the same repository, that repository is the project. The names timer looks every 5 minutes. An agent
  that looks through many repositories, one command each, keeps its name, and a command that keeps running, such as a
  dev server or a loop that keeps starting processes, counts once. A command that starts and ends between two passes
  is not seen. Between commands the name stays, written down in `projects` in the state directory. An agent started
  inside a repository is named after it at once, another agent in its place starts afresh, and a git worktree, a bare
  repository's too, counts as the repository it was made from. When the agent ends, the session's titles and directory
  name it again. A repository whose name has no English word leaves the title its turn.

## [0.13.2] - 2026-11-10

### Fixed

- The copy a rename leaves under the old name (0.13.1) was deleted whatever the name. A copy under a name the protect
  pattern covers, or the protected list holds when read again, now stays, as a session under it does; a rename by hand
  moves the list's entry to the new name, so there only the pattern protects the old one. A copy whose files were
  written more than 1.5 s after the rename stays too: a pass looks only once it has been through its sessions, and
  someone may have brought the copy back and used it meanwhile.
- A pass could start that delete past its 40 seconds; it does not look then.
- After a rename in the dashboard or with `rename`, the look ran outside the pass lock, so it could act at once with a
  pass; it holds the lock now, and an action by hand waits up to 5 s for it, not 2, since a names pass that renamed
  holds it 1.5 s longer. A copy someone brings back as it is deleted is kept, as an exited session is.
- The log said `-> deleted: NAME` for a copy, which the dashboard listed as a session reaped: it says
  `-> deleted the copy: NAME`. The guides list the new lines, and no longer say that the names timer only renames.

## [0.13.1] - 2026-11-10

### Fixed

- A rename could leave a second copy of the session under the name it had. zellij moves a renamed session's files to the
  new name, then tells the job that writes them every second; a write in between makes the old name's folder again, and
  zellij lists a session by that name as exited, which attach would bring back. It happened about once in some hundreds
  of renames. A pass that renamed sessions now waits 1.5 s and deletes an exited session under an old name, as exited
  sessions are deleted, never forced, and the log says so; a rename in the dashboard or with `rename` does the same once
  it has said the rename is done. Until the rename the name was the renamed session's own, so no other session had it.

## [0.13.0] - 2026-11-10

### Added

- Every archive, package and installer of a release, all that `checksums.txt` lists, comes with a signed build
  provenance attestation: a record of the workflow run and the commit it was built from, which
  `gh attestation verify FILE --repo kanggu12/zellij-reaper` checks. The setup guide says how.

### Changed

- A survey reads the zellij servers' sockets side by side, where it read one server after another: in a container with
  thirty sessions, a plan took 33.4 ms and takes 31.1 ms, of which zellij's own list is 26.

### Security

- Releases are built with Go 1.26.10. They were built with 1.26.0, in whose standard library govulncheck finds two
  vulnerabilities the program's calls reach, and more it does not call.
- The Windows task is registered by a PowerShell script that puts paths in single quotes, and only `'` was doubled.
  PowerShell ends such a string at the curly quotes U+2018 to U+201B as well, so a path with `’` in it, the program's,
  the settings' or zellij's, ended the string, and what followed ran as a command. All five are doubled now.
- Session names, pane titles and command lines are chosen by whatever runs in a pane, and reached the terminal as they
  were: a name holding an OSC 52 sequence wrote the clipboard when `plan` printed it. Their control characters (C0,
  DEL, C1, direction overrides, and bytes that are not UTF-8) are now written out as text, such as `\x1b`, in the log,
  in what the commands print to a terminal, and in the dashboard and the setup screen. A pipe or a file still gets the
  bytes as they are, so the prompt hook reads a session's exact name.
- The log holds pane titles and command lines, and was made readable by everyone, in a folder anyone could list: on a
  machine that hides other users' processes, it showed them yours. The log, the lock, the name lists and the settings
  are made 0600 now, in folders of 0700. On an upgrade, `install`, which the install scripts run, takes the program's
  own two folders from 0755 to 0700 and the files in them from 0644 to 0600; a log elsewhere goes to 0600 at its next
  line, with the one before it. A mode other than those an earlier release made was chosen, and stays.
- `recover --aggressive` sends `SIGTERM`, then `SIGKILL` a second later to a server still there. A server that ended in
  that second could pass its pid to another process of yours, which would have got the `SIGKILL`. It is sent only to the
  process that started when the server did.
- The workflows run every action at the commit its version names, and the release with goreleaser v2.19.0 rather than
  the newest v2, built without the shared module cache. `install.sh` downloads from GitHub over HTTPS only, redirects
  included, with TLS 1.2 or newer; a mirror in `ZELLIJ_REAPER_BASE_URL` is still taken as given.
- A crontab line carries the installing shell's `XDG_RUNTIME_DIR`, which was the one value put there without the check
  for a line break the others get. A session whose metadata or layout file is over 4 MiB is kept, its files unread: cut,
  the file would lose the commands past the cut, a protected program among them.

## [0.12.0] - 2026-11-10

### Added

- A zellij server stopped by a signal never answers, and `zellij list-sessions` waits for every server: each pass
  stopped with "no answer within 10s" and nothing more. A list cut short now names the stopped servers and the command
  that lets them go on (``the server of beta (pid 78) is stopped: `kill -CONT 78` lets it go on``), in the log, `plan` and
  `doctor`, on macOS too.
- `doctor` reads the space left where the log is, and fails below 64 KiB: a full file system takes the open and no
  line after it, and went unseen. It also counts the deletes zellij refused in the last pass, and warns.
- The dashboard keeps a name with <kbd>r</kbd> and has no key to let it go: the panel of a session whose name is kept
  says so, with the `forget-name` command.

### Changed

- `run` ends with status 1 when zellij refused a delete, with a line saying how many, under `--quiet` too, so the
  timer's own status shows it; a session kept at the last moment or a rename zellij refused is no failure. `recover`
  ends with 1 when a step failed or zellij's index cannot be read, and `resize` when a process could not be signalled.
  All three printed the failure and ended with 0.

### Fixed

- `recover` could delete a live session with `--force`. Where zellij reads its session index and zellij-reaper cannot,
  a session with a long name is listed under its name while its socket is named by its id, and it read as broken.
  `recover` now changes nothing while the index is there and cannot be read, says why, and ends with status 1.
- Without the kernel's socket table (`unix_diag`), a server is found by the socket its command line names, which a
  rename does not change. After `bar` was renamed `baz` and `foo` was renamed `bar`, the session `bar` was judged by the
  other server's idle panes, and could be reaped while busy. The command line is used only while no rename shows;
  otherwise the running sessions are kept as `no-server`, and `doctor` says why.
- An exited session was deleted with `--force`, so one brought back between the last look and the delete was killed with
  its client attached. It is deleted without `--force` now, by passes and by hand: zellij refuses one running again, and
  the pass keeps it (`it is running again`), as the dashboard says too. Right before a delete, the panes are read again
  too: a job started in those seconds (`zellij run`, keys sent to a detached session) was ended with the session.
- A session protected from another terminal during a pass was still renamed, which left the protection on the old
  name and the session protected by nothing. The rename looks at the list again, and keeps the name.
- zellij keeps session files under `contract_version_N`, and the highest `N` present was read. A folder another zellij
  left, with sessions of the same names, had exited sessions judged by stale files. The newest folder among those the
  running servers' sockets name is read now: the zellij in use is the newest that runs, and an older one's sessions do
  not stand for its own.
- A pass that could not read zellij's index forgot every kept and given name, since zellij then lists ids, and one
  empty list, from a timer looking in another place, forgot every kept name. Kept names now go only after a pass that
  saw sessions and could name them all.
- A damaged index could make the SQLite reader copy one overflow chain for every cell that pointed at it: 2 GB from a
  10 MB file. A page is read once per table, and a size field is not made room for ahead of the pages that hold it.
- systemd and the Task Scheduler stop a pass after a minute, and one stopped so writes no end line and keeps none of its
  book of quiet sessions. A pass that has run 40 seconds, its wait for the lock included, starts no more deletes or
  renames, leaves the sessions it has not come to for the next pass and says so in the log
  (`time is up after 40s: 3 sessions left for the next pass`); every call it makes to zellij ends by 50 seconds, so the
  pass ends as passes do, names passes too.
- On macOS `lsof` had no time limit: a pane working in a directory on a file server that stopped answering held the
  pass and the lock for good. It gets 10 seconds. `systemctl`, `loginctl`, `launchctl`, PowerShell and `crontab` get a
  minute each, where one that never answered held `install`, `uninstall` and `doctor`.
- `crontab -l` was read with its error stream: a warning there became a line of the crontab at the next `install`.
- `install.ps1` never read what the program returned, so a task that did not register still ended the install as
  done; it stops with the same words as `install.sh` now. An upgrade while the program runs (the dashboard, the task)
  failed on a file in use: the running copy is moved aside, and the next install removes it.
- <kbd>ctrl+c</kbd> on the setup screen ended with status 0, which an install script took for a finished install with
  no timer running. It ends with 130, and the script says the timers are not running.
- Help ran past 80 columns in most commands, and a flag's words to 125; the line above the tables of `plan` and `run`
  was about 94. Help is set in lines of 80 and the line above the tables breaks between its parts, both held by tests.
  `install --help` said Windows gets two tasks; it gets one. `recover --help` gives macOS's 103-byte limit too.
- A missing zellij, a busy lock in `recover`, `keep-name` and `forget-name`, a setup screen that did not open, a missing
  session name, an unknown shell for `shell-hook`, a negative `--lines`, and `config init` and `config edit` stopping
  were said in English on a Korean screen. They are said in the language on screen; a missing zellij also says how to
  name it in the settings.
- The dashboard cut a long error to one line, and with it the end that says what to do, such as the command that lets
  a stopped server go on. It is wrapped now.
- `forget-name` said a name was no longer kept, and logged it, for a name that never was. It says the name was not on
  the list, as `unprotect` does.

## [0.11.0] - 2026-11-10

### Added

- zellij's main branch, its next release, names a session's socket and files by a short id that stays through renames,
  and keeps the names in an SQLite index. zellij-reaper reads that index, without a database driver, so passes, the
  dashboard, `recover` and `doctor` find the sessions there; until now every one of them read as unknown and was kept.
  A session's first name comes from its server's environment, which keeps it through every rename, and an orphan server
  of a session kept by id is named, and protected, by the name the index gives. An index that is there but cannot be
  read keeps every session, as `name-unknown`: zellij then lists the sessions it keeps by id under their ids, which no
  protection names. The integration scenarios run on zellij's main branch as well as on 0.43, 0.44 and 0.45.
- `install` writes the places the installing shell keeps its files into the timer: `XDG_CONFIG_HOME`,
  `XDG_STATE_HOME`, `XDG_CACHE_HOME`, `ZELLIJ_SOCKET_DIR`, and `TMPDIR` where it decides zellij's socket directory. With
  one of them set, the dashboard protected a session in one list while the timer read another, or zellij listed the
  sessions from a place the timer's zellij never looked, and the timer reaped what the dashboard showed as protected.
  `doctor` notes each variable this shell sets otherwise than the timer.
- `uninstall --purge` takes the prompt hook out of `~/.bashrc`, `~/.zshrc` and fish's `config.fish`: the line `setup`
  added, where it still reads as `setup` wrote it. The file is changed in place, so a link into a directory of dotfiles
  stays a link; a line that mentions the hook in other words stays, and is named. A purge also removes what a save cut
  short left beside the settings and the lists, says which file it could not remove and why, names what is still in a
  folder it had to leave, and ends with where the program is, since that stays.
- `recover` writes what it does to the log, one line for each step as it takes it: a session deleted and its files
  cleared, an orphan server stopped (with `SIGKILL` where `SIGTERM` was not enough) or kept on the look right before the
  stop, and every step that did not go through. Long after, the log still says which servers a recover stopped and why
  one was kept. A dry run writes nothing. The lines are in the
  [table of the log's lines](https://github.com/kanggu12/zellij-reaper/blob/main/docs/how-it-decides.md#the-log).
- `recover` ends with the number of orphan servers it leaves and the command that stops them, in a dry run too. It
  listed such a server and closed with "nothing to repair": only `--help` said that `--aggressive` stops it.
- `doctor` says how the last pass went, read from the log: when it ran and what it did, or why it stopped. It also says
  when the passes stopped coming, when something holds the pass lock so that every pass gives up, and when the log
  cannot be written. Measured in a container, a process holding the lock, a pass killed half way and a log on a
  read-only or full file system each left the timer running passes that ended with status 0, with nothing to show it.

### Fixed

- On Linux `uninstall` removed the timers of the scheduler chosen that day. After systemd was turned on or off (WSL),
  the other one's timers ran on while `uninstall` said they were gone; it removes both now, the systemd units and the
  reaper's lines in the crontab. A timer whose unit files are removed from a shell where the user's systemd does not
  answer (`su`, `sudo`) stays loaded and keeps firing: `uninstall` says so there, and from a shell where systemd answers
  it also stops the timers by name. A crontab that cannot be read while a cron daemon runs is said to keep its jobs.
- `uninstall` stopped at a settings file that does not parse, the state in which one reaches for it. It removes the
  timers on the default settings now, and a purge says that a log `log.path` put elsewhere stays.
- With the prompt hook in fish's startup file, every fish printed six lines of error once the program was gone: the
  line `setup` wrote for fish did not ask whether the program was there, as the lines for bash and zsh do. `setup` writes
  `type -q zellij-reaper; and zellij-reaper shell-hook fish | source` now, and `uninstall --purge` takes out the line
  earlier releases wrote too.
- On Alpine a user without a crontab could not install: busybox says "can't open" where cron and cronie say "no crontab
  for", and only the latter was taken for an empty crontab. Where the crontab could not be read at all, `install` had
  written the settings file before it failed; it reads the crontab first now. A crontab is written back byte for byte: a
  blank line at its end, as in Alpine's default one, was dropped by `install` followed by `uninstall`.
- A line break in the binary's path, the settings file, zellij's directory or a carried variable is refused for cron,
  and in a carried variable for launchd, as it already was for systemd: it would end the line it sits on.
- `uninstall` said "Removed the timers." where there were none; it says "Found no timers to remove." `doctor` says
  "1 session".
- Where no scheduler answers, the install script ended with `zellij-reaper install`, before the line about adding its
  directory to `PATH`. It prints that line now, says the binary is in place and the timers are not, gives the command
  to try again, and ends with that status.
- `delete` with no name answered "requires at least 1 arg(s), only received 0"; it says "name a session to reap". Its
  question said "Its panes are closed", which reads as the state the session is in; it says "This closes its panes".
- The dashboard's recent decisions showed a name kept with `keep-name`, but not a session reaped with `delete` or
  renamed with `rename`, though the log has both.
- A command that picks its colors in several places asked the terminal for its background each time. On a terminal that
  answers no such question each asking waited out its time limit, and `setup --hook` took 12 s; it asks once.
- A zellij command stopped at its time limit kept a pass waiting where something it had started still held its output.
  Measured with such a zellij, a pass hung until it was killed from outside, holding the pass lock: under cron or
  launchd, which do not stop a pass, every later pass would have given up. The output is let go a second after the
  limit, and the pass goes on.
- `logs` stopped at a protected list that could not be read, which stops every pass as well: the log is where one looks
  for why. It reads only the settings now.
- An error from a zellij command stopped at its time limit named the command twice: "zellij list-sessions: zellij
  list-sessions --no-formatting: no answer within 10s".
- `uninstall --purge` with `--config FILE` removed the default settings file and kept the one named.
- `recover --aggressive` reported a server that ended after `SIGTERM`, just before the `SIGKILL` that was to follow, as
  one it could not stop: "no such process". It says the server was stopped.
- The `.deb` package had no section, and pacman showed "Unknown Packager".

## [0.10.1] - 2026-11-10

### Fixed

- After 0.10.0, `install` counted the sessions the next pass reaps at the time of that pass, but `plan` and the
  dashboard counted them as of now under the same words, "to reap at the next pass": right after an install one said 1
  and the other 0 about a session a minute short of its wait. `plan` and the dashboard now count at the time the timer
  runs the next pass too, as `install` and the setup screen do, whenever the timer is running and says when that is:
  systemd, cron and the Task Scheduler do. launchd on macOS does not, so there `plan` and the dashboard count as of now
  and `install` gives no count. When the two counts differ, `plan` and the dashboard give both, as in "2 to reap at the
  next pass · 1 due now", since only the sessions due now show in yellow; the help screen's legend says yellow is due
  now. The details of a session whose wait ends first say "Will be reaped at the next pass, at 14:00, if nothing
  changes."
- A session the next pass would be the first to find quiet (one without terminal times, which is every session on
  Windows) is not counted for that pass, by `plan`, the dashboard, `install` or the setup screen: its wait starts
  there. A cron line's next time no longer counts while the cron daemon is down.
- In an 80-column terminal, `plan` puts the hint about the dashboard under the summary rather than wrap the line.

## [0.10.0] - 2026-11-10

### Added

- The dashboard's top line names the build: the name as the logo writes it, this build's version (`v0.10.0`, or `dev`
  and the commit for a build from source), then zellij's. The logo's mark, the text cursor whose top became a scythe,
  stands beside the name on the help screen, on the setup screen when it has the rows, and over an empty dashboard.

### Fixed

- In dry-run mode <kbd>f</kbd> asked whether to reap and then reported sessions as reaped. It runs without asking now
  and says what it would have reaped, with nothing deleted.
- A dialog's text was wrapped two cells too wide and then again by the screen library, which left a word alone on a
  line.
- On a short setup screen the sessions to keep fell under the keys, while <kbd>Space</kbd> still marked the one under
  the cursor. The intro gives way first now.
- With a filter on, the status line left out how many rows were scrolled above.
- `install` counted the sessions the first pass would reap at the time it ran, not at the time of that pass, so it could
  disagree with the setup screen just before it.
- `logs -n 0` said the log was empty; `doctor` said "every 0s" when it could not read the timer's interval; `tui`
  without a terminal printed the screen library's error about `/dev/tty` rather than pointing at `plan`.
- A rename zellij refused, or one held up by a pass, was reported in English on a Korean screen; `keep-name` did not
  say when another pass held the lock.
- Turning names on in setup left the prompt hook switched off.
- The question before reaping a protected exited session spoke of closing its panes.
- A narrow table cut long names at the end, so `release-notes` and `release-notes-2` looked the same; it keeps both ends
  now. With `NO_COLOR`, the selected row is drawn reversed, as its background no longer shows.
- On a short terminal the help screen's colors stand in two columns rather than lose their last lines.
- Wording: dry-run lines say what a dry run logs, setup says "reap" throughout, `protect` and `keep-name` say when
  their lists are empty, `unprotect` says when a name was not on the list, and `config init` says where it wrote.

## [0.9.0] - 2026-11-08

### Added

- `setup` takes the setup screen's choices as flags: `--idle-after`, `--exited-after`, `--dry-run`, `--auto-rename`,
  `--hook` and `--protect`. With any of them it opens no screen and needs no terminal, for scripts and
  `ssh host command`; it checks every value before it writes one, changes only the settings the flags name, and then
  starts the timers as <kbd>⏎</kbd> does.
- `delete NAME...` reaps sessions by hand, as <kbd>d</kbd> does in the dashboard, and keeps what it keeps: a session
  with a client attached, one whose clients nobody can tell, or one not listed, going by a plan made just before and by
  zellij asked once more. It asks the dashboard's question first; `--yes` reaps without asking, and with no terminal it
  needs it.
- `rename NAME NEW-NAME` renames a session, as <kbd>r</kbd> does in the dashboard: passes keep the new name, and a
  protection follows the session to it.

### Fixed

- A duration too long for Go to hold could wrap around: `9999999999w9999999999w` came out as zero, which switches the
  wait off. Such a value is refused now.
- The question before a reap by hand in the dashboard said "is protected (protected (list))"; it says "is protected
  (list)".

## [0.8.1] - 2026-11-08

### Fixed

- Where cron runs the passes, the setup screen said nothing is deleted before a first pass ten minutes away, but cron
  starts it the next time its line comes round, which can be sooner. The screen now gives that time, the one `install`
  reports, worked out on the cron daemon's clock: the system's time zone, which `TZ` can differ from, with a line that
  summer time skips counted as run right after the jump.
- Opened where the timers run already, the setup screen gives their next pass when it comes before the first one an
  install would start, since that pass runs with the settings as they are.
- On Windows, the next and last pass times showed in UTC, in `install`, `doctor` and the dashboard. They show in local
  time.
- The setup screen cut the reason it lists no sessions (zellij not installed, say) at the edge of a 100-column window;
  it wraps.
- The crontab lines named zellij's directory twice in their `PATH` when it is `/usr/local/bin` or another of the
  usual ones.
- `install.ps1` turned a user `PATH` stored as a plain string into one Windows expands; it keeps the kind it has.

## [0.8.0] - 2026-11-08

### Added

- Where systemd has no user instance (WSL without systemd, many containers) but a cron daemon runs, `install` puts the
  passes in your crontab: two lines marked `# zellij-reaper`, each with its own `PATH` and `XDG_RUNTIME_DIR`, at the
  nearest step cron can repeat at. Your other lines stay; `uninstall` removes only these, and an install once systemd
  answers moves the passes back to its timers.
- A settings file that does not load says how to get past it: `zellij-reaper config edit`, or moving it away.

### Changed

- The install scripts open the setup screen only on a first install; an upgrade rewrites the timers as before. Opened
  by them, <kbd>esc</kbd> keeps the defaults and the install goes on, ctrl+c stops it, and a screen that cannot open
  falls back to `install`.
- The setup screen lines up in Korean, keeps the column of notes in place and leaves out a note that does not fit
  instead of cutting it, wraps its first line on a narrow terminal, follows the cursor in a short one, and names the
  mode on <kbd>⏎</kbd> ("save and start (log only)").

### Fixed

- `setup` added a new setting at the end of a settings file that ends in a table (`[log]`), where TOML reads it as
  part of the table and the file stops loading. New settings now go above the first table, and a line keeps its
  Windows line ending.
- `setup` wrote values from `ZELLIJ_REAPER_*` variables into a new settings file, where they stayed after the
  variable was gone.
- `install.sh` stopped without starting the timers under dash when the terminal could not be opened (`/dev/tty`).

## [0.7.0] - 2026-11-08

### Added

- `zellij-reaper setup`: a screen that shows what the first pass would reap and lets you choose the idle and exited
  waits, whether passes reap or only log what they would reap, names and the prompt hook, and which listed sessions
  to keep, then starts the timers. It writes only the keys it sets into the settings file, so notes and other
  settings written by hand stay. The installers open it in a terminal (`ZELLIJ_REAPER_NO_SETUP=1` skips it), and
  with no settings file `zellij-reaper` opens it instead of the dashboard.
- The README shows the setup screen and a chart of how long a pass takes with 13 to 100 sessions; troubleshooting
  opens with a figure from what `doctor`, `plan` and `logs` say to the section that covers it, and the Files section
  of setup.md with a figure of where each system keeps its files.

## [0.6.2] - 2026-11-07

### Changed

- The README's Quick start is commands to paste: install, `plan`, the dashboard, and two lines for names and the
  prompt hook.
- `uninstall` says it removed the timers, which are launch agents or a scheduled task outside Linux; `here` says in
  its help that it prints the current name of the shell's session; two Korean messages no longer put a particle
  after a name it may not fit.

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

[Unreleased]: https://github.com/kanggu12/zellij-reaper/compare/v0.14.1...HEAD
[0.14.1]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.14.1
[0.14.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.14.0
[0.13.2]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.13.2
[0.13.1]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.13.1
[0.13.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.13.0
[0.12.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.12.0
[0.11.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.11.0
[0.10.1]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.10.1
[0.10.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.10.0
[0.9.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.9.0
[0.8.1]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.8.1
[0.8.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.8.0
[0.7.0]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.7.0
[0.6.2]: https://github.com/kanggu12/zellij-reaper/releases/tag/v0.6.2
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
