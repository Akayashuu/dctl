package dctl

import (
	"context"
	"net/http"

	"github.com/Herrscherd/dctl/internal/transport"
)

// Webhook is a Discord channel webhook.
type Webhook struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token Secret `json:"token"`
}

// Webhooks CRUDs and executes channel webhooks.
type Webhooks struct {
	rt transport.Doer
}

func (w *Webhooks) Create(ctx context.Context, channelID, name string) (*Webhook, error) {
	var hook Webhook
	if err := w.rt.Do(ctx, http.MethodPost, "/channels/"+seg(channelID)+"/webhooks",
		map[string]any{"name": name}, &hook); err != nil {
		return nil, err
	}
	return &hook, nil
}

func (w *Webhooks) List(ctx context.Context, channelID string) ([]Webhook, error) {
	var hooks []Webhook
	if err := w.rt.Do(ctx, http.MethodGet, "/channels/"+seg(channelID)+"/webhooks", nil, &hooks); err != nil {
		return nil, err
	}
	return hooks, nil
}

func (w *Webhooks) Delete(ctx context.Context, webhookID string) error {
	return w.rt.Do(ctx, http.MethodDelete, "/webhooks/"+seg(webhookID), nil, nil)
}

// Execute posts content through a webhook using its id+token.
func (w *Webhooks) Execute(ctx context.Context, webhookID, token, content string) error {
	return w.rt.Do(ctx, http.MethodPost, "/webhooks/"+seg(webhookID)+"/"+seg(token),
		map[string]any{"content": content}, nil)
}
