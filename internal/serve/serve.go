package serve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vskstudio/dctl"
	"github.com/vskstudio/dctl/internal/gateway"
	"github.com/vskstudio/dctl/internal/handler"
	"github.com/vskstudio/dctl/internal/health"
	"github.com/vskstudio/dctl/internal/state"
	"github.com/vskstudio/dctl/internal/supervisor"
	"github.com/vskstudio/dctl/internal/worktree"
)

// Options holds the parsed flags for the serve daemon.
type Options struct {
	StatePath     string
	DefaultCmd    string
	HealthAddr    string
	StatusChannel string
}

// DefaultStatePath returns the default path to the daemon state file.
func DefaultStatePath() string {
	if d := os.Getenv("DCTL_STATE_DIR"); d != "" {
		return filepath.Join(d, "state.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dctl", "state.json")
}

// Run is the always-on Gateway daemon (gateway + supervisor + liveness).
func Run(ctx context.Context, c *dctl.Client, o Options) error {
	h := health.NewHealth(time.Now())

	st, err := state.LoadState(o.StatePath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	// Seed the allowlist with the owner on first run.
	if owner := os.Getenv("DCTL_OWNER_ID"); owner != "" {
		_ = st.AddAllow(owner)
	}

	self, _ := os.Executable()
	sup := supervisor.NewSupervisor(ctx, self)
	// Restart persisted sessions.
	for _, sess := range st.SnapshotSessions() {
		_ = sup.Start(sess)
	}
	h.SetSessions(len(st.SnapshotSessions()))

	repo := st.Repo
	if repo == "" {
		repo, _ = os.Getwd()
	}
	wt := worktree.NewWorktreer(ctx, repo)
	hdl := handler.NewHandler(c, sup, wt, st, o.DefaultCmd)

	if err := c.RegisterCommands(ctx); err != nil {
		return fmt.Errorf("register commands: %w", err)
	}

	if o.HealthAddr != "" {
		go serveHealth(ctx, o.HealthAddr, h)
	}
	go pingLoop(ctx, c, h)
	if o.StatusChannel != "" {
		go statusLoop(ctx, c, st, h, o.StatusChannel)
	}

	fmt.Fprintln(os.Stderr, "dctl serve: commands registered; connecting to gateway…")

	// Reconnect loop: a dropped connection just re-IDENTIFYs (no resume).
	for ctx.Err() == nil {
		gw := gateway.NewGateway(c, h)
		errCh := make(chan error, 1)
		go func() { errCh <- gw.Run(ctx) }()
	dispatch:
		for {
			select {
			case in := <-gw.Interactions:
				resp := hdl.Handle(ctx, in)
				if err := c.RespondInteraction(ctx, in.ID, in.Token, resp); err != nil {
					fmt.Fprintf(os.Stderr, "respond: %v\n", err)
				}
				h.SetSessions(len(st.SnapshotSessions())) // session count may have changed
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
