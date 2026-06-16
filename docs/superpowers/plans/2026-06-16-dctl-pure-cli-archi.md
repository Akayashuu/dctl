# dctl Pure-CLI CRUD Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refondre dctl en CLI Discord pur à 3 couches (transport interne mockable / sous-clients par ressource / façade Client), couvrant le CRUD complet de l'API Discord et purgé de tout baggage d'écosystème.

**Architecture:** Un seul package public `dctl`. Le low-level HTTP est isolé dans `internal/transport` derrière une interface `Doer` (la seule chose mockée par les tests). Chaque ressource Discord (Channels, Messages, Roles, Members, Reactions, Threads, Permissions, Webhooks, Interactions, Components, Guilds) est un sous-client tenant un `Doer` partagé + des défauts (channel/guild). `Client` est une façade sans logique qui expose des accesseurs.

**Tech Stack:** Go 1.23, bibliothèque standard uniquement (`net/http`, `encoding/json`) — aucune dépendance externe (vérifié par `purity_test.go`).

---

## File Structure

- Create: `internal/transport/transport.go` — interface `Doer`, impl HTTP réelle, erreurs.
- Create: `internal/transport/stub.go` — stub `Doer` en mémoire pour les tests.
- Create: `types.go` — DTO partagés (Message, Channel, Guild, Author, Attachment, Role, GuildMember…).
- Create: `defaults.go` — résolution channel/guild par défaut.
- Modify/rename existing files into sub-clients:
  - `guilds.go` (type `Guilds`)
  - `channels.go` (type `Channels`)
  - `messages.go` (type `Messages`, ex-`dctl.go` méthodes message)
  - `reactions.go` (type `Reactions`)
  - `threads.go` (type `Threads`)
  - `components.go` (type `Components`)
  - `interactions.go` (type `Interactions`)
- Create: `roles.go` (type `Roles`), `members.go` (type `Members`), `permissions.go` (type `Permissions`), `webhooks.go` (type `Webhooks`)
- Modify: `dctl.go` — réduit à la façade `Client` + `New` + accesseurs.
- Test: un `_test.go` par sous-client, tous contre le stub `Doer`.

**Convention de test partagée (utilisée par toutes les tâches) :** chaque test construit un `*transport.Stub`, en injecte une réponse cannée, appelle le sous-client, puis asserte la requête capturée (method/path/body) et la valeur décodée.

---

## Task 1: Transport — interface Doer + impl HTTP

**Files:**
- Create: `internal/transport/transport.go`
- Test: `internal/transport/transport_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/transport/transport_test.go
package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	var out struct{ ID string `json:"id"` }
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
	if want := "discord 403"; !contains(err.Error(), want) {
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
	rt.Do(context.Background(), http.MethodPost, "/x", map[string]any{"content": "hi"}, nil)
	if got["content"] != "hi" {
		t.Errorf("body content = %v", got["content"])
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transport/`
Expected: FAIL — `undefined: NewHTTP`, `undefined: ErrDisabled`, `undefined: WithBase`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/transport/transport.go

// Package transport is dctl's HTTP boundary to the Discord REST API (v10):
// auth, request building, error decoding. It is the single mockable seam —
// resource clients depend on Doer, never on net/http directly.
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBase is the Discord REST API root.
const DefaultBase = "https://discord.com/api/v10"

// ErrDisabled is returned by Do when no bot token is configured.
var ErrDisabled = errors.New("dctl: no bot token (DISCORD_BOT_TOKEN)")

// Doer performs one Discord REST call: it marshals body (if non-nil), executes
// method+path against the API, and decodes the JSON response into out (if non-nil).
type Doer interface {
	Do(ctx context.Context, method, path string, body, out any) error
}

// HTTP is the real Doer.
type HTTP struct {
	token string
	base  string
	http  *http.Client
}

// Option configures an HTTP transport.
type Option func(*HTTP)

// WithBase overrides the API root (used by tests).
func WithBase(base string) Option { return func(h *HTTP) { h.base = base } }

// WithHTTPClient overrides the default 15s-timeout client.
func WithHTTPClient(c *http.Client) Option { return func(h *HTTP) { h.http = c } }

// NewHTTP builds the real transport. An empty token makes every Do return ErrDisabled.
func NewHTTP(token string, opts ...Option) *HTTP {
	h := &HTTP{token: token, base: DefaultBase, http: &http.Client{Timeout: 15 * time.Second}}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Enabled reports whether a token is configured.
func (h *HTTP) Enabled() bool { return h != nil && h.token != "" }

func (h *HTTP) Do(ctx context.Context, method, path string, body, out any) error {
	if !h.Enabled() {
		return ErrDisabled
	}
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+h.token)
	req.Header.Set("User-Agent", "dctl (https://github.com/Akayashuu/dctl, 1.0)")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	return json.Unmarshal(respBody, out)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/transport/`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/transport/transport.go internal/transport/transport_test.go
git commit -m "feat(transport): isolate Discord HTTP behind Doer interface"
```

---

## Task 2: Transport — stub Doer pour les tests

**Files:**
- Create: `internal/transport/stub.go`
- Test: `internal/transport/stub_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/transport/stub_test.go
package transport

import (
	"context"
	"net/http"
	"testing"
)

func TestStubCapturesAndReplies(t *testing.T) {
	s := NewStub()
	s.Reply(`{"id":"7"}`)
	var out struct{ ID string `json:"id"` }
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/transport/ -run Stub`
Expected: FAIL — `undefined: NewStub`.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/transport/stub.go
package transport

import (
	"context"
	"encoding/json"
)

// Call is one captured request.
type Call struct {
	Method string
	Path   string
	Body   any
}

// Stub is an in-memory Doer for tests: it records calls and replays canned
// JSON responses or errors in FIFO order. The zero value is unusable; use NewStub.
type Stub struct {
	calls   []Call
	replies []string
	err     error
}

// NewStub builds an empty stub.
func NewStub() *Stub { return &Stub{} }

// Reply queues a canned JSON response body (consumed in order by Do).
func (s *Stub) Reply(json string) *Stub { s.replies = append(s.replies, json); return s }

// Fail makes the next (and every) Do return err.
func (s *Stub) Fail(err error) *Stub { s.err = err; return s }

// Last returns the most recently captured call.
func (s *Stub) Last() Call { return s.calls[len(s.calls)-1] }

// Calls returns every captured call in order.
func (s *Stub) Calls() []Call { return s.calls }

func (s *Stub) Do(ctx context.Context, method, path string, body, out any) error {
	s.calls = append(s.calls, Call{Method: method, Path: path, Body: body})
	if s.err != nil {
		return s.err
	}
	if out == nil || len(s.replies) == 0 {
		return nil
	}
	reply := s.replies[0]
	s.replies = s.replies[1:]
	return json.Unmarshal([]byte(reply), out)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/transport/`
Expected: PASS (all transport tests).

- [ ] **Step 5: Commit**

```bash
git add internal/transport/stub.go internal/transport/stub_test.go
git commit -m "test(transport): add in-memory stub Doer"
```

---

## Task 3: DTO partagés + defaults

**Files:**
- Create: `types.go`
- Create: `defaults.go`
- Test: `defaults_test.go`

- [ ] **Step 1: Write the failing test**

```go
// defaults_test.go
package dctl

import (
	"context"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
)

func TestResolveChannelPrefersExplicit(t *testing.T) {
	d := &defaults{channel: "def"}
	got, err := d.resolveChannel("explicit")
	if err != nil || got != "explicit" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolveChannelFallsBackToDefault(t *testing.T) {
	d := &defaults{channel: "def"}
	got, err := d.resolveChannel("")
	if err != nil || got != "def" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolveChannelErrorsWhenNone(t *testing.T) {
	d := &defaults{}
	if _, err := d.resolveChannel(""); err != ErrNoChannel {
		t.Fatalf("err = %v, want ErrNoChannel", err)
	}
}

func TestResolveGuildUsesSoleGuild(t *testing.T) {
	s := transport.NewStub().Reply(`[{"id":"g1","name":"srv"}]`)
	d := &defaults{guilds: &Guilds{rt: s}}
	got, err := d.resolveGuild(context.Background(), "")
	if err != nil || got != "g1" {
		t.Fatalf("got %q, %v", got, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run Resolve`
Expected: FAIL — `undefined: defaults`, `undefined: Guilds`, `undefined: ErrNoChannel`.

- [ ] **Step 3: Write minimal implementation**

```go
// types.go

// Package dctl is a pure CLI client for the Discord bot REST API (v10).
// Auth is a bot token sent as `Authorization: Bot <token>`. No gateway/websocket:
// every call is on-demand HTTP. Mono-server by design (one bot token, one default
// channel).
package dctl

// Author identifies who wrote a message.
type Author struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Bot      bool   `json:"bot"`
}

// Attachment is a file uploaded alongside a message. URL points at the Discord CDN.
type Attachment struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
}

// Message is the subset of a Discord message we surface.
type Message struct {
	ID          string       `json:"id"`
	ChannelID   string       `json:"channel_id"`
	Content     string       `json:"content"`
	Author      Author       `json:"author"`
	Timestamp   string       `json:"timestamp"`
	Attachments []Attachment `json:"attachments"`
}

// Guild is a Discord server the bot belongs to.
type Guild struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Channel is a Discord channel. Type 0 is a text channel.
type Channel struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    int    `json:"type"`
	GuildID string `json:"guild_id,omitempty"`
}

// Role is a Discord guild role.
type Role struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Color       int    `json:"color"`
	Permissions string `json:"permissions"`
	Position    int    `json:"position"`
}

// GuildMember is a member of a guild.
type GuildMember struct {
	User  Author   `json:"user"`
	Nick  string   `json:"nick"`
	Roles []string `json:"roles"`
}
```

```go
// defaults.go
package dctl

import (
	"context"
	"errors"
)

// ErrNoChannel is returned when neither an explicit channel nor a default is set.
var ErrNoChannel = errors.New("dctl: no channel (DISCORD_CHANNEL_ID or --channel)")

// defaults resolves the default channel/guild shared across sub-clients.
type defaults struct {
	channel string
	guilds  *Guilds
}

func (d *defaults) resolveChannel(channelID string) (string, error) {
	if channelID != "" {
		return channelID, nil
	}
	if d.channel == "" {
		return "", ErrNoChannel
	}
	return d.channel, nil
}

func (d *defaults) resolveGuild(ctx context.Context, guildID string) (string, error) {
	if guildID != "" {
		return guildID, nil
	}
	g, err := d.guilds.Sole(ctx)
	if err != nil {
		return "", err
	}
	return g.ID, nil
}
```

- [ ] **Step 4: Run test to verify it fails on Guilds only, then implement Guilds in Task 4**

Run: `go test . -run Resolve`
Expected: FAIL — `undefined: Guilds` (resolved in Task 4). The `resolveChannel` tests cannot run until the package compiles, so **commit Task 3 together with Task 4** (Guilds is required for the package to build).

- [ ] **Step 5: Commit (deferred — see Task 4)**

No standalone commit: `types.go` + `defaults.go` reference `Guilds`. Proceed directly to Task 4, then commit Tasks 3+4 together.

---

## Task 4: Sous-client Guilds

**Files:**
- Modify: `guilds.go` (extract from current `channels.go`)
- Test: `guilds_test.go`

- [ ] **Step 1: Write the failing test**

```go
// guilds_test.go
package dctl

import (
	"context"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
)

func TestGuildsList(t *testing.T) {
	s := transport.NewStub().Reply(`[{"id":"g1","name":"srv"}]`)
	g := &Guilds{rt: s}
	gs, err := g.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(gs) != 1 || gs[0].ID != "g1" {
		t.Fatalf("guilds = %+v", gs)
	}
	if c := s.Last(); c.Method != "GET" || c.Path != "/users/@me/guilds" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestGuildsSoleErrorsOnMultiple(t *testing.T) {
	s := transport.NewStub().Reply(`[{"id":"a"},{"id":"b"}]`)
	g := &Guilds{rt: s}
	if _, err := g.Sole(context.Background()); err == nil {
		t.Fatal("want error on 2 guilds")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run Guilds`
Expected: FAIL — `undefined: Guilds`.

- [ ] **Step 3: Write minimal implementation**

```go
// guilds.go
package dctl

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Akayashuu/dctl/internal/transport"
)

// Guilds lists and resolves the bot's servers.
type Guilds struct {
	rt transport.Doer
}

// List returns the servers the bot is a member of.
func (g *Guilds) List(ctx context.Context) ([]Guild, error) {
	var gs []Guild
	if err := g.rt.Do(ctx, http.MethodGet, "/users/@me/guilds", nil, &gs); err != nil {
		return nil, err
	}
	return gs, nil
}

// Sole resolves the bot's single server (mono-server). Errors if the bot is in
// zero or several guilds, so callers never silently target the wrong one.
func (g *Guilds) Sole(ctx context.Context) (Guild, error) {
	gs, err := g.List(ctx)
	if err != nil {
		return Guild{}, err
	}
	switch len(gs) {
	case 0:
		return Guild{}, fmt.Errorf("dctl: bot is in no server (invite it first)")
	case 1:
		return gs[0], nil
	default:
		return Guild{}, fmt.Errorf("dctl: bot is in %d servers; pass a guild id", len(gs))
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test . -run 'Guilds|Resolve'`
Expected: PASS (Guilds tests + the Task 3 resolve tests now compile and pass).

- [ ] **Step 5: Commit (Tasks 3+4)**

```bash
git add types.go defaults.go defaults_test.go guilds.go guilds_test.go
git commit -m "feat: shared DTOs, defaults resolver, and Guilds sub-client"
```

---

## Task 5: Sous-client Channels (+ Update/Rename)

**Files:**
- Modify: `channels.go` (rewrite as `Channels` type)
- Test: `channels_test.go` (rewrite on stub)

- [ ] **Step 1: Write the failing test**

```go
// channels_test.go
package dctl

import (
	"context"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
)

func chans(s *transport.Stub) *Channels {
	return &Channels{rt: s, def: &defaults{guilds: &Guilds{rt: s}}}
}

func TestChannelsCreate(t *testing.T) {
	s := transport.NewStub().
		Reply(`[{"id":"g1"}]`).        // resolveGuild -> Sole -> List
		Reply(`{"id":"c1","name":"logs","type":0}`)
	ch, err := chans(s).Create(context.Background(), "g1", "logs")
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID != "c1" {
		t.Fatalf("channel = %+v", ch)
	}
	c := s.Last()
	if c.Method != "POST" || c.Path != "/guilds/g1/channels" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
	if c.Body.(map[string]any)["name"] != "logs" {
		t.Errorf("body = %v", c.Body)
	}
}

func TestChannelsRename(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"c1","name":"new"}`)
	ch, err := chans(s).Rename(context.Background(), "c1", "new")
	if err != nil {
		t.Fatal(err)
	}
	if ch.Name != "new" {
		t.Fatalf("name = %q", ch.Name)
	}
	c := s.Last()
	if c.Method != "PATCH" || c.Path != "/channels/c1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestChannelsDelete(t *testing.T) {
	s := transport.NewStub()
	if err := chans(s).Delete(context.Background(), "c1"); err != nil {
		t.Fatal(err)
	}
	if c := s.Last(); c.Method != "DELETE" || c.Path != "/channels/c1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run Channels`
Expected: FAIL — `undefined: Channels`.

- [ ] **Step 3: Write minimal implementation**

```go
// channels.go
package dctl

import (
	"context"
	"net/http"
	"strings"

	"github.com/Akayashuu/dctl/internal/transport"
)

// Channel type constants.
const (
	ChannelText     = 0
	ChannelCategory = 4
	ChannelForum    = 15
)

// Channels CRUDs guild channels.
type Channels struct {
	rt  transport.Doer
	def *defaults
}

// List returns the channels of a guild (or the sole guild when guildID is empty).
func (c *Channels) List(ctx context.Context, guildID string) ([]Channel, error) {
	gid, err := c.def.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	var chs []Channel
	if err := c.rt.Do(ctx, http.MethodGet, "/guilds/"+gid+"/channels", nil, &chs); err != nil {
		return nil, err
	}
	return chs, nil
}

// Get returns a channel by id.
func (c *Channels) Get(ctx context.Context, channelID string) (*Channel, error) {
	var ch Channel
	if err := c.rt.Do(ctx, http.MethodGet, "/channels/"+channelID, nil, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// Type returns the Discord channel-type integer for channelID.
func (c *Channels) Type(ctx context.Context, channelID string) (int, error) {
	ch, err := c.Get(ctx, channelID)
	if err != nil {
		return 0, err
	}
	return ch.Type, nil
}

// Create creates a text channel named name in guildID (or the sole guild).
func (c *Channels) Create(ctx context.Context, guildID, name string) (*Channel, error) {
	return c.create(ctx, guildID, map[string]any{"name": name, "type": ChannelText})
}

// CreateUnder creates a text channel nested under category parentID, in the sole guild.
func (c *Channels) CreateUnder(ctx context.Context, parentID, name string) (*Channel, error) {
	return c.create(ctx, "", map[string]any{"name": name, "type": ChannelText, "parent_id": parentID})
}

func (c *Channels) create(ctx context.Context, guildID string, body map[string]any) (*Channel, error) {
	gid, err := c.def.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	var ch Channel
	if err := c.rt.Do(ctx, http.MethodPost, "/guilds/"+gid+"/channels", body, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// Rename updates a channel's name.
func (c *Channels) Rename(ctx context.Context, channelID, name string) (*Channel, error) {
	return c.Update(ctx, channelID, map[string]any{"name": name})
}

// Update PATCHes arbitrary channel fields (name, parent_id, topic, position…).
func (c *Channels) Update(ctx context.Context, channelID string, fields map[string]any) (*Channel, error) {
	var ch Channel
	if err := c.rt.Do(ctx, http.MethodPatch, "/channels/"+channelID, fields, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// Delete deletes a channel by id.
func (c *Channels) Delete(ctx context.Context, channelID string) error {
	if channelID == "" {
		return ErrNoChannel
	}
	return c.rt.Do(ctx, http.MethodDelete, "/channels/"+channelID, nil, nil)
}

// Ensure returns the text channel named name in the guild, creating it if absent.
// Matching is case-insensitive.
func (c *Channels) Ensure(ctx context.Context, guildID, name string) (*Channel, error) {
	chs, err := c.List(ctx, guildID)
	if err != nil {
		return nil, err
	}
	for i := range chs {
		if chs[i].Type == ChannelText && strings.EqualFold(chs[i].Name, name) {
			return &chs[i], nil
		}
	}
	return c.Create(ctx, guildID, name)
}

// Archive archives a thread/forum-post, or deletes a plain text channel
// (text channels don't support PATCH {archived:true}).
func (c *Channels) Archive(ctx context.Context, channelID string) error {
	ct, err := c.Type(ctx, channelID)
	if err != nil {
		return err
	}
	if ct == 10 || ct == 11 || ct == 12 { // thread types
		return c.rt.Do(ctx, http.MethodPatch, "/channels/"+channelID, map[string]any{"archived": true}, nil)
	}
	return c.Delete(ctx, channelID)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run Channels`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add channels.go channels_test.go
git commit -m "feat(channels): Channels sub-client with update/rename"
```

---

## Task 6: Sous-client Messages (+ Edit/Delete, sans noMentions)

**Files:**
- Create: `messages.go` (extract message methods from old `dctl.go`)
- Test: `messages_test.go`

- [ ] **Step 1: Write the failing test**

```go
// messages_test.go
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
	// Discord returns newest-first; Read must reverse.
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run Messages`
Expected: FAIL — `undefined: Messages`.

- [ ] **Step 3: Write minimal implementation**

```go
// messages.go
package dctl

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Akayashuu/dctl/internal/transport"
)

// Messages CRUDs channel messages.
type Messages struct {
	rt  transport.Doer
	def *defaults
}

// Send posts content to channelID (or the default channel when empty).
func (m *Messages) Send(ctx context.Context, channelID, content string) (*Message, error) {
	return m.post(ctx, channelID, map[string]any{"content": content})
}

// Reply posts content as a reply to messageID in channelID (or the default channel).
func (m *Messages) Reply(ctx context.Context, channelID, messageID, content string) (*Message, error) {
	return m.post(ctx, channelID, map[string]any{
		"content":           content,
		"message_reference": map[string]any{"message_id": messageID, "fail_if_not_exists": false},
	})
}

func (m *Messages) post(ctx context.Context, channelID string, body map[string]any) (*Message, error) {
	ch, err := m.def.resolveChannel(channelID)
	if err != nil {
		return nil, err
	}
	var msg Message
	if err := m.rt.Do(ctx, http.MethodPost, "/channels/"+ch+"/messages", body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// Read returns up to limit (1..100, default 50) recent messages from channelID
// (or the default channel), oldest-first. When after is non-empty, only messages
// strictly newer than that id are returned (for polling).
func (m *Messages) Read(ctx context.Context, channelID string, limit int, after string) ([]Message, error) {
	ch, err := m.def.resolveChannel(channelID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	path := fmt.Sprintf("/channels/%s/messages?limit=%d", ch, limit)
	if after != "" {
		path += "&after=" + after
	}
	var msgs []Message
	if err := m.rt.Do(ctx, http.MethodGet, path, nil, &msgs); err != nil {
		return nil, err
	}
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// Edit replaces a message's content.
func (m *Messages) Edit(ctx context.Context, channelID, messageID, content string) (*Message, error) {
	ch, err := m.def.resolveChannel(channelID)
	if err != nil {
		return nil, err
	}
	var msg Message
	if err := m.rt.Do(ctx, http.MethodPatch, "/channels/"+ch+"/messages/"+messageID,
		map[string]any{"content": content}, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// Delete removes a message.
func (m *Messages) Delete(ctx context.Context, channelID, messageID string) error {
	ch, err := m.def.resolveChannel(channelID)
	if err != nil {
		return err
	}
	return m.rt.Do(ctx, http.MethodDelete, "/channels/"+ch+"/messages/"+messageID, nil, nil)
}

// LastMessageAt returns the timestamp of the channel's most recent message, or
// the zero Time if the channel has no messages.
func (m *Messages) LastMessageAt(ctx context.Context, channelID string) (time.Time, error) {
	msgs, err := m.Read(ctx, channelID, 1, "")
	if err != nil {
		return time.Time{}, err
	}
	if len(msgs) == 0 {
		return time.Time{}, nil
	}
	ts, err := time.Parse(time.RFC3339, msgs[len(msgs)-1].Timestamp)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse message timestamp %q: %w", msgs[len(msgs)-1].Timestamp, err)
	}
	return ts, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run Messages`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add messages.go messages_test.go
git commit -m "feat(messages): Messages sub-client with edit/delete, no allowed_mentions guard"
```

---

## Task 7: Sous-client Reactions

**Files:**
- Modify: `reactions.go`
- Test: `reactions_test.go`

- [ ] **Step 1: Write the failing test**

```go
// reactions_test.go
package dctl

import (
	"context"
	"net/url"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
)

func TestReactionsAddEncodesEmoji(t *testing.T) {
	s := transport.NewStub()
	r := &Reactions{rt: s}
	if err := r.Add(context.Background(), "c", "m", "👍"); err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	wantPath := "/channels/c/messages/m/reactions/" + url.PathEscape("👍") + "/@me"
	if c.Method != "PUT" || c.Path != wantPath {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestReactionsRemove(t *testing.T) {
	s := transport.NewStub()
	r := &Reactions{rt: s}
	if err := r.Remove(context.Background(), "c", "m", "👍"); err != nil {
		t.Fatal(err)
	}
	if c := s.Last(); c.Method != "DELETE" {
		t.Errorf("method = %s", c.Method)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run Reactions`
Expected: FAIL — `undefined: Reactions`.

- [ ] **Step 3: Write minimal implementation**

```go
// reactions.go
package dctl

import (
	"context"
	"net/http"
	"net/url"

	"github.com/Akayashuu/dctl/internal/transport"
)

// Reactions adds/removes the bot's reactions on a message.
type Reactions struct {
	rt transport.Doer
}

// Add reacts to messageID with emoji (unicode or "name:id" for custom).
func (r *Reactions) Add(ctx context.Context, channelID, messageID, emoji string) error {
	return r.do(ctx, http.MethodPut, channelID, messageID, emoji)
}

// Remove removes the bot's own reaction.
func (r *Reactions) Remove(ctx context.Context, channelID, messageID, emoji string) error {
	return r.do(ctx, http.MethodDelete, channelID, messageID, emoji)
}

func (r *Reactions) do(ctx context.Context, method, channelID, messageID, emoji string) error {
	path := "/channels/" + channelID + "/messages/" + messageID + "/reactions/" + url.PathEscape(emoji) + "/@me"
	return r.rt.Do(ctx, method, path, nil, nil)
}
```

Note: if the existing `reactions.go` builds the path differently, this rewrite supersedes it. Verify the existing `reactions_test.go` emoji-encoding expectation matches `url.PathEscape`; keep whichever encoding the old passing test asserted, adjusting the new test accordingly.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run Reactions`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add reactions.go reactions_test.go
git commit -m "feat(reactions): Reactions sub-client"
```

---

## Task 8: Sous-client Threads

**Files:**
- Modify: `threads.go`
- Test: `threads_test.go`

- [ ] **Step 1: Write the failing test**

```go
// threads_test.go
package dctl

import (
	"context"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
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
	if c := s.Last(); c.Path != "/channels/c/messages/m/threads" {
		t.Errorf("path = %s", c.Path)
	}
}

func TestThreadsForumPost(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"p1","type":11}`)
	if _, err := thr(s).ForumPost(context.Background(), "f1", "title", "body"); err != nil {
		t.Fatal(err)
	}
	if c := s.Last(); c.Path != "/channels/f1/threads" {
		t.Errorf("path = %s", c.Path)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run Threads`
Expected: FAIL — `undefined: Threads`.

- [ ] **Step 3: Write minimal implementation**

```go
// threads.go
package dctl

import (
	"context"
	"net/http"

	"github.com/Akayashuu/dctl/internal/transport"
)

// Threads creates threads and forum posts.
type Threads struct {
	rt  transport.Doer
	def *defaults
}

// Start opens a public thread off messageID in channelID.
func (t *Threads) Start(ctx context.Context, channelID, messageID, name string) (*Channel, error) {
	var ch Channel
	if err := t.rt.Do(ctx, http.MethodPost, "/channels/"+channelID+"/messages/"+messageID+"/threads",
		map[string]any{"name": name}, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// CreateForum creates a forum channel named name in guildID (or the sole guild).
func (t *Threads) CreateForum(ctx context.Context, guildID, name string) (*Channel, error) {
	gid, err := t.def.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	var ch Channel
	if err := t.rt.Do(ctx, http.MethodPost, "/guilds/"+gid+"/channels",
		map[string]any{"name": name, "type": ChannelForum}, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// ForumPost creates a thread (post) in forum forumID with an initial message.
func (t *Threads) ForumPost(ctx context.Context, forumID, name, content string) (*Channel, error) {
	var ch Channel
	if err := t.rt.Do(ctx, http.MethodPost, "/channels/"+forumID+"/threads",
		map[string]any{"name": name, "message": map[string]any{"content": content}}, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}
```

Note: cross-check the request bodies against the current `threads.go` (esp. ForumPost's `message` shape and any `auto_archive_duration`) and preserve whatever the existing implementation sent.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run Threads`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add threads.go threads_test.go
git commit -m "feat(threads): Threads sub-client"
```

---

## Task 9: Sous-client Components

**Files:**
- Modify: `components.go`
- Test: `components_test.go`

- [ ] **Step 1: Write the failing test**

```go
// components_test.go
package dctl

import (
	"context"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run Components`
Expected: FAIL — `undefined: Components` (or `SelectOption`).

- [ ] **Step 3: Write minimal implementation**

Port the existing `components.go` logic verbatim into a `Components` type holding `rt transport.Doer` and `def *defaults`. Replace `c.post(...)`/`c.newRequest`+`c.do` calls with `c.rt.Do(...)`, and **drop the `allowed_mentions: noMentions` injection** from the message payload.

```go
// components.go
package dctl

import (
	"context"
	"net/http"

	"github.com/Akayashuu/dctl/internal/transport"
)

// SelectOption is one entry in a select menu.
type SelectOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Components sends message components (select menus) and acks their interactions.
type Components struct {
	rt  transport.Doer
	def *defaults
}

// SendSelectMenu posts a message carrying a single-select menu (customID).
// When replyTo is set, the message references it.
func (c *Components) SendSelectMenu(ctx context.Context, channelID, replyTo, content, customID string, options []SelectOption) (*Message, error) {
	ch, err := c.def.resolveChannel(channelID)
	if err != nil {
		return nil, err
	}
	opts := make([]map[string]any, 0, len(options))
	for _, o := range options {
		opts = append(opts, map[string]any{"label": o.Label, "value": o.Value})
	}
	body := map[string]any{
		"content": content,
		"components": []map[string]any{{
			"type": 1,
			"components": []map[string]any{{
				"type": 3, "custom_id": customID, "options": opts,
			}},
		}},
	}
	if replyTo != "" {
		body["message_reference"] = map[string]any{"message_id": replyTo, "fail_if_not_exists": false}
	}
	var msg Message
	if err := c.rt.Do(ctx, http.MethodPost, "/channels/"+ch+"/messages", body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// Ack acknowledges a component interaction by updating the source message (type 7).
func (c *Components) Ack(ctx context.Context, id, token, content string) error {
	body := map[string]any{"type": 7, "data": map[string]any{"content": content, "components": []any{}}}
	return c.rt.Do(ctx, http.MethodPost, "/interactions/"+id+"/"+token+"/callback", body, nil)
}
```

Note: align the `Ack` body and select-menu structure with the existing `components.go` (preserve the exact `type`/field shapes the old passing test asserted; the old `components_test.go` is the source of truth for the payload).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run Components`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add components.go components_test.go
git commit -m "feat(components): Components sub-client, no allowed_mentions guard"
```

---

## Task 10: Sous-client Interactions

**Files:**
- Modify: `interactions.go`
- Test: `interactions_autocomplete_test.go` (rewrite on stub) + new assertions

- [ ] **Step 1: Write the failing test**

```go
// interactions_autocomplete_test.go
package dctl

import (
	"context"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
)

func TestInteractionsRespondNoAllowedMentions(t *testing.T) {
	s := transport.NewStub()
	in := &Interactions{rt: s, def: &defaults{guilds: &Guilds{rt: s}}}
	err := in.Respond(context.Background(), "id", "tok", Response{Content: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Path != "/interactions/id/tok/callback" {
		t.Errorf("path = %s", c.Path)
	}
	data := c.Body.(map[string]any)["data"].(map[string]any)
	if _, present := data["allowed_mentions"]; present {
		t.Error("allowed_mentions must NOT be injected")
	}
}

func TestInteractionsAutocompleteTrimsTo25(t *testing.T) {
	s := transport.NewStub()
	in := &Interactions{rt: s}
	choices := make([]AutocompleteChoice, 30)
	for i := range choices {
		choices[i] = AutocompleteChoice{Name: "n", Value: "v"}
	}
	if err := in.RespondAutocomplete(context.Background(), "id", "tok", choices); err != nil {
		t.Fatal(err)
	}
	body := s.Last().Body.(map[string]any)
	got := body["data"].(map[string]any)["choices"].([]map[string]any)
	if len(got) != 25 {
		t.Errorf("choices = %d, want 25", len(got))
	}
}
```

Keep the existing pure-function tests for `Opt`, `OptBool`, `Focused`, `Subcommand` (they don't touch the transport — port them unchanged).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run Interactions`
Expected: FAIL — `undefined: Interactions`.

- [ ] **Step 3: Write minimal implementation**

Port `interactions.go` into an `Interactions` type. Keep all the pure helpers (`Interaction`, `InteractionData`, `Opt`, `OptBool`, `Focused`, `Subcommand`, constants) as-is. Convert the `Client` methods to methods on `Interactions` using `in.rt.Do`, **removing every `allowed_mentions: noMentions`**:

```go
// interactions.go — Interactions type (pure helpers above unchanged)
type Interactions struct {
	rt  transport.Doer
	def *defaults
}

func (in *Interactions) AppID(ctx context.Context) (string, error) {
	var u struct{ ID string `json:"id"` }
	if err := in.rt.Do(ctx, http.MethodGet, "/users/@me", nil, &u); err != nil {
		return "", err
	}
	return u.ID, nil
}

func (in *Interactions) RegisterCommands(ctx context.Context, commands []map[string]any) error {
	appID, err := in.AppID(ctx)
	if err != nil {
		return err
	}
	gid, err := in.def.resolveGuild(ctx, "")
	if err != nil {
		return err
	}
	return in.rt.Do(ctx, http.MethodPut, "/applications/"+appID+"/guilds/"+gid+"/commands", commands, nil)
}

func (in *Interactions) Respond(ctx context.Context, id, token string, r Response) error {
	data := map[string]any{"content": r.Content}
	if r.Ephemeral {
		data["flags"] = 1 << 6
	}
	return in.rt.Do(ctx, http.MethodPost, "/interactions/"+id+"/"+token+"/callback",
		map[string]any{"type": 4, "data": data}, nil)
}

func (in *Interactions) Defer(ctx context.Context, id, token string, ephemeral bool) error {
	data := map[string]any{}
	if ephemeral {
		data["flags"] = 1 << 6
	}
	return in.rt.Do(ctx, http.MethodPost, "/interactions/"+id+"/"+token+"/callback",
		map[string]any{"type": 5, "data": data}, nil)
}

func (in *Interactions) RespondAutocomplete(ctx context.Context, id, token string, choices []AutocompleteChoice) error {
	if len(choices) > 25 {
		choices = choices[:25]
	}
	cs := make([]map[string]any, 0, len(choices))
	for _, ch := range choices {
		cs = append(cs, map[string]any{"name": ch.Name, "value": ch.Value})
	}
	return in.rt.Do(ctx, http.MethodPost, "/interactions/"+id+"/"+token+"/callback",
		map[string]any{"type": 8, "data": map[string]any{"choices": cs}}, nil)
}

func (in *Interactions) EditResponse(ctx context.Context, appID, token string, r Response) error {
	return in.rt.Do(ctx, http.MethodPatch, "/webhooks/"+appID+"/"+token+"/messages/@original",
		map[string]any{"content": r.Content}, nil)
}

func (in *Interactions) UpsertStatusMessage(ctx context.Context, channelID, msgID, content string) (string, error) {
	if msgID != "" {
		err := in.rt.Do(ctx, http.MethodPatch, "/channels/"+channelID+"/messages/"+msgID,
			map[string]any{"content": content}, nil)
		if err == nil {
			return msgID, nil
		}
	}
	var msg Message
	if err := in.rt.Do(ctx, http.MethodPost, "/channels/"+channelID+"/messages",
		map[string]any{"content": content}, &msg); err != nil {
		return "", err
	}
	return msg.ID, nil
}
```

Rename note: `RespondInteraction`→`Respond`, `DeferInteraction`→`Defer`, `EditInteractionResponse`→`EditResponse`. The `UpsertStatusMessage` doc comment must drop the "daemon" framing (just: "edits the existing status message or sends a new one").

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run Interactions`
Expected: PASS (transport tests + ported pure-helper tests).

- [ ] **Step 5: Commit**

```bash
git add interactions.go interactions_autocomplete_test.go
git commit -m "feat(interactions): Interactions sub-client, no allowed_mentions guard"
```

---

## Task 11: Sous-client Roles (nouveau)

**Files:**
- Create: `roles.go`
- Test: `roles_test.go`

- [ ] **Step 1: Write the failing test**

```go
// roles_test.go
package dctl

import (
	"context"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
)

func roles(s *transport.Stub) *Roles {
	return &Roles{rt: s, def: &defaults{guilds: &Guilds{rt: s}}}
}

func TestRolesList(t *testing.T) {
	s := transport.NewStub().
		Reply(`[{"id":"g1"}]`).               // resolveGuild
		Reply(`[{"id":"r1","name":"mod"}]`)
	rs, err := roles(s).List(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Name != "mod" {
		t.Fatalf("roles = %+v", rs)
	}
	if c := s.Last(); c.Path != "/guilds/g1/roles" {
		t.Errorf("path = %s", c.Path)
	}
}

func TestRolesCreate(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"r2","name":"mod"}`)
	r, err := roles(s).Create(context.Background(), "g1", "mod")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "r2" {
		t.Fatalf("role = %+v", r)
	}
	c := s.Last()
	if c.Method != "POST" || c.Path != "/guilds/g1/roles" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestRolesDelete(t *testing.T) {
	s := transport.NewStub()
	if err := roles(s).Delete(context.Background(), "g1", "r2"); err != nil {
		t.Fatal(err)
	}
	if c := s.Last(); c.Method != "DELETE" || c.Path != "/guilds/g1/roles/r2" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestRolesAssignToMember(t *testing.T) {
	s := transport.NewStub()
	if err := roles(s).Assign(context.Background(), "g1", "u1", "r2"); err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Method != "PUT" || c.Path != "/guilds/g1/members/u1/roles/r2" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run Roles`
Expected: FAIL — `undefined: Roles`.

- [ ] **Step 3: Write minimal implementation**

```go
// roles.go
package dctl

import (
	"context"
	"net/http"

	"github.com/Akayashuu/dctl/internal/transport"
)

// Roles CRUDs guild roles and assigns them to members.
type Roles struct {
	rt  transport.Doer
	def *defaults
}

// List returns the roles of guildID (or the sole guild when empty).
func (r *Roles) List(ctx context.Context, guildID string) ([]Role, error) {
	gid, err := r.def.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	var rs []Role
	if err := r.rt.Do(ctx, http.MethodGet, "/guilds/"+gid+"/roles", nil, &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// Create creates a role named name in guildID (or the sole guild).
func (r *Roles) Create(ctx context.Context, guildID, name string) (*Role, error) {
	gid, err := r.def.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	var role Role
	if err := r.rt.Do(ctx, http.MethodPost, "/guilds/"+gid+"/roles",
		map[string]any{"name": name}, &role); err != nil {
		return nil, err
	}
	return &role, nil
}

// Update PATCHes role fields (name, color, permissions…).
func (r *Roles) Update(ctx context.Context, guildID, roleID string, fields map[string]any) (*Role, error) {
	gid, err := r.def.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	var role Role
	if err := r.rt.Do(ctx, http.MethodPatch, "/guilds/"+gid+"/roles/"+roleID, fields, &role); err != nil {
		return nil, err
	}
	return &role, nil
}

// Delete removes a role.
func (r *Roles) Delete(ctx context.Context, guildID, roleID string) error {
	gid, err := r.def.resolveGuild(ctx, guildID)
	if err != nil {
		return err
	}
	return r.rt.Do(ctx, http.MethodDelete, "/guilds/"+gid+"/roles/"+roleID, nil, nil)
}

// Assign grants roleID to member userID.
func (r *Roles) Assign(ctx context.Context, guildID, userID, roleID string) error {
	gid, err := r.def.resolveGuild(ctx, guildID)
	if err != nil {
		return err
	}
	return r.rt.Do(ctx, http.MethodPut, "/guilds/"+gid+"/members/"+userID+"/roles/"+roleID, nil, nil)
}

// Unassign removes roleID from member userID.
func (r *Roles) Unassign(ctx context.Context, guildID, userID, roleID string) error {
	gid, err := r.def.resolveGuild(ctx, guildID)
	if err != nil {
		return err
	}
	return r.rt.Do(ctx, http.MethodDelete, "/guilds/"+gid+"/members/"+userID+"/roles/"+roleID, nil, nil)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run Roles`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add roles.go roles_test.go
git commit -m "feat(roles): Roles sub-client (CRUD + member assign)"
```

---

## Task 12: Sous-client Members (nouveau)

**Files:**
- Create: `members.go`
- Test: `members_test.go`

- [ ] **Step 1: Write the failing test**

```go
// members_test.go
package dctl

import (
	"context"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
)

func members(s *transport.Stub) *Members {
	return &Members{rt: s, def: &defaults{guilds: &Guilds{rt: s}}}
}

func TestMembersGet(t *testing.T) {
	s := transport.NewStub().Reply(`{"user":{"id":"u1","username":"bob"},"roles":["r1"]}`)
	m, err := members(s).Get(context.Background(), "g1", "u1")
	if err != nil {
		t.Fatal(err)
	}
	if m.User.Username != "bob" {
		t.Fatalf("member = %+v", m)
	}
	if c := s.Last(); c.Path != "/guilds/g1/members/u1" {
		t.Errorf("path = %s", c.Path)
	}
}

func TestMembersKick(t *testing.T) {
	s := transport.NewStub()
	if err := members(s).Kick(context.Background(), "g1", "u1"); err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Method != "DELETE" || c.Path != "/guilds/g1/members/u1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}

func TestMembersBan(t *testing.T) {
	s := transport.NewStub()
	if err := members(s).Ban(context.Background(), "g1", "u1"); err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Method != "PUT" || c.Path != "/guilds/g1/bans/u1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run Members`
Expected: FAIL — `undefined: Members`.

- [ ] **Step 3: Write minimal implementation**

```go
// members.go
package dctl

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Akayashuu/dctl/internal/transport"
)

// Members lists and moderates guild members.
type Members struct {
	rt  transport.Doer
	def *defaults
}

// List returns up to limit (1..1000, default 100) members of guildID (or the sole guild).
func (m *Members) List(ctx context.Context, guildID string, limit int) ([]GuildMember, error) {
	gid, err := m.def.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var ms []GuildMember
	path := fmt.Sprintf("/guilds/%s/members?limit=%d", gid, limit)
	if err := m.rt.Do(ctx, http.MethodGet, path, nil, &ms); err != nil {
		return nil, err
	}
	return ms, nil
}

// Get returns a single member.
func (m *Members) Get(ctx context.Context, guildID, userID string) (*GuildMember, error) {
	gid, err := m.def.resolveGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	var gm GuildMember
	if err := m.rt.Do(ctx, http.MethodGet, "/guilds/"+gid+"/members/"+userID, nil, &gm); err != nil {
		return nil, err
	}
	return &gm, nil
}

// Kick removes a member from the guild.
func (m *Members) Kick(ctx context.Context, guildID, userID string) error {
	gid, err := m.def.resolveGuild(ctx, guildID)
	if err != nil {
		return err
	}
	return m.rt.Do(ctx, http.MethodDelete, "/guilds/"+gid+"/members/"+userID, nil, nil)
}

// Ban bans a member from the guild.
func (m *Members) Ban(ctx context.Context, guildID, userID string) error {
	gid, err := m.def.resolveGuild(ctx, guildID)
	if err != nil {
		return err
	}
	return m.rt.Do(ctx, http.MethodPut, "/guilds/"+gid+"/bans/"+userID, nil, nil)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run Members`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add members.go members_test.go
git commit -m "feat(members): Members sub-client (list/get/kick/ban)"
```

---

## Task 13: Sous-client Permissions (nouveau)

**Files:**
- Create: `permissions.go`
- Test: `permissions_test.go`

- [ ] **Step 1: Write the failing test**

```go
// permissions_test.go
package dctl

import (
	"context"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
)

func TestPermissionsSetOverwrite(t *testing.T) {
	s := transport.NewStub()
	p := &Permissions{rt: s}
	// overwrite type 0 = role, allow/deny are bit-strings
	err := p.Set(context.Background(), "c1", "r1", 0, "1024", "0")
	if err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Method != "PUT" || c.Path != "/channels/c1/permissions/r1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
	body := c.Body.(map[string]any)
	if body["allow"] != "1024" || body["type"] != 0 {
		t.Errorf("body = %v", body)
	}
}

func TestPermissionsRemoveOverwrite(t *testing.T) {
	s := transport.NewStub()
	p := &Permissions{rt: s}
	if err := p.Remove(context.Background(), "c1", "r1"); err != nil {
		t.Fatal(err)
	}
	c := s.Last()
	if c.Method != "DELETE" || c.Path != "/channels/c1/permissions/r1" {
		t.Errorf("call = %s %s", c.Method, c.Path)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run Permissions`
Expected: FAIL — `undefined: Permissions`.

- [ ] **Step 3: Write minimal implementation**

```go
// permissions.go
package dctl

import (
	"context"
	"net/http"

	"github.com/Akayashuu/dctl/internal/transport"
)

// Permissions edits channel permission overwrites.
type Permissions struct {
	rt transport.Doer
}

// Overwrite target types.
const (
	OverwriteRole   = 0
	OverwriteMember = 1
)

// Set creates/updates a permission overwrite on channelID for overwriteID
// (a role or member id). allow/deny are Discord permission bit-strings;
// kind is OverwriteRole or OverwriteMember.
func (p *Permissions) Set(ctx context.Context, channelID, overwriteID string, kind int, allow, deny string) error {
	return p.rt.Do(ctx, http.MethodPut, "/channels/"+channelID+"/permissions/"+overwriteID,
		map[string]any{"type": kind, "allow": allow, "deny": deny}, nil)
}

// Remove deletes a permission overwrite.
func (p *Permissions) Remove(ctx context.Context, channelID, overwriteID string) error {
	return p.rt.Do(ctx, http.MethodDelete, "/channels/"+channelID+"/permissions/"+overwriteID, nil, nil)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run Permissions`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add permissions.go permissions_test.go
git commit -m "feat(permissions): Permissions sub-client (channel overwrites)"
```

---

## Task 14: Sous-client Webhooks (nouveau)

**Files:**
- Create: `webhooks.go`
- Test: `webhooks_test.go`

- [ ] **Step 1: Write the failing test**

```go
// webhooks_test.go
package dctl

import (
	"context"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run Webhooks`
Expected: FAIL — `undefined: Webhooks`.

- [ ] **Step 3: Write minimal implementation**

```go
// webhooks.go
package dctl

import (
	"context"
	"net/http"

	"github.com/Akayashuu/dctl/internal/transport"
)

// Webhook is a Discord channel webhook.
type Webhook struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

// Webhooks CRUDs and executes channel webhooks.
type Webhooks struct {
	rt transport.Doer
}

// Create creates a webhook named name on channelID.
func (w *Webhooks) Create(ctx context.Context, channelID, name string) (*Webhook, error) {
	var hook Webhook
	if err := w.rt.Do(ctx, http.MethodPost, "/channels/"+channelID+"/webhooks",
		map[string]any{"name": name}, &hook); err != nil {
		return nil, err
	}
	return &hook, nil
}

// List returns the webhooks of channelID.
func (w *Webhooks) List(ctx context.Context, channelID string) ([]Webhook, error) {
	var hooks []Webhook
	if err := w.rt.Do(ctx, http.MethodGet, "/channels/"+channelID+"/webhooks", nil, &hooks); err != nil {
		return nil, err
	}
	return hooks, nil
}

// Delete removes a webhook by id.
func (w *Webhooks) Delete(ctx context.Context, webhookID string) error {
	return w.rt.Do(ctx, http.MethodDelete, "/webhooks/"+webhookID, nil, nil)
}

// Execute posts content through a webhook using its id+token.
func (w *Webhooks) Execute(ctx context.Context, webhookID, token, content string) error {
	return w.rt.Do(ctx, http.MethodPost, "/webhooks/"+webhookID+"/"+token,
		map[string]any{"content": content}, nil)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test . -run Webhooks`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add webhooks.go webhooks_test.go
git commit -m "feat(webhooks): Webhooks sub-client (create/list/delete/execute)"
```

---

## Task 15: Façade Client + accesseurs

**Files:**
- Modify: `dctl.go` (reduce to facade)
- Test: `dctl_test.go` (rewrite)

- [ ] **Step 1: Write the failing test**

```go
// dctl_test.go
package dctl

import (
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
)

func TestNewWiresSubClientsWithSharedDefaults(t *testing.T) {
	c := New("tok", "chan123")
	if !c.Enabled() {
		t.Error("want enabled")
	}
	if c.DefaultChannel() != "chan123" {
		t.Errorf("default channel = %q", c.DefaultChannel())
	}
	// every accessor returns a wired sub-client (non-nil)
	if c.Messages() == nil || c.Channels() == nil || c.Roles() == nil ||
		c.Members() == nil || c.Reactions() == nil || c.Threads() == nil ||
		c.Permissions() == nil || c.Webhooks() == nil || c.Interactions() == nil ||
		c.Components() == nil || c.Guilds() == nil {
		t.Fatal("an accessor returned nil")
	}
}

func TestNewWithTransportInjectsStub(t *testing.T) {
	s := transport.NewStub().Reply(`{"id":"m1"}`)
	c := newWith(s, "chan")
	if c.Messages() == nil {
		t.Fatal("messages nil")
	}
}

func TestDisabledClientErrors(t *testing.T) {
	c := New("", "")
	_, err := c.Messages().Send(t.Context(), "x", "hi")
	if err != transport.ErrDisabled {
		t.Errorf("err = %v, want ErrDisabled", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test . -run 'New|Disabled'`
Expected: FAIL — `New` no longer matches the new shape / `newWith` undefined / accessor methods undefined.

- [ ] **Step 3: Write minimal implementation**

```go
// dctl.go
package dctl

import (
	"net/http"

	"github.com/Akayashuu/dctl/internal/transport"
)

// Client is the dctl façade: it wires the HTTP transport into per-resource
// sub-clients sharing one default channel/guild resolver. Build it with New.
type Client struct {
	rt       transport.Doer
	enabled  func() bool
	defChan  string
	def      *defaults
	guilds   *Guilds
}

// Option configures a Client.
type Option func(*clientConfig)

type clientConfig struct {
	httpClient *http.Client
}

// WithHTTPClient overrides the default 15s-timeout HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *clientConfig) { c.httpClient = h }
}

// New builds a Client. token is the bot token (kept in memory only). defaultChannel
// is the channel that message ops target when no explicit channel id is passed.
func New(token, defaultChannel string, opts ...Option) *Client {
	cfg := &clientConfig{}
	for _, o := range opts {
		o(cfg)
	}
	var topts []transport.Option
	if cfg.httpClient != nil {
		topts = append(topts, transport.WithHTTPClient(cfg.httpClient))
	}
	rt := transport.NewHTTP(token, topts...)
	c := newWith(rt, defaultChannel)
	c.enabled = rt.Enabled
	return c
}

// newWith wires a Client around an arbitrary Doer (used by tests with a stub).
func newWith(rt transport.Doer, defaultChannel string) *Client {
	guilds := &Guilds{rt: rt}
	def := &defaults{channel: defaultChannel, guilds: guilds}
	return &Client{
		rt:      rt,
		enabled: func() bool { return true },
		defChan: defaultChannel,
		def:     def,
		guilds:  guilds,
	}
}

// Enabled reports whether a bot token is configured.
func (c *Client) Enabled() bool { return c != nil && c.enabled() }

// DefaultChannel returns the configured default channel id.
func (c *Client) DefaultChannel() string {
	if c == nil {
		return ""
	}
	return c.defChan
}

// Accessors — each returns a sub-client sharing the transport and defaults.
func (c *Client) Guilds() *Guilds             { return c.guilds }
func (c *Client) Messages() *Messages         { return &Messages{rt: c.rt, def: c.def} }
func (c *Client) Channels() *Channels         { return &Channels{rt: c.rt, def: c.def} }
func (c *Client) Roles() *Roles               { return &Roles{rt: c.rt, def: c.def} }
func (c *Client) Members() *Members           { return &Members{rt: c.rt, def: c.def} }
func (c *Client) Reactions() *Reactions       { return &Reactions{rt: c.rt} }
func (c *Client) Threads() *Threads           { return &Threads{rt: c.rt, def: c.def} }
func (c *Client) Permissions() *Permissions   { return &Permissions{rt: c.rt} }
func (c *Client) Webhooks() *Webhooks         { return &Webhooks{rt: c.rt} }
func (c *Client) Interactions() *Interactions { return &Interactions{rt: c.rt, def: c.def} }
func (c *Client) Components() *Components      { return &Components{rt: c.rt, def: c.def} }
```

Note: `ErrDisabled` now lives in `transport`; if any code referenced `dctl.ErrDisabled`, either re-export it (`var ErrDisabled = transport.ErrDisabled` in `dctl.go`) or update callers. The package doc comment moved to `types.go` in Task 3 — `dctl.go` no longer carries it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test .`
Expected: PASS (whole package compiles and all sub-client tests green).

- [ ] **Step 5: Commit**

```bash
git add dctl.go dctl_test.go
git commit -m "feat: reduce Client to a façade with per-resource accessors"
```

---

## Task 16: Migrer `dctl_lastmessage_test.go` + purge finale & vérification

**Files:**
- Modify: `dctl_lastmessage_test.go` (port to `Messages` + stub)
- Modify: `purity_test.go` (verify still valid)
- Verify: whole package

- [ ] **Step 1: Port the lastmessage test to the new shape**

```go
// dctl_lastmessage_test.go
package dctl

import (
	"context"
	"testing"

	"github.com/Akayashuu/dctl/internal/transport"
)

func TestLastMessageAtReturnsZeroWhenEmpty(t *testing.T) {
	s := transport.NewStub().Reply(`[]`)
	m := &Messages{rt: s, def: &defaults{channel: "c"}}
	ts, err := m.LastMessageAt(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	if !ts.IsZero() {
		t.Errorf("ts = %v, want zero", ts)
	}
}

func TestLastMessageAtParsesTimestamp(t *testing.T) {
	s := transport.NewStub().Reply(`[{"id":"1","timestamp":"2026-06-16T12:00:00.000000+00:00"}]`)
	m := &Messages{rt: s, def: &defaults{channel: "c"}}
	ts, err := m.LastMessageAt(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	if ts.Year() != 2026 {
		t.Errorf("year = %d", ts.Year())
	}
}
```

- [ ] **Step 2: Verify the timestamp format matches Discord**

Discord timestamps are RFC3339 with microseconds; `time.Parse(time.RFC3339, ...)` accepts them. If the parse test fails, switch the canned timestamp in the test to `2026-06-16T12:00:00+00:00` (RFC3339 without fractional seconds) — both must parse. Keep whichever the implementation's `time.Parse(time.RFC3339, ...)` accepts.

- [ ] **Step 3: Grep for residual ecosystem vocabulary and remove it**

Run:
```bash
grep -rniE 'prospector|bridge|daemon|claude|agent|session clean|slow clone|noMentions|allowed_mentions|fan-out' --include='*.go' .
```
Expected: **no matches** in non-test source. Any hit in a doc comment must be reworded to pure-CLI framing; any `allowed_mentions`/`noMentions` in source is a leftover guard and must be deleted.

- [ ] **Step 4: Full verification sweep**

Run:
```bash
go build ./... && go vet ./... && gofmt -l . && go test ./...
```
Expected:
- `go build` — no output (success)
- `go vet` — no output
- `gofmt -l .` — no output (all files formatted)
- `go test ./...` — all packages PASS

If `gofmt -l .` lists files, run `gofmt -w <files>` and re-run.

- [ ] **Step 5: Confirm purity test still guards dependencies**

Run: `go test . -run Purity` (or the actual test name in `purity_test.go`).
Expected: PASS — the module must still depend only on the standard library (`go.mod` has no `require` block beyond the module line).

- [ ] **Step 6: Commit**

```bash
git add dctl_lastmessage_test.go purity_test.go
git commit -m "test: port lastmessage test; verify pure-CLI purge and stdlib-only"
```

---

## Self-Review

**Spec coverage check:**
- 3 couches (transport / sous-clients / façade) → Tasks 1–2 (transport), 4–14 (sous-clients), 15 (façade). ✓
- Package public unique + `internal/transport` → structure des fichiers. ✓
- CRUD complet : Channels (T5), Messages (T6), Roles (T11), Members (T12), Reactions (T7), Threads/Forums (T8), Permissions (T13), Webhooks (T14), Interactions (T10), Components (T9), Guilds (T4). ✓
- Purification A (doc) → package doc déplacé/nettoyé en T3, "daemon" retiré en T10, grep final T16. ✓
- Purification B (suppression noMentions) → assertions explicites "no allowed_mentions" en T6/T9/T10, grep final T16. ✓
- Tests sur stub `Doer` → T2 fournit le stub, toutes les tâches l'utilisent. ✓
- `purity_test.go` reste vert → T16 step 5. ✓
- Renommages big-bang → listés en T5–T10, façade T15. ✓

**Placeholder scan:** aucun TBD/TODO ; chaque step de code porte le code réel. Les "Note:" pointent vers le fichier existant comme source de vérité pour préserver les payloads exacts (reactions emoji-encoding, threads body, components shape) — ce sont des instructions de vérification, pas des placeholders.

**Type consistency:** `transport.Doer` / `transport.NewHTTP` / `transport.NewStub` cohérents partout ; `defaults{channel, guilds}` et `*Guilds{rt}` cohérents ; accesseurs T15 référencent exactement les types définis T4–T14 ; `resolveChannel`/`resolveGuild` définis T3 et utilisés tels quels.

**Known divergence to verify during execution:** les payloads exacts de `components.go`, `threads.go` et l'encodage emoji de `reactions.go` doivent être recoupés avec les implémentations actuelles (les anciens tests qui passent sont la source de vérité). Signalé dans les tâches concernées.
