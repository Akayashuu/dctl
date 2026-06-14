package serve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vskstudio/dctl"
	"github.com/vskstudio/dctl/internal/forge"
	"github.com/vskstudio/dctl/internal/gateway"
	"github.com/vskstudio/dctl/internal/handler"
	"github.com/vskstudio/dctl/internal/health"
	"github.com/vskstudio/dctl/internal/instanceid"
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
	// InstanceID is the explicit per-daemon namespace (-instance flag /
	// DCTL_INSTANCE_ID). Empty falls back to DCTL_OWNER_ID, then legacy mode.
	InstanceID string
	// Token is the bot token used for the gateway IDENTIFY (same value the
	// client was built with). Sourced from the caller, not read off the client.
	Token string
}

// DefaultStatePath returns the default path to the daemon state file.
func DefaultStatePath() string {
	if d := os.Getenv("DCTL_STATE_DIR"); d != "" {
		return filepath.Join(d, "state.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "dctl", "state.json")
}

// resolveInstanceID computes and freezes the daemon's instanceID, per Spec §2/§8.
//   - An invalid explicit optID is an error.
//   - If the state already carries an id, a different non-empty resolved id is
//     refused (changing it would orphan existing branches/worktrees); a matching
//     or empty resolved id keeps the stored id.
//   - On a fresh state (no id) with a non-empty resolved id and NO sessions, the
//     id is frozen (persisted). If sessions already exist, the daemon stays in
//     legacy (empty) mode so pre-existing sessions are never orphaned.
func resolveInstanceID(st *state.State, optID, ownerID string) (string, error) {
	resolved, err := instanceid.Resolve(optID, ownerID)
	if err != nil {
		return "", err
	}
	if st.InstanceID != "" {
		if resolved != "" && resolved != st.InstanceID {
			return "", fmt.Errorf("instanceID mismatch: state has %q but %q was requested; "+
				"changing it would orphan existing sessions", st.InstanceID, resolved)
		}
		return st.InstanceID, nil
	}
	if resolved == "" {
		return "", nil
	}
	if len(st.SnapshotSessions()) > 0 {
		// Legacy sessions exist; stay non-namespaced so they keep working.
		fmt.Fprintf(os.Stderr, "dctl serve: %d legacy session(s) present; staying in non-namespaced mode\n",
			len(st.SnapshotSessions()))
		return "", nil
	}
	if err := st.SetInstanceID(resolved); err != nil {
		return "", fmt.Errorf("persist instanceID: %w", err)
	}
	return resolved, nil
}

// handleDeferred acks a slow interaction immediately (type 5), runs the handler
// off the dispatch loop, then edits the deferred reply in. On a defer failure it
// falls back to a direct reply so the user is never left without a response.
func handleDeferred(ctx context.Context, c *dctl.Client, hdl *handler.Handler, h *health.Health, st *state.State, appID string, in dctl.Interaction) {
	if err := c.DeferInteraction(ctx, in.ID, in.Token, true); err != nil {
		fmt.Fprintf(os.Stderr, "defer: %v\n", err)
		resp := hdl.Handle(ctx, in)
		if err := c.RespondInteraction(ctx, in.ID, in.Token, resp); err != nil {
			fmt.Fprintf(os.Stderr, "respond: %v\n", err)
		}
		h.SetSessions(len(st.SnapshotSessions()))
		return
	}
	resp := hdl.Handle(ctx, in)
	if err := c.EditInteractionResponse(ctx, appID, in.Token, resp); err != nil {
		fmt.Fprintf(os.Stderr, "edit response: %v\n", err)
	}
	h.SetSessions(len(st.SnapshotSessions()))
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
	partDir := filepath.Dir(o.StatePath) // participants/<name>.log lives beside state.json
	sup := supervisor.NewSupervisor(ctx, self)
	sup.PartDir = partDir
	sup.StatePath = o.StatePath // enables per-session allowlist enforcement in bridge children
	// Restart persisted sessions.
	for _, sess := range st.SnapshotSessions() {
		_ = sup.Start(sess)
	}
	h.SetSessions(len(st.SnapshotSessions()))

	instID, err := resolveInstanceID(st, o.InstanceID, os.Getenv("DCTL_OWNER_ID"))
	if err != nil {
		return fmt.Errorf("resolve instance id: %w", err)
	}
	if instID != "" {
		fmt.Fprintf(os.Stderr, "dctl serve: instance %q\n", instID)
	}

	wt := worktree.NewWorktreer(ctx, instID)
	fg := forge.New()
	hdl := handler.NewHandler(c, sup, wt, fg, st, o.DefaultCmd, partDir)

	if err := c.RegisterCommands(ctx); err != nil {
		return fmt.Errorf("register commands: %w", err)
	}
	// Needed to edit deferred interaction replies (webhook @original).
	appID, err := c.AppID(ctx)
	if err != nil {
		return fmt.Errorf("resolve app id: %w", err)
	}

	if o.HealthAddr != "" {
		go serveHealth(ctx, o.HealthAddr, h)
	}
	go pingLoop(ctx, c, h)
	if o.StatusChannel != "" {
		go statusLoop(ctx, c, st, h, o.StatusChannel, instID)
	}

	fmt.Fprintln(os.Stderr, "dctl serve: commands registered; connecting to gateway…")

	// Reconnect loop: a dropped connection just re-IDENTIFYs (no resume).
	for ctx.Err() == nil {
		gw := gateway.NewGateway(c, o.Token, h)
		errCh := make(chan error, 1)
		go func() { errCh <- gw.Run(ctx) }()
	dispatch:
		for {
			select {
			case in := <-gw.Interactions:
				if in.Type == dctl.InteractionAutocomplete {
					if err := c.RespondAutocomplete(ctx, in.ID, in.Token, hdl.Autocomplete(in)); err != nil {
						fmt.Fprintf(os.Stderr, "autocomplete: %v\n", err)
					}
					continue
				}
				if hdl.Slow(in) {
					// Ack within 3s, then do the slow work and edit the reply in
					// off the dispatch loop so one clone can't stall the daemon.
					go handleDeferred(ctx, c, hdl, h, st, appID, in)
				} else {
					resp := hdl.Handle(ctx, in)
					if err := c.RespondInteraction(ctx, in.ID, in.Token, resp); err != nil {
						fmt.Fprintf(os.Stderr, "respond: %v\n", err)
					}
					h.SetSessions(len(st.SnapshotSessions())) // session count may have changed
				}
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
