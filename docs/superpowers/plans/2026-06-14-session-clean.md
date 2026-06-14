# Session Clean Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a bulk session-cleanup capability to dctl, exposed both as the `/session clean` Discord slash-command (online, supervisor-driven) and the `dctl sessions clean` CLI (offline maintenance), purging dead sessions, optionally closing live ones, and sweeping orphan residue.

**Architecture:** Shared, side-effect-free classification lives in a new `internal/sessionclean` package (`Inspect` over mocked probes). The teardown sequence is extracted from `handler.sessionClose` into a reusable function the daemon and CLI both call. The daemon path uses the live supervisor; the CLI path is offline-only and refuses to run while the daemon answers its health endpoint. Stale detection is derived from Discord's last-message timestamp (no new persistent field), with the idle threshold configured via `config.json` (`sessionMaxIdleDays`, default 14).

**Tech Stack:** Go 1.23, standard library (`flag`, `net/http`, `os/exec` for tmux), Discord REST via the existing `dctl.Client`.

---

## File Structure

- **Create** `internal/sessionclean/sessionclean.go` — `Inspect`, `Candidate`, `Reason`, `Probes` (pure classification).
- **Create** `internal/sessionclean/sessionclean_test.go` — table tests for `Inspect`.
- **Create** `internal/sessionclean/teardown.go` — shared `Teardown` used by both surfaces.
- **Create** `internal/sessionclean/teardown_test.go` — teardown tests.
- **Create** `cmd/dctl/sessions.go` — `runSessions` CLI dispatch + offline clean + orphan scan.
- **Modify** `internal/config/config.go` — add `SessionMaxIdleDays`; add template entry.
- **Modify** `internal/session/tmux.go` — add `TmuxSessionExists`.
- **Modify** `dctl.go` — add `Client.LastMessageAt`.
- **Modify** `internal/handler/handler.go` — `sessionClean`, dispatch case, refactor `sessionClose` onto `Teardown`, store maxIdle.
- **Modify** `interactions.go` — register the `clean` subcommand.
- **Modify** `cmd/dctl/main.go` — `case "sessions"` + usage text.
- **Modify** `cmd/dctl/serve.go` — pass `SessionMaxIdleDays` into the handler.

---

## Task 1: Config field `SessionMaxIdleDays`

**Files:**
- Modify: `internal/config/config.go:31-44` (struct), `:104-137` (template)
- Test: `internal/config/config_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Create or append to `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSessionMaxIdleDays(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	if err := os.WriteFile(p, []byte(`{"sessionMaxIdleDays": 7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.SessionMaxIdleDays != 7 {
		t.Fatalf("SessionMaxIdleDays = %d, want 7", c.SessionMaxIdleDays)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoadSessionMaxIdleDays -v`
Expected: FAIL — `c.SessionMaxIdleDays` undefined (compile error).

- [ ] **Step 3: Add the struct field**

In `internal/config/config.go`, inside `type Config struct`, after the `Owner` field (line 38):

```go
	Owner         string   `json:"owner"`                 // Discord user id seeded into the allowlist
	// Stale threshold for `session clean`: sessions inactive longer than this
	// many days are reported as stale. 0 disables stale detection. Unset (zero)
	// means the built-in default (14) is applied by the caller.
	SessionMaxIdleDays int `json:"sessionMaxIdleDays,omitempty"`
```

- [ ] **Step 4: Add the template entry**

In `Template`, after the `"owner"` block (line 125), add:

```go
  // Discord user id seeded into the allowlist on first run (like DCTL_OWNER_ID).
  "owner": ` + string(ownerJSON) + `,

  // `session clean` stale threshold in days: sessions with no message for longer
  // are reported as stale (acted on only with all:true + stale). 0 disables.
  // Omit to use the built-in default of 14.
  // "sessionMaxIdleDays": 14,
```

Note: `"owner"` is currently a literal `""` in the template, not interpolated. Keep it exactly as it is in the file — do **not** introduce `ownerJSON`. Use this instead, matching the existing literal:

```go
  // Discord user id seeded into the allowlist on first run (like DCTL_OWNER_ID).
  "owner": "",

  // `session clean` stale threshold in days: sessions with no message for longer
  // are reported as stale (acted on only with all:true + stale). 0 disables.
  // Omit to use the built-in default of 14.
  // "sessionMaxIdleDays": 14,
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestLoadSessionMaxIdleDays -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add sessionMaxIdleDays knob for session clean"
```

---

## Task 2: `session.TmuxSessionExists` helper

**Files:**
- Modify: `internal/session/tmux.go` (near `KillTmuxSession`, ~line 532)
- Test: `internal/session/tmux_clean_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/session/tmux_clean_test.go`:

```go
package session

import "testing"

// TestTmuxSessionExistsUnknown verifies a clearly-absent session reports false
// without panicking, even when tmux is not installed (tmuxRun returns an error).
func TestTmuxSessionExistsUnknown(t *testing.T) {
	if TmuxSessionExists("dctl-this-channel-does-not-exist-zzz") {
		t.Fatal("expected false for a non-existent tmux session")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/session/ -run TestTmuxSessionExistsUnknown -v`
Expected: FAIL — `TmuxSessionExists` undefined.

- [ ] **Step 3: Add the helper**

In `internal/session/tmux.go`, immediately after `KillTmuxSession` (after line 534):

```go
// TmuxSessionExists reports whether the persistent pane for channel is still
// alive. Symmetric to KillTmuxSession: same name derivation, best-effort. A
// missing tmux binary or absent session both yield false.
func TmuxSessionExists(channel string) bool {
	_, err := tmuxRun("has-session", "-t", tmuxSessionName(channel))
	return err == nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/session/ -run TestTmuxSessionExistsUnknown -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/session/tmux.go internal/session/tmux_clean_test.go
git commit -m "feat(session): add TmuxSessionExists liveness probe"
```

---

## Task 3: `Client.LastMessageAt` helper

**Files:**
- Modify: `dctl.go` (after the `Read` method, ~line 144)
- Test: `dctl_lastmessage_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `dctl_lastmessage_test.go`:

```go
package dctl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLastMessageAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Discord returns newest-first; one message is enough.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"9","channel_id":"c","content":"hi","author":{"id":"u","username":"a"},"timestamp":"2026-06-01T12:00:00.000000+00:00"}]`))
	}))
	defer srv.Close()

	c := New("tok", "c")
	c.api = srv.URL // override REST base (see Step 3 note)

	ts, err := c.LastMessageAt(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if !ts.Equal(want) {
		t.Fatalf("LastMessageAt = %v, want %v", ts, want)
	}
}

func TestLastMessageAtEmptyChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := New("tok", "c")
	c.api = srv.URL
	ts, err := c.LastMessageAt(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	if !ts.IsZero() {
		t.Fatalf("expected zero time for empty channel, got %v", ts)
	}
}
```

> **Note for Step 1:** the test sets `c.api`. Inspect `dctl.go` for the actual field name holding the REST base URL (it may be `baseURL`, `api`, or similar) and whether `New` accepts an override. If the client has no injectable base URL, adapt the test to whatever seam exists (e.g. an unexported field set directly within the same package, which this `package dctl` test can do). Do not add a new exported setter solely for the test — use the existing field.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run TestLastMessageAt -v`
Expected: FAIL — `LastMessageAt` undefined (and possibly the `c.api` seam needs adjusting per the note).

- [ ] **Step 3: Implement the helper**

In `dctl.go`, after the `Read` method (after line 144), add. Reuse the existing `Read` method so URL/auth/decoding stay in one place:

```go
// LastMessageAt returns the timestamp of the channel's most recent message, or
// the zero Time if the channel has no messages. It is the inactivity signal for
// `session clean` (no persistent LastActive is stored on a session). A transport
// or decode error is returned so callers can stay conservative and NOT treat the
// session as stale on failure.
func (c *Client) LastMessageAt(ctx context.Context, channelID string) (time.Time, error) {
	msgs, err := c.Read(ctx, channelID, 1, "")
	if err != nil {
		return time.Time{}, err
	}
	if len(msgs) == 0 {
		return time.Time{}, nil
	}
	// Read reverses to chronological order, so the single returned message is the
	// latest. Discord timestamps are RFC3339 with a numeric offset.
	ts, err := time.Parse(time.RFC3339, msgs[len(msgs)-1].Timestamp)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse message timestamp %q: %w", msgs[len(msgs)-1].Timestamp, err)
	}
	return ts, nil
}
```

Ensure `time` and `fmt` are imported in `dctl.go` (add to the import block if missing).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run TestLastMessageAt -v`
Expected: PASS for both `TestLastMessageAt` and `TestLastMessageAtEmptyChannel`.

- [ ] **Step 5: Commit**

```bash
git add dctl.go dctl_lastmessage_test.go
git commit -m "feat(client): add LastMessageAt for session staleness"
```

---

## Task 4: `internal/sessionclean` classification

**Files:**
- Create: `internal/sessionclean/sessionclean.go`
- Test: `internal/sessionclean/sessionclean_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/sessionclean/sessionclean_test.go`:

```go
package sessionclean

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vskstudio/dctl/internal/state"
)

func reasons(c Candidate) map[Reason]bool {
	m := map[Reason]bool{}
	for _, r := range c.Reasons {
		m[r] = true
	}
	return m
}

func TestInspectDeadSession(t *testing.T) {
	now := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	sessions := []state.Session{{Name: "ghost", ChannelID: "c1", Worktree: "/gone"}}
	p := Probes{
		TmuxExists:  func(string) bool { return false },
		WorktreeAt:  func(string) bool { return false },
		ChannelLive: func(context.Context, string) (bool, error) { return false, nil },
		LastMessage: func(context.Context, string) (time.Time, error) { return time.Time{}, nil },
	}
	got := Inspect(context.Background(), sessions, p, 14*24*time.Hour, now)
	if len(got) != 1 || !got[0].Dead {
		t.Fatalf("expected one dead candidate, got %+v", got)
	}
	r := reasons(got[0])
	if !r[ReasonTmuxGone] || !r[ReasonWorktreeGone] || !r[ReasonChannelGone] {
		t.Fatalf("missing reasons: %v", got[0].Reasons)
	}
}

func TestInspectAliveSessionNotDead(t *testing.T) {
	now := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	sessions := []state.Session{{Name: "live", ChannelID: "c1", Worktree: "/there"}}
	p := Probes{
		TmuxExists:  func(string) bool { return true }, // alive
		WorktreeAt:  func(string) bool { return true },
		ChannelLive: func(context.Context, string) (bool, error) { return true, nil },
		LastMessage: func(context.Context, string) (time.Time, error) { return now.Add(-time.Hour), nil },
	}
	got := Inspect(context.Background(), sessions, p, 14*24*time.Hour, now)
	if got[0].Dead {
		t.Fatalf("alive session marked dead: %+v", got[0])
	}
	if got[0].Stale {
		t.Fatalf("recently-active session marked stale: %+v", got[0])
	}
}

func TestInspectStaleSession(t *testing.T) {
	now := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	sessions := []state.Session{{Name: "old", ChannelID: "c1", Worktree: "/there"}}
	p := Probes{
		TmuxExists:  func(string) bool { return true },
		WorktreeAt:  func(string) bool { return true },
		ChannelLive: func(context.Context, string) (bool, error) { return true, nil },
		LastMessage: func(context.Context, string) (time.Time, error) { return now.Add(-30 * 24 * time.Hour), nil },
	}
	got := Inspect(context.Background(), sessions, p, 14*24*time.Hour, now)
	if !got[0].Stale || got[0].Dead {
		t.Fatalf("expected stale & not dead, got %+v", got[0])
	}
	if !reasons(got[0])[ReasonStale] {
		t.Fatalf("missing stale reason: %v", got[0].Reasons)
	}
}

func TestInspectProbeErrorIsConservative(t *testing.T) {
	now := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	sessions := []state.Session{{Name: "uncertain", ChannelID: "c1", Worktree: "/gone"}}
	p := Probes{
		TmuxExists:  func(string) bool { return false },
		WorktreeAt:  func(string) bool { return false },
		ChannelLive: func(context.Context, string) (bool, error) { return false, errors.New("network down") },
		LastMessage: func(context.Context, string) (time.Time, error) { return time.Time{}, errors.New("network down") },
	}
	got := Inspect(context.Background(), sessions, p, 14*24*time.Hour, now)
	if got[0].Dead {
		t.Fatalf("session must NOT be dead when channel probe errored: %+v", got[0])
	}
	if got[0].ProbeErr == "" {
		t.Fatalf("expected ProbeErr to be recorded")
	}
}

func TestInspectMaxIdleZeroDisablesStale(t *testing.T) {
	now := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	sessions := []state.Session{{Name: "old", ChannelID: "c1", Worktree: "/there"}}
	p := Probes{
		TmuxExists:  func(string) bool { return true },
		WorktreeAt:  func(string) bool { return true },
		ChannelLive: func(context.Context, string) (bool, error) { return true, nil },
		LastMessage: func(context.Context, string) (time.Time, error) { return now.Add(-365 * 24 * time.Hour), nil },
	}
	got := Inspect(context.Background(), sessions, p, 0, now)
	if got[0].Stale {
		t.Fatalf("maxIdle=0 must disable stale detection: %+v", got[0])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sessionclean/ -v`
Expected: FAIL — package/`Inspect` undefined (compile error).

- [ ] **Step 3: Implement `Inspect`**

Create `internal/sessionclean/sessionclean.go`:

```go
// Package sessionclean classifies daemon sessions for the `session clean`
// command. Inspect is pure: it takes probe closures (so it is trivially
// unit-tested with fakes) and decides which sessions are dead (no remaining
// proof of life) or stale (inactive beyond a threshold). It performs no
// teardown — see Teardown for the side-effecting half.
package sessionclean

import (
	"context"
	"time"

	"github.com/vskstudio/dctl/internal/state"
)

// Reason labels why a session surfaced in a report.
type Reason string

const (
	ReasonTmuxGone     Reason = "tmux-gone"
	ReasonWorktreeGone Reason = "worktree-gone"
	ReasonChannelGone  Reason = "channel-gone"
	ReasonStale        Reason = "stale"
)

// Candidate is the verdict for one session.
type Candidate struct {
	Session  state.Session
	Reasons  []Reason
	Dead     bool   // no remaining proof of life (safe to purge without --all)
	Stale    bool   // last message older than maxIdle
	ProbeErr string // non-empty if a liveness probe errored (then Dead is never set)
}

// Probes are the liveness signals Inspect consults. Each is a closure so tests
// inject fakes and production wires real tmux/filesystem/Discord checks.
type Probes struct {
	TmuxExists  func(channelID string) bool
	WorktreeAt  func(path string) bool
	ChannelLive func(ctx context.Context, channelID string) (bool, error)
	LastMessage func(ctx context.Context, channelID string) (time.Time, error)
}

// Inspect classifies each session. now is injected for deterministic tests.
// maxIdle == 0 disables stale detection.
//
// Death rule: a session is Dead only when EVERY proof of life is absent —
// tmux gone AND (worktree gone OR no worktree configured) AND channel gone —
// and no probe errored. A probe error makes the verdict uncertain: Dead stays
// false and ProbeErr is recorded so the caller can report it without acting.
func Inspect(ctx context.Context, sessions []state.Session, p Probes, maxIdle time.Duration, now time.Time) []Candidate {
	out := make([]Candidate, 0, len(sessions))
	for _, s := range sessions {
		c := Candidate{Session: s}

		tmuxGone := !p.TmuxExists(s.ChannelID)
		if tmuxGone {
			c.Reasons = append(c.Reasons, ReasonTmuxGone)
		}

		// A session with no worktree (shared checkout) contributes no worktree
		// proof-of-life either way; treat "no worktree" as not-a-signal but it
		// does not keep the session alive.
		worktreeGone := s.Worktree != "" && !p.WorktreeAt(s.Worktree)
		if worktreeGone {
			c.Reasons = append(c.Reasons, ReasonWorktreeGone)
		}
		worktreeAbsent := s.Worktree == "" || worktreeGone

		channelLive, chErr := p.ChannelLive(ctx, s.ChannelID)
		if chErr != nil {
			c.ProbeErr = chErr.Error()
		} else if !channelLive {
			c.Reasons = append(c.Reasons, ReasonChannelGone)
		}

		// Stale: only when we could actually read the last message.
		if maxIdle > 0 {
			last, err := p.LastMessage(ctx, s.ChannelID)
			if err != nil {
				if c.ProbeErr == "" {
					c.ProbeErr = err.Error()
				}
			} else if !last.IsZero() && now.Sub(last) > maxIdle {
				c.Stale = true
				c.Reasons = append(c.Reasons, ReasonStale)
			}
		}

		// Dead requires certainty: no probe error, and no proof of life anywhere.
		if c.ProbeErr == "" && tmuxGone && worktreeAbsent && !channelLive {
			c.Dead = true
		}

		out = append(out, c)
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sessionclean/ -v`
Expected: PASS (all five tests).

- [ ] **Step 5: Commit**

```bash
git add internal/sessionclean/sessionclean.go internal/sessionclean/sessionclean_test.go
git commit -m "feat(sessionclean): add Inspect classification with probe fakes"
```

---

## Task 5: Shared `Teardown`

**Files:**
- Create: `internal/sessionclean/teardown.go`
- Test: `internal/sessionclean/teardown_test.go`

This extracts the close sequence so `sessionClose` (Task 6) and the CLI (Task 7) share one path. Define narrow interfaces here so both a live supervisor and a nil/offline stub satisfy them.

- [ ] **Step 1: Write the failing test**

Create `internal/sessionclean/teardown_test.go`:

```go
package sessionclean

import (
	"context"
	"errors"
	"testing"

	"github.com/vskstudio/dctl/internal/state"
)

type fakeDeps struct {
	stopped, killed, archived, removed, journaled bool
	worktreeRemoved                               bool
	archiveErr                                    error
}

func (f *fakeDeps) StopBridge(name string) error        { f.stopped = true; return nil }
func (f *fakeDeps) KillTmux(channelID string)           { f.killed = true }
func (f *fakeDeps) RemoveWorktree(repo, name string, force bool) error {
	f.worktreeRemoved = true
	return nil
}
func (f *fakeDeps) ArchiveChannel(ctx context.Context, channelID string) error {
	f.archived = true
	return f.archiveErr
}
func (f *fakeDeps) RemoveSession(name string) error { f.removed = true; return nil }
func (f *fakeDeps) RemoveJournal(name string)       { f.journaled = true }

func TestTeardownFullSequence(t *testing.T) {
	f := &fakeDeps{}
	sess := state.Session{Name: "x", ChannelID: "c", Worktree: "/wt", Project: "proj"}
	err := Teardown(context.Background(), f, sess, TeardownOpts{Repo: "/repo", Force: false})
	if err != nil {
		t.Fatal(err)
	}
	if !(f.stopped && f.killed && f.worktreeRemoved && f.archived && f.removed && f.journaled) {
		t.Fatalf("not all steps ran: %+v", f)
	}
}

func TestTeardownSkipsWorktreeWhenShared(t *testing.T) {
	f := &fakeDeps{}
	sess := state.Session{Name: "x", ChannelID: "c"} // no worktree
	if err := Teardown(context.Background(), f, sess, TeardownOpts{Repo: "/repo"}); err != nil {
		t.Fatal(err)
	}
	if f.worktreeRemoved {
		t.Fatal("worktree removal should be skipped for shared sessions")
	}
}

func TestTeardownArchiveErrorStillPrunesState(t *testing.T) {
	f := &fakeDeps{archiveErr: errors.New("gone")}
	sess := state.Session{Name: "x", ChannelID: "c"}
	// Archive failing (e.g. channel already deleted) must NOT block state pruning.
	if err := Teardown(context.Background(), f, sess, TeardownOpts{Repo: "/repo"}); err != nil {
		t.Fatal(err)
	}
	if !f.removed {
		t.Fatal("state should be pruned even when archive fails")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sessionclean/ -run TestTeardown -v`
Expected: FAIL — `Teardown`/`TeardownOpts` undefined.

- [ ] **Step 3: Implement `Teardown`**

Create `internal/sessionclean/teardown.go`:

```go
package sessionclean

import (
	"context"

	"github.com/vskstudio/dctl/internal/state"
)

// Deps are the side effects Teardown performs. The daemon supplies a live
// supervisor / Discord client / worktreer; the offline CLI supplies a stub
// whose StopBridge is a no-op (the supervised process is not in its address
// space — it kills tmux and reaps any stray PID separately before calling in).
type Deps interface {
	StopBridge(name string) error
	KillTmux(channelID string)
	RemoveWorktree(repo, name string, force bool) error
	ArchiveChannel(ctx context.Context, channelID string) error
	RemoveSession(name string) error
	RemoveJournal(name string)
}

// TeardownOpts carries per-call settings the Deps cannot infer.
type TeardownOpts struct {
	Repo  string // git repo root the worktree lives under
	Force bool   // discard a dirty worktree
}

// Teardown runs the full close sequence for one session, mirroring the original
// /session close: stop the bridge, reap the persistent tmux pane, remove the
// worktree (unless shared), archive the channel, drop the session from state,
// and delete the participant journal.
//
// Worktree removal is the one hard-fail: if it errors (e.g. a dirty tree without
// force), Teardown returns that error and does NOT prune state — so the operator
// can commit or retry with force. Archive failures are tolerated (the channel may
// already be gone) and never block state pruning.
func Teardown(ctx context.Context, d Deps, sess state.Session, opts TeardownOpts) error {
	_ = d.StopBridge(sess.Name)
	d.KillTmux(sess.ChannelID)
	if sess.Worktree != "" {
		if err := d.RemoveWorktree(opts.Repo, sess.Name, opts.Force); err != nil {
			return err
		}
	}
	_ = d.ArchiveChannel(ctx, sess.ChannelID) // best-effort
	if err := d.RemoveSession(sess.Name); err != nil {
		return err
	}
	d.RemoveJournal(sess.Name)
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sessionclean/ -run TestTeardown -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add internal/sessionclean/teardown.go internal/sessionclean/teardown_test.go
git commit -m "feat(sessionclean): add shared Teardown over Deps interface"
```

---

## Task 6: Daemon `/session clean`

**Files:**
- Modify: `internal/handler/handler.go` (struct, NewHandler, dispatch, refactor `sessionClose`, add `sessionClean`)
- Modify: `interactions.go:188-219` (register subcommand)
- Modify: `cmd/dctl/serve.go` (pass maxIdle into NewHandler)
- Test: `internal/handler/handler_clean_test.go` (create)

This task wires the daemon. It also refactors `sessionClose` to call `sessionclean.Teardown`, proving the shared path before the CLI reuses it.

- [ ] **Step 1: Add a daemon Deps adapter + refactor sessionClose**

In `internal/handler/handler.go`, add a small adapter type and a helper that builds it, then rewrite `sessionClose` (lines 597-625) to use `Teardown`. Add this adapter near the bottom of the file:

```go
// daemonDeps adapts the handler's live collaborators to sessionclean.Deps so the
// online /session close and /session clean share one teardown path.
type daemonDeps struct{ h *Handler }

func (d daemonDeps) StopBridge(name string) error { return d.h.sup.Stop(name) }
func (d daemonDeps) KillTmux(channelID string)    { session.KillTmuxSession(channelID) }
func (d daemonDeps) RemoveWorktree(repo, name string, force bool) error {
	return d.h.wt.Remove(repo, name, force)
}
func (d daemonDeps) ArchiveChannel(ctx context.Context, channelID string) error {
	return d.h.d.ArchiveChannel(ctx, channelID)
}
func (d daemonDeps) RemoveSession(name string) error { return d.h.st.RemoveSession(name) }
func (d daemonDeps) RemoveJournal(name string) {
	_ = state.RemoveParticipantJournal(state.ParticipantsPath(d.h.partDir, name))
}
```

Now rewrite `sessionClose` to delegate:

```go
func (h *Handler) sessionClose(ctx context.Context, in dctl.Interaction) dctl.Response {
	name, ok := in.Data.Opt("name")
	if !ok {
		return errf("missing name")
	}
	sess, exists := h.st.FindSession(name)
	if !exists {
		return errf("no session %q", name)
	}
	repo := repoFor(h.st.WorkspaceRoot(), sess.Project)
	force := in.Data.OptBool("force")
	if err := sessionclean.Teardown(ctx, daemonDeps{h}, sess, sessionclean.TeardownOpts{Repo: repo, Force: force}); err != nil {
		return errf("%v — commit, or close with force:true to discard (branch session/%s is kept)", err, name)
	}
	return dctl.Response{Content: fmt.Sprintf("🗄️ Session **%s** closed.", name), Ephemeral: true}
}
```

Add the import `"github.com/vskstudio/dctl/internal/sessionclean"` to the handler's import block. (`session` and `state` are already imported.)

- [ ] **Step 2: Run existing handler tests to confirm no regression**

Run: `go test ./internal/handler/ -v`
Expected: PASS — existing close behaviour is preserved by the refactor. If a test referenced the old inline sequence, it still observes the same effects through the mocks.

- [ ] **Step 3: Store maxIdle on the handler**

Modify the `Handler` struct (line 112-122) to add a field after `partDir`:

```go
	partDir     string   // dir holding participants/<name>.log journals
	maxIdle     time.Duration // session clean stale threshold (0 disables)
```

Update `NewHandler` (line 129-131) signature and body to accept and set it:

```go
func NewHandler(d discord, sup supervisor, wt worktrees, fg forges, up updater, st *state.State, defaultCmd string, defaultInit []string, partDir string, maxIdle time.Duration) *Handler {
	return &Handler{d: d, sup: sup, wt: wt, fg: fg, up: up, st: st, defaultCmd: defaultCmd, defaultInit: defaultInit, partDir: partDir, maxIdle: maxIdle}
}
```

Add `"time"` to the handler imports if not present.

- [ ] **Step 4: Update the serve wiring**

In `cmd/dctl/serve.go`, find the `handler.NewHandler(...)` call and add the maxIdle argument. Compute it from config with the 14-day default:

```go
maxIdle := 14 * 24 * time.Hour
if cfg.SessionMaxIdleDays != 0 {
	maxIdle = time.Duration(cfg.SessionMaxIdleDays) * 24 * time.Hour
}
```

Then pass `maxIdle` as the final argument to `handler.NewHandler(...)`. Ensure `"time"` is imported in `serve.go`.

> Note: `cfg.SessionMaxIdleDays == 0` means "unset → default 14". To let an operator truly disable stale detection, document that they set a negative-free sentinel; per spec, `0` disables. Resolve the ambiguity here: treat config `0`/absent as default 14, and let the *runtime* disable happen only via the CLI/slash flag (stale is opt-in via `stale:true` anyway, so a 14-day default is harmless when stale is not requested). Keep this exactly as written.

- [ ] **Step 5: Register the `clean` subcommand**

In `interactions.go`, inside the `"session"` command's `options` array (after the `"list"` entry at line 205), add:

```go
				map[string]any{"name": "clean", "description": "Bulk-remove dead sessions (dry-run unless apply:true)", "type": typeSub, "options": []map[string]any{
					{"name": "apply", "description": "Actually perform the cleanup (default: dry-run)", "type": typeBool},
					{"name": "all", "description": "Also close LIVE sessions, not just dead ones", "type": typeBool},
					{"name": "stale", "description": "Include long-inactive sessions as candidates", "type": typeBool},
				}},
```

> Note: confirm the boolean option type constant name used elsewhere in this file (search for existing bool options, e.g. the `shared`/`force` options). Use that same constant (shown here as `typeBool`). If booleans are declared inline as an integer (Discord type 5), match the existing style verbatim.

- [ ] **Step 6: Add the dispatch case**

In the `/session` dispatch switch (line 339-350), add before `default`:

```go
		case "clean":
			return h.sessionClean(ctx, in)
```

- [ ] **Step 7: Write the failing test for sessionClean**

Create `internal/handler/handler_clean_test.go`. Model it on the existing handler tests (inspect `internal/handler/handler_test.go` for the mock `discord`/`supervisor`/`worktrees` fakes and the `Handler` construction helper — reuse them). The test drives a dry-run clean over one dead and one live session and asserts nothing is torn down:

```go
package handler

import (
	"strings"
	"testing"
	// reuse the same test helpers/fakes the existing handler tests use
)

func TestSessionCleanDryRunTouchesNothing(t *testing.T) {
	// Arrange: a handler with two sessions in state — see handler_test.go for the
	// exact constructor/fakes. One session's channel/tmux/worktree are all absent
	// (dead), one is alive.
	// Build the interaction for /session clean with NO options (apply=false).
	// Act: call h.Handle(...) or h.sessionClean(...) directly.
	// Assert: the response Content lists the dead session under a "would remove"
	// heading, the supervisor's Stop was never called, and state still has both
	// sessions.
	t.Skip("fill in using the fakes from handler_test.go")
}
```

> The agent implementing this MUST replace the `t.Skip` with a real test using the existing fakes in `internal/handler/handler_test.go`. Read that file first; mirror its construction (mock `discord` with a `ChannelType` that returns an error for the dead channel, a `supervisor` recording `Stop` calls). Assert: (a) dry-run reports but does not call `Stop`/`RemoveSession`; (b) with `apply:true`, the dead session is removed and `Stop` is called for it; (c) without `all:true`, a live session is never removed.

- [ ] **Step 8: Implement `sessionClean`**

Add to `internal/handler/handler.go`:

```go
// sessionClean bulk-removes sessions. By default (apply:false) it only reports.
// Dead sessions (no proof of life) are removed when apply:true; live sessions are
// touched only with all:true; stale sessions count as candidates only with
// stale:true (and still need all:true to be closed, since they are alive).
func (h *Handler) sessionClean(ctx context.Context, in dctl.Interaction) dctl.Response {
	apply := in.Data.OptBool("apply")
	all := in.Data.OptBool("all")
	wantStale := in.Data.OptBool("stale")

	maxIdle := time.Duration(0)
	if wantStale {
		maxIdle = h.maxIdle
	}
	probes := sessionclean.Probes{
		TmuxExists: session.TmuxSessionExists,
		WorktreeAt: func(path string) bool { _, err := os.Stat(path); return err == nil },
		ChannelLive: func(ctx context.Context, id string) (bool, error) {
			if _, err := h.d.ChannelType(ctx, id); err != nil {
				if isChannelGone(err) {
					return false, nil
				}
				return false, err
			}
			return true, nil
		},
		LastMessage: h.lastMessageProbe,
	}
	cands := sessionclean.Inspect(ctx, h.st.SnapshotSessions(), probes, maxIdle, time.Now())

	var dead, live, stale, errd []sessionclean.Candidate
	for _, c := range cands {
		switch {
		case c.ProbeErr != "":
			errd = append(errd, c)
		case c.Dead:
			dead = append(dead, c)
		case c.Stale:
			stale = append(stale, c)
		default:
			live = append(live, c)
		}
	}

	var b strings.Builder
	report := func(title string, cs []sessionclean.Candidate) {
		if len(cs) == 0 {
			return
		}
		fmt.Fprintf(&b, "**%s** (%d):\n", title, len(cs))
		for _, c := range cs {
			fmt.Fprintf(&b, "• %s — %s\n", c.Session.Name, joinReasons(c.Reasons))
		}
	}
	report("Dead", dead)
	if all {
		report("Live (will close)", live)
		if wantStale {
			report("Stale (will close)", stale)
		}
	} else {
		report("Live (kept; use all:true)", live)
		if wantStale {
			report("Stale (kept; use all:true)", stale)
		}
	}
	report("Probe errors (skipped)", errd)

	if !apply {
		if b.Len() == 0 {
			return dctl.Response{Content: "Nothing to clean.", Ephemeral: true}
		}
		return dctl.Response{Content: "🔎 Dry-run — nothing removed.\n\n" + b.String(), Ephemeral: true}
	}

	// Apply: tear down dead always; live/stale only with all:true.
	targets := append([]sessionclean.Candidate{}, dead...)
	if all {
		targets = append(targets, live...)
		if wantStale {
			targets = append(targets, stale...)
		}
	}
	var closed, failed int
	for _, c := range targets {
		repo := repoFor(h.st.WorkspaceRoot(), c.Session.Project)
		if err := sessionclean.Teardown(ctx, daemonDeps{h}, c.Session, sessionclean.TeardownOpts{Repo: repo, Force: true}); err != nil {
			fmt.Fprintf(&b, "\n⚠️ %s: %v", c.Session.Name, err)
			failed++
			continue
		}
		closed++
	}
	return dctl.Response{Content: fmt.Sprintf("🧹 Cleaned %d session(s), %d failed.\n\n%s", closed, failed, b.String()), Ephemeral: true}
}
```

Add the small helpers (same file):

```go
// lastMessageProbe adapts the Discord client to the sessionclean probe shape. It
// is a method so the test fake's client is used; the live handler's discord
// interface must expose LastMessageAt (add it — see Step 9).
func (h *Handler) lastMessageProbe(ctx context.Context, channelID string) (time.Time, error) {
	return h.d.LastMessageAt(ctx, channelID)
}

func joinReasons(rs []sessionclean.Reason) string {
	if len(rs) == 0 {
		return "alive"
	}
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = string(r)
	}
	return strings.Join(parts, ", ")
}

// isChannelGone reports whether err from ChannelType means the channel no longer
// exists (HTTP 404) as opposed to a transient transport error.
func isChannelGone(err error) bool {
	// dctl.Client surfaces HTTP failures with the status code in the message;
	// match the 404 marker used by Client.do. Adjust to the actual sentinel/format
	// in dctl.go (search for "404" / status formatting) when implementing.
	return err != nil && strings.Contains(err.Error(), "404")
}
```

> **Implementer note:** verify how `Client.do` reports a 404 (inspect `dctl.go` for the error format — it may wrap a typed `*APIError` with a `Status` field). Prefer matching a typed error over substring-matching "404" if one exists. If a typed error exists, change `isChannelGone` to use `errors.As`.

- [ ] **Step 9: Extend the handler's `discord` interface**

In `internal/handler/handler.go`, add `LastMessageAt` to the `discord` interface (line 72-78):

```go
type discord interface {
	ChannelType(ctx context.Context, id string) (int, error)
	CreateChannelUnder(ctx context.Context, parentID, name string) (*dctl.Channel, error)
	ForumPost(ctx context.Context, forumID, name, content string) (*dctl.Channel, error)
	ArchiveChannel(ctx context.Context, id string) error
	Send(ctx context.Context, channelID, content string) (*dctl.Message, error)
	LastMessageAt(ctx context.Context, channelID string) (time.Time, error)
}
```

Update the test fake `discord` in `handler_test.go` to implement `LastMessageAt`.

- [ ] **Step 10: Run the tests**

Run: `go test ./internal/handler/ -v` then `go build ./...`
Expected: PASS and a clean build. Fix the `NewHandler` call sites flagged by the compiler (there is the serve.go one from Step 4 and possibly tests).

- [ ] **Step 11: Commit**

```bash
git add internal/handler/ interactions.go cmd/dctl/serve.go
git commit -m "feat(handler): add /session clean and share teardown with close"
```

---

## Task 7: CLI `dctl sessions clean`

**Files:**
- Create: `cmd/dctl/sessions.go`
- Modify: `cmd/dctl/main.go:53` (add case) and usage text
- Test: `cmd/dctl/sessions_test.go` (create)

The CLI is offline-only: it loads `state.json` directly, refuses to run while the daemon answers its health endpoint, performs the same `Inspect` + `Teardown`, and additionally scans for orphan residue.

- [ ] **Step 1: Write the failing test for the daemon-alive guard and flag parsing**

Create `cmd/dctl/sessions_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDaemonAliveRefuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if !daemonAlive(srv.URL) {
		t.Fatal("expected daemonAlive to detect a live health endpoint")
	}
}

func TestDaemonAliveDown(t *testing.T) {
	// An address nobody is listening on must report not-alive quickly.
	if daemonAlive("http://127.0.0.1:0") {
		t.Fatal("expected daemonAlive=false for a dead endpoint")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/dctl/ -run TestDaemonAlive -v`
Expected: FAIL — `daemonAlive` undefined.

- [ ] **Step 3: Implement the CLI command**

Create `cmd/dctl/sessions.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vskstudio/dctl"
	"github.com/vskstudio/dctl/internal/config"
	"github.com/vskstudio/dctl/internal/session"
	"github.com/vskstudio/dctl/internal/sessionclean"
	"github.com/vskstudio/dctl/internal/state"
)

func runSessions(ctx context.Context, c *dctl.Client, args []string) error {
	if len(args) == 0 || args[0] != "clean" {
		return fmt.Errorf("usage: dctl sessions clean [--apply] [--all] [--stale] [--prune-branches] [--max-idle DAYS] [--state FILE]")
	}
	fs := flag.NewFlagSet("sessions clean", flag.ExitOnError)
	apply := fs.Bool("apply", false, "actually perform the cleanup (default: dry-run)")
	all := fs.Bool("all", false, "also close LIVE sessions, not just dead ones")
	stale := fs.Bool("stale", false, "include long-inactive sessions as candidates")
	pruneBranches := fs.Bool("prune-branches", false, "also delete orphan session/* git branches (destructive)")
	maxIdleDays := fs.Int("max-idle", 0, "stale threshold in days (0 = config default)")
	statePath := fs.String("state", defaultStatePath(), "path to daemon state.json")
	_ = fs.Parse(args[1:])

	cfg, _ := config.Load(config.DefaultPath())

	// Offline guard: never write state while the daemon owns it.
	if *apply && cfg.HealthAddr != "" && daemonAlive(healthURL(cfg.HealthAddr)) {
		return fmt.Errorf("daemon is running — use the Discord /session clean instead, or stop the service first")
	}

	st, err := state.LoadState(*statePath)
	if err != nil {
		return err
	}

	maxIdle := time.Duration(0)
	if *stale {
		days := *maxIdleDays
		if days == 0 {
			days = cfg.SessionMaxIdleDays
		}
		if days == 0 {
			days = 14
		}
		maxIdle = time.Duration(days) * 24 * time.Hour
	}

	probes := sessionclean.Probes{
		TmuxExists: session.TmuxSessionExists,
		WorktreeAt: func(p string) bool { _, err := os.Stat(p); return err == nil },
		ChannelLive: func(ctx context.Context, id string) (bool, error) {
			if _, err := c.ChannelType(ctx, id); err != nil {
				if strings.Contains(err.Error(), "404") {
					return false, nil
				}
				return false, err
			}
			return true, nil
		},
		LastMessage: c.LastMessageAt,
	}
	cands := sessionclean.Inspect(ctx, st.SnapshotSessions(), probes, maxIdle, time.Now())

	// Report + act. Offline StopBridge is a no-op; we kill tmux directly in Deps.
	deps := offlineDeps{st: st}
	var closed int
	for _, cand := range cands {
		dead := cand.Dead
		actionable := dead || (*all && cand.ProbeErr == "")
		mark := "keep"
		if dead {
			mark = "dead"
		} else if cand.Stale {
			mark = "stale"
		} else if cand.ProbeErr != "" {
			mark = "probe-error: " + cand.ProbeErr
		} else {
			mark = "live"
		}
		fmt.Printf("%s\t%s\t%s\n", cand.Session.Name, mark, strings.Join(reasonStrings(cand.Reasons), ","))
		if *apply && actionable {
			repo := repoForCLI(st, cand.Session.Project)
			if err := sessionclean.Teardown(ctx, deps, cand.Session, sessionclean.TeardownOpts{Repo: repo, Force: true}); err != nil {
				fmt.Fprintf(os.Stderr, "  %s: %v\n", cand.Session.Name, err)
				continue
			}
			closed++
		}
	}

	// Orphan residue: worktrees / journals / branches with no state entry.
	orphans := scanOrphans(st)
	for _, o := range orphans {
		fmt.Printf("orphan\t%s\t%s\n", o.Kind, o.Path)
		if *apply {
			if o.Kind == "branch" && !*pruneBranches {
				continue // never delete branches without explicit opt-in
			}
			if err := o.Remove(); err != nil {
				fmt.Fprintf(os.Stderr, "  %s %s: %v\n", o.Kind, o.Path, err)
			}
		}
	}

	if !*apply {
		fmt.Println("# dry-run — nothing removed; re-run with --apply")
	} else {
		fmt.Printf("# cleaned %d session(s)\n", closed)
	}
	return nil
}

// daemonAlive reports whether a GET to healthURL returns a response (any status),
// meaning the daemon owns state.json and the CLI must not write it.
func daemonAlive(healthURL string) bool {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(healthURL)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// healthURL turns a serve --health-addr like ":8787" or "0.0.0.0:8787" into a
// loopback URL the CLI can probe.
func healthURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	addr = strings.Replace(addr, "0.0.0.0:", "127.0.0.1:", 1)
	return "http://" + addr + "/health"
}

func reasonStrings(rs []sessionclean.Reason) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = string(r)
	}
	return out
}
```

> **Implementer notes:**
> - `defaultStatePath()` — reuse whatever the existing CLI/serve code uses to locate `state.json` (search `cmd/dctl` and `internal/serve` for `state.json` path resolution; there is a `DefaultStatePath`-like helper — call it, do not reinvent the path logic). If it lives in another package, import and call it.
> - `repoForCLI(st, project)` — mirror the handler's `repoFor(workspaceRoot, project)` using `st.WorkspaceRoot()`. Implement it in this file (a 3-line join), since `repoFor` is in the handler package.
> - The `/health` path must match what `serve.go` actually serves (confirm the route; it may be `/health` exactly — Step uses that).

- [ ] **Step 4: Implement the offline Deps + orphan scan**

Add to `cmd/dctl/sessions.go`:

```go
// offlineDeps satisfies sessionclean.Deps without a supervisor. StopBridge is a
// no-op: the supervised bridge (if any) is not in this process. tmux panes and
// any stray PID are reaped via KillTmuxSession; disk/state cleanup proceeds.
type offlineDeps struct{ st *state.State }

func (o offlineDeps) StopBridge(string) error    { return nil }
func (o offlineDeps) KillTmux(channelID string)  { session.KillTmuxSession(channelID) }
func (o offlineDeps) RemoveWorktree(repo, name string, force bool) error {
	// Offline removal: drop the worktree dir. Use git if available for a clean
	// `git worktree remove`, else fall back to RemoveAll. Keep the branch.
	return removeWorktreeOffline(repo, name, force)
}
func (o offlineDeps) ArchiveChannel(ctx context.Context, channelID string) error { return nil }
func (o offlineDeps) RemoveSession(name string) error { return o.st.RemoveSession(name) }
func (o offlineDeps) RemoveJournal(name string) {
	_ = state.RemoveParticipantJournal(state.ParticipantsPath(participantsDirFor(o.st), name))
}

type orphan struct {
	Kind string // "worktree" | "journal" | "branch" | "tmux"
	Path string
	Remove func() error
}

// scanOrphans finds residue with no matching state entry. See implementer notes
// for the exact directories. It NEVER returns a branch with a Remove that the
// caller will run unless --prune-branches is set (the caller enforces that).
func scanOrphans(st *state.State) []orphan {
	// Implementer: enumerate <repo>/.dctl-sessions/** dirs, participants/*.log,
	// and `git branch --list 'session/*'` under each project repo; diff against
	// st.SnapshotSessions() names. Build orphan entries with a Remove closure.
	// Return nil if nothing found. Keep this focused; see notes.
	return nil
}
```

> **Implementer notes for Step 4 (fill the stubs, do not ship them empty):**
> - `removeWorktreeOffline(repo, name, force)` — run `git -C <repo> worktree remove [--force] <path>` via `os/exec`; the worktree path is `<repo>/.dctl-sessions/<instanceID>/<name>` (read `st.InstanceID` — there is a path helper in `internal/worktree`; prefer calling `worktree.NewWorktreer(...).Path(repo, name)` if it is exported, to avoid duplicating the path scheme). On `git` failure, fall back to `os.RemoveAll(path)`.
> - `participantsDirFor(st)` — the participants dir is `filepath.Dir(statePath)/participants` in the daemon; resolve it the same way serve.go does (search for `participants` dir construction). Reuse that helper.
> - `scanOrphans` — implement the three sweeps. For worktrees: `os.ReadDir` of each project's `.dctl-sessions/<instanceID>/`; a subdir whose name is not a known session is an orphan worktree. For journals: `participants/*.log` whose basename (minus `.log`) is not a known session. For branches: `git -C <repo> for-each-ref --format='%(refname:short)' refs/heads/session/` and diff. Each `orphan.Remove` does the corresponding deletion (`git worktree remove`/`os.Remove`/`git branch -D`). Add a focused test `TestScanOrphans` with a temp repo.

- [ ] **Step 5: Wire the CLI case + usage**

In `cmd/dctl/main.go`, add to the switch (after `case "channel":`, line 54):

```go
	case "sessions":
		err = runSessions(ctx, client, args)
```

Add a usage line in `usage()` after the `channel` entry:

```go
  dctl sessions clean [--apply] [--all] [--stale] [--prune-branches]
                      [--max-idle DAYS] [--state FILE]
                                              offline maintenance: purge dead
                                              sessions, sweep orphan worktrees/
                                              journals; dry-run unless --apply.
                                              Refuses to run while the daemon is up
```

- [ ] **Step 6: Run tests + build**

Run: `go test ./cmd/dctl/ -v && go build ./...`
Expected: PASS (`TestDaemonAlive*`, `TestScanOrphans`) and a clean build.

- [ ] **Step 7: Manual dry-run smoke test**

Run: `go run ./cmd/dctl sessions clean` (with no daemon and an empty/absent state)
Expected: prints `# dry-run — nothing removed; re-run with --apply` and exits 0.

- [ ] **Step 8: Commit**

```bash
git add cmd/dctl/sessions.go cmd/dctl/main.go cmd/dctl/sessions_test.go
git commit -m "feat(cli): add 'dctl sessions clean' offline maintenance"
```

---

## Task 8: Documentation

**Files:**
- Modify: `README.md` (or the skill doc that documents `/session` — search for "session close")

- [ ] **Step 1: Document both surfaces**

Find where `/session close` and the CLI are documented (search the repo for `session close` and `dctl channel`). Add, in the same style:

- `/session clean [apply] [all] [stale]` — bulk cleanup from Discord. Dry-run by default; `apply:true` removes dead sessions; `all:true` also closes live ones; `stale:true` includes long-inactive sessions (threshold `sessionMaxIdleDays`, default 14).
- `dctl sessions clean [--apply] [--all] [--stale] [--prune-branches] [--max-idle DAYS]` — offline equivalent; refuses to run while the daemon is up; also sweeps orphan worktrees/journals (`--prune-branches` for orphan `session/*` branches).

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: document session clean (slash + CLI)"
```

---

## Self-Review Notes

- **Spec coverage:** dead-purge (Tasks 4-7), close-all opt-in (`all`, Tasks 6-7), orphan sweep (Task 7 Step 4), stale via Discord last-message (Tasks 3-4), dry-run + `--all` double guard (Tasks 6-7), both surfaces (Tasks 6, 7), configurable maxIdle (Tasks 1, 6, 7), branches listed-not-deleted unless `--prune-branches` (Task 7), shared logic + teardown refactor (Tasks 4-6). All covered.
- **Probe-error conservatism** is enforced in `Inspect` (Task 4) and tested.
- **Implementer notes** flag the three places where exact repo APIs must be confirmed (404 error shape, state/participants path helpers, worktree path helper). These are deliberate — the agent must read the existing code rather than guess, and the notes say exactly what to look for.
- **Type consistency:** `Probes`, `Candidate`, `Reason`, `Teardown`, `TeardownOpts`, `Deps` names are used identically across Tasks 4-7. `daemonDeps`/`offlineDeps` both implement `Deps`.
