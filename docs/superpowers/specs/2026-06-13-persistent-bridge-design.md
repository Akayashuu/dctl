# dctl — Persistent bridge (stream-json) design

**Date:** 2026-06-13
**Status:** Approved

## Problem

The bridge today spawns a **cold** `claude -p --continue <msg>` process for *every*
Discord message (`runCmd` in `cmd/dctl/bridge.go`). Each cold start reloads the
full Claude Code context — system prompt, tool definitions, and the SessionStart
hook injection (the superpowers skill) — before answering.

Measured on this machine, a one-word answer ("PONG") in Haiku cost **$0.0136**
and burned ~26k tokens of overhead (17.6k cache-read + 8.8k cache-creation) for
10 tokens of real input. That overhead is re-paid on every message, inflating
both token cost / quota usage and latency.

## Goal

Hold **one long-lived `claude` process per session**, fed messages over a
documented streaming protocol, so the context stays hot: the ~26k-token overhead
is paid **once at startup** instead of per message. Subsequent turns pay only the
incremental input + output.

## Approach

Use Claude Code's official headless streaming mode (confirmed via `claude --help`
and a live smoke test):

```
claude -p --input-format stream-json --output-format stream-json --verbose
```

- **Input:** newline-delimited JSON on stdin, one user message per line:
  `{"type":"user","message":{"role":"user","content":"<text>"}}`
- **Output:** a stream of JSON events on stdout. Each line has a `type`:
  - `system` (subtype `init` carries `session_id`, `model`; also `hook_*`)
  - `assistant` (content blocks: `thinking`, `text`, tool use)
  - `rate_limit_event`
  - `result` — **marks end of turn.** Carries `subtype` (`success`/error),
    `is_error`, `result` (the final assembled assistant text), `total_cost_usd`,
    `session_id`, `usage`.

The `result` event is the turn delimiter. Its `result` field is the canonical
final text — we return that rather than re-assembling `assistant` text blocks.

## Components

### `cmd/dctl/stream.go` (new)

Pure, testable helpers + a process wrapper.

- `userLine(text string) ([]byte, error)` — marshals one stdin user message
  (newline-terminated). Pure.
- `turnResult` — `{Text string; CostUSD float64; SessionID string; IsError bool; ErrMsg string}`.
- `readTurn(r *bufio.Reader) (turnResult, error)` — reads events until a `result`
  event, ignoring everything else, capturing `session_id`. Pure over a reader.
  **Uses `bufio.Reader.ReadBytes('\n')`, not `bufio.Scanner`** — the `system/init`
  event embeds the entire skill injection and exceeds Scanner's 64 KB token cap.
- `streamSession` — wraps the live process:
  - `Start(ctx, base []string, model, resumeID, dir string) error` — builds the
    argv (`base` + stream flags + optional `--model` + optional `--resume`),
    launches with `cmd.Dir = dir`, wires stdin pipe + stdout `bufio.Reader`.
  - `Send(ctx, text string) (turnResult, error)` — under a mutex (one turn at a
    time per session): writes `userLine`, then `readTurn`. Records `SessionID`.
    Returns an error if the process/stdout has closed (caller restarts).
  - `Close() error` — closes stdin, kills the process.

### `cmd/dctl/bridge.go` (modified)

- New flags: `--stream` (bool, **default true**) and `--model` (string, optional).
- A `responder` seam with two implementations:
  - `streamResponder` (default) — owns a `streamSession`; `Respond` calls `Send`;
    on a send error (process died) it restarts the session with `--resume <last
    session id>` to recover context, then retries once.
  - `oneShotResponder` — the existing `runCmd` behavior, used when `--stream=false`
    (for arbitrary non-claude `--cmd` commands).
- The poll loop is otherwise unchanged (ack reaction → respond → chunked reply →
  done reaction). Only the per-message call swaps `runCmd` for `responder.Respond`.
- When `--stream=true`, the base command is the `--cmd` value (default `claude`);
  the bridge appends the stream flags itself. So `--cmd 'claude --permission-mode
  acceptEdits'` still works (extra claude args preserved).

### `cmd/dctl/serve.go` (modified)

- Default session command changes from `claude -p --continue` to **`claude`**
  (the bridge now supplies `-p` and the stream flags). The supervisor keeps
  passing `session.Cmd` as `--cmd`; `--stream` defaults true so no extra wiring.

## Error handling

- **Process death mid-session:** `Send`/`readTurn` returns an error on EOF;
  `streamResponder` restarts the `streamSession` with `--resume <session_id>` and
  retries the message once. A second failure surfaces as a `⚠️` reply.
- **`result` with `is_error: true`:** returned as a `turnResult` with `IsError`
  set; the bridge posts the error text with the fail reaction.
- **Long events:** handled by `ReadBytes` (no token-size cap).
- **Supervisor:** unchanged. The persistent `claude` lives *inside* the
  `dctl bridge` process; if the whole bridge dies, the supervisor restarts it as
  today.

## Testing (TDD)

- `userLine` — round-trips to the expected JSON shape, newline-terminated.
- `readTurn` — fed canned NDJSON (system/init + assistant thinking + assistant
  text + result) over a `strings.NewReader`, asserts `Text`, `CostUSD`,
  `SessionID`. A second case with `is_error: true`. A third with a 100 KB `init`
  line to prove the no-Scanner-cap requirement.
- `streamSession.Send` — injected with in-memory pipes (a fake "process": a
  goroutine that reads a user line and writes a canned `result`) to exercise the
  write→read turn loop without spawning `claude`.
- Bridge responder selection — `--stream=false` uses one-shot; default uses
  stream. (Light test on argv construction.)

## Out of scope (v1)

- **Live message editing** while the answer streams (would need per-delta Discord
  edits; rate-limit sensitive). v1 posts the full turn on `result`, chunked.
- **Disabling hooks/skills** for bridge sessions to shave the one-time startup
  overhead further (`--settings`/`CLAUDE_CONFIG_DIR`). Persistent process already
  removes the *per-message* overhead; this is a later optimization.
- **`dctl service install`** (cross-OS autostart command) — parked; resumed after
  this lands.
