# Security

## Reporting a vulnerability

Please report a security problem privately: on GitHub, open the repository's **Security** tab and choose **Report a
vulnerability**. Do not open a public issue for it.

Say what an attacker can do, how to reproduce it, and which version you ran (a release tag or a commit). The
maintainer will reply, agree on a fix with you, and publish an advisory with the fixed release, crediting you unless
you ask not to be named.

The latest release and `main` get security fixes.

## What matters most

zellij-reaper runs on a timer, as you, and deletes zellij sessions. These are the problems to report:

- a way to make it delete a session that a check should have kept (one with a client attached, a busy pane, a
  protected program or a protected name), or rename one with a protected or chosen name;
- a way for another user, or a program you did not run, to make it act on your sessions, or on theirs;
- a session name, pane title or file under zellij's directory that makes it run a command, write outside its own
  files, or follow a link to somewhere else;
- `scripts/install.sh` installing something other than the release it checked.

It makes no network connection and needs no privileges. Session names and pane titles are written to its own log,
on your machine.
