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
	"strings"
	"syscall"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// reactOptions is the parsed `km-slack react` command line.
type reactOptions struct {
	channel   string
	ts        string
	emoji     string   // bare name, already normalised
	exclusive []string // bare names, already normalised; may include emoji
	remove    bool
}

// runReact is the dispatch entry point for `km-slack react` (2026-10-22): add
// or remove the bot's reaction on a message in this sandbox's own channel.
//
//	km-slack react --channel C… --ts <ts> --emoji <name> [--exclusive a,b,c] [--remove]
//
// --exclusive names a mutually exclusive status set: the bridge adds --emoji and
// then removes the bot's own reaction for every OTHER name in the set, so a
// status caller never has to track the previous state. Slack's already_reacted
// and no_reaction count as success, so a retry never fails on its own earlier
// success. Exit 2 is a usage error (checked before any key or network), exit 1
// a bridge or Slack error with the Slack code on stderr.
func runReact(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("react", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		channel   = fs.String("channel", "", "Slack channel ID (C...); default $KM_SLACK_CHANNEL_ID")
		ts        = fs.String("ts", "", "Message ts to react to — required")
		emoji     = fs.String("emoji", "", "Reaction name without colons (white_check_mark, rotating_light) — required")
		exclusive = fs.String("exclusive", "", "Comma-separated names of a mutually exclusive set; every one other than --emoji is removed after the add")
		remove    = fs.Bool("remove", false, "Remove the reaction instead of adding it")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts := reactOptions{channel: *channel, ts: *ts, remove: *remove}
	if opts.channel == "" {
		opts.channel = os.Getenv("KM_SLACK_CHANNEL_ID")
	}
	if opts.channel == "" {
		fmt.Fprintln(stderr, "km-slack react: --channel is required (or set KM_SLACK_CHANNEL_ID)")
		return 2
	}
	if opts.ts == "" {
		fmt.Fprintln(stderr, "km-slack react: --ts is required")
		return 2
	}
	if *emoji == "" {
		fmt.Fprintln(stderr, "km-slack react: --emoji is required")
		return 2
	}
	name, err := slack.NormalizeEmojiName(*emoji)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack react: --emoji: bad emoji name %q (want ^[a-z0-9_+'-]{1,100}$, colons optional)\n", *emoji)
		return 2
	}
	opts.emoji = name
	if *exclusive != "" {
		for _, raw := range strings.Split(*exclusive, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			n, nerr := slack.NormalizeEmojiName(raw)
			if nerr != nil {
				fmt.Fprintf(stderr, "km-slack react: --exclusive: bad emoji name %q\n", raw)
				return 2
			}
			opts.exclusive = append(opts.exclusive, n)
		}
		if len(opts.exclusive) > slack.MaxExclusiveReactions {
			fmt.Fprintf(stderr, "km-slack react: --exclusive takes at most %d names (got %d)\n", slack.MaxExclusiveReactions, len(opts.exclusive))
			return 2
		}
	}

	sandboxID := os.Getenv("KM_SANDBOX_ID")
	bridgeURL := os.Getenv("KM_SLACK_BRIDGE_URL")
	if sandboxID == "" || bridgeURL == "" {
		fmt.Fprintln(stderr, "km-slack react: KM_SANDBOX_ID and KM_SLACK_BRIDGE_URL required")
		return 2
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		fmt.Fprintln(stderr, "km-slack react: AWS_REGION (or AWS_DEFAULT_REGION) not set")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-sigCh; cancel() }()

	priv, err := loadPrivateKey(ctx, region, sandboxID)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack react: load signing key: %v\n", err)
		return 1
	}
	if err := runReactWith(ctx, priv, sandboxID, bridgeURL, os.Stdout, opts); err != nil {
		fmt.Fprintf(stderr, "km-slack react: %v\n", err)
		return 1
	}
	return 0
}

// runReactWith signs one react envelope and POSTs it to the bridge, which does
// the add and every exclusive remove. On success it prints
// `km-slack: reacted|removed ts=<ts> emoji=<name>` to stdout. Testable entry
// point: tests inject an ephemeral key and a stub bridge URL, bypassing SSM.
func runReactWith(ctx context.Context, priv ed25519.PrivateKey, sandboxID, bridgeURL string, stdout io.Writer, opts reactOptions) error {
	if sandboxID == "" {
		return errors.New("sandboxID is required")
	}
	if bridgeURL == "" {
		return errors.New("bridgeURL is required")
	}
	// Normalise here as well as in runReact, so every caller of this entry point
	// signs the bare names the bridge re-validates.
	emoji, err := slack.NormalizeEmojiName(opts.emoji)
	if err != nil {
		return err
	}
	exclusive := make([]string, 0, len(opts.exclusive))
	for _, raw := range opts.exclusive {
		n, nerr := slack.NormalizeEmojiName(raw)
		if nerr != nil {
			return nerr
		}
		exclusive = append(exclusive, n)
	}
	if len(exclusive) > slack.MaxExclusiveReactions {
		return fmt.Errorf("--exclusive takes at most %d names (got %d)", slack.MaxExclusiveReactions, len(exclusive))
	}

	env, err := slack.BuildEnvelope(slack.ActionReact, sandboxID, opts.channel, "", "", "")
	if err != nil {
		return fmt.Errorf("build envelope: %w", err)
	}
	env.MessageTS = opts.ts
	env.Emoji = emoji
	env.Remove = opts.remove
	if len(exclusive) > 0 {
		env.Exclusive = exclusive
	}

	_, sig, err := slack.SignEnvelope(env, priv)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	resp, err := slack.PostToBridge(ctx, bridgeURL, env, sig)
	if err != nil {
		return fmt.Errorf("bridge POST: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("bridge returned not-ok: %s", resp.Error)
	}
	verb := "reacted"
	if opts.remove {
		verb = "removed"
	}
	fmt.Fprintf(stdout, "km-slack: %s ts=%s emoji=%s\n", verb, opts.ts, emoji)
	return nil
}
