# Development

## Tests

```bash
make test                                                  # unit tests with the race detector
TGBRIDGE_E2E_OWNER=<your id> go test -tags e2e ./e2e -v    # real bot, real claude (haiku), real Orca
```

The e2e tests post messages to your bot chat and open (then close) a throwaway Orca terminal. They need `~/.config/tgbridge/token`.

## Layout

| Package | Role |
|---|---|
| `cmd/tgbridge` | CLI: `serve`, `mcp`, `hook <event>`, `install`, `uninstall`, `version` |
| `internal/telegram` | Minimal Bot API client (raw HTTP; rich messages, drafts, topics) and the long-poll update source |
| `internal/engine` | Runs `claude -p` stream-json processes, one per topic; idle reaping, slot limit, queueing |
| `internal/streamjson` | Parses Claude's stream-json events into turns |
| `internal/render` | Turns into rich drafts and messages, the HTML fallback, approval text |
| `internal/bot` | Routing, commands, approvals, context budget, engine settings |
| `internal/handoff` | Orca session handoff: hooks, transcript tailing, input into the terminal |
| `internal/mcp`, `internal/ipc`, `internal/hookcmd` | The MCP server and hook command Claude runs, and their unix-socket link to the daemon |
| `internal/store` | `state.json` and `audit.log` |
| `internal/orca` | Orca CLI wrapper |
| `internal/install` | Adds and removes hooks and the MCP server in `~/.claude/settings.json` |

Design history: [`docs/plans/`](plans/).

## Release

Merge to `main`, then run `tgb update` on the machine.
