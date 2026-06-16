package dctl

import (
	"context"
	"net/url"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func TestReactionsAddEncodesEmoji(t *testing.T) {
	s := transport.NewStub()
	r := &Reactions{rt: s}
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
	r := &Reactions{rt: s}
	if err := r.Remove(context.Background(), "c", "m", "👍"); err != nil {
		t.Fatal(err)
	}
	if c := s.Last(); c.Method != "DELETE" {
		t.Errorf("method = %s", c.Method)
	}
}
