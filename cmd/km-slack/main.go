// Command km-slack is the sandbox-side Phase 63 / Phase 68 Slack-notify client.
// It signs an envelope with the sandbox's Ed25519 key (loaded from SSM
// /sandbox/{id}/signing-key) and POSTs it to the km-slack-bridge Lambda
// Function URL ($KM_SLACK_BRIDGE_URL). It is invoked by /opt/km/bin/km-notify-hook
// when KM_NOTIFY_SLACK_ENABLED=1 — see pkg/compiler/userdata.go.
//
// Subcommands:
//
//	km-slack post           --channel C... --body /tmp/body.txt [--subject ...] [--thread ts]
//	km-slack upload         --channel C... --thread ts --s3-key transcripts/sb-x/y --filename name.gz \
//	                        --content-type application/gzip --size-bytes 12345
//	km-slack record-mapping --channel C... --slack-ts 1.2 --offset 1024 --session sid
//
// Required env (post + upload): KM_SANDBOX_ID, KM_SLACK_BRIDGE_URL, AWS_REGION
// (or AWS_DEFAULT_REGION).
// Required env (record-mapping):  KM_SANDBOX_ID, KM_SLACK_STREAM_TABLE.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/whereiskurt/klankrmkr/pkg/slack"
)

const defaultTimeout = 30 * time.Second

func main() {
	os.Exit(dispatch(os.Args[1:], os.Stderr))
}

// dispatch routes a subcommand argument vector (without the program name) to
// the matching runX implementation. Extracted from main() so tests can drive
// the dispatch table without manipulating os.Args.
func dispatch(args []string, stderr io.Writer) int {
	if len(args) < 1 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "post":
		return runPost(args[1:], stderr)
	case "upload":
		return runUpload(args[1:], stderr)
	case "record-mapping":
		return runRecordMapping(args[1:], stderr)
	case "-h", "--help", "help":
		usage(stderr)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown subcommand: %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: km-slack <subcommand> [args]
Subcommands:
  post           Post a message to a channel/thread (signs and POSTs to bridge).
  upload         Upload a file via the bridge (3-step flow), referencing an S3 key.
  record-mapping Write a (channel_id, slack_ts) → transcript-offset row to DDB.`)
}

// runPost is the Phase 63 post subcommand entry point. Returns a process exit
// code (0 success, non-zero failure) so dispatch() can surface it.
func runPost(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("post", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var channel, subject, bodyPath, thread string
	fs.StringVar(&channel, "channel", "", "Slack channel ID (C...)")
	fs.StringVar(&subject, "subject", "", "Optional subject text (rendered as bold header by bridge; omit for clean threaded replies)")
	fs.StringVar(&bodyPath, "body", "", "Path to body file (stdin '-' NOT supported)")
	fs.StringVar(&thread, "thread", "", "Thread parent ts")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if channel == "" || bodyPath == "" {
		fmt.Fprintln(stderr, "km-slack post: --channel and --body are required")
		return 2
	}
	if bodyPath == "-" {
		fmt.Fprintln(stderr, "km-slack post: stdin not supported (use a file path); see CLAUDE.md OpenSSL 3.5+ constraint")
		return 1
	}

	if err := run(channel, subject, bodyPath, thread); err != nil {
		fmt.Fprintf(stderr, "km-slack post: %v\n", err)
		return 1
	}
	return 0
}

// runUpload signs an ActionUpload envelope and POSTs it to the bridge. The
// bridge does the actual Slack 3-step file upload using the S3 key.
func runUpload(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("upload", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		channel     = fs.String("channel", "", "Slack channel ID (C...) — required")
		thread      = fs.String("thread", "", "Thread timestamp (parent ts)")
		s3Key       = fs.String("s3-key", "", "S3 key (transcripts/{sandbox_id}/...) — required")
		filename    = fs.String("filename", "", "Filename for Slack — required")
		contentType = fs.String("content-type", "", "MIME type (application/gzip|application/json|text/plain) — required")
		sizeBytes   = fs.Int64("size-bytes", 0, "Size in bytes (must equal actual S3 object size) — required")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *channel == "" || *s3Key == "" || *filename == "" || *contentType == "" || *sizeBytes <= 0 {
		fmt.Fprintln(stderr, "km-slack upload: missing required flags (--channel --s3-key --filename --content-type --size-bytes)")
		return 2
	}
	sandboxID := os.Getenv("KM_SANDBOX_ID")
	bridgeURL := os.Getenv("KM_SLACK_BRIDGE_URL")
	if sandboxID == "" || bridgeURL == "" {
		fmt.Fprintln(stderr, "km-slack upload: KM_SANDBOX_ID and KM_SLACK_BRIDGE_URL required")
		return 2
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		fmt.Fprintln(stderr, "km-slack upload: AWS_REGION (or AWS_DEFAULT_REGION) not set")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	priv, err := loadPrivateKey(ctx, region, sandboxID)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack upload: load signing key: %v\n", err)
		return 1
	}

	env, err := slack.BuildEnvelopeUpload(sandboxID, *channel, *thread, *s3Key, *filename, *contentType, *sizeBytes)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack upload: build envelope: %v\n", err)
		return 1
	}

	_, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack upload: sign: %v\n", err)
		return 1
	}

	resp, err := slack.PostToBridge(ctx, bridgeURL, env, sig)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack upload: bridge POST: %v\n", err)
		return 1
	}
	if !resp.OK {
		fmt.Fprintf(stderr, "km-slack upload: bridge returned not-ok: %s\n", resp.Error)
		return 1
	}
	fmt.Fprintf(stderr, "km-slack upload: ok ts=%s\n", resp.TS)
	return 0
}

// runRecordMapping writes a (channel_id, slack_ts) → transcript-offset row
// directly to DynamoDB using the sandbox's IAM PutItem permission. Called by
// the hook script after a successful km-slack post so a future Phase B
// reaction-handler can resolve a Slack message back to its transcript byte
// offset.
func runRecordMapping(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("record-mapping", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		channel = fs.String("channel", "", "Slack channel ID (PK) — required")
		slackTS = fs.String("slack-ts", "", "Slack message ts (SK) — required")
		offset  = fs.Int64("offset", -1, "Transcript byte offset at time of post — required")
		session = fs.String("session", "", "Claude session_id — required")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *channel == "" || *slackTS == "" || *offset < 0 || *session == "" {
		fmt.Fprintln(stderr, "km-slack record-mapping: missing required flags (--channel --slack-ts --offset --session)")
		return 2
	}
	sandboxID := os.Getenv("KM_SANDBOX_ID")
	table := os.Getenv("KM_SLACK_STREAM_TABLE")
	if sandboxID == "" || table == "" {
		fmt.Fprintln(stderr, "km-slack record-mapping: KM_SANDBOX_ID and KM_SLACK_STREAM_TABLE required")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack record-mapping: aws config: %v\n", err)
		return 1
	}
	ddb := dynamodb.NewFromConfig(cfg)

	ttlExpiry := time.Now().Add(30 * 24 * time.Hour).Unix()

	_, err = ddb.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(table),
		Item: map[string]ddbtypes.AttributeValue{
			"channel_id":        &ddbtypes.AttributeValueMemberS{Value: *channel},
			"slack_ts":          &ddbtypes.AttributeValueMemberS{Value: *slackTS},
			"sandbox_id":        &ddbtypes.AttributeValueMemberS{Value: sandboxID},
			"session_id":        &ddbtypes.AttributeValueMemberS{Value: *session},
			"transcript_offset": &ddbtypes.AttributeValueMemberN{Value: strconv.FormatInt(*offset, 10)},
			"ttl_expiry":        &ddbtypes.AttributeValueMemberN{Value: strconv.FormatInt(ttlExpiry, 10)},
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "km-slack record-mapping: PutItem: %v\n", err)
		return 1
	}
	return 0
}

// run is the outer entry point that loads env vars and the SSM key before
// calling runWith. Separated so tests can inject an ephemeral key via runWith.
func run(channel, subject, bodyPath, thread string) error {
	sandboxID := os.Getenv("KM_SANDBOX_ID")
	if sandboxID == "" {
		return errors.New("KM_SANDBOX_ID env var not set")
	}
	bridgeURL := os.Getenv("KM_SLACK_BRIDGE_URL")
	if bridgeURL == "" {
		return errors.New("KM_SLACK_BRIDGE_URL env var not set")
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		return errors.New("AWS_REGION (or AWS_DEFAULT_REGION) not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	// Cancel on SIGTERM/SIGINT so a teardown signal cuts retries cleanly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	priv, err := loadPrivateKey(ctx, region, sandboxID)
	if err != nil {
		return fmt.Errorf("load signing key: %w", err)
	}

	return runWith(ctx, priv, sandboxID, bridgeURL, channel, subject, bodyPath, thread)
}

// runWith is the testable inner entry point. Tests inject an ephemeral key and
// stub bridge server URL directly, bypassing SSM entirely.
func runWith(ctx context.Context, priv ed25519.PrivateKey, sandboxID, bridgeURL, channel, subject, bodyPath, thread string) error {
	if sandboxID == "" {
		return errors.New("sandboxID is required")
	}
	if bridgeURL == "" {
		return errors.New("bridgeURL is required")
	}

	body, err := os.ReadFile(bodyPath)
	if err != nil {
		return fmt.Errorf("read body file: %w", err)
	}
	if len(body) > slack.MaxBodyBytes {
		return fmt.Errorf("body file %s exceeds %d bytes (40KB Slack limit)", bodyPath, slack.MaxBodyBytes)
	}

	env, err := slack.BuildEnvelope(slack.ActionPost, sandboxID, channel, subject, string(body), thread)
	if err != nil {
		return err
	}

	_, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		return err
	}

	resp, err := slack.PostToBridge(ctx, bridgeURL, env, sig)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("bridge returned not-ok: %s", resp.Error)
	}
	fmt.Fprintf(os.Stderr, "km-slack: posted ts=%s\n", resp.TS)
	return nil
}

// loadPrivateKey fetches /sandbox/{sandboxID}/signing-key from SSM (decrypted),
// base64-decodes, returns an ed25519.PrivateKey using the first 32 bytes as seed.
func loadPrivateKey(ctx context.Context, region, sandboxID string) (ed25519.PrivateKey, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	client := ssm.NewFromConfig(cfg)
	keyPath := fmt.Sprintf("/sandbox/%s/signing-key", sandboxID)
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(keyPath),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return nil, fmt.Errorf("ssm GetParameter %s: %w", keyPath, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return nil, fmt.Errorf("ssm parameter %s missing value", keyPath)
	}
	raw, err := base64.StdEncoding.DecodeString(*out.Parameter.Value)
	if err != nil {
		return nil, fmt.Errorf("decode key: %w", err)
	}
	if len(raw) < 32 {
		return nil, fmt.Errorf("key too short: %d bytes", len(raw))
	}
	return ed25519.NewKeyFromSeed(raw[:32]), nil
}
