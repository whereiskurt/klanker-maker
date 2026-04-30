// Command km-slack is the sandbox-side Phase 63 Slack-notify client. It
// signs an envelope with the sandbox's Ed25519 key (loaded from SSM
// /sandbox/{id}/signing-key) and POSTs it to the km-slack-bridge Lambda
// Function URL ($KM_SLACK_BRIDGE_URL). It is invoked by /opt/km/bin/km-notify-hook
// when KM_NOTIFY_SLACK_ENABLED=1 — see pkg/compiler/userdata.go.
//
// Usage:
//
//	km-slack post --channel C0123ABC --subject "[sb-id] needs permission" --body /tmp/body.txt
//
// Required env: KM_SANDBOX_ID, KM_SLACK_BRIDGE_URL, AWS_REGION (or
// AWS_DEFAULT_REGION). Body file argument required; --body - (stdin) is
// rejected per the OpenSSL 3.5+ signing constraint (CLAUDE.md).
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/whereiskurt/klankrmkr/pkg/slack"
)

const defaultTimeout = 30 * time.Second

func main() {
	if len(os.Args) < 2 || os.Args[1] != "post" {
		fmt.Fprintln(os.Stderr, "usage: km-slack post --channel <id> --subject <text> --body <file> [--thread <ts>]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("post", flag.ExitOnError)
	var channel, subject, bodyPath, thread string
	fs.StringVar(&channel, "channel", "", "Slack channel ID (C...)")
	fs.StringVar(&subject, "subject", "", "Subject text (used as bold header by bridge)")
	fs.StringVar(&bodyPath, "body", "", "Path to body file (stdin '-' NOT supported)")
	fs.StringVar(&thread, "thread", "", "Thread parent ts (wired, unused in v1)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}
	if channel == "" || subject == "" || bodyPath == "" {
		fmt.Fprintln(os.Stderr, "km-slack: --channel, --subject, --body are required")
		os.Exit(2)
	}
	if bodyPath == "-" {
		fmt.Fprintln(os.Stderr, "km-slack: stdin not supported (use a file path); see CLAUDE.md OpenSSL 3.5+ constraint")
		os.Exit(1)
	}

	if err := run(channel, subject, bodyPath, thread); err != nil {
		fmt.Fprintf(os.Stderr, "km-slack: %v\n", err)
		os.Exit(1)
	}
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
