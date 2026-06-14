package session

import (
	"strings"
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
