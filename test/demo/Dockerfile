# The demo image: zellij, zellij-reaper and the staging script in one place, so
# the dashboard can be tried without a clone, Go or Docker build:
#
#   docker run --rm -it ghcr.io/kanggu12/zellij-reaper-demo
#
# Built from the repository root (the build needs cmd/ and test/), by
# .github/workflows/release.yml for every release. `make demo` builds the same
# sessions from a working tree instead.
ARG GO_VERSION=1.26
ARG ZELLIJ_VERSION=0.45.1

# zellij-reaper and the stand-in for a long-running program, static.
FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/zellij-reaper ./cmd/zellij-reaper \
 && CGO_ENABLED=0 go build -trimpath -o /out/sleeper ./test/demo/sleeper

# zellij itself, from its own release; TARGETARCH is amd64 or arm64.
FROM debian:bookworm-slim AS zellij
ARG ZELLIJ_VERSION
ARG TARGETARCH
RUN apt-get update \
 && apt-get install -y --no-install-recommends curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*
# zellij names its archives by the machine, not by Docker's word for it.
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) m=x86_64 ;; \
      arm64) m=aarch64 ;; \
      *) echo "no zellij release for $TARGETARCH" >&2; exit 2 ;; \
    esac; \
    curl -fsSL "https://github.com/zellij-org/zellij/releases/download/v${ZELLIJ_VERSION}/zellij-${m}-unknown-linux-musl.tar.gz" \
      | tar -xz -C /usr/local/bin zellij; \
    curl -fsSL "https://raw.githubusercontent.com/zellij-org/zellij/v${ZELLIJ_VERSION}/LICENSE.md" \
      -o /zellij-LICENSE.md

FROM debian:bookworm-slim
# procps for pgrep, bsdutils for script(1), which attaches a real client.
RUN apt-get update \
 && apt-get install -y --no-install-recommends procps bsdutils \
 && rm -rf /var/lib/apt/lists/*
COPY --from=zellij /usr/local/bin/zellij /usr/local/bin/zellij
COPY --from=build /out/zellij-reaper /usr/local/bin/zellij-reaper
COPY --from=build /out/sleeper /sleeper
COPY test/demo/stage.sh /stage.sh
# The licenses of everything in the image, where a reader looks for them.
COPY --from=zellij /zellij-LICENSE.md /usr/share/licenses/zellij/LICENSE.md
COPY LICENSE /usr/share/licenses/zellij-reaper/LICENSE
COPY docs/THIRD_PARTY_NOTICES.md /usr/share/licenses/zellij-reaper/THIRD_PARTY_NOTICES.md
ENV HOME=/root SHELL=/bin/bash TERM=xterm-256color LANG=C.UTF-8
# The sessions take about a minute to stage; then the dashboard opens, or a
# shell with --shell.
ENTRYPOINT ["/stage.sh"]
