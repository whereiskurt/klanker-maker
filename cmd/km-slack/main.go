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
//	km-slack reply          [--session id] [--thread ts [--channel C...]] [--body /file] [--render plain|mrkdwn|blocks]
//
// Required env (post + upload): KM_SANDBOX_ID, KM_SLACK_BRIDGE_URL, AWS_REGION
// (or AWS_DEFAULT_REGION).
// Required env (record-mapping):  KM_SANDBOX_ID, KM_SLACK_STREAM_TABLE.
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
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
	case "permalink":
		return runPermalink(args[1:], stderr)
	case "update":
		return runUpdate(args[1:], stderr)
	case "reply":
		return runReply(args[1:], stderr)
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
  post           Post a message to a channel/thread (signs and POSTs to bridge). --new-message omits thread_ts.
  upload         Upload a file via the bridge (3-step flow), referencing an S3 key.
  record-mapping Write a (channel_id, slack_ts) → transcript-offset row to DDB.
  permalink      Resolve a Slack permalink URL for --channel + --ts.
  update         Edit a previously-posted bot message via --channel, --ts, and --text/--body.
  reply          Post a reply into the thread bound to the current session (4-step resolution chain).
                 Resolution: --thread > $KM_SLACK_THREAD_TS > session-id lookup > channel root.`)
}

// runPost is the Phase 63 post subcommand entry point. Returns a process exit
// code (0 success, non-zero failure) so dispatch() can surface it.
//
// Phase 74 adds --render=plain|mrkdwn|blocks (default plain). An explicit
// flag beats the KM_SLACK_RENDER environment variable. Unknown values fall
// back to plain with a stderr warning. "blocks" is accepted by the flag but
// treated as plain until Plan 74-02 (PR2) lands.
func runPost(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("post", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var channel, subject, bodyPath, thread string
	var renderMode string
	var newMessage bool
	fs.StringVar(&channel, "channel", "", "Slack channel ID (C...)")
	fs.StringVar(&subject, "subject", "", "Optional subject text (rendered as bold header by bridge; omit for clean threaded replies)")
	fs.StringVar(&bodyPath, "body", "", "Path to body file (stdin '-' NOT supported)")
	fs.StringVar(&thread, "thread", "", "Thread parent ts")
	fs.StringVar(&renderMode, "render", "", "Render mode: plain (default, no-op), mrkdwn (Phase 74 Tier 1 transformer), blocks (Phase 74 PR2 Tier 2 Block Kit; falls back to mrkdwn on 50-block cap), blocks-rich (Phase 111 Tier 3 markdown/table blocks, opt-in; falls back to blocks then mrkdwn)")
	fs.BoolVar(&newMessage, "new-message", false, "Post as new top-level message (omits thread_ts); prints ts=<value> to stdout for poller capture. Phase 70.")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// Resolve render mode precedence: explicit flag > KM_SLACK_RENDER env > "plain".
	if renderMode == "" {
		renderMode = os.Getenv("KM_SLACK_RENDER")
	}
	if renderMode == "" {
		renderMode = "plain"
	}
	switch renderMode {
	case "plain", "mrkdwn", "blocks", "blocks-rich":
		// valid
	default:
		fmt.Fprintf(stderr, "km-slack post: unknown --render value %q; falling back to plain\n", renderMode)
		renderMode = "plain"
	}

	if channel == "" || bodyPath == "" {
		fmt.Fprintln(stderr, "km-slack post: --channel and --body are required")
		return 2
	}
	if bodyPath == "-" {
		fmt.Fprintln(stderr, "km-slack post: stdin not supported (use a file path); see CLAUDE.md OpenSSL 3.5+ constraint")
		return 1
	}

	// Phase 70: --new-message forces thread="" (new top-level) and prints ts to stdout.
	threadArg := thread
	if newMessage {
		threadArg = ""
	}

	ts, err := run(channel, subject, bodyPath, threadArg, renderMode)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack post: %v\n", err)
		return 1
	}
	if newMessage {
		fmt.Printf("ts=%s\n", ts) // STDOUT — poller captures with grep/sed
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
// renderMode is one of "plain", "mrkdwn", "blocks" — resolved by runPost before calling run.
// Returns the message ts on success (empty string if the bridge didn't return one).
func run(channel, subject, bodyPath, thread, renderMode string) (string, error) {
	sandboxID := os.Getenv("KM_SANDBOX_ID")
	if sandboxID == "" {
		return "", errors.New("KM_SANDBOX_ID env var not set")
	}
	bridgeURL := os.Getenv("KM_SLACK_BRIDGE_URL")
	if bridgeURL == "" {
		return "", errors.New("KM_SLACK_BRIDGE_URL env var not set")
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		return "", errors.New("AWS_REGION (or AWS_DEFAULT_REGION) not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()

	// Cancel on SIGTERM/SIGINT so a teardown signal cuts retries cleanly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	priv, err := loadPrivateKey(ctx, region, sandboxID)
	if err != nil {
		return "", fmt.Errorf("load signing key: %w", err)
	}

	return runWith(ctx, priv, sandboxID, bridgeURL, channel, subject, bodyPath, thread, renderMode)
}

// runWith is the testable inner entry point. Tests inject an ephemeral key and
// stub bridge server URL directly, bypassing SSM entirely.
//
// renderMode controls Phase 74 rendering:
//   - "plain" (default): body is passed through unchanged. Existing Phase 62/63/68
//     callers omit --render and land here — no behavior change.
//   - "mrkdwn": body is run through slack.Mrkdwnify before envelope construction.
//   - "blocks": Phase 74 PR2 Tier 2. Calls slack.RenderBlocks; on ok==true sets
//     env.Blocks to the Block Kit JSON array and uses the plain-text fallback as
//     the body. On ok==false (50-block cap or panic), falls back to Mrkdwnify.
//
// Overflow: if the rendered body exceeds slack.MaxRenderedBytes (35KB), it is
// hard-truncated and a footer is appended. The existing MaxBodyBytes (40KB) check
// remains as defense-in-depth AFTER the overflow truncation.
// runWith is the testable inner entry point. Tests inject an ephemeral key and
// stub bridge server URL directly, bypassing SSM entirely.
//
// Returns the message ts from the bridge 200 response on success (empty string
// if the bridge didn't include one). Phase 70 made this the return value so
// runPost --new-message can print ts= to stdout for poller capture.
func runWith(ctx context.Context, priv ed25519.PrivateKey, sandboxID, bridgeURL, channel, subject, bodyPath, thread, renderMode string) (string, error) {
	if sandboxID == "" {
		return "", errors.New("sandboxID is required")
	}
	if bridgeURL == "" {
		return "", errors.New("bridgeURL is required")
	}

	body, err := os.ReadFile(bodyPath)
	if err != nil {
		return "", fmt.Errorf("read body file: %w", err)
	}

	// Phase 74: apply renderer then check overflow before building envelope.
	var rendered string
	var blocksJSON string
	switch renderMode {
	case "blocks-rich":
		// Tier 3: attempt RenderRich (markdown + table blocks, GFM-verbatim prose).
		// KM_SLACK_AI_FOOTER=true appends a trailing AI-disclaimer context block.
		// On ok==false (12K cap, 50-block cap, or panic), degrade to Tier 2 (RenderBlocks).
		// If Tier 2 also returns ok==false, degrade to Tier 1 (Mrkdwnify).
		aiFooter := os.Getenv("KM_SLACK_AI_FOOTER") == "true"
		bj, fallback, okRR := slack.RenderRich(string(body), aiFooter)
		if okRR {
			rendered = fallback
			blocksJSON = bj
		} else {
			// Tier 2 fallback.
			bj2, fallback2, okBK := slack.RenderBlocks(string(body))
			if okBK {
				rendered = fallback2
				blocksJSON = bj2
			} else {
				// Tier 1 final fallback.
				rendered = slack.Mrkdwnify(string(body))
			}
		}
	case "blocks":
		// Tier 2: attempt Block Kit rendering. On ok==false (50-block cap or
		// panic), degrade to Mrkdwnify (Tier 1). The fallback path is indistinguishable
		// from an explicit --render=mrkdwn call for the bridge.
		bj, fallback, okBK := slack.RenderBlocks(string(body))
		if okBK {
			rendered = fallback
			blocksJSON = bj
		} else {
			rendered = slack.Mrkdwnify(string(body))
		}
	case "mrkdwn":
		rendered = slack.Mrkdwnify(string(body))
	default: // "plain" and any unknown value (already normalised in runPost)
		rendered = string(body)
	}
	if len(rendered) > slack.MaxRenderedBytes {
		rendered = rendered[:slack.MaxRenderedBytes] +
			"\n_…truncated; see full transcript at Stop_"
	}

	// Defense-in-depth: the 40KB hard cap still applies post-render.
	if len(rendered) > slack.MaxBodyBytes {
		return "", fmt.Errorf("rendered body exceeds %d bytes (40KB Slack limit)", slack.MaxBodyBytes)
	}

	env, err := slack.BuildEnvelope(slack.ActionPost, sandboxID, channel, subject, rendered, thread)
	if err != nil {
		return "", err
	}
	// Tier 2: populate the Blocks field if RenderBlocks succeeded.
	if blocksJSON != "" {
		env.Blocks = blocksJSON
	}

	_, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		return "", err
	}

	resp, err := slack.PostToBridge(ctx, bridgeURL, env, sig)
	if err != nil {
		// Defense in depth (2026-06-24 invalid_blocks incident): a block payload
		// that passes the local size/overflow caps but is schematically rejected
		// by Slack (invalid_blocks) would otherwise drop the reply entirely. When
		// we actually sent blocks, re-post ONCE without them using the mrkdwn/plain
		// fallback already computed in `rendered`. Build a FRESH envelope (new
		// nonce) — the bridge reserved the first nonce, so reuse returns
		// replayed_nonce 401. A single attempt, no loop.
		if blocksJSON != "" && strings.Contains(err.Error(), "invalid_blocks") {
			fmt.Fprintf(os.Stderr, "km-slack: blocks rejected (invalid_blocks); re-posting as mrkdwn\n")
			fbEnv, fbErr := slack.BuildEnvelope(slack.ActionPost, sandboxID, channel, subject, rendered, thread)
			if fbErr != nil {
				return "", err // original error
			}
			_, fbSig, fbErr := slack.SignEnvelope(fbEnv, priv)
			if fbErr != nil {
				return "", err // original error
			}
			fbResp, fbErr := slack.PostToBridge(ctx, bridgeURL, fbEnv, fbSig)
			if fbErr != nil {
				return "", err // original error — fallback also failed
			}
			if !fbResp.OK {
				return "", fmt.Errorf("bridge returned not-ok on mrkdwn fallback: %s", fbResp.Error)
			}
			fmt.Fprintf(os.Stderr, "km-slack: posted ts=%s (mrkdwn fallback)\n", fbResp.TS)
			return fbResp.TS, nil
		}
		return "", err
	}
	if !resp.OK {
		return "", fmt.Errorf("bridge returned not-ok: %s", resp.Error)
	}
	fmt.Fprintf(os.Stderr, "km-slack: posted ts=%s\n", resp.TS)
	return resp.TS, nil
}

// runPermalink wraps chat.getPermalink via the bridge.
// Output: permalink URL to stdout on success; error to stderr + non-zero exit on failure.
// Phase 70 — used by Plan 70-06 cross-agent thread switch.
func runPermalink(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("permalink", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		channel = fs.String("channel", "", "Slack channel ID (C...) — required")
		ts      = fs.String("ts", "", "Slack message ts (NNNNNN.MMMMMM) — required")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *channel == "" || *ts == "" {
		fmt.Fprintln(stderr, "km-slack permalink: --channel and --ts are required")
		return 2
	}

	sandboxID := os.Getenv("KM_SANDBOX_ID")
	bridgeURL := os.Getenv("KM_SLACK_BRIDGE_URL")
	if sandboxID == "" || bridgeURL == "" {
		fmt.Fprintln(stderr, "km-slack permalink: KM_SANDBOX_ID and KM_SLACK_BRIDGE_URL required")
		return 2
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		fmt.Fprintln(stderr, "km-slack permalink: AWS_REGION (or AWS_DEFAULT_REGION) not set")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	priv, err := loadPrivateKey(ctx, region, sandboxID)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack permalink: load signing key: %v\n", err)
		return 1
	}

	env, err := slack.BuildEnvelope(slack.ActionPermalink, sandboxID, *channel, "", "", "")
	if err != nil {
		fmt.Fprintf(stderr, "km-slack permalink: build envelope: %v\n", err)
		return 1
	}
	env.MessageTS = *ts

	permalink, err := postForPermalink(ctx, priv, bridgeURL, env)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack permalink: bridge call failed: %v\n", err)
		return 1
	}
	fmt.Println(permalink) // STDOUT — poller pipes/captures
	return 0
}

// runUpdate wraps chat.update via the bridge. Subject to Slack's 10-minute
// bot-edit window. Phase 70 — optional cleaner-UX path for Plan 70-06.
func runUpdate(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		channel  = fs.String("channel", "", "Slack channel ID (C...) — required")
		ts       = fs.String("ts", "", "Slack message ts — required")
		text     = fs.String("text", "", "New message text (or use --body file)")
		bodyPath = fs.String("body", "", "Path to body file (alternative to --text)")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *channel == "" || *ts == "" {
		fmt.Fprintln(stderr, "km-slack update: --channel and --ts are required")
		return 2
	}
	body := *text
	if body == "" && *bodyPath != "" {
		b, err := os.ReadFile(*bodyPath)
		if err != nil {
			fmt.Fprintf(stderr, "km-slack update: read body file: %v\n", err)
			return 1
		}
		body = string(b)
	}
	if body == "" {
		fmt.Fprintln(stderr, "km-slack update: one of --text or --body required")
		return 2
	}

	sandboxID := os.Getenv("KM_SANDBOX_ID")
	bridgeURL := os.Getenv("KM_SLACK_BRIDGE_URL")
	if sandboxID == "" || bridgeURL == "" {
		fmt.Fprintln(stderr, "km-slack update: KM_SANDBOX_ID and KM_SLACK_BRIDGE_URL required")
		return 2
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		fmt.Fprintln(stderr, "km-slack update: AWS_REGION (or AWS_DEFAULT_REGION) not set")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	priv, err := loadPrivateKey(ctx, region, sandboxID)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack update: load signing key: %v\n", err)
		return 1
	}

	env, err := slack.BuildEnvelope(slack.ActionUpdate, sandboxID, *channel, "", "", "")
	if err != nil {
		fmt.Fprintf(stderr, "km-slack update: build envelope: %v\n", err)
		return 1
	}
	env.MessageTS = *ts
	env.Text = body

	_, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack update: sign: %v\n", err)
		return 1
	}

	resp, err := slack.PostToBridge(ctx, bridgeURL, env, sig)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack update: bridge POST: %v\n", err)
		return 1
	}
	if !resp.OK {
		fmt.Fprintf(stderr, "km-slack update: bridge returned not-ok: %s\n", resp.Error)
		return 1
	}
	return 0
}

// postForPermalink signs and POSTs a permalink envelope and returns the permalink URL.
// The bridge returns {"ok":true,"permalink":"..."} decoded into PostResponse.Permalink.
// Phase 70.
func postForPermalink(ctx context.Context, priv ed25519.PrivateKey, bridgeURL string, env *slack.SlackEnvelope) (string, error) {
	_, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	resp, err := slack.PostToBridge(ctx, bridgeURL, env, sig)
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return "", fmt.Errorf("bridge returned not-ok: %s", resp.Error)
	}
	return resp.Permalink, nil
}

// runPermalinkWith is the testable inner entry point for the permalink subcommand.
// Tests inject an ephemeral key and stub bridge server URL directly, bypassing SSM.
// Returns the permalink URL on success. Phase 70.
func runPermalinkWith(ctx context.Context, priv ed25519.PrivateKey, sandboxID, bridgeURL, channel, messageTS string) (string, error) {
	if sandboxID == "" {
		return "", errors.New("sandboxID is required")
	}
	if bridgeURL == "" {
		return "", errors.New("bridgeURL is required")
	}
	env, err := slack.BuildEnvelope(slack.ActionPermalink, sandboxID, channel, "", "", "")
	if err != nil {
		return "", fmt.Errorf("build envelope: %w", err)
	}
	env.MessageTS = messageTS
	return postForPermalink(ctx, priv, bridgeURL, env)
}

// runUpdateWith is the testable inner entry point for the update subcommand.
// Tests inject an ephemeral key and stub bridge server URL directly, bypassing SSM.
// Returns nil on success. Phase 70.
func runUpdateWith(ctx context.Context, priv ed25519.PrivateKey, sandboxID, bridgeURL, channel, messageTS, text string) error {
	if sandboxID == "" {
		return errors.New("sandboxID is required")
	}
	if bridgeURL == "" {
		return errors.New("bridgeURL is required")
	}
	env, err := slack.BuildEnvelope(slack.ActionUpdate, sandboxID, channel, "", "", "")
	if err != nil {
		return fmt.Errorf("build envelope: %w", err)
	}
	env.MessageTS = messageTS
	env.Text = text

	_, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	resp, err := slack.PostToBridge(ctx, bridgeURL, env, sig)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("bridge returned not-ok: %s", resp.Error)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Phase 110 Plan 03: km-slack reply — session-aware thread resolution
// ---------------------------------------------------------------------------

// claudeProjectsRoot is the directory scanned by autoDetectClaudeSession.
// Overridable in tests via package-level assignment.
var claudeProjectsRoot = filepath.Join(os.Getenv("HOME"), ".claude", "projects")

// codexStoreRoot is the directory scanned by autoDetectCodexSession.
// Overridable in tests.
var codexStoreRoot = filepath.Join(os.Getenv("HOME"), ".codex", "store")

// runReplyOptions carries the parsed flags for runReplyWith. Separated from
// the flag-parsing entry point (runReply) so tests can construct options
// directly without constructing an os.Args-style slice.
type runReplyOptions struct {
	// session is the explicit --session flag value (may be empty; falls back to auto-detect).
	session string
	// channel is the explicit --channel flag value (optional; defaults to KM_SLACK_CHANNEL_ID).
	channel string
	// thread is the explicit --thread flag value (optional; triggers step 1 of resolution chain).
	thread string
	// subject is the optional --subject flag value (forwarded to the post envelope).
	subject string
	// bodyPath is the --body flag value (required; path to a file).
	bodyPath string
	// render is the --render flag value (default "plain").
	render string
}

// runReply is the dispatch entry point for the "reply" subcommand.
func runReply(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("reply", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opts runReplyOptions
	fs.StringVar(&opts.session, "session", "", "Explicit session id; if empty, auto-detected from newest session file")
	fs.StringVar(&opts.channel, "channel", "", "Slack channel ID override (default: $KM_SLACK_CHANNEL_ID)")
	fs.StringVar(&opts.thread, "thread", "", "Explicit thread parent ts (step 1: requires --channel or $KM_SLACK_CHANNEL_ID)")
	fs.StringVar(&opts.subject, "subject", "", "Optional subject text")
	fs.StringVar(&opts.bodyPath, "body", "", "Path to body file (required)")
	fs.StringVar(&opts.render, "render", "", "Render mode: plain (default), mrkdwn, blocks, blocks-rich (Phase 111 Tier 3 opt-in)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if opts.bodyPath == "" {
		fmt.Fprintln(stderr, "km-slack reply: --body is required")
		return 2
	}
	if opts.bodyPath == "-" {
		fmt.Fprintln(stderr, "km-slack reply: stdin not supported (use a file path)")
		return 1
	}

	// Resolve render mode: explicit flag > KM_SLACK_RENDER env > "plain".
	if opts.render == "" {
		opts.render = os.Getenv("KM_SLACK_RENDER")
	}
	if opts.render == "" {
		opts.render = "plain"
	}
	switch opts.render {
	case "plain", "mrkdwn", "blocks", "blocks-rich":
		// valid
	default:
		fmt.Fprintf(stderr, "km-slack reply: unknown --render value %q; falling back to plain\n", opts.render)
		opts.render = "plain"
	}

	sandboxID := os.Getenv("KM_SANDBOX_ID")
	bridgeURL := os.Getenv("KM_SLACK_BRIDGE_URL")
	if sandboxID == "" {
		fmt.Fprintln(stderr, "km-slack reply: KM_SANDBOX_ID env var not set")
		return 1
	}
	if bridgeURL == "" {
		fmt.Fprintln(stderr, "km-slack reply: KM_SLACK_BRIDGE_URL env var not set")
		return 1
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		fmt.Fprintln(stderr, "km-slack reply: AWS_REGION (or AWS_DEFAULT_REGION) not set")
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	priv, err := loadPrivateKey(ctx, region, sandboxID)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack reply: load signing key: %v\n", err)
		return 1
	}

	if err := runReplyWith(ctx, priv, sandboxID, bridgeURL, opts); err != nil {
		fmt.Fprintf(stderr, "km-slack reply: %v\n", err)
		return 1
	}
	return 0
}

// runReplyWith is the testable inner entry point for the reply subcommand.
// Tests inject an ephemeral key, stub bridge URL, and options directly.
//
// Resolution chain (first-hit-wins):
//  1. opts.thread non-empty → post to (opts.channel or KM_SLACK_CHANNEL_ID, opts.thread)
//  2. $KM_SLACK_THREAD_TS non-empty → post to (KM_SLACK_CHANNEL_ID, that ts)
//  3. session id (opts.session or auto-detect) → bridge lookup-thread → on found:true post to result
//  4. fallback: top-level post to KM_SLACK_CHANNEL_ID
//
// The sandbox NEVER reads DynamoDB directly; step 3 resolves via the bridge
// lookup-thread action (Plan 02).
func runReplyWith(ctx context.Context, priv ed25519.PrivateKey, sandboxID, bridgeURL string, opts runReplyOptions) error {
	envChannel := os.Getenv("KM_SLACK_CHANNEL_ID")
	envThreadTS := os.Getenv("KM_SLACK_THREAD_TS")

	// Resolve effective channel: explicit flag beats env.
	effectiveChannel := opts.channel
	if effectiveChannel == "" {
		effectiveChannel = envChannel
	}

	// Guard: we need a destination channel for any post or fallback.
	if effectiveChannel == "" && opts.thread == "" {
		return errors.New("Slack not configured for this sandbox; re-create with notification.slack.enabled: true")
	}

	var resolvedChannel, resolvedThread string

	// Step 1: explicit --thread flag.
	if opts.thread != "" {
		// Require a channel.
		if effectiveChannel == "" {
			return errors.New("km-slack reply: --thread requires --channel or $KM_SLACK_CHANNEL_ID")
		}
		resolvedChannel = effectiveChannel
		resolvedThread = opts.thread
	}

	// Step 2: $KM_SLACK_THREAD_TS env var.
	if resolvedChannel == "" && envThreadTS != "" {
		resolvedChannel = effectiveChannel
		resolvedThread = envThreadTS
	}

	// Step 3: session-id → bridge lookup-thread.
	if resolvedChannel == "" {
		sessionID := opts.session
		if sessionID == "" {
			sessionID = autoDetectSession()
		}
		if sessionID != "" {
			ch, ts, lookupErr := lookupThreadBySession(ctx, priv, sandboxID, bridgeURL, sessionID)
			if lookupErr != nil {
				// Log the error but don't fail — fall through to channel root.
				fmt.Fprintf(os.Stderr, "km-slack reply: lookup-thread error (falling back to channel root): %v\n", lookupErr)
			} else if ch != "" {
				resolvedChannel = ch
				resolvedThread = ts
			}
		} else {
			// No session auto-detected (Codex or no files found) — warn and fall through.
			fmt.Fprintf(os.Stderr, "km-slack reply: no session id resolved; falling back to channel root\n")
		}
	}

	// Step 4: fallback to channel root.
	if resolvedChannel == "" {
		resolvedChannel = effectiveChannel
		resolvedThread = ""
	}

	if resolvedChannel == "" {
		return errors.New("Slack not configured for this sandbox; re-create with notification.slack.enabled: true")
	}

	// Reuse the existing runWith post path with the resolved (channel, thread).
	_, err := runWith(ctx, priv, sandboxID, bridgeURL, resolvedChannel, opts.subject, opts.bodyPath, resolvedThread, opts.render)
	return err
}

// lookupThreadBySession posts a signed ActionLookupThread envelope to the bridge
// and returns (channelID, threadTS) on a found:true response, or ("","","") on found:false.
// Returns an error only on network failure or non-2xx; found:false is NOT an error.
func lookupThreadBySession(ctx context.Context, priv ed25519.PrivateKey, sandboxID, bridgeURL, sessionID string) (channelID, threadTS string, err error) {
	env, buildErr := slack.BuildEnvelope(slack.ActionLookupThread, sandboxID, "", "", "", "")
	if buildErr != nil {
		return "", "", fmt.Errorf("build lookup-thread envelope: %w", buildErr)
	}
	env.SessionID = sessionID

	canonical, sig, signErr := slack.SignEnvelope(env, priv)
	if signErr != nil {
		return "", "", fmt.Errorf("sign lookup-thread envelope: %w", signErr)
	}
	sigB64 := base64.StdEncoding.EncodeToString(sig)

	req, reqErr := http.NewRequestWithContext(ctx, "POST", bridgeURL, bytes.NewReader(canonical))
	if reqErr != nil {
		return "", "", fmt.Errorf("build request: %w", reqErr)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-KM-Sender-ID", sandboxID)
	req.Header.Set("X-KM-Signature", sigB64)

	resp, httpErr := http.DefaultClient.Do(req)
	if httpErr != nil {
		return "", "", fmt.Errorf("lookup-thread HTTP: %w", httpErr)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("lookup-thread bridge returned %d: %s", resp.StatusCode, string(respBody))
	}

	var pr slack.PostResponse
	if jsonErr := json.Unmarshal(respBody, &pr); jsonErr != nil {
		return "", "", fmt.Errorf("decode lookup-thread response: %w", jsonErr)
	}
	if !pr.OK {
		return "", "", fmt.Errorf("lookup-thread bridge error: %s", pr.Error)
	}
	if !pr.Found {
		return "", "", nil // found:false — caller falls through to channel root
	}
	return pr.ChannelID, pr.ThreadTS, nil
}

// autoDetectSession branches on $KM_AGENT and returns a session id, or "" if none found.
// Never errors — on Codex with no resolvable path, returns "" so caller falls through
// to channel-root fallback.
func autoDetectSession() string {
	agent := os.Getenv("KM_AGENT")
	if agent == "codex" {
		id := autoDetectCodexSession()
		if id == "" {
			fmt.Fprintf(os.Stderr, "km-slack reply: WARN: KM_AGENT=codex but no session file found under %s; falling back to channel root\n", codexStoreRoot)
		}
		return id
	}
	// Default: claude path.
	return autoDetectClaudeSession()
}

// autoDetectClaudeSession walks claudeProjectsRoot recursively for *.jsonl files
// and returns the UUID stem of the newest by mtime. Returns "" if none found.
func autoDetectClaudeSession() string {
	root := claudeProjectsRoot
	var newest string
	var newestTime time.Time

	_ = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info == nil || info.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = path
		}
		return nil
	})
	if newest == "" {
		return ""
	}
	base := filepath.Base(newest)
	// Strip .jsonl extension to get the session UUID.
	return base[:len(base)-len(".jsonl")]
}

// autoDetectCodexSession walks codexStoreRoot for the newest session file
// (best-effort; LOW-confidence path per OQ1). Returns "" if nothing found.
func autoDetectCodexSession() string {
	root := codexStoreRoot
	var newest string
	var newestTime time.Time

	_ = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info == nil || info.IsDir() {
			return nil
		}
		if info.ModTime().After(newestTime) {
			newestTime = info.ModTime()
			newest = path
		}
		return nil
	})
	if newest == "" {
		return ""
	}
	base := filepath.Base(newest)
	// Strip extension to get session id.
	if ext := filepath.Ext(base); ext != "" {
		return base[:len(base)-len(ext)]
	}
	return base
}

// loadPrivateKey fetches /{resource_prefix}/sandbox/{sandboxID}/signing-key
// from SSM (decrypted), base64-decodes, returns an ed25519.PrivateKey using
// the first 32 bytes as seed.
//
// resource_prefix is read from KM_RESOURCE_PREFIX (defaults to "km") so this
// binary, deployed to a sandbox, picks the same SSM namespace the operator-
// side km create wrote the key into.
func loadPrivateKey(ctx context.Context, region, sandboxID string) (ed25519.PrivateKey, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	client := ssm.NewFromConfig(cfg)
	resourcePrefix := os.Getenv("KM_RESOURCE_PREFIX")
	if resourcePrefix == "" {
		resourcePrefix = "km"
	}
	keyPath := fmt.Sprintf("/%s/sandbox/%s/signing-key", resourcePrefix, sandboxID)
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
