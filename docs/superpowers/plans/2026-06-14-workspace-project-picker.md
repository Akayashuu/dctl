# Workspace + Project Picker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make dctl operate over a configurable workspace root of many projects: pick a project (or clone one via gh/glab) at `/session create`, creating the worktree inside that project instead of one global repo.

**Architecture:** `Worktreer` becomes repo-stateless — `Create(repo, name)` / `Remove(repo, name, force)` run `git -C <repo>`. The handler resolves `repo = filepath.Join(workspace, project)` per call. A new dependency-free `internal/forge` package wraps `gh`/`glab` via an injectable `runner` seam (so `exec` is faked in tests, exactly like `internal/handler` fakes `discord`). State gains `Workspace` and `Session.Project`.

**Tech Stack:** Go 1.23, stdlib only (`os/exec`, `encoding/json`, `path/filepath`, `regexp`). Module `github.com/vskstudio/dctl`.

**This is Spec 2/4.** Global implementation order is 3 → 2 → 1 → 4. This plan assumes **Spec 3 (multi-instance isolation) already landed**: `instanceID`/namespacing exists where relevant. Spec 3 already started the worktree refactor, so where this plan changes `Worktreer`/`worktrees`, apply on top of whatever Spec 3 left — if `Worktreer` is already stateless after Spec 3, skip Task 2's struct change and keep only the signature/test deltas.

---

## File Structure

- `internal/state/state.go` — **modify**. Add `State.Workspace`, `Session.Project`, accessors `SetWorkspace`, `WorkspaceRoot`. One responsibility: persisted config.
- `internal/worktree/worktree.go` — **modify**. Drop the stored `repo` field; `Create`/`Remove`/`path`/`isGitRepo` take `repo string`. One responsibility: per-repo git worktree lifecycle.
- `internal/forge/forge.go` — **create**. `Available`, `List`, `Clone`, `Repo`, and a `runner` seam. One responsibility: gh/glab shelling.
- `internal/forge/forge_test.go` — **create**. Fake runner, table tests.
- `internal/handler/handler.go` — **modify**. New `worktrees` signature; `handleSet` `case "workspace"`; `sessionCreate`/`sessionClose` resolve repo from workspace+project; new `handleWorkspace` (list/remotes); `forge` injected behind an interface.
- `internal/handler/handler_test.go` — **modify**. Update `fakeWT` to the new signature; add `fakeForge`; new tests for project resolution, path-escape, workspace set, remotes-absent.
- `interactions.go` — **modify**. `/set workspace`; `create` `project:`/`clone:`; new `/workspace` command (`list`, `remotes` with `forge` choice).
- `internal/serve/serve.go` — **modify**. Build the stateless `Worktreer`; build a `forge.Lister`; pass both to `NewHandler`.

Note on existing helpers: `InteractionData` exposes `Opt(name) (string,bool)` and `OptBool(name) bool` (interactions.go:44/63). The spec mentions `OptString` — that does **not** exist; use `Opt`. There is no project-slug helper yet; this plan adds `projectRe` in handler.go mirroring `sessionNameRe`.

---

## Task 1: State — Workspace + Session.Project + accessors

**Files:**
- Modify: `internal/state/state.go`
- Test: `internal/state/state_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Append to `internal/state/state_test.go` (create the file with this content if it does not exist):

```go
package state

import "testing"

func TestSetWorkspacePersists(t *testing.T) {
	p := t.TempDir() + "/s.json"
	s := NewState(p)
	if err := s.SetWorkspace("/home/u/dev"); err != nil {
		t.Fatalf("SetWorkspace: %v", err)
	}
	reloaded, err := LoadState(p)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if reloaded.Workspace != "/home/u/dev" {
		t.Fatalf("workspace not persisted: %q", reloaded.Workspace)
	}
}

func TestWorkspaceRootFallsBack(t *testing.T) {
	s := NewState(t.TempDir() + "/s.json")
	if got := s.WorkspaceRoot(); got != "" {
		t.Fatalf("empty state should give empty root, got %q", got)
	}
	s.Repo = "/legacy/repo"
	if got := s.WorkspaceRoot(); got != "/legacy/repo" {
		t.Fatalf("should fall back to Repo, got %q", got)
	}
	_ = s.SetWorkspace("/ws")
	if got := s.WorkspaceRoot(); got != "/ws" {
		t.Fatalf("Workspace should win, got %q", got)
	}
}

func TestSessionProjectRoundTrips(t *testing.T) {
	p := t.TempDir() + "/s.json"
	s := NewState(p)
	if err := s.AddSession(Session{Name: "x", ChannelID: "c", Project: "myproj"}); err != nil {
		t.Fatalf("AddSession: %v", err)
	}
	reloaded, _ := LoadState(p)
	got, ok := reloaded.FindSession("x")
	if !ok || got.Project != "myproj" {
		t.Fatalf("project not persisted: %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shan/dev/dctl && go test ./internal/state/`
Expected: FAIL — `reloaded.Workspace undefined`, `s.SetWorkspace undefined`, `s.WorkspaceRoot undefined`, `Session.Project undefined` (compile errors).

- [ ] **Step 3: Write minimal implementation**

In `internal/state/state.go`, add `Project` to `Session` (after the `Worktree` field):

```go
// Session is one bridged channel/post supervised by the daemon.
type Session struct {
	Name      string `json:"name"`
	ChannelID string `json:"channelID"`
	Type      string `json:"type"` // "text" | "forum"
	Cmd       string `json:"cmd"`
	Worktree  string `json:"worktree,omitempty"` // abs path; empty for a shared session
	Project   string `json:"project,omitempty"`  // workspace sub-dir the session started from
}
```

Add `Workspace` to `State` (after the `Repo` field):

```go
	Repo            string     `json:"repo,omitempty"`      // legacy single-repo root; defaults to daemon cwd
	Workspace       string     `json:"workspace,omitempty"` // abs path to the workspace root; preferred over Repo
```

Add the two accessors (after `SetHome`):

```go
// SetWorkspace records the workspace root and persists.
func (s *State) SetWorkspace(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Workspace = path
	return s.saveLocked()
}

// WorkspaceRoot returns the configured workspace, else the legacy Repo, else "".
func (s *State) WorkspaceRoot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Workspace != "" {
		return s.Workspace
	}
	return s.Repo
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/shan/dev/dctl && go test ./internal/state/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /home/shan/dev/dctl
git add internal/state/state.go internal/state/state_test.go
git commit -m "feat(state): add Workspace + Session.Project with accessors"
```

---

## Task 2: Worktreer becomes repo-stateless

This makes `git -C <repo>` use a per-call repo. (If Spec 3 already removed the `repo` field, only the signature/path-arg parts below remain — apply the deltas idempotently.)

**Files:**
- Modify: `internal/worktree/worktree.go`
- Test: `internal/worktree/worktree_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Create/append `internal/worktree/worktree_test.go`:

```go
package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo makes a real git repo with one commit, so worktree add works.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestCreateUsesPassedRepo(t *testing.T) {
	repo := initRepo(t)
	w := NewWorktreer(context.Background())
	path, err := w.Create(repo, "feat1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := filepath.Join(repo, ".dctl-sessions", "feat1")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("worktree dir missing: %v", err)
	}
}

func TestCreateNonGitRepoFallsBack(t *testing.T) {
	w := NewWorktreer(context.Background())
	path, err := w.Create(t.TempDir(), "feat1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if path != "" {
		t.Fatalf("non-git repo should yield empty path, got %q", path)
	}
}

func TestRemoveUsesPassedRepo(t *testing.T) {
	repo := initRepo(t)
	w := NewWorktreer(context.Background())
	if _, err := w.Create(repo, "feat1"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := w.Remove(repo, "feat1", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".dctl-sessions", "feat1")); !os.IsNotExist(err) {
		t.Fatalf("worktree should be gone, stat err = %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shan/dev/dctl && go test ./internal/worktree/`
Expected: FAIL — `NewWorktreer(context.Background())` has too few arguments; `w.Create(repo, "feat1")` too many arguments (compile errors).

- [ ] **Step 3: Write minimal implementation**

Replace the whole body of `internal/worktree/worktree.go` with:

```go
package worktree

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktreer manages git worktrees. It is stateless: the repo root is passed to
// each method, so one Worktreer serves every project in the workspace.
// Worktrees live under <repo>/.dctl-sessions/<name> on branch session/<name>.
type Worktreer struct {
	ctx context.Context
}

// NewWorktreer builds a stateless Worktreer.
func NewWorktreer(ctx context.Context) *Worktreer {
	return &Worktreer{ctx: ctx}
}

func (w *Worktreer) isGitRepo(repo string) bool {
	return exec.CommandContext(w.ctx, "git", "-C", repo, "rev-parse", "--git-dir").Run() == nil
}

func (w *Worktreer) path(repo, name string) string {
	return filepath.Join(repo, ".dctl-sessions", name)
}

// Create adds a worktree on branch session/<name> inside repo. Returns ("", nil)
// when repo is not a git repo (caller falls back to a shared session).
func (w *Worktreer) Create(repo, name string) (string, error) {
	if !w.isGitRepo(repo) {
		return "", nil
	}
	p := w.path(repo, name)
	out, err := exec.CommandContext(w.ctx, "git", "-C", repo,
		"worktree", "add", p, "-b", "session/"+name).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("worktree add: %s", strings.TrimSpace(string(out)))
	}
	return p, nil
}

// Remove removes the worktree. If it has uncommitted changes and !force, it
// refuses with the status. The branch session/<name> is always left intact.
func (w *Worktreer) Remove(repo, name string, force bool) error {
	p := w.path(repo, name)
	if !force {
		out, _ := exec.CommandContext(w.ctx, "git", "-C", p, "status", "--porcelain").Output()
		if strings.TrimSpace(string(out)) != "" {
			return fmt.Errorf("worktree %q has uncommitted changes", name)
		}
	}
	args := []string{"-C", repo, "worktree", "remove", p}
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

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/shan/dev/dctl && go test ./internal/worktree/`
Expected: PASS (3 tests). The handler/serve packages will not compile yet — that is fixed in Tasks 4 and 7.

- [ ] **Step 5: Commit**

```bash
cd /home/shan/dev/dctl
git add internal/worktree/worktree.go internal/worktree/worktree_test.go
git commit -m "refactor(worktree): make Worktreer stateless, repo passed per call"
```

---

## Task 3: forge package — Available, List, Clone behind a runner seam

`forge` shells `gh`/`glab`. To keep it testable like `handler` mocks `discord`, all exec goes through a `runner` interface; production uses `execRunner`, tests use a fake.

**Files:**
- Create: `internal/forge/forge.go`
- Test: `internal/forge/forge_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/forge/forge_test.go`:

```go
package forge

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner records calls and returns scripted output/errors keyed by argv[0].
type fakeRunner struct {
	calls   [][]string
	out     map[string][]byte // keyed by first arg (e.g. "gh", "glab", "git")
	err     map[string]error
	lookErr map[string]error // exec.LookPath result per binary
}

func (f *fakeRunner) look(name string) error {
	if f.lookErr == nil {
		return nil
	}
	return f.lookErr[name]
}

func (f *fakeRunner) run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.out[name], f.err[name]
}

func TestAvailableReportsBothAbsent(t *testing.T) {
	r := &fakeRunner{lookErr: map[string]error{"gh": errors.New("nope"), "glab": errors.New("nope")}}
	c := &Client{r: r}
	gh, gl := c.Available()
	if gh || gl {
		t.Fatalf("expected both absent, got gh=%v gl=%v", gh, gl)
	}
}

func TestListMergesGitHubOnly(t *testing.T) {
	r := &fakeRunner{
		lookErr: map[string]error{"glab": errors.New("nope")}, // only gh present
		out: map[string][]byte{
			"gh": []byte(`[{"nameWithOwner":"me/app","sshUrl":"git@github.com:me/app.git","description":"d"}]`),
		},
	}
	c := &Client{r: r}
	repos, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(repos) != 1 || repos[0].FullName != "me/app" || repos[0].Forge != "github" {
		t.Fatalf("unexpected repos: %+v", repos)
	}
}

func TestListEmptyWhenNoForge(t *testing.T) {
	r := &fakeRunner{lookErr: map[string]error{"gh": errors.New("x"), "glab": errors.New("x")}}
	c := &Client{r: r}
	repos, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("expected no repos, got %+v", repos)
	}
}

func TestCloneRejectsBadSpec(t *testing.T) {
	c := &Client{r: &fakeRunner{}}
	if _, err := c.Clone(context.Background(), "../evil", "/ws"); err == nil {
		t.Fatal("expected rejection of traversal spec")
	}
	if _, err := c.Clone(context.Background(), "a b; rm -rf", "/ws"); err == nil {
		t.Fatal("expected rejection of spec with shell metacharacters")
	}
}

func TestCloneOwnerNameUsesGh(t *testing.T) {
	r := &fakeRunner{} // gh present (no lookErr)
	c := &Client{r: r}
	dir, err := c.Clone(context.Background(), "me/app", "/ws")
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if dir != "/ws/app" {
		t.Fatalf("dir = %q, want /ws/app", dir)
	}
	if len(r.calls) != 1 || r.calls[0][0] != "gh" || !strings.Contains(strings.Join(r.calls[0], " "), "me/app") {
		t.Fatalf("expected gh clone call, got %+v", r.calls)
	}
}

func TestCloneFullURLUsesGit(t *testing.T) {
	r := &fakeRunner{}
	c := &Client{r: r}
	dir, err := c.Clone(context.Background(), "https://github.com/me/app.git", "/ws")
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if dir != "/ws/app" {
		t.Fatalf("dir = %q, want /ws/app", dir)
	}
	if r.calls[0][0] != "git" {
		t.Fatalf("expected git clone, got %+v", r.calls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shan/dev/dctl && go test ./internal/forge/`
Expected: FAIL — `undefined: Client`, `undefined: forge.Repo` (compile errors; the package has no non-test source yet).

- [ ] **Step 3: Write minimal implementation**

Create `internal/forge/forge.go`:

```go
// Package forge wraps the gh / glab CLIs to list and clone remote repos.
// All process execution goes through the runner seam so tests can fake exec.
package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Repo is one remote repository discovered via a forge CLI.
type Repo struct {
	FullName string // owner/name
	CloneURL string
	Desc     string
	Forge    string // "github" | "gitlab"
}

// runner abstracts exec.LookPath + exec.CommandContext so it can be faked.
type runner interface {
	look(name string) error
	run(ctx context.Context, dir, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) look(name string) error { _, err := exec.LookPath(name); return err }

func (execRunner) run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.CombinedOutput()
}

// Client is the forge facade injected into the handler.
type Client struct {
	r runner
}

// New returns a Client backed by real exec.
func New() *Client { return &Client{r: execRunner{}} }

// Available reports which forge CLIs are installed.
func (c *Client) Available() (github, gitlab bool) {
	return c.r.look("gh") == nil, c.r.look("glab") == nil
}

// List returns repos from every installed forge, labeled by Forge.
func (c *Client) List(ctx context.Context) ([]Repo, error) {
	gh, gl := c.Available()
	var out []Repo
	if gh {
		repos, err := c.listGitHub(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, repos...)
	}
	if gl {
		repos, err := c.listGitLab(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, repos...)
	}
	return out, nil
}

func (c *Client) listGitHub(ctx context.Context) ([]Repo, error) {
	raw, err := c.r.run(ctx, "", "gh", "repo", "list",
		"--json", "nameWithOwner,sshUrl,description", "--limit", "100")
	if err != nil {
		return nil, fmt.Errorf("gh repo list: %s", strings.TrimSpace(string(raw)))
	}
	var items []struct {
		NameWithOwner string `json:"nameWithOwner"`
		SSHURL        string `json:"sshUrl"`
		Description   string `json:"description"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("gh repo list: parse: %w", err)
	}
	out := make([]Repo, 0, len(items))
	for _, it := range items {
		out = append(out, Repo{FullName: it.NameWithOwner, CloneURL: it.SSHURL, Desc: it.Description, Forge: "github"})
	}
	return out, nil
}

func (c *Client) listGitLab(ctx context.Context) ([]Repo, error) {
	raw, err := c.r.run(ctx, "", "glab", "repo", "list", "--output", "json")
	if err != nil {
		return nil, fmt.Errorf("glab repo list: %s", strings.TrimSpace(string(raw)))
	}
	var items []struct {
		PathWithNamespace string `json:"path_with_namespace"`
		SSHURLToRepo      string `json:"ssh_url_to_repo"`
		Description       string `json:"description"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("glab repo list: parse: %w", err)
	}
	out := make([]Repo, 0, len(items))
	for _, it := range items {
		out = append(out, Repo{FullName: it.PathWithNamespace, CloneURL: it.SSHURLToRepo, Desc: it.Description, Forge: "gitlab"})
	}
	return out, nil
}

// specRe permits owner/name style specs (no shell metacharacters, no traversal).
var specRe = regexp.MustCompile(`^[\w.\-]+/[\w.\-]+$`)

// Clone clones spec into workspace and returns the project dir. It refuses
// traversal / shell-unsafe specs. If the target dir already exists it is
// returned as-is (idempotent "already cloned").
func (c *Client) Clone(ctx context.Context, spec, workspace string) (string, error) {
	base, isURL, err := parseSpec(spec)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(workspace, base)

	gh, gl := c.Available()
	var name string
	var args []string
	switch {
	case isURL:
		name, args = "git", []string{"clone", spec, dir}
	case gh:
		name, args = "gh", []string{"repo", "clone", spec, "--", dir}
	case gl:
		name, args = "glab", []string{"repo", "clone", spec, dir}
	default:
		return "", fmt.Errorf("no gh/glab installed to clone %q; pass a full git URL instead", spec)
	}
	if out, err := c.r.run(ctx, workspace, name, args...); err != nil {
		msg := strings.TrimSpace(string(out))
		// A pre-existing checkout is success (idempotent).
		if strings.Contains(msg, "already exists") {
			return dir, nil
		}
		return "", fmt.Errorf("%s clone: %s", name, msg)
	}
	return dir, nil
}

// parseSpec validates spec and returns the project basename + whether it's a URL.
func parseSpec(spec string) (base string, isURL bool, err error) {
	if strings.HasPrefix(spec, "https://") || strings.HasPrefix(spec, "git@") || strings.HasPrefix(spec, "ssh://") {
		b := path.Base(strings.TrimSuffix(spec, ".git"))
		if b == "" || b == "." || b == "/" || strings.Contains(b, "..") {
			return "", false, fmt.Errorf("cannot derive project name from %q", spec)
		}
		return b, true, nil
	}
	if !specRe.MatchString(spec) {
		return "", false, fmt.Errorf("invalid repo spec %q — use owner/name or a full git URL", spec)
	}
	return path.Base(spec), false, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/shan/dev/dctl && go test ./internal/forge/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /home/shan/dev/dctl
git add internal/forge/
git commit -m "feat(forge): gh/glab list+clone behind a fakeable runner seam"
```

---

## Task 4: Handler — new worktrees signature + forge seam (compile-green)

This task only re-wires the `worktrees` interface to the new signature and injects the forge facade; behaviour additions come in Tasks 5–6. It makes `handler` and its tests compile again.

**Files:**
- Modify: `internal/handler/handler.go`
- Modify: `internal/handler/handler_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/handler/handler_test.go`, replace the `fakeWT` block (lines ~39-55) and `newTestHandler` so the fake matches the new signature and a `fakeForge` exists. Replace:

```go
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
```

with:

```go
type fakeWT struct {
	createdRepos []string // repo arg captured per Create
	created      []string
	removed      []string
	path         string // "" → simulate shared fallback
	removeErr    error  // simulate dirty worktree
}

func (f *fakeWT) Create(repo, name string) (string, error) {
	f.createdRepos = append(f.createdRepos, repo)
	f.created = append(f.created, name)
	return f.path, nil
}
func (f *fakeWT) Remove(repo, name string, force bool) error {
	if f.removeErr != nil && !force {
		return f.removeErr
	}
	f.removed = append(f.removed, name)
	return nil
}

type fakeForge struct {
	repos    []forge.Repo
	cloneDir string
	cloneErr error
	cloned   []string // specs passed to Clone
	gh, gl   bool
}

func (f *fakeForge) Available() (bool, bool) { return f.gh, f.gl }
func (f *fakeForge) List(ctx context.Context) ([]forge.Repo, error) {
	return f.repos, nil
}
func (f *fakeForge) Clone(ctx context.Context, spec, workspace string) (string, error) {
	f.cloned = append(f.cloned, spec)
	if f.cloneErr != nil {
		return "", f.cloneErr
	}
	return f.cloneDir, nil
}
```

Add the `forge` import to the test file's import block:

```go
	"github.com/vskstudio/dctl/internal/forge"
```

Update `newTestHandler` to build and inject a `fakeForge` and return it:

```go
func newTestHandler(t *testing.T, homeType int) (*Handler, *fakeDiscord, *fakeSup, *fakeWT, *fakeForge, *state.State) {
	t.Helper()
	d := &fakeDiscord{homeType: homeType}
	sup := &fakeSup{}
	wt := &fakeWT{path: "/wt/x"}
	fg := &fakeForge{gh: true}
	st := state.NewState(t.TempDir() + "/s.json")
	st.AddAllow("owner")
	return NewHandler(d, sup, wt, fg, st, "claude"), d, sup, wt, fg, st
}
```

Then update **every** existing `newTestHandler` call site in the file to the new 6-value return. The blank-aware edits (apply to each test):

- `TestHandlerDeniesNonAllowlisted`: `h, _, _, _, _ := newTestHandler(...)` → `h, _, _, _, _, _ := newTestHandler(...)`
- `TestSetHomeDetectsCategory`: `h, _, _, _, st :=` → `h, _, _, _, _, st :=`
- `TestSetHomeDetectsForum`: `h, _, _, _, st :=` → `h, _, _, _, _, st :=`
- `TestSessionCreateText`: `h, d, sup, wt, st :=` → `h, d, sup, wt, _, st :=`
- `TestSessionCreateShared`: `h, _, _, wt, st :=` → `h, _, _, wt, _, st :=`
- `TestSessionCreateRejectsUnsafeName`: `h, d, _, wt, st :=` → `h, d, _, wt, _, st :=`
- `TestSessionCreateAcceptsSafeName`: `h, d, _, _, st :=` → `h, d, _, _, _, st :=`
- `TestSessionCreateRequiresHome`: `h, _, _, _, _ :=` → `h, _, _, _, _, _ :=`
- `TestSessionCreateForum`: `h, d, sup, _, st :=` → `h, d, sup, _, _, st :=`
- `TestSessionCloseStopsAndArchives`: `h, d, sup, wt, st :=` → `h, d, sup, wt, _, st :=`
- `TestSessionCloseDirtyRefusedWithoutForce`: `h, d, _, wt, st :=` → `h, d, _, wt, _, st :=`
- `TestSessionCloseDirtyForced`: `h, _, _, wt, st :=` → `h, _, _, wt, _, st :=`

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shan/dev/dctl && go test ./internal/handler/`
Expected: FAIL — `NewHandler` wants 5 args but got 6 / `wt.Create` signature mismatch / `forge` imported and not used in handler.go (compile errors).

- [ ] **Step 3: Write minimal implementation**

In `internal/handler/handler.go`:

1. Add the import:

```go
	"github.com/vskstudio/dctl/internal/forge"
```

2. Replace the `worktrees` interface:

```go
// worktrees owns per-session git worktree lifecycle. Create returns the worktree
// path ("" + nil error means "fall back to shared", e.g. not a git repo). The
// repo root is passed per call so one Worktreer serves every project.
type worktrees interface {
	Create(repo, name string) (path string, err error)
	Remove(repo, name string, force bool) error
}
```

3. Add a `forges` interface (after `worktrees`):

```go
// forges lists/clones remote repos via gh/glab (see internal/forge).
type forges interface {
	Available() (github, gitlab bool)
	List(ctx context.Context) ([]forge.Repo, error)
	Clone(ctx context.Context, spec, workspace string) (projectDir string, err error)
}
```

4. Add `fg forges` to `Handler` and `NewHandler`:

```go
type Handler struct {
	d          discord
	sup        supervisor
	wt         worktrees
	fg         forges
	st         *state.State
	defaultCmd string
}

// NewHandler builds a Handler. defaultCmd is the bridge command used when a
// session is created without an explicit cmd (e.g. "claude -p --continue").
func NewHandler(d discord, sup supervisor, wt worktrees, fg forges, st *state.State, defaultCmd string) *Handler {
	return &Handler{d: d, sup: sup, wt: wt, fg: fg, st: st, defaultCmd: defaultCmd}
}
```

5. Update the two existing worktree call sites in `sessionCreate` and `sessionClose` to pass a repo. For now (behaviour added in Task 5) resolve the legacy repo so existing tests pass unchanged. In `sessionCreate`, replace the `if !shared { … }` block:

```go
	// Worktree isolation by default; shared:true runs in the main checkout.
	shared := in.Data.OptBool("shared")
	repo := h.st.WorkspaceRoot() // legacy root for now; project resolution added in a later task
	var worktree, note string
	if !shared {
		path, err := h.wt.Create(repo, name)
		if err != nil {
			return errf("worktree: %v", err)
		}
		if path == "" {
			note = " (shared — not a git repo)"
		} else {
			worktree = path
		}
	}
```

And the two rollback `Remove` calls in `sessionCreate`:

```go
			_ = h.wt.Remove(repo, name, true) // roll back the worktree we just made
```

(apply to both the category and forum branches).

In `sessionClose`, replace the removal block:

```go
	if sess.Worktree != "" {
		force := in.Data.OptBool("force")
		repo := h.st.WorkspaceRoot()
		if err := h.wt.Remove(repo, name, force); err != nil {
			return errf("%v — commit, or close with force:true to discard (branch session/%s is kept)", err, name)
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/shan/dev/dctl && go test ./internal/handler/`
Expected: PASS (all pre-existing tests). `serve` still won't compile — fixed in Task 7.

- [ ] **Step 5: Commit**

```bash
cd /home/shan/dev/dctl
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "refactor(handler): repo-per-call worktrees + forges seam (no behaviour change)"
```

---

## Task 5: `/set workspace` + project resolution in sessionCreate/close

**Files:**
- Modify: `internal/handler/handler.go`
- Modify: `internal/handler/handler_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/handler/handler_test.go`:

```go
func TestSetWorkspacePersists(t *testing.T) {
	h, _, _, _, _, st := newTestHandler(t, dctl.ChannelText)
	dir := t.TempDir()
	r := h.Handle(context.Background(), it("owner", "set", "workspace",
		dctl.InteractionOption{Name: "path", Value: dir}))
	if r.Content == "" || !r.Ephemeral {
		t.Fatalf("expected ephemeral confirmation, got %+v", r)
	}
	if st.Workspace != dir {
		t.Fatalf("workspace not set: %q", st.Workspace)
	}
}

func TestSetWorkspaceRejectsMissingDir(t *testing.T) {
	h, _, _, _, _, st := newTestHandler(t, dctl.ChannelText)
	h.Handle(context.Background(), it("owner", "set", "workspace",
		dctl.InteractionOption{Name: "path", Value: "/no/such/dir/here"}))
	if st.Workspace != "" {
		t.Fatalf("missing dir should not be saved, got %q", st.Workspace)
	}
}

func TestSessionCreateUsesWorkspaceProject(t *testing.T) {
	h, _, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	_ = st.SetWorkspace("/ws")
	h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"},
		dctl.InteractionOption{Name: "project", Value: "myproj"}))
	if len(wt.createdRepos) != 1 || wt.createdRepos[0] != "/ws/myproj" {
		t.Fatalf("expected Create on /ws/myproj, got %+v", wt.createdRepos)
	}
	sess, _ := st.FindSession("demo")
	if sess.Project != "myproj" {
		t.Fatalf("session.Project not persisted: %+v", sess)
	}
}

func TestSessionCreateRequiresProjectWhenWorkspaceSet(t *testing.T) {
	h, d, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	_ = st.SetWorkspace("/ws")
	r := h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if !r.Ephemeral || r.Content == "" {
		t.Fatalf("expected error asking for project, got %+v", r)
	}
	if len(wt.created) != 0 || len(d.created) != 0 {
		t.Fatalf("nothing should be created: wt=%v ch=%v", wt.created, d.created)
	}
}

func TestSessionCreateRejectsProjectTraversal(t *testing.T) {
	for _, p := range []string{"../escape", "a/b", "..", "with space"} {
		h, d, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
		st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
		_ = st.SetWorkspace("/ws")
		r := h.Handle(context.Background(), it("owner", "session", "create",
			dctl.InteractionOption{Name: "name", Value: "demo"},
			dctl.InteractionOption{Name: "project", Value: p}))
		if !r.Ephemeral || r.Content == "" {
			t.Fatalf("project %q: expected rejection, got %+v", p, r)
		}
		if len(wt.created) != 0 || len(d.created) != 0 {
			t.Fatalf("project %q: nothing should be created", p)
		}
	}
}

func TestSessionCreateLegacyNoWorkspace(t *testing.T) {
	// No workspace set → legacy behaviour: repo is "" (WorkspaceRoot), still works.
	h, d, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if len(d.created) != 1 || len(wt.created) != 1 {
		t.Fatalf("legacy create should still work: ch=%v wt=%v", d.created, wt.created)
	}
}

func TestSessionCloseUsesProjectRepo(t *testing.T) {
	h, _, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
	_ = st.SetWorkspace("/ws")
	st.AddSession(state.Session{Name: "demo", ChannelID: "ch9", Type: "text", Worktree: "/ws/myproj/.dctl-sessions/demo", Project: "myproj"})
	h.Handle(context.Background(), it("owner", "session", "close",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if len(wt.removed) != 1 {
		t.Fatalf("expected worktree removed: %+v", wt.removed)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shan/dev/dctl && go test ./internal/handler/ -run 'Workspace|Project'`
Expected: FAIL — `/set workspace` returns "unknown /set subcommand"; project not resolved; `TestSessionCreateRequiresProjectWhenWorkspaceSet` proceeds instead of erroring.

- [ ] **Step 3: Write minimal implementation**

In `internal/handler/handler.go`:

1. Add `os` and `path/filepath` imports, and a `projectRe`:

```go
	"os"
	"path/filepath"
```

```go
// projectRe constrains a workspace project name to a single safe path segment
// (no "/", no "..", no spaces), so workspace+project cannot escape the root.
var projectRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
```

2. Extend `handleSet` to handle `workspace`. Replace the early `if sub != "home"` guard with a switch:

```go
func (h *Handler) handleSet(ctx context.Context, in dctl.Interaction) dctl.Response {
	sub, _ := in.Data.Subcommand()
	switch sub {
	case "home":
		return h.setHome(ctx, in)
	case "workspace":
		return h.setWorkspace(in)
	default:
		return errf("unknown /set subcommand")
	}
}
```

Move the existing home body into `setHome` (rename, keep the logic verbatim):

```go
func (h *Handler) setHome(ctx context.Context, in dctl.Interaction) dctl.Response {
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
	case dctl.ChannelForum:
		typ = "forum"
	default:
		return errf("home must be a category or a forum (got type %d)", ct)
	}
	if err := h.st.SetHome(state.HomeRef{ID: id, Type: typ}); err != nil {
		return errf("save failed: %v", err)
	}
	return dctl.Response{Content: fmt.Sprintf("🏠 Home set to %s `%s`.", typ, id), Ephemeral: true}
}

func (h *Handler) setWorkspace(in dctl.Interaction) dctl.Response {
	p, ok := in.Data.Opt("path")
	if !ok || p == "" {
		return errf("missing path")
	}
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return errf("bad path: %v", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return errf("not a directory: %s", abs)
	}
	if err := h.st.SetWorkspace(abs); err != nil {
		return errf("save failed: %v", err)
	}
	return dctl.Response{Content: fmt.Sprintf("📂 Workspace set to `%s`.", abs), Ephemeral: true}
}
```

Add the `strings` import.

3. In `sessionCreate`, replace the `repo := h.st.WorkspaceRoot()` line (added in Task 4) with full resolution. Insert after the existing `cmd` resolution and before the `shared := …` line:

```go
	ws := h.st.WorkspaceRoot()
	project := ""
	if ws != "" {
		project, _ = in.Data.Opt("project")
		if project == "" {
			return errf("specify project: (see `/workspace list`) or clone:")
		}
		if !projectRe.MatchString(project) {
			return errf("invalid project %q — use a single name (no /, spaces, or ..)", project)
		}
	}
	repo := ws
	if project != "" {
		repo = filepath.Join(ws, project)
	}
```

4. Persist `Project` on the created session. Update both `sess = state.Session{…}` literals to add `Project: project`:

```go
		sess = state.Session{Name: name, ChannelID: ch.ID, Type: "text", Cmd: cmd, Worktree: worktree, Project: project}
```
```go
		sess = state.Session{Name: name, ChannelID: ch.ID, Type: "forum", Cmd: cmd, Worktree: worktree, Project: project}
```

5. In `sessionClose`, replace `repo := h.st.WorkspaceRoot()` with project-aware resolution:

```go
		repo := h.st.WorkspaceRoot()
		if sess.Project != "" {
			repo = filepath.Join(repo, sess.Project)
		}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/shan/dev/dctl && go test ./internal/handler/`
Expected: PASS (new + all pre-existing tests).

- [ ] **Step 5: Commit**

```bash
cd /home/shan/dev/dctl
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "feat(handler): /set workspace + per-project repo resolution"
```

---

## Task 6: `clone:` option + `/workspace` (list / remotes)

**Files:**
- Modify: `internal/handler/handler.go`
- Modify: `internal/handler/handler_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/handler/handler_test.go`:

```go
func TestSessionCreateClonesThenUsesProject(t *testing.T) {
	h, _, _, wt, fg, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	_ = st.SetWorkspace("/ws")
	fg.cloneDir = "/ws/app"
	h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"},
		dctl.InteractionOption{Name: "clone", Value: "me/app"}))
	if len(fg.cloned) != 1 || fg.cloned[0] != "me/app" {
		t.Fatalf("expected clone of me/app, got %+v", fg.cloned)
	}
	if len(wt.createdRepos) != 1 || wt.createdRepos[0] != "/ws/app" {
		t.Fatalf("expected Create on /ws/app, got %+v", wt.createdRepos)
	}
	sess, _ := st.FindSession("demo")
	if sess.Project != "app" {
		t.Fatalf("project should be derived from clone: %+v", sess)
	}
}

func TestSessionCreateCloneErrorSurfaces(t *testing.T) {
	h, d, _, wt, fg, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	_ = st.SetWorkspace("/ws")
	fg.cloneErr = errors.New("auth required")
	r := h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"},
		dctl.InteractionOption{Name: "clone", Value: "me/app"}))
	if !r.Ephemeral || r.Content == "" {
		t.Fatalf("expected ephemeral clone error, got %+v", r)
	}
	if len(wt.created) != 0 || len(d.created) != 0 {
		t.Fatalf("nothing should be created after clone failure")
	}
}

func TestWorkspaceListShowsGitProjects(t *testing.T) {
	h, _, _, _, _, st := newTestHandler(t, dctl.ChannelText)
	ws := t.TempDir()
	// proj1 is a git repo; plain is a normal dir; file is not a dir.
	if err := os.MkdirAll(ws+"/proj1/.git", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws+"/plain", 0o755); err != nil {
		t.Fatal(err)
	}
	_ = st.SetWorkspace(ws)
	r := h.Handle(context.Background(), it("owner", "workspace", "list"))
	if !strings.Contains(r.Content, "proj1") {
		t.Fatalf("expected proj1 listed, got %q", r.Content)
	}
	if strings.Contains(r.Content, "plain") {
		t.Fatalf("non-git dir should not be listed, got %q", r.Content)
	}
}

func TestWorkspaceListErrorsWithoutWorkspace(t *testing.T) {
	h, _, _, _, _, _ := newTestHandler(t, dctl.ChannelText)
	r := h.Handle(context.Background(), it("owner", "workspace", "list"))
	if !r.Ephemeral || r.Content == "" {
		t.Fatalf("expected error when no workspace set, got %+v", r)
	}
}

func TestWorkspaceRemotesLists(t *testing.T) {
	h, _, _, _, fg, _ := newTestHandler(t, dctl.ChannelText)
	fg.gh = true
	fg.repos = []forge.Repo{{FullName: "me/app", Forge: "github"}}
	r := h.Handle(context.Background(), it("owner", "workspace", "remotes"))
	if !strings.Contains(r.Content, "me/app") || !strings.Contains(r.Content, "github") {
		t.Fatalf("expected labeled remote, got %q", r.Content)
	}
}

func TestWorkspaceRemotesNoForge(t *testing.T) {
	h, _, _, _, fg, _ := newTestHandler(t, dctl.ChannelText)
	fg.gh, fg.gl = false, false
	r := h.Handle(context.Background(), it("owner", "workspace", "remotes"))
	if !strings.Contains(r.Content, "gh/glab") {
		t.Fatalf("expected no-forge message, got %q", r.Content)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shan/dev/dctl && go test ./internal/handler/ -run 'Clone|Workspace'`
Expected: FAIL — `unknown command "workspace"`; clone path not wired (Create runs on `/ws` not `/ws/app`).

- [ ] **Step 3: Write minimal implementation**

In `internal/handler/handler.go`:

1. Route `workspace` in `Handle`'s switch (add a case):

```go
	case "workspace":
		return h.handleWorkspace(ctx, in)
```

2. Wire `clone:` into `sessionCreate`. Replace the project-resolution block from Task 5 with one that handles clone first:

```go
	ws := h.st.WorkspaceRoot()
	project := ""
	if ws != "" {
		if spec, ok := in.Data.Opt("clone"); ok && spec != "" {
			dir, err := h.fg.Clone(ctx, spec, ws)
			if err != nil {
				return errf("clone: %v", err)
			}
			project = filepath.Base(dir)
		} else {
			project, _ = in.Data.Opt("project")
		}
		if project == "" {
			return errf("specify project: (see `/workspace list`) or clone:")
		}
		if !projectRe.MatchString(project) {
			return errf("invalid project %q — use a single name (no /, spaces, or ..)", project)
		}
	}
	repo := ws
	if project != "" {
		repo = filepath.Join(ws, project)
	}
```

3. Add `handleWorkspace`:

```go
func (h *Handler) handleWorkspace(ctx context.Context, in dctl.Interaction) dctl.Response {
	sub, _ := in.Data.Subcommand()
	switch sub {
	case "list":
		return h.workspaceList()
	case "remotes":
		return h.workspaceRemotes(ctx)
	default:
		return errf("unknown /workspace subcommand")
	}
}

func (h *Handler) workspaceList() dctl.Response {
	ws := h.st.WorkspaceRoot()
	if ws == "" {
		return errf("no workspace set — run /set workspace first")
	}
	entries, err := os.ReadDir(ws)
	if err != nil {
		return errf("read workspace: %v", err)
	}
	out := "Projects:\n"
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(ws, e.Name(), ".git")); err != nil {
			continue
		}
		out += "• " + e.Name() + "\n"
		n++
	}
	if n == 0 {
		out = "No git projects in workspace."
	}
	return dctl.Response{Content: out, Ephemeral: true}
}

func (h *Handler) workspaceRemotes(ctx context.Context) dctl.Response {
	gh, gl := h.fg.Available()
	if !gh && !gl {
		return errf("no gh/glab found — install one and authenticate")
	}
	repos, err := h.fg.List(ctx)
	if err != nil {
		return errf("list remotes: %v", err)
	}
	if len(repos) == 0 {
		return dctl.Response{Content: "No remote repos found.", Ephemeral: true}
	}
	out := "Remotes:\n"
	for _, r := range repos {
		out += fmt.Sprintf("• [%s] %s\n", r.Forge, r.FullName)
	}
	return dctl.Response{Content: out, Ephemeral: true}
}
```

(`.git` of a worktree-style repo is a file, not a dir; `os.Stat` on it still succeeds, so the check holds for both normal and worktree checkouts.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/shan/dev/dctl && go test ./internal/handler/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /home/shan/dev/dctl
git add internal/handler/handler.go internal/handler/handler_test.go
git commit -m "feat(handler): clone: option + /workspace list & remotes"
```

---

## Task 7: Slash-command schema + serve wiring (build green)

**Files:**
- Modify: `interactions.go`
- Modify: `internal/serve/serve.go`

- [ ] **Step 1: Write the failing test**

Append to `interactions_test.go` (create if absent):

```go
package dctl

import (
	"strings"
	"testing"
)

func TestDctlCommandsHasWorkspaceAndOptions(t *testing.T) {
	cmds := dctlCommands()
	var names []string
	for _, c := range cmds {
		names = append(names, c["name"].(string))
	}
	joined := strings.Join(names, ",")
	if !strings.Contains(joined, "workspace") {
		t.Fatalf("expected a /workspace command, got %v", names)
	}

	// /set must have a workspace subcommand.
	set := findCmd(t, cmds, "set")
	if !hasSub(set, "workspace") {
		t.Fatalf("/set missing workspace subcommand")
	}

	// /session create must expose project + clone options.
	sess := findCmd(t, cmds, "session")
	create := findSub(t, sess, "create")
	if !hasOpt(create, "project") || !hasOpt(create, "clone") {
		t.Fatalf("/session create missing project/clone options")
	}
}

func findCmd(t *testing.T, cmds []map[string]any, name string) map[string]any {
	t.Helper()
	for _, c := range cmds {
		if c["name"] == name {
			return c
		}
	}
	t.Fatalf("command %q not found", name)
	return nil
}

func subs(cmd map[string]any) []map[string]any {
	raw, _ := cmd["options"].([]map[string]any)
	return raw
}

func hasSub(cmd map[string]any, name string) bool {
	for _, o := range subs(cmd) {
		if o["name"] == name {
			return true
		}
	}
	return false
}

func findSub(t *testing.T, cmd map[string]any, name string) map[string]any {
	t.Helper()
	for _, o := range subs(cmd) {
		if o["name"] == name {
			return o
		}
	}
	t.Fatalf("subcommand %q not found", name)
	return nil
}

func hasOpt(sub map[string]any, name string) bool {
	opts, _ := sub["options"].([]map[string]any)
	for _, o := range opts {
		if o["name"] == name {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/shan/dev/dctl && go test . -run TestDctlCommands`
Expected: FAIL — `expected a /workspace command` / `/set missing workspace subcommand`.

- [ ] **Step 3: Write minimal implementation**

In `interactions.go`, inside `dctlCommands()`:

Add the workspace subcommand to the `set` command's options (after the `home` sub):

```go
		{"name": "set", "description": "dctl settings", "options": []map[string]any{
			{"name": "home", "description": "Set the category/forum holding sessions", "type": typeSub,
				"options": []map[string]any{
					{"name": "channel", "description": "Category or forum", "type": typeChan, "required": true},
				}},
			{"name": "workspace", "description": "Set the workspace root holding projects", "type": typeSub,
				"options": []map[string]any{
					{"name": "path", "description": "Absolute path to the workspace dir", "type": typeStr, "required": true},
				}},
		}},
```

Add `project` and `clone` to the `create` sub's options (after `shared`):

```go
			{"name": "create", "description": "Create a session", "type": typeSub, "options": []map[string]any{
				{"name": "name", "description": "Session name", "type": typeStr, "required": true},
				{"name": "cmd", "description": "Override bridged command", "type": typeStr},
				{"name": "shared", "description": "Run in the main checkout (no worktree)", "type": typeBool},
				{"name": "project", "description": "Workspace project to start from (see /workspace list)", "type": typeStr},
				{"name": "clone", "description": "Remote repo to clone first (owner/name or URL)", "type": typeStr},
			}},
```

Add a new top-level `workspace` command (after the `session` command block, before `allow`):

```go
		{"name": "workspace", "description": "Inspect the workspace", "options": []map[string]any{
			{"name": "list", "description": "List local git projects in the workspace", "type": typeSub},
			{"name": "remotes", "description": "List remote repos via gh/glab", "type": typeSub, "options": []map[string]any{
				{"name": "forge", "description": "Limit to one forge", "type": typeStr, "choices": []map[string]any{
					{"name": "github", "value": "github"},
					{"name": "gitlab", "value": "gitlab"},
				}},
			}},
		}},
```

Then wire serve. In `internal/serve/serve.go`:

Add the forge import:

```go
	"github.com/vskstudio/dctl/internal/forge"
```

Replace the worktree/handler construction block (lines ~60-65):

```go
	wt := worktree.NewWorktreer(ctx)
	fg := forge.New()
	hdl := handler.NewHandler(c, sup, wt, fg, st, o.DefaultCmd)
```

(Delete the now-unused `repo := st.Repo … os.Getwd()` lines; `os` is still used elsewhere in the file so keep the import.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /home/shan/dev/dctl && go build ./... && go test . -run TestDctlCommands`
Expected: build succeeds; PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/shan/dev/dctl
git add interactions.go interactions_test.go internal/serve/serve.go
git commit -m "feat: workspace/project slash-command schema + serve wiring"
```

---

## Task 8: Full build + test sweep

**Files:** none (verification only).

- [ ] **Step 1: Run the whole suite**

Run: `cd /home/shan/dev/dctl && go build ./... && go vet ./... && go test ./...`
Expected: all packages build, vet clean, all tests PASS.

- [ ] **Step 2: Commit (only if vet/format produced changes)**

```bash
cd /home/shan/dev/dctl
gofmt -w .
git add -A
git commit -m "chore: gofmt after workspace/project picker" || true
```

---

## Self-Review (plan vs Spec 2/4)

**Spec coverage:**
- §2.1 `Workspace`, `Session.Project`, `SetWorkspace`, `WorkspaceRoot` → Task 1. ✔
- §3 Approach A (stateless `Worktreer`, repo per call) → Task 2 + interface in Task 4. ✔
- §4.1 `/set workspace` → Tasks 5 (handler) + 7 (schema). ✔
- §4.2 `create` `project:`/`clone:` → Tasks 5/6 (handler) + 7 (schema). ✔
- §4.3 `/workspace list` + `remotes` (with `forge` choice) → Tasks 6 + 7. ✔
- §5 `internal/forge` `Available`/`List`/`Clone`/`Repo`, exec seam, regex validation, idempotent clone → Task 3. ✔
- §6 sessionCreate flow (ws resolution, clone→project, missing-project error, path-escape reject, legacy path, no auto-delete of clone) → Tasks 5/6. The "do not auto-delete clone on failure" holds: rollback only ever calls `wt.Remove`, never deletes the checkout. ✔
- §6 sessionClose rebuilds repo from `Project` → Task 5. ✔
- §7 file list (state, worktree, handler, interactions, serve, forge, handler_test) → all touched. ✔
- §8 edge cases: `~`/relative expand (Task 5 setWorkspace), missing-dir reject (Task 5), traversal project (Task 5), non-git project shared fallback (preserved — `Create` returns `"",nil`), clone-already-exists (Task 3 `parseSpec`/"already exists"), neither forge (Tasks 3/6), forge stderr verbatim (Task 3 surfaces CombinedOutput), both-forge labeling (Task 6 `[forge]` prefix). ✔
- §9 success criteria 1-8 each map to a test in Tasks 1/5/6. ✔

**Placeholder scan:** No TBD/TODO/"handle errors"/"similar to". Every code step is complete.

**Type consistency:** `worktrees.Create(repo,name)` / `Remove(repo,name,force)` consistent across worktree.go, handler interface, fakeWT (Tasks 2/4). `forges` interface matches `forge.Client` method set and `fakeForge` (Tasks 3/4/6). `Session.Project`, `State.Workspace`, `SetWorkspace`, `WorkspaceRoot` consistent (Task 1 ↔ 5/6).

**Discrepancies found in spec vs real code (carried into the plan):**
1. Spec says `OptString`/`OptBool`; the real API is `Opt`/`OptBool` (interactions.go:44). Plan uses `Opt`. 
2. Spec's `forge` examples show free functions (`Available()`, `List(ctx)`, `Clone(...)`); to be fakeable like `handler` fakes `discord`, the plan wraps them on a `*forge.Client` with an injected `runner` seam and a `forges` interface on the handler. Behaviour identical; just testable.
3. Spec lists `cmd/dctl` as a file to read — it has no `NewWorktreer`/forge references, so no change is needed there (only `internal/serve` constructs the Worktreer). No task touches `cmd/dctl`.
4. Spec §4.3 mentions an optional `forge:` filter on `remotes`; the schema declares it (Task 7) but the handler currently ignores it and lists all (YAGNI per spec's "nice-to-have"). If filtering is desired later, branch on `in.Data.Opt("forge")` in `workspaceRemotes`.
5. Multi-instance (Spec 3) namespacing: this plan assumes Spec 3 already adjusted worktree naming; the `.dctl-sessions/<name>` path here is shown unchanged — reconcile with Spec 3's actual naming at execution time if it differs.
