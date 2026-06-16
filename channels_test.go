package dctl

import (
	"context"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func chans(s *transport.Stub) *Channels {
	return &Channels{rt: s, def: &defaults{guilds: &Guilds{rt: s}}}
}

func TestChannelsCreate(t *testing.T) {
	s := transport.NewStub().
		Reply(`{"id":"c1","name":"logs","type":0}`)
	ch, err := chans(s).Create(context.Background(), "g1", "logs")
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID != "c1" {
		t.Fatalf("channel = %+v", ch)
	}
	c := s.Last()
	if c.Method != "POST" || c.Path != "/guilds/g1/channels" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
	if c.Body.(map[string]any)["name"] != "logs" {
		t.Errorf("body = %v", c.Body)
	}
}

func TestChannelsRename(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"c1","name":"new"}`)
	ch, err := chans(s).Rename(context.Background(), "c1", "new")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Name != "new" {
		t.Fatalf("name = %q", ch.Name)
	}
	c := s.Last()
	if c.Method != "PATCH" || c.Path != "/channels/c1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestChannelsDelete(t *testing.T) {
	s := transport.NewStub()
	if err := chans(s).Delete(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if c := s.Last(); c.Method != "DELETE" || c.Path != "/channels/c1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}
