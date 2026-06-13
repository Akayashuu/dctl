package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vskstudio/dctl"
	"github.com/vskstudio/dctl/internal/forge"
	"github.com/vskstudio/dctl/internal/state"
)

// sessionNameRe constrains a session name to a safe slug: it becomes both a
// filesystem path (<repo>/.dctl-sessions/<name>) and a git branch
// (session/<name>), so anything outside this set could traverse directories or
// forge odd refs even though the caller is allowlisted.
var sessionNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// projectRe constrains a workspace project name to a single safe path segment
// (no "/", no "..", no spaces), so workspace+project cannot escape the root.
var projectRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// Forge/git operations run inline on the gateway dispatch loop, so bound them:
// an unreachable host or a hung CLI must not wedge the daemon. Clone gets a
// generous ceiling (large repos), listing a tight one.
const (
	cloneTimeout = 10 * time.Minute
	listTimeout  = 30 * time.Second
)

// repoFor resolves the git repo root a session operates on: the workspace root
// when no project is set (legacy single-repo), else <workspace>/<project>.
func repoFor(workspace, project string) string {
	if project == "" {
		return workspace
	}
	return filepath.Join(workspace, project)
}

// discord is the subset of Client the Handler needs (injected so routing is testable).
type discord interface {
	ChannelType(ctx context.Context, id string) (int, error)
	CreateChannelUnder(ctx context.Context, parentID, name string) (*dctl.Channel, error)
	ForumPost(ctx context.Context, forumID, name, content string) (*dctl.Channel, error)
	ArchiveChannel(ctx context.Context, id string) error
	Send(ctx context.Context, channelID, content string) (*dctl.Message, error)
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
	Branch(name string) string
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
	partDir    string // dir holding participants/<name>.log journals
}

// NewHandler builds a Handler. defaultCmd is the bridge command used when a
// session is created without an explicit cmd (e.g. "claude -p --continue").
// partDir is the directory under which per-session participant journals live
// (participants/<name>.log).
func NewHandler(d discord, sup supervisor, wt worktrees, fg forges, st *state.State, defaultCmd, partDir string) *Handler {
	return &Handler{d: d, sup: sup, wt: wt, fg: fg, st: st, defaultCmd: defaultCmd, partDir: partDir}
}

// PartDir returns the participants journal directory (used by tests/wiring).
func (h *Handler) PartDir() string { return h.partDir }

func deny() dctl.Response { return dctl.Response{Content: "⛔ Not authorized.", Ephemeral: true} }
func errf(f string, a ...any) dctl.Response {
	return dctl.Response{Content: "⚠️ " + fmt.Sprintf(f, a...), Ephemeral: true}
}

// sessionBanner renders the shared context body posted on /session create.
// worktree=="" means no isolated worktree was made; shared distinguishes an
// explicit shared:true run (main checkout) from a non-git fallback. branch is
// the real (possibly instanceID-namespaced) branch produced by the worktreer.
func sessionBanner(repo, name, worktree, branch, cmd string, shared bool) string {
	b := fmt.Sprintf("🚀 Session **%s** ready.\n", name)
	if repo == "" {
		b += "• Project: **(cwd)**\n"
	} else {
		b += fmt.Sprintf("• Project: **%s** (`%s`)\n", filepath.Base(repo), repo)
	}
	switch {
	case worktree != "":
		b += "• Mode: isolated worktree\n"
		b += fmt.Sprintf("• Worktree: `%s`\n", worktree)
		b += fmt.Sprintf("• Branch: `%s`\n", branch)
	case shared:
		b += "• Mode: shared (main checkout)\n"
		b += "• Branch: — (runs on current branch)\n"
	default:
		b += "• Mode: shared (not a git repo)\n"
	}
	b += fmt.Sprintf("• Command: `%s`", cmd)
	return b
}

// Slow reports whether an interaction does network/exec work that can exceed
// Discord's 3s callback deadline, so the caller should defer (ack now, edit the
// reply in when ready): session create/close (channel + git ops, optional clone)
// and workspace remotes (gh/glab over the network).
func (h *Handler) Slow(in dctl.Interaction) bool {
	switch in.Data.Name {
	case "session":
		sub, _ := in.Data.Subcommand()
		return sub == "create" || sub == "close"
	case "workspace":
		sub, _ := in.Data.Subcommand()
		return sub == "remotes"
	}
	return false
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
	case "workspace":
		return h.handleWorkspace(ctx, in)
	case "allow":
		return h.handleAllow(ctx, in)
	default:
		return errf("unknown command %q", in.Data.Name)
	}
}

func (h *Handler) handleSet(ctx context.Context, in dctl.Interaction) dctl.Response {
	sub, _ := in.Data.Subcommand()
	switch sub {
	case "home":
		return h.setHome(ctx, in)
	case "workspace":
		return h.setWorkspace(in)
	default:
		return errf("unknown /set subcommand")
	}
}

func (h *Handler) setHome(ctx context.Context, in dctl.Interaction) dctl.Response {
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

func (h *Handler) setWorkspace(in dctl.Interaction) dctl.Response {
	p, ok := in.Data.Opt("path")
	if !ok || p == "" {
		return errf("missing path")
	}
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return errf("bad path: %v", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return errf("not a directory: %s", abs)
	}
	if err := h.st.SetWorkspace(abs); err != nil {
		return errf("save failed: %v", err)
	}
	return dctl.Response{Content: fmt.Sprintf("📂 Workspace set to `%s`.", abs), Ephemeral: true}
}

func (h *Handler) handleSession(ctx context.Context, in dctl.Interaction) dctl.Response {
	// The "allow" sub-command group is type 2, which Subcommand() (type-1 only)
	// does not surface; detect it explicitly.
	if allowAction(in.Data.Options) != "" {
		return h.sessionAllow(in)
	}
	sub, _ := in.Data.Subcommand()
	switch sub {
	case "create":
		return h.sessionCreate(ctx, in)
	case "close":
		return h.sessionClose(ctx, in)
	case "list":
		return h.sessionList()
	case "who":
		return h.sessionWho(in)
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
	ws := h.st.WorkspaceRoot()
	project := ""
	if ws != "" {
		if spec, ok := in.Data.Opt("clone"); ok && spec != "" {
			cctx, cancel := context.WithTimeout(ctx, cloneTimeout)
			dir, err := h.fg.Clone(cctx, spec, ws)
			cancel()
			if err != nil {
				return errf("clone: %v", err)
			}
			project = filepath.Base(dir)
		} else {
			project, _ = in.Data.Opt("project")
		}
		if project == "" {
			return errf("specify project: (see `/workspace list`) or clone:")
		}
		if !projectRe.MatchString(project) {
			return errf("invalid project %q — use a single name (no /, spaces, or ..)", project)
		}
	}
	repo := repoFor(ws, project)
	// Worktree isolation by default; shared:true runs in the main checkout.
	shared := in.Data.OptBool("shared")
	var worktree string
	if !shared {
		path, err := h.wt.Create(repo, name)
		if err != nil {
			return errf("worktree: %v", err)
		}
		worktree = path // "" means non-git fallback
	}
	// Logical name stays the state/worktree key; the qualified name namespaces
	// the Discord title so daemons sharing a home stay distinguishable (Spec §3).
	title := h.st.QualifiedName(name)
	var sess state.Session
	switch home.Type {
	case "category":
		ch, err := h.d.CreateChannelUnder(ctx, home.ID, title)
		if err != nil {
			if rmErr := h.wt.Remove(repo, name, true); rmErr != nil { // roll back the worktree we just made
				fmt.Fprintf(os.Stderr, "dctl: worktree rollback for %q failed: %v\n", name, rmErr)
			}
			return errf("create channel: %v", err)
		}
		sess = state.Session{Name: name, ChannelID: ch.ID, Type: "text", Cmd: cmd, Worktree: worktree, Project: project}
	case "forum":
		ch, err := h.d.ForumPost(ctx, home.ID, title, "Session **"+title+"** started.")
		if err != nil {
			if rmErr := h.wt.Remove(repo, name, true); rmErr != nil {
				fmt.Fprintf(os.Stderr, "dctl: worktree rollback for %q failed: %v\n", name, rmErr)
			}
			return errf("create forum post: %v", err)
		}
		sess = state.Session{Name: name, ChannelID: ch.ID, Type: "forum", Cmd: cmd, Worktree: worktree, Project: project}
	default:
		return errf("home type %q unsupported", home.Type)
	}
	if err := h.st.AddSession(sess); err != nil {
		return errf("persist: %v", err)
	}
	if err := h.sup.Start(sess); err != nil {
		return errf("start bridge: %v", err)
	}
	banner := sessionBanner(repo, name, worktree, h.wt.Branch(name), cmd, shared)
	_, _ = h.d.Send(ctx, sess.ChannelID, banner) // best-effort; reply is source of truth
	reply := fmt.Sprintf("✅ Running on <#%s>.\n\n%s", sess.ChannelID, banner)
	return dctl.Response{Content: reply, Ephemeral: true}
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
		repo := repoFor(h.st.WorkspaceRoot(), sess.Project)
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
	_ = state.RemoveParticipantJournal(state.ParticipantsPath(h.partDir, name))
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

// sessionAllow routes /session allow add|remove|list. The option group is the
// SUB_COMMAND_GROUP "allow"; its single child SUB_COMMAND is the action.
func (h *Handler) sessionAllow(in dctl.Interaction) dctl.Response {
	action := allowAction(in.Data.Options)
	name, ok := in.Data.Opt("name")
	if !ok {
		return errf("missing name")
	}
	if _, exists := h.st.FindSession(name); !exists {
		return errf("no session %q", name)
	}
	switch action {
	case "add":
		id, ok := in.Data.Opt("user")
		if !ok {
			return errf("missing user")
		}
		id = normalizeUserID(id)
		added, err := h.st.AddSessionAllow(name, id)
		if err != nil {
			return errf("%v", err)
		}
		if !added {
			return dctl.Response{Content: fmt.Sprintf("<@%s> already allowed on **%s**.", id, name), Ephemeral: true}
		}
		return dctl.Response{Content: fmt.Sprintf("✅ <@%s> allowed on **%s**.", id, name), Ephemeral: true}
	case "remove":
		id, ok := in.Data.Opt("user")
		if !ok {
			return errf("missing user")
		}
		id = normalizeUserID(id)
		removed, err := h.st.RemoveSessionAllow(name, id)
		if err != nil {
			return errf("%v", err)
		}
		if !removed {
			return dctl.Response{Content: fmt.Sprintf("<@%s> was not in **%s**'s allowlist.", id, name), Ephemeral: true}
		}
		return dctl.Response{Content: fmt.Sprintf("✅ <@%s> removed from **%s**.", id, name), Ephemeral: true}
	case "list":
		ids := h.st.SessionAllowlist(name)
		if len(ids) == 0 {
			return dctl.Response{Content: fmt.Sprintf("**%s** has no per-session allowlist (the global allowlist still applies).", name), Ephemeral: true}
		}
		out := fmt.Sprintf("Per-session allowlist for **%s** (plus the global allowlist):\n", name)
		for _, id := range ids {
			out += fmt.Sprintf("• <@%s>\n", id)
		}
		return dctl.Response{Content: out, Ephemeral: true}
	default:
		return errf("unknown /session allow action")
	}
}

// sessionWho lists observed participants (journal) for the session.
func (h *Handler) sessionWho(in dctl.Interaction) dctl.Response {
	name, ok := in.Data.Opt("name")
	if !ok {
		return errf("missing name")
	}
	if _, exists := h.st.FindSession(name); !exists {
		return errf("no session %q", name)
	}
	ids := state.ReadParticipants(state.ParticipantsPath(h.partDir, name))
	if len(ids) == 0 {
		return dctl.Response{Content: "Personne n'a encore écrit dans cette session.", Ephemeral: true}
	}
	out := fmt.Sprintf("Participants observed in **%s**:\n", name)
	for _, id := range ids {
		out += fmt.Sprintf("• <@%s>\n", id)
	}
	return dctl.Response{Content: out, Ephemeral: true}
}

// allowAction returns the SUB_COMMAND name nested in the "allow" group.
func allowAction(opts []dctl.InteractionOption) string {
	for _, o := range opts {
		if o.Name == "allow" && o.Type == 2 { // SUB_COMMAND_GROUP
			for _, c := range o.Options {
				if c.Type == 1 { // SUB_COMMAND
					return c.Name
				}
			}
		}
	}
	return ""
}

// normalizeUserID strips a Discord mention wrapper (<@id> / <@!id>) to the bare id.
func normalizeUserID(s string) string {
	s = strings.TrimPrefix(s, "<@")
	s = strings.TrimPrefix(s, "!")
	s = strings.TrimSuffix(s, ">")
	return s
}

func (h *Handler) handleWorkspace(ctx context.Context, in dctl.Interaction) dctl.Response {
	sub, _ := in.Data.Subcommand()
	switch sub {
	case "list":
		return h.workspaceList()
	case "remotes":
		return h.workspaceRemotes(ctx)
	default:
		return errf("unknown /workspace subcommand")
	}
}

func (h *Handler) workspaceList() dctl.Response {
	ws := h.st.WorkspaceRoot()
	if ws == "" {
		return errf("no workspace set — run /set workspace first")
	}
	entries, err := os.ReadDir(ws)
	if err != nil {
		return errf("read workspace: %v", err)
	}
	out := "Projects:\n"
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(ws, e.Name(), ".git")); err != nil {
			continue
		}
		out += "• " + e.Name() + "\n"
		n++
	}
	if n == 0 {
		out = "No git projects in workspace."
	}
	return dctl.Response{Content: out, Ephemeral: true}
}

func (h *Handler) workspaceRemotes(ctx context.Context) dctl.Response {
	gh, gl := h.fg.Available()
	if !gh && !gl {
		return errf("no gh/glab found — install one and authenticate")
	}
	lctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	repos, err := h.fg.List(lctx)
	if err != nil {
		return errf("list remotes: %v", err)
	}
	if len(repos) == 0 {
		return dctl.Response{Content: "No remote repos found.", Ephemeral: true}
	}
	out := "Remotes:\n"
	for _, r := range repos {
		out += fmt.Sprintf("• [%s] %s\n", r.Forge, r.FullName)
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
