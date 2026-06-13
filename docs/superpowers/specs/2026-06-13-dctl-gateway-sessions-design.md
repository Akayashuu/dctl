# DCTL — Gateway daemon + Sessions

**Date:** 2026-06-13
**Status:** Approved

## Goal

Turn `dctl` from a REST-only CLI into a persistently-online Discord bot that can
spin up "sessions" (channels or forum posts bound to a bridged Claude process)
through native slash commands.

Three user-facing capabilities:

1. **Bot online 24/7** — a real Gateway (websocket) connection so the bot shows
   the green "online" presence at all times.
2. **Session commands** — create/close/list "sessions"; each session is a channel
   (or forum post) with a bridged command (Claude by default) running on it.
3. **`/set home`** — designate the category (or forum) that holds all session
   channels. The home type is auto-detected (category → text channels, forum →
   forum posts).
4. **Liveness dashboard** — at-a-glance "is the bot up?" that depends **only on the
   bot + daemon, never on any Claude/AI session**. Surfaced two ways: an HTTP
   `/health` endpoint (for uptime monitors) and a self-updating Discord status
   message.

## Architecture

### `dctl serve` — the daemon

A new top-level command. Responsibilities:

- **Gateway client** (`gateway.go`, package `dctl`): minimal websocket client —
  IDENTIFY, heartbeat loop, RESUME on reconnect, dispatch of `INTERACTION_CREATE`.
  Intents: `Guilds` (interactions don't need message intents). Maintaining the
  connection is what gives the bot its online presence.
- **Slash-command registration**: on startup, `PUT` the application command set
  (global or guild-scoped) via REST so the commands appear in Discord.
- **Interaction handler**: routes each interaction to a handler, replies via the
  interaction-callback REST endpoint. Errors and allowlist denials reply
  *ephemeral* (visible only to the invoker).
- **Supervisor**: for each active session, spawn a child `dctl bridge` process
  (reusing the existing bridge) and keep it alive (restart on crash). Sessions are
  persisted, so they are respawned when the daemon restarts.

### Slash commands

| Command | Effect |
|---|---|
| `/set home <channel>` | Set the category **or** forum that contains sessions. Auto-detect type. |
| `/session <name> [cmd]` | Create a text channel under the home category **or** a forum post; start a bridge on it; register the session. `cmd` optional (defaults to Claude). |
| `/session close <name>` | Stop the bridge and archive the channel/post; deregister. |
| `/session list` | List active sessions. |
| `/allow add\|remove\|list [user]` | Manage the allowlist. |

### Allowlist

All slash commands are gated by an **allowlist of Discord user IDs** (not a
permission check). Invocations from a non-listed user get an ephemeral refusal.
Seeded with `343535234303787009` (akayashuu) by default.

### Persistent state — `state.json`

```json
{
  "home":     { "id": "...", "type": "category" | "forum" },
  "allow":    ["343535234303787009"],
  "sessions": [ { "name": "...", "channelID": "...", "type": "text|forum", "cmd": "..." } ]
}
```

Load on startup, save on every mutation. The store is a small, independently
testable unit.

### Health & status (bot/daemon liveness)

The daemon keeps one in-memory `Health` snapshot, refreshed by the parts that
already run — **no dependency on any bridged Claude process**:

```go
type Health struct {
    Online        bool      // gateway connected AND heartbeat ACK'd within the window
    StartedAt     time.Time // → uptime
    LastHeartbeat time.Time // last gateway heartbeat ACK
    LastPing      time.Time // last successful REST self-ping (GET /users/@me)
    PingLatencyMS int64     // round-trip of that self-ping
    Sessions      int       // active supervised bridges
}
```

- **Signal source:** `Online` is driven by the Gateway heartbeat ACK loop (already
  required for presence). A lightweight ticker also does a REST `GET /users/@me`
  every ~30 s to record an independent reachability latency. Both are pure
  bot/transport facts — a hung or crashed Claude session never flips it.

**Surface 1 — HTTP `/health`** (`--health-addr`, e.g. `:8787`; disabled if unset):

```
GET /health  → 200 {"online":true,"uptime_s":12345,"ping_ms":48,"sessions":2,
                    "last_heartbeat":"...","last_ping":"..."}
             → 503 (same body, online:false) when the gateway is down
```

200/503 status lets UptimeKuma / curl alert without parsing. JSON for a dashboard.

**Surface 2 — Discord status message** (`--status-channel <id>`, optional): the
daemon maintains a single embed (find-or-create + edit-in-place, id cached in
`state.json`) refreshed on heartbeat and on each session change:

```
🟢 dctl online  ·  uptime 3h41m  ·  ping 48ms  ·  2 sessions   (updated 21:32)
```

Flips to `🔴 dctl offline` only via the systemd unit's restart gap (the daemon
can't post when it's the thing that's down) — so the embed showing a stale
timestamp + the 503 from `/health` are the two complementary "it's down" signals.

## Data flow

```
Discord  ──INTERACTION_CREATE──▶  Gateway client  ──▶  handler
                                                         │
                          mutate state + REST calls (create channel / forum post,
                          archive, etc.) + spawn/stop child `dctl bridge`
                                                         │
                                                         ▼
                                       interaction-callback REST response
```

## Error handling

- Allowlist denial → ephemeral "not authorized".
- Missing/invalid `home` when creating a session → ephemeral error telling the
  user to run `/set home` first.
- Discord REST failures (e.g. missing Manage Channels) → ephemeral error with the
  Discord message surfaced.
- Child bridge crash → supervisor restarts it; repeated failures logged to stderr.

## Testing

- **State store**: load/save round-trip, allowlist add/remove/contains, session
  add/remove/list. Pure unit tests.
- **Command routing**: feed synthetic `INTERACTION_CREATE` payloads into the
  handler (gateway kept thin) and assert the chosen action + interaction response,
  including allowlist gating.
- Websocket transport itself is kept minimal and exercised manually / via a fake
  connection; logic lives in testable handlers, not the socket loop.
- **Health**: the `/health` handler is a pure function of a `Health` value — table
  test the 200-vs-503 split and the JSON body; the embed renderer (Health → string)
  is unit-tested without touching Discord.

## Implementation phases

1. **Gateway daemon foundation** — `dctl serve`, Gateway connect + online presence,
   slash-command registration, interaction plumbing, allowlist, `/set home`.
   Includes the `Health` snapshot + `/health` HTTP endpoint (the heartbeat loop is
   already here, so liveness is essentially free at this stage).
2. **Sessions** — `/session create|close|list` + the supervisor (spawn/restart
   child bridges) + session persistence. Wire `Sessions` count into `Health`.
3. **Status embed** — the self-updating Discord status message (`--status-channel`).
4. **Forum variant** — sessions as forum posts when `home` is a forum.

## Out of scope (YAGNI)

- Multi-guild home configuration (mono-server assumption holds).
- Rich web dashboard, historical metrics / time-series, alerting rules. (The
  `/health` endpoint + status embed cover live liveness; graphing is a monitor's
  job, e.g. point UptimeKuma at `/health`.)
- Per-session model or richer Claude config beyond an optional `cmd`.
