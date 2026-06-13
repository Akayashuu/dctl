package dctl

import (
	"context"
	"net/http"
)

// Interaction is the subset of a Discord INTERACTION_CREATE we handle
// (application slash commands, type 2).
type Interaction struct {
	ID      string          `json:"id"`
	Token   string          `json:"token"`
	GuildID string          `json:"guild_id"`
	Member  Member          `json:"member"`
	Data    InteractionData `json:"data"`
}

// Member carries the invoking user (interactions in a guild come via member.user).
type Member struct {
	User Author `json:"user"`
}

// InteractionData is the invoked command + its options.
type InteractionData struct {
	Name    string              `json:"name"`
	Options []InteractionOption `json:"options"`
}

// InteractionOption is one command option; for subcommands, Options nests.
type InteractionOption struct {
	Name    string              `json:"name"`
	Type    int                 `json:"type"`
	Value   any                 `json:"value"`
	Options []InteractionOption `json:"options"`
}

// Response is what the Handler decides to reply with.
type Response struct {
	Content   string
	Ephemeral bool
}

// Opt returns the string value of a (possibly nested) option by name.
func (d InteractionData) Opt(name string) (string, bool) {
	return findOpt(d.Options, name)
}

func findOpt(opts []InteractionOption, name string) (string, bool) {
	for _, o := range opts {
		if o.Name == name {
			if s, ok := o.Value.(string); ok {
				return s, true
			}
		}
		if v, ok := findOpt(o.Options, name); ok {
			return v, true
		}
	}
	return "", false
}

// optBool returns the bool value of a (possibly nested) option, false if absent.
func optBool(d InteractionData, name string) bool {
	if b, ok := findBool(d.Options, name); ok {
		return b
	}
	return false
}

func findBool(opts []InteractionOption, name string) (bool, bool) {
	for _, o := range opts {
		if o.Name == name {
			if b, ok := o.Value.(bool); ok {
				return b, true
			}
		}
		if b, ok := findBool(o.Options, name); ok {
			return b, true
		}
	}
	return false, false
}

// Subcommand returns the name of the first sub-command option, if any.
func (d InteractionData) Subcommand() (string, []InteractionOption) {
	for _, o := range d.Options {
		if o.Type == 1 { // SUB_COMMAND
			return o.Name, o.Options
		}
	}
	return "", nil
}

// AppID returns the bot's application id (== bot user id) via /users/@me.
func (c *Client) AppID(ctx context.Context) (string, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/users/@me", nil)
	if err != nil {
		return "", err
	}
	var u struct {
		ID string `json:"id"`
	}
	if err := c.do(req, &u); err != nil {
		return "", err
	}
	return u.ID, nil
}

// RegisterCommands (re)registers the dctl slash command set for the sole guild
// (guild-scoped commands appear instantly, unlike global ones).
func (c *Client) RegisterCommands(ctx context.Context) error {
	appID, err := c.AppID(ctx)
	if err != nil {
		return err
	}
	g, err := c.SoleGuild(ctx)
	if err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodPut,
		"/applications/"+appID+"/guilds/"+g.ID+"/commands", dctlCommands())
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// dctlCommands is the declarative slash-command set.
func dctlCommands() []map[string]any {
	const (
		typeSub  = 1
		typeStr  = 3
		typeBool = 5
		typeUser = 6
		typeChan = 7
	)
	return []map[string]any{
		{"name": "set", "description": "dctl settings", "options": []map[string]any{
			{"name": "home", "description": "Set the category/forum holding sessions", "type": typeSub,
				"options": []map[string]any{
					{"name": "channel", "description": "Category or forum", "type": typeChan, "required": true},
				}},
		}},
		{"name": "session", "description": "Manage Claude sessions", "options": []map[string]any{
			{"name": "create", "description": "Create a session", "type": typeSub, "options": []map[string]any{
				{"name": "name", "description": "Session name", "type": typeStr, "required": true},
				{"name": "cmd", "description": "Override bridged command", "type": typeStr},
				{"name": "shared", "description": "Run in the main checkout (no worktree)", "type": typeBool},
			}},
			{"name": "close", "description": "Close a session", "type": typeSub, "options": []map[string]any{
				{"name": "name", "description": "Session name", "type": typeStr, "required": true},
				{"name": "force", "description": "Discard uncommitted worktree changes", "type": typeBool},
			}},
			{"name": "list", "description": "List active sessions", "type": typeSub},
		}},
		{"name": "allow", "description": "Manage the command allowlist", "options": []map[string]any{
			{"name": "add", "description": "Allow a user", "type": typeSub, "options": []map[string]any{
				{"name": "user", "description": "User", "type": typeUser, "required": true}}},
			{"name": "remove", "description": "Disallow a user", "type": typeSub, "options": []map[string]any{
				{"name": "user", "description": "User", "type": typeUser, "required": true}}},
			{"name": "list", "description": "Show the allowlist", "type": typeSub},
		}},
	}
}

// RespondInteraction sends a CHANNEL_MESSAGE_WITH_SOURCE (type 4) reply.
func (c *Client) RespondInteraction(ctx context.Context, id, token string, r Response) error {
	data := map[string]any{"content": r.Content}
	if r.Ephemeral {
		data["flags"] = 1 << 6 // EPHEMERAL
	}
	body := map[string]any{"type": 4, "data": data}
	req, err := c.newRequest(ctx, http.MethodPost,
		"/interactions/"+id+"/"+token+"/callback", body)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// UpsertStatusMessage edits the existing status message (if msgID is set and
// still exists) or sends a new one, returning the live message id.
func (c *Client) UpsertStatusMessage(ctx context.Context, channelID, msgID, content string) (string, error) {
	if msgID != "" {
		req, err := c.newRequest(ctx, http.MethodPatch,
			"/channels/"+channelID+"/messages/"+msgID, map[string]any{"content": content})
		if err == nil {
			if err := c.do(req, nil); err == nil {
				return msgID, nil
			}
		}
		// fall through to re-create if the edit failed (message deleted)
	}
	m, err := c.Send(ctx, channelID, content)
	if err != nil {
		return "", err
	}
	return m.ID, nil
}
