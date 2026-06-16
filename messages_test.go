package dctl

import (
	"context"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
)

func msgs(s *transport.Stub, def string) *Messages {
	return &Messages{rt: s, def: &defaults{channel: def}}
}

func TestMessagesSendUsesDefaultChannelAndNoAllowedMentions(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"m1","content":"hi @everyone"}`)
	m, err := msgs(s, "def").Send(context.Background(), "", "hi @everyone")
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "m1" {
		t.Fatalf("msg = %+v", m)
	}
	c := s.Last()
	if c.Path != "/channels/def/messages" {
		t.Errorf("path = %s", c.Path)
	}
	if _, present := c.Body.(map[string]any)["allowed_mentions"]; present {
		t.Error("noMentions removed: allowed_mentions must NOT be injected")
	}
}

func TestMessagesReadReversesToChronological(t *testing.T) {
	s := transport.NewStub().Reply(`[{"id":"3"},{"id":"2"},{"id":"1"}]`)
	got, err := msgs(s, "def").Read(context.Background(), "c", 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ID != "1" || got[2].ID != "3" {
		t.Fatalf("order = %v", []string{got[0].ID, got[1].ID, got[2].ID})
	}
}

func TestMessagesEdit(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"m1","content":"new"}`)
	if _, err := msgs(s, "").Edit(context.Background(), "c", "m1", "new"); err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Method != "PATCH" || c.Path != "/channels/c/messages/m1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestMessagesDelete(t *testing.T) {
	s := transport.NewStub()
	if err := msgs(s, "").Delete(context.Background(), "c", "m1"); err != nil {
		t.Fatal(err)
	}
	if c := s.Last(); c.Method != "DELETE" || c.Path != "/channels/c/messages/m1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}
