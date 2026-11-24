# Contributing

Thanks for helping. Bug reports, fixes and small focused features are welcome. For a larger change, open an issue first so we can agree on the approach.

## Set up

You need Go 1.27+ and Linux or macOS. Claude Code, Telegram and Orca are **not** needed to build or test: the tests use a fake `claude` script and a fake Telegram server.

```bash
git clone https://github.com/romcodedao/tgbridge && cd tgbridge
go build ./...
go test -race ./...
```

Code layout and test notes: [docs/development.md](docs/development.md).

## Before you open a pull request

- `go vet ./...`, `go test -race ./...` and [golangci-lint](https://golangci-lint.run/) pass (CI runs all three).
- New behaviour has a test. A bug fix has a test that failed before the fix.
- Keep the change small and on one topic. Don't add dependencies for a few lines of code.
- Commit messages and the PR title follow [Conventional Commits](https://www.conventionalcommits.org/): `feat: …`, `fix: …`, `docs: …`, `chore: …`.
- Never commit a bot token, a chat or user ID, or paths from your own machine. Use placeholders like `123456789`.

## Security issues

Don't report them in a public issue. See [SECURITY.md](SECURITY.md).

By contributing you agree that your contribution is licensed under the [MIT License](LICENSE) and that you follow the [Code of Conduct](CODE_OF_CONDUCT.md).
