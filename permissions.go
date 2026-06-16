package dctl

import (
	"context"
	"net/http"

	"github.com/Herrscherd/dctl/internal/transport"
)

// Permissions edits channel permission overwrites.
type Permissions struct {
	rt transport.Doer
}

// Overwrite target types.
const (
	OverwriteRole   = 0
	OverwriteMember = 1
)

// Set creates/updates a permission overwrite on channelID for overwriteID
// (a role or member id). allow/deny are Discord permission bit-strings;
// kind is OverwriteRole or OverwriteMember.
func (p *Permissions) Set(ctx context.Context, channelID, overwriteID string, kind int, allow, deny string) error {
	return p.rt.Do(ctx, http.MethodPut, "/channels/"+seg(channelID)+"/permissions/"+seg(overwriteID),
		map[string]any{"type": kind, "allow": allow, "deny": deny}, nil)
}

func (p *Permissions) Remove(ctx context.Context, channelID, overwriteID string) error {
	return p.rt.Do(ctx, http.MethodDelete, "/channels/"+seg(channelID)+"/permissions/"+seg(overwriteID), nil, nil)
}
