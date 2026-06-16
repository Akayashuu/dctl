package dctl

import (
	"context"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func TestPermissionsSetOverwrite(t *testing.T) {
	s := transport.NewStub()
	p := &Permissions{rt: s}
	err := p.Set(context.Background(), "c1", "r1", 0, "1024", "0")
	if err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Method != "PUT" || c.Path != "/channels/c1/permissions/r1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
	body := c.Body.(map[string]any)
	if body["allow"] != "1024" || body["type"] != 0 {
		t.Errorf("body = %v", body)
	}
}

func TestPermissionsRemoveOverwrite(t *testing.T) {
	s := transport.NewStub()
	p := &Permissions{rt: s}
	if err := p.Remove(context.Background(), "c1", "r1"); err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Method != "DELETE" || c.Path != "/channels/c1/permissions/r1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}
