<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img alt="zellij-reaper" src="assets/logo-light.svg" width="360">
  </picture>
</p>

# Guides

New here? The [Quick start](../README.md#quick-start) installs it, shows `plan` and opens the dashboard. To try the
dashboard on demo sessions without installing anything:

```sh
docker run --rm -it --init ghcr.io/kanggu12/zellij-reaper-demo
```

## Using it

| Guide | For |
|:---|:---|
| [How it decides](how-it-decides.md) | what a pass does, every check in order, where each fact comes from, every line of the log |
| [Setup](setup.md) | installing, every setting, protecting and naming sessions, zellij versions, macOS, Windows, WSL 2 |
| [Commands](commands.md) | every command and flag, the `plan --json` shape, the dashboard's keys and colors |
| [Troubleshooting](troubleshooting.md) | a timer that does not run, a session kept longer than you expect, a session nothing can delete |

## Working on the code

| Guide | For |
|:---|:---|
| [Internals](internals.md) | the layers, how the facts are read and why, the design decisions, the tests |
| [Contributing](../.github/CONTRIBUTING.md) | how the code fits together, what a change needs, how to add a rule, a setting or a language |
| [Changelog](../CHANGELOG.md) | what changed in each release |
| [Third-party notices](THIRD_PARTY_NOTICES.md) | the license of everything zellij-reaper is built from |
