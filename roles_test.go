// roles_test.go
package dctl

import (
	"context"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func roles(s *transport.Stub) *Roles {
	return &Roles{rt: s, def: &defaults{guilds: &Guilds{rt: s}}}
}

func TestRolesList(t *testing.T) {
	s := transport.NewStub().
		Reply(`[{"id":"g1"}]`). // resolveGuild
		Reply(`[{"id":"r1","name":"mod"}]`)
	rs, err := roles(s).List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Name != "mod" {
		t.Fatalf("roles = %+v", rs)
	}
	if c := s.Last(); c.Path != "/guilds/g1/roles" {
		t.Errorf("path = %s", c.Path)
	}
}

func TestRolesCreate(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"r2","name":"mod"}`)
	r, err := roles(s).Create(context.Background(), "g1", "mod")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "r2" {
		t.Fatalf("role = %+v", r)
	}
	c := s.Last()
	if c.Method != "POST" || c.Path != "/guilds/g1/roles" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestRolesDelete(t *testing.T) {
	s := transport.NewStub()
	if err := roles(s).Delete(context.Background(), "g1", "r2"); err != nil {
		t.Fatal(err)
	}
	if c := s.Last(); c.Method != "DELETE" || c.Path != "/guilds/g1/roles/r2" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestRolesAssignToMember(t *testing.T) {
	s := transport.NewStub()
	if err := roles(s).Assign(context.Background(), "g1", "u1", "r2"); err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Method != "PUT" || c.Path != "/guilds/g1/members/u1/roles/r2" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}
