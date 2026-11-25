# Upgrading and moving

## Upgrade notes

To upgrade, run the installer again (it keeps your config), or `make update` in a source checkout. If you built tgbridge from source before the first public release, read these first:

- **Orca is now opt-in.** If you use `/sessions` with Orca agents or handoff, add `orca_enabled: true` to `~/.config/tgbridge/config.yaml`, then restart. Without it, Orca agents disappear from `/sessions` and handoff stops; handoff topics left open show a hint to `/close` them.
- **Setup changed to `tgbridge init`.** In your checkout run `git pull && make setup`. It rebuilds the binary and re-runs `init`, which keeps your config and token, asks nothing on a configured machine, and rewrites the service file with the binary's absolute path (`deploy/tgbridge.service` is gone; `tgbridge service install` generates the unit). If your old unit had extra `Environment=` lines (more PATH entries, proxies, `TGBRIDGE_CONFIG`), the edited file is kept as `tgbridge.service.bak` next to the new one: copy those lines over. A custom `TGBRIDGE_CONFIG` set when you run `init` is written in for you.
- **`tgbridge uninstall` now removes the service too** (it used to remove only the hooks). To remove only the hooks and the MCP server, use `tgbridge hooks uninstall`. `install` still works as an alias for `hooks install`. Config and state are kept unless you add `--purge`.
- **Rebuild before the other targets.** After pulling, run `make setup` (or `make build`) first: `make status`, `hooks`, `unhooks`, `doctor` and `uninstall` call subcommands the old binary lacks, and stop with "the installed binary is older than this checkout" until you do.
- **`owner_id` is set by `tgbridge init`**, which waits for your first message to the bot (`tgbridge whoami` still prints the ID if you'd rather edit the config by hand). Existing configs need no change.
- **Local files in a tracked folder.** If you kept your own files in a folder of the checkout that is no longer tracked (`docs/plans/`, for example), back it up before you pull: a pull deletes the tracked copy.

## Moving to another machine

**What moves:** the config, the token and tgbridge's state (`~/.config/tgbridge` and `~/.local/state/tgbridge`). **What gets rebuilt:** the binary, the service and the hooks.

**What doesn't move:** Claude's own conversation transcripts live in `~/.claude/projects/` on the old machine. Copy them too if you want old topics to resume their conversations. Without them, old topics start fresh on their next message.

**Run only one at a time.** Two machines polling the same bot token fight each other (`409 Conflict` in the log), so stop the old one first.

### 1. On the old machine

Stop the service, then archive the config and state:

```bash
systemctl --user stop tgbridge                                                  # Linux
# macOS: launchctl bootout gui/$(id -u)/io.github.romcodedao.tgbridge
(umask 077 && tar czf ~/tgbridge-backup.tgz -C ~ .config/tgbridge .local/state/tgbridge)
```

Expected: no output, and `~/tgbridge-backup.tgz` exists. (In a source checkout, `make stop && make backup` does the same.)

Optional, to keep conversations resumable: `tar czf ~/claude-projects.tgz -C ~ .claude/projects`.

Copy the archive(s) to the new machine, for example: `scp ~/tgbridge-backup.tgz newhost:~/`.

### 2. On the new machine: prerequisites

| Needed | Check | Expected |
|---|---|---|
| Claude Code, logged in | `claude --version`, then run `claude` once to log in | a version; a working session |
| Orca (only with `orca_enabled`) | `orca status --json` (or the `orca_cli` path in your config) | `"ok": true` |
| Tools Claude should use (kubectl, gh, …) and their configs | for example `gh auth status` | logged in |

### 3. On the new machine: install

```bash
cd ~ && tar xzf ~/tgbridge-backup.tgz           # restores ~/.config/tgbridge and ~/.local/state/tgbridge
# optional: cd ~ && tar xzf ~/claude-projects.tgz
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/romcodedao/tgbridge/main/install.sh | sh
tgbridge doctor
```

Expected: the installer ends with `Done.` (the restored config already has `owner_id`, so `init` asks nothing); `tgbridge doctor` shows `ok` lines. Installing from source instead: [docs/install.md](install.md#from-source).

- **Config paths:** if the username or home path changed, edit `~/.config/tgbridge/config.yaml` (`default_cwd`, `orca_cli`) and run `tgbridge service install`. Paths written with `~` need no change.
- **Linger (Linux):** if `doctor` reports it off, run `sudo loginctl enable-linger $USER`.
- **Hooks:** if you used them on the old machine (`telegram_send`, Orca handoff), run `tgbridge hooks install`, then start new Claude sessions.

### 4. Check from the phone

Send `/sessions` to the bot. You should see your old topics, and Orca agents if `orca_enabled` is on. Then send a message in General: a new topic opens and Claude answers.

### 5. Clean up the old machine

```bash
tgbridge uninstall
```

Expected: a `Removed:` list (the service and the hooks) and a `Kept:` list (config, token, state). Delete `~/.config/tgbridge`, `~/.local/state/tgbridge` and the backup archive when you're sure the new machine works. The token is in them.
