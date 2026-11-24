# Operations

**How it runs.** tgbridge is one binary, `~/.local/bin/tgbridge`, running as a **systemd user service** named `tgbridge`. Nothing runs as root. It long-polls Telegram, so it needs no open port. The Claude sessions it starts are its child processes.

**Running while logged out.** For it to keep running after you log out, *linger* must be on for your user (`tgb doctor` checks this).

## The `tgb` shortcut

All day-to-day commands are `make` targets in the repo's [`Makefile`](../Makefile). The alias runs them from any folder:

```bash
alias tgb='make -s -C ~/projects/tgbridge'     # optional, in ~/.bashrc; use the path of your checkout
```

Without the alias, use `make -s -C <checkout> <target>`, or `make <target>` inside the repo. The examples below use `tgb`.

| Command | What it does | Raw equivalent |
|---|---|---|
| `tgb` | List the commands | |
| `tgb status` | Running? since when, memory, last log lines | `systemctl --user status tgbridge` |
| `tgb doctor` | Check service, linger, binary, config, token mode, claude, orca, socket, Threaded Mode, hooks | |
| `tgb logs` | Follow the log live (Ctrl+C quits) | `journalctl --user -u tgbridge -f` |
| `tgb logs-recent` | Last 100 log lines | `journalctl --user -u tgbridge -n 100` |
| `tgb errors` | Warnings and errors from the last 24 h | |
| `tgb start` / `stop` / `restart` | Control the service | `systemctl --user start\|stop\|restart tgbridge` |
| `tgb version` | Installed version (git commit) | `~/.local/bin/tgbridge version` |
| `tgb build` | Build the binary from the checkout | `go build -o ~/.local/bin/tgbridge ./cmd/tgbridge` |
| `tgb deploy` | Build, then restart | |
| `tgb update` | `git pull --ff-only`, run tests, build, restart | |
| `tgb test` | Unit tests | `go test -race ./...` |
| `tgb setup` | First install: config folder, systemd unit, linger; starts the service once `owner_id` is set (run it again after `tgbridge whoami`) | |
| `tgb hooks` / `unhooks` | Optional: add or remove the Claude Code hooks (Orca handoff) and the MCP server (`telegram_send`) in `~/.claude/settings.json` (a backup is kept) | `tgbridge install` / `uninstall` |
| `tgb backup` | Archive config, token and state to `~/tgbridge-backup-<date>.tgz` (mode 600) | |
| `tgb uninstall` | Remove the service and hooks; config and state are kept | |

**What a stop or restart does to sessions.** Running Claude turns end, but conversations are kept. The next message in a topic resumes its conversation. Unanswered permission prompts are marked expired.

## Files

| Path | What it is | In backup? |
|---|---|---|
| `~/.local/bin/tgbridge` | The binary | no (rebuilt) |
| `~/.config/tgbridge/config.yaml` | Settings | yes |
| `~/.config/tgbridge/token` | Bot token (mode 600) | yes |
| `~/.config/systemd/user/tgbridge.service` | Service unit (copied from `deploy/`) | no (`tgb setup`) |
| `~/.local/state/tgbridge/state.json` | Topics ↔ Claude sessions, handoffs | yes |
| `~/.local/state/tgbridge/audit.log` | Every message and approval decision (JSON lines) | yes |
| `~/.local/state/tgbridge/engine-*.json` | Generated Claude settings and MCP configs | regenerated |
| `$XDG_RUNTIME_DIR/tgbridge.sock` | Local socket (only while running) | no |
| `~/.claude/settings.json` | Holds the handoff hooks after `tgb hooks` | no |
| `~/.claude/projects/…` | Claude's own conversation transcripts (needed to resume) | Claude's data |

## Troubleshooting

| Symptom | Check / fix |
|---|---|
| Bot doesn't answer | `tgb doctor`, then `tgb logs-recent`. Make sure you write from the `owner_id` account. |
| `getUpdates: request failed` now and then | A network blip; the poller retries on its own. Only a problem if it repeats nonstop. |
| `getUpdates … 409 Conflict` | Two copies are running with the same token (for example, the old machine after a move). Stop one. |
| `Threaded Mode is off` warning | Turn on BotFather → Mini App → Threads Settings → Threaded Mode, then `tgb restart` |
| Every command gets denied / "unknown session" | Fixed in v1 (permissions are tied to the topic). Make sure you run the latest version: `tgb update`. |
| `⚠️ The Claude session crashed` | Send your message again; the conversation resumes. Repeated crashes: `tgb errors`. Check memory with `free -h`. |
| Claude says it's not logged in | Run `claude` once in a terminal on the machine and log in, then `tgb restart`. |
| `/sessions` shows `orca:` errors | Orca isn't running, or `orca_cli` is wrong (only with `orca_enabled: true`). `tgb doctor` shows the path it uses. |
| Handoff does nothing | Check `orca_enabled: true`, run `tgb hooks`, then start a **new** Orca Claude session. Existing ones don't load the hooks. |
| Service won't start after a config edit | `tgb logs-recent` shows the config error (for example `owner_id is required`). |

## Limiting memory (optional)

To cap tgbridge and every Claude it starts:

```bash
mkdir -p ~/.config/systemd/user/tgbridge.service.d && printf '[Service]\nMemoryMax=3G\nMemorySwapMax=0\n' > ~/.config/systemd/user/tgbridge.service.d/memory.conf && systemctl --user daemon-reload && tgb restart
```

Expected: the restart prints the `serving @…` log line.
