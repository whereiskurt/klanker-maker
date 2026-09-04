// km-presence is the Phase 79 sandbox-side liveness daemon.
// It replaces the per-shell bash _km_heartbeat function with a single
// systemd-managed service that ticks every 60 seconds and emits a heartbeat
// event into /run/km/audit-pipe if any of eight concrete signals is active.
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
	tickInterval          = 60 * time.Second
	defaultMailDir        = "/var/mail/km/new"
	defaultSlackStamp     = "/run/km/last-slack-inbound"
	defaultPresStamp      = "/run/km/.presence-last-tick"
	defaultHerdrConfigDir = "/home/sandbox/.config/herdr"
)

func main() {
	os.Exit(run())
}

// herdrConfigDir returns the directory signal 8 probes for Herdr sockets.
// Hardcoded to Herdr's default location, because userdata never sets
// XDG_CONFIG_HOME for the sandbox user today — but if it ever does, or the
// config dir is relocated some other way, signal 8 goes permanently negative
// with NO diagnostic: herdrSocketPaths finds nothing, checkHerdrPaneBusy
// returns false forever, and the negative-case tests (which construct their
// own config dir) cannot catch a real-world path that no longer matches this
// constant. KM_HERDR_CONFIG_DIR is the escape hatch for that day, wired here
// rather than left as a pure comment since it costs one line and one test.
func herdrConfigDir() string {
	if v := os.Getenv("KM_HERDR_CONFIG_DIR"); v != "" {
		return v
	}
	return defaultHerdrConfigDir
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
		active, emitted := tick(runner, sandboxID, defaultMailDir, defaultSlackStamp, defaultPresStamp, herdrConfigDir())
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
