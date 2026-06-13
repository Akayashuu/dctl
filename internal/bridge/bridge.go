// Package bridge implements the dctl bridge loop: it watches a Discord channel
// for human messages and, for each, runs an external command (or a persistent
// Claude session) then posts the output back as a threaded reply.
package bridge

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vskstudio/dctl"
	"github.com/vskstudio/dctl/internal/session"
	"github.com/vskstudio/dctl/internal/state"
)

// discordMaxLen is Discord's hard per-message character limit.
const discordMaxLen = 2000

// Reaction marks the bridge puts on a human message: ack on pickup, swapped for
// done/fail once the command finishes.
const (
	ackEmoji  = "👀"
	doneEmoji = "✅"
	failEmoji = "⚠️"
)

// Options configures one bridge run (parsed from CLI flags by cmd/dctl).
type Options struct {
	Channel  string
	Cmd      string
	Stream   bool
	Model    string
	Ensure   string
	Interval int
	State    string
	After    string
	Participants string // append-only journal of message authors (empty = disabled)
	Verbose      bool
}

// Run links the channel to the command until ctx is cancelled.
// Body lifted verbatim from the old runBridge minus flag parsing.
func Run(ctx context.Context, c *dctl.Client, o Options) error {
	if !o.Stream && strings.TrimSpace(o.Cmd) == "" {
		return fmt.Errorf("usage: dctl bridge --cmd '<command>' --stream=false [-c CHANNEL] [-i 5] [--state FILE]")
	}
	if !c.Enabled() {
		return dctl.ErrDisabled
	}

	// No channel configured anywhere → create (or reuse) a default one so the
	// bridge always has somewhere to talk.
	ch := o.Channel
	if ch == "" && c.DefaultChannel() == "" {
		created, err := c.EnsureChannel(ctx, "", o.Ensure)
		if err != nil {
			return fmt.Errorf("no channel set and could not create %q: %w", o.Ensure, err)
		}
		ch = created.ID
		logf(true, "no default channel — using #%s (%s)", created.Name, created.ID)
	}

	// The persisted state file is authoritative: a restart resumes exactly where
	// it left off, never replaying messages it already handled. --after only
	// seeds the very first run (before any state exists).
	var last string
	if o.State != "" {
		if b, err := os.ReadFile(o.State); err == nil {
			last = strings.TrimSpace(string(b))
		}
	}
	if last == "" {
		last = o.After
	}
	// No baseline yet → anchor on the latest message so we don't replay history.
	if last == "" {
		if msgs, err := c.Read(ctx, ch, 1, ""); err == nil && len(msgs) > 0 {
			last = msgs[len(msgs)-1].ID
		}
	}
	logf(o.Verbose, "bridge up: cmd=%q stream=%v model=%q interval=%ds last=%s", o.Cmd, o.Stream, o.Model, o.Interval, last)

	oneShot := func(ctx context.Context, mm session.DctlMessage) (string, error) {
		return runCmd(ctx, o.Cmd, mm)
	}
	resp := session.NewResponder(ctx, o.Stream, o.Cmd, o.Model, oneShot)
	defer resp.Close()

	for {
		msgs, err := c.Read(ctx, ch, 100, last)
		if err != nil {
			logf(true, "read error: %v", err)
			time.Sleep(time.Duration(o.Interval) * time.Second)
			continue
		}
		for _, m := range msgs {
			last = m.ID
			persist(o.State, last)
			if m.Author.Bot {
				continue // never answer a bot (incl. ourselves) → no loops
			}
			recordParticipant(o.Participants, m.Author.ID)
			logf(o.Verbose, "<%s> %s", m.Author.Username, oneline(m.Content))
			// Acknowledge immediately so the human sees the message was picked
			// up while the (slow) command runs. Best-effort: ignore if the bot
			// lacks Add Reactions.
			_ = c.React(ctx, ch, m.ID, ackEmoji)
			out, err := resp.Respond(ctx, session.DctlMessage{
				Content:   m.Content,
				Author:    m.Author.Username,
				MessageID: m.ID,
				ChannelID: m.ChannelID,
			})
			if err != nil && out == "" {
				out = "⚠️ " + err.Error()
			}
			out = strings.TrimSpace(out)
			if out == "" {
				_ = c.Unreact(ctx, ch, m.ID, ackEmoji)
				_ = c.React(ctx, ch, m.ID, failEmoji)
				continue
			}
			for _, chunk := range chunk(out, discordMaxLen) {
				if _, err := c.Reply(ctx, ch, m.ID, chunk); err != nil {
					logf(true, "reply error: %v", err)
				}
			}
			// Swap the "seen" mark for a "done" mark once the answer is posted.
			_ = c.Unreact(ctx, ch, m.ID, ackEmoji)
			_ = c.React(ctx, ch, m.ID, doneEmoji)
		}
		time.Sleep(time.Duration(o.Interval) * time.Second)
	}
}

// runCmd executes cmdStr (split on whitespace) with the message text appended
// as the final argument, piped on stdin, and exposed via DCTL_* env vars.
func runCmd(ctx context.Context, cmdStr string, m session.DctlMessage) (string, error) {
	fields := strings.Fields(cmdStr)
	args := append(fields[1:], m.Content)
	cmd := exec.CommandContext(ctx, fields[0], args...)
	cmd.Stdin = strings.NewReader(m.Content)
	cmd.Env = append(os.Environ(),
		"DCTL_MSG="+m.Content,
		"DCTL_AUTHOR="+m.Author,
		"DCTL_MESSAGE_ID="+m.MessageID,
		"DCTL_CHANNEL="+m.ChannelID,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func persist(path, id string) {
	if path == "" || id == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(id+"\n"), 0o644)
}

// recordParticipant best-effort appends a human author id to the journal so the
// daemon can answer /session who. Errors are swallowed: observability must never
// break the bridge loop.
func recordParticipant(path, userID string) {
	_, _ = state.AppendParticipant(path, userID)
}

// chunk splits s into pieces no longer than max, preferring to break on a
// newline boundary so multi-line command output stays readable.
func chunk(s string, max int) []string {
	var out []string
	for len(s) > max {
		cut := max
		if nl := strings.LastIndexByte(s[:max], '\n'); nl > max/2 {
			cut = nl
		}
		out = append(out, s[:cut])
		s = strings.TrimPrefix(s[cut:], "\n")
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

func oneline(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

func logf(on bool, format string, a ...any) {
	if !on {
		return
	}
	w := bufio.NewWriter(os.Stderr)
	fmt.Fprintf(w, "dctl bridge: "+format+"\n", a...)
	w.Flush()
}
