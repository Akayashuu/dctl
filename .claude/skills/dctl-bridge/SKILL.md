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
| `--state FILE` | — | Persist last-seen id. **Authoritative**: a restart resumes exactly here and never replays handled messages. Always pass it. |
| `--after ID` | — | Seeds the start id for the **first run only** (ignored once `--state` exists). |
| `-v` | off | Log activity to stderr. |
| `--backend tmux\|stream\|oneshot` | `tmux` | Responder strategy (see below). `--stream` is legacy, consulted only when this is unset. |
| `--tmux-timeout DUR` | `5m` | tmux backend: max wait for a turn to settle. |

Per-message environment passed to the command: `DCTL_MSG`, `DCTL_AUTHOR`,
`DCTL_MESSAGE_ID`, `DCTL_CHANNEL`.

## Backends

The bridge can talk to Claude three ways:

- **`stream`** — one persistent `claude -p` **stream-json** process.
  Structured, token-frugal, context stays hot. Permission prompts are not
  interactive (Claude runs pre-approved). Select with `--backend stream`.
- **`oneshot`** — runs `--cmd` fresh per message (arbitrary non-Claude commands).
- **`tmux`** (**default**) — drives the **interactive `claude` TUI** inside a tmux session and
  relays its **text** back (no screenshots/ANSI). One persistent `claude` per
  channel (`tmux send-keys` in, `capture-pane` out, diffed and chrome-stripped).
  Launched with `--dangerously-skip-permissions`, so there are no permission
  prompts to answer yet (rendering prompts as Discord buttons is a future
  phase). Requires the `tmux` binary on PATH. You can `tmux attach -t
  dctl-<channel>` (or `dctl-<DCTL_INSTANCE_ID>-<channel>` when that env var is
  set) to land in the same live session the bridge is driving. **Known limit:**
  multi-line messages are flattened to one line before sending (a literal
  newline would submit early); send separate messages for separate turns.

From the daemon: `/session create name:foo backend:tmux` creates a tmux-backed
session; the backend is persisted, so a daemon restart respawns it the same way.

### Security (read before exposing tmux)

- **The allowlist is the only gate.** With `--dangerously-skip-permissions`, every
  message from an *allowed* author becomes a command Claude runs unprompted. Always
  deploy the tmux backend with `--allow-state` on a **dedicated control channel** —
  never an open channel.
- **The tmux backend runs `--cmd` through a shell.** `tmux new-session` execs the
  command string via `/bin/sh -c`, so shell metacharacters in `--cmd` are
  interpreted (the `stream`/`oneshot` backends exec an explicit argv with no shell).
  Treat `--cmd` as trusted operator input and don't build it from untrusted text.
- The pane working directory is pinned explicitly (the tmux *server* is a daemon
  whose cwd may differ from the bridge's); a stale namesake session is killed before
  a fresh one starts.

## Feedback while it works

The command is slow (a Claude run takes tens of seconds), so the bridge reacts to
each human message **immediately on pickup** with 👀, then swaps it for ✅ once the
answer is posted (⚠️ on empty/error). The human sees the message was registered
without waiting. Reactions need the bot's **Add Reactions** permission; if missing
they're skipped silently (the reply still posts).

**State / no-replay:** the bridge marks a message handled (persists its id) *before*
running the command. This guarantees a restart never replays — at the cost that a
crash mid-command drops that one reply rather than re-answering it. Always run with
`--state`; never bake `--after` into a supervised launcher (it only seeds the first
run anyway).

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
