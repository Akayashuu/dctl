package dctl

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
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

func TestRespondAutocompleteSendsType8(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.WriteHeader(http.StatusNoContent)
	})
	err := c.RespondAutocomplete(context.Background(), "id1", "tok1", []AutocompleteChoice{
		{Name: "alpha", Value: "alpha"},
		{Name: "beta", Value: "beta"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := body["type"]; got != float64(8) {
		t.Fatalf("response type = %v, want 8", got)
	}
	data, _ := body["data"].(map[string]any)
	choices, _ := data["choices"].([]any)
	if len(choices) != 2 {
		t.Fatalf("choices = %v, want 2", choices)
	}
	first, _ := choices[0].(map[string]any)
	if first["name"] != "alpha" || first["value"] != "alpha" {
		t.Fatalf("first choice = %v, want alpha/alpha", first)
	}
}
