package supervisor

import (
	"context"
	"strings"
	"testing"

	"github.com/vskstudio/dctl/internal/state"
)

func TestBridgeArgsIncludeParticipants(t *testing.T) {
	s := NewSupervisor(context.Background(), "/bin/dctl")
	s.PartDir = "/var/dctl"
	args := s.bridgeArgs(state.Session{Name: "demo", ChannelID: "c1"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--participants") ||
		!strings.Contains(joined, state.ParticipantsPath("/var/dctl", "demo")) {
		t.Fatalf("expected --participants <journal> in args: %v", args)
	}
}
