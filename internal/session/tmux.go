package session

import (
	"context"
	"fmt"
	"strings"
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
	// Border / input-box frame: only box-drawing chars, spaces, and a leading
	// prompt marker.
	if strings.ContainsAny(t, "╭╮╰╯┌┐└┘") {
		return true
	}
	if strings.HasPrefix(t, "│") || strings.HasPrefix(t, ">") {
		return true // input box content line
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
// captures that mark "done"; poll = delay between captures; timeout = hard cap.
type quiesceCfg struct {
	stable  int
	poll    time.Duration
	timeout time.Duration
}

// awaitQuiescence polls capture until the pane text is unchanged for cfg.stable
// consecutive reads, then returns that text. It errors on timeout or a capture
// error, or if ctx is cancelled.
func awaitQuiescence(ctx context.Context, capture func() (string, error), cfg quiesceCfg) (string, error) {
	deadline := time.Now().Add(cfg.timeout)
	var last string
	same := 0
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		cur, err := capture()
		if err != nil {
			return "", err
		}
		if cur == last {
			same++
		} else {
			same, last = 0, cur
		}
		if same >= cfg.stable {
			return cur, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("tmux pane did not settle within %s", cfg.timeout)
		}
		if cfg.poll > 0 {
			time.Sleep(cfg.poll)
		}
	}
}
