# Bridge Live Progress Feedback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface a Claude turn's intermediate tool uses and reasoning text into the Discord channel as one live-updating progress message per question, at a detail level chosen when the bridge launches.

**Architecture:** The stream-json reader (`internal/session/stream.go`) already sees every event but keeps only the terminal `result`. We thread an `onEvent` callback through `readTurn` → `streamSession.Send` → `Responder.Respond` so intermediate `assistant` blocks are emitted as `Event`s. The bridge feeds those into a new `progressView` (`internal/bridge/progress.go`) that renders a capped, throttled, live-edited Discord message via `UpsertStatusMessage`, then collapses it to a one-line summary on completion.

**Tech Stack:** Go, standard library only (`encoding/json`, `bufio`, `strings`, `time`, `fmt`). Tests use Go's `testing` package with canned stream-json strings and injected fakes.

---

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `internal/session/stream.go` | Parse stream-json, emit intermediate events | Modify: add `Event`, `toolDetail`, callback through `readTurn`/`Send`/`Respond` |
| `internal/session/stream_test.go` | Stream parsing tests | Modify: update call sites to pass `nil`; add event-emission tests |
| `internal/bridge/progress.go` | Render + throttle + post the live progress message | **Create** |
| `internal/bridge/progress_test.go` | Renderer/summary/throttle tests | **Create** |
| `internal/bridge/bridge.go` | Wire `progressView` into the per-message loop | Modify: new `Options` fields, per-message progress wiring |
| `cmd/dctl/bridge.go` | CLI flags | Modify: add `--progress`, `--progress-keep` |
| `~/.claude/skills/dctl-bridge/SKILL.md` | User docs | Modify: document the flags |

**Event type (defined in Task 1, used everywhere downstream):**

```go
// Event is one intermediate stream occurrence surfaced to a progress consumer.
type Event struct {
	Kind    string  // "tool" | "text" | "result"
	Tool    string  // tool name (Kind == "tool")
	Detail  string  // tool: salient input field; text: the assistant text
	Cost    float64 // Kind == "result": total_cost_usd
	IsError bool    // Kind == "result"
}
```

---

## Task 1: Emit intermediate events from `readTurn`

**Files:**
- Modify: `internal/session/stream.go`
- Test: `internal/session/stream_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/session/stream_test.go`:

```go
func TestReadTurnEmitsEvents(t *testing.T) {
	canned := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"je regarde"}]},"session_id":"s"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"git status"}}]},"session_id":"s"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"stream.go"}}]},"session_id":"s"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"done","total_cost_usd":0.04,"session_id":"s"}`,
	}, "\n") + "\n"

	var got []Event
	tr, err := readTurn(bufio.NewReader(strings.NewReader(canned)), func(e Event) { got = append(got, e) })
	if err != nil {
		t.Fatal(err)
	}
	if tr.Text != "done" {
		t.Fatalf("text = %q, want done", tr.Text)
	}
	want := []Event{
		{Kind: "text", Detail: "je regarde"},
		{Kind: "tool", Tool: "Bash", Detail: "git status"},
		{Kind: "tool", Tool: "Read", Detail: "stream.go"},
		{Kind: "result", Cost: 0.04, IsError: false},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestReadTurnNilCallback(t *testing.T) {
	canned := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"x"}}]}}` + "\n" +
		`{"type":"result","is_error":false,"result":"ok","session_id":"s"}` + "\n"
	tr, err := readTurn(bufio.NewReader(strings.NewReader(canned)), nil)
	if err != nil {
		t.Fatalf("nil callback must not panic: %v", err)
	}
	if tr.Text != "ok" {
		t.Fatalf("text = %q, want ok", tr.Text)
	}
}
```

Also update the three existing `readTurn(...)` call sites in this file to pass `nil`:
- `TestReadTurnSuccess`: `readTurn(bufio.NewReader(strings.NewReader(canned)), nil)`
- `TestReadTurnError`: `readTurn(bufio.NewReader(strings.NewReader(canned)), nil)`
- `TestReadTurnHandlesHugeLine`: `readTurn(bufio.NewReader(strings.NewReader(canned)), nil)`

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shan/dev/dctl && go test ./internal/session/ -run TestReadTurn -v`
Expected: compile error / FAIL — `readTurn` takes 1 arg, `Event` undefined.

- [ ] **Step 3: Write minimal implementation**

In `internal/session/stream.go`, add the `Event` type and `toolDetail` helper (place near `streamEvent`):

```go
// Event is one intermediate stream occurrence surfaced to a progress consumer.
type Event struct {
	Kind    string  // "tool" | "text" | "result"
	Tool    string  // tool name (Kind == "tool")
	Detail  string  // tool: salient input field; text: the assistant text
	Cost    float64 // Kind == "result": total_cost_usd
	IsError bool    // Kind == "result"
}

// contentBlock is one block of an assistant message's content array.
type contentBlock struct {
	Type  string          `json:"type"` // "text" | "tool_use" | "thinking" | ...
	Text  string          `json:"text"`
	Name  string          `json:"name"`  // tool name (tool_use)
	Input json.RawMessage `json:"input"` // tool input (tool_use)
}

// toolDetail extracts the most informative single field from a tool's input
// (command for Bash, file_path for Read/Edit, etc.) for a one-line summary.
func toolDetail(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	for _, k := range []string{"command", "file_path", "path", "pattern", "query", "url", "description", "prompt"} {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
```

Extend `streamEvent` to capture assistant content:

```go
type streamEvent struct {
	Type         string  `json:"type"`
	SessionID    string  `json:"session_id"`
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Message      struct {
		Content []contentBlock `json:"content"`
	} `json:"message"`
}
```

Replace `readTurn` with the callback-aware version:

```go
// readTurn consumes stream-json events until the terminal `result` event. When
// onEvent is non-nil it emits an Event per intermediate assistant block (tool
// uses and text) and a terminal "result" event carrying cost. It uses ReadBytes
// (not bufio.Scanner) because the system/init event can exceed Scanner's 64 KB cap.
func readTurn(r *bufio.Reader, onEvent func(Event)) (turnResult, error) {
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var ev streamEvent
			if json.Unmarshal(line, &ev) == nil {
				switch ev.Type {
				case "assistant":
					if onEvent != nil {
						for _, b := range ev.Message.Content {
							switch b.Type {
							case "text":
								if t := strings.TrimSpace(b.Text); t != "" {
									onEvent(Event{Kind: "text", Detail: t})
								}
							case "tool_use":
								onEvent(Event{Kind: "tool", Tool: b.Name, Detail: toolDetail(b.Input)})
							}
						}
					}
				case "result":
					if onEvent != nil {
						onEvent(Event{Kind: "result", Cost: ev.TotalCostUSD, IsError: ev.IsError})
					}
					tr := turnResult{
						Text:      ev.Result,
						CostUSD:   ev.TotalCostUSD,
						SessionID: ev.SessionID,
						IsError:   ev.IsError,
					}
					if ev.IsError {
						tr.ErrMsg = ev.Result
					}
					return tr, nil
				}
			}
		}
		if err != nil {
			return turnResult{}, err
		}
	}
}
```

Note: `streamSession.Send` (Task 2) currently calls `readTurn(s.out)` — leave it broken until Task 2; this task only needs the package to compile for the `readTurn` tests. Update the `Send` call to `readTurn(s.out, nil)` now so the package compiles:

```go
	tr, err := readTurn(s.out, nil)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/shan/dev/dctl && go test ./internal/session/ -v`
Expected: PASS (all existing tests + the two new ones).

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add internal/session/stream.go internal/session/stream_test.go
/usr/bin/git commit -m "feat(session): emit intermediate tool/text events from readTurn"
```

---

## Task 2: Thread the callback through Send and the Responder interface

**Files:**
- Modify: `internal/session/stream.go`
- Test: `internal/session/stream_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/session/stream_test.go`, update `TestStreamSessionSend` to pass a capturing callback and assert it receives the tool event:

```go
func TestStreamSessionSend(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	go func() {
		br := bufio.NewReader(stdinR)
		if _, err := br.ReadBytes('\n'); err != nil {
			return
		}
		io.WriteString(stdoutW,
			`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`+"\n"+
				`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`+"\n"+
				`{"type":"result","subtype":"success","is_error":false,"result":"hello back","total_cost_usd":0.002,"session_id":"abc"}`+"\n")
		stdoutW.Close()
	}()

	s := newStreamSession(stdinW, stdoutR)
	var got []Event
	tr, err := s.Send("hello", func(e Event) { got = append(got, e) })
	if err != nil {
		t.Fatal(err)
	}
	if tr.Text != "hello back" {
		t.Fatalf("text = %q, want 'hello back'", tr.Text)
	}
	if s.sessID != "abc" {
		t.Fatalf("session id not recorded: %q", s.sessID)
	}
	if len(got) < 1 || got[0].Kind != "tool" || got[0].Tool != "Bash" {
		t.Fatalf("expected a Bash tool event, got %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shan/dev/dctl && go test ./internal/session/ -run TestStreamSessionSend -v`
Expected: compile error — `Send` takes 1 arg.

- [ ] **Step 3: Write minimal implementation**

In `internal/session/stream.go`, change `Send` to accept and forward the callback:

```go
// Send writes one user message and reads back the full assistant turn, emitting
// intermediate events to onEvent (nil = none). An error means the stream closed
// (process died) — the caller should restart.
func (s *streamSession) Send(text string, onEvent func(Event)) (turnResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	line, err := userLine(text)
	if err != nil {
		return turnResult{}, err
	}
	if _, err := s.stdin.Write(line); err != nil {
		return turnResult{}, err
	}
	tr, err := readTurn(s.out, onEvent)
	if err != nil {
		return tr, err
	}
	if tr.SessionID != "" {
		s.sessID = tr.SessionID
	}
	return tr, nil
}
```

Change the `Responder` interface and both implementations:

```go
// Responder turns one Discord message into a reply string, optionally emitting
// intermediate progress events. Two implementations: a persistent stream-json
// claude session, or a per-message one-shot command.
type Responder interface {
	Respond(ctx context.Context, m DctlMessage, onEvent func(Event)) (string, error)
	Close() error
}
```

```go
func (o *oneShotResponder) Respond(ctx context.Context, m DctlMessage, _ func(Event)) (string, error) {
	return o.run(ctx, m)
}
```

In `streamResponder.Respond`, accept `onEvent` and pass it to both `Send` calls:

```go
func (r *streamResponder) Respond(ctx context.Context, m DctlMessage, onEvent func(Event)) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sess == nil {
		s, err := startStreamSession(r.ctx, r.base, r.model, "", r.dir)
		if err != nil {
			return "", err
		}
		r.sess = s
	}
	tr, err := r.sess.Send(m.Content, onEvent)
	if err != nil {
		// Process likely died: restart with the last session id and retry once.
		resume := r.sess.sessID
		_ = r.sess.Close()
		s, startErr := startStreamSession(r.ctx, r.base, r.model, resume, r.dir)
		if startErr != nil {
			return "", startErr
		}
		r.sess = s
		if tr, err = r.sess.Send(m.Content, onEvent); err != nil {
			return "", err
		}
	}
	if tr.IsError {
		return tr.Text, errFromTurn(tr)
	}
	return tr.Text, nil
}
```

This breaks the `bridge.go` call site (`resp.Respond(ctx, ...)`) — fixed in Task 4. To keep `internal/session` self-contained and green, that's fine: `go test ./internal/session/` compiles independently.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/shan/dev/dctl && go test ./internal/session/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add internal/session/stream.go internal/session/stream_test.go
/usr/bin/git commit -m "feat(session): thread progress callback through Send and Responder"
```

---

## Task 3: progressView renderer (pure render + summary)

**Files:**
- Create: `internal/bridge/progress.go`
- Test: `internal/bridge/progress_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/bridge/progress_test.go`:

```go
package bridge

import (
	"strings"
	"testing"
	"time"

	"github.com/vskstudio/dctl/internal/session"
)

func TestProgressRenderCapsLines(t *testing.T) {
	pv := newProgressView(nil, "full", false, time.Now())
	for i := 0; i < 20; i++ {
		pv.add(session.Event{Kind: "tool", Tool: "Read", Detail: "f.go"})
	}
	out := pv.render()
	if !strings.HasPrefix(out, "⏳ en cours…\n") {
		t.Fatalf("missing header: %q", out)
	}
	if !strings.Contains(out, "…\n") {
		t.Fatal("expected elision marker for >maxLines")
	}
	// header + elision + maxLines content lines
	if n := strings.Count(out, "\n"); n != maxLines+1 {
		t.Fatalf("line count = %d, want %d", n, maxLines+1)
	}
}

func TestProgressActionsLevelDropsText(t *testing.T) {
	pv := newProgressView(nil, "actions", false, time.Now())
	pv.add(session.Event{Kind: "text", Detail: "thinking out loud"})
	pv.add(session.Event{Kind: "tool", Tool: "Bash", Detail: "ls"})
	out := pv.render()
	if strings.Contains(out, "thinking out loud") {
		t.Fatal("actions level must drop text events")
	}
	if !strings.Contains(out, "Bash · ls") {
		t.Fatalf("expected Bash line, got %q", out)
	}
}

func TestProgressSummary(t *testing.T) {
	pv := newProgressView(nil, "full", false, time.Now())
	pv.add(session.Event{Kind: "tool", Tool: "Bash", Detail: "a"})
	pv.add(session.Event{Kind: "tool", Tool: "Bash", Detail: "b"})
	pv.add(session.Event{Kind: "tool", Tool: "Read", Detail: "x"})
	pv.add(session.Event{Kind: "text", Detail: "noise"}) // text not counted
	pv.add(session.Event{Kind: "result", Cost: 0.04})
	s := pv.summary(false)
	if !strings.HasPrefix(s, "✅ 3 actions (Bash×2, Read)") {
		t.Fatalf("summary = %q", s)
	}
	if !strings.Contains(s, "$0.04") {
		t.Fatalf("expected cost in summary: %q", s)
	}
}

func TestProgressSummaryError(t *testing.T) {
	pv := newProgressView(nil, "full", false, time.Now())
	pv.add(session.Event{Kind: "tool", Tool: "Bash", Detail: "a"})
	if s := pv.summary(true); !strings.HasPrefix(s, "⚠️ 1 action") {
		t.Fatalf("summary = %q", s)
	}
}

func TestProgressPostsThrottledThenFlushes(t *testing.T) {
	var posts []string
	post := func(id, content string) (string, error) {
		posts = append(posts, content)
		return "msg-1", nil
	}
	pv := newProgressView(post, "full", false, time.Now())
	// Rapid adds: throttle coalesces — first add posts, immediate next ones do not.
	pv.add(session.Event{Kind: "tool", Tool: "Bash", Detail: "a"})
	pv.add(session.Event{Kind: "tool", Tool: "Read", Detail: "b"})
	if len(posts) != 1 {
		t.Fatalf("expected 1 throttled post, got %d", len(posts))
	}
	// finish forces a final flush (collapse summary).
	pv.finish(false)
	if len(posts) != 2 {
		t.Fatalf("expected final flush, got %d posts", len(posts))
	}
	if !strings.HasPrefix(posts[1], "✅") {
		t.Fatalf("final post should be summary, got %q", posts[1])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shan/dev/dctl && go test ./internal/bridge/ -run TestProgress -v`
Expected: compile error — `newProgressView`, `maxLines` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `internal/bridge/progress.go`:

```go
package bridge

import (
	"fmt"
	"strings"
	"time"

	"github.com/vskstudio/dctl/internal/session"
)

// maxLines caps the live progress message body so it stays readable and well
// under Discord's 2000-char limit; older lines are elided with a leading "…".
const maxLines = 15

// progressInterval throttles live edits so a tool-heavy turn doesn't hammer
// Discord's per-channel edit rate limit. Events are coalesced between edits.
const progressInterval = 1500 * time.Millisecond

// progressView accumulates one turn's activity and pushes it to a single
// live-updating Discord message, then collapses it to a one-line summary.
// post creates (empty id) or edits (non-empty id) the message and returns its id.
type progressView struct {
	post     func(msgID, content string) (string, error)
	level    string // "actions" | "full"
	keep     bool
	start    time.Time
	lines    []string
	counts   map[string]int
	order    []string // tool names in first-seen order, for the summary
	cost     float64
	msgID    string
	lastEdit time.Time
	dirty    bool
}

func newProgressView(post func(string, string) (string, error), level string, keep bool, start time.Time) *progressView {
	return &progressView{post: post, level: level, keep: keep, start: start, counts: map[string]int{}}
}

// add records one event and flushes (throttled) if it produced a visible line.
func (p *progressView) add(ev session.Event) {
	switch ev.Kind {
	case "result":
		p.cost = ev.Cost
		return
	case "text":
		if p.level != "full" {
			return
		}
		p.lines = append(p.lines, "💭 "+clip(flatten(ev.Detail), 120))
	case "tool":
		if _, seen := p.counts[ev.Tool]; !seen {
			p.order = append(p.order, ev.Tool)
		}
		p.counts[ev.Tool]++
		line := emojiFor(ev.Tool) + " " + ev.Tool
		if d := clip(flatten(ev.Detail), 120); d != "" {
			line += " · " + d
		}
		p.lines = append(p.lines, line)
	default:
		return
	}
	p.dirty = true
	p.flush(false)
}

// flush posts the current view if dirty and (force or the throttle interval has
// elapsed). Best-effort: a post error is swallowed so it never blocks the reply.
func (p *progressView) flush(force bool) {
	if !p.dirty || p.post == nil {
		return
	}
	if !force && !p.lastEdit.IsZero() && time.Since(p.lastEdit) < progressInterval {
		return
	}
	id, err := p.post(p.msgID, p.render())
	if err != nil {
		return
	}
	p.msgID = id
	p.lastEdit = time.Now()
	p.dirty = false
}

// finish renders the terminal state: a collapsed one-line summary by default, or
// (keep) a final flush of the full running list.
func (p *progressView) finish(failed bool) {
	if len(p.lines) == 0 {
		return // nothing happened (e.g. instant reply) — no progress message
	}
	if p.keep {
		p.dirty = true
		p.flush(true)
		return
	}
	if p.post != nil {
		p.post(p.msgID, p.summary(failed))
	}
}

func (p *progressView) render() string {
	lines := p.lines
	var b strings.Builder
	b.WriteString("⏳ en cours…\n")
	if len(lines) > maxLines {
		b.WriteString("…\n")
		lines = lines[len(lines)-maxLines:]
	}
	b.WriteString(strings.Join(lines, "\n"))
	return b.String()
}

func (p *progressView) summary(failed bool) string {
	icon := "✅"
	if failed {
		icon = "⚠️"
	}
	total := 0
	for _, n := range p.counts {
		total += n
	}
	parts := make([]string, 0, len(p.order))
	for _, name := range p.order {
		if n := p.counts[name]; n > 1 {
			parts = append(parts, fmt.Sprintf("%s×%d", name, n))
		} else {
			parts = append(parts, name)
		}
	}
	s := fmt.Sprintf("%s %d action%s", icon, total, plural(total))
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}
	s += fmt.Sprintf(" · %ds", int(time.Since(p.start).Round(time.Second)/time.Second))
	if p.cost > 0 {
		s += " · " + formatCost(p.cost)
	}
	return s
}

func plural(n int) string {
	if n > 1 {
		return "s"
	}
	return ""
}

func formatCost(c float64) string {
	if c < 0.01 {
		return fmt.Sprintf("$%.4f", c)
	}
	return fmt.Sprintf("$%.2f", c)
}

func emojiFor(tool string) string {
	switch tool {
	case "Read":
		return "📖"
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return "✏️"
	case "Grep", "Glob":
		return "🔎"
	case "Task", "Agent":
		return "🤖"
	case "WebFetch", "WebSearch":
		return "🌐"
	case "TodoWrite":
		return "📝"
	default: // Bash and anything unrecognized
		return "🔧"
	}
}

// flatten collapses all whitespace runs (incl. newlines) into single spaces.
func flatten(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clip truncates s to n runes, appending "…" when cut.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/shan/dev/dctl && go test ./internal/bridge/ -run TestProgress -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add internal/bridge/progress.go internal/bridge/progress_test.go
/usr/bin/git commit -m "feat(bridge): progressView renderer with throttle and collapse summary"
```

---

## Task 4: Wire progressView into the bridge loop

**Files:**
- Modify: `internal/bridge/bridge.go`
- Test: `internal/bridge/bridge.go` compiles + full package tests (`go test ./...`)

- [ ] **Step 1: Add the new Options fields**

In `internal/bridge/bridge.go`, add to the `Options` struct (after `Verbose bool`):

```go
	Progress     string // "off" | "actions" | "full" (default "full")
	ProgressKeep bool   // keep the full running list instead of collapsing to a summary
```

- [ ] **Step 2: Validate/normalize the level at the top of Run**

In `Run`, immediately after the `if !c.Enabled()` block, add:

```go
	switch o.Progress {
	case "", "off", "actions", "full":
	default:
		return fmt.Errorf("invalid --progress %q (want off|actions|full)", o.Progress)
	}
	if o.Progress == "" {
		o.Progress = "full"
	}
```

- [ ] **Step 3: Wire the progress view per message**

Replace the message-handling block in the `for _, m := range msgs` loop (from the `_ = c.React(ctx, ch, m.ID, ackEmoji)` line through the done-reaction) with:

```go
			_ = c.React(ctx, ch, m.ID, ackEmoji)

			var pv *progressView
			var onEvent func(session.Event)
			if o.Progress != "off" {
				post := func(id, content string) (string, error) {
					return c.UpsertStatusMessage(ctx, ch, id, content)
				}
				pv = newProgressView(post, o.Progress, o.ProgressKeep, time.Now())
				onEvent = pv.add
			}

			out, err := resp.Respond(ctx, session.DctlMessage{
				Content:   m.Content,
				Author:    m.Author.Username,
				MessageID: m.ID,
				ChannelID: m.ChannelID,
			}, onEvent)
			if err != nil && out == "" {
				out = "⚠️ " + err.Error()
			}
			out = strings.TrimSpace(out)
			if out == "" {
				if pv != nil {
					pv.finish(true)
				}
				_ = c.Unreact(ctx, ch, m.ID, ackEmoji)
				_ = c.React(ctx, ch, m.ID, failEmoji)
				continue
			}
			for _, chunk := range chunk(out, discordMaxLen) {
				if _, err := c.Reply(ctx, ch, m.ID, chunk); err != nil {
					logf(true, "reply error: %v", err)
				}
			}
			if pv != nil {
				pv.finish(err != nil)
			}
			// Swap the "seen" mark for a "done" mark once the answer is posted.
			_ = c.Unreact(ctx, ch, m.ID, ackEmoji)
			_ = c.React(ctx, ch, m.ID, doneEmoji)
```

- [ ] **Step 4: Run the full package test to verify it compiles and passes**

Run: `cd /home/shan/dev/dctl && go build ./... && go test ./internal/bridge/ ./internal/session/ -v`
Expected: build OK, all tests PASS.

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add internal/bridge/bridge.go
/usr/bin/git commit -m "feat(bridge): post live progress per message via progressView"
```

---

## Task 5: CLI flags

**Files:**
- Modify: `cmd/dctl/bridge.go`

- [ ] **Step 1: Add the flags and pass them through**

In `cmd/dctl/bridge.go`, after the `verbose := fs.Bool(...)` line, add:

```go
	progress := fs.String("progress", "full", "live activity feedback level: off | actions | full")
	progressKeep := fs.Bool("progress-keep", false, "keep the full progress list instead of collapsing to a one-line summary")
```

And add to the `bridge.Options{...}` literal:

```go
		Progress:     *progress,
		ProgressKeep: *progressKeep,
```

- [ ] **Step 2: Verify it builds and the flags appear**

Run: `cd /home/shan/dev/dctl && go build ./... && go run ./cmd/dctl bridge -h 2>&1 | grep -E "progress"`
Expected: build OK; output shows `-progress` and `-progress-keep` flag help lines.

- [ ] **Step 3: Commit**

```bash
/usr/bin/git add cmd/dctl/bridge.go
/usr/bin/git commit -m "feat(cli): --progress and --progress-keep flags for bridge"
```

---

## Task 6: Document the flags in the skill

**Files:**
- Modify: `~/.claude/skills/dctl-bridge/SKILL.md`

- [ ] **Step 1: Add the flags to the Flags table**

In the Flags table of `/home/shan/.claude/skills/dctl-bridge/SKILL.md`, add two rows (before the `-v` row):

```markdown
| `--progress LEVEL` | `full` | Live activity feedback per message: `off`, `actions` (tools only), `full` (tools + reasoning). |
| `--progress-keep` | off | Keep the full progress list instead of collapsing to a one-line summary. |
```

- [ ] **Step 2: Expand the "Feedback while it works" section**

In that section, after the existing reaction paragraph, add:

```markdown
Beyond the reaction, in stream mode the bridge posts a single **live progress
message** per question (created on the first tool call, edited in place). At
`--progress full` (default) it shows each tool the session runs plus its reasoning
text; `--progress actions` shows tools only; `--progress off` disables it. When the
answer is ready the progress message collapses to a one-line summary
(`✅ 6 actions (Bash×2, Read×3, Edit) · 28s · $0.04`) unless `--progress-keep` is set.
Editing needs the bot's Manage Messages / send permission; failures are ignored so
the reply still posts.
```

- [ ] **Step 3: Commit**

The skill lives outside the repo (`~/.claude/skills/...`), so this is a separate, optional commit if that directory is under version control. Otherwise just save the file. Verify the edit:

Run: `grep -n "progress" /home/shan/.claude/skills/dctl-bridge/SKILL.md`
Expected: shows the new rows and paragraph.

---

## Final Verification

- [ ] **Run the whole suite**

Run: `cd /home/shan/dev/dctl && go build ./... && go test ./...`
Expected: all packages build and PASS.

- [ ] **Manual smoke (optional, needs a live bot token + channel)**

Run a bridge with `--progress full -v` against a test channel, send a message that triggers tool use, and confirm: a 👀 reaction appears, a live progress message grows with `🔧/📖/✏️/💭` lines, then collapses to a `✅ … · Ns` summary alongside the final reply.

---

## Self-Review Notes

- **Spec coverage:** levels off/actions/full (Tasks 4–5), live single message + throttle + cap (Task 3), collapse summary with counts/duration/cost (Task 3), per-question fresh message (Task 4 constructs a new `progressView` per message), `--progress-keep` (Tasks 3–5), best-effort error handling (Task 3 `flush`/`finish` swallow errors; Task 4 keeps the existing reply path), one-shot mode unaffected (Task 2 `oneShotResponder` ignores the callback). All covered.
- **Type consistency:** `Event{Kind,Tool,Detail,Cost,IsError}` defined in Task 1 is used identically in Tasks 2–4. `newProgressView(post, level, keep, start)`, `add`, `render`, `summary`, `finish`, `flush`, `maxLines`, `progressInterval` are consistent across Task 3 code and tests and Task 4 wiring. `Respond(ctx, m, onEvent)` signature matches between Task 2 (definition) and Task 4 (call).
- **No placeholders:** every code step shows full code; commands have expected output.
