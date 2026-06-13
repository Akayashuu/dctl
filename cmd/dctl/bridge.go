package main

import (
	"context"
	"flag"

	"github.com/vskstudio/dctl"
	"github.com/vskstudio/dctl/internal/bridge"
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
	participants := fs.String("participants", "", "append-only journal of message authors for /session who")
	allowState := fs.String("allow-state", "", "daemon state.json read per-message to enforce the session allowlist (empty = no enforcement)")
	allowSession := fs.String("allow-session", "", "session name used with --allow-state to resolve the per-session allowlist")
	after := fs.String("after", "", "seed start id for the first run (state file wins once it exists)")
	verbose := fs.Bool("v", false, "log activity to stderr")
	fs.Parse(args)

	return bridge.Run(ctx, c, bridge.Options{
		Channel:      *ch,
		Cmd:          *cmdStr,
		Stream:       *stream,
		Model:        *model,
		Ensure:       *ensure,
		Interval:     *interval,
		State:        *state,
		Participants: *participants,
		AllowState:   *allowState,
		Session:      *allowSession,
		After:        *after,
		Verbose:      *verbose,
	})
}

// bridgeOptionsHasParticipants exists so a compile-time test can assert the
// --participants journal is wired into bridge.Options.
var bridgeOptionsHasParticipants = bridge.Options{}.Participants
