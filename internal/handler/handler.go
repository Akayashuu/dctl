package handler

import (
	"context"
	"fmt"
	"regexp"

	"github.com/vskstudio/dctl"
	"github.com/vskstudio/dctl/internal/forge"
	"github.com/vskstudio/dctl/internal/state"
)

// sessionNameRe constrains a session name to a safe slug: it becomes both a
// filesystem path (<repo>/.dctl-sessions/<name>) and a git branch
// (session/<name>), so anything outside this set could traverse directories or
// forge odd refs even though the caller is allowlisted.
var sessionNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// discord is the subset of Client the Handler needs (injected so routing is testable).
type discord interface {
	ChannelType(ctx context.Context, id string) (int, error)
	CreateChannelUnder(ctx context.Context, parentID, name string) (*dctl.Channel, error)
	ForumPost(ctx context.Context, forumID, name, content string) (*dctl.Channel, error)
	ArchiveChannel(ctx context.Context, id string) error
}

// supervisor starts/stops the bridge process backing a session.
type supervisor interface {
	Start(s state.Session) error
	Stop(name string) error
}

// worktrees owns per-session git worktree lifecycle. Create returns the worktree
// path ("" + nil error means "fall back to shared", e.g. not a git repo). The
// repo root is passed per call so one Worktreer serves every project.
type worktrees interface {
	Create(repo, name string) (path string, err error)
	Remove(repo, name string, force bool) error
}

// forges lists/clones remote repos via gh/glab (see internal/forge).
type forges interface {
	Available() (github, gitlab bool)
	List(ctx context.Context) ([]forge.Repo, error)
	Clone(ctx context.Context, spec, workspace string) (projectDir string, err error)
}

// Handler routes slash-command interactions to actions.
type Handler struct {
	d          discord
	sup        supervisor
	wt         worktrees
	fg         forges
	st         *state.State
	defaultCmd string
}

// NewHandler builds a Handler. defaultCmd is the bridge command used when a
// session is created without an explicit cmd (e.g. "claude -p --continue").
func NewHandler(d discord, sup supervisor, wt worktrees, fg forges, st *state.State, defaultCmd string) *Handler {
	return &Handler{d: d, sup: sup, wt: wt, fg: fg, st: st, defaultCmd: defaultCmd}
}

func deny() dctl.Response { return dctl.Response{Content: "⛔ Not authorized.", Ephemeral: true} }
func errf(f string, a ...any) dctl.Response {
	return dctl.Response{Content: "⚠️ " + fmt.Sprintf(f, a...), Ephemeral: true}
}

// Handle processes one interaction and returns the reply.
func (h *Handler) Handle(ctx context.Context, in dctl.Interaction) dctl.Response {
	if !h.st.Allowed(in.Member.User.ID) {
		return deny()
	}
	switch in.Data.Name {
	case "set":
		return h.handleSet(ctx, in)
	case "session":
		return h.handleSession(ctx, in)
	case "allow":
		return h.handleAllow(ctx, in)
	default:
		return errf("unknown command %q", in.Data.Name)
	}
}

func (h *Handler) handleSet(ctx context.Context, in dctl.Interaction) dctl.Response {
	sub, _ := in.Data.Subcommand()
	if sub != "home" {
		return errf("unknown /set subcommand")
	}
	id, ok := in.Data.Opt("channel")
	if !ok {
		return errf("missing channel")
	}
	ct, err := h.d.ChannelType(ctx, id)
	if err != nil {
		return errf("cannot read channel: %v", err)
	}
	var typ string
	switch ct {
	case 4: // GUILD_CATEGORY
		typ = "category"
	case dctl.ChannelForum:
		typ = "forum"
	default:
		return errf("home must be a category or a forum (got type %d)", ct)
	}
	if err := h.st.SetHome(state.HomeRef{ID: id, Type: typ}); err != nil {
		return errf("save failed: %v", err)
	}
	return dctl.Response{Content: fmt.Sprintf("🏠 Home set to %s `%s`.", typ, id), Ephemeral: true}
}

func (h *Handler) handleSession(ctx context.Context, in dctl.Interaction) dctl.Response {
	sub, _ := in.Data.Subcommand()
	switch sub {
	case "create":
		return h.sessionCreate(ctx, in)
	case "close":
		return h.sessionClose(ctx, in)
	case "list":
		return h.sessionList()
	default:
		return errf("unknown /session subcommand")
	}
}

func (h *Handler) sessionCreate(ctx context.Context, in dctl.Interaction) dctl.Response {
	name, ok := in.Data.Opt("name")
	if !ok {
		return errf("missing name")
	}
	if !sessionNameRe.MatchString(name) {
		return errf("invalid name %q — use letters, digits, - or _ (max 64, no /, spaces or ..)", name)
	}
	if _, exists := h.st.FindSession(name); exists {
		return errf("session %q already exists", name)
	}
	home := h.st.Home
	if home.ID == "" {
		return errf("no home set — run /set home first")
	}
	cmd := h.defaultCmd
	if c, ok := in.Data.Opt("cmd"); ok && c != "" {
		cmd = c
	}
	// Worktree isolation by default; shared:true runs in the main checkout.
	shared := in.Data.OptBool("shared")
	repo := h.st.WorkspaceRoot() // legacy root for now; project resolution added in a later task
	var worktree, note string
	if !shared {
		path, err := h.wt.Create(repo, name)
		if err != nil {
			return errf("worktree: %v", err)
		}
		if path == "" {
			note = " (shared — not a git repo)"
		} else {
			worktree = path
		}
	}
	// Logical name stays the state/worktree key; the qualified name namespaces
	// the Discord title so daemons sharing a home stay distinguishable (Spec §3).
	title := h.st.QualifiedName(name)
	var sess state.Session
	switch home.Type {
	case "category":
		ch, err := h.d.CreateChannelUnder(ctx, home.ID, title)
		if err != nil {
			_ = h.wt.Remove(repo, name, true) // roll back the worktree we just made
			return errf("create channel: %v", err)
		}
		sess = state.Session{Name: name, ChannelID: ch.ID, Type: "text", Cmd: cmd, Worktree: worktree}
	case "forum":
		ch, err := h.d.ForumPost(ctx, home.ID, title, "Session **"+title+"** started.")
		if err != nil {
			_ = h.wt.Remove(repo, name, true)
			return errf("create forum post: %v", err)
		}
		sess = state.Session{Name: name, ChannelID: ch.ID, Type: "forum", Cmd: cmd, Worktree: worktree}
	default:
		return errf("home type %q unsupported", home.Type)
	}
	if err := h.st.AddSession(sess); err != nil {
		return errf("persist: %v", err)
	}
	if err := h.sup.Start(sess); err != nil {
		return errf("start bridge: %v", err)
	}
	return dctl.Response{Content: fmt.Sprintf("✅ Session **%s** running on <#%s>%s.", name, sess.ChannelID, note), Ephemeral: true}
}

func (h *Handler) sessionClose(ctx context.Context, in dctl.Interaction) dctl.Response {
	name, ok := in.Data.Opt("name")
	if !ok {
		return errf("missing name")
	}
	sess, exists := h.st.FindSession(name)
	if !exists {
		return errf("no session %q", name)
	}
	_ = h.sup.Stop(name)
	if sess.Worktree != "" {
		force := in.Data.OptBool("force")
		repo := h.st.WorkspaceRoot()
		if err := h.wt.Remove(repo, name, force); err != nil {
			return errf("%v — commit, or close with force:true to discard (branch session/%s is kept)", err, name)
		}
	}
	if err := h.d.ArchiveChannel(ctx, sess.ChannelID); err != nil {
		return errf("archive: %v", err)
	}
	if err := h.st.RemoveSession(name); err != nil {
		return errf("persist: %v", err)
	}
	return dctl.Response{Content: fmt.Sprintf("🗄️ Session **%s** closed.", name), Ephemeral: true}
}

func (h *Handler) sessionList() dctl.Response {
	sessions := h.st.SnapshotSessions()
	if len(sessions) == 0 {
		return dctl.Response{Content: "No active sessions.", Ephemeral: true}
	}
	out := "Active sessions:\n"
	for _, s := range sessions {
		out += fmt.Sprintf("• **%s** (%s) <#%s>\n", s.Name, s.Type, s.ChannelID)
	}
	return dctl.Response{Content: out, Ephemeral: true}
}

func (h *Handler) handleAllow(ctx context.Context, in dctl.Interaction) dctl.Response {
	sub, _ := in.Data.Subcommand()
	switch sub {
	case "add":
		id, ok := in.Data.Opt("user")
		if !ok {
			return errf("missing user")
		}
		if err := h.st.AddAllow(id); err != nil {
			return errf("save: %v", err)
		}
		return dctl.Response{Content: "✅ Added to allowlist.", Ephemeral: true}
	case "remove":
		id, ok := in.Data.Opt("user")
		if !ok {
			return errf("missing user")
		}
		if err := h.st.RemoveAllow(id); err != nil {
			return errf("save: %v", err)
		}
		return dctl.Response{Content: "✅ Removed from allowlist.", Ephemeral: true}
	case "list":
		return dctl.Response{Content: fmt.Sprintf("Allowlist: %v", h.st.Allow), Ephemeral: true}
	default:
		return errf("unknown /allow subcommand")
	}
}
