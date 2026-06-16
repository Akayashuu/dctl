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
	req.Header.Set("User-Agent", "dctl (https://github.com/Herrscherd/dctl, 1.0)")
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
