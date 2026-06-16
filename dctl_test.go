package dctl

import (
	"context"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func TestNewWiresSubClientsWithSharedDefaults(t *testing.T) {
	c := New("tok", "chan123")
	if !c.Enabled() {
		t.Error("want enabled")
	}
	if c.DefaultChannel() != "chan123" {
		t.Errorf("default channel = %q", c.DefaultChannel())
	}
	if c.Messages() == nil || c.Channels() == nil || c.Roles() == nil ||
		c.Members() == nil || c.Reactions() == nil || c.Threads() == nil ||
		c.Permissions() == nil || c.Webhooks() == nil || c.Interactions() == nil ||
		c.Components() == nil || c.Guilds() == nil {
		t.Fatal("an accessor returned nil")
	}
}

func TestNewWithTransportInjectsStub(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"m1"}`)
	c := newWith(s, "chan")
	if c.Messages() == nil {
		t.Fatal("messages nil")
	}
}

func TestDisabledClientErrors(t *testing.T) {
	c := New("", "")
	_, err := c.Messages().Send(context.Background(), "x", "hi")
	if err != transport.ErrDisabled {
		t.Errorf("err = %v, want ErrDisabled", err)
	}
}
