package dctl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient points a Client at a stub server instead of the real Discord API.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("tok", "defchan")
	c.http = srv.Client()
	// Redirect the API base by overriding the request URL host via a RoundTripper.
	c.http.Transport = rewriteHost{base: srv.URL, rt: srv.Client().Transport}
	return c
}

type rewriteHost struct {
	base string
	rt   http.RoundTripper
}

func (rw rewriteHost) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = "http"
	r.URL.Host = strings.TrimPrefix(rw.base, "http://")
	if rw.rt == nil {
		rw.rt = http.DefaultTransport
	}
	return rw.rt.RoundTrip(r)
}

func TestReadReversesToChronological(t *testing.T) {
	// Discord returns newest-first; Read must flip to oldest-first.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":"3","content":"c"},{"id":"2","content":"b"},{"id":"1","content":"a"}]`))
	})
	msgs, err := c.Read(context.Background(), "", 10, "")
	if err != nil {
		t.Fatal(err)
	}
	got := msgs[0].ID + msgs[1].ID + msgs[2].ID
	if got != "123" {
		t.Fatalf("want chronological 1,2,3 got %s", got)
	}
}

func TestResolveChannelFallsBackToDefault(t *testing.T) {
	c := New("tok", "defchan")
	if ch, _ := c.resolveChannel(""); ch != "defchan" {
		t.Fatalf("want defchan, got %q", ch)
	}
	if ch, _ := c.resolveChannel("explicit"); ch != "explicit" {
		t.Fatalf("want explicit, got %q", ch)
	}
	c2 := New("tok", "")
	if _, err := c2.resolveChannel(""); err != ErrNoChannel {
		t.Fatalf("want ErrNoChannel, got %v", err)
	}
}

func TestDisabledClientErrors(t *testing.T) {
	c := New("", "defchan")
	if _, err := c.Read(context.Background(), "", 10, ""); err != ErrDisabled {
		t.Fatalf("want ErrDisabled, got %v", err)
	}
	if _, err := c.Send(context.Background(), "", "hi"); err != ErrDisabled {
		t.Fatalf("want ErrDisabled, got %v", err)
	}
}
