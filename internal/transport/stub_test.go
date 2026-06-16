package transport

import (
	"context"
	"net/http"
	"testing"
)

func TestStubCapturesAndReplies(t *testing.T) {
	s := NewStub()
	s.Reply(`{"id":"7"}`)
	var out struct {
		ID string `json:"id"`
	}
	err := s.Do(context.Background(), http.MethodPost, "/channels/1/messages", map[string]any{"content": "hi"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "7" {
		t.Errorf("id = %q", out.ID)
	}
	call := s.Last()
	if call.Method != "POST" || call.Path != "/channels/1/messages" {
		t.Errorf("captured %s %s", call.Method, call.Path)
	}
	if call.Body.(map[string]any)["content"] != "hi" {
		t.Errorf("body = %v", call.Body)
	}
}

func TestStubReturnsConfiguredError(t *testing.T) {
	s := NewStub()
	s.Fail(context.DeadlineExceeded)
	if err := s.Do(context.Background(), http.MethodGet, "/x", nil, nil); err != context.DeadlineExceeded {
		t.Errorf("err = %v", err)
	}
}
