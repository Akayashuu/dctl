package dctl

import (
	"context"
	"testing"

	"github.com/Herrscherd/dctl/internal/transport"
)

func thr(s *transport.Stub) *Threads {
	return &Threads{rt: s, def: &defaults{guilds: &Guilds{rt: s}}}
}

func TestThreadsStart(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"t1","name":"topic","type":11}`)
	ch, err := thr(s).Start(context.Background(), "c", "m", "topic")
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID != "t1" {
		t.Fatalf("thread = %+v", ch)
	}
	c := s.Last()
	if c.Path != "/channels/c/messages/m/threads" {
		t.Errorf("path = %s", c.Path)
	}
	body := c.Body.(map[string]any)
	if body["name"] != "topic" {
		t.Errorf("name = %v", body["name"])
	}
	if body["auto_archive_duration"] != autoArchive {
		t.Errorf("auto_archive_duration = %v", body["auto_archive_duration"])
	}
}

func TestThreadsCreateForum(t *testing.T) {
	s := transport.NewStub().
		Reply(`[{"id":"g1"}]`).
		Reply(`{"id":"f1","name":"forum","type":15}`)
	ch, err := thr(s).CreateForum(context.Background(), "", "forum")
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID != "f1" {
		t.Fatalf("channel = %+v", ch)
	}
	c := s.Last()
	if c.Method != "POST" || c.Path != "/guilds/g1/channels" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
	body := c.Body.(map[string]any)
	if body["type"] != ChannelForum {
		t.Errorf("type = %v", body["type"])
	}
}

func TestThreadsForumPost(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"p1","type":11}`)
	ch, err := thr(s).ForumPost(context.Background(), "f1", "title", "body")
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID != "p1" {
		t.Fatalf("thread = %+v", ch)
	}
	c := s.Last()
	if c.Path != "/channels/f1/threads" {
		t.Errorf("path = %s", c.Path)
	}
	body := c.Body.(map[string]any)
	msg := body["message"].(map[string]any)
	if msg["content"] != "body" {
		t.Errorf("message.content = %v", msg["content"])
	}
}
