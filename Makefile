FOUNDRY_GO ?= go

.PHONY: build test start help

build:
	@mkdir -p bin
	$(FOUNDRY_GO) build -o bin/foundry ./cmd/foundry

test:
	$(FOUNDRY_GO) test ./...

start: build
	./bin/foundry

help:
	@echo "FOUNDRY_DATA_DIR  Controller database, workspaces, and lock (default: user config dir/foundry)"
	@echo "FOUNDRY_ADDR      Loopback listen address (default: 127.0.0.1:4317; only 127.0.0.1 is accepted)"
	@echo "FOUNDRY_WEB_DIR   Compiled SPA directory (default: web/dist when present; empty disables static UI)"
	@echo "FOUNDRY_DOCKER    Docker executable name or path (default: docker)"
	@echo "FOUNDRY_GO        Go command used by this Makefile (default: go)"
