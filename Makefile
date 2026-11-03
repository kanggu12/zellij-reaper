# Everyday tasks. CI runs `make check` and `make integration`.

BIN      := zellij-reaper
PREFIX   ?= $(HOME)/.local
STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2

.PHONY: build install check fmt vet lint test integration demo snapshot clean

## build: the binary, in this directory
build:
	CGO_ENABLED=0 go build -trimpath -o $(BIN) ./cmd/zellij-reaper

## install: copy the binary to $(PREFIX)/bin and set up the timer
install: build
	install -Dm0755 $(BIN) $(PREFIX)/bin/$(BIN)
	$(PREFIX)/bin/$(BIN) install

## check: everything CI checks before the integration tests
check: fmt vet lint test

fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not gofmt'ed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

lint:
	go run $(STATICCHECK) ./...
	shellcheck install.sh test/integration/*.sh test/demo/*.sh

test:
	go test -race -count=1 ./...

## integration: real zellij releases in throwaway containers (needs Docker);
## ZELLIJ_VERSIONS="0.45.1" picks releases. Not ZELLIJ: zellij sets that
## variable inside every session, and make would take it for a version.
integration:
	test/integration/run.sh $(ZELLIJ_VERSIONS)

## demo: the dashboard on demo sessions in a throwaway container (DEMO_SHELL=1 for a shell)
demo:
	test/demo/run.sh $(if $(DEMO_SHELL),--shell,)

## snapshot: a local release build in dist/, as the release workflow makes it
snapshot:
	go run github.com/goreleaser/goreleaser/v2@v2.19.0 release --snapshot --clean

clean:
	rm -rf $(BIN) dist test/integration/.cache
