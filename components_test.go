package dctl

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChoiceCustomIDRoundTrip(t *testing.T) {
	id := ChoiceCustomID("my-session")
	if !strings.HasPrefix(id, "dctlchoice:") {
		t.Fatalf("custom id %q missing prefix", id)
	}
	got, ok := ParseChoiceCustomID(id)
	if !ok || got != "my-session" {
		t.Fatalf("ParseChoiceCustomID(%q) = %q,%v; want my-session,true", id, got, ok)
	}
	if _, ok := ParseChoiceCustomID("other:thing"); ok {
		t.Fatal("a non-choice custom id must not parse as a choice menu")
	}
}

func TestChoiceMenuComponentsShape(t *testing.T) {
	comps := choiceMenuComponents("dctlchoice:s", []SelectOption{
		{Label: "Yes", Value: "1"},
		{Label: "No", Value: "2", Description: "stop here"},
	})
	if len(comps) != 1 || comps[0]["type"] != 1 {
		t.Fatalf("want a single ACTION_ROW (type 1), got %v", comps)
	}
	inner := comps[0]["components"].([]map[string]any)
	if len(inner) != 1 || inner[0]["type"] != 3 || inner[0]["custom_id"] != "dctlchoice:s" {
		t.Fatalf("want a STRING_SELECT (type 3) with our custom_id, got %v", inner)
	}
	opts := inner[0]["options"].([]map[string]any)
	if len(opts) != 2 || opts[0]["value"] != "1" || opts[1]["description"] != "stop here" {
		t.Fatalf("options not built as expected: %v", opts)
	}
}

func TestClampRuneSafe(t *testing.T) {
	// Clamping multibyte text must never yield invalid UTF-8.
	in := strings.Repeat("é", 60) // 60 runes, 120 bytes
	got := clamp(in, 100)
	if r := []rune(got); len(r) != 60 {
		t.Fatalf("clamp shortened a sub-limit string: %d runes", len(r))
	}
	long := strings.Repeat("é", 150)
	got = clamp(long, 100)
	if r := []rune(got); len(r) != 100 {
		t.Fatalf("clamp(150 runes,100) = %d runes, want 100", len(r))
	}
}

// A real MESSAGE_COMPONENT payload unmarshals into the same Interaction struct,
// exposing the clicked custom_id and selected value.
func TestComponentInteractionUnmarshal(t *testing.T) {
	raw := `{
		"id": "111", "type": 3, "token": "tok", "channel_id": "999",
		"member": {"user": {"id": "42", "username": "neo"}},
		"data": {"custom_id": "dctlchoice:s", "component_type": 3, "values": ["2"]}
	}`
	var in Interaction
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	if in.Type != InteractionComponent {
		t.Fatalf("type = %d, want %d", in.Type, InteractionComponent)
	}
	if in.ChannelID != "999" {
		t.Fatalf("channel_id = %q, want 999", in.ChannelID)
	}
	sess, ok := ParseChoiceCustomID(in.Data.CustomID)
	if !ok || sess != "s" {
		t.Fatalf("custom id routing failed: %q,%v", sess, ok)
	}
	if len(in.Data.Values) != 1 || in.Data.Values[0] != "2" {
		t.Fatalf("values = %v, want [2]", in.Data.Values)
	}
}
