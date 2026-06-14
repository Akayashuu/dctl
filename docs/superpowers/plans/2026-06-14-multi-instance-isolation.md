# Multi-Instance Isolation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let several `dctl serve` daemons (distinct users/instances) share one Discord home (category or forum) without colliding on session names, worktree paths, git branches, or Discord posts, by namespacing global resources with a per-daemon `instanceID`.

**Architecture:** Each daemon resolves a short, stable `instanceID` (from `DCTL_INSTANCE_ID`, else derived from `DCTL_OWNER_ID`, else legacy mode) and persists it once in `state.json`. The instance id is frozen: starting with a different id than the one stored is refused. Namespacing is applied **at the boundary** — git branch `session/<instanceID>/<name>`, worktree dir `.dctl-sessions/<instanceID>/<name>`, Discord title `<instanceID>__<name>` — while the local state keeps the bare logical name as its key. Legacy sessions (empty `instanceID`) keep behaving exactly as today. This is Spec 3/4 and the structural socle the other specs depend on (global implementation order 3 → 2 → 1 → 4).

**Tech Stack:** Go 1.23, standard library only (`encoding/json`, `os/exec` git, `regexp`, `path/filepath`), table-driven `testing`. Module `github.com/vskstudio/dctl`.

---

## File Structure

**Created:**
- `internal/instanceid/instanceid.go` — pure resolution + validation logic: `Validate`, `Slugify`, `Resolve`. No I/O, fully unit-testable. One clear responsibility: turn env/owner inputs into a validated `instanceID` slug.
- `internal/instanceid/instanceid_test.go` — table-driven tests for the above.

**Modified:**
- `internal/state/state.go` — add `State.InstanceID` field, `QualifiedName(name)` helper, and `SetInstanceID(id)` persister.
- `internal/state/state_test.go` — tests for `QualifiedName` and `SetInstanceID`/round-trip.
- `internal/worktree/worktree.go` — `NewWorktreer` takes `instanceID`; `path` and branch become namespaced; empty id = legacy.
- `internal/worktree/worktree_test.go` *(created — no test file exists today)* — tests for namespaced vs legacy `path`/branch via an exported helper.
- `internal/handler/handler.go` — Discord title uses `st.QualifiedName(name)`; logical name stays the state key.
- `internal/handler/handler_test.go` — assert qualified title is what's sent to Discord; assert ownership invariant on close.
- `internal/serve/serve.go` — resolve + freeze `instanceID` before building the worktreer; thread it through; add `Options.InstanceID`.
- `internal/serve/loops.go` — prefix the status embed title with the instance id.
- `cmd/dctl/serve.go` — add `-instance` flag wired to `Options.InstanceID`.

> The status embed prefix (loops.go) is included because Spec §6 requires it and it is a one-line change in a file this plan already touches; it is verified by a dedicated test.

---

## Task 1: instanceID validation + slugify

**Files:**
- Create: `internal/instanceid/instanceid.go`
- Test: `internal/instanceid/instanceid_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/instanceid/instanceid_test.go`:

```go
package instanceid

import "testing"

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		id   string
		ok   bool
	}{
		{"simple", "alice", true},
		{"with-digits", "u12345678", true},
		{"with-hyphen", "team-a", true},
		{"max-len-16", "abcdefghijklmnop", true},
		{"single-char", "a", true},
		{"empty", "", false},
		{"too-long-17", "abcdefghijklmnopq", false},
		{"uppercase", "Alice", false},
		{"leading-hyphen", "-alice", false},
		{"double-underscore", "ali__ce", false},
		{"single-underscore", "ali_ce", false},
		{"slash", "ali/ce", false},
		{"dotdot", "..", false},
		{"space", "ali ce", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Validate(tt.id); got != tt.ok {
				t.Fatalf("Validate(%q) = %v, want %v", tt.id, got, tt.ok)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/instanceid/ -run TestValidate -v`
Expected: FAIL — build error `undefined: Validate` (package `instanceid` does not yet exist).

- [ ] **Step 3: Write minimal implementation**

Create `internal/instanceid/instanceid.go`:

```go
// Package instanceid resolves and validates the per-daemon instance identifier
// used to namespace global resources (git branches, worktree paths, Discord
// titles) so multiple dctl daemons can share one Discord home.
package instanceid

import "regexp"

// idRe is the strict slug accepted as an instanceID: lowercase alnum start,
// then lowercase alnum or '-', total length 1..16. No '_' (so the '__'
// title separator can never appear inside an id), no '/' and no '.'.
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,15}$`)

// Validate reports whether id is a well-formed instanceID slug.
func Validate(id string) bool {
	return idRe.MatchString(id)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/instanceid/ -run TestValidate -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/instanceid/instanceid.go internal/instanceid/instanceid_test.go
git commit -m "feat(instanceid): add instanceID slug validation"
```

---

## Task 2: Slugify a snowflake owner id into an instanceID

**Files:**
- Modify: `internal/instanceid/instanceid.go`
- Test: `internal/instanceid/instanceid_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/instanceid/instanceid_test.go`:

```go
func TestSlugify(t *testing.T) {
	tests := []struct {
		name  string
		owner string
		want  string
	}{
		{"snowflake-18-digits", "343535234303787009", "u03787009"},
		{"short-snowflake-5", "12345", "u12345"},
		{"exactly-8", "12345678", "u12345678"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Slugify(tt.owner)
			if got != tt.want {
				t.Fatalf("Slugify(%q) = %q, want %q", tt.owner, got, tt.want)
			}
			if got != "" && !Validate(got) {
				t.Fatalf("Slugify(%q) = %q which fails Validate", tt.owner, got)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/instanceid/ -run TestSlugify -v`
Expected: FAIL — `undefined: Slugify`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/instanceid/instanceid.go`:

```go
// Slugify derives a short instanceID from a Discord owner snowflake. It returns
// "u" + the last up-to-8 characters of owner, which keeps the result <=9 chars
// and within the validation regex. An empty owner yields an empty string.
func Slugify(owner string) string {
	if owner == "" {
		return ""
	}
	if len(owner) > 8 {
		owner = owner[len(owner)-8:]
	}
	return "u" + owner
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/instanceid/ -run TestSlugify -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/instanceid/instanceid.go internal/instanceid/instanceid_test.go
git commit -m "feat(instanceid): derive instanceID slug from owner snowflake"
```

---

## Task 3: Resolve instanceID from explicit id → owner → legacy

**Files:**
- Modify: `internal/instanceid/instanceid.go`
- Test: `internal/instanceid/instanceid_test.go`

Resolution order (Spec §2): explicit `DCTL_INSTANCE_ID` (must validate, else error), else slugified `DCTL_OWNER_ID`, else empty (legacy). `Resolve` is pure: callers pass the two raw strings.

- [ ] **Step 1: Write the failing test**

Append to `internal/instanceid/instanceid_test.go`:

```go
func TestResolve(t *testing.T) {
	tests := []struct {
		name     string
		explicit string
		owner    string
		want     string
		wantErr  bool
	}{
		{"explicit-wins", "alice", "343535234303787009", "alice", false},
		{"explicit-invalid-errors", "Alice!", "12345678", "", true},
		{"derive-from-owner", "", "343535234303787009", "u03787009", false},
		{"legacy-no-inputs", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.explicit, tt.owner)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Resolve(%q,%q) err = %v, wantErr %v", tt.explicit, tt.owner, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("Resolve(%q,%q) = %q, want %q", tt.explicit, tt.owner, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/instanceid/ -run TestResolve -v`
Expected: FAIL — `undefined: Resolve`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/instanceid/instanceid.go` (add `"fmt"` to the import block):

```go
// Resolve computes the instanceID from an explicit id (e.g. DCTL_INSTANCE_ID)
// and a fallback owner snowflake (e.g. DCTL_OWNER_ID), per Spec §2:
//  1. explicit, when set, must be a valid slug or Resolve errors;
//  2. otherwise the owner is slugified;
//  3. otherwise "" (legacy, non-namespaced mode).
func Resolve(explicit, owner string) (string, error) {
	if explicit != "" {
		if !Validate(explicit) {
			return "", fmt.Errorf("invalid DCTL_INSTANCE_ID %q: want %s", explicit, idRe.String())
		}
		return explicit, nil
	}
	return Slugify(owner), nil
}
```

Update the import block in `internal/instanceid/instanceid.go` from:

```go
import "regexp"
```

to:

```go
import (
	"fmt"
	"regexp"
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/instanceid/ -v`
Expected: PASS (TestValidate, TestSlugify, TestResolve).

- [ ] **Step 5: Commit**

```bash
git add internal/instanceid/instanceid.go internal/instanceid/instanceid_test.go
git commit -m "feat(instanceid): resolve instanceID from explicit id or owner"
```

---

## Task 4: State.InstanceID field, QualifiedName, SetInstanceID

**Files:**
- Modify: `internal/state/state.go`
- Test: `internal/state/state_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/state/state_test.go`:

```go
func TestQualifiedName(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		logical    string
		want       string
	}{
		{"namespaced", "alice", "foo", "alice__foo"},
		{"legacy-empty-id", "", "foo", "foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewState(filepath.Join(t.TempDir(), "s.json"))
			s.InstanceID = tt.instanceID
			if got := s.QualifiedName(tt.logical); got != tt.want {
				t.Fatalf("QualifiedName(%q) with id %q = %q, want %q", tt.logical, tt.instanceID, got, tt.want)
			}
		})
	}
}

func TestSetInstanceIDPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	s := NewState(path)
	if err := s.SetInstanceID("alice"); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.InstanceID != "alice" {
		t.Fatalf("InstanceID not persisted: %q", got.InstanceID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/state/ -run 'TestQualifiedName|TestSetInstanceIDPersists' -v`
Expected: FAIL — `s.InstanceID undefined`, `s.QualifiedName undefined`, `s.SetInstanceID undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/state/state.go`, add the `InstanceID` field to `State`. Change:

```go
type State struct {
	mu              sync.Mutex `json:"-"`
	path            string     `json:"-"`
	Home            HomeRef    `json:"home"`
	Allow           []string   `json:"allow"`
	Repo            string     `json:"repo,omitempty"` // project sessions operate on; defaults to daemon cwd
	Sessions        []Session  `json:"sessions"`
	StatusMessageID string     `json:"statusMessageID,omitempty"` // cached id of the status embed
}
```

to:

```go
type State struct {
	mu              sync.Mutex `json:"-"`
	path            string     `json:"-"`
	Home            HomeRef    `json:"home"`
	Allow           []string   `json:"allow"`
	Repo            string     `json:"repo,omitempty"` // project sessions operate on; defaults to daemon cwd
	Sessions        []Session  `json:"sessions"`
	StatusMessageID string     `json:"statusMessageID,omitempty"` // cached id of the status embed
	InstanceID      string     `json:"instanceID,omitempty"`      // per-daemon namespace for global resources; "" = legacy
}
```

Then append these two methods at the end of `internal/state/state.go`:

```go
// QualifiedName maps a logical session name to the name used on global resources
// (Discord title): "<InstanceID>__<name>". In legacy mode (empty InstanceID) it
// returns the bare logical name, preserving pre-namespacing behavior.
func (s *State) QualifiedName(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.InstanceID == "" {
		return name
	}
	return s.InstanceID + "__" + name
}

// SetInstanceID records the per-daemon instance id and persists. The id is meant
// to be frozen after first resolution; callers enforce that invariant.
func (s *State) SetInstanceID(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.InstanceID = id
	return s.saveLocked()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/state/ -v`
Expected: PASS (existing tests plus TestQualifiedName, TestSetInstanceIDPersists).

- [ ] **Step 5: Commit**

```bash
git add internal/state/state.go internal/state/state_test.go
git commit -m "feat(state): add InstanceID, QualifiedName and SetInstanceID"
```

---

## Task 5: Namespaced worktree path and branch

**Files:**
- Modify: `internal/worktree/worktree.go`
- Test: `internal/worktree/worktree_test.go` (create)

`NewWorktreer` gains an `instanceID` parameter. `path` and the branch become namespaced; empty id reproduces today's exact paths/branch (legacy). We expose an unexported-but-testable seam by adding an exported `Branch(name)` method alongside the existing `path` (which we expose as `Path(name)` to test it without invoking git).

- [ ] **Step 1: Write the failing test**

Create `internal/worktree/worktree_test.go`:

```go
package worktree

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPathAndBranch(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		session    string
		wantPath   string
		wantBranch string
	}{
		{
			name:       "namespaced",
			instanceID: "alice",
			session:    "foo",
			wantPath:   filepath.Join("/repo", ".dctl-sessions", "alice", "foo"),
			wantBranch: "session/alice/foo",
		},
		{
			name:       "legacy-empty-id",
			instanceID: "",
			session:    "foo",
			wantPath:   filepath.Join("/repo", ".dctl-sessions", "foo"),
			wantBranch: "session/foo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWorktreer(context.Background(), "/repo", tt.instanceID)
			if got := w.Path(tt.session); got != tt.wantPath {
				t.Fatalf("Path = %q, want %q", got, tt.wantPath)
			}
			if got := w.Branch(tt.session); got != tt.wantBranch {
				t.Fatalf("Branch = %q, want %q", got, tt.wantBranch)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worktree/ -run TestPathAndBranch -v`
Expected: FAIL — `too many arguments in call to NewWorktreer`, `w.Path undefined`, `w.Branch undefined`.

- [ ] **Step 3: Write minimal implementation**

Replace the top of `internal/worktree/worktree.go` (struct + constructor + `path`) and the two git call sites.

Change the struct and constructor:

```go
// Worktreer manages git worktrees rooted at repo. Implements dctl.worktrees.
// Worktrees live under <repo>/.dctl-sessions/<name> on branch session/<name>.
type Worktreer struct {
	ctx  context.Context
	repo string
}

// NewWorktreer builds a Worktreer for the project at repo.
func NewWorktreer(ctx context.Context, repo string) *Worktreer {
	return &Worktreer{ctx: ctx, repo: repo}
}
```

to:

```go
// Worktreer manages git worktrees rooted at repo. Implements dctl.worktrees.
// With a non-empty instanceID, worktrees live under
// <repo>/.dctl-sessions/<instanceID>/<name> on branch session/<instanceID>/<name>;
// with an empty instanceID (legacy) under <repo>/.dctl-sessions/<name> on
// branch session/<name>, so multiple daemons sharing a repo never collide.
type Worktreer struct {
	ctx        context.Context
	repo       string
	instanceID string
}

// NewWorktreer builds a Worktreer for the project at repo, namespaced by
// instanceID ("" means legacy, non-namespaced layout).
func NewWorktreer(ctx context.Context, repo, instanceID string) *Worktreer {
	return &Worktreer{ctx: ctx, repo: repo, instanceID: instanceID}
}
```

Replace the `path` method:

```go
func (w *Worktreer) path(name string) string {
	return filepath.Join(w.repo, ".dctl-sessions", name)
}
```

with the namespaced `Path` and a `Branch` helper:

```go
// Path returns the on-disk worktree directory for a logical session name,
// namespaced by instanceID when set.
func (w *Worktreer) Path(name string) string {
	if w.instanceID == "" {
		return filepath.Join(w.repo, ".dctl-sessions", name)
	}
	return filepath.Join(w.repo, ".dctl-sessions", w.instanceID, name)
}

// Branch returns the git branch backing a logical session name, namespaced by
// instanceID when set.
func (w *Worktreer) Branch(name string) string {
	if w.instanceID == "" {
		return "session/" + name
	}
	return "session/" + w.instanceID + "/" + name
}
```

Update `Create` to use `Path`/`Branch`. Change:

```go
	p := w.path(name)
	out, err := exec.CommandContext(w.ctx, "git", "-C", w.repo,
		"worktree", "add", p, "-b", "session/"+name).CombinedOutput()
```

to:

```go
	p := w.Path(name)
	out, err := exec.CommandContext(w.ctx, "git", "-C", w.repo,
		"worktree", "add", p, "-b", w.Branch(name)).CombinedOutput()
```

Update `Remove`'s first line. Change:

```go
	p := w.path(name)
```

to:

```go
	p := w.Path(name)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/worktree/ -run TestPathAndBranch -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/worktree.go internal/worktree/worktree_test.go
git commit -m "feat(worktree): namespace worktree path and branch by instanceID"
```

---

## Task 6: Fix serve.go to pass instanceID to the worktreer (compile gate)

**Files:**
- Modify: `internal/serve/serve.go`

Task 5 changed `NewWorktreer`'s signature, so `internal/serve/serve.go` no longer compiles. This task restores compilation with a minimal call; full resolution/freezing lands in Task 7. (We keep the build green between commits.)

- [ ] **Step 1: Verify the build is currently broken**

Run: `go build ./...`
Expected: FAIL — `not enough arguments in call to worktree.NewWorktreer` at `internal/serve/serve.go`.

- [ ] **Step 2: Apply the minimal compile fix**

In `internal/serve/serve.go`, change:

```go
	wt := worktree.NewWorktreer(ctx, repo)
```

to:

```go
	wt := worktree.NewWorktreer(ctx, repo, st.InstanceID)
```

- [ ] **Step 3: Verify the build passes**

Run: `go build ./... && go test ./... `
Expected: build OK; all existing tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/serve/serve.go
git commit -m "fix(serve): pass instanceID to worktreer constructor"
```

---

## Task 7: Resolve and freeze instanceID at serve startup

**Files:**
- Modify: `internal/serve/serve.go`
- Test: `internal/serve/serve_test.go` (create)

Add `Options.InstanceID` (from the `-instance` flag / `DCTL_INSTANCE_ID`). At startup, resolve via `instanceid.Resolve(o.InstanceID, DCTL_OWNER_ID)`. Freeze logic (Spec §2/§8):
- if state already has an id and the resolved id differs and is non-empty → refuse to start;
- if state has no id and resolved id is non-empty and there are **no** sessions → persist (freeze) it;
- if state has no id but sessions exist → stay legacy (keep empty), even if a resolved id is available, so existing sessions are never orphaned.

Extract this into a pure, testable `resolveInstanceID(st, optID, ownerID)` function returning `(effectiveID string, err error)` and persisting via `st.SetInstanceID` when it freezes.

- [ ] **Step 1: Write the failing test**

Create `internal/serve/serve_test.go`:

```go
package serve

import (
	"testing"

	"github.com/vskstudio/dctl/internal/state"
)

func TestResolveInstanceID(t *testing.T) {
	tests := []struct {
		name        string
		stateID     string
		hasSessions bool
		optID       string
		ownerID     string
		wantID      string
		wantFrozen  string // expected persisted State.InstanceID after the call
		wantErr     bool
	}{
		{
			name:       "fresh-state-freezes-explicit",
			optID:      "alice",
			wantID:     "alice",
			wantFrozen: "alice",
		},
		{
			name:       "fresh-state-derives-from-owner",
			ownerID:    "343535234303787009",
			wantID:     "u03787009",
			wantFrozen: "u03787009",
		},
		{
			name:       "no-inputs-stays-legacy",
			wantID:     "",
			wantFrozen: "",
		},
		{
			name:       "existing-id-matches",
			stateID:    "alice",
			optID:      "alice",
			wantID:     "alice",
			wantFrozen: "alice",
		},
		{
			name:       "existing-id-differs-errors",
			stateID:    "alice",
			optID:      "bob",
			wantErr:    true,
			wantFrozen: "alice", // unchanged
		},
		{
			name:        "legacy-sessions-block-new-id",
			hasSessions: true,
			optID:       "alice",
			wantID:      "",
			wantFrozen:  "", // not frozen, sessions preserved as legacy
		},
		{
			name:    "invalid-explicit-errors",
			optID:   "Bad!",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := state.NewState(t.TempDir() + "/s.json")
			st.InstanceID = tt.stateID
			if tt.hasSessions {
				if err := st.AddSession(state.Session{Name: "old", ChannelID: "c"}); err != nil {
					t.Fatal(err)
				}
			}
			got, err := resolveInstanceID(st, tt.optID, tt.ownerID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if st.InstanceID != tt.wantFrozen {
					t.Fatalf("state id mutated on error: got %q want %q", st.InstanceID, tt.wantFrozen)
				}
				return
			}
			if got != tt.wantID {
				t.Fatalf("effective id = %q, want %q", got, tt.wantID)
			}
			if st.InstanceID != tt.wantFrozen {
				t.Fatalf("frozen id = %q, want %q", st.InstanceID, tt.wantFrozen)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serve/ -run TestResolveInstanceID -v`
Expected: FAIL — `undefined: resolveInstanceID`.

- [ ] **Step 3: Write minimal implementation**

In `internal/serve/serve.go`, add `"github.com/vskstudio/dctl/internal/instanceid"` to the import block (keep imports sorted within their group):

```go
	"github.com/vskstudio/dctl/internal/handler"
	"github.com/vskstudio/dctl/internal/health"
	"github.com/vskstudio/dctl/internal/instanceid"
	"github.com/vskstudio/dctl/internal/state"
```

Add `InstanceID` to `Options`. Change:

```go
type Options struct {
	StatePath     string
	DefaultCmd    string
	HealthAddr    string
	StatusChannel string
	// Token is the bot token used for the gateway IDENTIFY (same value the
	// client was built with). Sourced from the caller, not read off the client.
	Token string
}
```

to:

```go
type Options struct {
	StatePath     string
	DefaultCmd    string
	HealthAddr    string
	StatusChannel string
	// InstanceID is the explicit per-daemon namespace (-instance flag /
	// DCTL_INSTANCE_ID). Empty falls back to DCTL_OWNER_ID, then legacy mode.
	InstanceID string
	// Token is the bot token used for the gateway IDENTIFY (same value the
	// client was built with). Sourced from the caller, not read off the client.
	Token string
}
```

Add the resolver function (place it just above `func Run`):

```go
// resolveInstanceID computes and freezes the daemon's instanceID, per Spec §2/§8.
//   - An invalid explicit optID is an error.
//   - If the state already carries an id, a different non-empty resolved id is
//     refused (changing it would orphan existing branches/worktrees); a matching
//     or empty resolved id keeps the stored id.
//   - On a fresh state (no id) with a non-empty resolved id and NO sessions, the
//     id is frozen (persisted). If sessions already exist, the daemon stays in
//     legacy (empty) mode so pre-existing sessions are never orphaned.
func resolveInstanceID(st *state.State, optID, ownerID string) (string, error) {
	resolved, err := instanceid.Resolve(optID, ownerID)
	if err != nil {
		return "", err
	}
	if st.InstanceID != "" {
		if resolved != "" && resolved != st.InstanceID {
			return "", fmt.Errorf("instanceID mismatch: state has %q but %q was requested; "+
				"changing it would orphan existing sessions", st.InstanceID, resolved)
		}
		return st.InstanceID, nil
	}
	if resolved == "" {
		return "", nil
	}
	if len(st.SnapshotSessions()) > 0 {
		// Legacy sessions exist; stay non-namespaced so they keep working.
		fmt.Fprintf(os.Stderr, "dctl serve: %d legacy session(s) present; staying in non-namespaced mode\n",
			len(st.SnapshotSessions()))
		return "", nil
	}
	if err := st.SetInstanceID(resolved); err != nil {
		return "", fmt.Errorf("persist instanceID: %w", err)
	}
	return resolved, nil
}
```

Now wire it into `Run`. Change:

```go
	repo := st.Repo
	if repo == "" {
		repo, _ = os.Getwd()
	}
	wt := worktree.NewWorktreer(ctx, repo, st.InstanceID)
	hdl := handler.NewHandler(c, sup, wt, st, o.DefaultCmd)
```

to:

```go
	instID, err := resolveInstanceID(st, o.InstanceID, os.Getenv("DCTL_OWNER_ID"))
	if err != nil {
		return fmt.Errorf("resolve instance id: %w", err)
	}
	if instID != "" {
		fmt.Fprintf(os.Stderr, "dctl serve: instance %q\n", instID)
	}

	repo := st.Repo
	if repo == "" {
		repo, _ = os.Getwd()
	}
	wt := worktree.NewWorktreer(ctx, repo, instID)
	hdl := handler.NewHandler(c, sup, wt, st, o.DefaultCmd)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/serve/ -v && go build ./...`
Expected: PASS; build OK.

- [ ] **Step 5: Commit**

```bash
git add internal/serve/serve.go internal/serve/serve_test.go
git commit -m "feat(serve): resolve and freeze instanceID at startup"
```

---

## Task 8: Wire the -instance flag in the CLI

**Files:**
- Modify: `cmd/dctl/serve.go`

The `cmd` package has no unit tests today; this is a flag-plumbing change verified by build + manual `-h`. Default reads `DCTL_INSTANCE_ID` so env and flag stay consistent.

- [ ] **Step 1: Verify the flag is absent**

Run: `go run ./cmd/dctl serve -h 2>&1 | grep -c instance`
Expected: `0` (no `-instance` flag yet).

- [ ] **Step 2: Add the flag and pass it through**

In `cmd/dctl/serve.go`, add `"os"` to the import block:

```go
import (
	"context"
	"flag"
	"os"

	"github.com/vskstudio/dctl"
	"github.com/vskstudio/dctl/internal/serve"
)
```

Add the flag after `statusChannel`. Change:

```go
	statusChannel := fs.String("status-channel", "", "if set, maintain a self-updating status embed there")
	fs.Parse(args)
```

to:

```go
	statusChannel := fs.String("status-channel", "", "if set, maintain a self-updating status embed there")
	instanceID := fs.String("instance", os.Getenv("DCTL_INSTANCE_ID"), "per-daemon instance id (slug) used to namespace shared Discord/git resources; defaults to DCTL_INSTANCE_ID")
	fs.Parse(args)
```

Pass it into `Options`. Change:

```go
	return serve.Run(ctx, c, serve.Options{
		StatePath:     *statePath,
		DefaultCmd:    *defaultCmd,
		HealthAddr:    *healthAddr,
		StatusChannel: *statusChannel,
		Token:         token,
	})
```

to:

```go
	return serve.Run(ctx, c, serve.Options{
		StatePath:     *statePath,
		DefaultCmd:    *defaultCmd,
		HealthAddr:    *healthAddr,
		StatusChannel: *statusChannel,
		InstanceID:    *instanceID,
		Token:         token,
	})
```

- [ ] **Step 3: Verify the flag is present and build passes**

Run: `go build ./... && go run ./cmd/dctl serve -h 2>&1 | grep -c instance`
Expected: build OK; grep count `1`.

- [ ] **Step 4: Commit**

```bash
git add cmd/dctl/serve.go
git commit -m "feat(cli): add -instance flag to dctl serve"
```

---

## Task 9: Discord title uses the qualified name

**Files:**
- Modify: `internal/handler/handler.go`
- Test: `internal/handler/handler_test.go`

The logical name stays the state key and the worktree/supervisor key (Spec §3, §4.3). Only the **title** passed to `CreateChannelUnder` / `ForumPost` becomes `st.QualifiedName(name)`. We set the handler's state `InstanceID` in the test to observe the change.

- [ ] **Step 1: Write the failing test**

Append to `internal/handler/handler_test.go`:

```go
func TestSessionCreateUsesQualifiedTitle(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		homeType   int
		setHome    state.HomeRef
		logical    string
		wantTitle  string
	}{
		{
			name:       "category-namespaced",
			instanceID: "alice",
			homeType:   dctl.ChannelText,
			setHome:    state.HomeRef{ID: "cat1", Type: "category"},
			logical:    "foo",
			wantTitle:  "alice__foo",
		},
		{
			name:       "forum-namespaced",
			instanceID: "bob",
			homeType:   dctl.ChannelForum,
			setHome:    state.HomeRef{ID: "f1", Type: "forum"},
			logical:    "foo",
			wantTitle:  "forum:bob__foo",
		},
		{
			name:       "category-legacy",
			instanceID: "",
			homeType:   dctl.ChannelText,
			setHome:    state.HomeRef{ID: "cat1", Type: "category"},
			logical:    "foo",
			wantTitle:  "foo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d, _, _, st := newTestHandler(t, tt.homeType)
			st.InstanceID = tt.instanceID
			st.SetHome(tt.setHome)
			h.Handle(context.Background(), it("owner", "session", "create",
				dctl.InteractionOption{Name: "name", Value: tt.logical}))
			if len(d.created) != 1 || d.created[0] != tt.wantTitle {
				t.Fatalf("created titles = %+v, want [%q]", d.created, tt.wantTitle)
			}
			// State key stays the logical name.
			if _, ok := st.FindSession(tt.logical); !ok {
				t.Fatalf("session must be keyed by logical name %q", tt.logical)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/handler/ -run TestSessionCreateUsesQualifiedTitle -v`
Expected: FAIL — created titles are the bare logical name (`foo` / `forum:foo`), not the qualified ones.

- [ ] **Step 3: Write minimal implementation**

In `internal/handler/handler.go`, inside `sessionCreate`, compute the qualified title once before the `switch home.Type` and use it for both Discord calls. Change:

```go
	var sess state.Session
	switch home.Type {
	case "category":
		ch, err := h.d.CreateChannelUnder(ctx, home.ID, name)
		if err != nil {
			_ = h.wt.Remove(name, true) // roll back the worktree we just made
			return errf("create channel: %v", err)
		}
		sess = state.Session{Name: name, ChannelID: ch.ID, Type: "text", Cmd: cmd, Worktree: worktree}
	case "forum":
		ch, err := h.d.ForumPost(ctx, home.ID, name, "Session **"+name+"** started.")
		if err != nil {
			_ = h.wt.Remove(name, true)
			return errf("create forum post: %v", err)
		}
		sess = state.Session{Name: name, ChannelID: ch.ID, Type: "forum", Cmd: cmd, Worktree: worktree}
	default:
		return errf("home type %q unsupported", home.Type)
	}
```

to:

```go
	// Logical name stays the state/worktree key; the qualified name namespaces
	// the Discord title so daemons sharing a home stay distinguishable (Spec §3).
	title := h.st.QualifiedName(name)
	var sess state.Session
	switch home.Type {
	case "category":
		ch, err := h.d.CreateChannelUnder(ctx, home.ID, title)
		if err != nil {
			_ = h.wt.Remove(name, true) // roll back the worktree we just made
			return errf("create channel: %v", err)
		}
		sess = state.Session{Name: name, ChannelID: ch.ID, Type: "text", Cmd: cmd, Worktree: worktree}
	case "forum":
		ch, err := h.d.ForumPost(ctx, home.ID, title, "Session **"+title+"** started.")
		if err != nil {
			_ = h.wt.Remove(name, true)
			return errf("create forum post: %v", err)
		}
		sess = state.Session{Name: name, ChannelID: ch.ID, Type: "forum", Cmd: cmd, Worktree: worktree}
	default:
		return errf("home type %q unsupported", home.Type)
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/handler/ -v`
Expected: PASS (all existing handler tests, which use empty `InstanceID` and thus bare titles, plus the new test).

- [ ] **Step 5: Commit**

```bash
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "feat(handler): namespace Discord title with qualified name"
```

---

## Task 10: Ownership invariant on close (regression test)

**Files:**
- Test: `internal/handler/handler_test.go`

Spec §7 / criterion 2: a daemon can only close what is in **its own** state, acting on a locally stored `ChannelID`. No code change is required (the invariant already holds because `sessionClose` resolves through `FindSession` and `ArchiveChannel(sess.ChannelID)`); we lock it with a test so future changes can't regress it.

- [ ] **Step 1: Write the failing-by-construction test**

Append to `internal/handler/handler_test.go`:

```go
func TestSessionCloseOnlyTouchesOwnSession(t *testing.T) {
	// Two instances each own a logically-identical "foo" with distinct channel
	// ids. Closing on instance "bob" must archive only bob's channel.
	h, d, sup, wt, st := newTestHandler(t, dctl.ChannelText)
	st.InstanceID = "bob"
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	// bob's own session, keyed by logical name, pointing at bob's channel.
	st.AddSession(state.Session{Name: "foo", ChannelID: "bob-foo-ch", Type: "text", Worktree: "/wt/bob"})

	h.Handle(context.Background(), it("owner", "session", "close",
		dctl.InteractionOption{Name: "name", Value: "foo"}))

	if len(d.archived) != 1 || d.archived[0] != "bob-foo-ch" {
		t.Fatalf("close must archive only bob's channel, got %+v", d.archived)
	}
	if len(sup.stopped) != 1 || sup.stopped[0] != "foo" {
		t.Fatalf("close must stop only the local logical session, got %+v", sup.stopped)
	}
	if len(wt.removed) != 1 {
		t.Fatalf("close must remove exactly bob's worktree, got %+v", wt.removed)
	}
	if _, ok := st.FindSession("foo"); ok {
		t.Fatal("bob's foo should be gone after close")
	}
}

func TestSessionCloseUnknownNameIsNoop(t *testing.T) {
	// Closing a name absent from this instance's state touches nothing — it
	// cannot reach another instance's resources.
	h, d, sup, _, st := newTestHandler(t, dctl.ChannelText)
	st.InstanceID = "bob"
	r := h.Handle(context.Background(), it("owner", "session", "close",
		dctl.InteractionOption{Name: "name", Value: "alice-only"}))
	if !r.Ephemeral {
		t.Fatal("expected ephemeral error for unknown session")
	}
	if len(d.archived) != 0 || len(sup.stopped) != 0 {
		t.Fatalf("unknown close must be a no-op, got archived=%+v stopped=%+v", d.archived, sup.stopped)
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/handler/ -run 'TestSessionCloseOnlyTouchesOwnSession|TestSessionCloseUnknownNameIsNoop' -v`
Expected: PASS immediately (the invariant already holds; these tests document and lock it).

> If either test fails, do NOT weaken the test — the invariant is the contract; use superpowers:systematic-debugging on `sessionClose`.

- [ ] **Step 3: Commit**

```bash
git add internal/handler/handler_test.go
git commit -m "test(handler): lock ownership invariant on session close"
```

---

## Task 11: Prefix the status embed title with the instanceID

**Files:**
- Modify: `internal/serve/loops.go`
- Modify: `internal/serve/serve.go` (pass instance id into `statusLoop`)
- Test: `internal/serve/loops_test.go` (create)

Spec §6: the status embed must carry the instance id so two daemons posting to the same `StatusChannel` are distinguishable. Extract the content rendering into a pure `statusContent(instanceID string, snap health.Snapshot) string` and test it; `statusLoop` calls it.

- [ ] **Step 1: Write the failing test**

Create `internal/serve/loops_test.go`:

```go
package serve

import (
	"strings"
	"testing"

	"github.com/vskstudio/dctl/internal/health"
)

func TestStatusContent(t *testing.T) {
	tests := []struct {
		name        string
		instanceID  string
		online      bool
		wantSubstr  string
		wantNoSubstr string
	}{
		{
			name:       "online-namespaced",
			instanceID: "alice",
			online:     true,
			wantSubstr: "[alice]",
		},
		{
			name:        "online-legacy-no-prefix",
			instanceID:  "",
			online:      true,
			wantSubstr:  "online",
			wantNoSubstr: "[]",
		},
		{
			name:       "offline-namespaced",
			instanceID: "bob",
			online:     false,
			wantSubstr: "[bob]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := health.Snapshot{Online: tt.online, UptimeS: 5, PingMS: 12, Sessions: 2}
			got := statusContent(tt.instanceID, snap)
			if !strings.Contains(got, tt.wantSubstr) {
				t.Fatalf("statusContent = %q, want substring %q", got, tt.wantSubstr)
			}
			if tt.wantNoSubstr != "" && strings.Contains(got, tt.wantNoSubstr) {
				t.Fatalf("statusContent = %q, must not contain %q", got, tt.wantNoSubstr)
			}
		})
	}
}
```

> Confirm the field names on `health.Snapshot` (`Online`, `UptimeS`, `PingMS`, `Sessions`) match `internal/health/health.go` before running; they are taken from the existing `render` closure in `loops.go`. If a field name differs, adjust the literal accordingly — do not invent fields.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serve/ -run TestStatusContent -v`
Expected: FAIL — `undefined: statusContent`.

- [ ] **Step 3: Write minimal implementation**

In `internal/serve/loops.go`, extract a pure renderer and have `statusLoop` use it. Change the `statusLoop` signature and `render` closure. Replace:

```go
// statusLoop maintains a single self-updating status embed in channelID.
func statusLoop(ctx context.Context, c *dctl.Client, st *state.State, h *health.Health, channelID string) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	render := func() {
		snap := h.Snapshot(time.Now(), healthWindow)
		dot, word := "🟢", "online"
		if !snap.Online {
			dot, word = "🔴", "offline"
		}
		uptime := (time.Duration(snap.UptimeS) * time.Second).String()
		content := fmt.Sprintf("%s **dctl %s** · uptime %s · ping %dms · %d sessions",
			dot, word, uptime, snap.PingMS, snap.Sessions)
		id, err := c.UpsertStatusMessage(ctx, channelID, st.StatusMessageID, content)
		if err == nil && id != st.StatusMessageID {
			_ = st.SetStatusMessageID(id)
		}
	}
```

with:

```go
// statusContent renders the status embed body. When instanceID is non-empty it
// is prefixed as "[instanceID] " so daemons sharing a status channel are
// distinguishable (Spec §6).
func statusContent(instanceID string, snap health.Snapshot) string {
	dot, word := "🟢", "online"
	if !snap.Online {
		dot, word = "🔴", "offline"
	}
	uptime := (time.Duration(snap.UptimeS) * time.Second).String()
	prefix := ""
	if instanceID != "" {
		prefix = "[" + instanceID + "] "
	}
	return fmt.Sprintf("%s%s **dctl %s** · uptime %s · ping %dms · %d sessions",
		prefix, dot, word, uptime, snap.PingMS, snap.Sessions)
}

// statusLoop maintains a single self-updating status embed in channelID.
func statusLoop(ctx context.Context, c *dctl.Client, st *state.State, h *health.Health, channelID, instanceID string) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	render := func() {
		snap := h.Snapshot(time.Now(), healthWindow)
		content := statusContent(instanceID, snap)
		id, err := c.UpsertStatusMessage(ctx, channelID, st.StatusMessageID, content)
		if err == nil && id != st.StatusMessageID {
			_ = st.SetStatusMessageID(id)
		}
	}
```

In `internal/serve/serve.go`, update the `statusLoop` call to pass the resolved `instID`. Change:

```go
	if o.StatusChannel != "" {
		go statusLoop(ctx, c, st, h, o.StatusChannel)
	}
```

to:

```go
	if o.StatusChannel != "" {
		go statusLoop(ctx, c, st, h, o.StatusChannel, instID)
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/serve/ -v && go build ./...`
Expected: PASS; build OK.

- [ ] **Step 5: Commit**

```bash
git add internal/serve/loops.go internal/serve/serve.go internal/serve/loops_test.go
git commit -m "feat(serve): prefix status embed with instanceID"
```

---

## Task 12: Full-suite green + vet

**Files:** none (verification gate).

- [ ] **Step 1: Run the entire suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build OK; vet clean; all packages PASS (`internal/instanceid`, `internal/state`, `internal/worktree`, `internal/serve`, `internal/handler`, and untouched packages).

- [ ] **Step 2: Commit (only if vet/format produced changes)**

```bash
gofmt -w ./internal ./cmd
git add -A
git commit -m "chore: gofmt after multi-instance isolation" || echo "nothing to commit"
```

---

## Self-Review

**1. Spec coverage**

| Spec section | Requirement | Task |
|---|---|---|
| §2 identity (env → owner → legacy) | `Resolve` order, slug validation | 1, 2, 3 |
| §2 persisted + frozen, mismatch refused | `SetInstanceID`, `resolveInstanceID` freeze/mismatch | 4, 7 |
| §3 naming: branch `session/<id>/<name>` | `Worktreer.Branch` | 5 |
| §3 naming: worktree `.dctl-sessions/<id>/<name>` | `Worktreer.Path` | 5 |
| §3 naming: Discord title `<id>__<name>` | `QualifiedName` + handler title | 4, 9 |
| §3 state key unchanged (logical name) | handler keeps `Name: name` | 9, 10 |
| §3 `instanceIDRe` distinct/strict | `idRe` in instanceid pkg | 1 |
| §4.1 `State.InstanceID`, `QualifiedName`, AddSession unchanged | field + helper, AddSession untouched | 4 |
| §4.2 `NewWorktreer(ctx,repo,instanceID)`, legacy when empty | constructor + empty-id branches | 5 |
| §4.3 handler title qualified, unicity guard unchanged | title swap only | 9 |
| §4.4 serve resolves/persists/validates, builds worktreer | `resolveInstanceID`, wiring | 7 |
| §4.5 supervisor unchanged | not modified (confirmed: keyed on logical `sess.Name`) | — |
| §6 `/session list` shows logical name | unchanged (already logical) | — (no-op, correct) |
| §6 status embed prefixed by instanceID | `statusContent` | 11 |
| §7 ownership invariant + test | regression tests | 10 |
| §8 migration: legacy sessions never broken | sessions-present → stay legacy | 7 |
| Criteria 1–6 | covered by Tasks 5/7/9/10/11 tests | — |

No spec requirement is left without a task. §4.5 (supervisor) and the `/session list` logical-name display are intentional no-ops — verified against the current code (`supervisor.go` keys `cancels` on `sess.Name`; `sessionList` already prints `s.Name`).

**2. Placeholder scan:** No "TBD"/"add error handling"/"similar to Task N". Every code step contains complete, copy-pasteable Go. Two notes ("confirm field names on `health.Snapshot`", "confirm DCTL_OWNER_ID") are verification reminders, not deferred work — the code is fully written.

**3. Type consistency check:**
- `instanceid.Resolve(explicit, owner string) (string, error)` — used identically in Task 7.
- `instanceid.Validate(string) bool`, `instanceid.Slugify(string) string` — consistent across Tasks 1–3, 7.
- `state.QualifiedName(name string) string` — defined Task 4, used Task 9.
- `state.SetInstanceID(id string) error` — defined Task 4, used Task 7.
- `worktree.NewWorktreer(ctx, repo, instanceID)` — signature changed in Task 5, every caller (serve.go Tasks 6/7, test Task 5) matches.
- `worktree.Path(name)` / `worktree.Branch(name)` — exported in Task 5, used only in tests/internally; `Create`/`Remove` updated to call them.
- `serve.Options.InstanceID` — added Task 7, populated Task 8.
- `serve.resolveInstanceID(st *state.State, optID, ownerID string) (string, error)` — defined and used Task 7.
- `serve.statusContent(instanceID string, snap health.Snapshot) string` and `statusLoop(..., channelID, instanceID string)` — defined Task 11, call site updated in same task.

All signatures align across tasks. Build is kept green at every commit (Task 6 is an explicit compile-gate between the signature change in Task 5 and the full wiring in Task 7).
