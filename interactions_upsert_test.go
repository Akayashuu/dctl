package dctl

import (
	"context"
	"net/http"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func TestUpsertStatusMessageRepostsOn404(t *testing.T) {
	s := transport.NewStub().
		FailNext(&transport.APIError{Status: 404, Body: "Unknown Message"}).
		Reply(`{"id":"new"}`)
	in := &Interactions{rt: s, def: &defaults{}}
	id, err := in.UpsertStatusMessage(context.Background(), "c", "old", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if id != "new" {
		t.Errorf("id = %q, want new", id)
	}
	calls := s.Calls()
	if len(calls) != 2 {
		t.Fatalf("want 2 calls, got %d", len(calls))
	}
	if calls[0].Method != http.MethodPatch || calls[1].Method != http.MethodPost {
		t.Errorf("calls = %s %s", calls[0].Method, calls[1].Method)
	}
}

func TestUpsertStatusMessagePropagatesNon404(t *testing.T) {
	s := transport.NewStub().
		FailNext(&transport.APIError{Status: 500, Body: "boom"})
	in := &Interactions{rt: s, def: &defaults{}}
	_, err := in.UpsertStatusMessage(context.Background(), "c", "old", "hi")
	if err == nil {
		t.Fatal("want error")
	}
	if len(s.Calls()) != 1 {
		t.Errorf("want no re-post, got %d calls", len(s.Calls()))
	}
}
