package dctl

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

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
	return c.createChannel(ctx, guildID, name, ChannelText)
}

// createChannel creates a channel of the given type (see ChannelText/ChannelForum).
func (c *Client) createChannel(ctx context.Context, guildID, name string, chType int) (*Channel, error) {
	gid, err := c.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/guilds/"+gid+"/channels", map[string]any{"name": name, "type": chType})
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

// ChannelType returns the Discord channel-type integer for channelID.
func (c *Client) ChannelType(ctx context.Context, channelID string) (int, error) {
	if !c.Enabled() {
		return 0, ErrDisabled
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/channels/"+channelID, nil)
	if err != nil {
		return 0, err
	}
	var ch Channel
	if err := c.do(req, &ch); err != nil {
		return 0, err
	}
	return ch.Type, nil
}

// CreateChannelUnder creates a text channel named name nested under category
// parentID, in the sole guild.
func (c *Client) CreateChannelUnder(ctx context.Context, parentID, name string) (*Channel, error) {
	gid, err := c.resolveGuild(ctx, "")
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/guilds/"+gid+"/channels",
		map[string]any{"name": name, "type": ChannelText, "parent_id": parentID})
	if err != nil {
		return nil, err
	}
	var ch Channel
	if err := c.do(req, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// ArchiveChannel archives a thread/forum-post, or deletes a plain text channel.
// Threads support PATCH {archived:true}; text channels do not, so they are deleted.
func (c *Client) ArchiveChannel(ctx context.Context, channelID string) error {
	ct, err := c.ChannelType(ctx, channelID)
	if err != nil {
		return err
	}
	// Thread types: 10 (announcement), 11 (public/forum post), 12 (private).
	if ct == 10 || ct == 11 || ct == 12 {
		req, err := c.newRequest(ctx, http.MethodPatch, "/channels/"+channelID,
			map[string]any{"archived": true})
		if err != nil {
			return err
		}
		return c.do(req, nil)
	}
	return c.DeleteChannel(ctx, channelID)
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
