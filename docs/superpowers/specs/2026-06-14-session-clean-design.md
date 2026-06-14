# Session Clean — Design

**Date:** 2026-06-14
**Status:** Approved for planning

## Problem

dctl can create sessions (`/session create`) and close them one at a time
(`/session close <name>`), but there is no way to **bulk-clean** sessions. Over
time the daemon accumulates:

- **Dead sessions** — the bridge/tmux pane is gone, the worktree directory was
  removed, and/or the Discord channel was deleted, yet the entry lingers in
  `state.json`.
- **Stale sessions** — alive but inactive for a long time.
- **Orphan residue** — worktrees under `.dctl-sessions/`, git branches
  `session/*`, tmux panes `dctl-*`, and participant journals left behind by
  sessions already removed from `state.json`.

We want one maintenance command, exposed on **both** surfaces (Discord
slash-command and the `dctl` CLI), with strong safety guards.

## Goals

- Purge dead sessions (full teardown: stop bridge, kill tmux, remove worktree,
  archive channel, drop from state, remove participant journal).
- Optionally close **all** live sessions (explicit opt-in).
- Sweep orphan residue (worktrees / tmux panes / journals with no state entry).
- Report stale (long-inactive) sessions; only act on them when asked.
- Two safety guards everywhere: **dry-run by default** + **`--all` required to
  touch live sessions**.

## Non-goals

- No automatic deletion of `session/*` git branches (they may hold unmerged
  commits). They are **listed** but only removed behind an explicit
  `--prune-branches` flag.
- No IPC/RPC protocol between CLI and daemon (the CLI path is offline-only).
- No new persistent `LastActive` timestamp on the `Session` model — inactivity is
  derived from Discord's last-message timestamp.

## Architecture decision

The daemon owns `state.json` in memory and runs the supervisor that holds the
bridge child processes. Therefore:

- **`/session clean` runs inside the daemon** — it has the supervisor to
  `Stop()` bridges cleanly and writes state without conflict. This is the primary
  surface.
- **`dctl sessions clean` is offline maintenance** — it edits `state.json`
  directly. It **refuses to run while the daemon is alive** (probes the configured
  health address) to avoid having its writes stomped by the daemon's in-memory
  copy. It cannot `Stop()` a supervised bridge, but it can kill residual tmux
  panes / bridge processes, delete worktrees and journals, and prune state.

Shared detection + teardown logic lives in a new package so both surfaces stay in
lockstep.

## Components

### 1. `internal/sessionclean` (new package)

Pure inspection logic, no side effects, fully unit-testable via mocked probes.

```go
type Reason string // "tmux-gone" | "worktree-gone" | "channel-gone" | "stale"

type Candidate struct {
    Session state.Session
    Reasons []Reason // why it surfaced (for the report)
    Dead    bool     // no remaining proof of life
    Stale   bool     // last message older than maxIdle
}

type Probes struct {
    TmuxExists  func(channelID string) bool
    WorktreeAt  func(path string) bool                       // os.Stat on the dir
    ChannelLive func(ctx context.Context, channelID string) (bool, error)
    LastMessage func(ctx context.Context, channelID string) (time.Time, error)
}

// Inspect classifies sessions. maxIdle == 0 disables stale detection.
// now is passed in (no time.Now() inside) for deterministic tests.
func Inspect(ctx context.Context, sessions []state.Session, p Probes,
    maxIdle time.Duration, now time.Time) []Candidate
```

**Death rule:** a session is `Dead` when it has **no remaining proof of life**:
`!TmuxExists` AND (worktree absent OR no worktree configured) AND
`!ChannelLive`. Individual failing probes are recorded in `Reasons` for the
report even when the session is not dead overall.

**Stale rule:** `LastMessage` older than `maxIdle` (and `maxIdle > 0`). Stale is
reported separately and never acted on without `--all`/`all:true` + `stale`.

**Probe errors are conservative:** a network/transport error on `ChannelLive` or
`LastMessage` means we do **not** assert death. The session stays out of the
candidate-for-deletion set and the error is surfaced in the report's "probe
errors" section.

### 2. Shared teardown (refactor of `handler.sessionClose`)

Extract the close sequence (stop bridge → `KillTmuxSession` → remove worktree →
archive channel → `RemoveSession` → remove participant journal) into a reusable
function. The daemon path passes the live `supervisor` for `Stop()`. The CLI
offline path has no supervisor: it kills the tmux pane and any locatable bridge
process, then proceeds with disk/state cleanup. `sessionClose` becomes a single
call into this teardown — no duplicated logic.

### 3. `/session clean` (daemon slash-command)

Allowlist-gated like the other `/session` subcommands. Options:

- `apply:bool` (default **false**) — dry-run lists what would happen; `true`
  executes.
- `all:bool` (default **false**) — without it, only dead sessions + orphan
  residue are touched; `true` also closes live sessions.
- `stale:bool` (default false) — include long-inactive sessions as candidates
  (still requires `all:true` to actually close them).

Ephemeral grouped report: dead / live / stale / orphan-residue / probe-errors,
then the teardown tally when `apply:true`.

### 4. `dctl sessions clean` (CLI)

New `cmd/dctl/sessions.go`; add `case "sessions"` to `main.go`. Mirror flags:
`--apply` (default false), `--all`, `--stale`, `--prune-branches`,
`--max-idle <days>` (defaults to the config value), `--state <path>`.

**Daemon-alive guard:** before writing, probe the configured health address. If
the daemon answers, refuse with a message pointing at `/session clean`.

**Orphan residue scan** (CLI-only, daemon doesn't track it): worktrees under
`<repo>/.dctl-sessions/` and branches `session/*` with no matching `state.json`
entry, orphan `dctl-*` tmux panes, and `participants/*.log` with no session.
Listed in dry-run; removed with `--apply` (branches only with
`--prune-branches`).

### 5. New helpers

- `session.TmuxSessionExists(channel string) bool` — symmetric to
  `KillTmuxSession`, via `tmux has-session`.
- `dctl.Client.LastMessageAt(ctx, channelID) (time.Time, error)` — timestamp of
  the channel's most recent message (drives stale detection).
- `dctl.Client.ChannelType` is reused as-is as the channel liveness probe (a 404
  means the channel is gone).

### 6. Config

Add to `internal/config/config.go` `Config`:

```go
SessionMaxIdleDays int `json:"sessionMaxIdleDays"` // stale threshold; 0 disables
```

Default when unset/zero in code: **14 days**. `0` in config explicitly disables
stale detection. Add a commented entry to the `Template` scaffold. Wire the value
into the daemon handler and expose it as the CLI `--max-idle` default.

## Data flow

```
sessions (state.json)
        │
        ▼
   Inspect(probes, maxIdle, now)  ──►  []Candidate {Dead, Stale, Reasons}
        │
        ▼
   report (always)  ──►  if apply: teardown(dead [+ live if all] [+ stale if all+stale])
                                   + orphan-residue sweep (CLI)
```

## Error handling

- Probe errors never cause deletion; reported separately.
- Teardown is best-effort per session: a failure on one (e.g. worktree has
  uncommitted changes without force) is reported and the sweep continues to the
  next session.
- Worktree removal without force fails on a dirty tree — surfaced with the same
  guidance as `/session close` (commit, or force).
- CLI refuses entirely if the daemon is alive (no partial writes).

## Testing

- `Inspect`: table tests over probe combinations — dead, alive, stale,
  probe-error — with mocked `Probes` and a fixed `now`.
- Shared teardown: session with/without worktree; tmux present/absent;
  participant journal removal.
- Orphan-residue scan: worktree / branch / journal with no state entry detected;
  branch never deleted without `--prune-branches`.
- CLI flag parsing + daemon-alive refusal.
- Config: `sessionMaxIdleDays` parse, default 14, `0` disables.

## Open questions

None — maxIdle is configurable via `config.json` (`sessionMaxIdleDays`, default
14); `session/*` branches are listed but never auto-deleted (require
`--prune-branches`).
