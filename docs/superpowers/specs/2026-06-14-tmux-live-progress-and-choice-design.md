# tmux backend: live progress, clean output, working choice selector

**Date:** 2026-06-14
**Status:** Approved (design)

## Problem

The bridge's live-progress feature (a single Discord message edited in place that
shows each tool the session runs, then collapses to a one-line summary) and the
clean reply rendering both work on the **stream** backend but not on **tmux** —
which is now the default backend. Concretely:

1. **No live progress on tmux.** `tmuxResponder.Respond` ignores its `onEvent`
   callback (the parameter is named `_`, `internal/session/tmux.go:335`). The tmux
   turn polls the pane until it settles (`capturePoll` → `awaitQuiescence`) and
   returns the whole turn at once, discarding intermediate frames. So `progressView`
   never receives an event, no live message is ever posted, and `pv.finish()` sees
   an empty line set and posts nothing. Only the 👀→✅ reactions work.

2. **Raw / ugly final output.** `extractTurn` strips TUI chrome (borders, input
   box, spinner) but keeps the tool-call lines (`⏺ Tool(args)` and their `⎿`
   continuations) verbatim in the reply, so the final Discord message is a noisy
   dump of tool I/O rather than Claude's prose answer.

3. **Choice selector posts no menu (daemon mode).** When Claude leaves the pane on
   an interactive prompt, the user sees plain numbered text instead of a native
   Discord select menu. The wiring (control socket on both sides, `custom_id`
   carrying the session, daemon routing in `serve.go:138`, `supervisor.go:37`) is
   statically correct, so the cause is runtime and must be reproduced. Leading
   hypothesis: the default `claude --dangerously-skip-permissions` suppresses
   tool-permission prompts entirely, and the prompts that *do* appear (plan-mode /
   AskUserQuestion) may not match `parseChoicePrompt`, so `pending` is never set and
   `postResult` falls through to the text path.

## Goals

- tmux turns emit a `session.Event{Kind:"tool"}` per tool as it appears, driving the
  existing `progressView` exactly like the stream backend.
- The final reply on tmux is Claude's prose only — tool-call blocks removed (they are
  already shown in the live progress message).
- A real interactive choice prompt on tmux, in daemon mode, renders as a native
  Discord select menu whose click is applied in the pane.

## Non-goals

- No change to the `progressView` pipeline, the daemon select-menu routing, or the
  control-socket protocol — those are reused as-is.
- No `text`/reasoning events from tmux (the TUI has no clean reasoning feed); the
  progress message shows tools only on this backend. `--progress full` degrades to
  effectively `actions` for tmux. This is acceptable and documented.

## Design

### Live progress (tmux)

- **`awaitQuiescence` gains `onFrame func(string)`** in `quiesceCfg`: called once per
  *changed* capture with the current pane text. Nil = no-op (existing callers
  unaffected). Quiescence/timeout/busy logic is otherwise unchanged.
- **`session/toolfeed.go` — `parseToolEvents(turnText string) []toolEvent`**: scans
  the lines a turn added and returns the ordered tool invocations. A tool line is the
  TUI bullet form `⏺ Name(args)` / `● Name(args)`; `Name` is the tool, the
  parenthesized text is the detail. `⎿` continuation/result lines are not tools.
- **`tmuxResponder.turn` drives the feed.** It holds a per-turn `emitted int`
  counter. In the `onFrame` callback it computes `newLines(before, frame)`, runs
  `parseToolEvents`, and for every tool beyond `emitted` calls
  `onEvent(session.Event{Kind:"tool", Tool, Detail})`, then advances `emitted`.
  Dedup is positional (count already emitted), so a repainted frame never
  double-emits.
- **`Respond` relays `onEvent`** (today `_`) into `turn` → `capturePoll` → the
  `onFrame` hook. `turn` is shared with the priming loop, which passes a nil
  `onEvent` (no progress for priming).

### Clean final output

- **`extractTurn` drops tool blocks.** A new pass `stripToolBlocks(lines)` removes any
  line that is a tool bullet (`⏺`/`●`) and its following `⎿`-prefixed continuation
  lines, keeping only Claude's prose. Applied after `stripChrome`. The existing
  empty-reply fallback (raw diff) is preserved for the degenerate case.
- **Decision:** tool blocks are removed *completely* from the final reply (not
  condensed) — the live progress message already carries the tool trace, and its
  collapsed summary (`✅ N actions (…)`) is the durable record.

### Choice selector (diagnosis + fix)

- **Reproduce** in daemon mode (`dctl serve` + a tmux session) with a prompt that the
  default `--dangerously-skip-permissions` does *not* suppress (e.g. plan-mode exit,
  or run the session without skip-permissions). Capture the real pane text.
- **Fix to the isolated cause**, most likely one or both of:
  - Broaden `parseChoicePrompt` to the real captured prompt format (box glyphs /
    selector glyph variants / `capture-pane` stripping styling) so `pending` is set.
  - Surface the swallowed `SendChoiceMenu` error / confirm the control socket binds,
    if the menu post itself is failing.
- Keep the numeric-reply text fallback intact for standalone (no control socket) runs.

## Testing

- `toolfeed_test.go`: `parseToolEvents` over fixture captures — single tool, multiple
  tools, tool + `⎿` output, prose-only (no tools), repainted frame (no double-emit).
- `tmux_test.go`: `awaitQuiescence` invokes `onFrame` per changed capture and not on
  unchanged ones; `extractTurn` strips tool blocks leaving prose; empty-reply fallback
  still fires.
- A capture fixture for the real choice-prompt format feeding `parseChoicePrompt`
  (added once the repro pins the format).
- `go test ./...` green; manual daemon verification that the live message updates and
  the select menu appears + applies.

## Files touched

- `internal/session/tmux.go` — `onFrame` in `quiesceCfg`, `turn` feed, `Respond`
  relays `onEvent`, `extractTurn` tool-block stripping.
- `internal/session/toolfeed.go` (new) — `parseToolEvents`.
- `internal/session/choice.go` — broaden detection if the repro requires it.
- `internal/bridge/bridge.go` — surface `SendChoiceMenu` error if that is the cause.
- Tests as above; skill doc note that tmux progress is tools-only.
