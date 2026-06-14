package session

import (
	"regexp"
	"strings"
)

// toolEvent is one tool invocation parsed from a tmux pane capture.
type toolEvent struct {
	Tool   string
	Detail string
}

// toolBullets are the glyphs Claude's TUI prints before a tool-call line. The
// current build uses ⏺; older builds use ●.
const toolBullets = "⏺●"

// toolLineRe matches a tool-call bullet line after leading whitespace: the bullet,
// the tool name, and an optional "(args)" group. "⎿" continuation lines never
// match (they carry no bullet).
var toolLineRe = regexp.MustCompile(`^[` + toolBullets + `]\s+([A-Za-z][A-Za-z0-9_.-]*)\s*(?:\((.*)\))?\s*$`)

// parseToolEvents returns the tool invocations in text, in first-seen order.
// Non-tool prose and ⎿ result lines are ignored.
func parseToolEvents(text string) []toolEvent {
	var out []toolEvent
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		m := toolLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// m[2] is safe to index: a non-participating optional group returns "" in Go.
		out = append(out, toolEvent{Tool: m[1], Detail: strings.TrimSpace(m[2])})
	}
	return out
}
