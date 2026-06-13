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
```

`read`/`watch` print oldest-first (chronological). `--after <id>` returns only
messages newer than `<id>` — feed back the last id you saw to poll.

## Bind a Claude session to a channel

`dctl bridge` watches a channel and, for each **human** message (bot messages
are skipped, so it never answers itself), runs a command and posts its stdout
back as a threaded reply. Point it at a persistent Claude session and the channel
becomes a chat with Claude:

```sh
dctl bridge --cmd 'claude -p --continue' --state ~/.local/state/dctl/bridge.last
```

`--continue` keeps **one** conversation across messages (shared context). The
message text reaches the command three ways — appended as the last arg, piped on
stdin, and as env vars `DCTL_MSG` / `DCTL_AUTHOR` / `DCTL_MESSAGE_ID` /
`DCTL_CHANNEL` — so a wrapper script can do anything. `--state FILE` persists the
last-seen id so a restart doesn't replay history. Replies over Discord's 2000-char
limit are chunked.

Run it permanently two ways:

- **systemd (survives reboot/logout):** see [`contrib/dctl-bridge.service`](contrib/dctl-bridge.service).
- **background (quick test):** `nohup dctl bridge --cmd 'claude -p --continue' -v &`

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
dctl channel delete <channel_id>  # remove a channel
```

Guild defaults to the bot's sole server (mono-server); pass `--guild <id>` to
target another. `dctl bridge` calls `ensure` automatically when no channel is
configured, so it always has somewhere to talk.

## License

MIT
