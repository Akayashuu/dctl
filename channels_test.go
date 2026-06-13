package dctl

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSoleGuild(t *testing.T) {
	// Exactly one guild → returned.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":"g1","name":"vskstudio"}]`))
	})
	g, err := c.SoleGuild(context.Background())
	if err != nil || g.ID != "g1" {
		t.Fatalf("want g1, got %+v err=%v", g, err)
	}

	// Zero guilds → error (don't silently target nothing).
	c0 := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	if _, err := c0.SoleGuild(context.Background()); err == nil {
		t.Fatal("want error for zero guilds")
	}
}

func TestEnsureChannelReusesExisting(t *testing.T) {
	created := false
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/guilds/g1/channels") && r.Method == http.MethodGet:
			w.Write([]byte(`[{"id":"c9","name":"Prospector","type":0}]`))
		case strings.HasSuffix(r.URL.Path, "/guilds/g1/channels") && r.Method == http.MethodPost:
			created = true
			w.Write([]byte(`{"id":"cNEW","name":"prospector","type":0}`))
		}
	})
	// Case-insensitive match on existing "Prospector" → no creation.
	ch, err := c.EnsureChannel(context.Background(), "g1", "prospector")
	if err != nil || ch.ID != "c9" {
		t.Fatalf("want existing c9, got %+v err=%v", ch, err)
	}
	if created {
		t.Fatal("should not have created a channel when one matches")
	}
}

func TestEnsureChannelCreatesWhenMissing(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`[{"id":"c1","name":"general","type":0}]`))
		case http.MethodPost:
			w.Write([]byte(`{"id":"cNEW","name":"prospector","type":0}`))
		}
	})
	ch, err := c.EnsureChannel(context.Background(), "g1", "prospector")
	if err != nil || ch.ID != "cNEW" {
		t.Fatalf("want created cNEW, got %+v err=%v", ch, err)
	}
}

func TestChannelTypeParsesType(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"x","type":4,"name":"cat"}`))
	})
	ct, err := c.ChannelType(context.Background(), "x")
	if err != nil || ct != 4 {
		t.Fatalf("got %d,%v", ct, err)
	}
}

func TestCreateChannelUnderSendsParent(t *testing.T) {
	var gotParent string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/guilds/g1/channels") && r.Method == http.MethodPost {
			b := make([]byte, r.ContentLength)
			r.Body.Read(b)
			if strings.Contains(string(b), `"parent_id":"cat1"`) {
				gotParent = "cat1"
			}
			w.Write([]byte(`{"id":"new","name":"demo","type":0}`))
			return
		}
		w.Write([]byte(`[{"id":"g1","name":"vsk"}]`)) // SoleGuild
	})
	ch, err := c.CreateChannelUnder(context.Background(), "cat1", "demo")
	if err != nil || ch.ID != "new" {
		t.Fatalf("got %+v err=%v", ch, err)
	}
	if gotParent != "cat1" {
		t.Fatal("expected parent_id forwarded")
	}
}
