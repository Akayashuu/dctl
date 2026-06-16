# dctl

A pure, dependency-free Go client for the Discord REST API (v10). No gateway,
no websocket, no CLI — every call is an on-demand HTTP request behind a small,
ergonomic API.

```sh
go get github.com/Herrscherd/dctl@v1.0.1
```

```go
c := dctl.New(os.Getenv("DISCORD_BOT_TOKEN"), os.Getenv("DISCORD_CHANNEL_ID"))

msg, err := c.Messages().Send(ctx, "", "deploy finished ✅") // "" => default channel
msgs, err := c.Messages().Read(ctx, "", 20, "")             // oldest-first
_, err = c.Messages().Reply(ctx, "", msg.ID, "ack")
```

## Design

A single public package with a three-layer architecture:

```
dctl/                    package "dctl"
├── dctl.go              façade Client + per-resource accessors      (layer 3)
├── channels.go …        one sub-client per resource                (layer 2)
├── types.go defaults.go shared DTOs, default channel/guild resolver
└── internal/transport/  the only HTTP seam, behind the Doer iface   (layer 1)
```

- **Transport** is the single place that touches `net/http` (auth, request
  building, error decoding, a 1 MiB response cap). It's the one mockable seam —
  an in-memory `Stub` makes resource tests network-free.
- **Sub-clients** group operations by resource and share the transport plus a
  default channel/guild resolver.
- **`Client`** is a thin façade exposing each sub-client through an accessor
  method (`c.Messages()`, `c.Channels()`, …).

Zero third-party dependencies — enforced by a test that fails on any non-stdlib
import.

## Resources

| Accessor | Operations |
|----------|------------|
| `c.Messages()` | `Send` · `Reply` · `Read` · `Edit` · `Delete` · `LastMessageAt` |
| `c.Channels()` | `List` · `Get` · `Type` · `Create` · `CreateUnder` · `Rename` · `Update` · `Delete` · `Ensure` · `EnsureUnder` · `Archive` |
| `c.Roles()` | `List` · `Create` · `Update` · `Delete` · `Assign` · `Unassign` |
| `c.Members()` | `List` · `Get` · `Kick` · `Ban` |
| `c.Reactions()` | `Add` · `Remove` |
| `c.Threads()` | `Start` · `CreateForum` · `ForumPost` |
| `c.Permissions()` | `Set` · `Remove` |
| `c.Webhooks()` | `Create` · `List` · `Delete` · `Execute` |
| `c.Interactions()` | `Register` · `RegisterCommands` · `List` · `Create` · `Edit` · `Delete` · `Registry` · `Respond` · `Defer` · `RespondAutocomplete` · `EditResponse` · `UpsertStatusMessage` · `AppID` |
| `c.Components()` | `SendSelectMenu` · `Ack` |
| `c.Guilds()` | `List` · `Sole` |

## Slash commands

Build commands with typed builders, then let a `Registry` own registration and
dispatch — the gateway only binds a name to a function:

```go
reg := c.Interactions().Registry()

reg.Add(
	dctl.NewCommand("set", "dctl settings").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("home", "category holding sessions",
				dctl.ChannelOpt("channel", "category", true).ChannelTypes(dctl.ChannelCategory)),
			dctl.Sub("count", "how many",
				dctl.Int("n", "1–100", true).Range(1, 100)).Loc(dctl.LocaleFR, "nombre", "combien"),
		),
	func(ctx context.Context, ix dctl.Interaction) (dctl.Response, error) {
		return dctl.Response{Content: "ok"}, nil
	})

reg.Sync(ctx)              // diff against Discord: create / edit / delete
resp, _ := reg.Dispatch(ctx, ix)  // route incoming interaction to its handler
```

`Sync` reconciles the live command set (add / remove / update) and refuses to run
on an empty registry while commands exist, so it never silently wipes everything.
`Dispatch` routes command interactions by name; attach an autocomplete handler
with `reg.Autocomplete(name, fn)` and route those via `reg.DispatchAutocomplete`.
The registry is stable across `c.Interactions().Registry()` calls, and the bot's
app id / sole guild are resolved once and cached.

Read option values in a handler with `ix.Data.Opt` (string), `OptBool`, `OptInt`,
`OptFloat`. Option builders cover every Discord type, `Choices`, `Range`, `Len`,
`ChannelTypes`, `Autocomplete`, and full `Loc` localization. For one-shot bulk
registration without a registry, use `Register(cmds...)`.

Channel-scoped ops accept `""` to target the configured default channel.
Guild-scoped ops accept `""` to target the bot's sole server (mono-server); pass
an explicit id to target another.

## Auth & permissions

Auth is a bot token sent as `Authorization: Bot <token>`. `New` keeps it in
memory only; nothing is read from the environment for you — pass the token (and
optional default channel) explicitly.

`Enabled()` reports whether a token is set; a client built without one returns
`transport.ErrDisabled` from every call, so callers can stay oblivious to whether
the feature is on. `DefaultChannel()` returns the configured default.

Minimal bot permissions for send/reply/read: **View Channels + Send Messages +
Read Message History** (`68608`). Reading message *content* also needs the
**Message Content Intent**. Managing channels/threads needs **Manage Channels**
(`68624`); reactions need **Add Reactions**.

## Security

- **No URL injection** — every dynamic path segment is percent-escaped and query
  strings are built with `url.Values`, so caller- or Discord-supplied ids and
  tokens can't smuggle extra path segments or parameters.
- **Self-redacting secrets** — `Webhook.Token` and `Interaction.Token` are of
  type `Secret`, which renders as `[REDACTED]` in logs (`%v`, `%+v`, `%#v`) and
  JSON. Call `.Reveal()` to get the real value when you must send it.

## Options

```go
c := dctl.New(token, channelID, dctl.WithHTTPClient(myClient)) // override the default 15s client
```

## License

MIT
