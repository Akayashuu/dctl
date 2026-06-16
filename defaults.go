package dctl

import (
	"context"
	"errors"
)

// ErrNoChannel is returned when neither an explicit channel nor a default is set.
var ErrNoChannel = errors.New("dctl: no channel (DISCORD_CHANNEL_ID or --channel)")

// defaults resolves the default channel/guild shared across sub-clients.
type defaults struct {
	channel string
	guilds  *Guilds
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
	g, err := d.guilds.Sole(ctx)
	if err != nil {
		return "", err
	}
	return g.ID, nil
}
