# Security policy

tgbridge lets a Telegram account run Claude Code on your machine, so security bugs matter here.

## Reporting a vulnerability

Please **don't open a public issue**. Report it privately through GitHub:
**Security → Report a vulnerability** on this repository ([private advisory form](https://github.com/romcodedao/tgbridge/security/advisories/new)).

Include what you found, how to reproduce it, and the version (`tgbridge version`). You'll get a first answer within 7 days. Once a fix is released, the advisory is published with credit, unless you'd rather stay anonymous.

Please don't include real bot tokens, chat IDs or private data in a report.

## Supported versions

Only the latest release and `main` get security fixes.

## In scope

- Anyone other than `owner_id` getting the bot to act, or reading its output
- Bypassing a permission prompt, the "allow for session" scope, or the deny list
- The bot token, the local socket, or tgbridge's state leaking to Claude sessions or other users

How the bridge is protected, and what it does not protect against: [docs/security.md](docs/security.md).
