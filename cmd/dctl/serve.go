package main

import (
	"context"
	"flag"

	"github.com/vskstudio/dctl"
	"github.com/vskstudio/dctl/internal/serve"
)

func runServe(ctx context.Context, c *dctl.Client, token string, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	statePath := fs.String("state", serve.DefaultStatePath(), "path to the daemon state file")
	defaultCmd := fs.String("cmd", "claude", "default bridged base command for new sessions (stream-json mode adds -p and the stream flags)")
	healthAddr := fs.String("health-addr", "", "if set (e.g. :8787), serve GET /health")
	statusChannel := fs.String("status-channel", "", "if set, maintain a self-updating status embed there")
	fs.Parse(args)
	if !c.Enabled() {
		return dctl.ErrDisabled
	}
	return serve.Run(ctx, c, serve.Options{
		StatePath:     *statePath,
		DefaultCmd:    *defaultCmd,
		HealthAddr:    *healthAddr,
		StatusChannel: *statusChannel,
		Token:         token,
	})
}
