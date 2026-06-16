package dctl

import (
	"context"
	"net/http"

	"github.com/Herrscherd/dctl/internal/transport"
)

// SelectOption is one entry in a select menu: Label is shown in the dropdown,
// Value is what the interaction submits when the entry is chosen.
type SelectOption struct {
	Label       string
	Value       string
	Description string
}

// Components sends message components (select menus) and acks their interactions.
type Components struct {
	rt  transport.Doer
	def *defaults
}

// choiceMenuComponents builds the Discord component tree for a single-select
// dropdown: one ACTION_ROW (type 1) wrapping one STRING_SELECT (type 3).
func choiceMenuComponents(customID string, options []SelectOption) []map[string]any {
	opts := make([]map[string]any, 0, len(options))
	for _, o := range options {
		m := map[string]any{"label": clamp(o.Label, 100), "value": clamp(o.Value, 100)}
		if o.Description != "" {
			m["description"] = clamp(o.Description, 100)
		}
		opts = append(opts, m)
	}
	return []map[string]any{{
		"type": 1,
		"components": []map[string]any{{
			"type":      3,
			"custom_id": customID,
			"options":   opts,
		}},
	}}
}

// SendSelectMenu posts content with a single-select dropdown. When replyTo is
// set the message threads under it; customID routes the click back to the caller.
func (c *Components) SendSelectMenu(ctx context.Context, channelID, replyTo, content, customID string, options []SelectOption) (*Message, error) {
	ch, err := c.def.resolveChannel(channelID)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"content":    content,
		"components": choiceMenuComponents(customID, options),
	}
	if replyTo != "" {
		body["message_reference"] = map[string]any{"message_id": replyTo, "fail_if_not_exists": false}
	}
	var msg Message
	if err := c.rt.Do(ctx, http.MethodPost, "/channels/"+seg(ch)+"/messages", body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// Ack acknowledges a component interaction with an UPDATE_MESSAGE (type 7):
// rewrites the message content and drops the menu so the click is confirmed
// and the dropdown can't be used twice. Must be sent within Discord's 3s deadline.
func (c *Components) Ack(ctx context.Context, id, token, content string) error {
	body := map[string]any{
		"type": 7,
		"data": map[string]any{"content": content, "components": []any{}},
	}
	return c.rt.Do(ctx, http.MethodPost, "/interactions/"+seg(id)+"/"+seg(token)+"/callback", body, nil)
}

// clamp truncates s to at most max runes without splitting a multibyte rune.
func clamp(s string, max int) string {
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
