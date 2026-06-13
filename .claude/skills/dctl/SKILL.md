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
| `dctl reply [-c CH] <msg_id> "<text>"` | Threaded reply; prints reply id. |
| `dctl read [-c CH] [-n 20] [--after ID]` | Recent messages, oldest→newest, one per line. |
| `dctl watch [-c CH] [-i 10] [--after ID]` | Stream new messages forever (foreground). |
| `dctl channel list [--guild ID]` | List channels (`id type name`). |
| `dctl channel create <name>` | Create a text channel. |
| `dctl channel ensure <name>` | Find-or-create by name (no duplicate). |
| `dctl channel delete <id>` | **Delete** a channel (irreversible). |

`-c`/`--channel` overrides the default channel. `--guild` defaults to the bot's
sole server.

## Rules

- **Read before you reply.** `dctl read` to get the `message_id` and context, then
  `dctl reply <id>`. Replying blind loses the thread.
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
