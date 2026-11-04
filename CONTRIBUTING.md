# Contributing to zellij-reaper

zellij-reaper deletes sessions without asking, so a change has to come with a test showing it still keeps every
session it should.

## Reporting

- **A session it got wrong**, kept or deleted: open an issue with `zellij-reaper doctor`, `zellij-reaper plan --json`
  for that session, its lines from `zellij-reaper logs`, `zellij --version`, and what was running in it.
- **A zellij release, a distribution or a shell CI does not cover**: say what `doctor` and `plan` report there.
- **A larger change** (a new rule, a new setting): open an issue first, so the behavior is agreed before the code.
- **A security problem**: see [SECURITY.md](SECURITY.md), not an issue.

## Building

You need Go (the version in `go.mod`; `go` fetches it if yours is older) and, for the integration tests and the
sandbox, Docker.

```sh
git clone https://github.com/kanggu12/zellij-reaper && cd zellij-reaper
make build && ./zellij-reaper plan
```

`plan` and the dashboard change nothing until you press a key that acts, so they are safe on your own sessions. `run`
is not: use `run --dry-run`, or `make demo`, which stages the same demo sessions in the integration image and
mounts the binary you just built. The published demo image is built from `test/demo/Dockerfile`.

## Testing

```sh
make check                               # gofmt, go vet, staticcheck, shellcheck, go test -race ./...
make integration                         # real zellij, every release in the matrix, in containers
make integration ZELLIJ_VERSIONS=0.45.1  # one release
```

Unit tests build fake machines with `internal/testutil`: a `/proc` with chosen processes and terminals, real Unix
sockets for the inode matching, session files with chosen times. [Internals](docs/internals.md#tests) lists what each
level covers.

## Where a change goes

- **A rule** goes in `internal/policy`: a `Code`, its place in `Decide`, its `LogText`, its words in both catalogs
  (`why.*`, `check.*`), and rows in `policy_test.go`. A fact that cannot be read must keep the session.
- **A protected program** goes in `config.DefaultProtectCommands`, and in the defaults in `docs/setup.md`. If it runs
  as an interpreter's script, check that `TestProtectedCommands` finds it.
- **A zellij release**: run `make integration ZELLIJ_VERSIONS=x.y.z`. If it passes, add it to the matrix in
  `.github/workflows/ci.yml` and to `default_versions` in `test/integration/run.sh`. If it fails, record what changed
  (`list-sessions -n`, `action list-clients`, a `session-metadata.kdl`) under `internal/zellij/testdata/x.y.z/`, add a
  parser test, then fix.
- **A dependency**: after adding, removing or upgrading a module, run `make notices`. CI fails while
  `docs/THIRD_PARTY_NOTICES.md` does not match what the binary is built from.
- **A language**: add a catalog next to `en` and `ko` in `internal/i18n/catalog.go`, then a `Lang` constant, its entry
  in `catalogs`, and its locale prefix in `Detect` and `Set` (`internal/i18n/i18n.go`). `go test ./internal/i18n/
  ./internal/tui/` checks that every key and placeholder is there and that every frame still fits.
- **The dashboard**: `go test ./internal/tui/` renders every state at five sizes in both languages.
  `zellij-reaper tui --snapshot 132x32` prints one frame (a hidden flag; also `--theme light`, `--select NAME`); the screenshots in
  `docs/assets` come from such frames, taken in the `make demo` sandbox.

## Style

- The log's words are an interface: add new kinds of lines, never reword the ones there.
- Words people see go through `i18n.T`, in English and Korean. In Korean, a particle never follows a name directly
  (the catalog tests check).
- Comment nearly every step, saying why it is there.
- Commits: a short subject in the imperative ("Keep sessions with a web client attached"), then what was wrong, what
  changed and how it was checked.

By contributing you agree that your contribution is licensed under the [MIT License](LICENSE), and to follow the
[code of conduct](CODE_OF_CONDUCT.md).
