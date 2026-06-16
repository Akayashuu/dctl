package dctl

import (
	"context"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func TestWebhooksCreate(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"w1","token":"t","name":"hook"}`)
	w := &Webhooks{rt: s}
	hook, err := w.Create(context.Background(), "c1", "hook")
	if err != nil {
		t.Fatal(err)
	}
	if hook.ID != "w1" {
		t.Fatalf("hook = %+v", hook)
	}
	c := s.Last()
	if c.Method != "POST" || c.Path != "/channels/c1/webhooks" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestWebhooksList(t *testing.T) {
	s := transport.NewStub().Reply(`[{"id":"w1"}]`)
	w := &Webhooks{rt: s}
	hooks, err := w.List(context.Background(), "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(hooks) != 1 {
		t.Fatalf("hooks = %+v", hooks)
	}
}

func TestWebhooksDelete(t *testing.T) {
	s := transport.NewStub()
	w := &Webhooks{rt: s}
	if err := w.Delete(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}
	if c := s.Last(); c.Method != "DELETE" || c.Path != "/webhooks/w1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestWebhooksExecute(t *testing.T) {
	s := transport.NewStub()
	w := &Webhooks{rt: s}
	if err := w.Execute(context.Background(), "w1", "tok", "hello"); err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Method != "POST" || c.Path != "/webhooks/w1/tok" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
	if c.Body.(map[string]any)["content"] != "hello" {
		t.Errorf("body = %v", c.Body)
	}
}
