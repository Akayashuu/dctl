# tmux Live Progress, Clean Output & Choice Selector Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the tmux bridge backend emit live per-tool progress, return clean prose-only replies, and render real interactive prompts as native Discord select menus.

**Architecture:** Add an `onFrame` hook to the tmux quiescence poll so each changed pane capture is parsed for newly-appeared tool-call lines; emit one `session.Event{Kind:"tool"}` per new tool into the existing `progressView`. Strip tool-call blocks from the final reply so only Claude's prose ships. Diagnose and fix the choice-menu post path in daemon mode.

**Tech Stack:** Go, tmux `capture-pane`, existing `internal/session` + `internal/bridge` packages.

---

## File Structure

- `internal/session/toolfeed.go` (new) — `parseToolEvents`: parse tool-call lines from a turn's added text.
- `internal/session/toolfeed_test.go` (new) — unit tests for the parser.
- `internal/session/tmux.go` (modify) — `onFrame` in `quiesceCfg`; `awaitQuiescence` calls it; `turn` drives the feed and relays `onEvent`; `extractTurn` strips tool blocks.
- `internal/session/tmux_test.go` (modify) — `onFrame` invocation + tool-block stripping tests.
- `internal/session/choice.go` (modify, if repro requires) — broaden prompt detection.
- `internal/bridge/bridge.go` (modify, if repro requires) — surface `SendChoiceMenu` error.

---

## Task 1: Parse tool-call lines from a turn capture

**Files:**
- Create: `internal/session/toolfeed.go`
- Test: `internal/session/toolfeed_test.go`

Claude's TUI prints each tool invocation as a bullet line `⏺ Name(args)` (older
builds use `●`). Result/continuation lines are prefixed `⎿` and are NOT tools.

- [ ] **Step 1: Write the failing test**

```go
package session

import "testing"

func TestParseToolEvents(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []toolEvent
	}{
		{
			name: "single tool with args",
			in:   "I'll run the tests.\n⏺ Bash(npm test)\n  ⎿ 12 passed",
			want: []toolEvent{{Tool: "Bash", Detail: "npm test"}},
		},
		{
			name: "multiple tools in order",
			in:   "⏺ Read(src/app.ts)\n  ⎿ 40 lines\n⏺ Edit(src/app.ts)",
			want: []toolEvent{{Tool: "Read", Detail: "src/app.ts"}, {Tool: "Edit", Detail: "src/app.ts"}},
		},
		{
			name: "filled-circle variant",
			in:   "● Grep(pattern)",
			want: []toolEvent{{Tool: "Grep", Detail: "pattern"}},
		},
		{
			name: "prose only, no tools",
			in:   "2 + 2 = 4.\nNothing to run here.",
			want: nil,
		},
		{
			name: "tool with no args",
			in:   "⏺ TodoWrite",
			want: []toolEvent{{Tool: "TodoWrite", Detail: ""}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseToolEvents(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("got %d events %v, want %d %v", len(got), got, len(c.want), c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("event %d: got %+v, want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/session/ -run TestParseToolEvents -v`
Expected: FAIL — `undefined: parseToolEvents` / `undefined: toolEvent`.

- [ ] **Step 3: Write minimal implementation**

```go
package session

import (
	"regexp"
	"strings"
)

// toolEvent is one tool invocation parsed from a tmux pane capture.
type toolEvent struct {
	Tool   string
	Detail string
}

// toolBullets are the glyphs Claude's TUI prints before a tool-call line. The
// current build uses ⏺; older builds use ●.
const toolBullets = "⏺●"

// toolLineRe matches a tool-call bullet line after leading whitespace: the bullet,
// the tool name, and an optional "(args)" group. "⎿" continuation lines never
// match (they carry no bullet).
var toolLineRe = regexp.MustCompile(`^[` + toolBullets + `]\s+([A-Za-z][A-Za-z0-9_]*)\s*(?:\((.*)\))?\s*$`)

// parseToolEvents returns the tool invocations in text, in first-seen order.
// Non-tool prose and ⎿ result lines are ignored.
func parseToolEvents(text string) []toolEvent {
	var out []toolEvent
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		m := toolLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out = append(out, toolEvent{Tool: m[1], Detail: strings.TrimSpace(m[2])})
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/session/ -run TestParseToolEvents -v`
Expected: PASS (all subcases).

- [ ] **Step 5: Commit**

```bash
git add internal/session/toolfeed.go internal/session/toolfeed_test.go
git commit -m "feat(session): parse tool-call lines from tmux pane captures"
```

---

## Task 2: Add an onFrame hook to the quiescence poll

**Files:**
- Modify: `internal/session/tmux.go` (`quiesceCfg`, `awaitQuiescence`)
- Test: `internal/session/tmux_test.go`

`onFrame` fires once per *changed* capture so a caller can stream intermediate
state; unchanged repaints do not re-fire.

- [ ] **Step 1: Write the failing test**

```go
func TestAwaitQuiescenceOnFrame(t *testing.T) {
	frames := []string{"a", "a", "b", "b", "b"}
	i := 0
	cap := func() (string, error) {
		f := frames[i]
		if i < len(frames)-1 {
			i++
		}
		return f, nil
	}
	var seen []string
	_, err := awaitQuiescence(context.Background(), cap, quiesceCfg{
		stable:  3,
		poll:    0,
		timeout: time.Second,
		onFrame: func(s string) { seen = append(seen, s) },
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// onFrame fires only when the capture changes: "a" then "b".
	want := []string{"a", "b"}
	if len(seen) != len(want) {
		t.Fatalf("got frames %v, want %v", seen, want)
	}
	for j := range want {
		if seen[j] != want[j] {
			t.Errorf("frame %d: got %q want %q", j, seen[j], want[j])
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/session/ -run TestAwaitQuiescenceOnFrame -v`
Expected: FAIL — `unknown field 'onFrame' in struct literal`.

- [ ] **Step 3: Add the field and call it**

In `internal/session/tmux.go`, add to `quiesceCfg`:

```go
	// onFrame, when non-nil, is called once per changed capture with the current
	// pane text, so a caller can stream intermediate state (e.g. tool progress).
	onFrame func(string)
```

In `awaitQuiescence`, inside the loop, replace the `cur == last` block so a change
both resets the counter and fires `onFrame`:

```go
		if cur == last {
			same++
		} else {
			same, last = 0, cur
			if cfg.onFrame != nil && cur != "" {
				cfg.onFrame(cur)
			}
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/session/ -run TestAwaitQuiescenceOnFrame -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/session/tmux.go internal/session/tmux_test.go
git commit -m "feat(session): onFrame hook on awaitQuiescence for streaming captures"
```

---

## Task 3: Emit tool events during a tmux turn

**Files:**
- Modify: `internal/session/tmux.go` (`capturePoll`, `turn`, `Respond`)

Relay `Respond`'s `onEvent` through `turn` → `capturePoll`'s `onFrame`, parsing the
diff since `before` and emitting each newly-seen tool exactly once.

- [ ] **Step 1: Thread onEvent through capturePoll**

Change `capturePoll` to accept an `onFrame` callback:

```go
func (t *tmuxResponder) capturePoll(ctx context.Context, onFrame func(string)) (string, error) {
	return awaitQuiescence(ctx, func() (string, error) { return t.capture(ctx) }, quiesceCfg{
		stable:  3,
		poll:    300 * time.Millisecond,
		timeout: t.timeout,
		busy:    paneBusy,
		onFrame: onFrame,
	})
}
```

Update the call in `start` (priming/first paint needs no progress):

```go
	settled, err := t.capturePoll(ctx, nil)
```

- [ ] **Step 2: Drive the feed in turn**

Change `turn`'s signature to take `onEvent func(Event)` and build the frame hook:

```go
func (t *tmuxResponder) turn(ctx context.Context, text string, onEvent func(Event)) (string, error) {
	before := t.baseline
	t.pending = nil // cleared up front; a new choice prompt re-sets it below
	if err := t.typeText(ctx, text); err != nil {
		return "", err
	}
	if out, err := tmuxRunCtx(ctx, "send-keys", "-t", t.sessName, "Enter"); err != nil {
		return "", fmt.Errorf("tmux send-keys Enter: %v: %s", err, out)
	}
	emitted := 0
	var onFrame func(string)
	if onEvent != nil {
		onFrame = func(frame string) {
			tools := parseToolEvents(strings.Join(newLines(before, frame), "\n"))
			for ; emitted < len(tools); emitted++ {
				onEvent(Event{Kind: "tool", Tool: tools[emitted].Tool, Detail: tools[emitted].Detail})
			}
		}
	}
	after, err := t.capturePoll(ctx, onFrame)
	if after != "" {
		t.baseline = after
	}
	...
```

(The rest of `turn` — choice-prompt detection, salvage, `extractTurn` — is unchanged.)

- [ ] **Step 3: Update turn callers**

In `Respond`, relay the callback (rename the `_` param to `onEvent`):

```go
func (t *tmuxResponder) Respond(ctx context.Context, m DctlMessage, onEvent func(Event)) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.started {
		if err := t.start(ctx); err != nil {
			return "", err
		}
	}
	return t.turn(ctx, normalizeNewlines(m.Content), onEvent)
}
```

In `start`'s priming loop and `InjectChoice`, pass `nil`:

```go
		if _, err := t.turn(ctx, p, nil); err != nil {
```
```go
	return t.turn(ctx, normalizeNewlines(value), nil)
```

- [ ] **Step 4: Verify build and existing tests pass**

Run: `go build ./... && go test ./internal/session/ -v`
Expected: PASS (no regressions; existing tmux tests still green).

- [ ] **Step 5: Commit**

```bash
git add internal/session/tmux.go
git commit -m "feat(session): emit per-tool progress events from the tmux backend"
```

---

## Task 4: Strip tool blocks from the final reply

**Files:**
- Modify: `internal/session/tmux.go` (`extractTurn` / new `stripToolBlocks`)
- Test: `internal/session/tmux_test.go`

The live progress message already shows the tools, so the final reply should be
Claude's prose only — drop tool bullet lines (`⏺`/`●`) and their `⎿` continuations.

- [ ] **Step 1: Write the failing test**

```go
func TestExtractTurnStripsToolBlocks(t *testing.T) {
	before := " ▐▛███▜▌  Claude Code\n> hi"
	after := before + "\nI'll run the tests.\n⏺ Bash(npm test)\n  ⎿ 12 passed\n  ⎿ done\nAll green."
	got := extractTurn(before, after)
	want := "I'll run the tests.\nAll green."
	if got != want {
		t.Fatalf("extractTurn = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/session/ -run TestExtractTurnStripsToolBlocks -v`
Expected: FAIL — output still contains the `⏺`/`⎿` lines.

- [ ] **Step 3: Implement stripToolBlocks and call it in extractTurn**

Add to `internal/session/tmux.go`:

```go
// stripToolBlocks drops tool-call bullet lines (⏺/●) and their ⎿ continuation
// lines, leaving only Claude's prose — the tools are already shown in the live
// progress message, so the final reply need not repeat them.
func stripToolBlocks(lines []string) []string {
	var out []string
	inTool := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.ContainsAny(firstRune(t), toolBullets) {
			inTool = true
			continue
		}
		if inTool && strings.HasPrefix(t, "⎿") {
			continue // continuation/result of the tool above
		}
		inTool = false
		out = append(out, l)
	}
	return out
}

// firstRune returns the first rune of s as a string ("" if empty), so a multibyte
// bullet glyph can be tested with ContainsAny without indexing bytes.
func firstRune(s string) string {
	for _, r := range s {
		return string(r)
	}
	return ""
}
```

Update `extractTurn` to apply it after `stripChrome`:

```go
func extractTurn(before, after string) string {
	lines := stripToolBlocks(stripChrome(newLines(before, after)))
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/session/ -run 'TestExtractTurn' -v`
Expected: PASS (new test + any existing extractTurn tests still green).

- [ ] **Step 5: Commit**

```bash
git add internal/session/tmux.go internal/session/tmux_test.go
git commit -m "feat(session): strip tool blocks from tmux final reply"
```

---

## Task 5: Diagnose & fix the choice select menu (daemon mode)

**Files:**
- Modify (likely): `internal/bridge/bridge.go` (`postResult`), `internal/session/choice.go`
- Test: `internal/session/choice_test.go` (fixture once format confirmed)

The wiring is statically correct, so reproduce first to isolate the runtime cause.

- [ ] **Step 1: Reproduce in daemon mode**

```bash
go build -o /tmp/dctl . && DCTL_INSTANCE_ID=verif /tmp/dctl serve 2>/tmp/dctl-serve.log &
```
Create a tmux-backed session, then send a message that triggers a real interactive
prompt the default `--dangerously-skip-permissions` does NOT suppress (e.g. an
ExitPlanMode confirmation, or configure the session command without skip-permissions
so a tool-permission box appears). Observe Discord: menu vs plain text.

- [ ] **Step 2: Capture the live prompt format**

```bash
tmux capture-pane -p -S - -t dctl-verif-<channel> > /tmp/prompt-capture.txt
```
Compare against `internal/session/testdata/claude_choice.txt`. Confirm whether
`parseChoicePrompt` matches the live capture:

```bash
go test ./internal/session/ -run TestParseChoicePrompt -v
```

- [ ] **Step 3: Identify which branch failed**

Add a temporary verbose log in `postResult` to see which gate dropped the menu:

```go
	if o.ControlSocket != "" {
		if ca, ok := resp.(session.ChoiceAware); ok {
			if pc, has := ca.PendingChoice(); has {
				...
			} else {
				logf(o.Verbose, "no pending choice — text fallback")
			}
		}
	} else {
		logf(o.Verbose, "no control socket — text fallback")
	}
```
Run the daemon with the bridge in verbose mode (confirm `-v` reaches the child via
the supervisor; if not, add it temporarily) and re-trigger the prompt. The log
pinpoints the cause: missing pending (detection), missing socket (wiring), or a
`SendChoiceMenu` error already logged at `internal/bridge/bridge.go:255`.

- [ ] **Step 4: Apply the targeted fix**

Per the isolated cause:
- **Detection miss** → broaden `parseChoicePrompt`/`optionRe`/`selectorGlyphs` in
  `internal/session/choice.go` to the live glyphs/box, and add a fixture test
  capturing the real format to `claude_choice.txt` (or a new testdata file).
- **`SendChoiceMenu` error** → fix the request (e.g. component shape) and keep the
  error logged, not swallowed.
- **Missing socket** → fix the supervisor/bridge socket wiring for the session.

- [ ] **Step 5: Verify and commit**

Re-trigger the prompt in daemon mode: the native select menu must appear and a click
must apply in the pane. Remove temporary logs (or keep behind `-v`). Then:

```bash
go test ./... && git add -A && git commit -m "fix(choice): render native select menu for tmux prompts in daemon mode"
```

---

## Task 6: Full suite, fmt, and docs note

**Files:**
- Modify: `.claude/skills/dctl-bridge/SKILL.md` (progress note)

- [ ] **Step 1: Run the whole suite and gofmt**

Run: `gofmt -l internal/ && go test ./...`
Expected: no files listed by gofmt; all packages PASS.

- [ ] **Step 2: Document the tmux progress behavior**

In `.claude/skills/dctl-bridge/SKILL.md`, in the "Feedback while it works" section,
add that on the tmux backend the live progress message shows tool actions (no
reasoning text — the TUI has no clean reasoning feed), so `--progress full` behaves
as `actions` there.

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "docs(skill): note tmux progress is tool-actions only"
```

---

## Self-Review Notes

- **Spec coverage:** Live progress (Tasks 1–3), clean output (Task 4), choice selector
  (Task 5), tests + docs (Task 6). All spec goals mapped.
- **Type consistency:** `toolEvent{Tool, Detail}`, `parseToolEvents`, `quiesceCfg.onFrame`,
  `capturePoll(ctx, onFrame)`, `turn(ctx, text, onEvent)` used consistently across tasks.
  `Event{Kind, Tool, Detail}` matches `internal/session/stream.go:86`.
- **No placeholders:** every code step shows real code; Task 5 is intentionally a
  diagnose-then-fix task with concrete repro commands and branch-specific fixes, since
  the root cause is runtime-determined.
