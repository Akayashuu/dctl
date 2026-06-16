package dctl

import (
	"context"
	"net/http"

	"github.com/Herrscherd/dctl/internal/transport"
)

// Threads creates threads and forum posts.
type Threads struct {
	rt  transport.Doer
	def *defaults
}

// Start opens a public thread off messageID in channelID.
// auto_archive_duration is set to 1440 minutes (24 h).
func (t *Threads) Start(ctx context.Context, channelID, messageID, name string) (*Channel, error) {
	var ch Channel
	if err := t.rt.Do(ctx, http.MethodPost, "/channels/"+channelID+"/messages/"+messageID+"/threads",
		map[string]any{"name": name, "auto_archive_duration": autoArchive}, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// CreateForum creates a forum channel named name in guildID (or the sole guild).
func (t *Threads) CreateForum(ctx context.Context, guildID, name string) (*Channel, error) {
	gid, err := t.def.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	var ch Channel
	if err := t.rt.Do(ctx, http.MethodPost, "/guilds/"+gid+"/channels",
		map[string]any{"name": name, "type": ChannelForum}, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// ForumPost creates a thread (post) in forum forumID with an initial message.
func (t *Threads) ForumPost(ctx context.Context, forumID, name, content string) (*Channel, error) {
	var ch Channel
	if err := t.rt.Do(ctx, http.MethodPost, "/channels/"+forumID+"/threads",
		map[string]any{"name": name, "message": map[string]any{"content": content}}, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

const autoArchive = 1440
