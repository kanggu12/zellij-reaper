# A throwaway machine for the integration scenarios: no zellij or zellij-reaper
# inside; run.sh mounts the release under test and the binary.
FROM debian:bookworm-slim
# procps for pgrep, bsdutils for script(1) (a real terminal client), jq to read
# `plan --json`, sqlite3 to read the session index zellij keeps since 0.46 with
# a reader that is not the reaper's own, ca-certificates for nothing in
# particular but sanity.
RUN apt-get update \
 && apt-get install -y --no-install-recommends procps bsdutils jq sqlite3 ca-certificates \
 && rm -rf /var/lib/apt/lists/*
ENV HOME=/root SHELL=/bin/bash TERM=xterm-256color
