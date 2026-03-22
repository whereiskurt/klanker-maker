// Command audit-log is the km audit log sidecar.
// It reads JSON-line events from stdin and routes them to the configured destination.
//
// Environment variables:
//
//	AUDIT_LOG_DEST   — destination: "stdout" (default), "cloudwatch", "s3"
//	SANDBOX_ID       — sandbox identifier (e.g. "sb-a1b2c3d4")
//	CW_LOG_GROUP     — CloudWatch log group (default: "/km/sandboxes/<SANDBOX_ID>/")
//	AWS_REGION       — AWS region for CloudWatch (default: "us-east-1")
//
// On EC2: piped from shell audit hook (PROMPT_COMMAND in /etc/profile.d/km-audit.sh)
// and receives events from dns-proxy and http-proxy via systemd journal pipe.
// On ECS: awslogs driver captures stdout of the audit-log container.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	auditlog "github.com/whereiskurt/klankrmkr/sidecars/audit-log"
)

func main() {
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	destName := envOr("AUDIT_LOG_DEST", "stdout")
	sandboxID := envOr("SANDBOX_ID", "unknown")
	cwLogGroup := envOr("CW_LOG_GROUP", fmt.Sprintf("/km/sandboxes/%s/", sandboxID))

	log.Info().
		Str("dest", destName).
		Str("sandbox_id", sandboxID).
		Str("cw_log_group", cwLogGroup).
		Msg("audit-log: starting")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dest, err := buildDest(ctx, destName, cwLogGroup)
	if err != nil {
		log.Fatal().Err(err).Msg("audit-log: failed to initialize destination")
	}

	// Handle SIGTERM/SIGINT: flush and exit cleanly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		log.Info().Str("signal", sig.String()).Msg("audit-log: received signal, flushing and exiting")
		if flushErr := dest.Flush(ctx); flushErr != nil {
			log.Error().Err(flushErr).Msg("audit-log: flush on shutdown failed")
		}
		cancel()
		os.Exit(0)
	}()

	if err := auditlog.Process(ctx, os.Stdin, dest); err != nil {
		log.Error().Err(err).Msg("audit-log: process error")
	}

	if err := dest.Flush(ctx); err != nil {
		log.Error().Err(err).Msg("audit-log: final flush failed")
	}

	log.Info().Msg("audit-log: stdin closed, exiting")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// buildDest constructs the configured destination. For cloudwatch, it uses
// the real AWS SDK CloudWatch Logs client via pkg/aws helpers.
func buildDest(ctx context.Context, destName, cwLogGroup string) (auditlog.Destination, error) {
	switch destName {
	case "cloudwatch":
		region := envOr("AWS_REGION", "us-east-1")
		backend, err := newRealCWBackend(ctx, region, cwLogGroup, "audit")
		if err != nil {
			return nil, fmt.Errorf("build cloudwatch dest: %w", err)
		}
		return auditlog.NewCloudWatchDest(backend, cwLogGroup, "audit"), nil

	case "s3":
		return auditlog.NewS3Dest(os.Stdout), nil

	default: // "stdout" or anything else
		return auditlog.NewStdoutDest(os.Stdout), nil
	}
}

// realCWBackend adapts kmaws.CWLogsAPI to the auditlog.CloudWatchBackend interface.
type realCWBackend struct {
	client kmaws.CWLogsAPI
}

func newRealCWBackend(ctx context.Context, region, logGroup, logStream string) (*realCWBackend, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	client := cloudwatchlogs.NewFromConfig(cfg)
	backend := &realCWBackend{client: client}
	if err := backend.EnsureLogGroup(ctx, logGroup, logStream); err != nil {
		return nil, fmt.Errorf("ensure log group: %w", err)
	}
	return backend, nil
}

// EnsureLogGroup delegates to pkg/aws EnsureLogGroup.
func (b *realCWBackend) EnsureLogGroup(ctx context.Context, logGroup, logStream string) error {
	return kmaws.EnsureLogGroup(ctx, b.client, logGroup, logStream)
}

// PutLogMessages converts string messages to LogEvents and calls pkg/aws PutLogEvents.
func (b *realCWBackend) PutLogMessages(ctx context.Context, logGroup, logStream string, messages []string) error {
	nowMs := time.Now().UnixMilli()
	events := make([]kmaws.LogEvent, 0, len(messages))
	for _, msg := range messages {
		events = append(events, kmaws.LogEvent{
			Timestamp: nowMs,
			Message:   msg,
		})
	}
	return kmaws.PutLogEvents(ctx, b.client, logGroup, logStream, events)
}
