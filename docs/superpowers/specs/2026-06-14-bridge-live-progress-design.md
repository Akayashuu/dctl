# Bridge live progress feedback — design

**Date:** 2026-06-14
**Component:** `dctl bridge` (stream mode)
**Status:** approved, pending implementation plan

## Problem

When the bridge runs a Claude turn in stream mode, the only feedback the channel
sees is the 👀 reaction on pickup, then the full answer dumped at the end (tens of
seconds later). Everything Claude does in between — the tools it runs, its
intermediate reasoning — is read off the stream and **discarded**: `readTurn`
(`internal/session/stream.go`) keeps only the terminal `result` event and ignores
all others. The user wants that activity surfaced into the conversation as it
happens.

## Goal

Surface intermediate stream events (tool uses + assistant reasoning text) into the
channel as a single live-updating "progress" message per human message, at a
detail level chosen when the bridge session is launched.

Non-goals: changing one-shot mode (it has no stream to surface); streaming partial
final-answer text; any change to the existing reply/reaction flow.

## Behaviour

### Detail levels — `--progress` flag

| Level | Shows |
|---|---|
| `off` | Nothing until the final reply (current behaviour). |
| `actions` | One line per tool use. |
| `full` *(default)* | Tool uses **and** assistant reasoning text. |

Set per session at launch, e.g. `dctl bridge --progress actions`. Default `full`.

### Live progress message

For each human message, while the turn runs, the bridge maintains **one** progress
message (created on the first event, edited in place via `UpsertStatusMessage`).
A fresh progress message is used per human message (tweak #3) so the channel keeps
a per-question trace.

Rendered view (capped to the last ~15 lines; older lines elided with a leading `…`):

```
⏳ en cours…
🔧 Bash · git status
📖 Read · stream.go
💭 je regarde comment le turn est lu…
✏️ Edit · bridge.go
```

- Tool use → `<emoji> <Tool> · <short detail>`. Detail is a one-line, truncated
  summary of the tool input (e.g. command for Bash, path for Read/Edit). Unknown
  tools get a generic 🔧.
- Reasoning text (level `full` only) → `💭 <snippet>` (truncated, newlines
  collapsed).

### Collapse on completion (tweak #1)

When the turn finishes and the final answer is posted as the reply, the progress
message is **edited down to a one-line summary** (default):

```
✅ 6 actions (Bash×2, Read×3, Edit) · 28s · $0.04
```

- Counts grouped by tool name.
- Duration measured by the bridge across the turn.
- Cost from the `result` event's `total_cost_usd` (tweak #2); omitted if 0.
- On error turn: `⚠️` prefix instead of `✅`.

A `--progress-keep` boolean (default false) disables the collapse and leaves the
full running list as the permanent trace.

## Architecture / changes

1. **`internal/session/stream.go`**
   - Extend the parsed event shape to capture `type:"assistant"` messages and walk
     `message.content` blocks (`tool_use` → name + input; `text` → text).
   - `readTurn(r, onEvent)` gains an `onEvent func(Event)` callback. It emits an
     `Event` per relevant block as lines arrive, still returning on the terminal
     `result` event. `onEvent` nil → no emission (cheap, same as today).
   - `Event` struct: `Kind` (`"tool"` | `"text"`), `Tool string`, `Detail string`.
   - `streamSession.Send(text, onEvent)` threads the callback through.
   - `Responder.Respond(ctx, m, onEvent)` gains the callback. `oneShotResponder`
     ignores it (no stream). `streamResponder` forwards it; on process-death retry
     the callback is reused.

2. **`internal/bridge/bridge.go`**
   - New `Options` fields: `Progress string` (`off|actions|full`), `ProgressKeep bool`.
   - Per human message, build a progress renderer holding the capped line list and
     the live message id. The `onEvent` callback (passed to `resp.Respond`):
     filters by level, appends a rendered line, and pushes the view via
     `UpsertStatusMessage` — **throttled**.
   - **Throttle:** coalesce rapid events; edit at most once per ~1.5s, with a
     guaranteed final flush before the collapse. Implemented with a last-sent
     timestamp + a pending-dirty flag (no extra goroutine needed if we flush on the
     next event past the interval and once more at end).
   - On completion: render the collapse summary (or keep, per `--progress-keep`)
     and do a final `UpsertStatusMessage`. Track per-tool counts + start time in
     the renderer.
   - `off` → skip all of the above (current path untouched).

3. **`cmd/dctl/bridge.go`**
   - Add `--progress` (string, default `full`) and `--progress-keep` (bool) flags;
     pass into `bridge.Options`.

4. **`~/.claude/skills/dctl-bridge/SKILL.md`**
   - Document `--progress` / `--progress-keep` under Flags and a short note in
     "Feedback while it works".

## Error handling

- Progress posting is **best-effort**: any `UpsertStatusMessage` error is logged
  (verbose) and ignored — it must never block or fail the actual reply.
- If the bot lacks edit permission, the first upsert falls back to a send and
  subsequent edits fail silently; the final reply still posts normally.
- Stream parse errors on a malformed assistant line are skipped, not fatal — the
  `result` terminator still governs turn completion.

## Testing

- `stream_test.go`: feed a canned stream-json sequence (assistant tool_use blocks,
  assistant text, terminal result) into `readTurn` with a capturing `onEvent`;
  assert the emitted `Event`s and the returned `turnResult`. Cover `onEvent == nil`
  (no panic, result still parsed).
- Renderer unit test: line capping/elision, tool-detail extraction per tool,
  collapse summary formatting (counts, duration, cost, error prefix), and the
  `--progress-keep` branch.
- Level filtering: `actions` drops `text` events; `off` is handled in bridge (no
  renderer constructed) — assert bridge wiring leaves the existing path unchanged.
- Throttle: simulate a burst of events and assert edits are coalesced and a final
  flush occurs.

## Open questions

None — levels, format, collapse, cost/duration, per-question message, and throttle
are all settled.
