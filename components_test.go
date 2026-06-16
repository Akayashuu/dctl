package dctl

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func TestComponentsSendSelectMenu(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"m1"}`)
	c := &Components{rt: s, def: &defaults{channel: "def"}}
	opts := []SelectOption{{Label: "A", Value: "a"}}
	m, err := c.SendSelectMenu(context.Background(), "", "", "pick", "menu1", opts)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "m1" {
		t.Fatalf("msg = %+v", m)
	}
	if call := s.Last(); call.Path != "/channels/def/messages" {
		t.Errorf("path = %s", call.Path)
	}
}

func TestComponentsAck(t *testing.T) {
	s := transport.NewStub()
	c := &Components{rt: s, def: &defaults{}}
	if err := c.Ack(context.Background(), "iid", "tok", "done"); err != nil {
		t.Fatal(err)
	}
	call := s.Last()
	if call.Method != "POST" || call.Path != "/interactions/iid/tok/callback" {
		t.Errorf("call = %s %s", call.Method, call.Path)
	}
	body := call.Body.(map[string]any)
	if body["type"] != 7 {
		t.Errorf("type = %v, want 7", body["type"])
	}
	data := body["data"].(map[string]any)
	if data["content"] != "done" {
		t.Errorf("content = %v", data["content"])
	}
	if _, ok := data["allowed_mentions"]; ok {
		t.Error("allowed_mentions must NOT be injected")
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
	in := strings.Repeat("é", 60)
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
	if in.Data.CustomID != "dctlchoice:s" {
		t.Fatalf("custom_id = %q, want dctlchoice:s", in.Data.CustomID)
	}
	if len(in.Data.Values) != 1 || in.Data.Values[0] != "2" {
		t.Fatalf("values = %v, want [2]", in.Data.Values)
	}
}
