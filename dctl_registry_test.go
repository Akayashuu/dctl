package dctl

import (
	"context"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

// The registry must be stable across c.Interactions() / .Registry() calls, so
// bindings registered at setup are visible at dispatch time.
func TestClientRegistryIsStable(t *testing.T) {
	c := newWith(transport.NewStub(), "chan")
	c.Interactions().Registry().Add(NewCommand("ping", "p"),
		func(ctx context.Context, ix Interaction) (Response, error) {
			return Response{Content: "pong"}, nil
		})

	r, err := c.Interactions().Registry().Dispatch(context.Background(),
		Interaction{Data: InteractionData{Name: "ping"}})
	if err != nil || r.Content != "pong" {
		t.Fatalf("re-derived registry lost its binding: r=%v err=%v", r, err)
	}
}

// AppID is fetched once and cached, even across separate sub-client ops.
func TestAppIDCached(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"app1"}`).Reply(`{"id":"app1"}`)
	c := newWith(s, "chan")

	for i := 0; i < 3; i++ {
		if id, err := c.Interactions().AppID(context.Background()); err != nil || id != "app1" {
			t.Fatalf("AppID = %q, %v", id, err)
		}
	}
	hits := 0
	for _, call := range s.Calls() {
		if call.Path == "/users/@me" {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("/users/@me fetched %d times, want 1 (cached)", hits)
	}
}
