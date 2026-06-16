package dctl

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Herrscherd/dctl/internal/transport"
)

// Guilds lists and resolves the bot's servers.
type Guilds struct {
	rt transport.Doer
}

// List returns the servers the bot is a member of.
func (g *Guilds) List(ctx context.Context) ([]Guild, error) {
	var gs []Guild
	if err := g.rt.Do(ctx, http.MethodGet, "/users/@me/guilds", nil, &gs); err != nil {
		return nil, err
	}
	return gs, nil
}

// Sole resolves the bot's single server (mono-server). Errors if the bot is in
// zero or several guilds, so callers never silently target the wrong one.
func (g *Guilds) Sole(ctx context.Context) (Guild, error) {
	gs, err := g.List(ctx)
	if err != nil {
		return Guild{}, err
	}
	switch len(gs) {
	case 0:
		return Guild{}, fmt.Errorf("dctl: bot is in no server (invite it first)")
	case 1:
		return gs[0], nil
	default:
		return Guild{}, fmt.Errorf("dctl: bot is in %d servers; pass a guild id", len(gs))
	}
}
