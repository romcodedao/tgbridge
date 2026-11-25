# Changelog

## 0.1.0 (2026-11-25)

First public release.

### Features

* Drive Claude Code sessions from Telegram: one forum topic per session, streaming replies, permission prompts as buttons, `/new`, `/stop`, `/sessions`, `/status` and more.
* Send photos and files to Claude from Telegram, with a retention policy for uploads.
* One-command install (`install.sh`, https-only with SHA-256 check) and `go install` support.
* `tgbridge init` setup wizard: checks Claude Code is installed and logged in, validates the bot token, confirms the owner account and installs the background service, then prints a summary with fixes for anything that fails.
* `tgbridge doctor`, `tgbridge service install|uninstall|status` (systemd on Linux, launchd on macOS) and `tgbridge uninstall` (`--dry-run`, `--purge`).
* Optional Orca handoff integration, off by default (`orca_enabled: true`).

### Releases

* Linux and macOS binaries (amd64, arm64) with SHA-256 checksums and GitHub build provenance attestations (`gh attestation verify`).
