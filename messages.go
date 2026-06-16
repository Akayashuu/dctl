package dctl

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Herrscherd/dctl/internal/transport"
)

// Messages CRUDs channel messages.
type Messages struct {
	rt  transport.Doer
	def *defaults
}

// Send posts content to channelID (or the default channel when empty).
func (m *Messages) Send(ctx context.Context, channelID, content string) (*Message, error) {
	return m.post(ctx, channelID, map[string]any{"content": content})
}

// Reply posts content as a reply to messageID in channelID (or the default channel).
func (m *Messages) Reply(ctx context.Context, channelID, messageID, content string) (*Message, error) {
	return m.post(ctx, channelID, map[string]any{
		"content":           content,
		"message_reference": map[string]any{"message_id": messageID, "fail_if_not_exists": false},
	})
}

func (m *Messages) post(ctx context.Context, channelID string, body map[string]any) (*Message, error) {
	ch, err := m.def.resolveChannel(channelID)
	if err != nil {
		return nil, err
	}
	var msg Message
	if err := m.rt.Do(ctx, http.MethodPost, "/channels/"+seg(ch)+"/messages", body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// Read returns up to limit (1..100, default 50) recent messages from channelID
// (or the default channel), oldest-first. When after is non-empty, only messages
// strictly newer than that id are returned (for polling).
func (m *Messages) Read(ctx context.Context, channelID string, limit int, after string) ([]Message, error) {
	ch, err := m.def.resolveChannel(channelID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	if after != "" {
		q.Set("after", after)
	}
	path := "/channels/" + seg(ch) + "/messages?" + q.Encode()
	var msgs []Message
	if err := m.rt.Do(ctx, http.MethodGet, path, nil, &msgs); err != nil {
		return nil, err
	}
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

func (m *Messages) Edit(ctx context.Context, channelID, messageID, content string) (*Message, error) {
	ch, err := m.def.resolveChannel(channelID)
	if err != nil {
		return nil, err
	}
	var msg Message
	if err := m.rt.Do(ctx, http.MethodPatch, "/channels/"+seg(ch)+"/messages/"+seg(messageID),
		map[string]any{"content": content}, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (m *Messages) Delete(ctx context.Context, channelID, messageID string) error {
	ch, err := m.def.resolveChannel(channelID)
	if err != nil {
		return err
	}
	return m.rt.Do(ctx, http.MethodDelete, "/channels/"+seg(ch)+"/messages/"+seg(messageID), nil, nil)
}

// LastMessageAt returns the timestamp of the channel's most recent message, or
// the zero Time if the channel has no messages.
func (m *Messages) LastMessageAt(ctx context.Context, channelID string) (time.Time, error) {
	msgs, err := m.Read(ctx, channelID, 1, "")
	if err != nil {
		return time.Time{}, err
	}
	if len(msgs) == 0 {
		return time.Time{}, nil
	}
	ts, err := time.Parse(time.RFC3339, msgs[len(msgs)-1].Timestamp)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse message timestamp %q: %w", msgs[len(msgs)-1].Timestamp, err)
	}
	return ts, nil
}
