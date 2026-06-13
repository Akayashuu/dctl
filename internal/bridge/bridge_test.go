package bridge

import (
	"path/filepath"
	"testing"

	"github.com/vskstudio/dctl/internal/state"
)

func TestRecordParticipantAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "participants", "demo.log")
	recordParticipant(path, "u1")
	recordParticipant(path, "u1") // idempotent
	recordParticipant(path, "u2")
	got := state.ReadParticipants(path)
	if len(got) != 2 || got[0] != "u1" || got[1] != "u2" {
		t.Fatalf("expected [u1 u2], got %+v", got)
	}
}

func TestRecordParticipantEmptyPathNoop(t *testing.T) {
	// must not panic or create anything when no journal configured
	recordParticipant("", "u1")
}
