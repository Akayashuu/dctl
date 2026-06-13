// Package dctl is a minimal, dependency-free client for the Discord bot REST
// API (v10). It powers both the `dctl` CLI (token-frugal Discord access for an
// AI agent) and the prospector backend's Discord bridge — one library, two
// consumers. Mono-server by design: a single bot token plus a default channel.
//
// Auth is a bot token (DISCORD_BOT_TOKEN) sent as `Authorization: Bot <token>`.
// No gateway/websocket — every call is on-demand HTTP, which suits agent-driven
// usage (send/read/reply) and best-effort notification fan-out.
package dctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIBase is the Discord REST API root.
const APIBase = "https://discord.com/api/v10"

// ErrDisabled is returned by every method when no bot token is configured.
var ErrDisabled = errors.New("dctl: no bot token (DISCORD_BOT_TOKEN)")

// ErrNoChannel is returned when neither an explicit channel nor a default one is set.
var ErrNoChannel = errors.New("dctl: no channel (DISCORD_CHANNEL_ID or --channel)")

// Client talks to the Discord bot REST API. Build it with New; the zero value
// is unusable. A client with an empty token returns ErrDisabled from every call
// so consumers can stay oblivious to whether the feature is on.
type Client struct {
	token          string
	defaultChannel string
	http           *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the default 15s-timeout HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New builds a Client. token is the bot token (kept in memory only, never
// logged). defaultChannel is the channel that send/read/reply target when no
// explicit channel id is passed.
func New(token, defaultChannel string, opts ...Option) *Client {
	c := &Client{
		token:          token,
		defaultChannel: defaultChannel,
		http:           &http.Client{Timeout: 15 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Enabled reports whether a bot token is configured.
func (c *Client) Enabled() bool { return c != nil && c.token != "" }

// DefaultChannel returns the configured fan-out / fallback channel id.
func (c *Client) DefaultChannel() string {
	if c == nil {
		return ""
	}
	return c.defaultChannel
}

// Message is the subset of a Discord message we surface.
type Message struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Content   string `json:"content"`
	Author    Author `json:"author"`
	Timestamp string `json:"timestamp"`
}

// Author identifies who wrote a message.
type Author struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bot      bool   `json:"bot"`
}

// Send posts content to channelID (or the default channel when empty) and
// returns the created message.
func (c *Client) Send(ctx context.Context, channelID, content string) (*Message, error) {
	ch, err := c.resolveChannel(channelID)
	if err != nil {
		return nil, err
	}
	return c.post(ctx, ch, map[string]any{"content": content})
}

// Reply posts content as a threaded reply to messageID in channelID (or the
// default channel).
func (c *Client) Reply(ctx context.Context, channelID, messageID, content string) (*Message, error) {
	ch, err := c.resolveChannel(channelID)
	if err != nil {
		return nil, err
	}
	return c.post(ctx, ch, map[string]any{
		"content":           content,
		"message_reference": map[string]any{"message_id": messageID, "fail_if_not_exists": false},
	})
}

// Read returns up to limit (1..100, default 50) recent messages from channelID
// (or the default channel), oldest-first (chronological — natural to read).
// When after is non-empty, only messages strictly newer than that id are
// returned (for polling).
func (c *Client) Read(ctx context.Context, channelID string, limit int, after string) ([]Message, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	ch, err := c.resolveChannel(channelID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	path := fmt.Sprintf("/channels/%s/messages?limit=%d", ch, limit)
	if after != "" {
		path += "&after=" + after
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var msgs []Message
	if err := c.do(req, &msgs); err != nil {
		return nil, err
	}
	// Discord returns newest-first; reverse to chronological order.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

func (c *Client) post(ctx context.Context, channelID string, body map[string]any) (*Message, error) {
	if !c.Enabled() {
		return nil, ErrDisabled
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/channels/"+channelID+"/messages", body)
	if err != nil {
		return nil, err
	}
	var msg Message
	if err := c.do(req, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (c *Client) resolveChannel(channelID string) (string, error) {
	if channelID != "" {
		return channelID, nil
	}
	if c.defaultChannel == "" {
		return "", ErrNoChannel
	}
	return c.defaultChannel, nil
}

func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, APIBase+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+c.token)
	req.Header.Set("User-Agent", "dctl (https://github.com/vskstudio/dctl, 1.0)")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	return json.Unmarshal(respBody, out)
}
