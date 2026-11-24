# tgbridge operations. Run from anywhere with:  make -s -C <repo> <target>
# Optional alias (see docs/operations.md):      alias tgb='make -s -C /path/to/tgbridge'

SHELL   := /bin/bash
BIN     ?= $(HOME)/.local/bin/tgbridge
UNIT    := tgbridge
VERSION := $(shell git -C $(CURDIR) describe --always --dirty 2>/dev/null || echo dev)
SC      := systemctl --user
JC      := journalctl --user -u $(UNIT)

.DEFAULT_GOAL := help
.PHONY: help status logs logs-recent errors start stop restart build test deploy update setup hooks unhooks doctor backup uninstall version

help: ## list commands
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/' | awk -F'\t' '{printf "  %-12s %s\n", $$1, $$2}'

status: ## is it running? (service status)
	@$(BIN) service status

logs: ## follow the log live (Ctrl+C to quit)
	@$(JC) -f -n 20

logs-recent: ## last 100 log lines
	@$(JC) -n 100 --no-pager

errors: ## warnings and errors from the last 24 hours
	@$(JC) --since -24h --no-pager | grep -iE 'error|warn|fail|rejected|panic' || echo "no errors in the last 24h"

start: ## start the service
	@$(SC) start $(UNIT) && sleep 3 && $(SC) is-active $(UNIT) || { echo "not running: make logs-recent shows why"; exit 1; }

stop: ## stop the service (Claude sessions end; conversations are kept)
	@$(SC) stop $(UNIT) && echo stopped

restart: ## restart the service
	@$(SC) restart $(UNIT) && sleep 2 && $(JC) -n 1 --no-pager

version: ## version of the installed binary
	@$(BIN) version

build: ## build the binary into ~/.local/bin/tgbridge
	@go build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/tgbridge && echo "built $(BIN) $(VERSION)"

test: ## unit tests
	@go test -race ./...

deploy: build restart ## build from this checkout and restart

update: ## pull main, test, build and restart
	@git -C $(CURDIR) pull --ff-only && $(MAKE) -s test && $(MAKE) -s deploy

setup: build ## first-time install: build, then the tgbridge init wizard
	@$(BIN) init

hooks: ## optional: Claude Code hooks + MCP server in ~/.claude for telegram_send and Orca handoff (backup kept)
	@$(BIN) hooks install

unhooks: ## remove the handoff hooks + MCP server from ~/.claude
	@$(BIN) hooks uninstall

doctor: ## check everything tgbridge needs
	@$(BIN) doctor

backup: ## archive config, token and state to ~/tgbridge-backup-<date>.tgz (for migration)
	@umask 077 && tar czf $(HOME)/tgbridge-backup-$$(date +%Y%m%d-%H%M).tgz -C $(HOME) .config/tgbridge .local/state/tgbridge && ls -l $(HOME)/tgbridge-backup-*.tgz | tail -1

uninstall: ## remove the service and hooks (add --purge via the binary to delete config and data)
	@$(BIN) uninstall
