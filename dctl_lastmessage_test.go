package dctl

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestLastMessageAt(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"9","channel_id":"c","content":"hi","author":{"id":"u","username":"a"},"timestamp":"2026-06-01T12:00:00.000000+00:00"}]`))
	})

	ts, err := c.LastMessageAt(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	if !ts.Equal(want) {
		t.Fatalf("LastMessageAt = %v, want %v", ts, want)
	}
}

func TestLastMessageAtEmptyChannel(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})

	ts, err := c.LastMessageAt(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if !ts.IsZero() {
		t.Fatalf("expected zero time for empty channel, got %v", ts)
	}
}
