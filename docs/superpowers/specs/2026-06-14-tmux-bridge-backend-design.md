# Design: tmux bridge backend

**Date:** 2026-06-14
**Status:** Approved (brainstorming)

## Goal

Let a Discord channel drive the **interactive `claude` TUI** running inside a
**tmux** session, with dctl acting as a text passthrough: it types human Discord
messages into the pane (`tmux send-keys`) and reads what Claude prints back
(`tmux capture-pane`), posting the new text as a threaded reply — **text only,
no screenshots, no ANSI**.

This is a third bridge *backend* alongside the existing `stream` (stream-json)
and `oneshot` backends. The bridge loop, chunking, reactions, allowlist,
participants journal, and supervisor are all reused unchanged.

### Why tmux (and the trade-off we accept)

- tmux runs the **real interactive `claude` TUI** (banner, input box, permission
  prompts, choice menus) — not the headless `-p` mode. A human can
  `tmux attach -t <session>` and land in the *same live Claude session* dctl is
  driving.
- The agent (`claude`) is identical to every other backend: same engine, same
  auth (subscription or `ANTHROPIC_API_KEY`), same agentic loop, same token
  cost. tmux is a dumb container; it adds **zero** Anthropic tokens.
- For a **human-facing** bridge the captured pane text goes to a person on
  Discord, not into an LLM context, so there is **no LLM token cost** for the
  output either. The cost of tmux is *not* tokens — it is engineering effort
  (scraping a redrawing TUI) and Discord message volume (must not spam).
- Trade-off accepted: a TUI redraws the whole screen, so capturing it yields
  chrome (input box, status line, borders, spinner). We strip the known chrome
  with targeted patterns rather than perfectly reconstructing prose. Robust
  first, refinable later.

## Non-goals (v1)

- **No interactive permission handling.** Claude is launched with
  `--dangerously-skip-permissions`, exactly like the current bridge default, so
  there are no permission prompts to detect. Rendering prompts/menus as Discord
  buttons (the tmuxcord approach, via regex detection) is an explicit **phase 2**.
- **No ANSI / colored output / screenshots.** `capture-pane -p` only.
- **No live progress events.** The `--progress` feature stays inert for the tmux
  backend in v1; only the final settled reply is posted. (YAGNI — revisit if the
  per-turn latency makes silence feel bad.)

## Architecture

The anchor is the existing `session.Responder` interface
(`internal/session/stream.go`):

```go
type Responder interface {
    Respond(ctx context.Context, m DctlMessage, onEvent func(Event)) (string, error)
    Close() error
}
```

There are already two implementations (`oneShotResponder`, `streamResponder`).
We add a third: **`tmuxResponder`** in a new file `internal/session/tmux.go`.

`bridge.Run` is unchanged: it calls `resp.Respond(...)` and posts the returned
string. The only change inside the bridge package is selecting the backend.

### Backend selection

Today `NewResponder(ctx, stream bool, cmd, model, oneShot)` picks stream vs
one-shot from a bool. We introduce an explicit backend string to make room for a
third option without overloading the bool.

- Add `Backend string` to `bridge.Options` (`"stream"` | `"oneshot"` | `"tmux"`).
- Keep the `--stream` flag for backward compatibility, mapped to the backend:
  - `--backend` set → wins.
  - else `--stream=true` (default) → `"stream"`; `--stream=false` → `"oneshot"`.
- `NewResponder` grows a backend parameter (or a small `NewResponderForBackend`)
  and returns `*tmuxResponder` for `"tmux"`.

### tmuxResponder

One tmux session per bridge (i.e. per Discord channel / dctl session). Holds:

- `sessName string` — tmux session name, derived from the channel id and
  namespaced by `DCTL_INSTANCE_ID` (same scheme as worktrees) so multiple
  daemons don't collide. e.g. `dctl-<instance>-<channel>`.
- `dir string` — working directory (the session worktree), passed to the launch.
- `cmd []string` — the program to run in the pane (default `claude
  --dangerously-skip-permissions`, overridable via `--cmd`).
- `lastCapture string` — the cleaned pane text after the previous turn, used as
  the diff baseline.
- `started bool` — lazy-start guard.

All tmux interaction is via `exec.Command("tmux", ...)` (no new Go deps — tmux is
an external binary, consistent with dctl's dependency-free stance). If `tmux` is
not on PATH, `Respond`/lazy-start returns a clear error.

## Message cycle (`tmuxResponder.Respond`)

Exactly one persistent `claude` per session — **not relaunched per message**.

1. **Lazy-start (first message only):**
   `tmux new-session -d -s <sessName> -x 200 -y 50` then send the launch command,
   OR launch directly:
   `tmux new-session -d -s <sessName> -x 200 -y 50 '<cmd>'` with `-c <dir>` for
   the working directory. Fixed width (`-x 200`) keeps line wrapping stable so
   the diff is clean. Wait for the pane to reach first quiescence (Claude's
   prompt is ready) before sending the user's first message.
2. **Pre-snapshot:** `tmux capture-pane -p -t <sessName>` → current screen.
3. **Send input:**
   `tmux send-keys -t <sessName> -l <text>` (literal, so the message is not
   interpreted as key chords), then `tmux send-keys -t <sessName> Enter`.
   Multi-line messages: send with literal newlines or per-line; submit at the end.
4. **Quiescence wait:** poll `capture-pane -p` every ~300 ms; consider the turn
   done when consecutive captures are **identical** for ~700 ms. Hard timeout
   (default 5 min, configurable) guards against a stuck pane.
5. **Diff:** new capture minus the pre-snapshot → only the lines Claude added
   this turn.
6. **Strip chrome (see below).**
7. **Return** the cleaned string. The bridge loop posts it as a threaded reply,
   chunked at 2000 chars (existing `chunk`).

`Close()` runs `tmux kill-session -t <sessName>`, which terminates `claude`.
Crash recovery is the supervisor's job (it already restarts the bridge child),
which yields a fresh tmux session + fresh `claude`; v1 does not attempt
`tmux`-level reattach to a survived session.

## Chrome stripping

Pragmatic, pattern-based — strip the **known** TUI furniture, do not attempt
perfect prose reconstruction:

- Drop box-drawing border lines (lines that are only `╭╮╰╯│─└┘┌┐ ` etc.).
- Drop the input box: the line containing the `>` prompt and its frame.
- Drop the status line(s) (e.g. `⏵⏵ accept edits on …`, token/context counters,
  `? for shortcuts`).
- Drop spinner/working lines (e.g. lines matching a known spinner glyph set +
  `(esc to interrupt)`).
- Collapse runs of blank lines.

Implemented as a small ordered list of line predicates in `tmux.go`, unit-tested
against captured fixture screens (real `capture-pane` dumps checked into the
test). If everything is stripped to empty, fall back to posting the raw diff so a
turn is never silently lost (and log it for fixture improvement).

## CLI surface

`dctl bridge`:

- New flag `--backend stream|oneshot|tmux` (default derived from `--stream` for
  back-compat).
- `--cmd` reused: in tmux mode it is the program launched in the pane (default
  `claude --dangerously-skip-permissions`).
- New flag `--tmux-timeout` (turn quiescence hard cap, default `5m`).
- `--model` reused: appended to the tmux launch command when set.
- `--progress` accepted but inert for tmux in v1.

## serve / `/session create`

`/session create` already starts a `dctl bridge` child via the supervisor. Add a
backend choice so a session can be created as tmux-backed:

- Extend the slash command with an optional `backend:` option
  (`stream` default | `tmux`), surfaced in the autocomplete alongside the
  existing model × effort matrix.
- The supervisor passes `--backend tmux` (and the worktree `dir`) to the bridge
  child. Session state records the backend so a daemon restart respawns it the
  same way.
- tmux session naming uses the same instance namespacing as worktrees, so
  `/session close` (which already stops the bridge child) plus
  `tmux kill-session` on `Close()` fully tears it down.

## Components & boundaries

| Unit | Responsibility | Depends on |
|------|----------------|------------|
| `tmuxResponder` (`internal/session/tmux.go`) | one persistent tmux+claude session; send/capture/quiescence/diff per turn | `os/exec`, `tmux` binary |
| chrome stripper (`tmux.go`, pure funcs) | turn a raw pane diff into clean prose | none (pure, unit-tested) |
| `NewResponder` backend select | map backend string → responder impl | session impls |
| `bridge.Options.Backend` + flag wiring | expose backend to CLI/serve | bridge, cmd/dctl |
| serve `/session create backend:` | create tmux-backed sessions | serve, supervisor, state |

## Testing

- **Chrome stripper:** table tests over checked-in `capture-pane` fixtures
  (shell idle, claude idle prompt, claude mid-answer, claude done) → expected
  clean text.
- **Diff logic:** pre/post capture pairs → expected new lines only.
- **Quiescence:** injected capture sequence (changing then stable) → settles at
  the right point; timeout path covered.
- **Backend selection:** `--backend`/`--stream` mapping table test (mirrors the
  existing `bridge_flags_test.go` style).
- **tmuxResponder against real tmux:** an integration test gated on `tmux`
  presence (skip if absent) that launches a trivial `bash` pane (not `claude`),
  sends `echo hi`, and asserts the diff contains `hi` — exercises the real
  send-keys/capture/quiescence path without needing a Claude login in CI.

## Open / phase 2

- Interactive permission prompts & choice menus → Discord buttons (regex
  detection on the pane, send the choice via `send-keys`).
- Live progress for tmux (stream partial captures as the turn runs).
- Reattach to a survived tmux session after a bridge child crash instead of
  starting fresh.
