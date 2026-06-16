package dctl

import (
	"context"
	"net/http"
)

// Interaction type constants we care about (Discord interaction-type field).
const (
	InteractionCommand      = 2 // APPLICATION_COMMAND
	InteractionComponent    = 3 // MESSAGE_COMPONENT (button/select click)
	InteractionAutocomplete = 4 // APPLICATION_COMMAND_AUTOCOMPLETE
)

// Interaction is the subset of a Discord INTERACTION_CREATE we handle
// (application slash commands, type 2; autocomplete requests, type 4).
type Interaction struct {
	ID        string          `json:"id"`
	Type      int             `json:"type"`
	Token     string          `json:"token"`
	GuildID   string          `json:"guild_id"`
	ChannelID string          `json:"channel_id"`
	Member    Member          `json:"member"`
	Data      InteractionData `json:"data"`
}

// Member carries the invoking user (interactions in a guild come via member.user).
type Member struct {
	User Author `json:"user"`
}

// InteractionData is the invoked command + its options. For a component
// interaction (type 3) the command fields are empty and CustomID/Values carry
// the clicked component's id and the selected value(s) instead.
type InteractionData struct {
	Name     string              `json:"name"`
	Options  []InteractionOption `json:"options"`
	CustomID string              `json:"custom_id"`
	Values   []string            `json:"values"`
}

// InteractionOption is one command option; for subcommands, Options nests.
// Focused is set on the single option the user is currently typing in an
// autocomplete interaction.
type InteractionOption struct {
	Name    string              `json:"name"`
	Type    int                 `json:"type"`
	Value   any                 `json:"value"`
	Focused bool                `json:"focused"`
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

// OptBool returns the bool value of a (possibly nested) option, false if absent.
func (d InteractionData) OptBool(name string) bool {
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

// Focused returns the name and current (partial) string value of the option the
// user is typing in an autocomplete interaction, searching nested subcommands.
func (d InteractionData) Focused() (name, value string, ok bool) {
	return findFocused(d.Options)
}

func findFocused(opts []InteractionOption) (string, string, bool) {
	for _, o := range opts {
		if o.Focused {
			s, _ := o.Value.(string)
			return o.Name, s, true
		}
		if n, v, ok := findFocused(o.Options); ok {
			return n, v, ok
		}
	}
	return "", "", false
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

// RegisterCommands (re)registers the given guild-scoped slash command set for the
// sole guild (guild-scoped commands appear instantly, unlike global ones).
func (c *Client) RegisterCommands(ctx context.Context, commands []map[string]any) error {
	appID, err := c.AppID(ctx)
	if err != nil {
		return err
	}
	gid, err := c.resolveGuild(ctx, "")
	if err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodPut,
		"/applications/"+appID+"/guilds/"+gid+"/commands", commands)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// RespondInteraction sends a CHANNEL_MESSAGE_WITH_SOURCE (type 4) reply.
func (c *Client) RespondInteraction(ctx context.Context, id, token string, r Response) error {
	data := map[string]any{"content": r.Content, "allowed_mentions": noMentions}
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

// DeferInteraction acknowledges an interaction with a DEFERRED_CHANNEL_MESSAGE_
// WITH_SOURCE (type 5) so the daemon has up to 15 minutes to produce the real
// reply (slow clones/network) instead of Discord's 3s callback deadline. The
// ephemeral flag must match the eventual reply's visibility.
func (c *Client) DeferInteraction(ctx context.Context, id, token string, ephemeral bool) error {
	data := map[string]any{}
	if ephemeral {
		data["flags"] = 1 << 6 // EPHEMERAL
	}
	body := map[string]any{"type": 5, "data": data}
	req, err := c.newRequest(ctx, http.MethodPost,
		"/interactions/"+id+"/"+token+"/callback", body)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// AutocompleteChoice is one suggestion returned for an autocomplete interaction.
// Name is shown in the picker; Value is what gets submitted.
type AutocompleteChoice struct {
	Name  string
	Value string
}

// RespondAutocomplete sends an APPLICATION_COMMAND_AUTOCOMPLETE_RESULT (type 8)
// reply carrying the suggestion list. Discord accepts at most 25 choices; extras
// are dropped here so callers need not pre-trim.
func (c *Client) RespondAutocomplete(ctx context.Context, id, token string, choices []AutocompleteChoice) error {
	if len(choices) > 25 {
		choices = choices[:25]
	}
	cs := make([]map[string]any, 0, len(choices))
	for _, ch := range choices {
		cs = append(cs, map[string]any{"name": ch.Name, "value": ch.Value})
	}
	body := map[string]any{"type": 8, "data": map[string]any{"choices": cs}}
	req, err := c.newRequest(ctx, http.MethodPost,
		"/interactions/"+id+"/"+token+"/callback", body)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// EditInteractionResponse fills in the deferred reply by editing the original
// interaction response via the webhook endpoint. appID is the bot's application
// id (see AppID); the interaction token authorizes the edit for ~15 minutes.
func (c *Client) EditInteractionResponse(ctx context.Context, appID, token string, r Response) error {
	req, err := c.newRequest(ctx, http.MethodPatch,
		"/webhooks/"+appID+"/"+token+"/messages/@original",
		map[string]any{"content": r.Content, "allowed_mentions": noMentions})
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
			"/channels/"+channelID+"/messages/"+msgID, map[string]any{"content": content, "allowed_mentions": noMentions})
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
