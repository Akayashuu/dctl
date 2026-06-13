package dctl

import (
	"context"
	"errors"
	"testing"

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
func (f *fakeDiscord) CreateChannelUnder(ctx context.Context, parentID, name string) (*Channel, error) {
	f.created = append(f.created, name)
	return &Channel{ID: "new-" + name, Name: name, Type: ChannelText}, nil
}
func (f *fakeDiscord) ForumPost(ctx context.Context, forumID, name, content string) (*Channel, error) {
	f.created = append(f.created, "forum:"+name)
	return &Channel{ID: "post-" + name, Name: name}, nil
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

func it(user, cmd string, sub string, opts ...InteractionOption) Interaction {
	data := InteractionData{Name: cmd}
	if sub != "" {
		data.Options = []InteractionOption{{Name: sub, Type: 1, Options: opts}}
	} else {
		data.Options = opts
	}
	return Interaction{Member: Member{User: Author{ID: user}}, Data: data}
}

func TestHandlerDeniesNonAllowlisted(t *testing.T) {
	h, _, _, _, _ := newTestHandler(t, ChannelText)
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
		InteractionOption{Name: "channel", Value: "cat1"}))
	if st.Home.ID != "cat1" || st.Home.Type != "category" {
		t.Fatalf("home wrong: %+v", st.Home)
	}
}

func TestSetHomeDetectsForum(t *testing.T) {
	h, _, _, _, st := newTestHandler(t, ChannelForum)
	h.Handle(context.Background(), it("owner", "set", "home",
		InteractionOption{Name: "channel", Value: "f1"}))
	if st.Home.Type != "forum" {
		t.Fatalf("expected forum, got %+v", st.Home)
	}
}

func TestSessionCreateText(t *testing.T) {
	h, d, sup, wt, st := newTestHandler(t, ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	h.Handle(context.Background(), it("owner", "session", "create",
		InteractionOption{Name: "name", Value: "demo"}))
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
	h, _, _, wt, st := newTestHandler(t, ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	h.Handle(context.Background(), it("owner", "session", "create",
		InteractionOption{Name: "name", Value: "demo"},
		InteractionOption{Name: "shared", Value: true}))
	if len(wt.created) != 0 {
		t.Fatalf("shared session should not create a worktree: %+v", wt.created)
	}
	sess, _ := st.FindSession("demo")
	if sess.Worktree != "" {
		t.Fatalf("shared session should have empty worktree: %+v", sess)
	}
}

func TestSessionCreateRequiresHome(t *testing.T) {
	h, _, _, _, _ := newTestHandler(t, ChannelText)
	r := h.Handle(context.Background(), it("owner", "session", "create",
		InteractionOption{Name: "name", Value: "demo"}))
	if !r.Ephemeral {
		t.Fatal("expected ephemeral error when home unset")
	}
}

func TestSessionCreateForum(t *testing.T) {
	h, d, sup, _, st := newTestHandler(t, ChannelForum)
	st.SetHome(state.HomeRef{ID: "forum1", Type: "forum"})
	h.Handle(context.Background(), it("owner", "session", "create",
		InteractionOption{Name: "name", Value: "topic"}))
	if len(d.created) != 1 || d.created[0] != "forum:topic" {
		t.Fatalf("expected forum post: %+v", d.created)
	}
	if len(sup.started) != 1 {
		t.Fatal("expected bridge started")
	}
}

func TestSessionCloseStopsAndArchives(t *testing.T) {
	h, d, sup, wt, st := newTestHandler(t, ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	st.AddSession(state.Session{Name: "demo", ChannelID: "ch9", Type: "text", Worktree: "/wt/x"})
	h.Handle(context.Background(), it("owner", "session", "close",
		InteractionOption{Name: "name", Value: "demo"}))
	if len(sup.stopped) != 1 || len(d.archived) != 1 || len(wt.removed) != 1 {
		t.Fatalf("expected stop+archive+wt-remove: %+v %+v %+v", sup.stopped, d.archived, wt.removed)
	}
	if _, ok := st.FindSession("demo"); ok {
		t.Fatal("session should be removed")
	}
}

func TestSessionCloseDirtyRefusedWithoutForce(t *testing.T) {
	h, d, _, wt, st := newTestHandler(t, ChannelText)
	wt.removeErr = errors.New(`worktree "demo" has uncommitted changes`)
	st.AddSession(state.Session{Name: "demo", ChannelID: "ch9", Type: "text", Worktree: "/wt/x"})
	r := h.Handle(context.Background(), it("owner", "session", "close",
		InteractionOption{Name: "name", Value: "demo"}))
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
	h, _, _, wt, st := newTestHandler(t, ChannelText)
	wt.removeErr = errors.New("dirty")
	st.AddSession(state.Session{Name: "demo", ChannelID: "ch9", Type: "text", Worktree: "/wt/x"})
	h.Handle(context.Background(), it("owner", "session", "close",
		InteractionOption{Name: "name", Value: "demo"},
		InteractionOption{Name: "force", Value: true}))
	if len(wt.removed) != 1 {
		t.Fatalf("force should remove worktree: %+v", wt.removed)
	}
	if _, ok := st.FindSession("demo"); ok {
		t.Fatal("session should be removed after forced close")
	}
}
