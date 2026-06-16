// members.go
package dctl

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Herrscherd/dctl/internal/transport"
)

// Members lists and moderates guild members.
type Members struct {
	rt  transport.Doer
	def *defaults
}

// List returns up to limit (1..1000, default 100) members of guildID (or the sole guild).
func (m *Members) List(ctx context.Context, guildID string, limit int) ([]GuildMember, error) {
	gid, err := m.def.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var ms []GuildMember
	path := fmt.Sprintf("/guilds/%s/members?limit=%d", gid, limit)
	if err := m.rt.Do(ctx, http.MethodGet, path, nil, &ms); err != nil {
		return nil, err
	}
	return ms, nil
}

// Get returns a single member.
func (m *Members) Get(ctx context.Context, guildID, userID string) (*GuildMember, error) {
	gid, err := m.def.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	var gm GuildMember
	if err := m.rt.Do(ctx, http.MethodGet, "/guilds/"+gid+"/members/"+userID, nil, &gm); err != nil {
		return nil, err
	}
	return &gm, nil
}

// Kick removes a member from the guild.
func (m *Members) Kick(ctx context.Context, guildID, userID string) error {
	gid, err := m.def.resolveGuild(ctx, guildID)
	if err != nil {
		return err
	}
	return m.rt.Do(ctx, http.MethodDelete, "/guilds/"+gid+"/members/"+userID, nil, nil)
}

// Ban bans a member from the guild.
func (m *Members) Ban(ctx context.Context, guildID, userID string) error {
	gid, err := m.def.resolveGuild(ctx, guildID)
	if err != nil {
		return err
	}
	return m.rt.Do(ctx, http.MethodPut, "/guilds/"+gid+"/bans/"+userID, nil, nil)
}
