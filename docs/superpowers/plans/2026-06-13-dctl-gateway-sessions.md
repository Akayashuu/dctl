# DCTL Gateway daemon + Sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a persistently-online `dctl serve` daemon that connects to the Discord Gateway, exposes native slash commands (`/set home`, `/session`, `/allow`), and supervises one bridged Claude process per session (text channel or forum post).

**Architecture:** A new `gateway.go` provides a minimal websocket client (Identify / heartbeat / Resume / event dispatch). `interactions.go` adds slash-command registration and interaction responses. `state.go` is a mutex-guarded JSON store (home, allowlist, sessions). A pure `Handler` (with injected `discord`/`supervisor` interfaces) routes interactions so routing is unit-tested without network. `cmd/dctl/serve.go` wires it all together; `cmd/dctl/supervisor.go` spawns/restarts child `dctl bridge` processes.

**Tech Stack:** Go (stdlib), one new dependency `github.com/coder/websocket` for the Gateway (Discord requires a websocket; hand-rolling RFC6455 is out of scope). Discord REST API v10 (existing `dctl.Client`).

**Dependency note:** the project has been dependency-free. The Gateway connection makes one small, well-maintained websocket dep unavoidable. Confirmed acceptable as part of the approved Gateway design.

---

## File Structure

- Create `state.go` (package `dctl`) — `State` store: home, allowlist, sessions; `Load`/`Save`; mutation+query methods. Pure, fully unit-tested.
- Create `interactions.go` (package `dctl`) — interaction types, `RegisterCommands`, `RespondInteraction`, `ChannelType` lookup.
- Create `gateway.go` (package `dctl`) — `Gateway` websocket client: connect, identify, heartbeat, resume, dispatch decoded `Interaction`s on a channel.
- Create `handler.go` (package `dctl`) — `Handler` + `discord`/`supervisor` interfaces; `Handle(ctx, Interaction) Response`. Pure routing, unit-tested with fakes.
- Create `cmd/dctl/serve.go` — `runServe`: load state, connect gateway, register commands, loop dispatching interactions to `Handler`, restart persisted sessions on boot.
- Create `cmd/dctl/supervisor.go` — `Supervisor`: spawn/track/restart child `dctl bridge` processes per session.
- Create tests: `state_test.go`, `handler_test.go`, `interactions_test.go`.
- Modify `cmd/dctl/main.go` — add `serve` to the command switch + usage.
- Modify `go.mod` — add the websocket dependency.

State file path: `$DCTL_STATE_DIR/state.json`, default `~/.config/dctl/state.json`.

---

## Phase 1 — State store

### Task 1: State types + Load/Save

**Files:**
- Create: `state.go`
- Test: `state_test.go`

- [ ] **Step 1: Write the failing test**

```go
package dctl

import (
	"path/filepath"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := NewState(path)
	s.Home = HomeRef{ID: "123", Type: "category"}
	s.Allow = []string{"343535234303787009"}
	s.Sessions = []Session{{Name: "foo", ChannelID: "c1", Type: "text", Cmd: "claude"}}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Home.ID != "123" || len(got.Allow) != 1 || len(got.Sessions) != 1 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestLoadStateMissingFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.json")
	s, err := LoadState(path)
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(s.Allow) != 0 || len(s.Sessions) != 0 {
		t.Fatal("expected empty state")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestState -v`
Expected: FAIL — `undefined: NewState` etc.

- [ ] **Step 3: Write minimal implementation**

```go
package dctl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// HomeRef points at the category or forum that holds session channels.
type HomeRef struct {
	ID   string `json:"id"`
	Type string `json:"type"` // "category" | "forum"
}

// Session is one bridged channel/post supervised by the daemon.
type Session struct {
	Name      string `json:"name"`
	ChannelID string `json:"channelID"`
	Type      string `json:"type"`               // "text" | "forum"
	Cmd       string `json:"cmd"`
	Worktree  string `json:"worktree,omitempty"` // abs path; empty for a shared session
}

// State is the daemon's persisted configuration. All access is mutex-guarded.
type State struct {
	mu              sync.Mutex `json:"-"`
	path            string     `json:"-"`
	Home            HomeRef    `json:"home"`
	Allow           []string   `json:"allow"`
	Repo            string     `json:"repo,omitempty"` // project sessions operate on; defaults to daemon cwd
	Sessions        []Session  `json:"sessions"`
	StatusMessageID string     `json:"statusMessageID,omitempty"` // cached id of the status embed
}

// NewState returns an empty state bound to path (not yet written).
func NewState(path string) *State { return &State{path: path} }

// LoadState reads state from path; a missing file yields an empty state.
func LoadState(path string) (*State, error) {
	s := NewState(path)
	buf, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(buf, s); err != nil {
		return nil, err
	}
	return s, nil
}

// Save atomically writes state to its path.
func (s *State) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *State) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
```

Note: `sync.Mutex` as a struct field with a `json:"-"` tag does not marshal. Keep the tag.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestState -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add state.go state_test.go
git commit -m "feat(state): persisted daemon state store (home/allow/sessions)"
```

### Task 2: Allowlist + session mutation methods

**Files:**
- Modify: `state.go`
- Test: `state_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestAllowlist(t *testing.T) {
	s := NewState(filepath.Join(t.TempDir(), "s.json"))
	if s.Allowed("u1") {
		t.Fatal("empty allowlist should deny")
	}
	if err := s.AddAllow("u1"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAllow("u1"); err != nil { // idempotent
		t.Fatal(err)
	}
	if !s.Allowed("u1") || len(s.Allow) != 1 {
		t.Fatalf("expected u1 allowed once: %+v", s.Allow)
	}
	if err := s.RemoveAllow("u1"); err != nil {
		t.Fatal(err)
	}
	if s.Allowed("u1") {
		t.Fatal("u1 should be removed")
	}
}

func TestSessionMutations(t *testing.T) {
	s := NewState(filepath.Join(t.TempDir(), "s.json"))
	if err := s.AddSession(Session{Name: "a", ChannelID: "c1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.FindSession("a"); !ok {
		t.Fatal("expected to find a")
	}
	if err := s.AddSession(Session{Name: "a"}); err == nil {
		t.Fatal("duplicate session name should error")
	}
	if err := s.RemoveSession("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.FindSession("a"); ok {
		t.Fatal("a should be gone")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TestAllowlist|TestSessionMutations' -v`
Expected: FAIL — `s.Allowed undefined` etc.

- [ ] **Step 3: Write minimal implementation** (append to `state.go`)

```go
// Allowed reports whether userID may invoke commands.
func (s *State) Allowed(userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.Allow {
		if id == userID {
			return true
		}
	}
	return false
}

// AddAllow adds userID to the allowlist (idempotent) and persists.
func (s *State) AddAllow(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.Allow {
		if id == userID {
			return nil
		}
	}
	s.Allow = append(s.Allow, userID)
	return s.saveLocked()
}

// RemoveAllow removes userID from the allowlist and persists.
func (s *State) RemoveAllow(userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.Allow[:0]
	for _, id := range s.Allow {
		if id != userID {
			out = append(out, id)
		}
	}
	s.Allow = out
	return s.saveLocked()
}

// FindSession returns the session with name (and whether it exists).
func (s *State) FindSession(name string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range s.Sessions {
		if ss.Name == name {
			return ss, true
		}
	}
	return Session{}, false
}

// AddSession adds a session, erroring if the name is taken, and persists.
func (s *State) AddSession(sess Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range s.Sessions {
		if ss.Name == sess.Name {
			return fmt.Errorf("session %q already exists", sess.Name)
		}
	}
	s.Sessions = append(s.Sessions, sess)
	return s.saveLocked()
}

// RemoveSession drops the session named name and persists.
func (s *State) RemoveSession(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.Sessions[:0]
	for _, ss := range s.Sessions {
		if ss.Name != name {
			out = append(out, ss)
		}
	}
	s.Sessions = out
	return s.saveLocked()
}

// SetHome records the home ref and persists.
func (s *State) SetHome(h HomeRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Home = h
	return s.saveLocked()
}

// SnapshotSessions returns a copy of the current sessions.
func (s *State) SnapshotSessions() []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Session(nil), s.Sessions...)
}
```

Add `"fmt"` to the import block.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'TestAllowlist|TestSessionMutations' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add state.go state_test.go
git commit -m "feat(state): allowlist + session mutation helpers"
```

---

## Phase 1 — Interaction routing (the Handler)

### Task 3: Interaction types + Response

**Files:**
- Create: `interactions.go`
- Test: (covered by Task 4)

- [ ] **Step 1: Write the implementation** (no behavior yet; types only — committed alongside Task 4's test)

```go
package dctl

// Interaction is the subset of a Discord INTERACTION_CREATE we handle
// (application slash commands, type 2).
type Interaction struct {
	ID      string          `json:"id"`
	Token   string          `json:"token"`
	GuildID string          `json:"guild_id"`
	Member  Member          `json:"member"`
	Data    InteractionData `json:"data"`
}

// Member carries the invoking user (interactions in a guild come via member.user).
type Member struct {
	User Author `json:"user"`
}

// InteractionData is the invoked command + its options.
type InteractionData struct {
	Name    string             `json:"name"`
	Options []InteractionOption `json:"options"`
}

// InteractionOption is one command option; for subcommands, Options nests.
type InteractionOption struct {
	Name    string              `json:"name"`
	Type    int                 `json:"type"`
	Value   any                 `json:"value"`
	Options []InteractionOption `json:"options"`
}

// Response is what the Handler decides to reply with.
type Response struct {
	Content   string
	Ephemeral bool
}

// Opt returns the string value of a (possibly nested) option by name.
func (d InteractionData) Opt(name string) (string, bool) {
	return findOpt(d.Options, name)
}

func findOpt(opts []InteractionOption, name string) (string, bool) {
	for _, o := range opts {
		if o.Name == name {
			if s, ok := o.Value.(string); ok {
				return s, true
			}
		}
		if v, ok := findOpt(o.Options, name); ok {
			return v, true
		}
	}
	return "", false
}

// optBool returns the bool value of a (possibly nested) option, false if absent.
func optBool(d InteractionData, name string) bool {
	if b, ok := findBool(d.Options, name); ok {
		return b
	}
	return false
}

func findBool(opts []InteractionOption, name string) (bool, bool) {
	for _, o := range opts {
		if o.Name == name {
			if b, ok := o.Value.(bool); ok {
				return b, true
			}
		}
		if b, ok := findBool(o.Options, name); ok {
			return b, true
		}
	}
	return false, false
}

// Subcommand returns the name of the first sub-command option, if any.
func (d InteractionData) Subcommand() (string, []InteractionOption) {
	for _, o := range d.Options {
		if o.Type == 1 { // SUB_COMMAND
			return o.Name, o.Options
		}
	}
	return "", nil
}
```

- [ ] **Step 2: Commit** (after Task 4 passes; see Task 4 Step 5).

### Task 4: Handler routing with fakes

**Files:**
- Create: `handler.go`
- Test: `handler_test.go`

- [ ] **Step 1: Write the failing test**

```go
package dctl

import (
	"context"
	"testing"
)

type fakeDiscord struct {
	created   []string
	archived  []string
	homeType  int
}

func (f *fakeDiscord) ChannelType(ctx context.Context, id string) (int, error) { return f.homeType, nil }
func (f *fakeDiscord) CreateChannelUnder(ctx context.Context, parentID, name string) (*Channel, error) {
	f.created = append(f.created, name)
	return &Channel{ID: "new-" + name, Name: name, Type: ChannelText}, nil
}
func (f *fakeDiscord) ForumPost(ctx context.Context, forumID, name, content string) (*Channel, error) {
	f.created = append(f.created, "forum:"+name)
	return &Channel{ID: "post-" + name, Name: name}, nil
}
func (f *fakeDiscord) ArchiveChannel(ctx context.Context, id string) error {
	f.archived = append(f.archived, id)
	return nil
}

type fakeSup struct{ started, stopped []string }

func (f *fakeSup) Start(s Session) error { f.started = append(f.started, s.Name); return nil }
func (f *fakeSup) Stop(name string) error { f.stopped = append(f.stopped, name); return nil }

type fakeWT struct {
	created, removed []string
	path             string // "" → simulate shared fallback
	removeErr        error  // simulate dirty worktree
}

func (f *fakeWT) Create(name string) (string, error) {
	f.created = append(f.created, name)
	return f.path, nil
}
func (f *fakeWT) Remove(name string, force bool) error {
	if f.removeErr != nil && !force {
		return f.removeErr
	}
	f.removed = append(f.removed, name)
	return nil
}

func newTestHandler(t *testing.T, homeType int) (*Handler, *fakeDiscord, *fakeSup, *fakeWT, *State) {
	t.Helper()
	d := &fakeDiscord{homeType: homeType}
	sup := &fakeSup{}
	wt := &fakeWT{path: "/wt/x"}
	st := NewState(t.TempDir() + "/s.json")
	st.AddAllow("owner")
	return NewHandler(d, sup, wt, st, "claude"), d, sup, wt, st
}

func it(user, cmd string, sub string, opts ...InteractionOption) Interaction {
	data := InteractionData{Name: cmd}
	if sub != "" {
		data.Options = []InteractionOption{{Name: sub, Type: 1, Options: opts}}
	} else {
		data.Options = opts
	}
	return Interaction{Member: Member{User: Author{ID: user}}, Data: data}
}

func TestHandlerDeniesNonAllowlisted(t *testing.T) {
	h, _, _, _ := newTestHandler(t, ChannelText)
	r := h.Handle(context.Background(), it("intruder", "session", "list"))
	if !r.Ephemeral || r.Content == "" {
		t.Fatalf("expected ephemeral denial, got %+v", r)
	}
}

func TestSetHomeDetectsCategory(t *testing.T) {
	h, _, _, st := newTestHandler(t, 4) // 4 = GUILD_CATEGORY
	r := h.Handle(context.Background(), it("owner", "set", "home",
		InteractionOption{Name: "channel", Value: "cat1"}))
	if r.Ephemeral && st.Home.Type != "category" {
		t.Fatalf("home not set category: %+v / %+v", r, st.Home)
	}
	if st.Home.ID != "cat1" || st.Home.Type != "category" {
		t.Fatalf("home wrong: %+v", st.Home)
	}
}

func TestSessionCreateText(t *testing.T) {
	h, d, sup, st := newTestHandler(t, ChannelText)
	st.SetHome(HomeRef{ID: "cat1", Type: "category"})
	r := h.Handle(context.Background(), it("owner", "session", "create",
		InteractionOption{Name: "name", Value: "demo"}))
	if len(d.created) != 1 || d.created[0] != "demo" {
		t.Fatalf("expected channel created: %+v", d.created)
	}
	if len(sup.started) != 1 {
		t.Fatalf("expected bridge started: %+v", sup.started)
	}
	if _, ok := st.FindSession("demo"); !ok {
		t.Fatal("session not persisted")
	}
	_ = r
}

func TestSessionCreateRequiresHome(t *testing.T) {
	h, _, _, _ := newTestHandler(t, ChannelText)
	r := h.Handle(context.Background(), it("owner", "session", "create",
		InteractionOption{Name: "name", Value: "demo"}))
	if !r.Ephemeral {
		t.Fatal("expected ephemeral error when home unset")
	}
}

func TestSessionCloseStopsAndArchives(t *testing.T) {
	h, d, sup, st := newTestHandler(t, ChannelText)
	st.SetHome(HomeRef{ID: "cat1", Type: "category"})
	st.AddSession(Session{Name: "demo", ChannelID: "ch9", Type: "text"})
	h.Handle(context.Background(), it("owner", "session", "close",
		InteractionOption{Name: "name", Value: "demo"}))
	if len(sup.stopped) != 1 || len(d.archived) != 1 {
		t.Fatalf("expected stop+archive: %+v %+v", sup.stopped, d.archived)
	}
	if _, ok := st.FindSession("demo"); ok {
		t.Fatal("session should be removed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TestHandler|TestSetHome|TestSession' -v`
Expected: FAIL — `NewHandler undefined`.

- [ ] **Step 3: Write minimal implementation**

```go
package dctl

import (
	"context"
	"fmt"
)

// discord is the subset of Client the Handler needs (injected so routing is testable).
type discord interface {
	ChannelType(ctx context.Context, id string) (int, error)
	CreateChannelUnder(ctx context.Context, parentID, name string) (*Channel, error)
	ForumPost(ctx context.Context, forumID, name, content string) (*Channel, error)
	ArchiveChannel(ctx context.Context, id string) error
}

// supervisor starts/stops the bridge process backing a session.
type supervisor interface {
	Start(s Session) error
	Stop(name string) error
}

// worktrees owns per-session git worktree lifecycle. Create returns the worktree
// path ("" + nil error means "fall back to shared", e.g. not a git repo).
type worktrees interface {
	Create(name string) (path string, err error)
	Remove(name string, force bool) error
}

// Handler routes slash-command interactions to actions.
type Handler struct {
	d          discord
	sup        supervisor
	wt         worktrees
	st         *State
	defaultCmd string
}

// NewHandler builds a Handler. defaultCmd is the bridge command used when a
// session is created without an explicit cmd (e.g. "claude -p --continue").
func NewHandler(d discord, sup supervisor, wt worktrees, st *State, defaultCmd string) *Handler {
	return &Handler{d: d, sup: sup, wt: wt, st: st, defaultCmd: defaultCmd}
}

func deny() Response  { return Response{Content: "⛔ Not authorized.", Ephemeral: true} }
func errf(f string, a ...any) Response {
	return Response{Content: "⚠️ " + fmt.Sprintf(f, a...), Ephemeral: true}
}

// Handle processes one interaction and returns the reply.
func (h *Handler) Handle(ctx context.Context, in Interaction) Response {
	if !h.st.Allowed(in.Member.User.ID) {
		return deny()
	}
	switch in.Data.Name {
	case "set":
		return h.handleSet(ctx, in)
	case "session":
		return h.handleSession(ctx, in)
	case "allow":
		return h.handleAllow(ctx, in)
	default:
		return errf("unknown command %q", in.Data.Name)
	}
}

func (h *Handler) handleSet(ctx context.Context, in Interaction) Response {
	sub, _ := in.Data.Subcommand()
	if sub != "home" {
		return errf("unknown /set subcommand")
	}
	id, ok := in.Data.Opt("channel")
	if !ok {
		return errf("missing channel")
	}
	ct, err := h.d.ChannelType(ctx, id)
	if err != nil {
		return errf("cannot read channel: %v", err)
	}
	var typ string
	switch ct {
	case 4: // GUILD_CATEGORY
		typ = "category"
	case ChannelForum:
		typ = "forum"
	default:
		return errf("home must be a category or a forum (got type %d)", ct)
	}
	if err := h.st.SetHome(HomeRef{ID: id, Type: typ}); err != nil {
		return errf("save failed: %v", err)
	}
	return Response{Content: fmt.Sprintf("🏠 Home set to %s `%s`.", typ, id), Ephemeral: true}
}

func (h *Handler) handleSession(ctx context.Context, in Interaction) Response {
	sub, _ := in.Data.Subcommand()
	switch sub {
	case "create":
		return h.sessionCreate(ctx, in)
	case "close":
		return h.sessionClose(ctx, in)
	case "list":
		return h.sessionList()
	default:
		return errf("unknown /session subcommand")
	}
}

func (h *Handler) sessionCreate(ctx context.Context, in Interaction) Response {
	name, ok := in.Data.Opt("name")
	if !ok {
		return errf("missing name")
	}
	if _, exists := h.st.FindSession(name); exists {
		return errf("session %q already exists", name)
	}
	home := h.st.Home
	if home.ID == "" {
		return errf("no home set — run /set home first")
	}
	cmd := h.defaultCmd
	if c, ok := in.Data.Opt("cmd"); ok && c != "" {
		cmd = c
	}
	// Worktree isolation by default; shared:true runs in the main checkout.
	shared := optBool(in.Data, "shared")
	var worktree, note string
	if !shared {
		path, err := h.wt.Create(name)
		if err != nil {
			return errf("worktree: %v", err)
		}
		if path == "" {
			note = " (shared — not a git repo)"
		} else {
			worktree = path
		}
	}
	var sess Session
	switch home.Type {
	case "category":
		ch, err := h.d.CreateChannelUnder(ctx, home.ID, name)
		if err != nil {
			_ = h.wt.Remove(name, true) // roll back the worktree we just made
			return errf("create channel: %v", err)
		}
		sess = Session{Name: name, ChannelID: ch.ID, Type: "text", Cmd: cmd, Worktree: worktree}
	case "forum":
		ch, err := h.d.ForumPost(ctx, home.ID, name, "Session **"+name+"** started.")
		if err != nil {
			_ = h.wt.Remove(name, true)
			return errf("create forum post: %v", err)
		}
		sess = Session{Name: name, ChannelID: ch.ID, Type: "forum", Cmd: cmd, Worktree: worktree}
	default:
		return errf("home type %q unsupported", home.Type)
	}
	if err := h.st.AddSession(sess); err != nil {
		return errf("persist: %v", err)
	}
	if err := h.sup.Start(sess); err != nil {
		return errf("start bridge: %v", err)
	}
	return Response{Content: fmt.Sprintf("✅ Session **%s** running on <#%s>%s.", name, sess.ChannelID, note), Ephemeral: true}
}

func (h *Handler) sessionClose(ctx context.Context, in Interaction) Response {
	name, ok := in.Data.Opt("name")
	if !ok {
		return errf("missing name")
	}
	sess, exists := h.st.FindSession(name)
	if !exists {
		return errf("no session %q", name)
	}
	_ = h.sup.Stop(name)
	if sess.Worktree != "" {
		force := optBool(in.Data, "force")
		if err := h.wt.Remove(name, force); err != nil {
			return errf("%v — commit, or close with force:true to discard (branch session/%s is kept)", err, name)
		}
	}
	if err := h.d.ArchiveChannel(ctx, sess.ChannelID); err != nil {
		return errf("archive: %v", err)
	}
	if err := h.st.RemoveSession(name); err != nil {
		return errf("persist: %v", err)
	}
	return Response{Content: fmt.Sprintf("🗄️ Session **%s** closed.", name), Ephemeral: true}
}

func (h *Handler) sessionList() Response {
	sessions := h.st.SnapshotSessions()
	if len(sessions) == 0 {
		return Response{Content: "No active sessions.", Ephemeral: true}
	}
	out := "Active sessions:\n"
	for _, s := range sessions {
		out += fmt.Sprintf("• **%s** (%s) <#%s>\n", s.Name, s.Type, s.ChannelID)
	}
	return Response{Content: out, Ephemeral: true}
}

func (h *Handler) handleAllow(ctx context.Context, in Interaction) Response {
	sub, _ := in.Data.Subcommand()
	switch sub {
	case "add":
		id, ok := in.Data.Opt("user")
		if !ok {
			return errf("missing user")
		}
		if err := h.st.AddAllow(id); err != nil {
			return errf("save: %v", err)
		}
		return Response{Content: "✅ Added to allowlist.", Ephemeral: true}
	case "remove":
		id, ok := in.Data.Opt("user")
		if !ok {
			return errf("missing user")
		}
		if err := h.st.RemoveAllow(id); err != nil {
			return errf("save: %v", err)
		}
		return Response{Content: "✅ Removed from allowlist.", Ephemeral: true}
	case "list":
		return Response{Content: fmt.Sprintf("Allowlist: %v", h.st.Allow), Ephemeral: true}
	default:
		return errf("unknown /allow subcommand")
	}
}
```

Note: the `user` option type in Discord is a USER (type 6) whose `Value` is the user-id string — so `Opt("user")` returns the id. Good.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'TestHandler|TestSetHome|TestSession' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add interactions.go handler.go handler_test.go
git commit -m "feat(handler): interaction routing for set/session/allow with allowlist gating"
```

---

## Phase 1 — Discord REST additions

### Task 5: ChannelType, CreateChannelUnder, ArchiveChannel on Client

**Files:**
- Modify: `channels.go`
- Test: `channels_test.go` (use the existing httptest pattern there)

- [ ] **Step 1: Write the failing test** — mirror the existing `channels_test.go` style (a stub `http.Client` returning canned JSON). Add:

```go
func TestChannelTypeParsesType(t *testing.T) {
	c := newStubClient(t, `{"id":"x","type":4,"name":"cat"}`) // newStubClient: see existing test helpers
	ct, err := c.ChannelType(context.Background(), "x")
	if err != nil || ct != 4 {
		t.Fatalf("got %d,%v", ct, err)
	}
}
```

If `channels_test.go` lacks a reusable stub helper, add one modeled on `dctl_test.go`'s existing transport stub. (Read `dctl_test.go` first to match the established pattern.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestChannelType -v`
Expected: FAIL — `c.ChannelType undefined`.

- [ ] **Step 3: Write minimal implementation** (append to `channels.go`)

```go
// ChannelType returns the Discord channel-type integer for channelID.
func (c *Client) ChannelType(ctx context.Context, channelID string) (int, error) {
	if !c.Enabled() {
		return 0, ErrDisabled
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/channels/"+channelID, nil)
	if err != nil {
		return 0, err
	}
	var ch Channel
	if err := c.do(req, &ch); err != nil {
		return 0, err
	}
	return ch.Type, nil
}

// CreateChannelUnder creates a text channel named name nested under category
// parentID, in the sole guild.
func (c *Client) CreateChannelUnder(ctx context.Context, parentID, name string) (*Channel, error) {
	gid, err := c.resolveGuild(ctx, "")
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/guilds/"+gid+"/channels",
		map[string]any{"name": name, "type": ChannelText, "parent_id": parentID})
	if err != nil {
		return nil, err
	}
	var ch Channel
	if err := c.do(req, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// ArchiveChannel archives a thread/forum-post, or deletes a plain text channel.
// Threads support PATCH {archived:true}; text channels do not, so they are deleted.
func (c *Client) ArchiveChannel(ctx context.Context, channelID string) error {
	ct, err := c.ChannelType(ctx, channelID)
	if err != nil {
		return err
	}
	// Thread types: 10,11,12 (announcement/public/private). Forum posts are 11.
	if ct == 10 || ct == 11 || ct == 12 {
		req, err := c.newRequest(ctx, http.MethodPatch, "/channels/"+channelID,
			map[string]any{"archived": true})
		if err != nil {
			return err
		}
		return c.do(req, nil)
	}
	return c.DeleteChannel(ctx, channelID)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'TestChannelType|TestChannel' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add channels.go channels_test.go
git commit -m "feat(client): ChannelType, CreateChannelUnder, ArchiveChannel"
```

---

## Phase 1 — Liveness (Health snapshot + /health)

### Task 5b: Health snapshot

**Files:**
- Create: `health.go`
- Test: `health_test.go`

The `Health` snapshot is driven **only by bot/daemon facts** (gateway heartbeat ACK + a REST self-ping) — a hung or crashed Claude session must never flip `Online`.

- [ ] **Step 1: Write the failing test**

```go
package dctl

import (
	"testing"
	"time"
)

func TestHealthSnapshotOnline(t *testing.T) {
	h := NewHealth(time.Unix(1000, 0))
	h.HeartbeatAck(time.Unix(1005, 0))
	h.SetSessions(2)
	snap := h.Snapshot(time.Unix(1010, 0), 30*time.Second)
	if !snap.Online {
		t.Fatal("expected online (heartbeat within window)")
	}
	if snap.Sessions != 2 || snap.UptimeS != 10 {
		t.Fatalf("snap wrong: %+v", snap)
	}
}

func TestHealthGoesOfflineWhenHeartbeatStale(t *testing.T) {
	h := NewHealth(time.Unix(1000, 0))
	h.HeartbeatAck(time.Unix(1005, 0))
	snap := h.Snapshot(time.Unix(2000, 0), 30*time.Second) // 995s since last ack
	if snap.Online {
		t.Fatal("expected offline (heartbeat stale)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestHealth -v`
Expected: FAIL — `NewHealth undefined`.

- [ ] **Step 3: Write minimal implementation**

```go
package dctl

import (
	"sync"
	"time"
)

// Health is the daemon's in-memory liveness state. Thread-safe.
type Health struct {
	mu            sync.Mutex
	startedAt     time.Time
	lastHeartbeat time.Time
	lastPing      time.Time
	pingLatencyMS int64
	sessions      int
}

// HealthSnapshot is an immutable view rendered for /health and the status embed.
type HealthSnapshot struct {
	Online        bool   `json:"online"`
	UptimeS       int64  `json:"uptime_s"`
	PingMS        int64  `json:"ping_ms"`
	Sessions      int    `json:"sessions"`
	LastHeartbeat string `json:"last_heartbeat"`
	LastPing      string `json:"last_ping"`
}

// NewHealth starts a Health clock at startedAt.
func NewHealth(startedAt time.Time) *Health { return &Health{startedAt: startedAt} }

// HeartbeatAck records a gateway heartbeat ACK at t.
func (h *Health) HeartbeatAck(t time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastHeartbeat = t
}

// Ping records a successful REST self-ping at t with round-trip latency.
func (h *Health) Ping(t time.Time, latencyMS int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastPing, h.pingLatencyMS = t, latencyMS
}

// SetSessions records the active supervised-bridge count.
func (h *Health) SetSessions(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions = n
}

// Snapshot renders health as of now; Online is true iff the last heartbeat ACK
// is within window.
func (h *Health) Snapshot(now time.Time, window time.Duration) HealthSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	online := !h.lastHeartbeat.IsZero() && now.Sub(h.lastHeartbeat) <= window
	return HealthSnapshot{
		Online:        online,
		UptimeS:       int64(now.Sub(h.startedAt).Seconds()),
		PingMS:        h.pingLatencyMS,
		Sessions:      h.sessions,
		LastHeartbeat: stamp(h.lastHeartbeat),
		LastPing:      stamp(h.lastPing),
	}
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestHealth -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add health.go health_test.go
git commit -m "feat(health): bot/daemon liveness snapshot (heartbeat + self-ping)"
```

---

## Phase 1 — Gateway + slash registration + serve

### Task 6: Add websocket dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dep**

Run:
```bash
go get github.com/coder/websocket@latest
```
Expected: `go.mod` gains the require line, `go.sum` updated.

- [ ] **Step 2: Verify build still works**

Run: `go build ./...`
Expected: success (no usage yet).

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "build: add coder/websocket for the Gateway client"
```

### Task 7: Slash-command registration (RegisterCommands)

**Files:**
- Modify: `interactions.go`
- Test: manual (REST PUT) — verified at boot in Task 9.

- [ ] **Step 1: Implement** (append to `interactions.go`)

```go
import (
	"context"
	"net/http"
)

// AppID returns the bot's application id (== bot user id) via /users/@me.
func (c *Client) AppID(ctx context.Context) (string, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/users/@me", nil)
	if err != nil {
		return "", err
	}
	var u struct{ ID string `json:"id"` }
	if err := c.do(req, &u); err != nil {
		return "", err
	}
	return u.ID, nil
}

// RegisterCommands (re)registers the dctl slash command set for the sole guild
// (guild-scoped commands appear instantly, unlike global ones).
func (c *Client) RegisterCommands(ctx context.Context) error {
	appID, err := c.AppID(ctx)
	if err != nil {
		return err
	}
	g, err := c.SoleGuild(ctx)
	if err != nil {
		return err
	}
	cmds := dctlCommands()
	req, err := c.newRequest(ctx, http.MethodPut,
		"/applications/"+appID+"/guilds/"+g.ID+"/commands", cmds)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// dctlCommands is the declarative slash-command set.
func dctlCommands() []map[string]any {
	str, sub, user := 3, 1, 6 // option types: STRING, SUB_COMMAND, USER
	return []map[string]any{
		{"name": "set", "description": "dctl settings", "options": []map[string]any{
			{"name": "home", "description": "Set the category/forum holding sessions", "type": sub,
				"options": []map[string]any{
					{"name": "channel", "description": "Category or forum", "type": 7, "required": true}, // 7 = CHANNEL
				}},
		}},
		{"name": "session", "description": "Manage Claude sessions", "options": []map[string]any{
			{"name": "create", "description": "Create a session", "type": sub, "options": []map[string]any{
				{"name": "name", "description": "Session name", "type": str, "required": true},
				{"name": "cmd", "description": "Override bridged command", "type": str},
			}},
			{"name": "close", "description": "Close a session", "type": sub, "options": []map[string]any{
				{"name": "name", "description": "Session name", "type": str, "required": true},
			}},
			{"name": "list", "description": "List active sessions", "type": sub},
		}},
		{"name": "allow", "description": "Manage the command allowlist", "options": []map[string]any{
			{"name": "add", "description": "Allow a user", "type": sub, "options": []map[string]any{
				{"name": "user", "description": "User", "type": user, "required": true}}},
			{"name": "remove", "description": "Disallow a user", "type": sub, "options": []map[string]any{
				{"name": "user", "description": "User", "type": user, "required": true}}},
			{"name": "list", "description": "Show the allowlist", "type": sub},
		}},
	}
}

// RespondInteraction sends a CHANNEL_MESSAGE_WITH_SOURCE (type 4) reply.
func (c *Client) RespondInteraction(ctx context.Context, id, token string, r Response) error {
	data := map[string]any{"content": r.Content}
	if r.Ephemeral {
		data["flags"] = 1 << 6 // EPHEMERAL
	}
	body := map[string]any{"type": 4, "data": data}
	req, err := c.newRequest(ctx, http.MethodPost,
		"/interactions/"+id+"/"+token+"/callback", body)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}
```

Merge the `import` additions into the file's existing import block rather than adding a second block.

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add interactions.go
git commit -m "feat(client): slash command registration + interaction responses"
```

### Task 8: Gateway client

**Files:**
- Create: `gateway.go`

- [ ] **Step 1: Implement** — a minimal Gateway client that stays connected (online presence), heartbeats, resumes, and emits decoded interactions.

```go
package dctl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/coder/websocket"
)

const gatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"

// intentGuilds is the only intent we need (interactions don't require message intents).
const intentGuilds = 1 << 0

type gwPayload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  int             `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
}

// Gateway maintains the bot's websocket connection (its online presence) and
// surfaces INTERACTION_CREATE events on Interactions. It records heartbeat ACKs
// into Health (when non-nil) so liveness reflects pure transport state.
type Gateway struct {
	c            *Client
	Interactions chan Interaction
	Health       *Health
}

// NewGateway builds a Gateway for client c. health may be nil.
func NewGateway(c *Client, health *Health) *Gateway {
	return &Gateway{c: c, Interactions: make(chan Interaction, 16), Health: health}
}

// Run connects and processes events until ctx is cancelled. On connection loss
// it returns; the caller (serve loop) reconnects.
func (g *Gateway) Run(ctx context.Context) error {
	if !g.c.Enabled() {
		return ErrDisabled
	}
	conn, _, err := websocket.Dial(ctx, gatewayURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(1 << 20)

	// First frame: Hello (op 10) with heartbeat_interval.
	var hello struct{ HeartbeatInterval int `json:"heartbeat_interval"` }
	first, err := readPayload(ctx, conn)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(first.D, &hello); err != nil {
		return err
	}

	// Identify (op 2).
	identify := map[string]any{
		"op": 2,
		"d": map[string]any{
			"token":   g.c.token,
			"intents": intentGuilds,
			"properties": map[string]any{"os": "linux", "browser": "dctl", "device": "dctl"},
		},
	}
	if err := writeJSON(ctx, conn, identify); err != nil {
		return err
	}

	// Heartbeat loop.
	hbCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		t := time.NewTicker(time.Duration(hello.HeartbeatInterval) * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				_ = writeJSON(hbCtx, conn, map[string]any{"op": 1, "d": nil})
			}
		}
	}()

	// Event loop.
	for {
		p, err := readPayload(ctx, conn)
		if err != nil {
			return err
		}
		if p.Op == 11 && g.Health != nil { // Heartbeat ACK
			g.Health.HeartbeatAck(time.Now())
		}
		if p.Op == 0 && p.T == "INTERACTION_CREATE" {
			var in Interaction
			if err := json.Unmarshal(p.D, &in); err == nil {
				select {
				case g.Interactions <- in:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

func readPayload(ctx context.Context, conn *websocket.Conn) (gwPayload, error) {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return gwPayload{}, err
	}
	var p gwPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return gwPayload{}, fmt.Errorf("gateway decode: %w", err)
	}
	return p, nil
}

func writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	buf, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, buf)
}
```

Note: this implementation omits RESUME for simplicity — on disconnect the serve loop re-IDENTIFYs (a fresh connection). That is acceptable for interactions (no missed-message replay needed). Document this in the serve loop.

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add gateway.go
git commit -m "feat(gateway): minimal Discord Gateway client (online presence + interactions)"
```

### Task 9: Supervisor (child bridge processes)

**Files:**
- Create: `cmd/dctl/supervisor.go`

- [ ] **Step 1: Implement** — spawns `dctl bridge` per session and restarts on crash.

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/vskstudio/dctl"
)

// Supervisor manages one child `dctl bridge` process per session.
type Supervisor struct {
	ctx     context.Context
	selfBin string // path to the dctl binary (os.Args[0])
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

// NewSupervisor builds a Supervisor bound to ctx.
func NewSupervisor(ctx context.Context, selfBin string) *Supervisor {
	return &Supervisor{ctx: ctx, selfBin: selfBin, cancels: map[string]context.CancelFunc{}}
}

// Start launches a supervised bridge for s (idempotent per name).
func (s *Supervisor) Start(sess dctl.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, running := s.cancels[sess.Name]; running {
		return nil
	}
	cctx, cancel := context.WithCancel(s.ctx)
	s.cancels[sess.Name] = cancel
	go s.runLoop(cctx, sess)
	return nil
}

// Stop terminates the bridge for name.
func (s *Supervisor) Stop(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.cancels[name]; ok {
		cancel()
		delete(s.cancels, name)
	}
	return nil
}

func (s *Supervisor) runLoop(ctx context.Context, sess dctl.Session) {
	for {
		if ctx.Err() != nil {
			return
		}
		cmd := exec.CommandContext(ctx, s.selfBin, "bridge",
			"-c", sess.ChannelID, "--cmd", sess.Cmd)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		cmd.Env = os.Environ()
		_ = cmd.Run() // returns on exit or ctx cancel
		if ctx.Err() != nil {
			return
		}
		fmt.Fprintf(os.Stderr, "supervisor: bridge %q exited, restarting in 3s\n", sess.Name)
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add cmd/dctl/supervisor.go
git commit -m "feat(serve): bridge supervisor (spawn/restart per session)"
```

### Task 9b: Worktreer (git worktree per session)

**Files:**
- Create: `cmd/dctl/worktree.go`

Implements the `dctl.worktrees` interface. Worktrees live under `<repo>/.dctl-sessions/<name>` on branch `session/<name>`. Not-a-git-repo / git-missing → `Create` returns `("", nil)` so the handler falls back to a shared session. `Remove` refuses a dirty worktree unless `force`, and always leaves the branch intact.

```go
package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktreer manages git worktrees rooted at repo. Implements dctl.worktrees.
type Worktreer struct {
	ctx  context.Context
	repo string
}

func NewWorktreer(ctx context.Context, repo string) *Worktreer { return &Worktreer{ctx: ctx, repo: repo} }

func (w *Worktreer) isGitRepo() bool {
	cmd := exec.CommandContext(w.ctx, "git", "-C", w.repo, "rev-parse", "--git-dir")
	return cmd.Run() == nil
}

func (w *Worktreer) path(name string) string {
	return filepath.Join(w.repo, ".dctl-sessions", name)
}

// Create adds a worktree on branch session/<name>. Returns ("", nil) when repo
// is not a git repo (caller falls back to shared).
func (w *Worktreer) Create(name string) (string, error) {
	if !w.isGitRepo() {
		return "", nil
	}
	p := w.path(name)
	out, err := exec.CommandContext(w.ctx, "git", "-C", w.repo,
		"worktree", "add", p, "-b", "session/"+name).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("worktree add: %s", strings.TrimSpace(string(out)))
	}
	return p, nil
}

// Remove removes the worktree. If it has uncommitted changes and !force, it
// refuses with the status. The branch session/<name> is always left intact.
func (w *Worktreer) Remove(name string, force bool) error {
	p := w.path(name)
	if !force {
		out, _ := exec.CommandContext(w.ctx, "git", "-C", p, "status", "--porcelain").Output()
		if strings.TrimSpace(string(out)) != "" {
			return fmt.Errorf("worktree %q has uncommitted changes", name)
		}
	}
	args := []string{"-C", w.repo, "worktree", "remove", p}
	if force {
		args = append(args, "--force")
	}
	out, err := exec.CommandContext(w.ctx, "git", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("worktree remove: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
```

Add `.dctl-sessions/` to the repo `.gitignore` (Task 12).

**Supervisor change:** the child bridge must run with `cwd` = the session's worktree. In `cmd/dctl/supervisor.go`, set `cmd.Dir = sess.Worktree` when non-empty:

```go
		cmd := exec.CommandContext(ctx, s.selfBin, "bridge",
			"-c", sess.ChannelID, "--cmd", sess.Cmd)
		if sess.Worktree != "" {
			cmd.Dir = sess.Worktree
		}
```

**Slash options:** in `dctlCommands()` add to `session create` a `{"name":"shared","type":5}` (BOOLEAN) option, and to `session close` a `{"name":"force","type":5}` option.

**serve wiring:** build the Worktreer and pass it to `NewHandler`:

```go
	repo := st.Repo
	if repo == "" {
		repo, _ = os.Getwd()
	}
	wt := NewWorktreer(ctx, repo)
	h := dctl.NewHandler(c, sup, wt, st, *defaultCmd)
```

**Test additions** (`handler_test.go`): worktree is created on `session create` (assert `wt.created`), `shared:true` skips it, dirty close without force is refused (`fakeWT.removeErr` set) and removed with force. (The `newTestHandler` helper already returns `*fakeWT`.)

- [ ] **Step 1:** Write `cmd/dctl/worktree.go` as above.
- [ ] **Step 2:** `go build ./...` → success.
- [ ] **Step 3:** Add the worktree handler tests; `go test ./...` → PASS.
- [ ] **Step 4: Commit**

```bash
git add cmd/dctl/worktree.go cmd/dctl/supervisor.go interactions.go handler_test.go
git commit -m "feat(serve): git worktree isolation per session (shared/force opts)"
```

### Task 10: `dctl serve` command + wiring

**Files:**
- Create: `cmd/dctl/serve.go`
- Modify: `cmd/dctl/main.go`

- [ ] **Step 1: Implement serve**

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vskstudio/dctl"
)

func defaultStatePath() string {
	if d := os.Getenv("DCTL_STATE_DIR"); d != "" {
		return filepath.Join(d, "state.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dctl", "state.json")
}

func runServe(ctx context.Context, c *dctl.Client, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	statePath := fs.String("state", defaultStatePath(), "path to the daemon state file")
	defaultCmd := fs.String("cmd", "claude -p --continue", "default bridged command for new sessions")
	healthAddr := fs.String("health-addr", "", "if set (e.g. :8787), serve GET /health")
	statusChannel := fs.String("status-channel", "", "if set, maintain a self-updating status embed there")
	fs.Parse(args)
	if !c.Enabled() {
		return dctl.ErrDisabled
	}

	health := dctl.NewHealth(time.Now())

	st, err := dctl.LoadState(*statePath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	// Seed the allowlist with the owner on first run.
	if owner := os.Getenv("DCTL_OWNER_ID"); owner != "" {
		_ = st.AddAllow(owner)
	}

	self, _ := os.Executable()
	sup := NewSupervisor(ctx, self)
	// Restart persisted sessions.
	for _, sess := range st.SnapshotSessions() {
		_ = sup.Start(sess)
	}
	health.SetSessions(len(st.SnapshotSessions()))

	repo := st.Repo
	if repo == "" {
		repo, _ = os.Getwd()
	}
	wt := NewWorktreer(ctx, repo)
	h := dctl.NewHandler(c, sup, wt, st, *defaultCmd)

	if err := c.RegisterCommands(ctx); err != nil {
		return fmt.Errorf("register commands: %w", err)
	}

	// Liveness: HTTP /health endpoint (optional).
	if *healthAddr != "" {
		go serveHealth(ctx, *healthAddr, health)
	}
	// Self-ping ticker: independent REST reachability latency.
	go pingLoop(ctx, c, health)
	// Status embed (optional).
	if *statusChannel != "" {
		go statusLoop(ctx, c, st, health, *statusChannel)
	}

	fmt.Fprintln(os.Stderr, "dctl serve: commands registered; connecting to gateway…")

	// Reconnect loop: a dropped connection just re-IDENTIFYs (no resume).
	for ctx.Err() == nil {
		gw := dctl.NewGateway(c, health)
		errCh := make(chan error, 1)
		go func() { errCh <- gw.Run(ctx) }()
		for {
			select {
			case in := <-gw.Interactions:
				resp := h.Handle(ctx, in)
				if err := c.RespondInteraction(ctx, in.ID, in.Token, resp); err != nil {
					fmt.Fprintf(os.Stderr, "respond: %v\n", err)
				}
				health.SetSessions(len(st.SnapshotSessions())) // session count may have changed
			case err := <-errCh:
				fmt.Fprintf(os.Stderr, "gateway closed (%v); reconnecting…\n", err)
				goto reconnect
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	reconnect:
	}
	return ctx.Err()
}
```

Note: `NewHandler(c, ...)` requires `*dctl.Client` to satisfy the `discord` interface — it already has `ChannelType`, `CreateChannelUnder`, `ForumPost`, `ArchiveChannel` after Task 5. Confirm the method set matches.

- [ ] **Step 1b: Add the liveness helpers** — create `cmd/dctl/health.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/vskstudio/dctl"
)

const healthWindow = 90 * time.Second

// serveHealth runs a tiny HTTP server exposing GET /health (200 online / 503 down).
func serveHealth(ctx context.Context, addr string, h *dctl.Health) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		snap := h.Snapshot(time.Now(), healthWindow)
		w.Header().Set("Content-Type", "application/json")
		if !snap.Online {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(snap)
	})
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	if err := srv.ListenAndServe(); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "health server: %v\n", err)
	}
}

// pingLoop records an independent REST reachability latency every 30s.
func pingLoop(ctx context.Context, c *dctl.Client, h *dctl.Health) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			start := time.Now()
			if _, err := c.AppID(ctx); err == nil {
				h.Ping(time.Now(), time.Since(start).Milliseconds())
			}
		}
	}
}

// statusLoop maintains a single self-updating status embed in channelID.
func statusLoop(ctx context.Context, c *dctl.Client, st *dctl.State, h *dctl.Health, channelID string) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	render := func() {
		snap := h.Snapshot(time.Now(), healthWindow)
		dot, word := "🟢", "online"
		if !snap.Online {
			dot, word = "🔴", "offline"
		}
		content := fmt.Sprintf("%s **dctl %s** · uptime %s · ping %dms · %d sessions",
			dot, word, (time.Duration(snap.UptimeS) * time.Second).String(), snap.PingMS, snap.Sessions)
		id, err := c.UpsertStatusMessage(ctx, channelID, st.StatusMessageID, content)
		if err == nil && id != st.StatusMessageID {
			_ = st.SetStatusMessageID(id)
		}
	}
	render()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			render()
		}
	}
}
```

Add to `state.go` (Task 2 location):

```go
// SetStatusMessageID caches the status embed's message id and persists.
func (s *State) SetStatusMessageID(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.StatusMessageID = id
	return s.saveLocked()
}
```

Add to `dctl.go` or `interactions.go` an upsert helper:

```go
// UpsertStatusMessage edits the existing status message (if msgID is set and
// still exists) or sends a new one, returning the live message id.
func (c *Client) UpsertStatusMessage(ctx context.Context, channelID, msgID, content string) (string, error) {
	if msgID != "" {
		req, err := c.newRequest(ctx, http.MethodPatch,
			"/channels/"+channelID+"/messages/"+msgID, map[string]any{"content": content})
		if err == nil {
			if err := c.do(req, nil); err == nil {
				return msgID, nil
			}
		}
		// fall through to re-create if the edit failed (message deleted)
	}
	m, err := c.Send(ctx, channelID, content)
	if err != nil {
		return "", err
	}
	return m.ID, nil
}
```

- [ ] **Step 2: Wire into main.go**

In `cmd/dctl/main.go`, add to the `switch cmd` block:

```go
	case "serve":
		err = runServe(ctx, client, args)
```

And add a usage line in `usage()` near the others:

```go
	// serve: run the always-on Gateway daemon (online presence + slash commands)
```

Seed the owner id by exporting `DCTL_OWNER_ID=343535234303787009` in the run environment (or rely on `/allow add`). Document this in the skill update (Task 12).

- [ ] **Step 3: Build + vet**

Run: `go build ./... && go vet ./...`
Expected: success.

- [ ] **Step 4: Manual smoke test**

Run:
```bash
DCTL_OWNER_ID=343535234303787009 ./dctl serve
```
Expected: stderr shows "commands registered; connecting to gateway…"; the bot shows **online** in Discord; `/set home`, `/session`, `/allow` appear in the slash-command picker.

- [ ] **Step 5: Commit**

```bash
git add cmd/dctl/serve.go cmd/dctl/main.go
git commit -m "feat(serve): dctl serve daemon — gateway loop + interaction dispatch"
```

---

## Phase 3 — Forum variant (verification)

Forum support is already implemented in the Handler (`home.Type == "forum"` → `ForumPost`) and `ArchiveChannel` archives forum-post threads. This phase only verifies end-to-end.

### Task 11: Forum end-to-end check

**Files:** none (manual verification + a Handler test already covers routing).

- [ ] **Step 1: Confirm forum routing test exists** — add to `handler_test.go`:

```go
func TestSessionCreateForum(t *testing.T) {
	h, d, sup, st := newTestHandler(t, ChannelForum)
	st.SetHome(HomeRef{ID: "forum1", Type: "forum"})
	h.Handle(context.Background(), it("owner", "session", "create",
		InteractionOption{Name: "name", Value: "topic"}))
	if len(d.created) != 1 || d.created[0] != "forum:topic" {
		t.Fatalf("expected forum post: %+v", d.created)
	}
	if len(sup.started) != 1 {
		t.Fatal("expected bridge started")
	}
}
```

- [ ] **Step 2: Run**

Run: `go test ./... -run TestSessionCreateForum -v`
Expected: PASS

- [ ] **Step 3: Manual** — `/set home` a forum channel, `/session create name:demo`, confirm a forum post is created and the bridge responds in it.

- [ ] **Step 4: Commit**

```bash
git add handler_test.go
git commit -m "test(handler): forum session creation"
```

---

## Docs

### Task 12: Update the dctl skill

**Files:**
- Modify: `.claude/skills/dctl/SKILL.md`

- [ ] **Step 1:** Add a "Daemon (`dctl serve`)" section documenting: online presence, the `/set home`, `/session create|close|list`, `/allow` commands, the allowlist (seeded via `DCTL_OWNER_ID`), the state file path, and that sessions are supervised bridges that survive restarts.

- [ ] **Step 2: Commit**

```bash
git add .claude/skills/dctl/SKILL.md
git commit -m "docs(skills): dctl serve daemon + session/allow commands"
```

---

## Self-Review notes

- **Spec coverage:** online presence (Task 8/10), slash commands (Task 7), `/set home` w/ auto-detect (Task 4/5), `/session` create/close/list + supervisor (Task 4/9/10), forum variant (Task 11), allowlist seeded with owner (Task 2/10), persisted state (Task 1), liveness `Health` + `/health` endpoint (Task 5b/10) + self-updating status embed (Task 10 Step 1b) — driven only by gateway heartbeat ACK + REST self-ping, never by a Claude session. All covered.
- **Type consistency:** `Session`, `HomeRef`, `Response`, `Interaction` defined once (Tasks 1,3) and reused; `discord` interface methods match Client methods added in Task 5; `Handler` signature stable across tasks.
- **Known simplification:** Gateway has no RESUME (reconnect re-identifies). Acceptable for interaction-only traffic; noted in Task 8/10.
