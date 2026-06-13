package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/vskstudio/dctl"
)

const healthWindow = 90 * time.Second

// serveHealth runs a tiny HTTP server exposing GET /health (200 online / 503 down).
func serveHealth(ctx context.Context, addr string, h *dctl.Health) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		snap := h.Snapshot(time.Now(), healthWindow)
		w.Header().Set("Content-Type", "application/json")
		if !snap.Online {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(snap)
	})
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	if err := srv.ListenAndServe(); err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "health server: %v\n", err)
	}
}

// pingLoop records an independent REST reachability latency every 30s.
func pingLoop(ctx context.Context, c *dctl.Client, h *dctl.Health) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			start := time.Now()
			if _, err := c.AppID(ctx); err == nil {
				h.Ping(time.Now(), time.Since(start).Milliseconds())
			}
		}
	}
}

// statusLoop maintains a single self-updating status embed in channelID.
func statusLoop(ctx context.Context, c *dctl.Client, st *dctl.State, h *dctl.Health, channelID string) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	render := func() {
		snap := h.Snapshot(time.Now(), healthWindow)
		dot, word := "🟢", "online"
		if !snap.Online {
			dot, word = "🔴", "offline"
		}
		uptime := (time.Duration(snap.UptimeS) * time.Second).String()
		content := fmt.Sprintf("%s **dctl %s** · uptime %s · ping %dms · %d sessions",
			dot, word, uptime, snap.PingMS, snap.Sessions)
		id, err := c.UpsertStatusMessage(ctx, channelID, st.StatusMessageID, content)
		if err == nil && id != st.StatusMessageID {
			_ = st.SetStatusMessageID(id)
		}
	}
	render()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			render()
		}
	}
}
