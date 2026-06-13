# dctl

Minimal, dependency-free Discord bot client for Go — and a **token-frugal CLI**
for an AI agent to send, read, and reply on Discord.

One library, two consumers:

- **`dctl` CLI** — an LLM drives a bot from the shell with deliberately tiny
  output (ids and one-line messages, no JSON) to minimise tokens.
- **Go package `github.com/vskstudio/dctl`** — embedded in services (e.g. the
  prospector backend) for sending and for notification fan-out.

Mono-server by design: one bot token + one default channel. REST only (no
gateway), so every call is on-demand HTTP.

## Install

```sh
go install github.com/vskstudio/dctl/cmd/dctl@latest
```

## Config (env)

| Var | Required | Meaning |
|-----|----------|---------|
| `DISCORD_BOT_TOKEN` | yes | Bot token, sent as `Authorization: Bot …`. Never commit it. |
| `DISCORD_CHANNEL_ID` | no | Default channel; override per-call with `-c/--channel`. |

Bot permissions (minimal): **View Channels + Send Messages + Read Message
History** (`68608`). Reading message *content* also needs the **Message Content
Intent** enabled in the Developer Portal.

## CLI

```sh
dctl send  "hello"                  # -> prints the new message id
dctl reply <message_id> "ok"        # -> prints the reply id
dctl read  -n 20 [--after <id>]     # -> "<id>\t<author>\t<content>" per line
dctl watch -i 10 [--after <id>]     # -> stream new messages forever
dctl react  <message_id> 👀         # -> add a reaction (needs Add Reactions)
dctl thread <message_id> "name"     # -> open a real thread off a message, prints thread id
```

`read`/`watch` print oldest-first (chronological). `--after <id>` returns only
messages newer than `<id>` — feed back the last id you saw to poll.

## Bind a Claude session to a channel

`dctl bridge` watches a channel and, for each **human** message (bot messages
are skipped, so it never answers itself), turns it into a reply posted back as a
threaded reply. The channel becomes a chat with Claude:

```sh
dctl bridge --state ~/.local/state/dctl/bridge.last
```

By default the bridge holds **one persistent `claude` process** per session in
Claude Code's stream-json mode (`-p --input-format stream-json --output-format
stream-json --verbose`). The context stays hot across messages, so the large
startup overhead (system prompt + tools) is paid **once**, not on every message.
Pick a model with `--model claude-haiku-4-5-20251001`; pass extra claude args via
`--cmd 'claude --permission-mode acceptEdits'`.

For an arbitrary per-message command instead (the legacy one-shot behavior), use
`--stream=false --cmd '<program>'`: the message text reaches it three ways —
appended as the last arg, piped on stdin, and as env vars `DCTL_MSG` /
`DCTL_AUTHOR` / `DCTL_MESSAGE_ID` / `DCTL_CHANNEL`.

`--state FILE` persists the last-seen id so a restart doesn't replay history.
Replies over Discord's 2000-char limit are chunked.

Run it permanently two ways:

- **systemd (survives reboot/logout):** see [`contrib/dctl-bridge.service`](contrib/dctl-bridge.service).
- **background (quick test):** `nohup dctl bridge -v &`

## Run the daemon at boot (cross-OS)

`dctl service install` registers the `dctl serve` daemon as a native,
boot-started service and starts it — on **Linux** a systemd *user* unit
(`~/.config/systemd/user/dctl.service` + `loginctl enable-linger`), on **macOS**
a launchd LaunchAgent, on **Windows** a Task Scheduler onlogon task.

```sh
dctl service install   [--health-addr 127.0.0.1:8787] [--env-file PATH]
dctl service status    # report whether the service is running
dctl service uninstall # stop and remove it
```

Secrets never go into the generated unit: it sources an env file
(`~/.config/dctl/dctl.env`, mode `0600`) that `install` creates as an empty
template **only if it doesn't already exist** — it never overwrites your token.
Fill it in (`DISCORD_BOT_TOKEN`, `DISCORD_CHANNEL_ID`, `DCTL_OWNER_ID`) and
restart the service. Install from an installed binary (`go install …`), not
`go run`, whose executable path is a temporary file.

## Sessions (Discord slash commands)

When `dctl serve` runs, it manages **sessions** — each one bridges a channel/forum
post to a Claude process — through slash commands (gated by the global allowlist):

```
/set workspace path:<dir>          # configurable root (e.g. ~/dev) sessions start from
/workspace list                    # list projects under the workspace
/workspace remotes forge:<github|gitlab>   # list clonable repos via gh / glab
/session create name:<n> [project:<repo>] [clone:<owner/repo>] [shared:<bool>]
/session close name:<n> [force:<bool>]
/session list
/session allow add|remove|list name:<n> [user:<@user>]   # per-session allowlist
/session who name:<n>              # observed participants in the session
```

`/session create` reports the worktree, branch, and project it set up. Worktrees,
branches (`session/<instance>/<name>`), and channel titles are namespaced by an
**instance id** (`DCTL_INSTANCE_ID`, else derived from `DCTL_OWNER_ID`), so two
daemons sharing one Discord home never collide.

**Authorization.** Slash commands always require the global allowlist. The
per-session allowlist (`/session allow`) *widens* who may drive a session's bridge
by writing in its channel: an author is accepted when **globally allowed OR on the
session's allowlist**. The bridge enforces this per message (reading the daemon
state fresh, so changes apply without a restart); unauthorized authors are recorded
for `/session who` but never trigger a Claude run.

## Library

```go
c := dctl.New(os.Getenv("DISCORD_BOT_TOKEN"), os.Getenv("DISCORD_CHANNEL_ID"))
msg, err := c.Send(ctx, "", "deploy finished ✅") // "" => default channel
msgs, err := c.Read(ctx, "", 20, "")              // chronological
_, err = c.Reply(ctx, "", msg.ID, "ack")
```

`Enabled()` reports whether a token is set; a disabled client returns
`ErrDisabled` from every call, so callers can stay oblivious to whether the
feature is on.

## Channels

```sh
dctl channel list                 # id  type  name  (per line)
dctl channel create prospection   # -> new channel id
dctl channel ensure prospector    # -> id (existing by that name, or created)
dctl channel create plaza --forum # -> new forum channel id
dctl channel post <forum_id> "title" "body"  # -> new forum post (thread) id
dctl channel delete <channel_id>  # remove a channel
```

Guild defaults to the bot's sole server (mono-server); pass `--guild <id>` to
target another. `dctl bridge` calls `ensure` automatically when no channel is
configured, so it always has somewhere to talk.

Creating/deleting channels and forum posts needs the bot's **Manage Channels**
permission (invite perms `68624`); reactions need **Add Reactions**. Plain
send/reply/read only need `68608`.

## License

MIT
