package dctl

import (
	"context"
	"net/http"
	"strings"
)

// choiceCustomIDPrefix tags a select-menu interaction as a dctl choice prompt so
// the daemon can recognize the click and route it to the owning session's bridge
// instead of treating it as a slash command.
const choiceCustomIDPrefix = "dctlchoice:"

// ChoiceCustomID builds the custom_id carried by a session's choice select menu.
// The session name lets the daemon find the bridge to forward the pick to.
func ChoiceCustomID(session string) string { return choiceCustomIDPrefix + session }

// ParseChoiceCustomID extracts the session name from a choice-menu custom_id and
// reports whether the id is a dctl choice menu at all (vs. some other component).
func ParseChoiceCustomID(id string) (string, bool) {
	return strings.CutPrefix(id, choiceCustomIDPrefix)
}

// SelectOption is one entry in a select menu: Label is shown in the dropdown,
// Value is what the interaction submits when the entry is chosen.
type SelectOption struct {
	Label       string
	Value       string
	Description string
}

// choiceMenuComponents builds the Discord component tree for a single-select
// dropdown: one ACTION_ROW (type 1) wrapping one STRING_SELECT (type 3). Label,
// value, and description are clamped to Discord's 100-char ceiling (rune-safe so
// a clamp never yields invalid UTF-8, which Discord rejects).
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
		"type": 1, // ACTION_ROW
		"components": []map[string]any{{
			"type":      3, // STRING_SELECT
			"custom_id": customID,
			"options":   opts,
		}},
	}}
}

// SendSelectMenu posts content with a single-select dropdown. When replyTo is set
// the message threads under it; customID routes the click back to a session.
func (c *Client) SendSelectMenu(ctx context.Context, channelID, replyTo, content, customID string, options []SelectOption) (*Message, error) {
	ch, err := c.resolveChannel(channelID)
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
	return c.post(ctx, ch, body)
}

// AckComponent acknowledges a component interaction with an UPDATE_MESSAGE
// (type 7): it rewrites the message content and drops the menu, so the click is
// confirmed and the dropdown can't be used twice. Must be sent within Discord's
// 3s callback deadline.
func (c *Client) AckComponent(ctx context.Context, id, token, content string) error {
	body := map[string]any{
		"type": 7, // UPDATE_MESSAGE
		"data": map[string]any{"content": content, "components": []any{}, "allowed_mentions": noMentions},
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/interactions/"+id+"/"+token+"/callback", body)
	if err != nil {
		return err
	}
	return c.do(req, nil)
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
