package worktree

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPathAndBranch(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		session    string
		wantPath   string
		wantBranch string
	}{
		{
			name:       "namespaced",
			instanceID: "alice",
			session:    "foo",
			wantPath:   filepath.Join("/repo", ".dctl-sessions", "alice", "foo"),
			wantBranch: "session/alice/foo",
		},
		{
			name:       "legacy-empty-id",
			instanceID: "",
			session:    "foo",
			wantPath:   filepath.Join("/repo", ".dctl-sessions", "foo"),
			wantBranch: "session/foo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWorktreer(context.Background(), "/repo", tt.instanceID)
			if got := w.Path(tt.session); got != tt.wantPath {
				t.Fatalf("Path = %q, want %q", got, tt.wantPath)
			}
			if got := w.Branch(tt.session); got != tt.wantBranch {
				t.Fatalf("Branch = %q, want %q", got, tt.wantBranch)
			}
		})
	}
}
