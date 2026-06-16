package dctl

import (
	"context"
	"errors"
	"net/http"

	"github.com/Herrscherd/dctl/internal/transport"
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

// AutocompleteChoice is one suggestion returned for an autocomplete interaction.
// Name is shown in the picker; Value is what gets submitted.
type AutocompleteChoice struct {
	Name  string
	Value string
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

// Interactions is the sub-client for Discord interaction endpoints.
type Interactions struct {
	rt  transport.Doer
	def *defaults
}

// AppID returns the bot's application id (== bot user id) via /users/@me.
func (in *Interactions) AppID(ctx context.Context) (string, error) {
	var u struct {
		ID string `json:"id"`
	}
	if err := in.rt.Do(ctx, http.MethodGet, "/users/@me", nil, &u); err != nil {
		return "", err
	}
	return u.ID, nil
}

// RegisterCommands (re)registers the given guild-scoped slash command set for the
// sole guild (guild-scoped commands appear instantly, unlike global ones).
func (in *Interactions) RegisterCommands(ctx context.Context, commands []map[string]any) error {
	appID, err := in.AppID(ctx)
	if err != nil {
		return err
	}
	gid, err := in.def.resolveGuild(ctx, "")
	if err != nil {
		return err
	}
	return in.rt.Do(ctx, http.MethodPut, "/applications/"+seg(appID)+"/guilds/"+seg(gid)+"/commands", commands, nil)
}

// Respond sends a CHANNEL_MESSAGE_WITH_SOURCE (type 4) reply.
func (in *Interactions) Respond(ctx context.Context, id, token string, r Response) error {
	data := map[string]any{"content": r.Content}
	if r.Ephemeral {
		data["flags"] = 1 << 6
	}
	return in.rt.Do(ctx, http.MethodPost, "/interactions/"+seg(id)+"/"+seg(token)+"/callback",
		map[string]any{"type": 4, "data": data}, nil)
}

// Defer acknowledges an interaction with a DEFERRED_CHANNEL_MESSAGE_WITH_SOURCE
// (type 5). The ephemeral flag must match the eventual reply's visibility.
func (in *Interactions) Defer(ctx context.Context, id, token string, ephemeral bool) error {
	data := map[string]any{}
	if ephemeral {
		data["flags"] = 1 << 6
	}
	return in.rt.Do(ctx, http.MethodPost, "/interactions/"+seg(id)+"/"+seg(token)+"/callback",
		map[string]any{"type": 5, "data": data}, nil)
}

// RespondAutocomplete sends an APPLICATION_COMMAND_AUTOCOMPLETE_RESULT (type 8)
// reply carrying the suggestion list. Discord accepts at most 25 choices; extras
// are dropped here so callers need not pre-trim.
func (in *Interactions) RespondAutocomplete(ctx context.Context, id, token string, choices []AutocompleteChoice) error {
	if len(choices) > 25 {
		choices = choices[:25]
	}
	cs := make([]map[string]any, 0, len(choices))
	for _, ch := range choices {
		cs = append(cs, map[string]any{"name": ch.Name, "value": ch.Value})
	}
	return in.rt.Do(ctx, http.MethodPost, "/interactions/"+seg(id)+"/"+seg(token)+"/callback",
		map[string]any{"type": 8, "data": map[string]any{"choices": cs}}, nil)
}

// EditResponse fills in the deferred reply by editing the original interaction
// response via the webhook endpoint. appID is the bot's application id (see
// AppID); the interaction token authorizes the edit for ~15 minutes.
func (in *Interactions) EditResponse(ctx context.Context, appID, token string, r Response) error {
	return in.rt.Do(ctx, http.MethodPatch, "/webhooks/"+seg(appID)+"/"+seg(token)+"/messages/@original",
		map[string]any{"content": r.Content}, nil)
}

// UpsertStatusMessage edits the existing status message or sends a new one,
// returning the live message id.
func (in *Interactions) UpsertStatusMessage(ctx context.Context, channelID, msgID, content string) (string, error) {
	if msgID != "" {
		err := in.rt.Do(ctx, http.MethodPatch, "/channels/"+seg(channelID)+"/messages/"+seg(msgID),
			map[string]any{"content": content}, nil)
		if err == nil {
			return msgID, nil
		}
		var apiErr *transport.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound {
			return "", err
		}
	}
	var msg Message
	if err := in.rt.Do(ctx, http.MethodPost, "/channels/"+seg(channelID)+"/messages",
		map[string]any{"content": content}, &msg); err != nil {
		return "", err
	}
	return msg.ID, nil
}
