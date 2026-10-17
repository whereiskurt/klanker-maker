package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// reDMUserID admits a bare Slack user id only: no names, no here/channel, no
// <@…> or @ forms.
var reDMUserID = regexp.MustCompile(`^[UW][A-Z0-9]+$`)

type dmOptions struct {
	to          string
	text        string
	bodyPath    string
	render      string
	fromChannel string
	fromThread  string
}

// runDM — `km-slack dm`: ask the bridge to DM a person. The bridge admits only
// a target whose email is on slack.default_route.allow_emails. Top-level post:
// no posted marker, so the originating thread's automatic reply still lands.
func runDM(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("dm", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o dmOptions
	var fromThread bool
	fs.StringVar(&o.to, "to", "", "Slack user id to DM (U…/W…); names are rejected — take the id from the [Slack] From: line")
	fs.StringVar(&o.text, "text", "", "Message text (or --body)")
	fs.StringVar(&o.bodyPath, "body", "", "Path to a body file (or --text)")
	fs.StringVar(&o.render, "render", "", "plain|mrkdwn|blocks|blocks-rich (default: KM_SLACK_RENDER or plain)")
	fs.BoolVar(&fromThread, "from-thread", false, "Append a permalink to the current turn's thread (KM_SLACK_CHANNEL_ID + KM_SLACK_THREAD_TS)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	_, perr := slack.ParseMentions([]string{o.to})
	if o.to == "" || perr != nil || !reDMUserID.MatchString(o.to) {
		msg := "km-slack dm: --to must be a Slack user id (U…), not a name"
		if perr != nil {
			msg += ": " + perr.Error()
		}
		fmt.Fprintln(stderr, msg)
		return 2
	}
	if (o.text == "") == (o.bodyPath == "") {
		fmt.Fprintln(stderr, "km-slack dm: exactly one of --text or --body is required")
		return 2
	}
	if fromThread {
		o.fromChannel, o.fromThread = os.Getenv("KM_SLACK_CHANNEL_ID"), os.Getenv("KM_SLACK_THREAD_TS")
		if o.fromChannel == "" || o.fromThread == "" {
			fmt.Fprintln(stderr, "km-slack dm: --from-thread needs KM_SLACK_CHANNEL_ID and KM_SLACK_THREAD_TS (set per turn by the inbound poller)")
			return 2
		}
	}
	if o.render == "" {
		o.render = os.Getenv("KM_SLACK_RENDER")
	}
	if o.render == "" {
		o.render = "plain"
	}

	sandboxID, bridgeURL := os.Getenv("KM_SANDBOX_ID"), os.Getenv("KM_SLACK_BRIDGE_URL")
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if sandboxID == "" || bridgeURL == "" || region == "" {
		fmt.Fprintln(stderr, "km-slack dm: KM_SANDBOX_ID, KM_SLACK_BRIDGE_URL and AWS_REGION must be set")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()
	priv, err := loadPrivateKey(ctx, region, sandboxID)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack dm: load signing key: %v\n", err)
		return 1
	}
	ch, ts, err := runDMWith(ctx, priv, sandboxID, bridgeURL, o)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack dm: %v\n", err)
		return 1
	}
	fmt.Printf("channel=%s ts=%s\n", ch, ts)
	return 0
}

// runDMWith renders, signs and POSTs an ActionDM envelope. Returns the DM
// channel id and message ts from the bridge. It never writes a posted marker.
func runDMWith(ctx context.Context, priv ed25519.PrivateKey, sandboxID, bridgeURL string, o dmOptions) (string, string, error) {
	body := []byte(o.text)
	if o.bodyPath != "" {
		b, err := os.ReadFile(o.bodyPath)
		if err != nil {
			return "", "", fmt.Errorf("read body file: %w", err)
		}
		body = b
	}
	rendered, blocksJSON := renderBody(o.render, body)
	if len(rendered) > slack.MaxRenderedBytes {
		rendered = rendered[:slack.MaxRenderedBytes] + "\n_…truncated_"
	}
	env, err := slack.BuildEnvelope(slack.ActionDM, sandboxID, o.fromChannel, "", rendered, o.fromThread)
	if err != nil {
		return "", "", err
	}
	env.TargetUser = o.to
	env.Blocks = blocksJSON
	_, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		return "", "", err
	}
	resp, err := slack.PostToBridge(ctx, bridgeURL, env, sig)
	if err != nil && blocksJSON != "" && strings.Contains(err.Error(), "invalid_blocks") {
		// Fresh envelope (new nonce: the bridge reserved the first), no blocks, once.
		fmt.Fprintln(os.Stderr, "km-slack: WARN dm blocks rejected (invalid_blocks); re-sent without blocks")
		fb, fbErr := slack.BuildEnvelope(slack.ActionDM, sandboxID, o.fromChannel, "", rendered, o.fromThread)
		if fbErr != nil {
			return "", "", err
		}
		fb.TargetUser = o.to
		_, fbSig, fbErr := slack.SignEnvelope(fb, priv)
		if fbErr != nil {
			return "", "", err
		}
		resp, fbErr = slack.PostToBridge(ctx, bridgeURL, fb, fbSig)
		if fbErr != nil {
			return "", "", err
		}
		err = nil
	}
	if err != nil {
		return "", "", err
	}
	if !resp.OK {
		return "", "", errors.New("bridge returned not-ok: " + resp.Error)
	}
	fmt.Fprintf(os.Stderr, "km-slack: dm sent channel=%s ts=%s\n", resp.ChannelID, resp.TS)
	return resp.ChannelID, resp.TS, nil
}
