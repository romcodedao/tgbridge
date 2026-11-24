# Upgrading and moving

## Upgrade notes

Read these before `make update` (it runs `git pull --ff-only`) when you come from a version before the open-source release:

- **Back up `docs/plans/` first.** It is no longer tracked, so pulling deletes the copy in your checkout: `cp -a docs/plans ~/tgbridge-plans-backup` in the checkout, then copy it back after the update. New files there stay untracked (it's in `.gitignore`).
- **Orca is now opt-in.** If you use `/sessions` with Orca agents or handoff, add `orca_enabled: true` to `~/.config/tgbridge/config.yaml`, then restart. Without it, Orca agents disappear from `/sessions` and handoff stops; handoff topics left open show a hint to `/close` them.
- **Setup changed to `tgbridge init`.** Existing source installs: in the checkout run `git pull && make setup`. It rebuilds the binary and re-runs `init`, which keeps your config and token, asks nothing on a configured machine, and rewrites the service file with the binary's absolute path (`deploy/tgbridge.service` is gone; `tgbridge service install` generates the unit). If your old unit had extra `Environment=` lines (more PATH entries, proxies, `TGBRIDGE_CONFIG`), the edited file is kept as `tgbridge.service.bak` next to the new one: copy those lines over. A custom `TGBRIDGE_CONFIG` set when you run `init` is written in for you.
- **`tgbridge uninstall` now removes the service too** (it used to remove only the hooks). To remove only the hooks and the MCP server, use `tgbridge hooks uninstall`. `install` still works as an alias for `hooks install`. Config and state are kept unless you add `--purge`.
- **`owner_id` is set by `tgbridge init`**, which waits for your first message to the bot (`tgbridge whoami` still prints the ID if you'd rather edit the config by hand). Existing configs need no change.

## Moving to another machine

**What moves:** the config, the token and tgbridge's state. **What gets rebuilt:** the binary, the service and the hooks.

**What doesn't move:** Claude's own conversation transcripts live in `~/.claude/projects/` on the old machine. Copy them too if you want old topics to resume their conversations. Without them, old topics start fresh on their next message.

**Run only one at a time.** Two machines polling the same bot token fight each other (`409 Conflict` in the log), so stop the old one first.

### 1. On the old machine

```bash
tgb stop && tgb backup
```

Expected: `stopped`, then a line for `~/tgbridge-backup-<date>.tgz`.

Optional, to keep conversations resumable: `tar czf ~/claude-projects.tgz -C ~ .claude/projects`.

Copy the archive(s) to the new machine, for example: `scp ~/tgbridge-backup-*.tgz newhost:~/`.

### 2. On the new machine: prerequisites

| Needed | Check | Expected |
|---|---|---|
| systemd user session | `systemctl --user status` | a running user manager |
| Go 1.27+ | `go version` | `go1.27` or newer |
| make, git | `make --version; git --version` | versions print |
| Claude Code, logged in | `claude --version`, then run `claude` once to log in | a version; a working session |
| Orca (only with `orca_enabled`) | `~/.config/orca/cli/bin/orca status --json` | `"ok": true` |
| Tools Claude should use (kubectl, gh, …) and their configs (`~/.kube`, gh auth) | `kubectl config get-contexts` | your contexts |

### 3. On the new machine: install

```bash
git clone https://github.com/romcodedao/tgbridge ~/projects/tgbridge
cd ~ && tar xzf ~/tgbridge-backup-*.tgz            # restores ~/.config/tgbridge and ~/.local/state/tgbridge
# optional: cd ~ && tar xzf ~/claude-projects.tgz
cd ~/projects/tgbridge && make setup
echo "alias tgb='make -s -C ~/projects/tgbridge'" >> ~/.bashrc && source ~/.bashrc   # optional shortcut
tgb doctor
```

Expected: `make setup` ends with `Done.` (the restored config already has `owner_id`, so `init` asks nothing); `tgb doctor` shows `ok` lines.

- **Config paths:** if the username or home path changed, edit `~/.config/tgbridge/config.yaml` (`default_cwd`, `orca_cli`) and run `tgbridge service install`. Paths written with `~` need no change.
- **Linger:** if `doctor` reports it off, run `sudo loginctl enable-linger $USER`.
- **Handoff (Orca only):** `tgbridge hooks install`, then start new Orca Claude sessions.

### 4. Check from the phone

Send `/sessions` to the bot. You should see your old topics, and Orca agents if `orca_enabled` is on. Then send a message in General: a new topic opens and Claude answers.

### 5. Clean up the old machine

```bash
tgb uninstall
```

Expected: a `Removed:` list (the service and the hooks) and a `Kept:` list (config, token, state). Delete `~/.config/tgbridge`, `~/.local/state/tgbridge` and the backup archive when you're sure the new machine works. The token is in them.
