package forge

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeRunner records calls and returns scripted output/errors keyed by argv[0].
type fakeRunner struct {
	calls   [][]string
	out     map[string][]byte // keyed by first arg (e.g. "gh", "glab", "git")
	err     map[string]error
	lookErr map[string]error // exec.LookPath result per binary
}

func (f *fakeRunner) look(name string) error {
	if f.lookErr == nil {
		return nil
	}
	return f.lookErr[name]
}

func (f *fakeRunner) run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.out[name], f.err[name]
}

func TestAvailableReportsBothAbsent(t *testing.T) {
	r := &fakeRunner{lookErr: map[string]error{"gh": errors.New("nope"), "glab": errors.New("nope")}}
	c := &Client{r: r}
	gh, gl := c.Available()
	if gh || gl {
		t.Fatalf("expected both absent, got gh=%v gl=%v", gh, gl)
	}
}

func TestListMergesGitHubOnly(t *testing.T) {
	r := &fakeRunner{
		lookErr: map[string]error{"glab": errors.New("nope")}, // only gh present
		out: map[string][]byte{
			"gh": []byte(`[{"nameWithOwner":"me/app","sshUrl":"git@github.com:me/app.git","description":"d"}]`),
		},
	}
	c := &Client{r: r}
	repos, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(repos) != 1 || repos[0].FullName != "me/app" || repos[0].Forge != "github" {
		t.Fatalf("unexpected repos: %+v", repos)
	}
}

func TestListEmptyWhenNoForge(t *testing.T) {
	r := &fakeRunner{lookErr: map[string]error{"gh": errors.New("x"), "glab": errors.New("x")}}
	c := &Client{r: r}
	repos, err := c.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("expected no repos, got %+v", repos)
	}
}

func TestCloneRejectsBadSpec(t *testing.T) {
	c := &Client{r: &fakeRunner{}}
	if _, err := c.Clone(context.Background(), "../evil", "/ws"); err == nil {
		t.Fatal("expected rejection of traversal spec")
	}
	if _, err := c.Clone(context.Background(), "a b; rm -rf", "/ws"); err == nil {
		t.Fatal("expected rejection of spec with shell metacharacters")
	}
}

func TestCloneOwnerNameUsesGh(t *testing.T) {
	r := &fakeRunner{} // gh present (no lookErr)
	c := &Client{r: r}
	dir, err := c.Clone(context.Background(), "me/app", "/ws")
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if dir != "/ws/app" {
		t.Fatalf("dir = %q, want /ws/app", dir)
	}
	if len(r.calls) != 1 || r.calls[0][0] != "gh" || !strings.Contains(strings.Join(r.calls[0], " "), "me/app") {
		t.Fatalf("expected gh clone call, got %+v", r.calls)
	}
}

func TestCloneFullURLUsesGit(t *testing.T) {
	r := &fakeRunner{}
	c := &Client{r: r}
	dir, err := c.Clone(context.Background(), "https://github.com/me/app.git", "/ws")
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if dir != "/ws/app" {
		t.Fatalf("dir = %q, want /ws/app", dir)
	}
	if r.calls[0][0] != "git" {
		t.Fatalf("expected git clone, got %+v", r.calls)
	}
}
