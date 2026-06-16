package dctl

import (
	"context"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
)

func TestGuildsList(t *testing.T) {
	s := transport.NewStub().Reply(`[{"id":"g1","name":"srv"}]`)
	g := &Guilds{rt: s}
	gs, err := g.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 1 || gs[0].ID != "g1" {
		t.Fatalf("guilds = %+v", gs)
	}
	if c := s.Last(); c.Method != "GET" || c.Path != "/users/@me/guilds" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestGuildsSoleErrorsOnMultiple(t *testing.T) {
	s := transport.NewStub().Reply(`[{"id":"a"},{"id":"b"}]`)
	g := &Guilds{rt: s}
	if _, err := g.Sole(context.Background()); err == nil {
		t.Fatal("want error on 2 guilds")
	}
}
