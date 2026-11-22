# Moving to another machine

**What moves:** the config, the token and tgbridge's state. **What gets rebuilt:** the binary, the service and the hooks.

**What doesn't move:** Claude's own conversation transcripts live in `~/.claude/projects/` on the old machine. Copy them too if you want old topics to resume their conversations. Without them, old topics start fresh on their next message.

**Run only one at a time.** Two machines polling the same bot token fight each other (`409 Conflict` in the log), so stop the old one first.

## 1. On the old machine

```bash
tgb stop && tgb backup
```

Expected: `stopped`, then a line for `~/tgbridge-backup-<date>.tgz`.

Optional, to keep conversations resumable: `tar czf ~/claude-projects.tgz -C ~ .claude/projects`.

Copy the archive(s) to the new machine, for example: `scp ~/tgbridge-backup-*.tgz newhost:~/`.

## 2. On the new machine: prerequisites

| Needed | Check | Expected |
|---|---|---|
| systemd user session | `systemctl --user status` | a running user manager |
| Go 1.27+ | `go version` | `go1.27` or newer |
| make, git | `make --version; git --version` | versions print |
| Claude Code, logged in | `claude --version`, then run `claude` once to log in | a version; a working session |
| Orca (optional) | `~/.config/orca/cli/bin/orca status --json` | `"ok": true` |
| Tools Claude should use (kubectl, gh, …) and their configs (`~/.kube`, gh auth) | `kubectl config get-contexts` | your contexts |

## 3. On the new machine: install

```bash
git clone https://github.com/romcodedao/tgbridge ~/projects/tgbridge
cd ~ && tar xzf ~/tgbridge-backup-*.tgz            # restores ~/.config/tgbridge and ~/.local/state/tgbridge
# optional: cd ~ && tar xzf ~/claude-projects.tgz
cd ~/projects/tgbridge && make setup
echo "alias tgb='make -s -C ~/projects/tgbridge'" >> ~/.bashrc && source ~/.bashrc
tgb doctor
```

Expected: `make setup` ends with `active`; `tgb doctor` shows `ok` lines.

- **Config paths:** if the username or home path changed, edit `~/.config/tgbridge/config.yaml` (`default_cwd`, `orca_cli`) and run `tgb restart`. Paths written with `~` need no change.
- **Linger:** if `doctor` reports it off, run `sudo loginctl enable-linger $USER`.
- **Handoff:** `tgb hooks`, then start new Orca Claude sessions.

## 4. Check from the phone

Send `/sessions` to the bot. You should see your old topics, and Orca agents if Orca runs there. Then send a message in General: a new topic opens and Claude answers.

## 5. Clean up the old machine

```bash
tgb uninstall
```

Expected: `removed service and hooks; kept …`. Delete `~/.config/tgbridge`, `~/.local/state/tgbridge` and the backup archive when you're sure the new machine works. The token is in them.
