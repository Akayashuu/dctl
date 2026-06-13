# Spec 2/4 — Workspace + Project Picker (design)

Date: 2026-06-14
Status: design only (no implementation)

## 1. Objective

Today dctl operates on a single, global repo (`State.Repo`, defaulting to the
daemon cwd — see `internal/serve/serve.go:60`). One `Worktreer` is built once at
startup and every session worktree lives under that one repo.

We want:

1. A configurable **workspace root** (e.g. `~/dev/`) holding many project
   checkouts.
2. At `/session create`, the user **picks which project** (a sub-directory of
   the workspace) the session starts from. The worktree is then created inside
   that project, not a global one.
3. If `gh` (GitHub CLI) and/or `glab` (GitLab CLI) are installed locally, the
   user can **list remote repos and clone one** into the workspace to start a
   session on a project not yet checked out — works for **both GitHub and
   GitLab**.
4. "Fully configurable" and functional with both forges.

Non-goals (YAGNI): auth setup for gh/glab (we assume the user has already run
`gh auth login` / `glab auth login`), multi-workspace, fuzzy matching, caching
of remote listings, private-key handling, monorepo sub-path selection.

## 2. Data model changes (`internal/state/state.go`)

### 2.1 Workspace becomes the configured root; `Repo` becomes per-session

- **Add** `State.Workspace string json:"workspace,omitempty"` — absolute path
  to the workspace root. When empty, fall back to the current `Repo`/cwd
  behaviour so existing setups keep working.
- **Keep** `State.Repo` for backward compatibility / the "no workspace
  configured" path. Document it as the legacy single-repo root.
- **Add** `Session.Project string json:"project,omitempty"` — the project
  sub-dir name the session was created from (informational + lets us rebuild the
  per-session `Worktreer` on restart / close). The existing `Session.Worktree`
  absolute path already records where the worktree lives.

New persisted accessors (mirroring `SetHome`):

```go
func (s *State) SetWorkspace(path string) error
func (s *State) WorkspaceRoot() string // Workspace, else Repo, else "" 
```

Rationale: the worktree path is derived as `<workspace>/<project>/.dctl-sessions/<name>`.
We do not need to store the repo path on the session redundantly — `project` +
`WorkspaceRoot()` reconstruct it.

## 3. The key design question: how does the chosen project reach the worktree?

`Worktreer` is currently `{ctx, repo}`, built once in `serve.Run` and injected
into the handler as the `worktrees` interface. The chosen project must change
the repo root **per `/session create` call**, so the global single-`Worktreer`
model no longer fits.

### Approach A — Per-call repo argument on the worktrees interface (recommended)

Change the `worktrees` interface so `Create` takes the repo root explicitly:

```go
type worktrees interface {
    Create(repo, name string) (path string, err error)
    Remove(repo, name string, force bool) error
}
```

`Worktreer` drops its stored `repo` field and becomes effectively stateless
(`{ctx}`); each method runs `git -C <repo> …`. The handler resolves
`repo = filepath.Join(workspace, project)` from the `project:` option and passes
it down. On close, repo is reconstructed from `Session.Project` + workspace.

- Pros: smallest, most honest change; the worktree layer stops pretending there
  is one global repo; trivially testable; no per-session object lifecycle.
- Cons: touches the interface signature and both call sites in handler + the
  fake in `handler_test.go`.

### Approach B — Factory: handler builds a `Worktreer` per project

Inject a `func(repo string) worktrees` factory instead of a single instance.
Handler calls `wt := h.newWT(repoPath)` then `wt.Create(name)`.

- Pros: keeps `Create(name)`/`Remove(name)` unchanged.
- Cons: more indirection (a factory type), and close-path must rebuild the same
  factory call from stored project. Net more moving parts than A for no benefit.

### Approach C — Keep global Worktreer, mutate `repo` per call

Add `SetRepo(string)` and mutate before each op.

- Rejected: shared mutable state on a long-lived object invoked from the gateway
  handler is a data-race waiting to happen, and obscures intent.

**Recommendation: Approach A.** It directly models "repo is a per-session input"
and is the least surprising. Estimated blast radius: the interface in
`handler.go`, the two call sites, `worktree.go`, and the test fake.

## 4. New / changed commands & options

### 4.1 `/set workspace` (new `/set` subcommand)

Declared in `dctlCommands()` (`interactions.go:138`) alongside `home`:

```
/set workspace path:<string, required>
```

Handler `handleSet`: new `case "workspace"` — expand `~`, make absolute,
validate the directory exists, then `st.SetWorkspace(abs)`. Reply
`📂 Workspace set to <path>`.

### 4.2 `/session create` gains `project:` and `clone:`

Add two string options to the `create` subcommand block
(`interactions.go:145`):

- `project` — name of an existing project sub-dir in the workspace to start
  from. Optional; if omitted and a workspace is set, fall back to legacy
  `Repo`/cwd behaviour (so old flows still work) OR error asking for a project —
  see §6.
- `clone` — a remote repo spec (`owner/name`, or a full URL) to clone into the
  workspace first, then use as the project. Mutually informative with
  `project`: if `clone` is set we clone and derive `project` from the repo name.

Discord lacks true dynamic dropdowns in the slash-command schema, so `project:`
is a free-text string (optionally we can attach **static choices** rebuilt from
the workspace listing at `RegisterCommands` time — but that is a nice-to-have;
free text is the YAGNI baseline). Autocomplete (Discord
APPLICATION_COMMAND_AUTOCOMPLETE) is a future enhancement, not in scope.

### 4.3 `/workspace list` and `/workspace remotes` (new top-level command)

To make project selection discoverable without dynamic dropdowns:

```
/workspace list                          # local project sub-dirs of the workspace
/workspace remotes [forge:github|gitlab] # remote repos via gh / glab
```

`list` shells nothing — just reads workspace dir entries that are git repos.
`remotes` drives gh/glab (see §5). Both reply ephemerally with a bullet list the
user copies into `/session create project:` / `clone:`.

## 5. gh / glab integration (`internal/forge/` — new package)

Keep it a thin, dependency-free `os/exec` wrapper. New package
`internal/forge` so the handler/worktree packages stay focused.

### Detection

```go
func Available() (github, gitlab bool) {
    _, gh := exec.LookPath("gh")
    _, gl := exec.LookPath("glab")
    return gh == nil, gl == nil
}
```

### Listing remotes

- GitHub: `gh repo list --json nameWithOwner,sshUrl,description --limit 100`
  (JSON, easy to parse into a `[]Repo`).
- GitLab: `glab repo list --output json` (glab supports JSON output;
  fall back to plain `glab repo list` parsing only if needed — prefer JSON).

```go
type Repo struct { FullName, CloneURL, Desc, Forge string }
func List(ctx context.Context) ([]Repo, error) // merges available forges
```

Only query a forge whose CLI is present; if neither is installed, `remotes`
replies "no gh/glab found — install one and authenticate".

### Cloning

```go
func Clone(ctx, spec, workspace string) (projectDir string, err error)
```

Resolution order for `spec`:
1. Full URL (`https://…` / `git@…`) → `git -C <workspace> clone <url>`.
2. `owner/name` and gh available → `gh repo clone owner/name -- <dir>`
   (gh handles auth + host).
3. `owner/name` and glab available → `glab repo clone owner/name <dir>`.

Project dir name = repo basename. Refuse if the target dir already exists
(return it as "already cloned" so create can proceed idempotently). All commands
use `exec.CommandContext` with `CombinedOutput` for error surfacing, mirroring
the existing `worktree.go` style. No shell interpolation; spec is validated
against a conservative regex (`^[\w.\-/]+$` or a parseable URL) before use.

## 6. `sessionCreate` flow (revised, `handler/handler.go:118`)

1. Validate `name` (unchanged).
2. Resolve workspace: `ws := h.st.WorkspaceRoot()`.
   - If `ws == ""` → legacy path: behave exactly as today (global repo). This
     preserves backward compatibility and is the "no workspace configured"
     branch.
3. If `clone:` set → `forge.Clone(ctx, spec, ws)` → `project = basename`.
   Else `project = OptString("project")`.
4. If `project == ""` and workspace IS set → error: "specify project: (see
   `/workspace list`) or clone:". (Explicit beats guessing.)
5. `repo := filepath.Join(ws, project)`; reject path-escape (project must be a
   single clean segment — reuse a slug check; no `/`, no `..`).
6. `shared`/worktree logic unchanged except `Create`/`Remove` now take `repo`
   (Approach A). Persist `Session{… Project: project, Worktree: path}`.
7. On any failure after a fresh `clone`, do **not** auto-delete the clone (it is
   a real checkout the user may want); only roll back the worktree, as today.

`sessionClose` rebuilds `repo = filepath.Join(WorkspaceRoot(), sess.Project)`
(or legacy repo when `Project == ""`) to call `wt.Remove(repo, name, force)`.

## 7. Files / functions to touch

- `internal/state/state.go` — add `Workspace`, `Session.Project`,
  `SetWorkspace`, `WorkspaceRoot`.
- `internal/worktree/worktree.go` — make `Worktreer` repo-stateless; `Create`
  and `Remove` take `repo string`; `path()`/`isGitRepo()` take repo.
- `internal/handler/handler.go` — `worktrees` interface signature; `handleSet`
  `case "workspace"`; `sessionCreate`/`sessionClose` resolve repo from
  workspace+project; new `handleWorkspace` (list/remotes); inject `*forge`
  helpers (or call package funcs directly).
- `interactions.go` — `dctlCommands()`: `/set workspace`, `create` `project:` +
  `clone:` options, new `/workspace` command (`list`, `remotes` with `forge`
  choice option).
- `internal/serve/serve.go:64` — stop building one `Worktreer` bound to a repo;
  build the stateless one (`NewWorktreer(ctx)`). Workspace comes from state at
  call time.
- `internal/forge/forge.go` — **new**: `Available`, `List`, `Clone`, `Repo`.
- `internal/handler/handler_test.go` — update the worktrees fake to the new
  signature; add tests for project resolution + path-escape rejection.

## 8. Edge cases

- Workspace path with `~` / relative → expand + absolutize on `/set workspace`.
- Workspace set but directory missing/deleted later → `/workspace list` reports
  the error instead of panicking.
- `project:` containing `/`, `..`, or absolute path → reject (path traversal;
  same threat class the existing `sessionNameRe` comment calls out).
- Chosen project is not a git repo → `Create` already returns `("", nil)` →
  session runs shared in that project dir; surface the existing "(shared — not a
  git repo)" note.
- `clone:` target already exists in workspace → treat as success, reuse it.
- Neither gh nor glab installed → `clone:` with bare `owner/name` (no URL)
  fails clearly; `/workspace remotes` says none available.
- gh/glab present but not authenticated → command exits non-zero; surface the
  CLI's stderr verbatim (don't try to interpret).
- Same repo name from both forges in `remotes` listing → prefix/label each line
  with its `Forge` so they're distinguishable.
- Concurrent `/session create` on the same fresh clone → clone is idempotent;
  worktree names are unique per session so no collision.

## 9. Success criteria

1. `/set workspace path:~/dev` persists an absolute workspace in state.
2. `/workspace list` shows git project sub-dirs of the workspace.
3. `/workspace remotes` lists repos from every installed+authed forge (gh and
   glab), labeled by forge; gracefully degrades when one/both absent.
4. `/session create name:x project:<existing>` creates the worktree under
   `<workspace>/<project>/.dctl-sessions/x` and bridges it.
5. `/session create name:x clone:owner/repo` clones into the workspace then
   starts the session on it, via gh OR glab as appropriate.
6. Legacy behaviour (no workspace set) is unchanged; existing tests still pass.
7. Path-traversal `project:`/`clone:` inputs are rejected.
8. New unit tests cover repo resolution, path-escape rejection, and the
   gh/glab-absent branches (forge funcs behind small seams so exec is faked).
