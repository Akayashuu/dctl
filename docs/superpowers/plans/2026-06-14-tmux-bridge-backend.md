# tmux Bridge Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a third bridge backend, `tmux`, that drives the interactive `claude` TUI inside a tmux session and relays its text output to Discord — one persistent Claude per channel, text only.

**Architecture:** A new `tmuxResponder` implements the existing `session.Responder` interface. Per turn it snapshots the pane (`tmux capture-pane`), types the message (`tmux send-keys -l`), waits for the pane to go quiescent, diffs the scrollback, strips the TUI chrome, and returns the clean text. The bridge loop, chunking, reactions, allowlist, and supervisor are reused unchanged. Backend selection moves from a `stream bool` to a `backend string`.

**Tech Stack:** Go (stdlib only + existing deps), the external `tmux` binary, `claude` CLI.

**Spec:** `docs/superpowers/specs/2026-06-14-tmux-bridge-backend-design.md`

---

## File Structure

- Create `internal/session/tmux.go` — pure extraction helpers (`newLines`, `stripChrome`, `extractTurn`), the quiescence poller (`awaitQuiescence`), and `tmuxResponder` (real tmux exec).
- Create `internal/session/tmux_test.go` — unit tests for the pure helpers + poller, and a `tmux`-gated integration test.
- Create `internal/session/testdata/` — captured pane fixtures.
- Modify `internal/session/stream.go` — change `NewResponder` to take a `backend string` (+ `dir`) and return `*tmuxResponder` for `"tmux"`.
- Modify `internal/session/stream_test.go` — update `TestResponderSelection` to the new signature.
- Modify `internal/bridge/bridge.go` — `Options.Backend` field; pass it (and dir) to `NewResponder`.
- Modify `cmd/dctl/bridge.go` — `--backend` / `--tmux-timeout` flags + `--stream` back-compat mapping.
- Modify `cmd/dctl/bridge_flags_test.go` — compile-time guard for `Options.Backend`.

---

## Task 1: Pure turn extraction (newLines + stripChrome)

**Files:**
- Create: `internal/session/tmux.go`
- Test: `internal/session/tmux_test.go`

- [ ] **Step 1: Write the failing test**

```go
package session

import (
	"strings"
	"testing"
)

func TestNewLines(t *testing.T) {
	before := "banner\n> \n"
	after := "banner\nhello from claude\nmore output\n> \n"
	got := strings.Join(newLines(before, after), "\n")
	want := "hello from claude\nmore output\n> "
	if got != want {
		t.Fatalf("newLines =\n%q\nwant\n%q", got, want)
	}
}

func TestStripChrome(t *testing.T) {
	in := []string{
		"╭────────────────────────╮",
		"│ > hello                │",
		"╰────────────────────────╯",
		"",
		"Here is your answer.",
		"It spans two lines.",
		"",
		"",
		"⏵⏵ accept edits on (shift+tab to cycle)",
		"? for shortcuts",
	}
	got := stripChrome(in)
	want := "Here is your answer.\nIt spans two lines."
	if strings.Join(got, "\n") != want {
		t.Fatalf("stripChrome =\n%q\nwant\n%q", strings.Join(got, "\n"), want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/session/ -run 'TestNewLines|TestStripChrome' -v`
Expected: FAIL — `undefined: newLines`, `undefined: stripChrome`.

- [ ] **Step 3: Write minimal implementation**

```go
package session

import "strings"

// newLines returns the lines of after that follow the longest common line
// prefix it shares with before. tmux scrollback is append-only above the input
// box, so the shared prefix is everything from prior turns and the remainder is
// what this turn added (echoed input + Claude's output + the redrawn input box).
func newLines(before, after string) []string {
	b := strings.Split(strings.TrimRight(before, "\n"), "\n")
	a := strings.Split(strings.TrimRight(after, "\n"), "\n")
	i := 0
	for i < len(b) && i < len(a) && b[i] == a[i] {
		i++
	}
	return a[i:]
}

// chromeLine reports whether a captured line is TUI furniture (borders, the
// input box, status/hint lines, spinner) rather than Claude's prose.
func chromeLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false // blank handled by the blank-collapser, not dropped here
	}
	// Border / input-box frame: only box-drawing chars, spaces, and a leading
	// prompt marker.
	if strings.ContainsAny(t, "╭╮╰╯┌┐└┘") {
		return true
	}
	if strings.HasPrefix(t, "│") || strings.HasPrefix(t, ">") {
		return true // input box content line
	}
	// Status / hint / spinner lines.
	for _, p := range []string{"⏵⏵", "? for shortcuts", "(esc to interrupt)", "esc to interrupt"} {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

// stripChrome drops chrome lines and collapses runs of blank lines, returning
// the cleaned prose lines with no leading/trailing blanks.
func stripChrome(lines []string) []string {
	var out []string
	blank := false
	for _, l := range lines {
		if chromeLine(l) {
			continue
		}
		if strings.TrimSpace(l) == "" {
			blank = true
			continue
		}
		if blank && len(out) > 0 {
			out = append(out, "")
		}
		blank = false
		out = append(out, strings.TrimRight(l, " "))
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/session/ -run 'TestNewLines|TestStripChrome' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/session/tmux.go internal/session/tmux_test.go
git commit -m "feat(session): pure tmux turn extraction (newLines + stripChrome)"
```

---

## Task 2: extractTurn over a real capture fixture

**Files:**
- Modify: `internal/session/tmux.go`
- Modify: `internal/session/tmux_test.go`
- Create: `internal/session/testdata/claude_done.txt`

- [ ] **Step 1: Add a captured fixture**

Create `internal/session/testdata/claude_done.txt` with a realistic post-turn
`capture-pane -p -S -` dump (banner + a prior exchange + this turn's answer +
the input box). Keep it small but representative:

```
 ▐▛███▜▌  Claude Code
 ▝▜█████▛▘
> what is 2+2

2 + 2 = 4.

╭──────────────────────────────────────────╮
│ >                                        │
╰──────────────────────────────────────────╯
  ⏵⏵ accept edits on (shift+tab to cycle)
```

- [ ] **Step 2: Write the failing test**

```go
func TestExtractTurn(t *testing.T) {
	before := " ▐▛███▜▌  Claude Code\n ▝▜█████▛▘\n> what is 2+2\n"
	after, err := os.ReadFile("testdata/claude_done.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := extractTurn(before, string(after))
	if got != "2 + 2 = 4." {
		t.Fatalf("extractTurn = %q, want %q", got, "2 + 2 = 4.")
	}
}
```

Add `"os"` to the test imports.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/session/ -run TestExtractTurn -v`
Expected: FAIL — `undefined: extractTurn`.

- [ ] **Step 4: Implement extractTurn**

Append to `internal/session/tmux.go`:

```go
// extractTurn turns a before/after scrollback pair into the clean text Claude
// added this turn. Empty result means nothing new survived stripping.
func extractTurn(before, after string) string {
	lines := stripChrome(newLines(before, after))
	// Drop a leading echoed-input line if present (the user's own message is
	// echoed into the transcript above Claude's reply); we only want the reply.
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/session/ -run TestExtractTurn -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/session/tmux.go internal/session/tmux_test.go internal/session/testdata/claude_done.txt
git commit -m "feat(session): extractTurn over a captured claude pane fixture"
```

---

## Task 3: Quiescence poller

**Files:**
- Modify: `internal/session/tmux.go`
- Modify: `internal/session/tmux_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestAwaitQuiescence(t *testing.T) {
	seq := []string{"a", "ab", "ab", "ab"} // changes then stabilizes
	i := 0
	capture := func() (string, error) {
		s := seq[i]
		if i < len(seq)-1 {
			i++
		}
		return s, nil
	}
	got, err := awaitQuiescence(context.Background(), capture, quiesceCfg{
		stable: 2, poll: 0, timeout: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "ab" {
		t.Fatalf("awaitQuiescence = %q, want %q", got, "ab")
	}
}

func TestAwaitQuiescenceTimeout(t *testing.T) {
	capture := func() (string, error) { return changing(), nil }
	_, err := awaitQuiescence(context.Background(), capture, quiesceCfg{
		stable: 3, poll: 0, timeout: 1,
	})
	if err == nil {
		t.Fatal("expected timeout error for never-stable pane")
	}
}

var n int

func changing() string { n++; return strings.Repeat("x", n) }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/session/ -run TestAwaitQuiescence -v`
Expected: FAIL — `undefined: awaitQuiescence` / `undefined: quiesceCfg`.

- [ ] **Step 3: Implement the poller**

Append to `internal/session/tmux.go` (add `"context"`, `"fmt"`, `"time"` to imports):

```go
// quiesceCfg tunes the quiescence poll. stable = number of consecutive equal
// captures that mark "done"; poll = delay between captures; timeout = hard cap.
type quiesceCfg struct {
	stable  int
	poll    time.Duration
	timeout time.Duration
}

// awaitQuiescence polls capture until the pane text is unchanged for cfg.stable
// consecutive reads, then returns that text. It errors on timeout or a capture
// error, or if ctx is cancelled.
func awaitQuiescence(ctx context.Context, capture func() (string, error), cfg quiesceCfg) (string, error) {
	deadline := time.Now().Add(cfg.timeout)
	var last string
	same := 0
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		cur, err := capture()
		if err != nil {
			return "", err
		}
		if cur == last {
			same++
		} else {
			same, last = 0, cur
		}
		if same >= cfg.stable {
			return cur, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("tmux pane did not settle within %s", cfg.timeout)
		}
		if cfg.poll > 0 {
			time.Sleep(cfg.poll)
		}
	}
}
```

Note: `quiesceCfg` numeric literals in the test (`stable: 2, poll: 0, timeout: 100`) rely on `Duration` accepting untyped constants; `timeout: 100` is 100ns, fine for tests. The poller compares against `time.Now()` so the timeout test (`timeout: 1`) trips after the first non-stable loop.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/session/ -run TestAwaitQuiescence -v`
Expected: PASS (both subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/session/tmux.go internal/session/tmux_test.go
git commit -m "feat(session): tmux pane quiescence poller"
```

---

## Task 4: tmuxResponder (real tmux wiring)

**Files:**
- Modify: `internal/session/tmux.go`

- [ ] **Step 1: Implement tmuxResponder**

Append to `internal/session/tmux.go` (add `"os"`, `"os/exec"`, `"sync"`):

```go
// tmuxResponder drives an interactive `claude` TUI inside a persistent tmux
// session: one session per bridge, lazily started on the first message and
// reused for every later message (Claude keeps its context hot). Each turn
// types the message with send-keys, waits for the pane to settle, and returns
// the cleaned text Claude added.
type tmuxResponder struct {
	sessName string
	dir      string
	cmd      []string // program launched in the pane (e.g. ["claude","--dangerously-skip-permissions"])
	timeout  time.Duration

	mu       sync.Mutex
	started  bool
	baseline string // cleaned-up: full capture after the previous turn settled
}

// newTmuxResponder builds a responder. base is the pane command (defaults to
// claude --dangerously-skip-permissions); model is appended when set.
func newTmuxResponder(sessName, dir string, base []string, model string, timeout time.Duration) *tmuxResponder {
	cmd := append([]string{}, base...)
	if len(cmd) == 0 {
		cmd = []string{"claude", "--dangerously-skip-permissions"}
	}
	if model != "" {
		cmd = append(cmd, "--model", model)
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &tmuxResponder{sessName: sessName, dir: dir, cmd: cmd, timeout: timeout}
}

func tmuxRun(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	return string(out), err
}

func (t *tmuxResponder) capture() (string, error) {
	return tmuxRun("capture-pane", "-p", "-S", "-", "-t", t.sessName)
}

func (t *tmuxResponder) capturePoll(ctx context.Context) (string, error) {
	return awaitQuiescence(ctx, t.capture, quiesceCfg{
		stable:  3,
		poll:    300 * time.Millisecond,
		timeout: t.timeout,
	})
}

func (t *tmuxResponder) start(ctx context.Context) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux not found on PATH: %w", err)
	}
	args := []string{"new-session", "-d", "-s", t.sessName, "-x", "200", "-y", "50"}
	if t.dir != "" {
		args = append(args, "-c", t.dir)
	}
	args = append(args, strings.Join(t.cmd, " "))
	if out, err := tmuxRun(args...); err != nil {
		return fmt.Errorf("tmux new-session: %v: %s", err, out)
	}
	// Wait for the TUI to finish drawing its first prompt before we type.
	settled, err := t.capturePoll(ctx)
	if err != nil {
		return err
	}
	t.baseline = settled
	t.started = true
	return nil
}

func (t *tmuxResponder) Respond(ctx context.Context, m DctlMessage, _ func(Event)) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.started {
		if err := t.start(ctx); err != nil {
			return "", err
		}
	}
	before := t.baseline
	if out, err := tmuxRun("send-keys", "-t", t.sessName, "-l", m.Content); err != nil {
		return "", fmt.Errorf("tmux send-keys: %v: %s", err, out)
	}
	if out, err := tmuxRun("send-keys", "-t", t.sessName, "Enter"); err != nil {
		return "", fmt.Errorf("tmux send-keys Enter: %v: %s", err, out)
	}
	after, err := t.capturePoll(ctx)
	if err != nil {
		return "", err
	}
	t.baseline = after
	reply := extractTurn(before, after)
	if reply == "" {
		// Never silently lose a turn: fall back to the raw diff.
		reply = strings.TrimSpace(strings.Join(newLines(before, after), "\n"))
	}
	return reply, nil
}

func (t *tmuxResponder) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		_, _ = tmuxRun("kill-session", "-t", t.sessName)
		t.started = false
	}
	return nil
}

// tmuxSessionName derives a collision-safe tmux session name from the channel,
// namespaced by DCTL_INSTANCE_ID (same scheme as session worktrees).
func tmuxSessionName(channel string) string {
	inst := os.Getenv("DCTL_INSTANCE_ID")
	if inst == "" {
		return "dctl-" + channel
	}
	return "dctl-" + inst + "-" + channel
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./...`
Expected: builds clean (no callers yet — that's Task 5).

- [ ] **Step 3: Commit**

```bash
git add internal/session/tmux.go
git commit -m "feat(session): tmuxResponder driving an interactive claude TUI"
```

---

## Task 5: Backend selection in NewResponder

**Files:**
- Modify: `internal/session/stream.go:14-21`
- Modify: `internal/session/stream_test.go:11-20`

- [ ] **Step 1: Update the selection test**

Replace `TestResponderSelection` in `internal/session/stream_test.go` with:

```go
func TestResponderSelection(t *testing.T) {
	noop := func(ctx context.Context, m DctlMessage) (string, error) { return "x", nil }
	if _, ok := NewResponder(context.Background(), "oneshot", "foo", "", "", "chan", noop).(*oneShotResponder); !ok {
		t.Fatal("oneshot backend should yield oneShotResponder")
	}
	if _, ok := NewResponder(context.Background(), "stream", "claude", "", "", "chan", noop).(*streamResponder); !ok {
		t.Fatal("stream backend should yield streamResponder")
	}
	if _, ok := NewResponder(context.Background(), "tmux", "claude", "", "/tmp", "chan", noop).(*tmuxResponder); !ok {
		t.Fatal("tmux backend should yield tmuxResponder")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/session/ -run TestResponderSelection -v`
Expected: FAIL — too many arguments to `NewResponder`.

- [ ] **Step 3: Update NewResponder**

Replace `NewResponder` in `internal/session/stream.go`:

```go
// NewResponder selects the response strategy by backend:
//   "stream"  — one persistent claude stream-json process (default)
//   "oneshot" — run cmd fresh per message
//   "tmux"    — interactive claude TUI inside a tmux session, text relayed
// dir is the working directory (tmux/stream); channel seeds the tmux session name.
func NewResponder(ctx context.Context, backend, cmdStr, model, dir, channel string, oneShot func(context.Context, DctlMessage) (string, error)) Responder {
	switch backend {
	case "oneshot":
		return &oneShotResponder{run: oneShot}
	case "tmux":
		return newTmuxResponder(tmuxSessionName(channel), dir, strings.Fields(cmdStr), model, 0)
	default: // "stream"
		r := &streamResponder{ctx: ctx, base: streamBase(strings.Fields(cmdStr)), model: model}
		r.dir = dir
		return r
	}
}
```

(Note: `streamResponder` already has a `dir` field; wiring it here is harmless and was previously always `""`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/session/ -run TestResponderSelection -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/session/stream.go internal/session/stream_test.go
git commit -m "feat(session): select responder by backend string (stream|oneshot|tmux)"
```

---

## Task 6: Wire Backend into bridge.Options

**Files:**
- Modify: `internal/bridge/bridge.go:32-48` (Options), `:101-105` (NewResponder call)

- [ ] **Step 1: Add the Backend field**

In `internal/bridge/bridge.go`, add to `Options`:

```go
	Backend string // "stream" | "oneshot" | "tmux" (empty → derived from Stream)
```

- [ ] **Step 2: Derive and pass the backend**

Replace the responder construction (currently lines ~101-105):

```go
	backend := o.Backend
	if backend == "" {
		if o.Stream {
			backend = "stream"
		} else {
			backend = "oneshot"
		}
	}
	oneShot := func(ctx context.Context, mm session.DctlMessage) (string, error) {
		return runCmd(ctx, o.Cmd, mm)
	}
	resp := session.NewResponder(ctx, backend, o.Cmd, o.Model, "", ch, oneShot)
	defer resp.Close()
```

(`dir` is `""` here — the bridge inherits the supervisor's working directory, which is already the session worktree. `ch` is the resolved channel computed earlier in `Run`.)

- [ ] **Step 3: Verify build + existing tests**

Run: `go build ./... && go test ./internal/bridge/ ./internal/session/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/bridge/bridge.go
git commit -m "feat(bridge): Backend option selecting the responder strategy"
```

---

## Task 7: CLI flags (--backend, --tmux-timeout)

**Files:**
- Modify: `cmd/dctl/bridge.go`
- Modify: `cmd/dctl/bridge_flags_test.go`

- [ ] **Step 1: Add the compile-time guard test**

Append to `cmd/dctl/bridge_flags_test.go`:

```go
// TestBridgeBackendFlagWired fails to build until bridge.Options gains a Backend
// field, ensuring runBridge can set it.
func TestBridgeBackendFlagWired(t *testing.T) {
	_ = bridgeOptionsHasBackend
}
```

And append to `cmd/dctl/bridge.go` (next to `bridgeOptionsHasParticipants`):

```go
var bridgeOptionsHasBackend = bridge.Options{}.Backend
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/dctl/ -run TestBridgeBackendFlagWired`
Expected: FAIL to build — `bridge.Options{}.Backend` undefined (until Task 6 is merged; if Task 6 is already done, it builds — then this guard simply passes).

- [ ] **Step 3: Add the flags and wire them**

In `cmd/dctl/bridge.go`, add flag declarations after `progressKeep`:

```go
	backend := fs.String("backend", "", "responder backend: stream | oneshot | tmux (default derived from --stream)")
	tmuxTimeout := fs.Duration("tmux-timeout", 5*time.Minute, "tmux backend: max wait for a turn to settle")
```

Add `"time"` to the imports. Then add to the `bridge.Options{...}` literal:

```go
		Backend: *backend,
```

The `--tmux-timeout` value is threaded once the responder honors a configurable
timeout; for v1 the default (5m) is baked into `newTmuxResponder` and the flag is
accepted for forward-compat. (If you want it live now, add a `TmuxTimeout
time.Duration` Options field and pass it through `NewResponder` →
`newTmuxResponder`; otherwise leave the flag parsed-but-default.)

- [ ] **Step 4: Verify build + flag parse**

Run: `go build ./... && go run ./cmd/dctl bridge --help 2>&1 | grep -E 'backend|tmux-timeout'`
Expected: both flags listed.

- [ ] **Step 5: Commit**

```bash
git add cmd/dctl/bridge.go cmd/dctl/bridge_flags_test.go
git commit -m "feat(cli): dctl bridge --backend and --tmux-timeout flags"
```

---

## Task 8: tmux-gated integration test

**Files:**
- Modify: `internal/session/tmux_test.go`

- [ ] **Step 1: Write the integration test**

```go
func TestTmuxResponderIntegration(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	r := newTmuxResponder("dctl-test-"+t.Name(), "", []string{"bash", "--norc"}, "", 10*time.Second)
	defer r.Close()
	out, err := r.Respond(context.Background(), DctlMessage{Content: "echo hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("expected output to contain \"hi\", got %q", out)
	}
}
```

Add `"os/exec"`, `"time"`, `"context"` to the test imports if not already present.

- [ ] **Step 2: Run the test**

Run: `go test ./internal/session/ -run TestTmuxResponderIntegration -v`
Expected: PASS where tmux is installed (drives a real `bash` pane — no Claude
login needed); SKIP otherwise.

- [ ] **Step 3: Commit**

```bash
git add internal/session/tmux_test.go
git commit -m "test(session): tmux-gated integration test for tmuxResponder"
```

---

## Task 9: serve `/session create backend:` option

**Files:**
- Modify: `internal/serve/serve.go` (slash command definition + handler)
- Modify: `internal/supervisor/supervisor.go` (pass `--backend` to the bridge child)
- Modify: `internal/state/…` (persist the session backend, mirroring existing per-session fields)

> Read `internal/serve/serve.go`, `internal/supervisor/supervisor.go`, and the
> session state struct first — wire `backend` exactly the way `model`/`cmd` are
> already wired for `/session create` (the model × effort matrix autocomplete
> from commit `3a63177` is the pattern to mirror).

- [ ] **Step 1: Add the `backend` option to `/session create`**

In the `/session create` command registration, add an optional string option
`backend` with choices `stream` (default) and `tmux`, alongside the existing
`name` / `cmd` / `shared` options. Mirror the existing option wiring exactly.

- [ ] **Step 2: Thread it to the supervisor**

In the create handler, read the `backend` option (default `"stream"`), store it
on the session record in state (add a `Backend string` field next to the
existing per-session fields and include it in save/load), and have the
supervisor append `--backend <value>` to the `dctl bridge` argv it builds for
that session — next to where it already appends `--cmd` / `--model` / `--state`
/ `--participants` / `--allow-state` / `--allow-session`.

- [ ] **Step 3: Build + existing serve/supervisor tests**

Run: `go build ./... && go test ./internal/serve/ ./internal/supervisor/`
Expected: PASS.

- [ ] **Step 4: Manual smoke (optional, needs a configured bot + tmux)**

In Discord: `/session create name:tui backend:tmux`, then send a message in the
new channel and confirm Claude's reply comes back. `/session close name:tui`
and confirm `tmux ls` no longer lists `dctl-*-<channel>`.

- [ ] **Step 5: Commit**

```bash
git add internal/serve/serve.go internal/supervisor/supervisor.go internal/state
git commit -m "feat(serve): /session create backend:tmux for TUI-backed sessions"
```

---

## Task 10: Full verification

- [ ] **Step 1: Run the whole suite**

Run: `go build ./... && go test ./...`
Expected: PASS (the tmux integration test SKIPs without tmux).

- [ ] **Step 2: Vet**

Run: `go vet ./...`
Expected: clean.

- [ ] **Step 3: Update the bridge skill doc**

Add a short "tmux backend" note to `.claude/skills/dctl-bridge/SKILL.md`: what
`--backend tmux` does (drives the interactive `claude` TUI, text-only relay,
one persistent Claude per channel, `--dangerously-skip-permissions`, no
interactive permission buttons yet).

- [ ] **Step 4: Commit**

```bash
git add .claude/skills/dctl-bridge/SKILL.md
git commit -m "docs(skill): document the tmux bridge backend"
```

---

## Self-Review Notes

- **Spec coverage:** architecture/Responder (T4–T6), message cycle (T4), chrome
  stripping (T1–T2), quiescence + fixed width + session naming (T3–T4), CLI
  surface (T7), serve/`/session create` (T9), testing matrix (T1–T3, T8),
  skip-permissions + text-only + no-progress non-goals (T4 launch cmd, no
  onEvent use). Phase-2 items (permission buttons, live progress, reattach) are
  intentionally excluded.
- **Type consistency:** `newTmuxResponder`, `tmuxResponder`, `quiesceCfg`,
  `awaitQuiescence`, `extractTurn`, `newLines`, `stripChrome`, `tmuxSessionName`,
  and the 6-arg `NewResponder(ctx, backend, cmd, model, dir, channel, oneShot)`
  signature are used consistently across tasks.
- **Known follow-up:** `--tmux-timeout` is parsed but not yet threaded into
  `newTmuxResponder` in v1 (T7 step 3 documents how to make it live); the default
  5m applies. Surfaced rather than hidden.
