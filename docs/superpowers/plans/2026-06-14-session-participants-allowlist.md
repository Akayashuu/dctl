# Implementation Plan — Session participants memory + per-session allowlist (Spec 4/4)

**Source spec:** `/home/shan/dev/dctl/docs/superpowers/specs/2026-06-14-session-participants-allowlist-design.md`
**Module:** `github.com/vskstudio/dctl` (Go 1.23)

## Goal

Give each session a memory of *who has written to it* (observed participants) and an
explicit *per-session allowlist*, so authorization can be `global OR per-session`
(union, never replacement). Add slash subcommands `/session allow add|remove|list`
and `/session who`, and record participants from the bridge child process via an
**append-only journal** (approach P2 of the spec — decoupled, no concurrent
`state.json` writes, does not depend on Spec 3's state lock).

## Architecture

- **State layer** (`internal/state/state.go`): two `omitempty` slices on `Session`
  (`Allow`, `Participants`) + six mutex-guarded methods. `Allow` is curated
  intent; `Participants` is the canonical *cache* of observed authors. With P2 the
  **journal is the source of truth** for participants; `SessionParticipants` reads
  the journal (union'd with any persisted `Participants`), so the in-struct field
  stays available for a later P1 migration but is not the read path today.
- **Authorization** (`internal/handler/handler.go`): all slash-commands stay gated
  by the **global** `h.st.Allowed(...)` at the top of `Handle` (management
  actions). The per-session allowlist only widens who may drive the *bridge* — it
  is **not** applied to slash-commands. Spec §8 step 4 (bridge-side enforcement,
  semantics B) is explicitly **out of scope** here; this plan delivers steps 1–3
  (state + commands + journal recording). The allowlist is declarative for now.
- **Participant recording** (`internal/bridge/bridge.go`): the bridge is a separate
  child process with no access to the daemon `*State`. After the `m.Author.Bot`
  filter it appends `m.Author.ID` to a per-session journal file
  (`<dir>/participants/<name>.log`, one ID per line, `O_APPEND`). A new
  `--participants <path>` flag (parsed in `cmd/dctl/bridge.go`, threaded from
  `Options.Participants`) selects the file; empty disables recording.
- **Wiring** (`internal/supervisor/supervisor.go`, `internal/serve/serve.go`): the
  supervisor passes `--participants <journalPath>` to each child `dctl bridge`. The
  journal directory is derived from the state-file directory
  (`filepath.Dir(statePath)/participants/<name>.log`), exposed via a small helper so
  handler and supervisor agree on the path. The daemon reads the journal at
  `/session who` / `/session allow list` time.
- **Slash-command declaration** (`interactions.go` `dctlCommands()`): nest a
  `allow` sub-command-**group** (type 2) under `session` with `add`/`remove`/`list`
  subcommands, and a `who` subcommand. Add a `participants` purge on session close
  (delete the journal file) to avoid file leaks (spec §7).

## Tech Stack

Go stdlib only (`os`, `bufio`, `path/filepath`, `strings`, `sync`, `encoding/json`).
Tests are standard `testing` table/round-trip style, matching existing
`state_test.go` / `handler_test.go` conventions. No new dependencies.

**For agentic workers:** REQUIRED SUB-SKILL: `superpowers:subagent-driven-development`.
Execute each task as an isolated TDD cycle (failing test → run-fail → minimal impl →
run-pass → commit). Do not batch tasks. Use exact file paths below; no placeholders.

---

## File Structure

```
internal/state/state.go                 (EDIT)  Session.Allow/Participants + 6 methods + ParticipantsPath helper
internal/state/state_test.go            (EDIT)  round-trip + per-session allow/participant unit tests
internal/state/journal.go               (NEW)   append-only journal read/append/remove helpers
internal/state/journal_test.go          (NEW)   journal append/read/dedup/remove tests
internal/handler/handler.go             (EDIT)  /session allow add|remove|list + /session who routing
internal/handler/handler_test.go        (EDIT)  routing tests for the four new paths
internal/bridge/bridge.go               (EDIT)  Options.Participants + record author in Run loop
internal/bridge/bridge_test.go          (NEW)   recordParticipant appends id, skips empty path
cmd/dctl/bridge.go                      (EDIT)  --participants flag → Options.Participants
internal/supervisor/supervisor.go       (EDIT)  Participants dir field + pass --participants to child
internal/serve/serve.go                 (EDIT)  derive participants dir from state path, wire supervisor+handler
interactions.go                         (EDIT)  declare session allow group + who subcommand
```

The journal lives in `internal/state` (not bridge) because **both** the bridge
(append) and the daemon (read, via handler) need it; placing it in `state` avoids an
import cycle (handler already imports state; bridge will import state for the helper).

---

## Task 1 — `Session.Allow` / `Session.Participants` fields + per-session methods

### 1a. Failing test

Append to `/home/shan/dev/dctl/internal/state/state_test.go`:

```go
func TestSessionAllowlist(t *testing.T) {
	s := NewState(filepath.Join(t.TempDir(), "s.json"))
	if err := s.AddSession(Session{Name: "a", ChannelID: "c1"}); err != nil {
		t.Fatal(err)
	}
	added, err := s.AddSessionAllow("a", "u1")
	if err != nil || !added {
		t.Fatalf("first add should report new: added=%v err=%v", added, err)
	}
	again, err := s.AddSessionAllow("a", "u1")
	if err != nil || again {
		t.Fatalf("second add should be idempotent (added=false): added=%v err=%v", again, err)
	}
	if !s.SessionAllowed("a", "u1") {
		t.Fatal("u1 should be allowed on session a")
	}
	if s.SessionAllowed("a", "u2") {
		t.Fatal("u2 not on any list")
	}
	// global OR per-session: a globally-allowed user passes even if not in session list
	s.AddAllow("g1")
	if !s.SessionAllowed("a", "g1") {
		t.Fatal("globally allowed user must pass SessionAllowed")
	}
	if list := s.SessionAllowlist("a"); len(list) != 1 || list[0] != "u1" {
		t.Fatalf("SessionAllowlist should hold only curated entry: %+v", list)
	}
	removed, err := s.RemoveSessionAllow("a", "u1")
	if err != nil || !removed {
		t.Fatalf("remove should report true: removed=%v err=%v", removed, err)
	}
	if s.SessionAllowed("a", "u1") {
		t.Fatal("u1 should be gone after remove")
	}
	if _, err := s.AddSessionAllow("missing", "u1"); err == nil {
		t.Fatal("add to missing session must error")
	}
}

func TestSessionAllowPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	s := NewState(path)
	s.AddSession(Session{Name: "a", ChannelID: "c1"})
	s.AddSessionAllow("a", "u1")
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.SessionAllowed("a", "u1") {
		t.Fatal("per-session allow must survive reload")
	}
}
```

### 1b. Run-fail

```
go test ./internal/state/
```

Expected: compile failure — `s.AddSessionAllow undefined`, `s.SessionAllowed
undefined`, `s.SessionAllowlist undefined`, `s.RemoveSessionAllow undefined`.

### 1c. Minimal impl

In `/home/shan/dev/dctl/internal/state/state.go`, extend the `Session` struct:

```go
// Session is one bridged channel/post supervised by the daemon.
type Session struct {
	Name         string   `json:"name"`
	ChannelID    string   `json:"channelID"`
	Type         string   `json:"type"` // "text" | "forum"
	Cmd          string   `json:"cmd"`
	Worktree     string   `json:"worktree,omitempty"`     // abs path; empty for a shared session
	Allow        []string `json:"allow,omitempty"`        // curated per-session allowlist
	Participants []string `json:"participants,omitempty"` // observed authors (cache; journal is source of truth)
}
```

Add these methods (anywhere after `RemoveSession`, before `SetHome`):

```go
// sessionIndexLocked returns the index of the named session, or -1.
func (s *State) sessionIndexLocked(name string) int {
	for i := range s.Sessions {
		if s.Sessions[i].Name == name {
			return i
		}
	}
	return -1
}

// AddSessionAllow adds userID to the session's per-session allowlist.
// Returns (true, nil) if newly added, (false, nil) if already present,
// and an error if the session does not exist.
func (s *State) AddSessionAllow(name, userID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.sessionIndexLocked(name)
	if i < 0 {
		return false, fmt.Errorf("no session %q", name)
	}
	for _, id := range s.Sessions[i].Allow {
		if id == userID {
			return false, nil
		}
	}
	s.Sessions[i].Allow = append(s.Sessions[i].Allow, userID)
	return true, s.saveLocked()
}

// RemoveSessionAllow removes userID from the session's allowlist.
// Returns (true, nil) if it was present, (false, nil) if absent.
func (s *State) RemoveSessionAllow(name, userID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.sessionIndexLocked(name)
	if i < 0 {
		return false, fmt.Errorf("no session %q", name)
	}
	out := s.Sessions[i].Allow[:0]
	found := false
	for _, id := range s.Sessions[i].Allow {
		if id == userID {
			found = true
			continue
		}
		out = append(out, id)
	}
	s.Sessions[i].Allow = out
	if !found {
		return false, nil
	}
	return true, s.saveLocked()
}

// SessionAllowed reports whether userID may drive the session's bridge:
// global allowlist OR the session's per-session allowlist.
func (s *State) SessionAllowed(name, userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.Allow { // global
		if id == userID {
			return true
		}
	}
	i := s.sessionIndexLocked(name)
	if i < 0 {
		return false
	}
	for _, id := range s.Sessions[i].Allow {
		if id == userID {
			return true
		}
	}
	return false
}

// SessionAllowlist returns a copy of the session's curated allowlist (nil if none).
func (s *State) SessionAllowlist(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.sessionIndexLocked(name)
	if i < 0 {
		return nil
	}
	return append([]string(nil), s.Sessions[i].Allow...)
}
```

> Note: `RecordParticipant` / `SessionParticipants` from spec §2 are *re-homed* onto
> the journal in Task 2 (P2). The struct field `Participants` stays for a future P1
> migration but the daemon read path goes through the journal, so we do not add an
> in-`State` `RecordParticipant` here.

### 1d. Run-pass

```
go test ./internal/state/
```

Expected: `ok  github.com/vskstudio/dctl/internal/state`.

### 1e. Commit

```
git add internal/state/state.go internal/state/state_test.go
git commit -m "state: per-session allowlist (Allow field + AddSessionAllow/RemoveSessionAllow/SessionAllowed/SessionAllowlist)"
```

---

## Task 2 — Append-only participants journal (P2)

### 2a. Failing test

Create `/home/shan/dev/dctl/internal/state/journal_test.go`:

```go
package state

import (
	"path/filepath"
	"testing"
)

func TestParticipantJournalAppendAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "participants", "a.log")
	added, err := AppendParticipant(path, "u1")
	if err != nil || !added {
		t.Fatalf("first append should report new: added=%v err=%v", added, err)
	}
	if again, _ := AppendParticipant(path, "u1"); again {
		t.Fatal("duplicate append should report false (idempotent)")
	}
	AppendParticipant(path, "u2")
	got := ReadParticipants(path)
	if len(got) != 2 || got[0] != "u1" || got[1] != "u2" {
		t.Fatalf("expected [u1 u2] in order, got %+v", got)
	}
}

func TestReadParticipantsMissingFileIsEmpty(t *testing.T) {
	if got := ReadParticipants(filepath.Join(t.TempDir(), "nope.log")); len(got) != 0 {
		t.Fatalf("missing journal should read empty, got %+v", got)
	}
}

func TestRemoveParticipantJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p", "a.log")
	AppendParticipant(path, "u1")
	if err := RemoveParticipantJournal(path); err != nil {
		t.Fatal(err)
	}
	if got := ReadParticipants(path); len(got) != 0 {
		t.Fatalf("journal should be gone, got %+v", got)
	}
	// removing an already-absent journal is not an error
	if err := RemoveParticipantJournal(path); err != nil {
		t.Fatalf("removing missing journal must be a no-op, got %v", err)
	}
}
```

### 2b. Run-fail

```
go test ./internal/state/
```

Expected: `undefined: AppendParticipant`, `undefined: ReadParticipants`,
`undefined: RemoveParticipantJournal`.

### 2c. Minimal impl

Create `/home/shan/dev/dctl/internal/state/journal.go`:

```go
package state

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// AppendParticipant records userID in the append-only journal at path (one id
// per line). It is idempotent: if userID is already present, the file is left
// untouched and added is false. A missing parent directory is created.
//
// Append uses O_APPEND so concurrent appenders (the bridge child) never race
// with the daemon's reads; the daemon only reads this file, never writes it.
func AppendParticipant(path, userID string) (added bool, err error) {
	if path == "" || userID == "" {
		return false, nil
	}
	for _, id := range ReadParticipants(path) {
		if id == userID {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if _, err := f.WriteString(userID + "\n"); err != nil {
		return false, err
	}
	return true, nil
}

// ReadParticipants returns the de-duplicated user ids in the journal at path,
// in first-seen order. A missing file yields an empty slice (no error: the
// journal is best-effort observability).
func ReadParticipants(path string) []string {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		id := strings.TrimSpace(sc.Text())
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// RemoveParticipantJournal deletes the journal at path. A missing file is not
// an error (called on session close to avoid leaking participants/*.log).
func RemoveParticipantJournal(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ParticipantsPath returns the journal path for session name under dir
// (dir/participants/<name>.log). Both the supervisor (which tells the bridge
// where to append) and the handler (which reads it) call this so they agree.
func ParticipantsPath(dir, name string) string {
	return filepath.Join(dir, "participants", name+".log")
}
```

### 2d. Run-pass

```
go test ./internal/state/
```

Expected: `ok`.

### 2e. Commit

```
git add internal/state/journal.go internal/state/journal_test.go
git commit -m "state: append-only participants journal (Append/Read/Remove/ParticipantsPath)"
```

---

## Task 3 — Handler routing: `/session allow add|remove|list` + `/session who`

The handler needs the participants directory to read journals. Inject it into
`Handler` via a new field set in `NewHandler` (wired in Task 6).

### 3a. Failing test

Append to `/home/shan/dev/dctl/internal/handler/handler_test.go`. First, the test
helper `newTestHandler` must give the handler a participants dir; update it and add
a nested-subcommand interaction builder + the new tests:

```go
// itGroup builds an interaction for a sub-command GROUP (type 2) → sub (type 1).
func itGroup(user, cmd, group, sub string, opts ...dctl.InteractionOption) dctl.Interaction {
	inner := dctl.InteractionOption{Name: sub, Type: 1, Options: opts}
	data := dctl.InteractionData{
		Name:    cmd,
		Options: []dctl.InteractionOption{{Name: group, Type: 2, Options: []dctl.InteractionOption{inner}}},
	}
	return dctl.Interaction{Member: dctl.Member{User: dctl.Author{ID: user}}, Data: data}
}

func TestSessionAllowAddListRemove(t *testing.T) {
	h, _, _, _, st := newTestHandler(t, dctl.ChannelText)
	st.AddSession(state.Session{Name: "demo", ChannelID: "c1", Type: "text"})

	r := h.Handle(context.Background(), itGroup("owner", "session", "allow", "add",
		dctl.InteractionOption{Name: "name", Value: "demo"},
		dctl.InteractionOption{Name: "user", Value: "u1"}))
	if r.Content == "" || !r.Ephemeral {
		t.Fatalf("expected ephemeral confirmation, got %+v", r)
	}
	if !st.SessionAllowed("demo", "u1") {
		t.Fatal("u1 should now be allowed on demo")
	}

	r = h.Handle(context.Background(), itGroup("owner", "session", "allow", "list",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if !contains(r.Content, "u1") {
		t.Fatalf("list should mention u1: %q", r.Content)
	}

	h.Handle(context.Background(), itGroup("owner", "session", "allow", "remove",
		dctl.InteractionOption{Name: "name", Value: "demo"},
		dctl.InteractionOption{Name: "user", Value: "u1"}))
	if st.SessionAllowed("demo", "u1") {
		t.Fatal("u1 should be removed")
	}
}

func TestSessionAllowMissingSession(t *testing.T) {
	h, _, _, _, _ := newTestHandler(t, dctl.ChannelText)
	r := h.Handle(context.Background(), itGroup("owner", "session", "allow", "add",
		dctl.InteractionOption{Name: "name", Value: "ghost"},
		dctl.InteractionOption{Name: "user", Value: "u1"}))
	if !r.Ephemeral || !contains(r.Content, "ghost") {
		t.Fatalf("expected ephemeral 'no session' error, got %+v", r)
	}
}

func TestSessionWhoListsParticipants(t *testing.T) {
	h, _, _, _, st := newTestHandler(t, dctl.ChannelText)
	st.AddSession(state.Session{Name: "demo", ChannelID: "c1", Type: "text"})
	// simulate the bridge having recorded two humans
	jp := state.ParticipantsPath(h.PartDir(), "demo")
	state.AppendParticipant(jp, "h1")
	state.AppendParticipant(jp, "h2")

	r := h.Handle(context.Background(), itGroup("owner", "session", "who", "",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if !contains(r.Content, "h1") || !contains(r.Content, "h2") {
		t.Fatalf("who should list both participants: %q", r.Content)
	}
}

func TestSessionWhoEmpty(t *testing.T) {
	h, _, _, _, st := newTestHandler(t, dctl.ChannelText)
	st.AddSession(state.Session{Name: "demo", ChannelID: "c1", Type: "text"})
	r := h.Handle(context.Background(), itGroup("owner", "session", "who", "",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if !contains(r.Content, "Personne") {
		t.Fatalf("empty who should say nobody wrote yet: %q", r.Content)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
```

Add `"strings"` to the test file's import block. Update `newTestHandler` to pass a
participants dir and expose it:

```go
func newTestHandler(t *testing.T, homeType int) (*Handler, *fakeDiscord, *fakeSup, *fakeWT, *state.State) {
	t.Helper()
	d := &fakeDiscord{homeType: homeType}
	sup := &fakeSup{}
	wt := &fakeWT{path: "/wt/x"}
	st := state.NewState(t.TempDir() + "/s.json")
	st.AddAllow("owner")
	return NewHandler(d, sup, wt, st, "claude", t.TempDir()), d, sup, wt, st
}
```

> `who` is a plain subcommand (not a group), so `itGroup(..., "who", "")` produces a
> type-2 wrapper whose single child is `who` with an empty inner sub — but `who` is
> a SUB_COMMAND directly under `session`, not under a group. Use the existing `it`
> helper instead for `who`:
>
> Replace the two `who` calls above with:
> ```go
> r := h.Handle(context.Background(), it("owner", "session", "who",
> 	dctl.InteractionOption{Name: "name", Value: "demo"}))
> ```
> (`it` already nests a SUB_COMMAND named "who" with the name option.)

### 3b. Run-fail

```
go test ./internal/handler/
```

Expected: compile errors — `NewHandler` arity (now takes a partDir), `h.PartDir
undefined`, and the new routing cases unhandled (`unknown /session subcommand`
returned → test assertions fail).

### 3c. Minimal impl

In `/home/shan/dev/dctl/internal/handler/handler.go`:

Add `strings` import. Add a `partDir` field and accessor, extend `NewHandler`:

```go
type Handler struct {
	d          discord
	sup        supervisor
	wt         worktrees
	st         *state.State
	defaultCmd string
	partDir    string // dir holding participants/<name>.log journals
}

// NewHandler builds a Handler. defaultCmd is the bridge command used when a
// session is created without an explicit cmd. partDir is the directory under
// which per-session participant journals live (participants/<name>.log).
func NewHandler(d discord, sup supervisor, wt worktrees, st *state.State, defaultCmd, partDir string) *Handler {
	return &Handler{d: d, sup: sup, wt: wt, st: st, defaultCmd: defaultCmd, partDir: partDir}
}

// PartDir returns the participants journal directory (used by tests/wiring).
func (h *Handler) PartDir() string { return h.partDir }
```

Route the new subcommands in `handleSession`:

```go
func (h *Handler) handleSession(ctx context.Context, in dctl.Interaction) dctl.Response {
	sub, _ := in.Data.Subcommand()
	switch sub {
	case "create":
		return h.sessionCreate(ctx, in)
	case "close":
		return h.sessionClose(ctx, in)
	case "list":
		return h.sessionList()
	case "allow":
		return h.sessionAllow(in)
	case "who":
		return h.sessionWho(in)
	default:
		return errf("unknown /session subcommand")
	}
}
```

Add the handlers (after `sessionList`):

```go
// sessionAllow routes /session allow add|remove|list. The option group is the
// SUB_COMMAND_GROUP "allow"; its single child SUB_COMMAND is the action.
func (h *Handler) sessionAllow(in dctl.Interaction) dctl.Response {
	action := allowAction(in.Data.Options)
	name, ok := in.Data.Opt("name")
	if !ok {
		return errf("missing name")
	}
	if _, exists := h.st.FindSession(name); !exists {
		return errf("no session %q", name)
	}
	switch action {
	case "add":
		id, ok := in.Data.Opt("user")
		if !ok {
			return errf("missing user")
		}
		id = normalizeUserID(id)
		added, err := h.st.AddSessionAllow(name, id)
		if err != nil {
			return errf("%v", err)
		}
		if !added {
			return dctl.Response{Content: fmt.Sprintf("<@%s> already allowed on **%s**.", id, name), Ephemeral: true}
		}
		return dctl.Response{Content: fmt.Sprintf("✅ <@%s> allowed on **%s**.", id, name), Ephemeral: true}
	case "remove":
		id, ok := in.Data.Opt("user")
		if !ok {
			return errf("missing user")
		}
		id = normalizeUserID(id)
		removed, err := h.st.RemoveSessionAllow(name, id)
		if err != nil {
			return errf("%v", err)
		}
		if !removed {
			return dctl.Response{Content: fmt.Sprintf("<@%s> was not in **%s**'s allowlist.", id, name), Ephemeral: true}
		}
		return dctl.Response{Content: fmt.Sprintf("✅ <@%s> removed from **%s**.", id, name), Ephemeral: true}
	case "list":
		ids := h.st.SessionAllowlist(name)
		if len(ids) == 0 {
			return dctl.Response{Content: fmt.Sprintf("**%s** has no per-session allowlist (the global allowlist still applies).", name), Ephemeral: true}
		}
		out := fmt.Sprintf("Per-session allowlist for **%s** (plus the global allowlist):\n", name)
		for _, id := range ids {
			out += fmt.Sprintf("• <@%s>\n", id)
		}
		return dctl.Response{Content: out, Ephemeral: true}
	default:
		return errf("unknown /session allow action")
	}
}

// sessionWho lists observed participants (journal) for the session.
func (h *Handler) sessionWho(in dctl.Interaction) dctl.Response {
	name, ok := in.Data.Opt("name")
	if !ok {
		return errf("missing name")
	}
	if _, exists := h.st.FindSession(name); !exists {
		return errf("no session %q", name)
	}
	ids := state.ReadParticipants(state.ParticipantsPath(h.partDir, name))
	if len(ids) == 0 {
		return dctl.Response{Content: "Personne n'a encore écrit dans cette session.", Ephemeral: true}
	}
	out := fmt.Sprintf("Participants observed in **%s**:\n", name)
	for _, id := range ids {
		out += fmt.Sprintf("• <@%s>\n", id)
	}
	return dctl.Response{Content: out, Ephemeral: true}
}

// allowAction returns the SUB_COMMAND name nested in the "allow" group.
func allowAction(opts []dctl.InteractionOption) string {
	for _, o := range opts {
		if o.Name == "allow" && o.Type == 2 { // SUB_COMMAND_GROUP
			for _, c := range o.Options {
				if c.Type == 1 { // SUB_COMMAND
					return c.Name
				}
			}
		}
	}
	return ""
}

// normalizeUserID strips a Discord mention wrapper (<@id> / <@!id>) to the bare id.
func normalizeUserID(s string) string {
	s = strings.TrimPrefix(s, "<@")
	s = strings.TrimPrefix(s, "!")
	s = strings.TrimSuffix(s, ">")
	return s
}
```

> `in.Data.Opt("name")` / `Opt("user")` already recurse through nested option lists
> (see `findOpt` in `interactions.go`), so they reach options under the group→sub
> nesting without extra plumbing.

### 3d. Run-pass

```
go test ./internal/handler/
```

Expected: `ok`.

### 3e. Commit

```
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "handler: /session allow add|remove|list and /session who"
```

---

## Task 4 — Bridge records participants to the journal

### 4a. Failing test

Create `/home/shan/dev/dctl/internal/bridge/bridge_test.go`:

```go
package bridge

import (
	"path/filepath"
	"testing"

	"github.com/vskstudio/dctl/internal/state"
)

func TestRecordParticipantAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "participants", "demo.log")
	recordParticipant(path, "u1")
	recordParticipant(path, "u1") // idempotent
	recordParticipant(path, "u2")
	got := state.ReadParticipants(path)
	if len(got) != 2 || got[0] != "u1" || got[1] != "u2" {
		t.Fatalf("expected [u1 u2], got %+v", got)
	}
}

func TestRecordParticipantEmptyPathNoop(t *testing.T) {
	// must not panic or create anything when no journal configured
	recordParticipant("", "u1")
}
```

### 4b. Run-fail

```
go test ./internal/bridge/
```

Expected: `undefined: recordParticipant`.

### 4c. Minimal impl

In `/home/shan/dev/dctl/internal/bridge/bridge.go`:

Add `"github.com/vskstudio/dctl/internal/state"` to imports. Add the
`Participants` field to `Options`:

```go
type Options struct {
	Channel      string
	Cmd          string
	Stream       bool
	Model        string
	Ensure       string
	Interval     int
	State        string
	After        string
	Participants string // append-only journal of message authors (empty = disabled)
	Verbose      bool
}
```

Add the helper (near `persist`):

```go
// recordParticipant best-effort appends a human author id to the journal so the
// daemon can answer /session who. Errors are swallowed: observability must never
// break the bridge loop.
func recordParticipant(path, userID string) {
	_, _ = state.AppendParticipant(path, userID)
}
```

Call it in `Run`, right after the bot filter:

```go
			if m.Author.Bot {
				continue // never answer a bot (incl. ourselves) → no loops
			}
			recordParticipant(o.Participants, m.Author.ID)
```

### 4d. Run-pass

```
go test ./internal/bridge/
```

Expected: `ok`.

### 4e. Commit

```
git add internal/bridge/bridge.go internal/bridge/bridge_test.go
git commit -m "bridge: record message authors to per-session participants journal"
```

---

## Task 5 — `--participants` flag on `dctl bridge`

### 5a. Failing test

`cmd/dctl` has no unit test harness for flag parsing (see existing
`cmd/dctl/bridge.go` — pure wiring). Use a build-level guard test. Create
`/home/shan/dev/dctl/cmd/dctl/bridge_flags_test.go`:

```go
package main

import "testing"

// TestBridgeParticipantsFlagWired is a compile-time guard: it fails to build until
// bridge.Options gains a Participants field, ensuring runBridge can set it.
func TestBridgeParticipantsFlagWired(t *testing.T) {
	// referenced only to assert the field exists at compile time
	_ = bridgeOptionsHasParticipants
}
```

> If a free symbol is awkward, fold this assertion into an existing test file
> instead; the load-bearing requirement is simply that `--participants` is parsed
> and threaded. The minimal impl below adds the sentinel.

### 5b. Run-fail

```
go build ./cmd/dctl/
```

Expected: `undefined: bridgeOptionsHasParticipants`.

### 5c. Minimal impl

In `/home/shan/dev/dctl/cmd/dctl/bridge.go`, add the flag and thread it:

```go
	state := fs.String("state", "", "file to persist the last-seen message id across restarts")
	participants := fs.String("participants", "", "append-only journal of message authors for /session who")
	after := fs.String("after", "", "seed start id for the first run (state file wins once it exists)")
	verbose := fs.Bool("v", false, "log activity to stderr")
	fs.Parse(args)

	return bridge.Run(ctx, c, bridge.Options{
		Channel:      *ch,
		Cmd:          *cmdStr,
		Stream:       *stream,
		Model:        *model,
		Ensure:       *ensure,
		Interval:     *interval,
		State:        *state,
		Participants: *participants,
		After:        *after,
		Verbose:      *verbose,
	})
```

Add the sentinel at file scope in `cmd/dctl/bridge.go`:

```go
// bridgeOptionsHasParticipants exists so a compile-time test can assert the
// --participants journal is wired into bridge.Options.
var bridgeOptionsHasParticipants = bridge.Options{}.Participants
```

### 5d. Run-pass

```
go test ./cmd/dctl/
go vet ./cmd/dctl/
```

Expected: `ok` / no vet complaints.

### 5e. Commit

```
git add cmd/dctl/bridge.go cmd/dctl/bridge_flags_test.go
git commit -m "cmd/dctl: --participants flag threads the journal path into bridge.Options"
```

---

## Task 6 — Supervisor passes `--participants`; serve wires the dir; close purges journal

### 6a. Failing test

Append to `/home/shan/dev/dctl/internal/state/state_test.go` a guard that the
handler purges the journal on close — but close lives in the handler. Add it to
`/home/shan/dev/dctl/internal/handler/handler_test.go` instead:

```go
func TestSessionClosePurgesParticipants(t *testing.T) {
	h, _, _, _, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	st.AddSession(state.Session{Name: "demo", ChannelID: "ch9", Type: "text", Worktree: "/wt/x"})
	jp := state.ParticipantsPath(h.PartDir(), "demo")
	state.AppendParticipant(jp, "h1")
	h.Handle(context.Background(), it("owner", "session", "close",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if got := state.ReadParticipants(jp); len(got) != 0 {
		t.Fatalf("close must purge the participants journal, got %+v", got)
	}
}
```

Also add a supervisor test
`/home/shan/dev/dctl/internal/supervisor/supervisor_test.go`:

```go
package supervisor

import (
	"context"
	"strings"
	"testing"

	"github.com/vskstudio/dctl/internal/state"
)

func TestBridgeArgsIncludeParticipants(t *testing.T) {
	s := NewSupervisor(context.Background(), "/bin/dctl")
	s.PartDir = "/var/dctl"
	args := s.bridgeArgs(state.Session{Name: "demo", ChannelID: "c1"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--participants") ||
		!strings.Contains(joined, state.ParticipantsPath("/var/dctl", "demo")) {
		t.Fatalf("expected --participants <journal> in args: %v", args)
	}
}
```

### 6b. Run-fail

```
go test ./internal/supervisor/ ./internal/handler/
```

Expected: `s.PartDir undefined`, `s.bridgeArgs undefined`; handler close test fails
(journal still present).

### 6c. Minimal impl

In `/home/shan/dev/dctl/internal/supervisor/supervisor.go` add a `PartDir` field
and factor args into a method:

```go
type Supervisor struct {
	ctx     context.Context
	selfBin string
	PartDir string // participants journal dir; empty disables --participants
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}
```

```go
// bridgeArgs builds the child `dctl bridge` argv for sess.
func (s *Supervisor) bridgeArgs(sess state.Session) []string {
	args := []string{"bridge", "-c", sess.ChannelID, "--cmd", sess.Cmd}
	if s.PartDir != "" {
		args = append(args, "--participants", state.ParticipantsPath(s.PartDir, sess.Name))
	}
	return args
}
```

And in `runLoop`, replace the inline arg list:

```go
		cmd := exec.CommandContext(ctx, s.selfBin, s.bridgeArgs(sess)...)
```

In `/home/shan/dev/dctl/internal/handler/handler.go`, purge the journal in
`sessionClose` after `RemoveSession` succeeds:

```go
	if err := h.st.RemoveSession(name); err != nil {
		return errf("persist: %v", err)
	}
	_ = state.RemoveParticipantJournal(state.ParticipantsPath(h.partDir, name))
	return dctl.Response{Content: fmt.Sprintf("🗄️ Session **%s** closed.", name), Ephemeral: true}
```

In `/home/shan/dev/dctl/internal/serve/serve.go`, derive the dir from the state
path and wire both supervisor and handler:

```go
	self, _ := os.Executable()
	partDir := filepath.Dir(o.StatePath) // participants/<name>.log lives beside state.json
	sup := supervisor.NewSupervisor(ctx, self)
	sup.PartDir = partDir
	// Restart persisted sessions.
	for _, sess := range st.SnapshotSessions() {
		_ = sup.Start(sess)
	}
```

and:

```go
	hdl := handler.NewHandler(c, sup, wt, st, o.DefaultCmd, partDir)
```

(`path/filepath` is already imported in serve.go.)

### 6d. Run-pass

```
go test ./internal/supervisor/ ./internal/handler/ ./internal/serve/
```

Expected: `ok` for all three.

### 6e. Commit

```
git add internal/supervisor/supervisor.go internal/supervisor/supervisor_test.go \
        internal/handler/handler.go internal/handler/handler_test.go \
        internal/serve/serve.go
git commit -m "wire participants journal: supervisor --participants, serve dir, close purge"
```

---

## Task 7 — Declare the new slash subcommands to Discord

### 7a. Failing test

`interactions.go` has no unit test. Add a declaration guard
`/home/shan/dev/dctl/interactions_commands_test.go`:

```go
package dctl

import "testing"

// findSub walks the declarative command set for a (top, group, sub) triple.
func hasSessionSub(top, name string) bool {
	for _, c := range dctlCommands() {
		if c["name"] != top {
			continue
		}
		for _, o := range c["options"].([]map[string]any) {
			if o["name"] == name {
				return true
			}
		}
	}
	return false
}

func TestSessionAllowGroupDeclared(t *testing.T) {
	if !hasSessionSub("session", "allow") {
		t.Fatal("session command must declare an 'allow' sub-command group")
	}
	if !hasSessionSub("session", "who") {
		t.Fatal("session command must declare a 'who' subcommand")
	}
}
```

### 7b. Run-fail

```
go test -run TestSessionAllowGroupDeclared .
```

Expected: both assertions fail (`allow`/`who` not declared under `session`).

### 7c. Minimal impl

In `/home/shan/dev/dctl/interactions.go`, add `typeGroup = 2` to the const block and
extend the `session` command's options (after the `list` subcommand) with the group
and `who`:

```go
		{"name": "session", "description": "Manage Claude sessions", "options": []map[string]any{
			{"name": "create", "description": "Create a session", "type": typeSub, "options": []map[string]any{
				{"name": "name", "description": "Session name", "type": typeStr, "required": true},
				{"name": "cmd", "description": "Override bridged command", "type": typeStr},
				{"name": "shared", "description": "Run in the main checkout (no worktree)", "type": typeBool},
			}},
			{"name": "close", "description": "Close a session", "type": typeSub, "options": []map[string]any{
				{"name": "name", "description": "Session name", "type": typeStr, "required": true},
				{"name": "force", "description": "Discard uncommitted worktree changes", "type": typeBool},
			}},
			{"name": "list", "description": "List active sessions", "type": typeSub},
			{"name": "allow", "description": "Per-session allowlist", "type": typeGroup, "options": []map[string]any{
				{"name": "add", "description": "Allow a user on this session", "type": typeSub, "options": []map[string]any{
					{"name": "name", "description": "Session name", "type": typeStr, "required": true},
					{"name": "user", "description": "User", "type": typeUser, "required": true},
				}},
				{"name": "remove", "description": "Remove a user from this session's allowlist", "type": typeSub, "options": []map[string]any{
					{"name": "name", "description": "Session name", "type": typeStr, "required": true},
					{"name": "user", "description": "User", "type": typeUser, "required": true},
				}},
				{"name": "list", "description": "Show this session's allowlist", "type": typeSub, "options": []map[string]any{
					{"name": "name", "description": "Session name", "type": typeStr, "required": true},
				}},
			}},
			{"name": "who", "description": "Show who has written in this session", "type": typeSub, "options": []map[string]any{
				{"name": "name", "description": "Session name", "type": typeStr, "required": true},
			}},
		}},
```

Add `typeGroup = 2` to the const block:

```go
	const (
		typeSub   = 1
		typeGroup = 2
		typeStr   = 3
		typeBool  = 5
		typeUser  = 6
		typeChan  = 7
	)
```

> Discord `USER` (type 6) options deliver a snowflake id as the option value, so
> `normalizeUserID` is defensive (handles a string-typed mention) but normally a
> bare id arrives — matching the existing global `/allow` which already treats
> `user` as an id string.

### 7d. Run-pass

```
go test -run TestSessionAllowGroupDeclared .
```

Expected: `ok`.

### 7e. Commit

```
git add interactions.go interactions_commands_test.go
git commit -m "interactions: declare /session allow add|remove|list and /session who"
```

---

## Task 8 — Full suite + vet

### 8a. Run

```
go build ./...
go vet ./...
go test ./...
```

Expected: clean build, no vet diagnostics, all packages `ok`. In particular the
pre-existing `state_test.go` round-trip and handler tests still pass (the new
`Session` fields are `omitempty`, so old JSON with no `allow`/`participants` loads to
nil slices — spec §7 migration requirement).

### 8b. Commit (only if any fixups were needed)

```
git commit -am "fix: full-suite cleanups"
```

---

## Self-review against the spec

| Spec requirement | Covered by | Notes |
|---|---|---|
| §2 `Session.Allow` / `Participants` (`omitempty`, retro-compat) | Task 1 (fields) + Task 8 (nil-load check) | `Participants` kept as a P1-future cache; not the read path. |
| §2 `AddSessionAllow`/`RemoveSessionAllow`/`SessionAllowed`/`SessionAllowlist` (mutex, persisted, FindSession-style) | Task 1 | Bool returns match spec's idempotency contract. |
| §2 `RecordParticipant`/`SessionParticipants` | Tasks 2–4 (re-homed to journal P2) | **Deviation:** per spec §5/§10 recommendation (P2), these live on the journal (`AppendParticipant`/`ReadParticipants`), not on `*State`, to avoid concurrent `state.json` writes before Spec 3. Documented in Architecture. |
| §3 global OR per-session union | Task 1 `SessionAllowed` | Implemented as union. |
| §3 slash-commands stay global-gated; per-session ≠ slash-commands | Task 3 (handler still gated by `Handle`'s `Allowed`) | Bridge-side enforcement (semantics B) is **out of scope** (spec §8 step 4). |
| §4 `/session allow add|remove|list` + `/session who` | Tasks 3 + 7 | `who` ephemeral, `<@id>` format, empty → French message verbatim from spec §4. |
| §4 validate name / `no session %q` via FindSession | Task 3 | Reuses FindSession; errors before mutating. |
| §5 record after bot filter, idempotent | Task 4 | `recordParticipant` post-`m.Author.Bot`. |
| §5 P2 journal `participants/<name>.log` one id/line, daemon reads on demand | Tasks 2,3,6 | `ParticipantsPath` shared helper. |
| §6 files touched | All match (state, handler, interactions decl, bridge, supervisor, cmd/dctl) | Plus serve.go for wiring (implied by §6 supervisor flags). |
| §7 session absent → no panic | Tasks 1,3 | Returns error, not panic. |
| §7 double add / remove-absent idempotent neutral message | Task 3 | "already allowed" / "was not in …". |
| §7 bot/self never recorded | Task 4 | Existing `m.Author.Bot` filter. |
| §7 close purges journal | Task 6 | `RemoveParticipantJournal` after `RemoveSession`. |
| §7 migration: nil slices load | Task 8 | `omitempty`. |
| §7 mention vs id normalization | Task 3 `normalizeUserID` | Strips `<@ !>`. |
| §9 success criteria (add→list→remove; who lists 2 humans not bots; persistence; no regression; no state race) | Tasks 1,3,4,6,8 | P2 means no `state.json` race (append-only journal). |
| §10 Spec 3 dependency avoided | P2 throughout | No daemon/bridge concurrent state write; struct `Participants` reserved for post-Spec-3 P1. |
