package session

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTmuxResponderIntegration(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	r := newTmuxResponder("dctl-test-"+t.Name(), "", []string{"bash", "--norc"}, "", 10*time.Second)
	defer r.Close()
	out, err := r.Respond(context.Background(), DctlMessage{Content: "echo hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("expected output to contain %q, got %q", "hi", out)
	}
}

func TestNewLines(t *testing.T) {
	before := "banner\n> \n"
	after := "banner\nhello from claude\nmore output\n> \n"
	got := strings.Join(newLines(before, after), "\n")
	want := "hello from claude\nmore output\n> "
	if got != want {
		t.Fatalf("newLines =\n%q\nwant\n%q", got, want)
	}
}

func TestStripChrome(t *testing.T) {
	in := []string{
		"╭────────────────────────╮",
		"│ > hello                │",
		"╰────────────────────────╯",
		"",
		"Here is your answer.",
		"It spans two lines.",
		"",
		"",
		"⏵⏵ accept edits on (shift+tab to cycle)",
		"? for shortcuts",
	}
	got := stripChrome(in)
	want := "Here is your answer.\nIt spans two lines."
	if strings.Join(got, "\n") != want {
		t.Fatalf("stripChrome =\n%q\nwant\n%q", strings.Join(got, "\n"), want)
	}
}

func TestExtractTurn(t *testing.T) {
	before := " ▐▛███▜▌  Claude Code\n ▝▜█████▛▘\n> what is 2+2\n"
	after, err := os.ReadFile("testdata/claude_done.txt")
	if err != nil {
		t.Fatal(err)
	}
	got := extractTurn(before, string(after))
	if got != "2 + 2 = 4." {
		t.Fatalf("extractTurn = %q, want %q", got, "2 + 2 = 4.")
	}
}

func TestAwaitQuiescence(t *testing.T) {
	seq := []string{"a", "ab", "ab", "ab"} // changes then stabilizes
	i := 0
	capture := func() (string, error) {
		s := seq[i]
		if i < len(seq)-1 {
			i++
		}
		return s, nil
	}
	got, err := awaitQuiescence(context.Background(), capture, quiesceCfg{
		stable: 2, poll: 0, timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "ab" {
		t.Fatalf("awaitQuiescence = %q, want %q", got, "ab")
	}
}

func TestAwaitQuiescenceTimeout(t *testing.T) {
	capture := func() (string, error) { return changing(), nil }
	_, err := awaitQuiescence(context.Background(), capture, quiesceCfg{
		stable: 3, poll: 0, timeout: 1 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error for never-stable pane")
	}
}

var n int

func changing() string { n++; return strings.Repeat("x", n) }
