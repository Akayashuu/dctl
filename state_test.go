package dctl

import (
	"path/filepath"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := NewState(path)
	s.Home = HomeRef{ID: "123", Type: "category"}
	s.Allow = []string{"343535234303787009"}
	s.Sessions = []Session{{Name: "foo", ChannelID: "c1", Type: "text", Cmd: "claude"}}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Home.ID != "123" || len(got.Allow) != 1 || len(got.Sessions) != 1 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestLoadStateMissingFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.json")
	s, err := LoadState(path)
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(s.Allow) != 0 || len(s.Sessions) != 0 {
		t.Fatal("expected empty state")
	}
}

func TestAllowlist(t *testing.T) {
	s := NewState(filepath.Join(t.TempDir(), "s.json"))
	if s.Allowed("u1") {
		t.Fatal("empty allowlist should deny")
	}
	if err := s.AddAllow("u1"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAllow("u1"); err != nil { // idempotent
		t.Fatal(err)
	}
	if !s.Allowed("u1") || len(s.Allow) != 1 {
		t.Fatalf("expected u1 allowed once: %+v", s.Allow)
	}
	if err := s.RemoveAllow("u1"); err != nil {
		t.Fatal(err)
	}
	if s.Allowed("u1") {
		t.Fatal("u1 should be removed")
	}
}

func TestSessionMutations(t *testing.T) {
	s := NewState(filepath.Join(t.TempDir(), "s.json"))
	if err := s.AddSession(Session{Name: "a", ChannelID: "c1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.FindSession("a"); !ok {
		t.Fatal("expected to find a")
	}
	if err := s.AddSession(Session{Name: "a"}); err == nil {
		t.Fatal("duplicate session name should error")
	}
	if err := s.RemoveSession("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.FindSession("a"); ok {
		t.Fatal("a should be gone")
	}
}
