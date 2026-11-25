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
| `cmd/tgbridge` | CLI: `init`, `doctor`, `service`, `hooks`, `serve`, `whoami`, `mcp`, `hook <event>`, `uninstall`, `version` (`install` is an alias for `hooks install`) |
| `internal/telegram` | Minimal Bot API client (raw HTTP; rich messages, drafts, topics) and the long-poll update source |
| `internal/engine` | Runs `claude -p` stream-json processes, one per topic; idle reaping, slot limit, queueing |
| `internal/streamjson` | Parses Claude's stream-json events into turns |
| `internal/render` | Turns into rich drafts and messages, the HTML fallback, approval text |
| `internal/bot` | Routing, commands, approvals, context budget, engine settings |
| `internal/handoff` | Optional Orca session handoff: hooks, transcript tailing, input into the terminal |
| `internal/mcp`, `internal/ipc`, `internal/hookcmd` | The MCP server and hook command Claude runs, and their unix-socket link to the daemon |
| `internal/store` | `state.json` and `audit.log` |
| `internal/orca` | Orca CLI wrapper (used only with `orca_enabled`) |
| `internal/setup` | The pieces of `init`, `doctor`, `service` (systemd and launchd) and `uninstall`; tested with fake runners and a fake Telegram |
| `internal/install` | Adds and removes hooks and the MCP server in `~/.claude/settings.json` |

## Release

A Conventional Commit on `main` (`feat:`, `fix:`, ...) makes [release-please](https://github.com/googleapis/release-please) open or update a release PR. Merging it creates the tag, a **draft** GitHub release and `CHANGELOG.md`. In the same workflow run, the tests run again, then [GoReleaser](https://goreleaser.com/) (`.goreleaser.yaml`) builds linux and darwin, amd64 and arm64 archives and uploads them with `checksums.txt` to the draft. The workflow then attests their build provenance and publishes the release. Verify a download with `gh attestation verify <file> -R romcodedao/tgbridge`.

If that job fails, the release stays a draft:

- **Transient failure** (network, GitHub outage): re-run the failed job (Actions → Release → Re-run failed jobs). A re-run builds the same tag with that tag's workflow, so it can't pick up fixes.
- **Code or workflow failure**: land the fix on `main` as a `fix:` commit; its release PR releases the next version. Once that next version is published, delete the stale draft and its tag: `gh release delete vX.Y.Z --cleanup-tag`.

The release PR is opened with `GITHUB_TOKEN`, so its CI run waits as `action_required`: a maintainer approves it in the PR's checks (or an admin bypasses the ruleset) before merging. To run release-please again by hand: Actions → Release → Run workflow. Try the build locally with `goreleaser release --snapshot --clean`.

`install.sh` has a test in `install_test.go` (`go test .`): it runs the script against a local fake release, including a bad checksum and no terminal. CI also runs `shellcheck install.sh`.

To update a source install: `make update` in the checkout.
