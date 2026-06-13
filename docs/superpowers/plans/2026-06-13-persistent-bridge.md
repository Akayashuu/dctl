# Plan — Persistent bridge (stream-json)

Spec: `docs/superpowers/specs/2026-06-13-persistent-bridge-design.md`

TDD throughout: write the failing test, then the implementation, then `go test ./...`.

## Task 1 — Stream protocol pure helpers
- **Test** `cmd/dctl/stream_test.go`:
  - `TestUserLineShape` — `userLine("hi")` unmarshals to
    `{type:"user", message:{role:"user", content:"hi"}}`, ends in `\n`.
  - `TestReadTurnSuccess` — canned NDJSON (system/init → assistant thinking →
    assistant text → result) → `Text=="PONG"`, `CostUSD>0`, `SessionID` captured.
  - `TestReadTurnError` — `result` with `is_error:true` → `IsError`, `ErrMsg` set.
  - `TestReadTurnHandlesHugeLine` — a >64 KB `system/init` line before the result
    is consumed without error (proves no Scanner cap).
- **Impl** `cmd/dctl/stream.go`: `userLine`, `turnResult`, `readTurn` (bufio.Reader).
- Gate: `go test ./cmd/dctl/ -run 'UserLine|ReadTurn'`.

## Task 2 — streamSession over injectable pipes
- **Test**: `TestStreamSessionSend` — wire an in-memory fake process (io.Pipe pair):
  a goroutine reads one user line, writes a canned `result`; assert `Send` returns
  the text. Build `streamSession` so the transport (stdin Writer + stdout Reader)
  can be injected without a real `exec.Cmd`.
- **Impl**: `streamSession` struct with `Send`/`Close`; an internal constructor
  that takes the io pair (used by both the test and the real `Start`).
- Gate: `go test ./cmd/dctl/ -run StreamSession`.

## Task 3 — streamSession.Start (real process) + argv
- **Test** `TestStreamArgv` — `streamArgv(base, model, resumeID)` yields
  `claude -p --input-format stream-json --output-format stream-json --verbose`
  (+ `--model`, `--resume` when set), preserving extra base args.
- **Impl**: extract `streamArgv`; `Start` uses it, launches the process, wires pipes.
- Gate: `go test ./cmd/dctl/ -run StreamArgv`.

## Task 4 — Bridge responder seam
- **Test** `TestResponderSelection` — `newResponder(stream=false,...)` is one-shot;
  `stream=true` is the stream responder (assert by type/behavior).
- **Impl**: `responder` interface (`Respond(ctx, dctl.Message) (string, error)`);
  `oneShotResponder` wrapping `runCmd`; `streamResponder` wrapping a `streamSession`
  with restart-on-death + single retry (`--resume`). Add `--stream`/`--model`
  flags; swap the loop's `runCmd` call for `responder.Respond`.
- Gate: `go test ./cmd/dctl/`.

## Task 5 — serve default cmd + docs
- Change `serve.go` default `--cmd` to `claude`.
- Update `.claude/skills/dctl/SKILL.md` + `README.md` (persistent bridge, flags).
- Gate: `go build ./... && go vet ./... && go test ./...`.

## Task 6 — Manual smoke + finish
- Rebuild, restart the systemd daemon, open a session, confirm a real reply and
  that the `claude` process is persistent (single PID across two messages).
- pr-finisher checklist, then branch finish.
