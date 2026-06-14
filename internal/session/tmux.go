package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// newLines returns the lines of after that follow the longest common line
// prefix it shares with before. tmux scrollback is append-only above the input
// box, so the shared prefix is everything from prior turns and the remainder is
// what this turn added (echoed input + Claude's output + the redrawn input box).
func newLines(before, after string) []string {
	b := strings.Split(strings.TrimRight(before, "\n"), "\n")
	a := strings.Split(strings.TrimRight(after, "\n"), "\n")
	i := 0
	for i < len(b) && i < len(a) && b[i] == a[i] {
		i++
	}
	return a[i:]
}

// chromeLine reports whether a captured line is TUI furniture (borders, the
// input box, status/hint lines, spinner) rather than Claude's prose.
func chromeLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false // blank handled by the blank-collapser, not dropped here
	}
	// Border / input-box frame: corner glyphs, or a line made up entirely of
	// box-drawing/space runes (a bare horizontal rule has no corners).
	if strings.ContainsAny(t, "╭╮╰╯┌┐└┘") {
		return true
	}
	if strings.TrimLeft(t, "─│╭╮╰╯┌┐└┘ ") == "" {
		return true
	}
	if strings.HasPrefix(t, "│") {
		return true // input box content line
	}
	// A bare ">" (optionally with a cursor/placeholder) is the empty input
	// prompt. Don't strip "> text": Claude prose can be a markdown blockquote,
	// and the echoed user input is already removed by the newLines prefix diff.
	if t == ">" || strings.TrimSpace(strings.TrimPrefix(t, ">")) == "" {
		return true
	}
	// Status / hint / spinner lines.
	for _, p := range []string{"⏵⏵", "? for shortcuts", "(esc to interrupt)", "esc to interrupt"} {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

// extractTurn turns a before/after scrollback pair into the clean text Claude
// added this turn. Empty result means nothing new survived stripping.
func extractTurn(before, after string) string {
	lines := stripChrome(newLines(before, after))
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// stripChrome drops chrome lines and collapses runs of blank lines, returning
// the cleaned prose lines with no leading/trailing blanks.
func stripChrome(lines []string) []string {
	var out []string
	blank := false
	for _, l := range lines {
		if chromeLine(l) {
			continue
		}
		if strings.TrimSpace(l) == "" {
			blank = true
			continue
		}
		if blank && len(out) > 0 {
			out = append(out, "")
		}
		blank = false
		out = append(out, strings.TrimRight(l, " "))
	}
	return out
}

// quiesceCfg tunes the quiescence poll. stable = number of consecutive equal
// captures that mark "done"; poll = delay between captures; timeout = hard cap;
// busy = optional predicate that, while it reports true for the current capture,
// forbids settling (the program is still working even if the frame looks static,
// e.g. Claude is mid-tool-call with the spinner repainted to an identical pixel).
type quiesceCfg struct {
	stable  int
	poll    time.Duration
	timeout time.Duration
	busy    func(string) bool
}

// paneBusy reports whether a capture shows Claude still actively working — its
// interrupt hint is on screen — so a brief static frame must not be mistaken for
// a finished turn.
func paneBusy(capture string) bool {
	return strings.Contains(capture, "esc to interrupt")
}

// awaitQuiescence polls capture until the pane text is unchanged for cfg.stable
// consecutive reads, then returns that text. It errors on timeout or a capture
// error, or if ctx is cancelled. An empty capture never counts as settled: a
// freshly created pane returns "" until the program paints its first frame, so
// settling on it would baseline against a blank screen and leak the whole first
// paint into the next turn's diff. While cfg.busy reports true the pane is never
// considered settled, so a static mid-turn frame can't be mistaken for "done".
//
// On timeout it returns the last capture seen alongside the error, so a caller
// can salvage a partial turn (and re-baseline) instead of losing it entirely.
func awaitQuiescence(ctx context.Context, capture func() (string, error), cfg quiesceCfg) (string, error) {
	deadline := time.Now().Add(cfg.timeout)
	var last string
	same := 0
	for {
		if ctx.Err() != nil {
			return last, ctx.Err()
		}
		cur, err := capture()
		if err != nil {
			return last, err
		}
		if cur == last {
			same++
		} else {
			same, last = 0, cur
		}
		busy := cfg.busy != nil && cfg.busy(cur)
		if same >= cfg.stable && cur != "" && !busy {
			return cur, nil
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("tmux pane did not settle within %s", cfg.timeout)
		}
		if cfg.poll > 0 {
			time.Sleep(cfg.poll)
		}
	}
}

// tmuxResponder drives an interactive `claude` TUI inside a persistent tmux
// session: one session per bridge, lazily started on the first message and
// reused for every later message (Claude keeps its context hot). Each turn
// types the message with send-keys, waits for the pane to settle, and returns
// the cleaned text Claude added.
type tmuxResponder struct {
	sessName string
	dir      string
	cmd      []string // program launched in the pane (e.g. ["claude","--dangerously-skip-permissions"])
	timeout  time.Duration

	mu       sync.Mutex
	started  bool
	baseline string // cleaned-up: full capture after the previous turn settled
}

// newTmuxResponder builds a responder. base is the pane command (defaults to
// claude --dangerously-skip-permissions); model is appended when set.
func newTmuxResponder(sessName, dir string, base []string, model string, timeout time.Duration) *tmuxResponder {
	cmd := append([]string{}, base...)
	if len(cmd) == 0 {
		cmd = []string{"claude", "--dangerously-skip-permissions"}
	}
	if model != "" {
		cmd = append(cmd, "--model", model)
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &tmuxResponder{sessName: sessName, dir: dir, cmd: cmd, timeout: timeout}
}

// tmuxRun runs a tmux command without cancellation — used for best-effort
// teardown (Close) where there is no caller context to honor.
func tmuxRun(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).CombinedOutput()
	return string(out), err
}

// tmuxRunCtx runs a tmux command bound to ctx so a cancelled turn doesn't leave
// a send-keys/capture blocking past the caller's deadline.
func tmuxRunCtx(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput()
	return string(out), err
}

func (t *tmuxResponder) capture(ctx context.Context) (string, error) {
	return tmuxRunCtx(ctx, "capture-pane", "-p", "-S", "-", "-t", t.sessName)
}

func (t *tmuxResponder) capturePoll(ctx context.Context) (string, error) {
	return awaitQuiescence(ctx, func() (string, error) { return t.capture(ctx) }, quiesceCfg{
		stable:  3,
		poll:    300 * time.Millisecond,
		timeout: t.timeout,
		busy:    paneBusy,
	})
}

func (t *tmuxResponder) start(ctx context.Context) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux not found on PATH: %w", err)
	}
	// A leftover session from a previous crash would make new-session fail with
	// "duplicate session". Best-effort kill any stale namesake first so we always
	// start from a clean, freshly-launched pane.
	_, _ = tmuxRun("kill-session", "-t", t.sessName)

	// Pin the working directory explicitly. new-session without -c inherits the
	// tmux *server* cwd (a long-lived daemon that may already run elsewhere), not
	// this process's, so an empty dir is resolved to our own cwd here.
	dir := t.dir
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	args := []string{"new-session", "-d", "-s", t.sessName, "-x", "200", "-y", "50"}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	args = append(args, strings.Join(t.cmd, " "))
	if out, err := tmuxRunCtx(ctx, args...); err != nil {
		return fmt.Errorf("tmux new-session: %v: %s", err, out)
	}
	// Wait for the TUI to finish drawing its first prompt before we type. On
	// failure the pane is left for Close (or the next start's stale-kill) to reap;
	// started stays false so we never type against an unsettled baseline.
	settled, err := t.capturePoll(ctx)
	if err != nil {
		return err
	}
	t.baseline = settled
	t.started = true
	return nil
}

// sendLiteralArgs builds the tmux send-keys argv that types text verbatim into a
// pane. The `--` terminates option parsing so a message beginning with `-` (e.g.
// "-h") is typed literally instead of being mistaken for a send-keys flag.
func sendLiteralArgs(sess, text string) []string {
	return []string{"send-keys", "-t", sess, "-l", "--", text}
}

func (t *tmuxResponder) Respond(ctx context.Context, m DctlMessage, _ func(Event)) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.started {
		if err := t.start(ctx); err != nil {
			return "", err
		}
	}
	before := t.baseline
	// Collapse embedded newlines to spaces: a literal newline sent to the TUI is
	// read as Enter, which would submit the message early and desync the turn.
	// A single coherent line is the v1 contract (see SKILL.md known limitation).
	text := sanitizeInput(m.Content)
	if out, err := tmuxRunCtx(ctx, sendLiteralArgs(t.sessName, text)...); err != nil {
		return "", fmt.Errorf("tmux send-keys: %v: %s", err, out)
	}
	if out, err := tmuxRunCtx(ctx, "send-keys", "-t", t.sessName, "Enter"); err != nil {
		return "", fmt.Errorf("tmux send-keys Enter: %v: %s", err, out)
	}
	after, err := t.capturePoll(ctx)
	// Advance the baseline to whatever was on screen even on timeout, so the next
	// turn diffs against the current pane instead of replaying this turn's output.
	if after != "" {
		t.baseline = after
	}
	if err != nil {
		// Salvage whatever Claude produced before the deadline rather than losing
		// the turn; surface the error only when there's nothing to show.
		if partial := extractTurn(before, after); partial != "" {
			return partial, nil
		}
		return "", err
	}
	reply := extractTurn(before, after)
	if reply == "" {
		// Never silently lose a turn: fall back to the raw diff.
		reply = strings.TrimSpace(strings.Join(newLines(before, after), "\n"))
	}
	return reply, nil
}

// sanitizeInput flattens a message to a single line so send-keys -l never feeds
// a newline (= Enter) into the TUI mid-message. CR/LF runs collapse to one space.
func sanitizeInput(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Join(strings.Split(s, "\n"), " ")
}

func (t *tmuxResponder) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Best-effort and unconditional: kill the namesake even if start() failed
	// after new-session (settled poll errored) so a half-started pane is never
	// leaked. A no-op when nothing is there.
	_, _ = tmuxRun("kill-session", "-t", t.sessName)
	t.started = false
	return nil
}

// tmuxSessionName derives a collision-safe tmux session name from the channel,
// namespaced by DCTL_INSTANCE_ID (same scheme as session worktrees). tmux forbids
// "." and ":" in session names (and trims whitespace), so they are folded to "-".
func tmuxSessionName(channel string) string {
	name := "dctl-" + channel
	if inst := os.Getenv("DCTL_INSTANCE_ID"); inst != "" {
		name = "dctl-" + inst + "-" + channel
	}
	return sanitizeSessionName(name)
}

// sanitizeSessionName folds characters tmux rejects in a session name ("." and
// ":") and any whitespace into "-", keeping the name addressable by -t.
func sanitizeSessionName(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '.', ':', ' ', '\t', '\n':
			return '-'
		}
		return r
	}, s)
}
