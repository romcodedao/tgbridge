# tgbridge

Drive real Claude Code sessions from a private Telegram chat: one topic per session, live streaming with thinking, and permission prompts as buttons.

It runs the `claude` CLI already installed on your machine, as a `systemd --user` service. Only your Telegram account can use it. It long-polls Telegram, so it needs no open port.

![tgbridge architecture: Telegram updates reach the tgbridge daemon by long polling; it runs one claude -p session per topic and keeps its state](docs/architecture.svg)

<sub>Diagram source: [`docs/architecture.html`](docs/architecture.html)</sub>

## Requirements

- Linux with systemd (for the service). macOS builds work, but there's no launchd unit: run `tgbridge serve` yourself.
- [Claude Code](https://docs.claude.com/en/docs/claude-code) installed and logged in (`claude` runs in a terminal).
- A Telegram account.
- To build from source: Go 1.27+, `git` and `make`.

## Install

### 1. Create the bot (phone)

In [@BotFather](https://t.me/BotFather) send `/newbot` and copy the token. Then in the BotFather Mini App → your bot → **Threads Settings**, turn **Threaded Mode** on and leave "Disallow users to create new threads" off. Open your bot and press **Start**.

### 2. Save the token (machine)

```bash
mkdir -p ~/.config/tgbridge && chmod 700 ~/.config/tgbridge
read -rsp "token: " T && printf '%s' "$T" > ~/.config/tgbridge/token && chmod 600 ~/.config/tgbridge/token && unset T && echo saved
```

Expected: `token: saved`

### 3. Install (machine)

**From source** (builds `~/.local/bin/tgbridge`, creates the config and installs the systemd unit):

```bash
git clone https://github.com/romcodedao/tgbridge && cd tgbridge
make setup
```

Expected: `built …`, `created ~/.config/tgbridge/config.yaml`, then `owner_id not set. Run: …/tgbridge whoami …`. The service isn't started yet.

**Or from a release binary** (pick your OS and architecture on the [releases page](https://github.com/romcodedao/tgbridge/releases)):

```bash
mkdir -p ~/.local/bin ~/.config/tgbridge ~/.config/systemd/user && cd "$(mktemp -d)"
curl -fsSLO https://github.com/romcodedao/tgbridge/releases/download/vX.Y.Z/tgbridge_X.Y.Z_linux_amd64.tar.gz
tar xzf tgbridge_*.tar.gz
install -m 755 tgbridge ~/.local/bin/tgbridge
install -m 600 config.example.yaml ~/.config/tgbridge/config.yaml
cp deploy/tgbridge.service ~/.config/systemd/user/ && systemctl --user daemon-reload
```

### 4. Set your user ID, then start (machine and phone)

```bash
~/.local/bin/tgbridge whoami
```

Expected: `Send any message to @yourbot now, in a private chat…`. Send the bot any message from your phone, in its private chat. It prints `Message from <name> (@<username>), user ID 123456789. Check this is you…` and the `owner_id:` line to use. Check the name: that account will control Claude on this machine. If it says `Conflict`, the service is already running: `systemctl --user stop tgbridge`, then try again.

Put that number in `~/.config/tgbridge/config.yaml` (replace `123456789` with yours):

```bash
sed -i 's/^owner_id: 0 .*/owner_id: 123456789/' ~/.config/tgbridge/config.yaml && grep '^owner_id' ~/.config/tgbridge/config.yaml
```

Then start the service. From source: `make setup` again. From a release binary:

```bash
systemctl --user enable --now tgbridge && sleep 3 && systemctl --user is-active tgbridge
```

Expected: `active`. If it says `activating` or `failed`, `journalctl --user -u tgbridge -n 50` shows why. Now send any message to the bot: a new topic opens with your Claude session.

To keep it running after you log out: `sudo loginctl enable-linger $USER`.

## Configure

Everything lives in `~/.config/tgbridge/config.yaml` ([example](config.example.yaml)). The settings you're most likely to change:

| Setting | Default | What it does |
|---|---|---|
| `owner_id` | *(required)* | The only Telegram account the bot answers |
| `default_cwd` | `~` | Folder new sessions start in (`/new <path> <task>` overrides it) |
| `model` | Claude Code's default | `claude --model` for Telegram sessions |
| `engine.permission_mode` | `ask` | `ask`, `auto` or `full`: how much Claude may do without asking |
| `orca_enabled` | `false` | Optional Orca integration (see below) |

All settings: [docs/configuration.md](docs/configuration.md).

## Security model

- **One owner.** Only `owner_id` can use the bot. Groups are left, other accounts are ignored, and messages are rate-limited.
- **You approve actions.** In `ask` mode, anything beyond reading files asks you with buttons. Each prompt has a single-use nonce and expires.
- **Secrets stay out of reach.** The Read tool is always denied SSH keys, `.env` files, cloud and Git credentials, and tgbridge's own token and state. The token is removed from the daemon's environment, so Claude never inherits it.
- **No inbound port.** Telegram is reached by outbound long polling. The local socket is mode 600.
- **Audit log** of every message (length only) and approval decision.

The deny list blocks the Read tool, not every shell command: that's why `ask` mode asks before any other Bash command. Details and limits: [docs/security.md](docs/security.md). To report a vulnerability: [SECURITY.md](SECURITY.md).

## Limits

- One owner, private chat only. Telegram's Threaded Mode must be on.
- One machine per bot token: two copies polling the same token conflict (`409 Conflict`).
- Files up to 20 MB (the Telegram bot limit). Voice, video, audio and stickers aren't supported.
- Long conversations are kept small: a warning at 80k tokens, an automatic fresh start with a summary at 150k, at most 3 Claude processes at once. All configurable.
- Claude runs as your user with your Claude Code setup. In `auto` and `full` modes, Claude Code's own rules decide what runs without asking.

## Optional: Orca integration

If you use the Orca multi-agent IDE, set `orca_enabled: true`. `/sessions` then lists Orca agents, `/status` shows their last messages, and you can hand a running Orca Claude session off to Telegram (run `make hooks` once). Without it, tgbridge doesn't need or call Orca. See [Using the bot](docs/usage.md#optional-orca-integration).

## Day to day

From the checkout (or add the optional alias `alias tgb='make -s -C /path/to/tgbridge'` and use `tgb <command>` anywhere):

| I want to… | Run |
|---|---|
| Check everything | `make doctor` |
| Check it's running | `make status` |
| Watch the log live | `make logs` |
| Restart / stop / start | `make restart` · `make stop` · `make start` |
| Update to the latest `main` | `make update` |
| See all commands | `make` |

In Telegram: `/help` lists the commands. `/mode ask|auto|full` sets how much Claude may do without asking. `/stop` interrupts. `/exit` ends a session.

## Docs

- [Using the bot](docs/usage.md): topics, photos and files, commands, permission modes, context limits, Orca
- [Operations](docs/operations.md): every `make` command, files, troubleshooting
- [Configuration](docs/configuration.md): every setting in `config.yaml`
- [Upgrading and moving to another machine](docs/migration.md): read the upgrade notes before updating
- [Security](docs/security.md)
- [Development](docs/development.md): tests, code layout, releases

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and the [Code of Conduct](CODE_OF_CONDUCT.md). Licensed under the [MIT License](LICENSE).
