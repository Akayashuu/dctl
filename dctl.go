package dctl

import (
	"net/http"

	"github.com/Herrscherd/dctl/internal/transport"
)

// Client is the dctl façade: it wires the HTTP transport into per-resource
// sub-clients sharing one default channel/guild resolver. Build it with New.
type Client struct {
	rt           transport.Doer
	def          *defaults
	guilds       *Guilds
	interactions *Interactions
}

// ClientOption configures a Client.
type ClientOption func(*clientConfig)

type clientConfig struct {
	httpClient *http.Client
}

// WithHTTPClient overrides the default 15s-timeout HTTP client.
func WithHTTPClient(h *http.Client) ClientOption {
	return func(c *clientConfig) { c.httpClient = h }
}

// New builds a Client. token is the bot token (kept in memory only). defaultChannel
// is the channel that message ops target when no explicit channel id is passed.
func New(token, defaultChannel string, opts ...ClientOption) *Client {
	cfg := &clientConfig{}
	for _, o := range opts {
		o(cfg)
	}
	var topts []transport.Option
	if cfg.httpClient != nil {
		topts = append(topts, transport.WithHTTPClient(cfg.httpClient))
	}
	rt := transport.NewHTTP(token, topts...)
	return newWith(rt, defaultChannel)
}

// newWith wires a Client around an arbitrary Doer (used by tests with a stub).
func newWith(rt transport.Doer, defaultChannel string) *Client {
	guilds := &Guilds{rt: rt}
	def := &defaults{rt: rt, channel: defaultChannel, guilds: guilds}
	return &Client{
		rt:           rt,
		def:          def,
		guilds:       guilds,
		interactions: &Interactions{rt: rt, def: def},
	}
}

// Enabled reports whether the underlying transport is configured.
func (c *Client) Enabled() bool { return c != nil && c.rt.Enabled() }

// DefaultChannel returns the configured default channel id.
func (c *Client) DefaultChannel() string {
	if c == nil {
		return ""
	}
	return c.def.channel
}

// Sub-client accessors. Each shares the transport and (where relevant) the
// default channel/guild resolver.
func (c *Client) Guilds() *Guilds             { return c.guilds }
func (c *Client) Messages() *Messages         { return &Messages{rt: c.rt, def: c.def} }
func (c *Client) Channels() *Channels         { return &Channels{rt: c.rt, def: c.def} }
func (c *Client) Roles() *Roles               { return &Roles{rt: c.rt, def: c.def} }
func (c *Client) Members() *Members           { return &Members{rt: c.rt, def: c.def} }
func (c *Client) Reactions() *Reactions       { return &Reactions{rt: c.rt, def: c.def} }
func (c *Client) Threads() *Threads           { return &Threads{rt: c.rt, def: c.def} }
func (c *Client) Permissions() *Permissions   { return &Permissions{rt: c.rt} }
func (c *Client) Webhooks() *Webhooks         { return &Webhooks{rt: c.rt} }
func (c *Client) Interactions() *Interactions { return c.interactions }
func (c *Client) Components() *Components     { return &Components{rt: c.rt, def: c.def} }
