package handler

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vskstudio/dctl"
	"github.com/vskstudio/dctl/internal/forge"
	"github.com/vskstudio/dctl/internal/state"
)

type sentMsg struct{ channelID, content string }

type fakeDiscord struct {
	created  []string
	archived []string
	homeType int
	sent     []sentMsg
	sendErr  error
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
func (f *fakeDiscord) Send(ctx context.Context, channelID, content string) (*dctl.Message, error) {
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.sent = append(f.sent, sentMsg{channelID: channelID, content: content})
	return &dctl.Message{ID: "msg-" + channelID, ChannelID: channelID, Content: content}, nil
}

func TestDiscordInterfaceHasSend(t *testing.T) {
	var _ discord = (*fakeDiscord)(nil)
	var _ discord = (*dctl.Client)(nil)
}

type fakeSup struct{ started, stopped []string }

func (f *fakeSup) Start(s state.Session) error { f.started = append(f.started, s.Name); return nil }
func (f *fakeSup) Stop(name string) error      { f.stopped = append(f.stopped, name); return nil }

type fakeWT struct {
	createdRepos []string // repo arg captured per Create
	created      []string
	removed      []string
	path         string // "" → simulate shared fallback
	removeErr    error  // simulate dirty worktree
}

func (f *fakeWT) Create(repo, name string) (string, error) {
	f.createdRepos = append(f.createdRepos, repo)
	f.created = append(f.created, name)
	return f.path, nil
}
func (f *fakeWT) Branch(name string) string { return "session/" + name }
func (f *fakeWT) Remove(repo, name string, force bool) error {
	if f.removeErr != nil && !force {
		return f.removeErr
	}
	f.removed = append(f.removed, name)
	return nil
}

type fakeForge struct {
	repos    []forge.Repo
	cloneDir string
	cloneErr error
	cloned   []string // specs passed to Clone
	gh, gl   bool
}

func (f *fakeForge) Available() (bool, bool) { return f.gh, f.gl }
func (f *fakeForge) List(ctx context.Context) ([]forge.Repo, error) {
	return f.repos, nil
}
func (f *fakeForge) Clone(ctx context.Context, spec, workspace string) (string, error) {
	f.cloned = append(f.cloned, spec)
	if f.cloneErr != nil {
		return "", f.cloneErr
	}
	return f.cloneDir, nil
}

func newTestHandler(t *testing.T, homeType int) (*Handler, *fakeDiscord, *fakeSup, *fakeWT, *fakeForge, *state.State) {
	t.Helper()
	d := &fakeDiscord{homeType: homeType}
	sup := &fakeSup{}
	wt := &fakeWT{path: "/wt/x"}
	fg := &fakeForge{gh: true}
	st := state.NewState(t.TempDir() + "/s.json")
	st.AddAllow("owner")
	return NewHandler(d, sup, wt, fg, st, "claude"), d, sup, wt, fg, st
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
	h, _, _, _, _, _ := newTestHandler(t, dctl.ChannelText)
	r := h.Handle(context.Background(), it("intruder", "session", "list"))
	if !r.Ephemeral || r.Content == "" {
		t.Fatalf("expected ephemeral denial, got %+v", r)
	}
	if r.Content != "⛔ Not authorized." {
		t.Fatalf("expected denial message, got %q", r.Content)
	}
}

func TestSetHomeDetectsCategory(t *testing.T) {
	h, _, _, _, _, st := newTestHandler(t, 4) // 4 = GUILD_CATEGORY
	h.Handle(context.Background(), it("owner", "set", "home",
		dctl.InteractionOption{Name: "channel", Value: "cat1"}))
	if st.Home.ID != "cat1" || st.Home.Type != "category" {
		t.Fatalf("home wrong: %+v", st.Home)
	}
}

func TestSetHomeDetectsForum(t *testing.T) {
	h, _, _, _, _, st := newTestHandler(t, dctl.ChannelForum)
	h.Handle(context.Background(), it("owner", "set", "home",
		dctl.InteractionOption{Name: "channel", Value: "f1"}))
	if st.Home.Type != "forum" {
		t.Fatalf("expected forum, got %+v", st.Home)
	}
}

func TestSessionCreateText(t *testing.T) {
	h, d, sup, wt, _, st := newTestHandler(t, dctl.ChannelText)
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
	h, _, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
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
		h, d, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
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
	h, d, _, _, _, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	r := h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "feat_login-2"}))
	if len(d.created) != 1 {
		t.Fatalf("safe name should be accepted: %+v / %q", d.created, r.Content)
	}
}

func TestSessionCreateRequiresHome(t *testing.T) {
	h, _, _, _, _, _ := newTestHandler(t, dctl.ChannelText)
	r := h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if !r.Ephemeral {
		t.Fatal("expected ephemeral error when home unset")
	}
}

func TestSessionCreateForum(t *testing.T) {
	h, d, sup, _, _, st := newTestHandler(t, dctl.ChannelForum)
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

func TestSessionBanner(t *testing.T) {
	const repo = "/home/me/proj"
	cases := []struct {
		name     string
		worktree string
		branch   string
		shared   bool
		want     []string
		absent   []string
	}{
		{
			name:     "isolated",
			worktree: "/home/me/proj/.dctl-sessions/demo",
			branch:   "session/demo",
			shared:   false,
			want: []string{
				"🚀 Session **demo** ready.",
				"Project: **proj** (`/home/me/proj`)",
				"Mode: isolated worktree",
				"Worktree: `/home/me/proj/.dctl-sessions/demo`",
				"Branch: `session/demo`",
				"Command: `claude`",
			},
			absent: []string{"main checkout", "not a git repo"},
		},
		{
			name:     "shared main checkout",
			worktree: "",
			branch:   "session/demo",
			shared:   true,
			want: []string{
				"🚀 Session **demo** ready.",
				"Project: **proj** (`/home/me/proj`)",
				"Mode: shared (main checkout)",
				"Branch: — (runs on current branch)",
				"Command: `claude`",
			},
			absent: []string{"Worktree:", "isolated worktree", "not a git repo"},
		},
		{
			name:     "non-git shared",
			worktree: "",
			branch:   "session/demo",
			shared:   false,
			want: []string{
				"🚀 Session **demo** ready.",
				"Project: **proj** (`/home/me/proj`)",
				"Mode: shared (not a git repo)",
				"Command: `claude`",
			},
			absent: []string{"Worktree:", "Branch:", "isolated worktree", "main checkout"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionBanner(repo, "demo", tc.worktree, tc.branch, "claude", tc.shared)
			for _, s := range tc.want {
				if !strings.Contains(got, s) {
					t.Errorf("banner missing %q\n--- got ---\n%s", s, got)
				}
			}
			for _, s := range tc.absent {
				if strings.Contains(got, s) {
					t.Errorf("banner unexpectedly contains %q\n--- got ---\n%s", s, got)
				}
			}
		})
	}
}

func TestSessionBannerEmptyRepo(t *testing.T) {
	got := sessionBanner("", "demo", "", "session/demo", "claude", true)
	if !strings.Contains(got, "Project: **(cwd)**") {
		t.Errorf("empty repo should render (cwd), got:\n%s", got)
	}
	if strings.Contains(got, "**.**") || strings.Contains(got, "(``)") {
		t.Errorf("empty repo must not render misleading path, got:\n%s", got)
	}
}

func TestSessionCreateBanner(t *testing.T) {
	cases := []struct {
		name      string
		homeType  int
		homeRef   state.HomeRef
		wtPath    string
		shared    bool
		wantReply []string
		wantSend  []string
	}{
		{
			name:     "category isolated",
			homeType: dctl.ChannelText,
			homeRef:  state.HomeRef{ID: "cat1", Type: "category"},
			wtPath:   "/wt/x",
			shared:   false,
			wantReply: []string{
				"✅ Running on <#new-demo>.",
				"Mode: isolated worktree",
				"Worktree: `/wt/x`",
				"Branch: `session/demo`",
				"Command: `claude`",
			},
			wantSend: []string{"Mode: isolated worktree", "Worktree: `/wt/x`"},
		},
		{
			name:     "category non-git shared",
			homeType: dctl.ChannelText,
			homeRef:  state.HomeRef{ID: "cat1", Type: "category"},
			wtPath:   "",
			shared:   false,
			wantReply: []string{
				"✅ Running on <#new-demo>.",
				"Mode: shared (not a git repo)",
			},
			wantSend: []string{"Mode: shared (not a git repo)"},
		},
		{
			name:     "category shared:true",
			homeType: dctl.ChannelText,
			homeRef:  state.HomeRef{ID: "cat1", Type: "category"},
			wtPath:   "/wt/x",
			shared:   true,
			wantReply: []string{
				"Mode: shared (main checkout)",
				"Branch: — (runs on current branch)",
			},
			wantSend: []string{"Mode: shared (main checkout)"},
		},
		{
			name:     "forum isolated",
			homeType: dctl.ChannelForum,
			homeRef:  state.HomeRef{ID: "forum1", Type: "forum"},
			wtPath:   "/wt/x",
			shared:   false,
			wantReply: []string{
				"✅ Running on <#post-demo>.",
				"Mode: isolated worktree",
			},
			wantSend: []string{"Mode: isolated worktree"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, d, _, wt, _, st := newTestHandler(t, tc.homeType)
			wt.path = tc.wtPath
			st.SetHome(tc.homeRef)
			opts := []dctl.InteractionOption{{Name: "name", Value: "demo"}}
			if tc.shared {
				opts = append(opts, dctl.InteractionOption{Name: "shared", Value: true})
			}
			r := h.Handle(context.Background(), it("owner", "session", "create", opts...))
			if !r.Ephemeral {
				t.Fatalf("reply must be ephemeral: %+v", r)
			}
			for _, s := range tc.wantReply {
				if !strings.Contains(r.Content, s) {
					t.Errorf("reply missing %q\n--- got ---\n%s", s, r.Content)
				}
			}
			if len(d.sent) != 1 {
				t.Fatalf("expected exactly one in-channel Send, got %d: %+v", len(d.sent), d.sent)
			}
			sess, _ := st.FindSession("demo")
			if d.sent[0].channelID != sess.ChannelID {
				t.Errorf("Send went to %q, want session channel %q", d.sent[0].channelID, sess.ChannelID)
			}
			for _, s := range tc.wantSend {
				if !strings.Contains(d.sent[0].content, s) {
					t.Errorf("in-channel banner missing %q\n--- got ---\n%s", s, d.sent[0].content)
				}
			}
			if strings.Contains(d.sent[0].content, "Running on") {
				t.Errorf("in-channel banner must not carry the 'Running on' prefix:\n%s", d.sent[0].content)
			}
		})
	}
}

func TestSessionCreateSendFailureDoesNotFail(t *testing.T) {
	h, d, sup, _, _, st := newTestHandler(t, dctl.ChannelText)
	d.sendErr = errors.New("discord 500")
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	r := h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if !strings.Contains(r.Content, "✅ Running on") {
		t.Fatalf("create must still succeed when Send fails, got: %q", r.Content)
	}
	if _, ok := st.FindSession("demo"); !ok {
		t.Fatal("session must remain persisted despite Send failure")
	}
	if len(sup.started) != 1 {
		t.Fatal("bridge must have started")
	}
	if len(d.sent) != 0 {
		t.Fatalf("no send should be recorded on error, got %+v", d.sent)
	}
}

func TestSessionCloseStopsAndArchives(t *testing.T) {
	h, d, sup, wt, _, st := newTestHandler(t, dctl.ChannelText)
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
	h, d, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
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
	h, _, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
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
			h, d, _, _, _, st := newTestHandler(t, tt.homeType)
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

func TestSessionCloseOnlyTouchesOwnSession(t *testing.T) {
	// Two instances each own a logically-identical "foo" with distinct channel
	// ids. Closing on instance "bob" must archive only bob's channel.
	h, d, sup, wt, _, st := newTestHandler(t, dctl.ChannelText)
	st.InstanceID = "bob"
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	// bob's own session, keyed by logical name, pointing at bob's channel.
	st.AddSession(state.Session{Name: "foo", ChannelID: "bob-foo-ch", Type: "text", Worktree: "/wt/bob"})

	h.Handle(context.Background(), it("owner", "session", "close",
		dctl.InteractionOption{Name: "name", Value: "foo"}))

	if len(d.archived) != 1 || d.archived[0] != "bob-foo-ch" {
		t.Fatalf("close must archive only bob's channel, got %+v", d.archived)
	}
	if len(sup.stopped) != 1 || sup.stopped[0] != "foo" {
		t.Fatalf("close must stop only the local logical session, got %+v", sup.stopped)
	}
	if len(wt.removed) != 1 {
		t.Fatalf("close must remove exactly bob's worktree, got %+v", wt.removed)
	}
	if _, ok := st.FindSession("foo"); ok {
		t.Fatal("bob's foo should be gone after close")
	}
}

func TestSessionCloseUnknownNameIsNoop(t *testing.T) {
	// Closing a name absent from this instance's state touches nothing — it
	// cannot reach another instance's resources.
	h, d, sup, _, _, st := newTestHandler(t, dctl.ChannelText)
	st.InstanceID = "bob"
	r := h.Handle(context.Background(), it("owner", "session", "close",
		dctl.InteractionOption{Name: "name", Value: "alice-only"}))
	if !r.Ephemeral {
		t.Fatal("expected ephemeral error for unknown session")
	}
	if len(d.archived) != 0 || len(sup.stopped) != 0 {
		t.Fatalf("unknown close must be a no-op, got archived=%+v stopped=%+v", d.archived, sup.stopped)
	}
}

func TestSetWorkspacePersists(t *testing.T) {
	h, _, _, _, _, st := newTestHandler(t, dctl.ChannelText)
	dir := t.TempDir()
	r := h.Handle(context.Background(), it("owner", "set", "workspace",
		dctl.InteractionOption{Name: "path", Value: dir}))
	if r.Content == "" || !r.Ephemeral {
		t.Fatalf("expected ephemeral confirmation, got %+v", r)
	}
	if st.Workspace != dir {
		t.Fatalf("workspace not set: %q", st.Workspace)
	}
}

func TestSetWorkspaceRejectsMissingDir(t *testing.T) {
	h, _, _, _, _, st := newTestHandler(t, dctl.ChannelText)
	h.Handle(context.Background(), it("owner", "set", "workspace",
		dctl.InteractionOption{Name: "path", Value: "/no/such/dir/here"}))
	if st.Workspace != "" {
		t.Fatalf("missing dir should not be saved, got %q", st.Workspace)
	}
}

func TestSessionCreateUsesWorkspaceProject(t *testing.T) {
	h, _, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	_ = st.SetWorkspace("/ws")
	h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"},
		dctl.InteractionOption{Name: "project", Value: "myproj"}))
	if len(wt.createdRepos) != 1 || wt.createdRepos[0] != "/ws/myproj" {
		t.Fatalf("expected Create on /ws/myproj, got %+v", wt.createdRepos)
	}
	sess, _ := st.FindSession("demo")
	if sess.Project != "myproj" {
		t.Fatalf("session.Project not persisted: %+v", sess)
	}
}

func TestSessionCreateRequiresProjectWhenWorkspaceSet(t *testing.T) {
	h, d, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	_ = st.SetWorkspace("/ws")
	r := h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if !r.Ephemeral || r.Content == "" {
		t.Fatalf("expected error asking for project, got %+v", r)
	}
	if len(wt.created) != 0 || len(d.created) != 0 {
		t.Fatalf("nothing should be created: wt=%v ch=%v", wt.created, d.created)
	}
}

func TestSessionCreateRejectsProjectTraversal(t *testing.T) {
	for _, p := range []string{"../escape", "a/b", "..", "with space"} {
		h, d, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
		st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
		_ = st.SetWorkspace("/ws")
		r := h.Handle(context.Background(), it("owner", "session", "create",
			dctl.InteractionOption{Name: "name", Value: "demo"},
			dctl.InteractionOption{Name: "project", Value: p}))
		if !r.Ephemeral || r.Content == "" {
			t.Fatalf("project %q: expected rejection, got %+v", p, r)
		}
		if len(wt.created) != 0 || len(d.created) != 0 {
			t.Fatalf("project %q: nothing should be created", p)
		}
	}
}

func TestSessionCreateLegacyNoWorkspace(t *testing.T) {
	// No workspace set → legacy behaviour: repo is "" (WorkspaceRoot), still works.
	h, d, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if len(d.created) != 1 || len(wt.created) != 1 {
		t.Fatalf("legacy create should still work: ch=%v wt=%v", d.created, wt.created)
	}
}

func TestSessionCloseUsesProjectRepo(t *testing.T) {
	h, _, _, wt, _, st := newTestHandler(t, dctl.ChannelText)
	_ = st.SetWorkspace("/ws")
	st.AddSession(state.Session{Name: "demo", ChannelID: "ch9", Type: "text", Worktree: "/ws/myproj/.dctl-sessions/demo", Project: "myproj"})
	h.Handle(context.Background(), it("owner", "session", "close",
		dctl.InteractionOption{Name: "name", Value: "demo"}))
	if len(wt.removed) != 1 {
		t.Fatalf("expected worktree removed: %+v", wt.removed)
	}
}

func TestSessionCreateClonesThenUsesProject(t *testing.T) {
	h, _, _, wt, fg, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	_ = st.SetWorkspace("/ws")
	fg.cloneDir = "/ws/app"
	h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"},
		dctl.InteractionOption{Name: "clone", Value: "me/app"}))
	if len(fg.cloned) != 1 || fg.cloned[0] != "me/app" {
		t.Fatalf("expected clone of me/app, got %+v", fg.cloned)
	}
	if len(wt.createdRepos) != 1 || wt.createdRepos[0] != "/ws/app" {
		t.Fatalf("expected Create on /ws/app, got %+v", wt.createdRepos)
	}
	sess, _ := st.FindSession("demo")
	if sess.Project != "app" {
		t.Fatalf("project should be derived from clone: %+v", sess)
	}
}

func TestSessionCreateCloneErrorSurfaces(t *testing.T) {
	h, d, _, wt, fg, st := newTestHandler(t, dctl.ChannelText)
	st.SetHome(state.HomeRef{ID: "cat1", Type: "category"})
	_ = st.SetWorkspace("/ws")
	fg.cloneErr = errors.New("auth required")
	r := h.Handle(context.Background(), it("owner", "session", "create",
		dctl.InteractionOption{Name: "name", Value: "demo"},
		dctl.InteractionOption{Name: "clone", Value: "me/app"}))
	if !r.Ephemeral || r.Content == "" {
		t.Fatalf("expected ephemeral clone error, got %+v", r)
	}
	if len(wt.created) != 0 || len(d.created) != 0 {
		t.Fatalf("nothing should be created after clone failure")
	}
}

func TestWorkspaceListShowsGitProjects(t *testing.T) {
	h, _, _, _, _, st := newTestHandler(t, dctl.ChannelText)
	ws := t.TempDir()
	// proj1 is a git repo; plain is a normal dir; file is not a dir.
	if err := os.MkdirAll(ws+"/proj1/.git", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws+"/plain", 0o755); err != nil {
		t.Fatal(err)
	}
	_ = st.SetWorkspace(ws)
	r := h.Handle(context.Background(), it("owner", "workspace", "list"))
	if !strings.Contains(r.Content, "proj1") {
		t.Fatalf("expected proj1 listed, got %q", r.Content)
	}
	if strings.Contains(r.Content, "plain") {
		t.Fatalf("non-git dir should not be listed, got %q", r.Content)
	}
}

func TestWorkspaceListErrorsWithoutWorkspace(t *testing.T) {
	h, _, _, _, _, _ := newTestHandler(t, dctl.ChannelText)
	r := h.Handle(context.Background(), it("owner", "workspace", "list"))
	if !r.Ephemeral || r.Content == "" {
		t.Fatalf("expected error when no workspace set, got %+v", r)
	}
}

func TestWorkspaceRemotesLists(t *testing.T) {
	h, _, _, _, fg, _ := newTestHandler(t, dctl.ChannelText)
	fg.gh = true
	fg.repos = []forge.Repo{{FullName: "me/app", Forge: "github"}}
	r := h.Handle(context.Background(), it("owner", "workspace", "remotes"))
	if !strings.Contains(r.Content, "me/app") || !strings.Contains(r.Content, "github") {
		t.Fatalf("expected labeled remote, got %q", r.Content)
	}
}

func TestWorkspaceRemotesNoForge(t *testing.T) {
	h, _, _, _, fg, _ := newTestHandler(t, dctl.ChannelText)
	fg.gh, fg.gl = false, false
	r := h.Handle(context.Background(), it("owner", "workspace", "remotes"))
	if !strings.Contains(r.Content, "gh/glab") {
		t.Fatalf("expected no-forge message, got %q", r.Content)
	}
}
