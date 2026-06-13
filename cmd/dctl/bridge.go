package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vskstudio/dctl"
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

// runBridge links a channel to an external command: it watches for new human
// messages and, for each, runs `--cmd` with the message text, then posts the
// command's stdout back as a threaded reply. The canonical use is binding a
// persistent Claude session to a channel:
//
//	dctl bridge --cmd 'claude -p --continue'
//
// The message text is passed to the command three ways (use whichever fits):
// appended as the final argument, piped on stdin, and via env vars
// (DCTL_MSG, DCTL_AUTHOR, DCTL_MESSAGE_ID, DCTL_CHANNEL).
func runBridge(ctx context.Context, c *dctl.Client, args []string) error {
	fs := flag.NewFlagSet("bridge", flag.ExitOnError)
	ch := channelFlag(fs)
	cmdStr := fs.String("cmd", "", "base command (default 'claude' in stream mode; the per-message program in one-shot mode)")
	stream := fs.Bool("stream", true, "keep one persistent claude stream-json process per session (false = one-shot per message)")
	model := fs.String("model", "", "model for the persistent claude session (e.g. claude-haiku-4-5-20251001)")
	ensure := fs.String("ensure", "prospector", "if no channel is set, create/reuse a channel with this name")
	interval := fs.Int("i", 5, "poll interval in seconds")
	state := fs.String("state", "", "file to persist the last-seen message id across restarts")
	after := fs.String("after", "", "seed start id for the first run (state file wins once it exists)")
	verbose := fs.Bool("v", false, "log activity to stderr")
	fs.Parse(args)

	if !*stream && strings.TrimSpace(*cmdStr) == "" {
		return fmt.Errorf("usage: dctl bridge --cmd '<command>' --stream=false [-c CHANNEL] [-i 5] [--state FILE]")
	}
	if !c.Enabled() {
		return dctl.ErrDisabled
	}

	// No channel configured anywhere → create (or reuse) a default one so the
	// bridge always has somewhere to talk.
	if *ch == "" && c.DefaultChannel() == "" {
		created, err := c.EnsureChannel(ctx, "", *ensure)
		if err != nil {
			return fmt.Errorf("no channel set and could not create %q: %w", *ensure, err)
		}
		*ch = created.ID
		logf(true, "no default channel — using #%s (%s)", created.Name, created.ID)
	}

	// The persisted state file is authoritative: a restart resumes exactly where
	// it left off, never replaying messages it already handled. --after only
	// seeds the very first run (before any state exists).
	var last string
	if *state != "" {
		if b, err := os.ReadFile(*state); err == nil {
			last = strings.TrimSpace(string(b))
		}
	}
	if last == "" {
		last = *after
	}
	// No baseline yet → anchor on the latest message so we don't replay history.
	if last == "" {
		if msgs, err := c.Read(ctx, *ch, 1, ""); err == nil && len(msgs) > 0 {
			last = msgs[len(msgs)-1].ID
		}
	}
	logf(*verbose, "bridge up: cmd=%q stream=%v model=%q interval=%ds last=%s", *cmdStr, *stream, *model, *interval, last)

	oneShot := func(ctx context.Context, mm dctlMessage) (string, error) {
		return runCmd(ctx, *cmdStr, mm)
	}
	resp := newResponder(ctx, *stream, *cmdStr, *model, oneShot)
	defer resp.Close()

	for {
		msgs, err := c.Read(ctx, *ch, 100, last)
		if err != nil {
			logf(true, "read error: %v", err)
			time.Sleep(time.Duration(*interval) * time.Second)
			continue
		}
		for _, m := range msgs {
			last = m.ID
			persist(*state, last)
			if m.Author.Bot {
				continue // never answer a bot (incl. ourselves) → no loops
			}
			logf(*verbose, "<%s> %s", m.Author.Username, oneline(m.Content))
			// Acknowledge immediately so the human sees the message was picked
			// up while the (slow) command runs. Best-effort: ignore if the bot
			// lacks Add Reactions.
			_ = c.React(ctx, *ch, m.ID, ackEmoji)
			out, err := resp.Respond(ctx, dctlMessage{
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
				_ = c.Unreact(ctx, *ch, m.ID, ackEmoji)
				_ = c.React(ctx, *ch, m.ID, failEmoji)
				continue
			}
			for _, chunk := range chunk(out, discordMaxLen) {
				if _, err := c.Reply(ctx, *ch, m.ID, chunk); err != nil {
					logf(true, "reply error: %v", err)
				}
			}
			// Swap the "seen" mark for a "done" mark once the answer is posted.
			_ = c.Unreact(ctx, *ch, m.ID, ackEmoji)
			_ = c.React(ctx, *ch, m.ID, doneEmoji)
		}
		time.Sleep(time.Duration(*interval) * time.Second)
	}
}

// runCmd executes cmdStr (split on whitespace) with the message text appended
// as the final argument, piped on stdin, and exposed via DCTL_* env vars.
func runCmd(ctx context.Context, cmdStr string, m dctlMessage) (string, error) {
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
