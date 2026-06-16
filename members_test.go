// members_test.go
package dctl

import (
	"context"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func members(s *transport.Stub) *Members {
	return &Members{rt: s, def: &defaults{guilds: &Guilds{rt: s}}}
}

func TestMembersGet(t *testing.T) {
	s := transport.NewStub().Reply(`{"user":{"id":"u1","username":"bob"},"roles":["r1"]}`)
	m, err := members(s).Get(context.Background(), "g1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if m.User.Username != "bob" {
		t.Fatalf("member = %+v", m)
	}
	if c := s.Last(); c.Path != "/guilds/g1/members/u1" {
		t.Errorf("path = %s", c.Path)
	}
}

func TestMembersKick(t *testing.T) {
	s := transport.NewStub()
	if err := members(s).Kick(context.Background(), "g1", "u1"); err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Method != "DELETE" || c.Path != "/guilds/g1/members/u1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestMembersBan(t *testing.T) {
	s := transport.NewStub()
	if err := members(s).Ban(context.Background(), "g1", "u1"); err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Method != "PUT" || c.Path != "/guilds/g1/bans/u1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}
