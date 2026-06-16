package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPDoSetsAuthAndDecodes(t *testing.T) {
	var gotAuth, gotUA, gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.Write([]byte(`{"id":"42"}`))
	}))
	defer srv.Close()

	rt := NewHTTP("tok", WithBase(srv.URL))
	var out struct {
		ID string `json:"id"`
	}
	if err := rt.Do(context.Background(), http.MethodGet, "/x", nil, &out); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bot tok" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotUA == "" {
		t.Error("missing user-agent")
	}
	if gotMethod != "GET" || gotPath != "/x" {
		t.Errorf("method/path = %s %s", gotMethod, gotPath)
	}
	if out.ID != "42" {
		t.Errorf("id = %q", out.ID)
	}
}

func TestHTTPDoDisabledWithoutToken(t *testing.T) {
	rt := NewHTTP("")
	if err := rt.Do(context.Background(), http.MethodGet, "/x", nil, nil); err != ErrDisabled {
		t.Errorf("err = %v, want ErrDisabled", err)
	}
}

func TestHTTPDoSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"Missing Permissions"}`))
	}))
	defer srv.Close()
	rt := NewHTTP("tok", WithBase(srv.URL))
	err := rt.Do(context.Background(), http.MethodGet, "/x", nil, nil)
	if err == nil {
		t.Fatal("want error")
	}
	if want := "discord 403"; !strings.Contains(err.Error(), want) {
		t.Errorf("err = %q, want containing %q", err.Error(), want)
	}
}

func TestHTTPDoMarshalsBody(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	rt := NewHTTP("tok", WithBase(srv.URL))
	if err := rt.Do(context.Background(), http.MethodPost, "/x", map[string]any{"content": "hi"}, nil); err != nil {
		t.Fatal(err)
	}
	if got["content"] != "hi" {
		t.Errorf("body content = %v", got["content"])
	}
}
