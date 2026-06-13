package bridge

import (
	"strings"
	"testing"
	"time"

	"github.com/vskstudio/dctl/internal/session"
)

func TestProgressRenderCapsLines(t *testing.T) {
	pv := newProgressView(nil, "full", false, time.Now())
	for i := 0; i < 20; i++ {
		pv.add(session.Event{Kind: "tool", Tool: "Read", Detail: "f.go"})
	}
	out := pv.render()
	if !strings.HasPrefix(out, "⏳ en cours…\n") {
		t.Fatalf("missing header: %q", out)
	}
	if !strings.Contains(out, "…\n") {
		t.Fatal("expected elision marker for >maxLines")
	}
	if n := strings.Count(out, "\n"); n != maxLines+1 {
		t.Fatalf("line count = %d, want %d", n, maxLines+1)
	}
}

func TestProgressActionsLevelDropsText(t *testing.T) {
	pv := newProgressView(nil, "actions", false, time.Now())
	pv.add(session.Event{Kind: "text", Detail: "thinking out loud"})
	pv.add(session.Event{Kind: "tool", Tool: "Bash", Detail: "ls"})
	out := pv.render()
	if strings.Contains(out, "thinking out loud") {
		t.Fatal("actions level must drop text events")
	}
	if !strings.Contains(out, "Bash · ls") {
		t.Fatalf("expected Bash line, got %q", out)
	}
}

func TestProgressSummary(t *testing.T) {
	pv := newProgressView(nil, "full", false, time.Now())
	pv.add(session.Event{Kind: "tool", Tool: "Bash", Detail: "a"})
	pv.add(session.Event{Kind: "tool", Tool: "Bash", Detail: "b"})
	pv.add(session.Event{Kind: "tool", Tool: "Read", Detail: "x"})
	pv.add(session.Event{Kind: "text", Detail: "noise"})
	pv.add(session.Event{Kind: "result", Cost: 0.04})
	s := pv.summary(false)
	if !strings.HasPrefix(s, "✅ 3 actions (Bash×2, Read)") {
		t.Fatalf("summary = %q", s)
	}
	if !strings.Contains(s, "$0.04") {
		t.Fatalf("expected cost in summary: %q", s)
	}
}

func TestProgressSummaryError(t *testing.T) {
	pv := newProgressView(nil, "full", false, time.Now())
	pv.add(session.Event{Kind: "tool", Tool: "Bash", Detail: "a"})
	if s := pv.summary(true); !strings.HasPrefix(s, "⚠️ 1 action") {
		t.Fatalf("summary = %q", s)
	}
}

func TestProgressPostsThrottledThenFlushes(t *testing.T) {
	var posts []string
	post := func(id, content string) (string, error) {
		posts = append(posts, content)
		return "msg-1", nil
	}
	pv := newProgressView(post, "full", false, time.Now())
	pv.add(session.Event{Kind: "tool", Tool: "Bash", Detail: "a"})
	pv.add(session.Event{Kind: "tool", Tool: "Read", Detail: "b"})
	if len(posts) != 1 {
		t.Fatalf("expected 1 throttled post, got %d", len(posts))
	}
	pv.finish(false)
	if len(posts) != 2 {
		t.Fatalf("expected final flush, got %d posts", len(posts))
	}
	if !strings.HasPrefix(posts[1], "✅") {
		t.Fatalf("final post should be summary, got %q", posts[1])
	}
}
