# tgbridge

Drive real Claude Code sessions from a private Telegram chat: one topic per session, live streaming with thinking, permission prompts as buttons, and handoff of running Orca sessions to your phone.

It runs the installed `claude` CLI on this machine as a `systemd --user` service. Only your Telegram account can use it.

![tgbridge architecture: Telegram updates reach the tgbridge daemon by long polling; it runs one claude -p session per topic, keeps its state, and hands off Orca Claude sessions through hooks, transcripts and the Orca CLI](docs/architecture.svg)

<sub>Diagram source: [`docs/architecture.html`](docs/architecture.html)</sub>

## Get started

You need: Linux with systemd, Go 1.27+, `make`, Claude Code installed and logged in, and (for `/sessions` and handoff) Orca.

1. **Create the bot (phone).** In @BotFather send `/newbot` and copy the token. In the BotFather Mini App → your bot → **Threads Settings**, turn **Threaded Mode** on and leave "Disallow users to create new threads" off. Open your bot and press **Start**.

2. **Get the code and save the token (machine).**

   ```bash
   git clone https://github.com/romcodedao/tgbridge ~/src/tgbridge && cd ~/src/tgbridge
   mkdir -p ~/.config/tgbridge && read -rsp "token: " T && printf '%s' "$T" > ~/.config/tgbridge/token && chmod 600 ~/.config/tgbridge/token && unset T && echo saved
   ```

   Expected: `token: saved`

3. **Install and start (machine).**

   ```bash
   make setup
   ```

   Expected: `built …`, then `created ~/.config/tgbridge/config.yaml: set owner_id`, then `active`.
   Send `/whoami` to the bot, put the number in `owner_id` in `~/.config/tgbridge/config.yaml`, then run `make restart`.

4. **Add the shortcut (machine), so every command below works from any folder.**

   ```bash
   echo "alias tgb='make -s -C ~/src/tgbridge'" >> ~/.bashrc && source ~/.bashrc && tgb doctor
   ```

   Expected: `ok` lines. `info  handoff hooks not installed` is fine until step 5.

5. **Optional: hand off Orca sessions (machine).** `tgb hooks`, then start new Orca Claude sessions.

Then send any message to the bot. A new topic opens with your Claude session.

## Day to day

| I want to… | Run |
|---|---|
| Check it's running | `tgb status` |
| Check everything (service, config, claude, orca) | `tgb doctor` |
| Watch the log live | `tgb logs` |
| See recent problems | `tgb errors` |
| Restart / stop / start | `tgb restart` · `tgb stop` · `tgb start` |
| Update to the latest `main` | `tgb update` |
| Rebuild from local changes | `tgb deploy` |
| Back up config and state | `tgb backup` |
| See all commands | `tgb` |

In Telegram: `/help` lists the commands. `/mode ask|auto|full` sets how much Claude may do without asking. `/stop` interrupts. `/exit` ends a session.

## Docs

- [Using the bot](docs/usage.md): topics, commands, permission modes, handoff, context limits
- [Operations](docs/operations.md): every `tgb` command, the raw commands behind it, files, troubleshooting
- [Configuration](docs/configuration.md): every setting in `config.yaml`
- [Moving to another machine](docs/migration.md)
- [Security](docs/security.md)
- [Development](docs/development.md): tests and code layout
- Design history: [`docs/plans/`](docs/plans/)
