---
name: dctl-bridge
description: Use when the user wants to link a persistent Claude (or other) session to a Discord channel so the channel becomes conversational — each human message runs a command and its output is posted back. Covers running, persisting, and supervising the bridge.
---

# dctl bridge — link a session to a channel

## Overview

`dctl bridge` turns a Discord channel into a chat front-end for a command. It polls
the channel; for each **human** message (bot messages are skipped → no loops) it runs
the command with the message text as the last arg + on stdin, then posts the command's
stdout back as a reply (chunked at Discord's 2000-char limit). It does **not** run a
poller inside the backend — the bridge is the long-lived process.

Mono-server: one bot token + default channel. If no channel is set it creates/reuses
one (`--ensure`, default `prospector`).

## Run it

```sh
# Link a single persistent Claude conversation to the default channel:
dctl bridge --cmd 'claude -p --continue' \
  --state ~/.local/state/dctl/bridge.last
```

`--continue` keeps one shared conversation across messages. Drop it for a fresh
session per message.

## Flags

| Flag | Default | Use |
|---|---|---|
| `--cmd '<command>'` | (required) | Command run per human message. |
| `-c CHANNEL` | `DISCORD_CHANNEL_ID` | Channel to bridge. |
| `--ensure NAME` | `prospector` | If no channel set, create/reuse this one. |
| `-i N` | `5` | Poll interval (seconds). |
| `--state FILE` | — | Persist last-seen id so a restart doesn't replay history. |
| `--after ID` | — | Start after this id (overrides `--state`). |
| `-v` | off | Log activity to stderr. |

Per-message environment passed to the command: `DCTL_MSG`, `DCTL_AUTHOR`,
`DCTL_MESSAGE_ID`, `DCTL_CHANNEL`.

## Keep it running

- **systemd (user):** template at `contrib/dctl-bridge.service`. Set `DISCORD_BOT_TOKEN`
  in its environment, then `systemctl --user enable --now dctl-bridge`.
- **Quick/detached:** `nohup dctl bridge --cmd '…' --state … >/var/log/dctl.log 2>&1 &`.
- Always pass `--state` for any supervised run so a restart resumes instead of
  re-answering old messages.

## Rules

- **Loop safety is built in:** the bridge ignores bot authors. Don't add a second
  process that replies to bot messages or you'll create an echo loop.
- **One bridge per channel.** Two bridges on the same channel double-answer.
- **Token in env only** (`DISCORD_BOT_TOKEN`) — never a flag, never tracked.
- **`--continue` shares context** across every human in the channel — fine for a solo
  control channel, not for multi-user privacy.

## Common mistakes

- No `--state` → every restart replays the whole channel and re-runs the command on
  old messages.
- Bridging a channel the backend also fans out notifications into → the command sees
  the bot's own notifications as input. Skipped automatically (bot author), but keep a
  dedicated control channel to avoid noise.
