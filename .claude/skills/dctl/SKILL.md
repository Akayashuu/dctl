---
name: dctl
description: Use when you need to send, read, or reply on Discord from a session, or manage channels, using the dctl CLI/bot. Mono-server (one bot token + a default channel) so no server/channel questions are needed.
---

# dctl — Discord from a session

## Overview

`dctl` is a token-frugal Discord bot CLI. One global token (`DISCORD_BOT_TOKEN`) +
an optional default channel (`DISCORD_CHANNEL_ID`). Output is one line per message
(`id\tauthor\tcontent`) so it's cheap to read. Mono-server: omit `-c` to hit the
default channel — never ask which server/channel.

Build: `go build -o dctl ./cmd/dctl`. Token must come from the environment, never
a flag or a versioned file.

## Quick reference

| Command | Use |
|---|---|
| `dctl send [-c CH] "<text>"` | Post a message; prints its id. |
| `dctl reply [-c CH] <msg_id> "<text>"` | Inline reply (message_reference); prints reply id. |
| `dctl thread [-c CH] <msg_id> "<name>"` | Open a **real thread** off a message; prints the thread's channel id. Post into it with `send -c <thread_id>`. |
| `dctl read [-c CH] [-n 20] [--after ID]` | Recent messages, oldest→newest, one per line. |
| `dctl watch [-c CH] [-i 10] [--after ID]` | Stream new messages forever (foreground). |
| `dctl channel list [--guild ID]` | List channels (`id type name`; type 0=text, 15=forum). |
| `dctl channel create [--forum] <name>` | Create a text (or `--forum`) channel.¹ |
| `dctl channel post <forum_id> <title> <content>` | Open a post (thread) in a forum.¹ |
| `dctl channel ensure <name>` | Find-or-create text channel by name (no duplicate).¹ |
| `dctl channel delete <id>` | **Delete** a channel (irreversible).¹ |

¹ Needs the bot's **Manage Channels** permission. The minimal invite perms
(`68608`) lack it → these return `discord 403 Missing Permissions`. Re-invite
with Manage Channels (perms `68624`) to use channel create/delete/forum.
`send`/`read`/`reply`/`thread` work on the minimal perms.

`-c`/`--channel` overrides the default channel. `--guild` defaults to the bot's
sole server.

## Rules

- **Read before you reply.** `dctl read` to get the `message_id` and context, then
  `dctl reply <id>`. Replying blind loses the thread.
- **Real thread vs inline reply.** `reply` = a one-off inline reply. `thread` = a
  proper sidebar thread for an ongoing sub-conversation; then `send -c <thread_id>`.
- **Default channel implicit.** Omit `-c` unless the user names another channel.
- **No default channel set?** `dctl channel ensure prospector` creates one without
  duplicating an existing same-name channel.
- **Deletion is destructive.** Never `dctl channel delete` without an explicit user
  request naming the channel.
- **Token stays in env.** `DISCORD_BOT_TOKEN` lives only in the shell/`.env`, never
  in a tracked file or command you echo back.

## Common mistakes

- Posting a notification the prospector backend already fans out (reply/bounce/
  reminder/rdv) → duplicate. The backend posts those automatically.
- Asking which server/channel → it's mono-server; default channel is implicit.
- Passing the token as a flag → it leaks into history/logs. Use the env var.

## Bridging a persistent session to a channel

To make a channel conversational with a long-lived Claude session, use the bridge —
see the `dctl-bridge` skill.
