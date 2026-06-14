package main

import (
	"context"
	"flag"
	"os"

	"github.com/vskstudio/dctl"
	"github.com/vskstudio/dctl/internal/serve"
)

func runServe(ctx context.Context, c *dctl.Client, token string, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	statePath := fs.String("state", serve.DefaultStatePath(), "path to the daemon state file")
	defaultCmd := fs.String("cmd", "claude", "default bridged base command for new sessions (stream-json mode adds -p and the stream flags)")
	healthAddr := fs.String("health-addr", "", "if set (e.g. :8787), serve GET /health")
	statusChannel := fs.String("status-channel", "", "if set, maintain a self-updating status embed there")
	instanceID := fs.String("instance", os.Getenv("DCTL_INSTANCE_ID"), "per-daemon instance id (slug) used to namespace shared Discord/git resources; defaults to DCTL_INSTANCE_ID")
	envFile := fs.String("env-file", "", "load DISCORD_BOT_TOKEN and other vars from this file before starting (used by `dctl service`)")
	fs.Parse(args)
	if *envFile != "" {
		// Load secrets in Go rather than via a shell/batch wrapper, then rebuild
		// the client from the now-populated environment (main built its client
		// before this file was read).
		if err := loadEnvFile(*envFile); err != nil {
			return err
		}
		token = os.Getenv("DISCORD_BOT_TOKEN")
		c = dctl.New(token, os.Getenv("DISCORD_CHANNEL_ID"))
	}
	if !c.Enabled() {
		return dctl.ErrDisabled
	}
	return serve.Run(ctx, c, serve.Options{
		StatePath:     *statePath,
		DefaultCmd:    *defaultCmd,
		HealthAddr:    *healthAddr,
		StatusChannel: *statusChannel,
		InstanceID:    *instanceID,
		Token:         token,
	})
}
