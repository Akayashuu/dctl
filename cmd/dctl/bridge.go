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
	cmdStr := fs.String("cmd", "", "command to run per message (message appended as last arg + piped on stdin)")
	ensure := fs.String("ensure", "prospector", "if no channel is set, create/reuse a channel with this name")
	interval := fs.Int("i", 5, "poll interval in seconds")
	state := fs.String("state", "", "file to persist the last-seen message id across restarts")
	after := fs.String("after", "", "start after this id (overrides state file)")
	verbose := fs.Bool("v", false, "log activity to stderr")
	fs.Parse(args)

	if strings.TrimSpace(*cmdStr) == "" {
		return fmt.Errorf("usage: dctl bridge --cmd '<command>' [-c CHANNEL] [-i 5] [--state FILE]")
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

	last := *after
	if last == "" && *state != "" {
		if b, err := os.ReadFile(*state); err == nil {
			last = strings.TrimSpace(string(b))
		}
	}
	// No baseline yet → anchor on the latest message so we don't replay history.
	if last == "" {
		if msgs, err := c.Read(ctx, *ch, 1, ""); err == nil && len(msgs) > 0 {
			last = msgs[len(msgs)-1].ID
		}
	}
	logf(*verbose, "bridge up: cmd=%q interval=%ds last=%s", *cmdStr, *interval, last)

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
			out, err := runCmd(ctx, *cmdStr, m)
			if err != nil && out == "" {
				out = "⚠️ " + err.Error()
			}
			out = strings.TrimSpace(out)
			if out == "" {
				continue
			}
			for _, chunk := range chunk(out, discordMaxLen) {
				if _, err := c.Reply(ctx, *ch, m.ID, chunk); err != nil {
					logf(true, "reply error: %v", err)
				}
			}
		}
		time.Sleep(time.Duration(*interval) * time.Second)
	}
}

// runCmd executes cmdStr (split on whitespace) with the message text appended
// as the final argument, piped on stdin, and exposed via DCTL_* env vars.
func runCmd(ctx context.Context, cmdStr string, m dctl.Message) (string, error) {
	fields := strings.Fields(cmdStr)
	args := append(fields[1:], m.Content)
	cmd := exec.CommandContext(ctx, fields[0], args...)
	cmd.Stdin = strings.NewReader(m.Content)
	cmd.Env = append(os.Environ(),
		"DCTL_MSG="+m.Content,
		"DCTL_AUTHOR="+m.Author.Username,
		"DCTL_MESSAGE_ID="+m.ID,
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
