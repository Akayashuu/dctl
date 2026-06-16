package dctl

import (
	"context"
	"net/http"
	"net/url"

	"github.com/Herrscherd/dctl/internal/transport"
)

// Reactions adds/removes the bot's reactions on a message.
type Reactions struct {
	rt  transport.Doer
	def *defaults
}

// Add reacts to messageID with emoji (unicode or "name:id" for custom).
func (r *Reactions) Add(ctx context.Context, channelID, messageID, emoji string) error {
	return r.do(ctx, http.MethodPut, channelID, messageID, emoji)
}

// Remove removes the bot's own reaction.
func (r *Reactions) Remove(ctx context.Context, channelID, messageID, emoji string) error {
	return r.do(ctx, http.MethodDelete, channelID, messageID, emoji)
}

func (r *Reactions) do(ctx context.Context, method, channelID, messageID, emoji string) error {
	ch, err := r.def.resolveChannel(channelID)
	if err != nil {
		return err
	}
	path := "/channels/" + ch + "/messages/" + messageID + "/reactions/" + url.PathEscape(emoji) + "/@me"
	return r.rt.Do(ctx, method, path, nil, nil)
}
