# dctl — Clean package architecture

**Date:** 2026-06-13
**Status:** Approved

## Goal

The module has grown to ~3100 lines with everything flattened into two packages:
the root `package dctl` and `cmd/dctl` (`package main`). The root package glues a
**public REST client** (the only surface external consumers vendor) together with
the **serve daemon's internals** (gateway, slash-command handler, state, health).
`cmd/dctl` is not thin — it owns real domain logic (a persistent claude
stream-json session, the supervisor, git-worktree lifecycle).

Restructure into an idiomatic Go layout that:

1. Keeps the public REST client at the module root, byte-for-byte compatible with
   its one external consumer (prospector).
2. Moves every daemon/CLI-only concern behind `internal/`, each package with one
   clear purpose.
3. Makes `cmd/dctl` thin: flag parsing + dispatch that delegates to importable
   packages.
4. Confines the only third-party dependency (`coder/websocket`) to one package so
   the vendored root stays stdlib-only.

This is a **pure refactor**: no behaviour change, no new feature, no public API
break. Build + the full test suite stay green throughout.

## Hard constraint — consumer compatibility

The `prospector` backend vendors `github.com/vskstudio/dctl` and imports it as
package `dctl`. It uses exactly:

- Types/ctor: `dctl.New`, `dctl.Client`, `dctl.Channel`, `dctl.Message`
- Client methods: `Send`, `Reply`, `Read`, `Channels`, `EnsureChannel`,
  `CreateChannel`, `DeleteChannel`

All of these — and their transitive helpers (`SoleGuild`/`Guilds`,
`resolveChannel`/`resolveGuild`, `do`/`newRequest`, `ErrDisabled`/`ErrNoChannel`)
— stay in root `package dctl` at the unchanged import path. The refactor must not
touch this surface.

## Target layout

```
github.com/vskstudio/dctl
│  ── package dctl : public REST client (vendored surface) ──
├── dctl.go            New/Client/Message/Author/Option/do/newRequest, Err*, APIBase
├── channels.go        Guild/Channel + channel REST (Channels/Create/Ensure/Delete/SoleGuild)
├── threads.go         thread + forum REST (StartThread/CreateForum/ForumPost)
├── reactions.go       reaction REST (React/Unreact)
├── interactions.go    interaction wire types (Interaction/Member/Response/…) + the
│                        4 daemon REST methods on *Client (AppID/RegisterCommands/
│                        RespondInteraction/UpsertStatusMessage). STAYS at root:
│                        these use the unexported newRequest/do plumbing, so moving
│                        them would force exporting raw HTTP primitives. The DTO
│                        types are harmless extra surface — prospector ignores them.
├── dctl_test.go
├── channels_test.go
│
├── internal/
│   ├── gateway/       package gateway   — websocket client only
│   │   └── gateway.go        (from gateway.go; imports root dctl for the
│   │                          Interaction type it unmarshals & emits)
│   ├── health/        package health    — daemon liveness snapshot (Health type)
│   │   ├── health.go         (from health.go)
│   │   └── health_test.go
│   ├── state/         package state     — on-disk daemon store (State/Session/HomeRef)
│   │   ├── state.go          (from state.go)
│   │   └── state_test.go
│   ├── handler/       package handler   — slash-command routing (interface-driven)
│   │   ├── handler.go        (from handler.go)
│   │   └── handler_test.go
│   ├── session/       package session   — persistent claude stream-json session
│   │   ├── stream.go         (from cmd/dctl/stream.go)
│   │   ├── stream_test.go
│   │   └── stream_live_test.go
│   ├── supervisor/    package supervisor — one bridge process per session
│   │   └── supervisor.go     (from cmd/dctl/supervisor.go)
│   ├── worktree/      package worktree   — per-session git worktree lifecycle
│   │   └── worktree.go       (from cmd/dctl/worktree.go)
│   ├── bridge/        package bridge     — channel↔command loop
│   │   └── bridge.go         (loop body of cmd/dctl/bridge.go)
│   └── serve/         package serve      — daemon wiring: gateway loop + health/ping/status loops
│       ├── serve.go          (loop body of cmd/dctl/serve.go)
│       └── loops.go          (from cmd/dctl/health.go — serveHealth/pingLoop/statusLoop)
│
└── cmd/dctl/
    └── main.go        package main   — flag parsing + dispatch only (~250 lines)
```

## Import direction (acyclic)

The single rule, enforced by `internal/` and Go's import checker:

- **`internal/*` may import root `dctl`.** Root `dctl` imports **no** `internal/*`.

| Package | Imports |
|---|---|
| `dctl` (root) | stdlib only (incl. interaction types + their REST methods) |
| `internal/health` | stdlib only |
| `internal/state` | stdlib only |
| `internal/worktree` | stdlib only |
| `internal/session` | stdlib / os-exec only |
| `internal/gateway` | `dctl` (for `Interaction`), `internal/health`, `coder/websocket` |
| `internal/handler` | `dctl` (Interaction/Response/Channel), `internal/state` |
| `internal/supervisor` | `internal/state` (for `Session`) |
| `internal/bridge` | `dctl`, `internal/session` |
| `internal/serve` | `dctl`, `internal/{gateway,health,state,handler,supervisor,worktree}` |

**Cycle watch:** root `dctl` owns the `Interaction`/`Response` DTO types, so both
`gateway` and `handler` import root `dctl` for them — one-way, no cycle (root
imports nothing internal). `gateway` never imports `handler`; the existing
`gateway.Interactions` channel (drained by `serve` into `handler.Handle`) is the
seam that keeps gateway decoupled from handler. `Session`/`HomeRef` live in
`internal/state`; handler/supervisor/serve import `state`, which imports none of
them.

**`Session`/`HomeRef` ownership:** referenced by handler, supervisor, serve. They
live in `internal/state` (their persistence home); the others import `state`.
`state` imports none of them, so no cycle.

## Files that mix concerns — how they split

- **`interactions.go`** stays at root, unsplit. It mixes wire types
  (`Interaction`, `Member`, `Response`, option helpers) with REST methods on
  `*Client` (`AppID`, `RegisterCommands`, `RespondInteraction`,
  `UpsertStatusMessage`, `dctlCommands`), but the REST methods depend on the
  unexported `newRequest`/`do` plumbing — extracting them would force exporting
  raw HTTP primitives, a worse trade. The DTO types are harmless extra public
  surface (prospector ignores them). Leaving the file intact is the correct,
  lowest-risk choice.
- **`cmd/dctl/bridge.go`** mixes flag parsing with the bridge loop. The loop body
  (`runBridge`/`runCmd`/`chunk`/`oneline`/`persist`) moves to `internal/bridge`
  as `bridge.Run(ctx, c, opts)`; `main` keeps a thin `runBridge(args)` that parses
  flags and calls it. Same pattern for `serve.go` → `internal/serve`.
- **`cmd/dctl/health.go`** is misnamed — it holds the daemon's *loops*
  (`serveHealth`, `pingLoop`, `statusLoop`), not the `Health` type. It moves to
  `internal/serve` (as `loops.go`), not `internal/health`.

## Thin-cmd boundary

`cmd/dctl/main.go` keeps only: `main()`, `usage()`, per-subcommand flag parsing
(`runSend`/`runReply`/`runRead`/`runWatch`/`runReact`/`runThread`/`runChannel`),
helpers `channelFlag`/`line`, and the dispatch switch. `serve` and `bridge`
delegate to `serve.Run` / `bridge.Run`. `channel` stays trivially in `main` (pure
REST on `c`). `cmd/dctl` drops from ~1100 to ~250 lines, all flag-glue.

## No signature changes

Every move is **package-qualification only** — no function or method signatures
change. References gain a package qualifier as code crosses a boundary:
`Channel` → `dctl.Channel`, `Session`/`HomeRef`/`State` → `state.*`,
`Health` → `health.Health`, `Interaction`/`Response` → `dctl.*` (they stay at
root). The interaction REST methods stay on `*dctl.Client` unchanged, so
`serve`'s call sites (`c.RegisterCommands`, `c.RespondInteraction`, `c.AppID`,
`c.UpsertStatusMessage`) are untouched.

**One deliberate export:** `optBool`, currently an unexported func in root
`interactions.go`, is consumed by `handler` (`optBool(in.Data, "shared"/"force")`).
Once `handler` lives in `internal/handler` it can no longer reach a root-package
unexported symbol, so `optBool` becomes the exported method
`(d InteractionData) OptBool(name string) bool` on root, and handler's two call
sites become `in.Data.OptBool("shared")` / `in.Data.OptBool("force")`. The
already-exported `Opt`/`Subcommand` methods handler also uses need no change.
Unexported helpers used only *within* a single moved package (e.g. `stamp` in
health, `findOpt`/`findBool` in root) stay unexported and move with their file.

## Error handling

No change to runtime error behaviour — this is a structural refactor. The only
risk surface is *compile-time*: import cycles and unqualified type references.
Each package move is a self-contained commit that must build + test green before
the next.

## Testing

- The existing suite (33 tests across the two packages) is the regression net:
  it must stay green after every commit. Tests move with their package (Go's
  same-dir rule): `health_test.go`, `state_test.go`, `handler_test.go`,
  `stream_test.go`, `stream_live_test.go` (gated by `DCTL_LIVE`).
- `handler_test.go`'s fakes (`fakeDiscord`/`fakeSup`/`fakeWT`) reference
  `*Channel`/`Session` — they gain `dctl.`/`state.` qualifiers.
- **Acceptance checks** after the refactor:
  - `gofmt -l .` → empty
  - `go vet ./...` → clean
  - `go build ./...` + `go build ./cmd/dctl` → ok
  - `go test ./...` → all green (same count, ≥33)
  - `go list -deps github.com/vskstudio/dctl` → must **not** include
    `coder/websocket` (root stays stdlib-only)
  - prospector still builds against the re-vendored dctl with no source change
    (verify `go build ./...` in prospector after re-vendoring)

## Migration risks

- **Build-broken mid-flight:** the gateway method→function change touches both
  `gateway` and `serve` — land it atomically.
- **`coder/websocket` location:** after the move it must be imported only by
  `internal/gateway`; confirm with `go list -deps` that the root is dep-free.
- **Re-vendoring prospector:** the import path is unchanged, so prospector's
  `go.mod` pin + `vendor/` only need a refresh if dctl is re-tagged; no source
  edits. This is the only external follow-up and is out of scope for this branch
  (tracked separately).

## Out of scope (YAGNI)

- Renaming the public package or its import path (would break prospector for no
  gain).
- Splitting the REST client further (it is cohesive at ~500 lines).
- Any behaviour change, new command, or new feature.
- Re-tagging/releasing dctl and updating prospector's pin (separate follow-up).
