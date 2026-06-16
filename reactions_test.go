package dctl

import (
	"context"
	"net/url"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func TestReactionsAddEncodesEmoji(t *testing.T) {
	s := transport.NewStub()
	r := &Reactions{rt: s, def: &defaults{}}
	if err := r.Add(context.Background(), "c", "m", "👍"); err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	wantPath := "/channels/c/messages/m/reactions/" + url.PathEscape("👍") + "/@me"
	if c.Method != "PUT" || c.Path != wantPath {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestReactionsRemove(t *testing.T) {
	s := transport.NewStub()
	r := &Reactions{rt: s, def: &defaults{}}
	if err := r.Remove(context.Background(), "c", "m", "👍"); err != nil {
		t.Fatal(err)
	}
	if c := s.Last(); c.Method != "DELETE" {
		t.Errorf("method = %s", c.Method)
	}
}

func TestReactionsUsesDefaultChannel(t *testing.T) {
	s := transport.NewStub()
	r := &Reactions{rt: s, def: &defaults{channel: "def"}}
	if err := r.Add(context.Background(), "", "m", "x"); err != nil {
		t.Fatal(err)
	}
	if c := s.Last(); c.Path != "/channels/def/messages/m/reactions/x/@me" {
		t.Errorf("path = %s", c.Path)
	}
}

func TestReactionsNoChannel(t *testing.T) {
	r := &Reactions{rt: transport.NewStub(), def: &defaults{}}
	if err := r.Add(context.Background(), "", "m", "x"); err != ErrNoChannel {
		t.Errorf("err = %v, want ErrNoChannel", err)
	}
}
