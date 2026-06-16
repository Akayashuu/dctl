package dctl

import (
	"context"
	"testing"
	"time"

	"github.com/Akayashuu/dctl/internal/transport"
)

func TestLastMessageAt(t *testing.T) {
	s := transport.NewStub().Reply(`[{"id":"9","channel_id":"c","content":"hi","author":{"id":"u","username":"a"},"timestamp":"2026-06-01T12:00:00.000000+00:00"}]`)
	m := msgs(s, "defchan")
	ts, err := m.LastMessageAt(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if !ts.Equal(want) {
		t.Fatalf("LastMessageAt = %v, want %v", ts, want)
	}
}

func TestLastMessageAtEmptyChannel(t *testing.T) {
	s := transport.NewStub().Reply(`[]`)
	m := msgs(s, "defchan")
	ts, err := m.LastMessageAt(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !ts.IsZero() {
		t.Fatalf("expected zero time for empty channel, got %v", ts)
	}
}
