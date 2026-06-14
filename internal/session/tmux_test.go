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
	r := newTmuxResponder("dctl-test-"+t.Name(), "", []string{"bash", "--norc"}, "", 10*time.Second, nil)
	defer r.Close()
	out, err := r.Respond(context.Background(), DctlMessage{Content: "echo hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("expected output to contain %q, got %q", "hi", out)
	}
}

// Priming runs at start() before the first human turn; with a bash pane the init
// commands execute, the baseline advances past them, and the human turn still
// returns its own output (priming is not echoed back).
func TestTmuxResponderPriming(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	r := newTmuxResponder("dctl-test-"+t.Name(), "", []string{"bash", "--norc"}, "", 10*time.Second,
		[]string{"echo primed", "   "}) // blank prompt is skipped, not sent
	defer r.Close()
	out, err := r.Respond(context.Background(), DctlMessage{Content: "echo hi"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("expected human turn output %q, got %q", "hi", out)
	}
	if strings.Contains(out, "primed") {
		t.Fatalf("priming output should not echo into the human turn: %q", out)
	}
}

func TestSendLiteralArgsTerminatesOptions(t *testing.T) {
	// A Discord message starting with "-" must reach the pane as literal keys,
	// never as a send-keys flag — the "--" terminator guarantees that.
	got := strings.Join(sendLiteralArgs("dctl-chan", "-h --version"), " ")
	want := "send-keys -t dctl-chan -l -- -h --version"
	if got != want {
		t.Fatalf("sendLiteralArgs = %q, want %q", got, want)
	}
}

func TestSanitizeSessionName(t *testing.T) {
	// tmux rejects "." and ":" and trims whitespace; all must fold to "-".
	if got := sanitizeSessionName("dctl-inst.1:2 3"); got != "dctl-inst-1-2-3" {
		t.Fatalf("sanitizeSessionName = %q, want %q", got, "dctl-inst-1-2-3")
	}
	if got := sanitizeSessionName("dctl-123456"); got != "dctl-123456" {
		t.Fatalf("sanitizeSessionName altered a clean name: %q", got)
	}
}

func TestStripChromeKeepsBlockquoteProse(t *testing.T) {
	// A bare ">" prompt is chrome; "> quoted" is Claude prose and must survive.
	in := []string{">", "> a markdown quote", "plain line"}
	got := strings.Join(stripChrome(in), "\n")
	want := "> a markdown quote\nplain line"
	if got != want {
		t.Fatalf("stripChrome = %q, want %q", got, want)
	}
}

func TestAwaitQuiescenceWaitsWhileBusy(t *testing.T) {
	// Frame is static ("working") but busy=true must forbid settling until the
	// interrupt hint clears, then it settles on the final text.
	seq := []string{"working (esc to interrupt)", "working (esc to interrupt)", "working (esc to interrupt)", "done", "done"}
	i := 0
	capture := func() (string, error) {
		s := seq[i]
		if i < len(seq)-1 {
			i++
		}
		return s, nil
	}
	got, err := awaitQuiescence(context.Background(), capture, quiesceCfg{
		stable: 2, poll: 0, timeout: time.Second, busy: paneBusy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "done" {
		t.Fatalf("awaitQuiescence settled on %q, want %q", got, "done")
	}
}

func TestAwaitQuiescenceReturnsLastOnTimeout(t *testing.T) {
	// On timeout the last capture is returned alongside the error so the caller
	// can salvage a partial turn.
	capture := func() (string, error) { return changing(), nil }
	last, err := awaitQuiescence(context.Background(), capture, quiesceCfg{
		stable: 3, poll: 0, timeout: 1 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if last == "" {
		t.Fatal("expected the last capture to be returned on timeout, got empty")
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

func TestSanitizeInput(t *testing.T) {
	if got := sanitizeInput("one\ntwo\r\nthree\rfour"); got != "one two three four" {
		t.Fatalf("sanitizeInput = %q, want %q", got, "one two three four")
	}
	if got := sanitizeInput("single line"); got != "single line" {
		t.Fatalf("sanitizeInput should leave single lines untouched, got %q", got)
	}
}

func TestStripChromeHorizontalRule(t *testing.T) {
	in := []string{"────────────────", "kept line", "│ > x            │"}
	got := stripChrome(in)
	if strings.Join(got, "\n") != "kept line" {
		t.Fatalf("stripChrome = %q, want %q", strings.Join(got, "\n"), "kept line")
	}
}

func TestAwaitQuiescenceSkipsEmpty(t *testing.T) {
	// Empty captures must never settle; once content appears and stabilizes it returns.
	seq := []string{"", "", "", "hi", "hi", "hi"}
	i := 0
	capture := func() (string, error) {
		s := seq[i]
		if i < len(seq)-1 {
			i++
		}
		return s, nil
	}
	got, err := awaitQuiescence(context.Background(), capture, quiesceCfg{stable: 2, poll: 0, timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got != "hi" {
		t.Fatalf("awaitQuiescence settled on %q, want %q", got, "hi")
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
