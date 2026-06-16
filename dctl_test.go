package dctl

import (
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
