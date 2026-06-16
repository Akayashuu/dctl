package dctl

import (
	"context"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func TestDefaultsResolveChannelPrefersExplicit(t *testing.T) {
	d := &defaults{channel: "def"}
	got, err := d.resolveChannel("explicit")
	if err != nil || got != "explicit" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDefaultsResolveChannelFallsBackToDefault(t *testing.T) {
	d := &defaults{channel: "def"}
	got, err := d.resolveChannel("")
	if err != nil || got != "def" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDefaultsResolveChannelErrorsWhenNone(t *testing.T) {
	d := &defaults{}
	if _, err := d.resolveChannel(""); err != ErrNoChannel {
		t.Fatalf("err = %v, want ErrNoChannel", err)
	}
}

func TestResolveGuildUsesSoleGuild(t *testing.T) {
	s := transport.NewStub().Reply(`[{"id":"g1","name":"srv"}]`)
	d := &defaults{guilds: &Guilds{rt: s}}
	got, err := d.resolveGuild(context.Background(), "")
	if err != nil || got != "g1" {
		t.Fatalf("got %q, %v", got, err)
	}
}
