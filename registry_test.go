package dctl

import (
	"context"
	"errors"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func newReg(s *transport.Stub) *Registry {
	in := &Interactions{rt: s, def: &defaults{guilds: &Guilds{rt: s}}}
	return in.Registry()
}

// stubGuild queues the @me and guild-list replies every commandsBase call needs.
func stubGuild(s *transport.Stub) {
	s.Reply(`{"id":"app1"}`).Reply(`[{"id":"g1"}]`)
}

func TestRegistrySyncReconciles(t *testing.T) {
	s := transport.NewStub()
	reg := newReg(s)
	reg.Add(NewCommand("keep", "kept"), nil) // exists → Edit
	reg.Add(NewCommand("new", "fresh"), nil) // missing → Create

	// Sync resolves the base (app id + guild) once, then lists existing commands.
	stubGuild(s)
	s.Reply(`[{"id":"c1","name":"keep"},{"id":"c2","name":"stale"}]`)

	if err := reg.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	var methods []string
	for _, c := range s.Calls() {
		if c.Method == "PATCH" || c.Method == "POST" || c.Method == "DELETE" {
			methods = append(methods, c.Method+" "+lastSeg(c.Path))
		}
	}
	want := []string{"PATCH c1", "POST commands", "DELETE c2"}
	if len(methods) != 3 || methods[0] != want[0] || methods[1] != want[1] || methods[2] != want[2] {
		t.Fatalf("ops = %v, want %v", methods, want)
	}
}

func TestRegistryDispatch(t *testing.T) {
	reg := newReg(transport.NewStub())
	called := false
	reg.Add(NewCommand("ping", "p"), func(ctx context.Context, ix Interaction) (Response, error) {
		called = true
		return Response{Content: "pong"}, nil
	})

	r, err := reg.Dispatch(context.Background(), Interaction{Data: InteractionData{Name: "ping"}})
	if err != nil || !called || r.Content != "pong" {
		t.Fatalf("dispatch hit failed: r=%v err=%v called=%v", r, err, called)
	}

	if _, err := reg.Dispatch(context.Background(), Interaction{Data: InteractionData{Name: "nope"}}); err == nil {
		t.Fatal("expected unknown-command error")
	}
}

func TestRegistryRemoveAndOrder(t *testing.T) {
	reg := newReg(transport.NewStub())
	reg.Add(NewCommand("a", "a"), nil)
	reg.Add(NewCommand("b", "b"), nil)
	reg.Add(NewCommand("c", "c"), nil)
	reg.Remove("b")
	got := reg.Commands()
	if len(got) != 2 || got[0].Name() != "a" || got[1].Name() != "c" {
		t.Fatalf("order/remove wrong: %v", names(got))
	}
}

func TestSyncPropagatesListError(t *testing.T) {
	s := transport.NewStub().Fail(errors.New("boom"))
	if err := newReg(s).Sync(context.Background()); err == nil {
		t.Fatal("expected list error to propagate")
	}
}

func lastSeg(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

func names(cs []*Command) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name()
	}
	return out
}
