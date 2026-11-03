# A throwaway machine for the integration scenarios: no zellij or zellij-reaper
# inside; run.sh mounts the release under test and the binary.
FROM debian:bookworm-slim
# procps for pgrep, bsdutils for script(1) (a real terminal client), jq to read
# `plan --json`, ca-certificates for nothing in particular but sanity.
RUN apt-get update \
 && apt-get install -y --no-install-recommends procps bsdutils jq ca-certificates \
 && rm -rf /var/lib/apt/lists/*
ENV HOME=/root SHELL=/bin/bash TERM=xterm-256color
