# tgbridge operations. Run from anywhere with:  make -s -C <repo> <target>
# Suggested alias (see docs/operations.md):     alias tgb='make -s -C ~/projects/tgbridge'

SHELL   := /bin/bash
BIN     ?= $(HOME)/.local/bin/tgbridge
UNIT    := tgbridge
UNITDIR := $(HOME)/.config/systemd/user
CONF    := $(HOME)/.config/tgbridge
STATE   := $(HOME)/.local/state/tgbridge
VERSION := $(shell git -C $(CURDIR) describe --always --dirty 2>/dev/null || echo dev)
SC      := systemctl --user
JC      := journalctl --user -u $(UNIT)

.DEFAULT_GOAL := help
.PHONY: help status logs logs-recent errors start stop restart build test deploy update setup hooks unhooks doctor backup uninstall version

help: ## list commands
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | sed 's/:.*## /\t/' | awk -F'\t' '{printf "  %-12s %s\n", $$1, $$2}'

status: ## is it running? version, uptime, memory, last log lines
	@$(SC) status $(UNIT) --no-pager -n 5 | sed -n '1,12p'

logs: ## follow the log live (Ctrl+C to quit)
	@$(JC) -f -n 20

logs-recent: ## last 100 log lines
	@$(JC) -n 100 --no-pager

errors: ## warnings and errors from the last 24 hours
	@$(JC) --since -24h --no-pager | grep -iE 'error|warn|fail|rejected|panic' || echo "no errors in the last 24h"

start: ## start the service
	@$(SC) start $(UNIT) && $(SC) is-active $(UNIT)

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

setup: build ## first-time install: config folder, systemd unit, service on
	@mkdir -p $(CONF) $(UNITDIR) && chmod 700 $(CONF)
	@test -f $(CONF)/config.yaml || { cp config.example.yaml $(CONF)/config.yaml && chmod 600 $(CONF)/config.yaml && echo "created $(CONF)/config.yaml: set owner_id"; }
	@test -f $(CONF)/token || echo "missing $(CONF)/token: see README step 2"
	@cp deploy/tgbridge.service $(UNITDIR)/ && $(SC) daemon-reload && $(SC) enable --now $(UNIT)
	@loginctl enable-linger $(USER) 2>/dev/null || echo "run: sudo loginctl enable-linger $(USER)  (keeps it running when you log out)"
	@$(SC) is-active $(UNIT)

hooks: ## enable Orca handoff: hooks + MCP server in ~/.claude (backup kept)
	@$(BIN) install

unhooks: ## remove the handoff hooks + MCP server from ~/.claude
	@$(BIN) uninstall

doctor: ## check everything tgbridge needs
	@ok(){ printf '  ok    %s\n' "$$1"; }; bad(){ printf '  FAIL  %s\n' "$$1"; }; \
	$(SC) is-active -q $(UNIT) && ok "service running" || bad "service not running (tgb start; tgb logs-recent)"; \
	[ "$$(loginctl show-user $(USER) -p Linger --value 2>/dev/null)" = yes ] && ok "linger on (survives logout)" || bad "linger off: sudo loginctl enable-linger $(USER)"; \
	[ -x $(BIN) ] && ok "binary $$($(BIN) version)" || bad "no binary at $(BIN) (tgb build)"; \
	[ -f $(CONF)/config.yaml ] && ok "config $(CONF)/config.yaml" || bad "no config (tgb setup)"; \
	[ "$$(stat -c %a $(CONF)/token 2>/dev/null)" = 600 ] && ok "token file mode 600" || bad "token missing or not chmod 600"; \
	command -v claude >/dev/null && ok "claude $$(claude --version 2>/dev/null | head -1)" || bad "claude not on PATH"; \
	O=$$(grep -E '^orca_cli:' $(CONF)/config.yaml 2>/dev/null | awk '{print $$2}'); O=$${O:-$$HOME/.config/orca/cli/bin/orca}; O=$${O/#\~/$$HOME}; \
	[ -x "$$O" ] && ok "orca cli $$O" || bad "orca cli not found ($$O); /sessions and handoff need it"; \
	[ -S "$${XDG_RUNTIME_DIR:-/run/user/$$(id -u)}/tgbridge.sock" ] && ok "socket present" || bad "socket missing (service down?)"; \
	$(JC) -n 50 --no-pager | grep -q 'Threaded Mode is off' && bad "Threaded Mode off in BotFather" || ok "no Threaded Mode warning"; \
	grep -q 'tgbridge hook' $(HOME)/.claude/settings.json 2>/dev/null && ok "handoff hooks installed" || echo "  info  handoff hooks not installed (tgb hooks)"

backup: ## archive config, token and state to ~/tgbridge-backup-<date>.tgz (for migration)
	@umask 077 && tar czf $(HOME)/tgbridge-backup-$$(date +%Y%m%d-%H%M).tgz -C $(HOME) .config/tgbridge .local/state/tgbridge && ls -l $(HOME)/tgbridge-backup-*.tgz | tail -1

uninstall: ## stop and remove the service and hooks (keeps config and state)
	@$(SC) disable --now $(UNIT) 2>/dev/null; rm -f $(UNITDIR)/tgbridge.service; $(SC) daemon-reload
	@$(BIN) uninstall 2>/dev/null || true
	@echo "removed service and hooks; kept $(CONF) and $(STATE)"
