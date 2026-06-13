# dctl Clean Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure dctl from two flat packages into an idiomatic Go layout — public REST client at the module root, every daemon/CLI concern under `internal/` — with zero behaviour change and zero break to the vendored consumer (prospector).

**Architecture:** Root `package dctl` keeps the REST client + interaction DTO types. Leaf daemon packages (`health`, `state`, `worktree`, `session`) move first, then `gateway`, `handler`, `supervisor`, `bridge`, `serve`, and finally `cmd/dctl` is slimmed to flag-glue. Each task is one package move that must build + test green before the next, so the module is never broken mid-flight.

**Tech Stack:** Go 1.23, `github.com/coder/websocket` (to be confined to `internal/gateway`). Spec: `docs/superpowers/specs/2026-06-13-clean-architecture-design.md`.

---

## Conventions for every task

- Move a file with `git mv` so history is preserved.
- After editing, run `gofmt -w` on touched files.
- The acceptance command after every task is:
  ```bash
  cd /home/shan/dev/dctl && gofmt -l . | grep -v '^\.dctl-sessions/' ; go vet ./... && go build ./... && go build ./cmd/dctl && go test ./...
  ```
  Expected: `gofmt -l` prints nothing, vet clean, both builds succeed, tests pass (count never drops below the previous task; final ≥33).
- Commit author is fixed: `git -c user.email=sauvageleo1@gmail.com -c user.name=Akayashuu commit`.
- Branch is `feat/clean-architecture` (already created off master).

---

### Task 1: Move `health` to `internal/health`

**Files:**
- Move: `health.go` → `internal/health/health.go`
- Move: `health_test.go` → `internal/health/health_test.go`

`health.go` and its test are self-contained (`package dctl`, stdlib `sync`/`time` only, no reference to any other dctl symbol). Only the package clause changes; nothing else in the repo references `Health` yet *in a moved package* — `cmd/dctl` and `gateway.go` reference it as `dctl.Health`, which Task 6/Task 5 will re-qualify. For now those callers still compile because `Health` is being *removed* from root → they will break. **Therefore this task also updates the current callers** to the new path.

- [ ] **Step 1: Create the package dir and move the files**

```bash
cd /home/shan/dev/dctl
mkdir -p internal/health
git mv health.go internal/health/health.go
git mv health_test.go internal/health/health_test.go
```

- [ ] **Step 2: Change the package clause in both moved files**

In `internal/health/health.go` and `internal/health/health_test.go`, change the first line:

```go
package health
```

- [ ] **Step 3: Re-qualify the callers of `Health` in cmd/dctl**

`cmd/dctl/serve.go` and `cmd/dctl/health.go` reference `dctl.Health`, `dctl.NewHealth`. These become `health.Health`, `health.NewHealth`. Add the import `"github.com/vskstudio/dctl/internal/health"` to both files. Concretely:

- `cmd/dctl/serve.go`: `health := dctl.NewHealth(time.Now())` → `health := healthpkg.NewHealth(time.Now())` — but the local variable is named `health`, which would shadow the package. Rename the import to avoid the clash:
  ```go
  import healthpkg "github.com/vskstudio/dctl/internal/health"
  ```
  then `health := healthpkg.NewHealth(time.Now())`.
- `cmd/dctl/health.go`: the loop signatures use `*dctl.Health` → `*healthpkg.Health`; add the same aliased import. The parameter named `h *dctl.Health` becomes `h *healthpkg.Health`.

- [ ] **Step 4: Re-qualify the `gateway.go` reference to Health**

`gateway.go` (still `package dctl` at root for now) has `Health *Health` and `NewGateway(c *Client, health *Health)`. Because `Health` is leaving root, this file must import the new package. Add `import "github.com/vskstudio/dctl/internal/health"` and change the two references: `Health *health.Health` and the `health *health.Health` parameter, and `g.Health.HeartbeatAck(...)` stays (it's a method call on the field).

- [ ] **Step 5: Run the acceptance command**

Run:
```bash
cd /home/shan/dev/dctl && gofmt -l . | grep -v '^\.dctl-sessions/' ; go vet ./... && go build ./... && go build ./cmd/dctl && go test ./...
```
Expected: gofmt prints nothing, vet clean, builds succeed, tests pass (3 health tests now run under `internal/health`).

- [ ] **Step 6: Commit**

```bash
git add -A
git -c user.email=sauvageleo1@gmail.com -c user.name=Akayashuu commit -m "refactor(health): move Health to internal/health"
```

---

### Task 2: Move `state` to `internal/state`

**Files:**
- Move: `state.go` → `internal/state/state.go`
- Move: `state_test.go` → `internal/state/state_test.go`

`state.go` defines `State`, `Session`, `HomeRef` and is stdlib-only. Callers of these in root are `handler.go` (still root) and in `cmd/dctl` (`serve.go`). `handler.go` will move in Task 7; for now it lives in root and references `State`/`Session`/`HomeRef` unqualified — once those leave root, `handler.go` breaks. So this task re-qualifies handler.go's references too (handler stays in root until Task 7 but must compile now).

- [ ] **Step 1: Move the files**

```bash
cd /home/shan/dev/dctl
mkdir -p internal/state
git mv state.go internal/state/state.go
git mv state_test.go internal/state/state_test.go
```

- [ ] **Step 2: Change the package clause in both moved files to `package state`**

- [ ] **Step 3: Re-qualify `handler.go` (still at root)**

Add import `"github.com/vskstudio/dctl/internal/state"`. Replace every bare `State`→`state.State`, `Session`→`state.Session`, `HomeRef`→`state.HomeRef` in `handler.go`. Affected spots: the `supervisor` interface `Start(s Session)`→`Start(s state.Session)`; `worktrees` unaffected; `Handler.st *State`→`*state.State`; `NewHandler(... st *State ...)`→`*state.State`; `Session{...}` literals in `sessionCreate` → `state.Session{...}`; `HomeRef{...}` in `handleSet` → `state.HomeRef{...}`.

- [ ] **Step 4: Re-qualify `cmd/dctl/serve.go` and `cmd/dctl/health.go`**

In `cmd/dctl/serve.go`: `dctl.LoadState`→`state.LoadState`, `dctl.NewHandler` arg types unaffected here. Add import `"github.com/vskstudio/dctl/internal/state"`. `st, err := dctl.LoadState(...)` → `state.LoadState(...)`. The variable is named `st` (no clash with package `state`). In `cmd/dctl/health.go`, `statusLoop(... st *dctl.State ...)` → `*state.State`; add the import.

- [ ] **Step 5: Run the acceptance command** (same as Task 1 Step 5). Expected: 4 state tests now under `internal/state`; all green.

- [ ] **Step 6: Commit**

```bash
git add -A
git -c user.email=sauvageleo1@gmail.com -c user.name=Akayashuu commit -m "refactor(state): move State/Session/HomeRef to internal/state"
```

---

### Task 3: Move `worktree` to `internal/worktree`

**Files:**
- Move: `cmd/dctl/worktree.go` → `internal/worktree/worktree.go`

`Worktreer` is `package main` today, uses only `context`/`exec`/`filepath`/`strings`. No dctl symbols. It's referenced once, in `cmd/dctl/serve.go` (`wt := NewWorktreer(ctx, repo)`).

- [ ] **Step 1: Move the file**

```bash
cd /home/shan/dev/dctl
mkdir -p internal/worktree
git mv cmd/dctl/worktree.go internal/worktree/worktree.go
```

- [ ] **Step 2: Change the package clause to `package worktree`**

- [ ] **Step 3: Update the caller in `cmd/dctl/serve.go`**

Add import `"github.com/vskstudio/dctl/internal/worktree"`. Change `wt := NewWorktreer(ctx, repo)` → `wt := worktree.NewWorktreer(ctx, repo)`.

- [ ] **Step 4: Run the acceptance command.** Expected: green (no new tests; worktree has none).

- [ ] **Step 5: Commit**

```bash
git add -A
git -c user.email=sauvageleo1@gmail.com -c user.name=Akayashuu commit -m "refactor(worktree): move Worktreer to internal/worktree"
```

---

### Task 4: Move `session` (stream) to `internal/session`

**Files:**
- Move: `cmd/dctl/stream.go` → `internal/session/stream.go`
- Move: `cmd/dctl/stream_test.go` → `internal/session/stream_test.go`
- Move: `cmd/dctl/stream_live_test.go` → `internal/session/stream_live_test.go`

`stream.go` is `package main` and holds the persistent claude stream-json session. Before moving, confirm what it exports and who calls it.

- [ ] **Step 1: Inspect the stream symbols and their callers**

Run:
```bash
cd /home/shan/dev/dctl
grep -nE '^func |^type ' cmd/dctl/stream.go
grep -rnE '\b(streamSession|newStreamSession|StreamSession|responder|runStream)\b' cmd/dctl --include='*.go' | grep -v stream
```
Expected: lists the exported/unexported symbols in `stream.go` and any reference from `bridge.go`/`main.go`. Note each caller — they must be re-qualified after the move. If a symbol is unexported (lowercase) but used outside `stream.go`, it must be exported as part of this task (Go cannot reach an unexported symbol across packages).

- [ ] **Step 2: Move the three files**

```bash
mkdir -p internal/session
git mv cmd/dctl/stream.go internal/session/stream.go
git mv cmd/dctl/stream_test.go internal/session/stream_test.go
git mv cmd/dctl/stream_live_test.go internal/session/stream_live_test.go
```

- [ ] **Step 3: Change the package clause to `package session` in all three files**

- [ ] **Step 4: Export any symbol used by `bridge.go`/`main.go`**

For each symbol the Step 1 grep showed used outside `stream.go`, capitalize its declaration and every use within the `session` package and at the call sites in `cmd/dctl`. Add `import "github.com/vskstudio/dctl/internal/session"` to the calling file(s) and qualify (e.g. `session.NewStreamSession(...)`). If the stream tests reference unexported helpers that are now exported, update them in the same file.

- [ ] **Step 5: Run the acceptance command.** Expected: the stream tests (and the `DCTL_LIVE`-gated live test, skipped without the env var) run under `internal/session`; all green.

- [ ] **Step 6: Commit**

```bash
git add -A
git -c user.email=sauvageleo1@gmail.com -c user.name=Akayashuu commit -m "refactor(session): move persistent stream-json session to internal/session"
```

---

### Task 5: Move `gateway` to `internal/gateway`

**Files:**
- Move: `gateway.go` → `internal/gateway/gateway.go`

`gateway.go` is the websocket client. It references: `Client` (`g.c *Client`, `g.c.Enabled()`, `g.c.token`), `ErrDisabled`, `Interaction` (the channel type + unmarshal target), and `health.Health` (already re-qualified in Task 1). After the move it imports root `dctl` for `Client`/`ErrDisabled`/`Interaction`.

**Caveat — unexported field access:** `gateway.go` reads `g.c.token` (unexported field on root `Client`). Across packages this is illegal. Confirm and fix:

- [ ] **Step 1: Confirm the unexported access**

Run:
```bash
cd /home/shan/dev/dctl
grep -nE '\bg\.c\.[a-z]' gateway.go
```
Expected: shows `g.c.token` (and possibly other lowercase field/method accesses). Each lowercase access to a root-`Client` member is a cross-package violation once gateway moves.

- [ ] **Step 2: Add an exported accessor on root `Client` for the token**

In root `dctl.go`, add (near the other `Client` methods):

```go
// Token returns the bot token (empty if disabled). Used by the gateway package
// to authenticate the websocket IDENTIFY.
func (c *Client) Token() string { return c.token }
```

- [ ] **Step 3: Move the file and switch the package**

```bash
mkdir -p internal/gateway
git mv gateway.go internal/gateway/gateway.go
```
Change the clause to `package gateway`. Add imports:
```go
import (
    "github.com/vskstudio/dctl"
    "github.com/vskstudio/dctl/internal/health"
)
```

- [ ] **Step 4: Re-qualify root types and fix the token access**

In `internal/gateway/gateway.go`: `*Client`→`*dctl.Client`, `ErrDisabled`→`dctl.ErrDisabled`, `Interaction`→`dctl.Interaction`, `*Health`→`*health.Health`. Replace `g.c.token` → `g.c.Token()`. The `gwPayload`, `readPayload`, `writeJSON`, `gatewayURL`, `intentGuilds` stay unexported in the package. `Gateway`, `NewGateway`, `Run`, the `Interactions` field stay exported.

- [ ] **Step 5: Re-qualify the caller in `cmd/dctl/serve.go`**

`gw := dctl.NewGateway(c, health)` → `gw := gateway.NewGateway(c, health)`; `gw.Interactions` and `gw.Run` are unchanged. Add import `"github.com/vskstudio/dctl/internal/gateway"`.

- [ ] **Step 6: Confirm `coder/websocket` left the root**

Run:
```bash
cd /home/shan/dev/dctl && go list -deps github.com/vskstudio/dctl | grep coder/websocket && echo "STILL PRESENT (bad)" || echo "root is websocket-free (good)"
```
Expected: `root is websocket-free (good)`.

- [ ] **Step 7: Run the acceptance command.** Expected: green.

- [ ] **Step 8: Commit**

```bash
git add -A
git -c user.email=sauvageleo1@gmail.com -c user.name=Akayashuu commit -m "refactor(gateway): move websocket client to internal/gateway; confine coder/websocket"
```

---

### Task 6: Export `OptBool`, then move `handler` to `internal/handler`

**Files:**
- Modify: `internal/... ` — first edit root `interactions.go` (export `OptBool`)
- Move: `handler.go` → `internal/handler/handler.go`
- Move: `handler_test.go` → `internal/handler/handler_test.go`

Handler references, after the earlier tasks: `dctl.Interaction`/`Response`/`InteractionData` (root), `dctl.Channel` + `dctl.ChannelForum` (root, in the `discord` interface and `handleSet`), `state.State`/`Session`/`HomeRef` (re-qualified in Task 2), and the unexported `optBool` (root) — which must be exported.

- [ ] **Step 1: Export `optBool` as a method in root `interactions.go`**

In `interactions.go`, replace:

```go
// optBool returns the bool value of a (possibly nested) option, false if absent.
func optBool(d InteractionData, name string) bool {
	if b, ok := findBool(d.Options, name); ok {
		return b
	}
	return false
}
```

with:

```go
// OptBool returns the bool value of a (possibly nested) option, false if absent.
func (d InteractionData) OptBool(name string) bool {
	if b, ok := findBool(d.Options, name); ok {
		return b
	}
	return false
}
```

(`findBool` stays unexported — it's only used inside `interactions.go`.)

- [ ] **Step 2: Build root to confirm the export compiles** (handler still at root, still calls `optBool(...)` — it will fail; that's expected, fixed in Step 4)

Run: `cd /home/shan/dev/dctl && go build . 2>&1 | head`
Expected: errors only about `optBool` being undefined in `handler.go` (proves the root package itself compiles otherwise). If other errors appear, fix them before moving on.

- [ ] **Step 3: Move the handler files**

```bash
mkdir -p internal/handler
git mv handler.go internal/handler/handler.go
git mv handler_test.go internal/handler/handler_test.go
```
Change both clauses to `package handler`.

- [ ] **Step 4: Re-qualify `internal/handler/handler.go`**

Add imports:
```go
import (
    "context"
    "fmt"

    "github.com/vskstudio/dctl"
    "github.com/vskstudio/dctl/internal/state"
)
```
Replace: `Interaction`→`dctl.Interaction`, `Response`→`dctl.Response`, `*Channel`→`*dctl.Channel` (in the `discord` interface), `ChannelForum`→`dctl.ChannelForum` (in `handleSet`), `State`→`state.State`, `Session`→`state.Session`, `HomeRef`→`state.HomeRef`. Change the two `optBool(in.Data, "shared")` / `optBool(in.Data, "force")` calls to `in.Data.OptBool("shared")` / `in.Data.OptBool("force")`. The `Opt`/`Subcommand` calls are unchanged (already exported methods).

- [ ] **Step 5: Re-qualify `internal/handler/handler_test.go`**

The fakes implement the package-private `discord`/`supervisor`/`worktrees` interfaces, so they stay in `package handler`. Re-qualify their signatures: `*Channel`→`*dctl.Channel`, `Session`→`state.Session`. Add the `dctl` and `state` imports. Any `Interaction{...}`/`InteractionData{...}` literals → `dctl.Interaction{...}`/`dctl.InteractionData{...}`. Any `Response` assertions → `dctl.Response`.

- [ ] **Step 6: Re-qualify the caller in `cmd/dctl/serve.go`**

`h := dctl.NewHandler(c, sup, wt, st, *defaultCmd)` → `h := handler.NewHandler(c, sup, wt, st, *defaultCmd)`; `h.Handle(ctx, in)` unchanged. Add import `"github.com/vskstudio/dctl/internal/handler"`.

- [ ] **Step 7: Run the acceptance command.** Expected: handler tests run under `internal/handler`; all green.

- [ ] **Step 8: Commit**

```bash
git add -A
git -c user.email=sauvageleo1@gmail.com -c user.name=Akayashuu commit -m "refactor(handler): move slash-command routing to internal/handler; export InteractionData.OptBool"
```

---

### Task 7: Move `supervisor` to `internal/supervisor`

**Files:**
- Move: `cmd/dctl/supervisor.go` → `internal/supervisor/supervisor.go`

`Supervisor` already imports `github.com/vskstudio/dctl` for `dctl.Session` — but `Session` moved to `internal/state` in Task 2, so that import is now wrong and must become `internal/state`. Confirm: `Start(sess dctl.Session)` → `Start(sess state.Session)`, `runLoop(ctx, sess dctl.Session)` → `state.Session`.

- [ ] **Step 1: Move the file**

```bash
cd /home/shan/dev/dctl
mkdir -p internal/supervisor
git mv cmd/dctl/supervisor.go internal/supervisor/supervisor.go
```

- [ ] **Step 2: Switch package + fix the Session import**

Change clause to `package supervisor`. Replace the import `"github.com/vskstudio/dctl"` with `"github.com/vskstudio/dctl/internal/state"`. Replace both `dctl.Session` → `state.Session`.

- [ ] **Step 3: Update the caller in `cmd/dctl/serve.go`**

`sup := NewSupervisor(ctx, self)` → `sup := supervisor.NewSupervisor(ctx, self)`. The `sup.Start(sess)` loop over `st.SnapshotSessions()` is unchanged (it now passes `state.Session`, which matches). Add import `"github.com/vskstudio/dctl/internal/supervisor"`.

- [ ] **Step 4: Run the acceptance command.** Expected: green.

- [ ] **Step 5: Commit**

```bash
git add -A
git -c user.email=sauvageleo1@gmail.com -c user.name=Akayashuu commit -m "refactor(supervisor): move per-session bridge supervisor to internal/supervisor"
```

---

### Task 8: Move the bridge loop to `internal/bridge`

**Files:**
- Modify: `cmd/dctl/bridge.go` (keep a thin `runBridge(args)`)
- Create: `internal/bridge/bridge.go` (the loop body)

`cmd/dctl/bridge.go` currently mixes flag parsing (`runBridge` flagset) with the loop. Move the loop into `bridge.Run`. First inspect what it actually contains (Task 4 may have already touched it if stream symbols are used).

- [ ] **Step 1: Inspect bridge.go's current symbols and stream usage**

Run:
```bash
cd /home/shan/dev/dctl
grep -nE '^func |session\.|stream' cmd/dctl/bridge.go
```
Expected: the function list (`runBridge`, `runCmd`, `chunk`, `oneline`, `persist`, `logf`) and whether it now calls into `internal/session`. Note the exact set — they all move except the flag parsing.

- [ ] **Step 2: Define the bridge options + `Run` in `internal/bridge/bridge.go`**

Create `internal/bridge/bridge.go` as `package bridge`. Move the loop body of `runBridge` plus the helpers (`runCmd`, `chunk`, `oneline`, `persist`, `logf`, and the `ack/done/fail` emoji consts and `discordMaxLen`) into it, exposing:

```go
package bridge

import (
    "context"

    "github.com/vskstudio/dctl"
    // plus "github.com/vskstudio/dctl/internal/session" if Step 1 showed stream usage
)

// Options configures one bridge run (parsed from CLI flags by cmd/dctl).
type Options struct {
    Channel  string
    Cmd      string
    Ensure   string
    Interval int
    State    string
    After    string
    Verbose  bool
}

// Run links the channel to the command until ctx is cancelled. (Body lifted
// verbatim from the old runBridge, minus the flag parsing.)
func Run(ctx context.Context, c *dctl.Client, o Options) error {
    // ... moved loop, using o.Channel/o.Cmd/... in place of the old flag pointers ...
}
```
Re-qualify any root references that were unqualified in `package main` — in `cmd/dctl` they were already `c.React`, `c.Reply`, `dctl.ErrDisabled`, `c.EnsureChannel`, `c.Read` etc.; in `package bridge` the `dctl.`-qualified ones stay, and `c.*` method calls stay (c is `*dctl.Client`). `ErrDisabled` (if referenced bare) → `dctl.ErrDisabled`.

- [ ] **Step 3: Reduce `cmd/dctl/bridge.go` to flag-glue**

Replace the body of `runBridge` so it only parses flags and delegates:

```go
func runBridge(ctx context.Context, c *dctl.Client, args []string) error {
    fs := flag.NewFlagSet("bridge", flag.ExitOnError)
    ch := channelFlag(fs)
    cmdStr := fs.String("cmd", "", "command to run per message (message appended as last arg + piped on stdin)")
    ensure := fs.String("ensure", "prospector", "if no channel is set, create/reuse a channel with this name")
    interval := fs.Int("i", 5, "poll interval in seconds")
    state := fs.String("state", "", "file to persist the last-seen message id across restarts")
    after := fs.String("after", "", "seed start id for the first run (state file wins once it exists)")
    verbose := fs.Bool("v", false, "log activity to stderr")
    fs.Parse(args)
    return bridge.Run(ctx, c, bridge.Options{
        Channel: *ch, Cmd: *cmdStr, Ensure: *ensure,
        Interval: *interval, State: *state, After: *after, Verbose: *verbose,
    })
}
```
Add import `"github.com/vskstudio/dctl/internal/bridge"`. Remove from `cmd/dctl/bridge.go` everything that moved (the helpers, consts) so nothing is duplicated.

- [ ] **Step 4: Run the acceptance command.** Expected: green (any bridge-related tests still pass; if the live/smoke test referenced the moved helpers, it moved with `session` in Task 4 — confirm no dangling references).

- [ ] **Step 5: Commit**

```bash
git add -A
git -c user.email=sauvageleo1@gmail.com -c user.name=Akayashuu commit -m "refactor(bridge): move bridge loop to internal/bridge; cmd keeps flag-glue"
```

---

### Task 9: Move serve orchestration to `internal/serve`

**Files:**
- Create: `internal/serve/serve.go` (from `cmd/dctl/serve.go` body)
- Create: `internal/serve/loops.go` (from `cmd/dctl/health.go`)
- Modify: `cmd/dctl/serve.go` → thin `runServe(args)` delegating to `serve.Run`
- Delete: `cmd/dctl/health.go` (its content moves to `internal/serve/loops.go`)

By now `cmd/dctl/serve.go` imports `healthpkg`, `state`, `worktree`, `gateway`, `supervisor`, `handler` and root `dctl`. Lift the orchestration into `internal/serve` so `cmd/dctl` only parses flags.

- [ ] **Step 1: Create `internal/serve/loops.go` from `cmd/dctl/health.go`**

```bash
cd /home/shan/dev/dctl
mkdir -p internal/serve
git mv cmd/dctl/health.go internal/serve/loops.go
```
Change the clause to `package serve`. Its references are already `dctl.Client`, `health.Health` (after Task 1 it's `healthpkg.Health` in cmd — re-import as `health` here), `state.State` (after Task 2). Set imports to:
```go
import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "time"

    "github.com/vskstudio/dctl"
    "github.com/vskstudio/dctl/internal/health"
    "github.com/vskstudio/dctl/internal/state"
)
```
and use `health.Health`, `state.State` in `serveHealth`/`pingLoop`/`statusLoop`. `c.AppID`, `c.UpsertStatusMessage` stay (root Client methods). Keep `serveHealth`/`pingLoop`/`statusLoop`/`healthWindow` unexported — they're called only from `serve.Run` in the same package.

- [ ] **Step 2: Create `internal/serve/serve.go` from `cmd/dctl/serve.go`**

```bash
git mv cmd/dctl/serve.go internal/serve/serve.go
```
Change the clause to `package serve`. Rename `runServe(ctx, c, args)` to:
```go
// Run is the always-on Gateway daemon (gateway + supervisor + liveness).
func Run(ctx context.Context, c *dctl.Client, o Options) error { ... }
```
where `Options` carries the parsed flags:
```go
type Options struct {
    StatePath     string
    DefaultCmd    string
    HealthAddr    string
    StatusChannel string
}
```
Replace the in-body flag pointers (`*statePath`, `*defaultCmd`, `*healthAddr`, `*statusChannel`) with `o.StatePath` etc. Keep the `defaultStatePath()` helper here (it's the default for the flag, but the CLI computes the default — see Step 3; move `defaultStatePath` to stay with serve OR cmd. Keep it in `serve` and export it as `serve.DefaultStatePath()` so cmd can use it for the flag default). Set imports to root `dctl` + `internal/{gateway,health,state,handler,supervisor,worktree}`. Re-qualify: `healthpkg`→`health`, `NewSupervisor`→`supervisor.NewSupervisor`, `NewWorktreer`→`worktree.NewWorktreer`, `dctl.NewHandler`→`handler.NewHandler`, `dctl.NewGateway`→`gateway.NewGateway`, `dctl.LoadState`→`state.LoadState`. The `c.RegisterCommands`/`c.RespondInteraction`/`c.AppID` calls stay (root Client methods).

- [ ] **Step 3: Add a thin `cmd/dctl/serve.go`**

Create a new `cmd/dctl/serve.go` (`package main`):
```go
package main

import (
    "context"
    "flag"

    "github.com/vskstudio/dctl"
    "github.com/vskstudio/dctl/internal/serve"
)

func runServe(ctx context.Context, c *dctl.Client, args []string) error {
    fs := flag.NewFlagSet("serve", flag.ExitOnError)
    statePath := fs.String("state", serve.DefaultStatePath(), "path to the daemon state file")
    defaultCmd := fs.String("cmd", "claude", "default bridged base command for new sessions")
    healthAddr := fs.String("health-addr", "", "if set (e.g. :8787), serve GET /health")
    statusChannel := fs.String("status-channel", "", "if set, maintain a self-updating status embed there")
    fs.Parse(args)
    if !c.Enabled() {
        return dctl.ErrDisabled
    }
    return serve.Run(ctx, c, serve.Options{
        StatePath: *statePath, DefaultCmd: *defaultCmd,
        HealthAddr: *healthAddr, StatusChannel: *statusChannel,
    })
}
```
(Move the `!c.Enabled()` guard to the CLI as shown, and drop it from `serve.Run` — or keep it in both; keeping it only in `serve.Run` is fine too. Pick one; do not leave the now-unused `flag`/`os` imports in `serve.Run`.)

- [ ] **Step 4: Run the acceptance command.** Expected: green; `cmd/dctl` no longer imports `gateway`/`supervisor`/`worktree`/`handler`/`health`/`state` directly except via `serve` (and `bridge`).

- [ ] **Step 5: Commit**

```bash
git add -A
git -c user.email=sauvageleo1@gmail.com -c user.name=Akayashuu commit -m "refactor(serve): move daemon orchestration to internal/serve; cmd keeps flag-glue"
```

---

### Task 10: Final verification + cmd/dctl thinness check

**Files:** none modified unless a check fails.

- [ ] **Step 1: Confirm `cmd/dctl` is thin**

Run:
```bash
cd /home/shan/dev/dctl && wc -l cmd/dctl/*.go && echo "---imports---" && grep -h '"github.com/vskstudio/dctl' cmd/dctl/*.go | sort -u
```
Expected: total well under ~400 lines; the only `internal/*` imports are `internal/bridge` and `internal/serve` (plus root `dctl`). If any `runX` still holds domain logic, note it (not necessarily a failure, but record it).

- [ ] **Step 2: Full acceptance sweep**

Run:
```bash
cd /home/shan/dev/dctl
gofmt -l . | grep -v '^\.dctl-sessions/'
go vet ./...
go build ./... && go build ./cmd/dctl
go test ./...
go list -deps github.com/vskstudio/dctl | grep coder/websocket && echo "WEBSOCKET LEAKED INTO ROOT (bad)" || echo "root websocket-free (good)"
```
Expected: gofmt empty, vet clean, builds ok, tests ≥33 green, `root websocket-free (good)`.

- [ ] **Step 3: Confirm the vendored consumer (prospector) still builds**

The import path is unchanged, but verify nothing prospector uses moved off root. Run:
```bash
cd /home/shan/dev/dctl && go list -f '{{range .Exports}}{{.}} {{end}}' . 2>/dev/null || true
grep -rhoE 'dctl\.[A-Z][A-Za-z]+|\.(Send|Reply|Read|Channels|EnsureChannel|CreateChannel|DeleteChannel)\(' /home/shan/dev/prospector-ci/backend --include='*.go' | grep -v vendor | sort -u
```
Expected: every symbol prospector uses (`dctl.New`, `dctl.Client`, `dctl.Channel`, `dctl.Message`, `Send`/`Reply`/`Read`/`Channels`/`EnsureChannel`/`CreateChannel`/`DeleteChannel`) is still defined in root `package dctl`. (Re-vendoring prospector is a separate follow-up — out of scope here.)

- [ ] **Step 4: Final structural commit (if any formatting/cleanup remained)**

If Steps 1–3 surfaced a stray import or formatting fix, apply it and:
```bash
git add -A
git -c user.email=sauvageleo1@gmail.com -c user.name=Akayashuu commit -m "refactor: final cleanup after package restructure"
```
Otherwise, no commit — the refactor is complete on `feat/clean-architecture`.

---

## Self-review notes

- **Spec coverage:** Tasks 1–9 implement the target tree section-by-section (health, state, worktree, session, gateway, handler, supervisor, bridge, serve); Task 10 covers the acceptance checks (gofmt/vet/build/test, `go list -deps` websocket confinement, prospector compat). The "thin cmd/dctl" requirement is realized across Tasks 8–9 and verified in Task 10 Step 1.
- **Ordering rationale:** leaves first (`health`, `state`, `worktree`, `session`) so later movers can depend on them already-relocated; `gateway`/`handler` next (both depend on root + the leaves); `supervisor`/`bridge`/`serve` last as they wire everything. Every task re-qualifies its current callers in the same commit, so the module builds green at every step — never broken mid-flight.
- **Discovered subtleties baked in:** `g.c.token` cross-package access → `Client.Token()` accessor (Task 5); `optBool` → exported `InteractionData.OptBool` (Task 6); the `health` local-var vs package-name clash → aliased import `healthpkg` in cmd, plain `health` in serve (Tasks 1, 9). `interactions.go` deliberately stays at root (uses unexported `newRequest`/`do`).
- **Type consistency:** `state.Session`/`state.State`/`state.HomeRef`, `dctl.Interaction`/`dctl.Response`/`dctl.Channel`, `health.Health` are used identically across Tasks 2/5/6/7/9. The bridge/serve `Options` structs are defined once (Tasks 8/9) and consumed only by their thin cmd wrappers.
