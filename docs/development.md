# Development

## Tests

```bash
make test                                                  # unit tests with the race detector
TGBRIDGE_E2E_OWNER=<your id> go test -tags e2e ./e2e -v    # real bot, real claude (haiku), Orca if installed
```

The unit tests need no Claude, Telegram or Orca. The e2e tests post messages to your bot chat and, if Orca is installed, open (then close) a throwaway Orca terminal. They need `~/.config/tgbridge/token`.

CI runs `go vet`, `go test -race` and [golangci-lint](https://golangci-lint.run/) (config: `.golangci.yml`) on every PR. To lint locally: `golangci-lint run ./...`.

## Layout

| Package | Role |
|---|---|
| `cmd/tgbridge` | CLI: `serve`, `mcp`, `hook <event>`, `install`, `uninstall`, `version` |
| `internal/telegram` | Minimal Bot API client (raw HTTP; rich messages, drafts, topics) and the long-poll update source |
| `internal/engine` | Runs `claude -p` stream-json processes, one per topic; idle reaping, slot limit, queueing |
| `internal/streamjson` | Parses Claude's stream-json events into turns |
| `internal/render` | Turns into rich drafts and messages, the HTML fallback, approval text |
| `internal/bot` | Routing, commands, approvals, context budget, engine settings |
| `internal/handoff` | Optional Orca session handoff: hooks, transcript tailing, input into the terminal |
| `internal/mcp`, `internal/ipc`, `internal/hookcmd` | The MCP server and hook command Claude runs, and their unix-socket link to the daemon |
| `internal/store` | `state.json` and `audit.log` |
| `internal/orca` | Orca CLI wrapper (used only with `orca_enabled`) |
| `internal/install` | Adds and removes hooks and the MCP server in `~/.claude/settings.json` |

## Release

A Conventional Commit on `main` (`feat:`, `fix:`, ...) makes [release-please](https://github.com/googleapis/release-please) open or update a release PR. Merging it creates the tag, a **draft** GitHub release and `CHANGELOG.md`. In the same workflow run, the tests run again, then [GoReleaser](https://goreleaser.com/) (`.goreleaser.yaml`) builds linux and darwin, amd64 and arm64 archives, uploads them with `checksums.txt` to the draft and publishes it; their build provenance is attested. If that job fails, the release stays a draft: fix the cause on `main` if needed, then re-run the failed `goreleaser` job from the workflow run (Actions → Release → Re-run failed jobs). Verify a download with `gh attestation verify <file> -R romcodedao/tgbridge`.

The release PR is opened with `GITHUB_TOKEN`, so CI does not run on it. Try the build locally with `goreleaser release --snapshot --clean`.

To update a source install: `make update` in the checkout.
