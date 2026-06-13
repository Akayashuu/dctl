package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/vskstudio/dctl"
	"github.com/vskstudio/dctl/internal/state"
)

type fakeDiscord struct {
	created  []string
	archived []string
	homeType int
}

func (f *fakeDiscord) ChannelType(ctx context.Context, id string) (int, error) {
	return f.homeType, nil
}
func (f *fakeDiscord) CreateChannelUnder(ctx context.Context, parentID, name string) (*dctl.Channel, error) {
	f.created = append(f.created, name)
	return &dctl.Channel{ID: "new-" + name, Name: name, Type: dctl.ChannelText}, nil
}
func (f *fakeDiscord) ForumPost(ctx context.Context, forumID, name, content string) (*dctl.Channel, error) {
	f.created = append(f.created, "forum:"+name)
	return &dctl.Channel{ID: "post-" + name, Name: name}, nil
}
func (f *fakeDiscord) ArchiveChannel(ctx context.Context, id string) error {
	f.archived = append(f.archived, id)
	return nil
}

type fakeSup struct{ started, stopped []string }

func (f *fakeSup) Start(s state.Session) error { f.started = append(f.started, s.Name); return nil }
func (f *fakeSup) Stop(name string) error      { f.stopped = append(f.stopped, name); return nil }

type fakeWT struct {
	created, removed []string
	path             string // "" → simulate shared fallback
	removeErr        error  // simulate dirty worktree
}

func (f *fakeWT) Create(name string) (string, error) {
	f.created = append(f.created, name)
	return f.path, nil
}
func (f *fakeWT) Remove(name string, force bool) error {
	if f.removeErr != nil && !force {
		return f.removeErr
	}
	f.removed = append(f.removed, name)
	return nil
}

func newTestHandler(t *testing.T, homeType int) (*Handler, *fakeDiscord, *fakeSup, *fakeWT, *state.State) {
	t.Helper()
	d := &fakeDiscord{homeType: homeType}
	sup := &fakeSup{}
	wt := &fakeWT{path: "/wt/x"}
	st := state.NewState(t.TempDir() + "/s.json")
	st.AddAllow("owner")
	return NewHandler(d, sup, wt, st, "claude"), d, sup, wt, st
}

func it(user, cmd string, sub string, opts ...dctl.InteractionOption) dctl.Interaction {
	data := dctl.InteractionData{Name: cmd}
	if sub != "" {
		data.Options = []dctl.InteractionOption{{Name: sub, Type: 1, Options: opts}}
	} else {
		data.Options = opts
	}
	return dctl.Interaction{Member: dctl.Member{User: dctl.Author{ID: user}}, Data: data}
}

func TestHandlerDeniesNonAllowlisted(t *testing.T) {
	h, _, _, _, _ := newTestHandler(t, dctl.ChannelText)
	r := h.Handle(context.Background(), it("intruder", "session", "list"))
	if !r.Ephemeral || r.Content == "" {
		t.Fatalf("expected ephemeral denial, got %+v", r)
	}
	if r.Content != "⛔ Not authorized." {
		t.Fatalf("expected denial message, got %q", r.Content)
	}
}

func TestSetHomeDetectsCategory(t *testing.T) {
	h, _, _, _, st := newTestHandler(t, 4) // 4 = GUILD_CATEGORY
	h.Handle(context.Background(), it("owner", "set", "home",
		dctl.InteractionOption{Name: "channel", Value: "cat1"}))
	if st.Home.ID != "cat1" || st.Home.Type != "category" {
		t.Fatalf("home wrong: %+v", st.Home)
	}
}

func TestSetHomeDetectsForum(t *testing.T) {
	h, _, _, _, st := newTestHandler(t, dctl.ChannelForum)
	h.Handle(context.Background(), it("owner", "set", "home",
		dctl.InteractionOption{Name: "channel", Value: "f1"}))
	if st.Home.Type != "forum" {
		t.Fatalf("expected forum, got %+v", st.Home)
	}
}

func TestSessionCreateText(t *testing.T) {
	h, d, sup, wt, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if len(d.created) != 1 || d.created[0] != "demo" {
		t.Fatalf("expected channel created: %+v", d.created)
	}
	if len(wt.created) != 1 {
		t.Fatalf("expected worktree created: %+v", wt.created)
	}
	if len(sup.started) != 1 {
		t.Fatalf("expected bridge started: %+v", sup.started)
	}
	sess, ok := st.FindSession("demo")
	if !ok || sess.Worktree != "/wt/x" {
		t.Fatalf("session not persisted with worktree: %+v", sess)
	}
}

func TestSessionCreateShared(t *testing.T) {
	h, _, _, wt, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"},
		dctl.InteractionOption{Name: "shared", Value: true}))
	if len(wt.created) != 0 {
		t.Fatalf("shared session should not create a worktree: %+v", wt.created)
	}
	sess, _ := st.FindSession("demo")
	if sess.Worktree != "" {
		t.Fatalf("shared session should have empty worktree: %+v", sess)
	}
}

func TestSessionCreateRejectsUnsafeName(t *testing.T) {
	for _, name := range []string{"../escape", "a/b", "..", "with space", "bad;rm", ""} {
		h, d, _, wt, st := newTestHandler(t, dctl.ChannelText)
		st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
		r := h.Handle(context.Background(), it("owner", "session", "create",
			dctl.InteractionOption{Name: "name", Value: name}))
		if !r.Ephemeral || r.Content == "" {
			t.Fatalf("name %q: expected ephemeral rejection, got %+v", name, r)
		}
		if len(wt.created) != 0 || len(d.created) != 0 {
			t.Fatalf("name %q: nothing should be created on rejection (wt=%v ch=%v)", name, wt.created, d.created)
		}
		if _, ok := st.FindSession(name); ok {
			t.Fatalf("name %q: must not persist a session", name)
		}
	}
}

func TestSessionCreateAcceptsSafeName(t *testing.T) {
	h, d, _, _, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	r := h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "feat_login-2"}))
	if len(d.created) != 1 {
		t.Fatalf("safe name should be accepted: %+v / %q", d.created, r.Content)
	}
}

func TestSessionCreateRequiresHome(t *testing.T) {
	h, _, _, _, _ := newTestHandler(t, dctl.ChannelText)
	r := h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if !r.Ephemeral {
		t.Fatal("expected ephemeral error when home unset")
	}
}

func TestSessionCreateForum(t *testing.T) {
	h, d, sup, _, st := newTestHandler(t, dctl.ChannelForum)
	st.SetHome(state.HomeRef{ID: "forum1", Type: "forum"})
	h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "topic"}))
	if len(d.created) != 1 || d.created[0] != "forum:topic" {
		t.Fatalf("expected forum post: %+v", d.created)
	}
	if len(sup.started) != 1 {
		t.Fatal("expected bridge started")
	}
}

func TestSessionCloseStopsAndArchives(t *testing.T) {
	h, d, sup, wt, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	st.AddSession(state.Session{Name: "demo", ChannelID: "ch9", Type: "text", Worktree: "/wt/x"})
	h.Handle(context.Background(), it("owner", "session", "close",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if len(sup.stopped) != 1 || len(d.archived) != 1 || len(wt.removed) != 1 {
		t.Fatalf("expected stop+archive+wt-remove: %+v %+v %+v", sup.stopped, d.archived, wt.removed)
	}
	if _, ok := st.FindSession("demo"); ok {
		t.Fatal("session should be removed")
	}
}

func TestSessionCloseDirtyRefusedWithoutForce(t *testing.T) {
	h, d, _, wt, st := newTestHandler(t, dctl.ChannelText)
	wt.removeErr = errors.New(`worktree "demo" has uncommitted changes`)
	st.AddSession(state.Session{Name: "demo", ChannelID: "ch9", Type: "text", Worktree: "/wt/x"})
	r := h.Handle(context.Background(), it("owner", "session", "close",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if !r.Ephemeral {
		t.Fatal("expected ephemeral refusal")
	}
	if len(d.archived) != 0 {
		t.Fatal("must not archive when worktree removal refused")
	}
	if _, ok := st.FindSession("demo"); !ok {
		t.Fatal("session must survive a refused close")
	}
}

func TestSessionCloseDirtyForced(t *testing.T) {
	h, _, _, wt, st := newTestHandler(t, dctl.ChannelText)
	wt.removeErr = errors.New("dirty")
	st.AddSession(state.Session{Name: "demo", ChannelID: "ch9", Type: "text", Worktree: "/wt/x"})
	h.Handle(context.Background(), it("owner", "session", "close",
		dctl.InteractionOption{Name: "name", Value: "demo"},
		dctl.InteractionOption{Name: "force", Value: true}))
	if len(wt.removed) != 1 {
		t.Fatalf("force should remove worktree: %+v", wt.removed)
	}
	if _, ok := st.FindSession("demo"); ok {
		t.Fatal("session should be removed after forced close")
	}
}

func TestSessionCreateUsesQualifiedTitle(t *testing.T) {
	tests := []struct {
		name       string
		instanceID string
		homeType   int
		setHome    state.HomeRef
		logical    string
		wantTitle  string
	}{
		{
			name:       "category-namespaced",
			instanceID: "alice",
			homeType:   dctl.ChannelText,
			setHome:    state.HomeRef{ID: "cat1", Type: "category"},
			logical:    "foo",
			wantTitle:  "alice__foo",
		},
		{
			name:       "forum-namespaced",
			instanceID: "bob",
			homeType:   dctl.ChannelForum,
			setHome:    state.HomeRef{ID: "f1", Type: "forum"},
			logical:    "foo",
			wantTitle:  "forum:bob__foo",
		},
		{
			name:       "category-legacy",
			instanceID: "",
			homeType:   dctl.ChannelText,
			setHome:    state.HomeRef{ID: "cat1", Type: "category"},
			logical:    "foo",
			wantTitle:  "foo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d, _, _, st := newTestHandler(t, tt.homeType)
			st.InstanceID = tt.instanceID
			st.SetHome(tt.setHome)
			h.Handle(context.Background(), it("owner", "session", "create",
				dctl.InteractionOption{Name: "name", Value: tt.logical}))
			if len(d.created) != 1 || d.created[0] != tt.wantTitle {
				t.Fatalf("created titles = %+v, want [%q]", d.created, tt.wantTitle)
			}
			// State key stays the logical name.
			if _, ok := st.FindSession(tt.logical); !ok {
				t.Fatalf("session must be keyed by logical name %q", tt.logical)
			}
		})
	}
}
