# Configuration

**File:** `~/.config/tgbridge/config.yaml`, mode 600. `tgbridge init` creates it from [`config.example.yaml`](../config.example.yaml).

**Paths:** `~` is expanded. You can override the file location with `TGBRIDGE_CONFIG`.

**After editing:** run `tgbridge service install`: it re-installs the service and restarts it (on Linux, `systemctl --user restart tgbridge` works too). A typo or invalid value stops the service from starting. `tgbridge doctor` shows the config error; the log has the detail (Linux: `journalctl --user -u tgbridge -n 50`, macOS: `~/Library/Logs/tgbridge.log`).

| Setting | Default | What it does |
|---|---|---|
| `owner_id` | *(required)* | Your Telegram user ID; only this account can use the bot. Find it with `tgbridge whoami` (or `/whoami` once the bot runs). |
| `token_file` | `~/.config/tgbridge/token` | Bot token file. It must be mode 600. Alternatively set `TGBRIDGE_TOKEN`. |
| `default_cwd` | `~` | The folder new sessions start in (`/new <path>` overrides it) |
| `model` | Claude Code's default | `claude --model` for Telegram sessions, for example `sonnet` |
| `claude_bin` | `claude` | The claude binary (it must be on the service's PATH) |
| `orca_enabled` | `false` | Optional [Orca](usage.md#optional-orca-integration) integration: Orca agents in `/sessions`, `/status`, handoff. Leave it off if you don't use Orca. |
| `orca_cli` | `$ORCA_CLI_COMMAND`, else `~/.config/orca/cli/bin/orca` | Orca CLI, used only with `orca_enabled: true`. Never the bare `orca` (on Linux that's the screen reader). |
| `engine.permission_mode` | `ask` | Default mode for new topics: `ask`, `auto` or `full` ([usage](usage.md#permission-modes)) |
| `engine.permission_timeout` | `10m` | An unanswered permission prompt is denied after this |
| `engine.idle_timeout` | `15m` | An idle Claude process is closed after this; the next message resumes it |
| `engine.max_live` | `3` | The most Claude processes at once (conversations are unlimited) |
| `engine.max_age` | `24h` | A topic quiet for longer starts fresh with a summary |
| `context.soft_limit` | `80000` | Tokens at which you're warned, with buttons |
| `context.hard_limit` | `150000` | Tokens at which the conversation starts fresh automatically |
| `draft_interval` | `700ms` | How often the streaming draft refreshes |
| `handoff_permission_timeout` | `10m` (max 10m) | Orca only: after this, a handoff prompt falls back to the Orca terminal |
| `rate_limit_per_min` | `30` | Messages per minute before the bot asks you to slow down |
| `state_dir` | `~/.local/state/tgbridge` | Topics, approvals, audit log |
| `socket_path` | `$XDG_RUNTIME_DIR/tgbridge.sock` | Local socket for the MCP server and hooks |
| `uploads.dir` | `~/.cache/tgbridge/uploads` | Where photos and files you send are saved, one folder per topic. Keep it outside `state_dir`: Claude is denied reading `state_dir`, so it couldn't open them there. |
| `uploads.max_age` | `168h` | Saved files older than this are deleted (checked at start and hourly) |
| `uploads.max_total_mb` | `500` | When saved files exceed this, the oldest are deleted first |

**Example:**

```yaml
owner_id: 123456789         # example; use your own from `tgbridge whoami`
token_file: ~/.config/tgbridge/token
default_cwd: ~/
engine:
  permission_mode: auto
```
