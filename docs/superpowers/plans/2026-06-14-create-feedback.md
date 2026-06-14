# Implementation Plan — Spec 1/4: Enriched `/session create` feedback

Date: 2026-06-14
Spec: `docs/superpowers/specs/2026-06-14-create-feedback-design.md`

## Goal

Make `/session create` report **exactly where the work will happen** instead of a bare
"running on <#…>". Produce a single `sessionBanner` body listing project/repo, worktree
path, namespaced branch, mode (isolated / shared-main / non-git) and bridge command, then
surface it on **two** surfaces: the ephemeral interaction reply (prefixed with "Running
on <#…>") AND a message posted into the freshly created channel/forum-post (via a new
`Send` method on the `discord` interface). The in-channel post is best-effort: a failed
`Send` must not fail the create.

## Architecture

`Handler` (in `internal/handler`) already owns the `discord`/`supervisor`/`worktrees`
interfaces and `*state.State`. This spec:
- Adds `repo string` to `Handler` + `NewHandler`, sourced from `serve.go` (same resolved
  `repo` var already passed to `NewWorktreer`).
- Adds `Send(ctx, channelID, content) (*dctl.Message, error)` to the `discord` interface
  (already implemented by `*dctl.Client`, `dctl.go:91`).
- Adds a pure helper `sessionBanner(repo, name, worktree, cmd string, shared bool) string`.
- Rewrites the tail of `sessionCreate`: drop the `note` var, compute the banner, post it
  best-effort after `sup.Start`, and return the enriched ephemeral reply.

Mode selection is derived in the banner, not stored: `worktree != ""` → isolated;
`worktree == "" && shared` → shared (main checkout); `worktree == "" && !shared` →
shared (not a git repo). This relies on Spec 3 (namespaced branch `session/<name>`) and
Spec 2 (per-session repo) already existing — the branch string and `repo` value are taken
as given here; this plan does not recompute namespacing.

## Tech Stack

Go (standard library only: `fmt`, `path/filepath`, `regexp`, `context`). Tests via
`go test ./...`, table-driven, using the existing fake `discord`/`supervisor`/`worktrees`
in `internal/handler/handler_test.go`.

## For agentic workers

REQUIRED SUB-SKILL: superpowers:subagent-driven-development. Execute each task as an
isolated TDD cycle (failing test → run-fail → minimal impl → run-pass → commit). Do not
batch tasks. Use exact file paths and the literal code below — no placeholders.

---

## File Structure

Files touched (all under `/home/shan/dev/dctl`):

```
internal/handler/handler.go        # discord.Send, Handler.repo, NewHandler param,
                                    #   sessionBanner helper, sessionCreate rewrite
internal/handler/handler_test.go   # fakeDiscord.Send + 5 table-driven banner tests
internal/serve/serve.go            # pass repo into NewHandler (1 line)
```

No changes to `internal/state/state.go` or `internal/worktree/worktree.go` (Specs 2 & 3
already landed). No new files.

Key fixtures already present in `handler_test.go` and reused as-is:
- `newTestHandler(t, homeType)` → returns `(*Handler, *fakeDiscord, *fakeSup, *fakeWT, *state.State)`.
- `it(user, cmd, sub, opts...)` builds an `dctl.Interaction`.
- `fakeWT.path` (`""` → shared fallback) controls the worktree result.
- The owner `"owner"` is pre-allowlisted; home must be set per-test.

---

## Task 1 — Add `Send` to the `discord` interface and the test fake

The new in-channel post needs `Send` on the interface. Wire the interface + fake first so
later tests can assert on captured sends; `*dctl.Client` already satisfies it.

### 1a. Failing test

Add a compile-time interface-satisfaction assertion and a `Send` recorder to the fake in
`internal/handler/handler_test.go`. Add this method right after the existing
`ArchiveChannel` method (after line 32) so `fakeDiscord` records sends:

```go
func (f *fakeDiscord) Send(ctx context.Context, channelID, content string) (*dctl.Message, error) {
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.sent = append(f.sent, sentMsg{channelID: channelID, content: content})
	return &dctl.Message{ID: "msg-" + channelID, ChannelID: channelID, Content: content}, nil
}
```

Extend the `fakeDiscord` struct (lines 12-16) to:

```go
type sentMsg struct{ channelID, content string }

type fakeDiscord struct {
	created  []string
	archived []string
	homeType int
	sent     []sentMsg
	sendErr  error
}
```

And add a new standalone test that fails until the interface declares `Send`:

```go
func TestDiscordInterfaceHasSend(t *testing.T) {
	var _ discord = (*fakeDiscord)(nil)
	var _ discord = (*dctl.Client)(nil)
}
```

### 1b. Run-fail

```
go test ./internal/handler/
```

Expected: compile failure, e.g.
```
internal/handler/handler_test.go:NN: cannot use (*fakeDiscord)(nil) (value of type *fakeDiscord) as discord value in variable declaration: *fakeDiscord does not implement discord (missing method Send)
```
(The interface assertion fails to compile because `discord` does not yet declare `Send`.)

### 1c. Minimal impl

In `internal/handler/handler.go`, add `Send` to the `discord` interface (lines 19-24):

```go
type discord interface {
	ChannelType(ctx context.Context, id string) (int, error)
	CreateChannelUnder(ctx context.Context, parentID, name string) (*dctl.Channel, error)
	ForumPost(ctx context.Context, forumID, name, content string) (*dctl.Channel, error)
	ArchiveChannel(ctx context.Context, id string) error
	Send(ctx context.Context, channelID, content string) (*dctl.Message, error)
}
```

### 1d. Run-pass

```
go test ./internal/handler/
```
Expected: PASS (existing tests unaffected; new assertion compiles).

### 1e. Commit

```
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "handler: add Send to discord interface + test fake"
```

---

## Task 2 — `Handler.repo` field, `NewHandler` param, serve.go wiring

The banner needs the repo path. Inject it the same way the worktreer gets it.

### 2a. Failing test

`newTestHandler` (handler_test.go lines 57-65) calls `NewHandler(d, sup, wt, st, "claude")`.
Change that single call to pass a repo so the test exercises the new signature, and add a
small assertion. Replace lines 64:

```go
	return NewHandler(d, sup, wt, st, "/proj/repo", "claude"), d, sup, wt, st
```

Add a focused test:

```go
func TestNewHandlerStoresRepo(t *testing.T) {
	h, _, _, _, _ := newTestHandler(t, dctl.ChannelText)
	if h.repo != "/proj/repo" {
		t.Fatalf("expected repo stored, got %q", h.repo)
	}
}
```

### 2b. Run-fail

```
go test ./internal/handler/
```
Expected: compile failure —
```
not enough arguments in call to NewHandler
    have (*fakeDiscord, *fakeSup, *fakeWT, *state.State, string)
    want (...)   // and: h.repo undefined (type *Handler has no field or method repo)
```

### 2c. Minimal impl

In `internal/handler/handler.go`, add the field to `Handler` (lines 40-46):

```go
type Handler struct {
	d          discord
	sup        supervisor
	wt         worktrees
	st         *state.State
	repo       string
	defaultCmd string
}
```

Update `NewHandler` (lines 48-52):

```go
// NewHandler builds a Handler. repo is the project root sessions operate on (used for
// the create banner). defaultCmd is the bridge command used when a session is created
// without an explicit cmd (e.g. "claude -p --continue").
func NewHandler(d discord, sup supervisor, wt worktrees, st *state.State, repo, defaultCmd string) *Handler {
	return &Handler{d: d, sup: sup, wt: wt, st: st, repo: repo, defaultCmd: defaultCmd}
}
```

### 2d. Run-pass

```
go test ./internal/handler/
```
Expected: PASS.

Then fix the production caller `internal/serve/serve.go` line 65:

```go
	hdl := handler.NewHandler(c, sup, wt, st, repo, o.DefaultCmd)
```

Verify the whole module still builds:

```
go build ./...
```
Expected: no output (success).

### 2e. Commit

```
git add internal/handler/handler.go internal/handler/handler_test.go internal/serve/serve.go
git commit -m "handler: inject repo into Handler/NewHandler; wire from serve"
```

---

## Task 3 — `sessionBanner` helper (3 modes, table-driven)

The pure formatter is the heart of the spec. Test all three modes before any wiring.

### 3a. Failing test

Add to `internal/handler/handler_test.go` (uses `strings` — add `"strings"` to the test
imports):

```go
func TestSessionBanner(t *testing.T) {
	const repo = "/home/me/proj"
	cases := []struct {
		name     string
		worktree string
		shared   bool
		want     []string // substrings that MUST appear
		absent   []string // substrings that MUST NOT appear
	}{
		{
			name:     "isolated",
			worktree: "/home/me/proj/.dctl-sessions/demo",
			shared:   false,
			want: []string{
				"🚀 Session **demo** ready.",
				"Project: **proj** (`/home/me/proj`)",
				"Mode: isolated worktree",
				"Worktree: `/home/me/proj/.dctl-sessions/demo`",
				"Branch: `session/demo`",
				"Command: `claude`",
			},
			absent: []string{"main checkout", "not a git repo"},
		},
		{
			name:     "shared main checkout",
			worktree: "",
			shared:   true,
			want: []string{
				"🚀 Session **demo** ready.",
				"Project: **proj** (`/home/me/proj`)",
				"Mode: shared (main checkout)",
				"Branch: — (runs on current branch)",
				"Command: `claude`",
			},
			absent: []string{"Worktree:", "isolated worktree", "not a git repo"},
		},
		{
			name:     "non-git shared",
			worktree: "",
			shared:   false,
			want: []string{
				"🚀 Session **demo** ready.",
				"Project: **proj** (`/home/me/proj`)",
				"Mode: shared (not a git repo)",
				"Command: `claude`",
			},
			absent: []string{"Worktree:", "Branch:", "isolated worktree", "main checkout"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionBanner(repo, "demo", tc.worktree, "claude", tc.shared)
			for _, s := range tc.want {
				if !strings.Contains(got, s) {
					t.Errorf("banner missing %q\n--- got ---\n%s", s, got)
				}
			}
			for _, s := range tc.absent {
				if strings.Contains(got, s) {
					t.Errorf("banner unexpectedly contains %q\n--- got ---\n%s", s, got)
				}
			}
		})
	}
}
```

### 3b. Run-fail

```
go test ./internal/handler/ -run TestSessionBanner
```
Expected: compile failure — `undefined: sessionBanner`.

### 3c. Minimal impl

In `internal/handler/handler.go`, add `"path/filepath"` to the import block (lines 3-10),
and add the helper (place it after `errf`, before `Handle`):

```go
// sessionBanner renders the shared context body posted on /session create. worktree=""
// means no isolated worktree was made; shared distinguishes an explicit shared:true run
// (main checkout) from a non-git fallback.
func sessionBanner(repo, name, worktree, cmd string, shared bool) string {
	project := filepath.Base(repo)
	b := fmt.Sprintf("🚀 Session **%s** ready.\n", name)
	b += fmt.Sprintf("• Project: **%s** (`%s`)\n", project, repo)
	switch {
	case worktree != "":
		b += "• Mode: isolated worktree\n"
		b += fmt.Sprintf("• Worktree: `%s`\n", worktree)
		b += fmt.Sprintf("• Branch: `session/%s`\n", name)
	case shared:
		b += "• Mode: shared (main checkout)\n"
		b += "• Branch: — (runs on current branch)\n"
	default:
		b += "• Mode: shared (not a git repo)\n"
	}
	b += fmt.Sprintf("• Command: `%s`", cmd)
	return b
}
```

### 3d. Run-pass

```
go test ./internal/handler/ -run TestSessionBanner
```
Expected: PASS (all three subtests).

### 3e. Commit

```
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "handler: add sessionBanner formatter (isolated/shared/non-git)"
```

---

## Task 4 — Wire banner into `sessionCreate` (ephemeral reply + in-channel Send)

Replace the `note` mechanism with the banner; post it best-effort after `sup.Start`.

### 4a. Failing test

Add to `internal/handler/handler_test.go`. These assert on the ephemeral reply content and
the captured `Send` for the three modes plus forum, plus the Send-failure tolerance:

```go
func TestSessionCreateBanner(t *testing.T) {
	cases := []struct {
		name      string
		homeType  int
		homeRef   state.HomeRef
		wtPath    string // fakeWT.path
		shared    bool
		wantReply []string
		wantSend  []string
	}{
		{
			name:     "category isolated",
			homeType: dctl.ChannelText,
			homeRef:  state.HomeRef{ID: "cat1", Type: "category"},
			wtPath:   "/wt/x",
			shared:   false,
			wantReply: []string{
				"✅ Running on <#new-demo>.",
				"Mode: isolated worktree",
				"Worktree: `/wt/x`",
				"Branch: `session/demo`",
				"Command: `claude`",
			},
			wantSend: []string{"Mode: isolated worktree", "Worktree: `/wt/x`"},
		},
		{
			name:     "category non-git shared",
			homeType: dctl.ChannelText,
			homeRef:  state.HomeRef{ID: "cat1", Type: "category"},
			wtPath:   "", // shared fallback
			shared:   false,
			wantReply: []string{
				"✅ Running on <#new-demo>.",
				"Mode: shared (not a git repo)",
			},
			wantSend: []string{"Mode: shared (not a git repo)"},
		},
		{
			name:     "category shared:true",
			homeType: dctl.ChannelText,
			homeRef:  state.HomeRef{ID: "cat1", Type: "category"},
			wtPath:   "/wt/x", // ignored: shared skips wt.Create
			shared:   true,
			wantReply: []string{
				"Mode: shared (main checkout)",
				"Branch: — (runs on current branch)",
			},
			wantSend: []string{"Mode: shared (main checkout)"},
		},
		{
			name:     "forum isolated",
			homeType: dctl.ChannelForum,
			homeRef:  state.HomeRef{ID: "forum1", Type: "forum"},
			wtPath:   "/wt/x",
			shared:   false,
			wantReply: []string{
				"✅ Running on <#post-demo>.",
				"Mode: isolated worktree",
			},
			wantSend: []string{"Mode: isolated worktree"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, d, _, wt, st := newTestHandler(t, tc.homeType)
			wt.path = tc.wtPath
			st.SetHome(tc.homeRef)
			opts := []dctl.InteractionOption{{Name: "name", Value: "demo"}}
			if tc.shared {
				opts = append(opts, dctl.InteractionOption{Name: "shared", Value: true})
			}
			r := h.Handle(context.Background(), it("owner", "session", "create", opts...))
			if !r.Ephemeral {
				t.Fatalf("reply must be ephemeral: %+v", r)
			}
			for _, s := range tc.wantReply {
				if !strings.Contains(r.Content, s) {
					t.Errorf("reply missing %q\n--- got ---\n%s", s, r.Content)
				}
			}
			if len(d.sent) != 1 {
				t.Fatalf("expected exactly one in-channel Send, got %d: %+v", len(d.sent), d.sent)
			}
			sess, _ := st.FindSession("demo")
			if d.sent[0].channelID != sess.ChannelID {
				t.Errorf("Send went to %q, want session channel %q", d.sent[0].channelID, sess.ChannelID)
			}
			for _, s := range tc.wantSend {
				if !strings.Contains(d.sent[0].content, s) {
					t.Errorf("in-channel banner missing %q\n--- got ---\n%s", s, d.sent[0].content)
				}
			}
			if strings.Contains(d.sent[0].content, "Running on") {
				t.Errorf("in-channel banner must not carry the 'Running on' prefix:\n%s", d.sent[0].content)
			}
		})
	}
}

func TestSessionCreateSendFailureDoesNotFail(t *testing.T) {
	h, d, sup, _, st := newTestHandler(t, dctl.ChannelText)
	d.sendErr = errors.New("discord 500")
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	r := h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if !strings.Contains(r.Content, "✅ Running on") {
		t.Fatalf("create must still succeed when Send fails, got: %q", r.Content)
	}
	if _, ok := st.FindSession("demo"); !ok {
		t.Fatal("session must remain persisted despite Send failure")
	}
	if len(sup.started) != 1 {
		t.Fatal("bridge must have started")
	}
	if len(d.sent) != 0 {
		t.Fatalf("no send should be recorded on error, got %+v", d.sent)
	}
}
```

Note: `errors` is already imported in the test file (line 5).

### 4b. Run-fail

```
go test ./internal/handler/ -run 'TestSessionCreateBanner|TestSessionCreateSendFailure'
```
Expected: FAIL — `sessionCreate` still returns the old `"✅ Session ... running on ..."`
string and never calls `Send`, so reply-substring checks and `len(d.sent) != 1` fail:
```
reply missing "✅ Running on <#new-demo>."
expected exactly one in-channel Send, got 0: []
```

### 4c. Minimal impl

In `internal/handler/handler.go`, rewrite the worktree block and the tail of
`sessionCreate`. Replace lines 137-150 (the `note`-bearing block) with:

```go
	// Worktree isolation by default; shared:true runs in the main checkout.
	shared := in.Data.OptBool("shared")
	var worktree string
	if !shared {
		path, err := h.wt.Create(name)
		if err != nil {
			return errf("worktree: %v", err)
		}
		worktree = path // "" means non-git fallback
	}
```

Replace the final `return` (line 176) and the lines just above it. After the
`if err := h.sup.Start(sess); err != nil { ... }` block, change the closing return to:

```go
	banner := sessionBanner(h.repo, name, worktree, cmd, shared)
	_, _ = h.d.Send(ctx, sess.ChannelID, banner) // best-effort; reply is source of truth
	reply := fmt.Sprintf("✅ Running on <#%s>.\n\n%s", sess.ChannelID, banner)
	return dctl.Response{Content: reply, Ephemeral: true}
```

(The `note` variable is now gone; ensure no remaining reference to `note` compiles.)

### 4d. Run-pass

```
go test ./internal/handler/
go build ./...
```
Expected: all handler tests PASS (including the older `TestSessionCreateText/Shared/Forum`
which only assert on created/started/persisted, untouched by the banner change), and a
clean build.

### 4e. Commit

```
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "handler: enrich /session create with banner reply + in-channel post"
```

---

## Task 5 — Full-suite green + manual render check

### 5a. Run

```
go test ./...
go vet ./...
go build ./...
```
Expected: all packages PASS, vet clean, build clean.

### 5b. Visual render (manual, from spec §7 success criterion)

Print one rendered isolated-mode banner to eyeball Discord markdown:

```
go test ./internal/handler/ -run TestSessionBanner -v
```
Confirm a body matching spec §4:
```
🚀 Session **demo** ready.
• Project: **proj** (`/home/me/proj`)
• Mode: isolated worktree
• Worktree: `/home/me/proj/.dctl-sessions/demo`
• Branch: `session/demo`
• Command: `claude`
```

### 5c. Commit (only if anything changed; otherwise skip)

No code change expected here — this task is verification per
superpowers:verification-before-completion. Do not claim done until the three commands
above show passing output.

---

## Self-review against the spec

- §1 two surfaces: ephemeral reply (Task 4, "✅ Running on …" + banner) and in-channel
  post (`h.d.Send`, Task 4). ✅
- §3 `repo` injected into `Handler`/`NewHandler`, wired from `serve.go` with the same
  resolved `repo` value (Task 2). ✅ `filepath.Base(repo)` used for project name (Task 3). ✅
- §4 exact banner format for all three modes; selection rule
  (`worktree!=""` → isolated; `""+shared` → main checkout; `""+!shared` → non-git)
  implemented and table-tested (Task 3). Ephemeral prefix `✅ Running on <#id>.` + blank
  line + banner; in-channel = banner only, no prefix (Task 4, asserted). ✅
- §5 files touched: handler.go (interface `Send`, `repo` field, `NewHandler` param,
  `sessionCreate` rewrite, `sessionBanner`, `path/filepath` import); serve.go line 65;
  test mock `Send`. `note` var removed. No state.go/worktree.go change. ✅
- §6 edge cases: non-git (`Create` returns `"",nil` → non-git mode, no worktree/branch
  lines); shared:true vs non-git distinguished by the `shared` flag; `Send` failure is
  best-effort (`_, _ =`) and tolerated (Task 4 test); rollback paths (create channel/forum,
  persist, start) unchanged — `Send` happens after, outside rollback. ✅ Forum keeps its
  `ForumPost` amorce ("Session **<name>** started.") AND gets a separate banner `Send`
  (Option A) — verified by `len(d.sent)==1` while `ForumPost` still recorded in
  `d.created`. ✅
- §7 tests: category+isolated, category+non-git, category+shared:true, forum+isolated,
  Send-failure, repo base/full-path display — all covered (Tasks 3 & 4). ✅

### Discrepancies / notes for the implementer

1. **Spec §4 helper signature** lists `sessionBanner(repo, name, worktree, cmd string)` —
   four params, no `shared`. But the three-way mode selection (main-checkout vs non-git)
   is **undecidable from `worktree` alone** when `worktree == ""`. This plan adds a fifth
   `shared bool` param (`sessionBanner(repo, name, worktree, cmd string, shared bool)`).
   This is the minimal correct deviation; the spec's own §3 selection rule requires the
   `shared` bit. Flag this if the spec's signature is considered binding.
2. **Spec §2 "Actuel" line refs (l.118-177)** match the current `handler.go` exactly, so
   Specs 2 & 3 evidently did **not** alter `sessionCreate`'s shape here — yet the global
   ordering says 3→2→1 already landed. The branch is hard-coded `session/<name>` in the
   banner per spec §3 example; if Spec 3's namespacing instead prefixes the branch with an
   `instanceID` (e.g. `session/<instanceID>/<name>`), the banner's `Branch:` line and its
   test substring must be updated to match the real branch string produced by the
   worktreer. Confirm the actual branch format before finalizing Task 3/4 assertions.
3. **Forum double-message**: per Option A the forum gets two posts (amorce + banner). Spec
   §6 accepts this; no archival/order change. Implementer should not "optimize" it into one.
