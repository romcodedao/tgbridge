# Security

**Who can use it.** Only `owner_id`.
- Every update from another account, or from a group, is ignored. The one exception is `/whoami`, which answers anyone with their ID.
- The bot leaves any group it's added to.
- Edited messages are ignored.
- More than `rate_limit_per_min` messages a minute get a "slow down" reply.

**Approvals.**
- Each permission prompt carries a random single-use nonce. Only the owner's taps count, and a prompt expires with its request.
- A request too long to show in full is never offered for approval:
  - from a Telegram session, it's denied and Claude is told to split it;
  - from a handed-off session, it goes back to the terminal prompt.
- Hidden or right-to-left characters are shown as `<U+XXXX>` with a ⚠️ warning. Ordinary emoji and accented text aren't flagged.
- "Allow for session" covers:
  - **Bash:** the exact command and all its options;
  - **Edit/Write:** the exact file;
  - **WebFetch:** the website;
  - **any other tool:** the exact call.

  It's cleared on `/reset`, rollover, `/close` and a new Claude session.

**What Telegram sessions can read without asking (`ask` mode).**
- **Without asking:** Read, Grep, Glob, `git status`, `ls`, and, only with `orca_enabled`, five exact read-only `orca` status commands.
- **Always denied to the Read tool**, in every mode: `.env*`, `*.pem`, `*.key`, SSH keys, `~/.ssh`, Claude credentials, gh, aws, kube, docker, netrc, gnupg, git-credentials, the Orca config, and tgbridge's own config, token and state.
- **The limit of that list:** it blocks the Read tool only, not every shell command, and files it doesn't name stay readable. That's why `ask` mode asks before any other Bash command. In `auto` and `full` modes, Claude Code's own rules decide instead.

**Secrets of the bridge.**
- **Token:** read from a mode-600 file and removed from the daemon's environment, so Claude, MCP servers and commands never inherit it. It's never logged.
- **Socket:** the local unix socket is mode 600.
- **No open port:** Telegram is reached by outbound long polling.

**Audit.** Every message (length only, not the text) and every approval decision goes to `~/.local/state/tgbridge/audit.log`.

**`tgbridge hooks install` (optional; `make hooks` in a checkout).** It gives **every** Claude session on the machine the `telegram_send` tool, so a prompt-injected agent could send you a message. It can't read anything back.
