# Install

The short path is in the [README](../README.md#install). This page has the options and the manual routes.

## The installer

```bash
curl -fsSL https://raw.githubusercontent.com/romcodedao/tgbridge/main/install.sh | sh
```

It finds the latest release, downloads the archive for your OS and architecture, checks its sha256 against `checksums.txt`, installs `tgbridge` and runs `tgbridge init`.

| Environment variable | Default | What it does |
|---|---|---|
| `TGBRIDGE_VERSION` | latest release | Release tag to install, for example `v0.1.0` |
| `TGBRIDGE_INSTALL_DIR` | `~/.local/bin` | Where the binary goes |
| `TGBRIDGE_NO_INIT=1` | off | Install only; skip the wizard |
| `TGBRIDGE_BASE_URL` | `https://github.com/romcodedao/tgbridge/releases` | Releases URL |

Set them on `sh`, not on `curl`:

```bash
curl -fsSL https://raw.githubusercontent.com/romcodedao/tgbridge/main/install.sh | TGBRIDGE_VERSION=v0.1.0 TGBRIDGE_NO_INIT=1 sh
```

`install.sh --help` prints the same list. If the terminal can't be opened (a script, CI), the installer stops after installing and prints `run: tgbridge init`. If the install folder isn't on your PATH, it prints the `export PATH=…` line to add.

## What `tgbridge init` does

| Step | What happens |
|---|---|
| Claude Code | Finds `claude` and checks you're logged in. A problem is reported, not fatal. |
| Config | Creates `~/.config/tgbridge/config.yaml` (its folder is mode 700) from the example, if missing. |
| Token | Checks the saved token with Telegram. If it's missing or rejected, asks for one (hidden input, three tries) and saves it mode 600. A working `TGBRIDGE_TOKEN` is saved to the token file when that file is missing, because the service doesn't see your shell's environment. |
| Owner | If `owner_id` is `0`, waits for a private message to the bot, shows the sender and asks `Is this you? [Y/n]`. A running service is stopped first, because it would hold the bot's updates (`409 Conflict`). |
| Service | Installs and starts the systemd user unit or launchd agent. A custom `TGBRIDGE_CONFIG` is written into it. If the existing file was edited by hand, it's copied to `<file>.bak` first. `--no-service` skips this, and so does a binary in the temp folder (`go run`). |
| Summary | Runs `tgbridge doctor`. |

Run `tgbridge init` again at any time: finished steps ask nothing and change nothing, except that the service file is rewritten with the current binary path and the service is restarted. The restart ends any Claude turn that is running, so re-run it when the bot is idle.

## Release tarball by hand

Pick your file on the [releases page](https://github.com/romcodedao/tgbridge/releases). Replace `X.Y.Z`, the OS (`linux` or `darwin`) and the architecture (`amd64` or `arm64`).

```bash
cd "$(mktemp -d)"
V=X.Y.Z; F=tgbridge_${V}_linux_amd64.tar.gz
curl -fsSLO https://github.com/romcodedao/tgbridge/releases/download/v$V/$F
curl -fsSLO https://github.com/romcodedao/tgbridge/releases/download/v$V/checksums.txt
grep " $F\$" checksums.txt | sha256sum -c -
```

Expected: `tgbridge_X.Y.Z_linux_amd64.tar.gz: OK`. On macOS use `shasum -a 256 -c -` instead of `sha256sum -c -`.

To check where the file was built (needs the [GitHub CLI](https://cli.github.com/)):

```bash
gh attestation verify "$F" -R romcodedao/tgbridge
```

Expected: `Verification succeeded!`. Then install and run the wizard:

```bash
mkdir -p ~/.local/bin && tar xzf "$F" tgbridge && install -m 755 tgbridge ~/.local/bin/tgbridge
~/.local/bin/tgbridge init
```

## From source

Needs Go 1.27+, `git` and `make`.

```bash
git clone https://github.com/romcodedao/tgbridge && cd tgbridge
make setup
```

Expected: `built ~/.local/bin/tgbridge …`, then the `init` wizard. `make setup` is the same as `make build` and `tgbridge init`, so run it again after `git pull`: it rewrites the service file with the binary's absolute path.

## With `go install`

```bash
go install github.com/romcodedao/tgbridge/cmd/tgbridge@latest
tgbridge init
```

The binary lands in `$(go env GOPATH)/bin`. Add it to your PATH if `tgbridge` isn't found. `tgbridge version` shows the module version.

## macOS

- The service is a launchd agent: `~/Library/LaunchAgents/io.github.romcodedao.tgbridge.plist`, label `io.github.romcodedao.tgbridge`. It starts when you log in.
- The log is `~/Library/Logs/tgbridge.log`: `tail -f ~/Library/Logs/tgbridge.log`.
- `tgbridge service install` re-loads the agent, so use it to apply a config change.
- The `make` targets that call `systemctl` or `journalctl` (`logs`, `logs-recent`, `errors`, `start`, `stop`, `restart`) are Linux-only. `make status`, `make doctor` and `make setup` work everywhere.

## Windows (WSL2)

There is no Windows binary. In a WSL2 distribution (Ubuntu, for example) with systemd on:

1. Install and log in to Claude Code inside WSL2, not on Windows.
2. Run the installer inside WSL2.

Without systemd (no `/run/systemd/system`), `init` skips the service and prints `No service: systemd isn't running …` with the fix, `tgbridge doctor` shows it as a `warn  service` line, and `tgbridge service install` exits with the same message. To turn systemd on, run inside WSL2:

```bash
printf '[boot]\nsystemd=true\n' | sudo tee -a /etc/wsl.conf
```

then in Windows PowerShell `wsl --shutdown`, reopen the WSL2 terminal, and run `tgbridge service install`. Expected: `installed and started: /home/you/.config/systemd/user/tgbridge.service`. Or skip the service and run `tgbridge serve` yourself.

## Uninstall

```bash
tgbridge uninstall
```

It stops and removes the service, and removes tgbridge's hooks and MCP server from `~/.claude/settings.json`. Your config, token, state and binary are kept, and it lists them.

| Flag | What it does |
|---|---|
| `--dry-run` | Print the plan and change nothing |
| `--purge` | Also delete the config, token, state, uploads, the macOS log and `~/.local/bin/tgbridge`. Asks first. |
| `--yes` | With `--purge`: don't ask. Needed when there's no terminal. |

`--purge` deletes only tgbridge's own default folders. A custom `state_dir`, or a path outside your home or without `tgbridge` in it, is skipped and listed under `Kept:`. A binary that `go install` or a package manager put elsewhere is left for you to remove.

To remove only the hooks and the MCP server: `tgbridge hooks uninstall`.

Preview before you purge:

```bash
tgbridge uninstall --purge --dry-run
```

Expected: a `Plan:` list, a `Kept:` list, then `dry run: nothing changed`.
