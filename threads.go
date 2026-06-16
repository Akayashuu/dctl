package dctl

import (
	"context"
	"net/http"
)

const autoArchive = 1440

// StartThread opens a real Discord thread hanging off an existing message and
// returns the thread.
func (c *Client) StartThread(ctx context.Context, channelID, messageID, name string) (*Channel, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	ch, err := c.resolveChannel(channelID)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost,
		"/channels/"+ch+"/messages/"+messageID+"/threads",
		map[string]any{"name": name, "auto_archive_duration": autoArchive})
	if err != nil {
		return nil, err
	}
	var t Channel
	if err := c.do(req, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateForum creates a forum channel in guildID (or the sole guild when empty).
func (c *Client) CreateForum(ctx context.Context, guildID, name string) (*Channel, error) {
	gid, err := c.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/guilds/"+gid+"/channels",
		map[string]any{"name": name, "type": ChannelForum})
	if err != nil {
		return nil, err
	}
	var ch Channel
	if err := c.do(req, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// ForumPost opens a new post in a forum channel and returns the post thread.
func (c *Client) ForumPost(ctx context.Context, forumID, name, content string) (*Channel, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	if forumID == "" {
		return nil, ErrNoChannel
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/channels/"+forumID+"/threads",
		map[string]any{"name": name, "message": map[string]any{"content": content, "allowed_mentions": noMentions}})
	if err != nil {
		return nil, err
	}
	var t Channel
	if err := c.do(req, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
