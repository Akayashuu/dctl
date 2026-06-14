package dctl

import (
	"context"
	"net/http"
)

// Channel types we create. Discord uses many more; these are the ones dctl drives.
const (
	ChannelText  = 0  // text channel
	ChannelForum = 15 // forum (a container of post-threads)
)

// autoArchive is the thread inactivity timeout in minutes (1 day).
const autoArchive = 1440

// StartThread opens a real Discord thread hanging off an existing message and
// returns the thread (itself a channel — post to thread.ID with Send to talk in
// it). Unlike Reply (a message_reference), this creates a proper threaded
// conversation in the sidebar.
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

// CreateForum creates a forum channel (a space for organised post-threads) in
// guildID (or the sole guild when empty).
func (c *Client) CreateForum(ctx context.Context, guildID, name string) (*Channel, error) {
	return c.createChannel(ctx, guildID, name, ChannelForum)
}

// ForumPost opens a new post (a thread with an initial message) in a forum
// channel and returns the post thread.
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
