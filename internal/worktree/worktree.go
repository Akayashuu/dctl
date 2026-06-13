package worktree

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktreer manages git worktrees rooted at repo. Implements dctl.worktrees.
// With a non-empty instanceID, worktrees live under
// <repo>/.dctl-sessions/<instanceID>/<name> on branch session/<instanceID>/<name>;
// with an empty instanceID (legacy) under <repo>/.dctl-sessions/<name> on
// branch session/<name>, so multiple daemons sharing a repo never collide.
type Worktreer struct {
	ctx        context.Context
	repo       string
	instanceID string
}

// NewWorktreer builds a Worktreer for the project at repo, namespaced by
// instanceID ("" means legacy, non-namespaced layout).
func NewWorktreer(ctx context.Context, repo, instanceID string) *Worktreer {
	return &Worktreer{ctx: ctx, repo: repo, instanceID: instanceID}
}

func (w *Worktreer) isGitRepo() bool {
	return exec.CommandContext(w.ctx, "git", "-C", w.repo, "rev-parse", "--git-dir").Run() == nil
}

// Path returns the on-disk worktree directory for a logical session name,
// namespaced by instanceID when set.
func (w *Worktreer) Path(name string) string {
	if w.instanceID == "" {
		return filepath.Join(w.repo, ".dctl-sessions", name)
	}
	return filepath.Join(w.repo, ".dctl-sessions", w.instanceID, name)
}

// Branch returns the git branch backing a logical session name, namespaced by
// instanceID when set.
func (w *Worktreer) Branch(name string) string {
	if w.instanceID == "" {
		return "session/" + name
	}
	return "session/" + w.instanceID + "/" + name
}

// Create adds a worktree on branch session/<name>. Returns ("", nil) when repo
// is not a git repo (caller falls back to a shared session).
func (w *Worktreer) Create(name string) (string, error) {
	if !w.isGitRepo() {
		return "", nil
	}
	p := w.Path(name)
	out, err := exec.CommandContext(w.ctx, "git", "-C", w.repo,
		"worktree", "add", p, "-b", w.Branch(name)).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("worktree add: %s", strings.TrimSpace(string(out)))
	}
	return p, nil
}

// Remove removes the worktree. If it has uncommitted changes and !force, it
// refuses with the status. The branch session/<name> is always left intact.
func (w *Worktreer) Remove(name string, force bool) error {
	p := w.Path(name)
	if !force {
		out, _ := exec.CommandContext(w.ctx, "git", "-C", p, "status", "--porcelain").Output()
		if strings.TrimSpace(string(out)) != "" {
			return fmt.Errorf("worktree %q has uncommitted changes", name)
		}
	}
	args := []string{"-C", w.repo, "worktree", "remove", p}
	if force {
		args = append(args, "--force")
	}
	out, err := exec.CommandContext(w.ctx, "git", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("worktree remove: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
