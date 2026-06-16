package dctl

import (
	"context"
	"errors"
	"sync"

	"github.com/Herrscherd/dctl/internal/transport"
)

// ErrNoChannel is returned when neither an explicit channel nor a default is set.
var ErrNoChannel = errors.New("dctl: no channel (DISCORD_CHANNEL_ID or --channel)")

// defaults resolves and caches the default channel/guild/app-id shared across
// sub-clients. The bot's application id and sole-guild id are immutable for the
// client's lifetime, so they are fetched once and memoized.
type defaults struct {
	rt      transport.Doer
	channel string
	guilds  *Guilds

	mu       sync.Mutex
	appID    string
	soleGuid string
}

func (d *defaults) resolveChannel(channelID string) (string, error) {
	if channelID != "" {
		return channelID, nil
	}
	if d.channel == "" {
		return "", ErrNoChannel
	}
	return d.channel, nil
}

func (d *defaults) resolveGuild(ctx context.Context, guildID string) (string, error) {
	if guildID != "" {
		return guildID, nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.soleGuid != "" {
		return d.soleGuid, nil
	}
	g, err := d.guilds.Sole(ctx)
	if err != nil {
		return "", err
	}
	d.soleGuid = g.ID
	return d.soleGuid, nil
}

func (d *defaults) appIDOnce(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.appID != "" {
		return d.appID, nil
	}
	id, err := fetchAppID(ctx, d.rt)
	if err != nil {
		return "", err
	}
	d.appID = id
	return id, nil
}
