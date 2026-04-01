// Command audit-log is the km audit log sidecar.
// It reads JSON-line events from stdin and routes them to the configured destination.
//
// Environment variables:
//
//	AUDIT_LOG_DEST        — destination: "stdout" (default), "cloudwatch", "s3"
//	SANDBOX_ID            — sandbox identifier (e.g. "sb-a1b2c3d4")
//	CW_LOG_GROUP          — CloudWatch log group (default: "/km/sandboxes/<SANDBOX_ID>/")
//	AWS_REGION            — AWS region for CloudWatch (default: "us-east-1")
//	IDLE_TIMEOUT_MINUTES  — if set and dest is cloudwatch, starts idle detection goroutine
//
// On EC2: piped from shell audit hook (PROMPT_COMMAND in /etc/profile.d/km-audit.sh)
// and receives events from dns-proxy and http-proxy via systemd journal pipe.
// On ECS: awslogs driver captures stdout of the audit-log container.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
	lifecycle "github.com/whereiskurt/klankrmkr/pkg/lifecycle"
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

	// Pre-create CW client when dest is cloudwatch so it can be shared with
	// both the destination and the idle detector.
	var cwClient kmaws.CWLogsAPI
	if destName == "cloudwatch" {
		var err error
		cwClient, err = newCWClient(ctx, envOr("AWS_REGION", "us-east-1"))
		if err != nil {
			log.Fatal().Err(err).Msg("audit-log: failed to create CloudWatch client")
		}
	}

	dest, err := buildDest(ctx, destName, cwLogGroup, cwClient)
	if err != nil {
		log.Fatal().Err(err).Msg("audit-log: failed to initialize destination")
	}

	// Wire IdleDetector when IDLE_TIMEOUT_MINUTES is set and dest is cloudwatch.
	// Create an EventBridge client for publishing SandboxIdle events on idle timeout.
	idleTimeoutStr := envOr("IDLE_TIMEOUT_MINUTES", "")
	var ebClient kmaws.EventBridgeAPI
	if idleTimeoutStr != "" {
		awsCfg, ebErr := config.LoadDefaultConfig(ctx, config.WithRegion(envOr("AWS_REGION", "us-east-1")))
		if ebErr != nil {
			log.Warn().Err(ebErr).Msg("audit-log: failed to create EventBridge client; idle destroy disabled")
		} else {
			ebClient = eventbridge.NewFromConfig(awsCfg)
		}
	}

	if idleTimeoutStr != "" && destName == "cloudwatch" {
		idleMinutes, parseErr := strconv.Atoi(idleTimeoutStr)
		if parseErr != nil {
			log.Warn().Str("IDLE_TIMEOUT_MINUTES", idleTimeoutStr).Err(parseErr).
				Msg("audit-log: invalid IDLE_TIMEOUT_MINUTES, idle detection disabled")
		} else {
			detector := newIdleDetector(sandboxID, idleMinutes, cwClient, cwLogGroup, "audit", func(id string) {
				log.Warn().Str("sandbox_id", id).Msg("audit-log: sandbox idle timeout reached, publishing idle event")
				if ebClient != nil {
					if err := kmaws.PublishSandboxIdleEvent(ctx, ebClient, id); err != nil {
						log.Error().Err(err).Str("sandbox_id", id).Msg("audit-log: failed to publish idle event")
					}
				}
				cancel()
			})
			go func() {
				if runErr := detector.Run(ctx); runErr != nil && runErr != context.Canceled {
					log.Error().Err(runErr).Msg("audit-log: idle detector error")
				}
			}()
			log.Info().Int("idle_timeout_minutes", idleMinutes).Msg("audit-log: idle detector started")
		}
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

	// Periodic flush: send buffered events to CloudWatch every 30 seconds
	// so events appear promptly even with low-volume activity.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if flushErr := dest.Flush(ctx); flushErr != nil {
					log.Error().Err(flushErr).Msg("audit-log: periodic flush failed")
				}
			}
		}
	}()

	// Open audit pipe (FIFO) if KM_AUDIT_PIPE is set; otherwise read from stdin.
	// Opening the FIFO read-write (O_RDWR) prevents blocking — the same fd acts as
	// both reader and writer, so the open succeeds immediately without waiting for
	// an external writer. External writers open the FIFO normally for writing.
	var inputReader io.Reader = os.Stdin
	if pipePath := envOr("KM_AUDIT_PIPE", ""); pipePath != "" {
		f, openErr := os.OpenFile(pipePath, os.O_RDWR, 0)
		if openErr != nil {
			log.Error().Err(openErr).Str("pipe", pipePath).Msg("audit-log: failed to open audit pipe")
		} else {
			inputReader = f
			log.Info().Str("pipe", pipePath).Msg("audit-log: reading from audit pipe")
		}
	}

	// Process input in a goroutine so the idle detector can keep running
	// even when input is closed or EOF.
	stdinDone := make(chan struct{})
	go func() {
		defer close(stdinDone)
		if err := auditlog.Process(ctx, inputReader, dest); err != nil {
			log.Error().Err(err).Msg("audit-log: process error")
		}
	}()

	// If idle timeout is configured, block on context (idle detector keeps running).
	// Otherwise, block on stdin EOF.
	if idleTimeoutStr != "" {
		log.Info().Msg("audit-log: idle detection active — staying alive after stdin EOF")
		<-ctx.Done()
	} else {
		<-stdinDone
	}

	if err := dest.Flush(ctx); err != nil {
		log.Error().Err(err).Msg("audit-log: final flush failed")
	}

	log.Info().Msg("audit-log: exiting")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// newCWClient constructs an AWS CloudWatch Logs client for the given region.
func newCWClient(ctx context.Context, region string) (kmaws.CWLogsAPI, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return cloudwatchlogs.NewFromConfig(cfg), nil
}

// buildDest constructs the configured destination and wraps it with a
// RedactingDestination so that secret patterns are scrubbed before any
// output reaches CloudWatch, S3, or stdout.
//
// cwClient is the pre-created CloudWatch Logs client; it is nil for non-cloudwatch dests.
func buildDest(ctx context.Context, destName, cwLogGroup string, cwClient kmaws.CWLogsAPI) (auditlog.Destination, error) {
	var inner auditlog.Destination

	switch destName {
	case "cloudwatch":
		backend, err := newRealCWBackend(ctx, cwClient, cwLogGroup, "audit")
		if err != nil {
			return nil, fmt.Errorf("build cloudwatch dest: %w", err)
		}
		inner = auditlog.NewCloudWatchDest(backend, cwLogGroup, "audit")

	case "s3":
		bucket := envOr("KM_ARTIFACTS_BUCKET", "")
		sandboxID := envOr("SANDBOX_ID", "unknown")
		if bucket == "" {
			log.Warn().Msg("audit-log: KM_ARTIFACTS_BUCKET not set, S3 dest falling back to stdout")
			inner = auditlog.NewStdoutDest(os.Stdout)
		} else {
			s3Backend, s3Err := newS3Backend(ctx, envOr("AWS_REGION", "us-east-1"))
			if s3Err != nil {
				log.Warn().Err(s3Err).Msg("audit-log: failed to create S3 client, falling back to stdout")
				inner = auditlog.NewStdoutDest(os.Stdout)
			} else {
				inner = auditlog.NewS3Dest(s3Backend, bucket, sandboxID, os.Stdout)
			}
		}

	default: // "stdout" or anything else
		inner = auditlog.NewStdoutDest(os.Stdout)
	}

	// Wrap every destination with RedactingDestination — this ensures secret
	// patterns (AWS keys, Bearer tokens, long hex strings) are scrubbed before
	// reaching CloudWatch, S3, or stdout. Pass nil literals; regex patterns cover
	// the standard secret formats. OBSV-07 requirement.
	return auditlog.NewRedactingDestination(inner, nil), nil
}

// newIdleDetector constructs a lifecycle.IdleDetector for the given sandbox.
// idleMinutes is the number of minutes of inactivity before OnIdle fires.
func newIdleDetector(sandboxID string, idleMinutes int, cwClient kmaws.CWLogsAPI, logGroup, logStream string, onIdle func(string)) *lifecycle.IdleDetector {
	return &lifecycle.IdleDetector{
		SandboxID:   sandboxID,
		IdleTimeout: time.Duration(idleMinutes) * time.Minute,
		CWClient:    cwClient,
		LogGroup:    logGroup,
		LogStream:   logStream,
		OnIdle:      onIdle,
	}
}

// realCWBackend adapts kmaws.CWLogsAPI to the auditlog.CloudWatchBackend interface.
type realCWBackend struct {
	client kmaws.CWLogsAPI
}

// newRealCWBackend uses a pre-created CW client (passed in from main) to avoid
// creating a second AWS session. It calls EnsureLogGroup to create the log group
// and stream if they do not exist.
func newRealCWBackend(ctx context.Context, client kmaws.CWLogsAPI, logGroup, logStream string) (*realCWBackend, error) {
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

// ---- S3 Backend ----

// realS3Backend adapts *s3.Client to the auditlog.S3Backend interface.
type realS3Backend struct {
	client *s3.Client
}

func newS3Backend(ctx context.Context, region string) (*realS3Backend, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config for S3: %w", err)
	}
	return &realS3Backend{client: s3.NewFromConfig(cfg)}, nil
}

func (b *realS3Backend) PutObject(ctx context.Context, bucket, key string, data []byte) error {
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      awssdk.String(bucket),
		Key:         awssdk.String(key),
		Body:        bytes.NewReader(data),
		ContentType: awssdk.String("application/x-ndjson"),
	})
	return err
}
