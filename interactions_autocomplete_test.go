package dctl

import (
	"context"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func TestFocusedFindsNestedOption(t *testing.T) {
	d := InteractionData{
		Name: "session",
		Options: []InteractionOption{{
			Name: "close", Type: 1,
			Options: []InteractionOption{
				{Name: "name", Type: 3, Value: "prosp", Focused: true},
				{Name: "force", Type: 5, Value: true},
			},
		}},
	}
	name, val, ok := d.Focused()
	if !ok || name != "name" || val != "prosp" {
		t.Fatalf("Focused() = (%q, %q, %v), want (name, prosp, true)", name, val, ok)
	}
}

func TestFocusedNoneWhenAbsent(t *testing.T) {
	d := InteractionData{Name: "session", Options: []InteractionOption{{
		Name: "close", Type: 1,
		Options: []InteractionOption{{Name: "name", Type: 3, Value: "x"}},
	}}}
	if _, _, ok := d.Focused(); ok {
		t.Fatal("expected no focused option")
	}
}

func TestInteractionsRespondNoAllowedMentions(t *testing.T) {
	s := transport.NewStub()
	in := &Interactions{rt: s, def: &defaults{guilds: &Guilds{rt: s}}}
	err := in.Respond(context.Background(), "id", "tok", Response{Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Path != "/interactions/id/tok/callback" {
		t.Errorf("path = %s", c.Path)
	}
	data := c.Body.(map[string]any)["data"].(map[string]any)
	if _, present := data["allowed_mentions"]; present {
		t.Error("allowed_mentions must NOT be injected")
	}
}

func TestInteractionsAutocompleteTrimsTo25(t *testing.T) {
	s := transport.NewStub()
	in := &Interactions{rt: s}
	choices := make([]AutocompleteChoice, 30)
	for i := range choices {
		choices[i] = AutocompleteChoice{Name: "n", Value: "v"}
	}
	if err := in.RespondAutocomplete(context.Background(), "id", "tok", choices); err != nil {
		t.Fatal(err)
	}
	body := s.Last().Body.(map[string]any)
	got := body["data"].(map[string]any)["choices"].([]map[string]any)
	if len(got) != 25 {
		t.Errorf("choices = %d, want 25", len(got))
	}
}
