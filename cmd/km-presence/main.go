// km-presence is the Phase 79 sandbox-side liveness daemon.
// It replaces the per-shell bash _km_heartbeat function with a single
// systemd-managed service that ticks every 60 seconds and emits a heartbeat
// event into /run/km/audit-pipe if any of five concrete signals is active.
//
// See docs/superpowers/specs/2026-05-10-km-presence-daemon-design.md for design.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	tickInterval      = 60 * time.Second
	defaultMailDir    = "/var/mail/km/new"
	defaultSlackStamp = "/run/km/last-slack-inbound"
	defaultPresStamp  = "/run/km/.presence-last-tick"
)

func main() {
	os.Exit(run())
}

// run is the testable entrypoint for the daemon. Returns 0 on clean shutdown,
// 1 on fatal startup error (e.g. SANDBOX_ID not set).
func run() int {
	// Match the zerolog initialization style used in sidecars/audit-log/cmd/main.go.
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, NoColor: true})

	sandboxID := os.Getenv("SANDBOX_ID")
	if sandboxID == "" {
		log.Error().Msg("SANDBOX_ID env var not set; refusing to start")
		return 1
	}

	log.Info().
		Str("sandbox_id", sandboxID).
		Dur("tick_interval", tickInterval).
		Msg("km-presence daemon starting")

	runner := realRunner{}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Run an immediate tick at startup so the first heartbeat does not lag 60s.
	tickNum := 0
	runOneTick := func() {
		tickNum++
		active, emitted := tick(runner, sandboxID, defaultMailDir, defaultSlackStamp, defaultPresStamp)
		log.Info().
			Int("tick", tickNum).
			Bool("active", active).
			Bool("emitted", emitted).
			Msg("presence tick complete")
	}
	runOneTick()

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("shutdown signal received; exiting")
			return 0
		case <-ticker.C:
			runOneTick()
		}
	}
}
