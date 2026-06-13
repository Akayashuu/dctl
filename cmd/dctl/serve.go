package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vskstudio/dctl"
	"github.com/vskstudio/dctl/internal/gateway"
	healthpkg "github.com/vskstudio/dctl/internal/health"
	"github.com/vskstudio/dctl/internal/state"
	"github.com/vskstudio/dctl/internal/worktree"
)

func defaultStatePath() string {
	if d := os.Getenv("DCTL_STATE_DIR"); d != "" {
		return filepath.Join(d, "state.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dctl", "state.json")
}

// runServe is the always-on Gateway daemon: it keeps the bot online, registers
// slash commands, supervises one bridge per session, and exposes liveness.
func runServe(ctx context.Context, c *dctl.Client, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	statePath := fs.String("state", defaultStatePath(), "path to the daemon state file")
	defaultCmd := fs.String("cmd", "claude", "default bridged base command for new sessions (stream-json mode adds -p and the stream flags)")
	healthAddr := fs.String("health-addr", "", "if set (e.g. :8787), serve GET /health")
	statusChannel := fs.String("status-channel", "", "if set, maintain a self-updating status embed there")
	fs.Parse(args)
	if !c.Enabled() {
		return dctl.ErrDisabled
	}

	health := healthpkg.NewHealth(time.Now())

	st, err := state.LoadState(*statePath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	// Seed the allowlist with the owner on first run.
	if owner := os.Getenv("DCTL_OWNER_ID"); owner != "" {
		_ = st.AddAllow(owner)
	}

	self, _ := os.Executable()
	sup := NewSupervisor(ctx, self)
	// Restart persisted sessions.
	for _, sess := range st.SnapshotSessions() {
		_ = sup.Start(sess)
	}
	health.SetSessions(len(st.SnapshotSessions()))

	repo := st.Repo
	if repo == "" {
		repo, _ = os.Getwd()
	}
	wt := worktree.NewWorktreer(ctx, repo)
	h := dctl.NewHandler(c, sup, wt, st, *defaultCmd)

	if err := c.RegisterCommands(ctx); err != nil {
		return fmt.Errorf("register commands: %w", err)
	}

	if *healthAddr != "" {
		go serveHealth(ctx, *healthAddr, health)
	}
	go pingLoop(ctx, c, health)
	if *statusChannel != "" {
		go statusLoop(ctx, c, st, health, *statusChannel)
	}

	fmt.Fprintln(os.Stderr, "dctl serve: commands registered; connecting to gateway…")

	// Reconnect loop: a dropped connection just re-IDENTIFYs (no resume).
	for ctx.Err() == nil {
		gw := gateway.NewGateway(c, health)
		errCh := make(chan error, 1)
		go func() { errCh <- gw.Run(ctx) }()
	dispatch:
		for {
			select {
			case in := <-gw.Interactions:
				resp := h.Handle(ctx, in)
				if err := c.RespondInteraction(ctx, in.ID, in.Token, resp); err != nil {
					fmt.Fprintf(os.Stderr, "respond: %v\n", err)
				}
				health.SetSessions(len(st.SnapshotSessions())) // session count may have changed
			case err := <-errCh:
				fmt.Fprintf(os.Stderr, "gateway closed (%v); reconnecting in 3s…\n", err)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(3 * time.Second):
				}
				break dispatch
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return ctx.Err()
}
