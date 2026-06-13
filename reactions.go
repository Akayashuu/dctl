package dctl

import (
	"context"
	"net/http"
	"net/url"
)

// React adds emoji as a reaction (from the bot) to messageID in channelID (or
// the default channel). emoji is a raw unicode emoji ("👀") or a custom emoji
// in "name:id" form. Best-effort: needs the bot's Add Reactions permission.
func (c *Client) React(ctx context.Context, channelID, messageID, emoji string) error {
	return c.reaction(ctx, http.MethodPut, channelID, messageID, emoji)
}

// Unreact removes the bot's own emoji reaction from messageID.
func (c *Client) Unreact(ctx context.Context, channelID, messageID, emoji string) error {
	return c.reaction(ctx, http.MethodDelete, channelID, messageID, emoji)
}

func (c *Client) reaction(ctx context.Context, method, channelID, messageID, emoji string) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	ch, err := c.resolveChannel(channelID)
	if err != nil {
		return err
	}
	path := "/channels/" + ch + "/messages/" + messageID +
		"/reactions/" + url.PathEscape(emoji) + "/@me"
	req, err := c.newRequest(ctx, method, path, nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}
