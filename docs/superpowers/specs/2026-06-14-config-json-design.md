# dctl `config.json` — declarative daemon defaults

## Problem

Daemon settings today live in three uncoordinated places: baked-into-unit
`serve` flags (`--cmd`, `--health-addr`), env vars (`DCTL_INSTANCE_ID`,
`DCTL_OWNER_ID`), and live runtime state (`state.json`, rewritten atomically by
`/set`). There is no single declarative file a user can author to say "this is
how my daemon should default." Putting defaults in `state.json` is unsafe: the
daemon rewrites it atomically and `encoding/json` strips comments, so any
hand-authored content (and comments) would be clobbered.

## Design

A user-authored, declarative `~/.config/dctl/config.json` holding **all daemon
settings except secrets**. The daemon **reads but never writes** it, so it is
safe to comment and hand-edit. Secrets (`DISCORD_BOT_TOKEN`,
`DISCORD_CHANNEL_ID`, owner token material) stay only in the `0600` env file.

### Scope split

- **In `config.json`:** `cmd`, `healthAddr`, `statusChannel`, `instance`,
  `owner`, `home` (`{id,type}`), `workspace`, `source`.
- **Stays runtime state (`state.json`):** the live allowlist and sessions —
  these are mutated continuously via Discord (`/session allow`, create/close)
  and are not declarative. `owner` in config seeds the *initial* allowlist
  entry, exactly as `DCTL_OWNER_ID` does.
- **Stays in env:** all secrets.

### Format

JSONC: full-line `//` comments are stripped before `json.Unmarshal`. Only
full-line comments (first non-space chars are `//`) are supported, so values
containing `//` (URLs) are never corrupted. The daemon never rewrites the file,
so comments survive.

### Precedence (highest wins)

```
explicit CLI flag  >  env var  >  config.json  >  built-in default      (daemon knobs)
live state.json    >  config.json  >  empty                             (home/workspace/source)
```

Daemon knobs are resolved in `cmd/dctl/serve.go` by seeding each flag's default
from config (and env where one exists) before `flag.Parse`; an explicitly-passed
flag then wins naturally. Declarative runtime fields are seeded **in-memory
only** into `state` via `state.ApplyDefaults` (set-if-empty, no persist), so a
live `/set` (which persists to `state.json`) always wins and config stays the
source for anything unset.

### Components

- **`internal/config`** — `Config` struct, `Load(path)` (missing file → zero
  value, no error; strips full-line `//`), `DefaultPath()`
  (`$DCTL_STATE_DIR` or `~/.config/dctl/config.json`), `Template(cmd)` (the
  commented scaffold).
- **`cmd/dctl/serve.go`** — load config, apply precedence, pass declarative
  fields into `serve.Options`.
- **`internal/serve`** — `Options` gains `Owner`, `Home`, `Workspace`,
  `Source`; `Run` seeds the allowlist from `Owner` and calls
  `st.ApplyDefaults`.
- **`internal/state`** — `ApplyDefaults(home *HomeRef, workspace, source)`
  sets each field only if empty, in-memory, no save.
- **`internal/service`** — install plan scaffolds `config.json` as a
  `FileWrite{Template:true}` (never clobbers an edited file). Tunable knobs are
  **not** baked into the unit's `ExecStart`: the install `--cmd` and
  `--health-addr` flags pre-fill the scaffold's `cmd`/`healthAddr` instead.
  Baking a knob as an explicit flag would shadow `config.json` (an explicit flag
  outranks it), so editing the file would silently have no effect — the unit
  carries only `--env-file` (a path, not a tunable). This unifies on
  `config.json` as the canonical home for every non-secret setting.

### Testing

- `config.Load`: missing file, comment stripping, URL-in-value untouched,
  precedence-relevant parsing.
- `state.ApplyDefaults`: set-if-empty, no-persist (reload from disk unchanged).
- `service`: config.json template present in install plan and never overwrites.
- `cmd serve` precedence: explicit flag > env > config > default.
</content>
</invoke>
