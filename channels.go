package dctl

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Guild is a Discord server the bot belongs to.
type Guild struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Channel is a Discord channel. Type 0 is a text channel.
type Channel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    int    `json:"type"`
	GuildID string `json:"guild_id,omitempty"`
}

// Guilds lists the servers the bot is a member of.
func (c *Client) Guilds(ctx context.Context) ([]Guild, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/users/@me/guilds", nil)
	if err != nil {
		return nil, err
	}
	var gs []Guild
	if err := c.do(req, &gs); err != nil {
		return nil, err
	}
	return gs, nil
}

// SoleGuild resolves the bot's single server (mono-server). It errors if the
// bot is in zero or several guilds, so callers don't silently target the wrong
// one.
func (c *Client) SoleGuild(ctx context.Context) (Guild, error) {
	gs, err := c.Guilds(ctx)
	if err != nil {
		return Guild{}, err
	}
	switch len(gs) {
	case 0:
		return Guild{}, fmt.Errorf("dctl: bot is in no server (invite it first)")
	case 1:
		return gs[0], nil
	default:
		return Guild{}, fmt.Errorf("dctl: bot is in %d servers; pass a guild id", len(gs))
	}
}

// Channels lists the channels of a guild (or the sole guild when guildID is empty).
func (c *Client) Channels(ctx context.Context, guildID string) ([]Channel, error) {
	gid, err := c.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/guilds/"+gid+"/channels", nil)
	if err != nil {
		return nil, err
	}
	var chs []Channel
	if err := c.do(req, &chs); err != nil {
		return nil, err
	}
	return chs, nil
}

// CreateChannel creates a text channel named name in guildID (or the sole guild
// when empty) and returns it.
func (c *Client) CreateChannel(ctx context.Context, guildID, name string) (*Channel, error) {
	gid, err := c.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/guilds/"+gid+"/channels", map[string]any{"name": name, "type": 0})
	if err != nil {
		return nil, err
	}
	var ch Channel
	if err := c.do(req, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// DeleteChannel deletes a channel by id.
func (c *Client) DeleteChannel(ctx context.Context, channelID string) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	if channelID == "" {
		return ErrNoChannel
	}
	req, err := c.newRequest(ctx, http.MethodDelete, "/channels/"+channelID, nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// EnsureChannel returns the id of the text channel named name in the guild,
// creating it if no channel by that name exists. Matching is case-insensitive.
// Use it to guarantee a default channel exists before posting.
func (c *Client) EnsureChannel(ctx context.Context, guildID, name string) (*Channel, error) {
	chs, err := c.Channels(ctx, guildID)
	if err != nil {
		return nil, err
	}
	for i := range chs {
		if chs[i].Type == 0 && strings.EqualFold(chs[i].Name, name) {
			return &chs[i], nil
		}
	}
	return c.CreateChannel(ctx, guildID, name)
}

func (c *Client) resolveGuild(ctx context.Context, guildID string) (string, error) {
	if !c.Enabled() {
		return "", ErrDisabled
	}
	if guildID != "" {
		return guildID, nil
	}
	g, err := c.SoleGuild(ctx)
	if err != nil {
		return "", err
	}
	return g.ID, nil
}
