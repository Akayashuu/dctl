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
	after := fs.String("after", "", "seed start id for the first run (state file wins once it exists)")
	verbose := fs.Bool("v", false, "log activity to stderr")
	progress := fs.String("progress", "full", "live activity feedback level: off | actions | full")
	progressKeep := fs.Bool("progress-keep", false, "keep the full progress list instead of collapsing to a one-line summary")
	fs.Parse(args)

	return bridge.Run(ctx, c, bridge.Options{
		Channel:      *ch,
		Cmd:          *cmdStr,
		Stream:       *stream,
		Model:        *model,
		Ensure:       *ensure,
		Interval:     *interval,
		State:        *state,
		After:        *after,
		Verbose:      *verbose,
		Progress:     *progress,
		ProgressKeep: *progressKeep,
	})
}
