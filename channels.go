package dctl

import (
	"context"
	"net/http"
	"strings"

	"github.com/Herrscherd/dctl/internal/transport"
)

// Channel type constants.
const (
	ChannelText          = 0
	ChannelCategory      = 4
	ChannelNewsThread    = 10
	ChannelPublicThread  = 11
	ChannelPrivateThread = 12
	ChannelForum         = 15
)

// Channels CRUDs guild channels.
type Channels struct {
	rt  transport.Doer
	def *defaults
}

// List returns the channels of a guild (or the sole guild when guildID is empty).
func (c *Channels) List(ctx context.Context, guildID string) ([]Channel, error) {
	gid, err := c.def.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	var chs []Channel
	if err := c.rt.Do(ctx, http.MethodGet, "/guilds/"+seg(gid)+"/channels", nil, &chs); err != nil {
		return nil, err
	}
	return chs, nil
}

// Get returns a channel by id.
func (c *Channels) Get(ctx context.Context, channelID string) (*Channel, error) {
	var ch Channel
	if err := c.rt.Do(ctx, http.MethodGet, "/channels/"+seg(channelID), nil, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// Type returns the Discord channel-type integer for channelID.
func (c *Channels) Type(ctx context.Context, channelID string) (int, error) {
	ch, err := c.Get(ctx, channelID)
	if err != nil {
		return 0, err
	}
	return ch.Type, nil
}

// Create creates a text channel named name in guildID (or the sole guild).
func (c *Channels) Create(ctx context.Context, guildID, name string) (*Channel, error) {
	return c.create(ctx, guildID, map[string]any{"name": name, "type": ChannelText})
}

// CreateUnder creates a text channel nested under category parentID, in the sole guild.
func (c *Channels) CreateUnder(ctx context.Context, parentID, name string) (*Channel, error) {
	return c.create(ctx, "", map[string]any{"name": name, "type": ChannelText, "parent_id": parentID})
}

func (c *Channels) create(ctx context.Context, guildID string, body map[string]any) (*Channel, error) {
	gid, err := c.def.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	var ch Channel
	if err := c.rt.Do(ctx, http.MethodPost, "/guilds/"+seg(gid)+"/channels", body, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// Rename updates a channel's name.
func (c *Channels) Rename(ctx context.Context, channelID, name string) (*Channel, error) {
	return c.Update(ctx, channelID, map[string]any{"name": name})
}

// Update PATCHes arbitrary channel fields (name, parent_id, topic, position…).
func (c *Channels) Update(ctx context.Context, channelID string, fields map[string]any) (*Channel, error) {
	var ch Channel
	if err := c.rt.Do(ctx, http.MethodPatch, "/channels/"+seg(channelID), fields, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// Delete deletes a channel by id.
func (c *Channels) Delete(ctx context.Context, channelID string) error {
	if channelID == "" {
		return ErrNoChannel
	}
	return c.rt.Do(ctx, http.MethodDelete, "/channels/"+seg(channelID), nil, nil)
}

// Ensure returns the text channel named name in the guild, creating it if absent.
// Matching is case-insensitive.
func (c *Channels) Ensure(ctx context.Context, guildID, name string) (*Channel, error) {
	chs, err := c.List(ctx, guildID)
	if err != nil {
		return nil, err
	}
	for i := range chs {
		if chs[i].Type == ChannelText && strings.EqualFold(chs[i].Name, name) {
			return &chs[i], nil
		}
	}
	return c.Create(ctx, guildID, name)
}

// Archive archives a thread/forum-post, or deletes a plain text channel
// (text channels don't support PATCH {archived:true}).
func (c *Channels) Archive(ctx context.Context, channelID string) error {
	ct, err := c.Type(ctx, channelID)
	if err != nil {
		return err
	}
	if ct == ChannelNewsThread || ct == ChannelPublicThread || ct == ChannelPrivateThread {
		return c.rt.Do(ctx, http.MethodPatch, "/channels/"+seg(channelID), map[string]any{"archived": true}, nil)
	}
	return c.Delete(ctx, channelID)
}
