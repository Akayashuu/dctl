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

## License

MIT
