# tgbridge

Drive real Claude Code sessions from a private Telegram chat.

- **One topic per session.** A message in General starts a new Claude session in its own topic; messages in a topic continue it.
- **Live streaming** with Telegram rich drafts: a native thinking block, a tool-call checklist, and a stop button. The final reply keeps the thinking and tool calls collapsed, with code blocks and tables.
- **Permission prompts as buttons:** Allow · Allow for session · Deny.
- **Hand off running Orca sessions.** Say "notify me on Telegram" inside an Orca Claude session (or use `/sessions`). Its output then appears in a topic, and what you type there goes back to the terminal. Say "stop notifying Telegram" (or `/handoff off`) to end.
- **Small contexts.** At 80k tokens you get a warning; at 150k tokens the conversation starts fresh, carrying over a summary.

It runs the installed `claude` binary (`claude -p` stream-json). It doesn't use the API directly or the Agent SDK. Only the owner's Telegram account can use the bot.

## Setup

1. **Create the bot (phone).** In @BotFather send `/newbot` and copy the token. In the BotFather Mini App → your bot → **Threads Settings**, turn **Threaded Mode** on. Leave "Disallow users to create new threads" off. Then open your bot and press **Start**.

2. **Save the token (machine).**

   ```bash
   mkdir -p ~/.config/tgbridge && read -rsp "token: " T && printf '%s' "$T" > ~/.config/tgbridge/token && chmod 600 ~/.config/tgbridge/token && unset T && echo saved
   ```

   Expected: `token: saved`

3. **Build and install (machine).**

   ```bash
   go build -o ~/.local/bin/tgbridge ./cmd/tgbridge
   cp config.example.yaml ~/.config/tgbridge/config.yaml   # then set owner_id
   tgbridge install
   ```

   Expected: `installed: hooks in ~/.claude/settings.json (backup: settings.json.tgbridge.bak) and user-scope MCP server 'tgbridge'`

   Don't know your user ID? Start the service (step 4), send `/whoami` to the bot, put the number in `owner_id`, and restart.

4. **Run as a user service (machine).**

   ```bash
   mkdir -p ~/.config/systemd/user && cp deploy/tgbridge.service ~/.config/systemd/user/
   systemctl --user daemon-reload && systemctl --user enable --now tgbridge
   journalctl --user -u tgbridge -n 5
   ```

   Expected: a log line `tgbridge dev serving @<your_bot> for owner <id>`.

## Commands

| Command | Does |
|---|---|
| *(any message in General)* | New session in a new topic |
| *(message in a topic)* | Continue that session |
| `/new [path] <task>` | New session, optionally in a folder |
| `/sessions` | Telegram sessions and Orca agents, with Hand off buttons |
| `/status <orca-session>` | Last messages of an Orca agent |
| `/stop` | Interrupt the running turn (same as the draft's stop button) |
| `/reset`, `/clear` | Fresh conversation in this topic |
| `/mode ask\|auto\|full` | How much Claude may do without asking, for this topic |
| `/compact` | Shrink this conversation's context |
| `/close`, `/exit` | End this session and close the topic |
| `/handoff off` | Stop forwarding a handed-off Orca session |
| `/whoami` | Your Telegram user ID |

Topics you create yourself in the Telegram app work like `/new`.

## Configuration

See [`config.example.yaml`](config.example.yaml). The main settings:

- `engine.idle_timeout` (15m): how long a Claude process can sit idle before it's closed. The next message resumes it.
- `engine.max_live` (3): the most Claude processes running at once. Conversations are unlimited. When a new one needs a slot, the least recently used idle process is closed. If every process is busy, the message waits.
- `engine.max_age` (24h): a topic quiet for longer than this starts fresh, carrying a summary.
- `context.soft_limit` / `hard_limit` (80k / 150k tokens).

## Permission modes

| Mode | Claude flag | What asks you |
|---|---|---|
| `ask` (default) | none | Everything except read-only tools (Read, Grep, Glob, `git status`, `ls`, read-only `orca` commands) |
| `auto` | `--permission-mode auto` | Only what Claude Code's auto-mode classifier considers risky |
| `full` | `--permission-mode bypassPermissions` | Nothing |

Set the default with `engine.permission_mode` in config, or change one topic with `/mode auto`. The topic's next message resumes the same conversation in the new mode. The secret-file deny rules apply in every mode.

## Security

- Every update from anyone other than `owner_id`, or from a group, is ignored. The only exception is `/whoami`. The bot leaves any group it's added to.
- Telegram sessions auto-allow only read-only tools. Everything else needs your tap. Secrets (`.env*`, `*.pem`, `*key*`, `~/.ssh`, the Orca config, and tgbridge's own token and state) are denied to Telegram sessions.
- Approval buttons carry a random single-use nonce, work only for the owner, and expire. Requests too long to show in full are never offered for approval.
- "Allow for session" covers only the exact Bash command, the exact file for Edit/Write, or the host for WebFetch.
- The secret deny rules are best effort: they stop Claude's Read tool, not every possible shell command, which is why Bash asks you in `ask` mode.
- There's no open port: Telegram is reached by long polling, and local parts use a mode-0600 unix socket.
- Every message and approval decision is recorded in `~/.local/state/tgbridge/audit.log`.

## Development

```bash
go test -race ./...
go test -tags e2e ./e2e -v      # real claude (haiku) and the real bot; needs ~/.config/tgbridge
```
