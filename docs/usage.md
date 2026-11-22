# Using the bot

## Sessions and topics

- A message in **General** starts a new Claude session in its own topic.
- A message in a topic continues that topic's conversation.
- A topic you create yourself in Telegram also starts a session.
- Each reply streams live: a thinking block, a checklist of tool calls, and the answer, with a ⏹ stop button. The final message keeps the thinking and tool calls collapsed. Its footer shows `ctx 64k · 2m14s · $0.12` (context size, time, cost).
- Pasting or forwarding an alert works: forwarded text is passed to Claude as data, not instructions.

## Photos and files

- Send a photo or a file (with or without a caption) in General or a topic. The bot saves it and tells Claude its path, so Claude can read it. The caption is the message.
- Up to **20 MB** (the Telegram bot limit). Voice, video, audio and stickers aren't supported.
- An album sent in General goes into one topic.
- Files named like `*.pem`, `*.key` or `.env*` are saved, but Claude's secret-file rules stop it reading them.
- Saved files are deleted after 7 days, when they pass 500 MB in total (oldest first), and on `/close` or `/reset` in their topic. See `uploads.*` in [configuration](configuration.md).

## Commands

| Command | Does |
|---|---|
| *(message in General)* | New session in a new topic |
| *(message in a topic)* | Continue that session |
| `/new [path] <task>` | New session, optionally in a folder (`/new ~/src/app fix the build`) |
| `/sessions` | Telegram sessions and Orca agents, with 📲 Hand off buttons |
| `/status <orca-session-id>` | Last messages of an Orca agent |
| `/stop` | Interrupt the running turn (same as the ⏹ button) |
| `/reset`, `/clear` | Fresh conversation in this topic |
| `/compact` | Shrink this conversation's context (only when Claude is idle) |
| `/mode ask\|auto\|full` | How much Claude may do without asking, for this topic |
| `/close`, `/exit` | End this session; the topic is renamed 🔒 |
| `/handoff off` | Stop forwarding a handed-off Orca session |
| `/whoami` | Your Telegram user ID |
| `/help` | This list |

Any other `/command` is sent to Claude as a message. Skill commands work. Menu-style commands that need Claude's interactive screen (`/model`, `/config`) don't.

## Permission modes

| Mode | What asks you on Telegram |
|---|---|
| `ask` | Everything except read-only tools (Read, Grep, Glob, `git status`, `ls`, read-only `orca` status commands) |
| `auto` | Only what Claude Code's auto mode considers risky |
| `full` | Nothing (`bypassPermissions`) |

- **Default for new topics:** `engine.permission_mode` in config (see [configuration](configuration.md)).
- **One topic:** `/mode auto`. The topic's next message resumes the same conversation in the new mode.
- **Prompt buttons:** **Allow**, **Allow for session** and **Deny**.
  - "Allow for session" covers only that exact command (Bash), that exact file (Edit/Write), that website (WebFetch), or that exact call (other tools).
  - An unanswered prompt is denied after `engine.permission_timeout` (10 min).
  - A ⚠️ on a prompt means it contains hidden or right-to-left characters. Read it carefully.

## Handing off an Orca session

Needs `tgb hooks` once, then a **new** Orca Claude session.

1. Inside the Orca session, say **"notify me on Telegram"**, or tap 📲 in `/sessions`. A 📲 topic opens.
2. Claude's output appears in that topic. What you type there goes into the terminal when Claude is idle; while it works, your message is queued.
3. Permission prompts appear as buttons. Unanswered after 10 min, the prompt goes back to the terminal.
4. To end it, say **"stop notifying Telegram"** in the session, or send `/handoff off` in the topic.

## Context limits

Long conversations cost more and answer worse, so tgbridge keeps them small:
- At **80k** tokens you get **[Compact]** and **[Start fresh with summary]** buttons.
- At **150k** tokens the conversation starts fresh automatically, carrying a summary. Messages you send meanwhile are kept.
- A topic quiet for **24 h** also starts fresh with a summary.
- An idle Claude process closes after **15 min**. Your next message resumes the conversation.
- At most **3** Claude processes run at once. Others wait, with buttons to stop a running one.

All of these are settings: see [configuration](configuration.md).
