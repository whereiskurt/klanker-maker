package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/whereiskurt/klanker-maker/pkg/slack"
)

// runSession records (or clears) the agent session id and agent type on a
// thread row the bridge routed. The bridge checks ownership; the sandbox role
// has no direct grant on the threads tables.
type sessionOptions struct {
	source, channel, thread, repo string
	number                        int
	report, target                string
	sessionID, agent              string
	clear                         bool
}

func runSession(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("session", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o sessionOptions
	fs.StringVar(&o.source, "source", "", "slack | github | h1")
	fs.StringVar(&o.channel, "channel", "", "slack: channel id")
	fs.StringVar(&o.thread, "thread", "", "slack: thread ts")
	fs.StringVar(&o.repo, "repo", "", "github: owner/name")
	fs.IntVar(&o.number, "number", 0, "github: issue or PR number")
	fs.StringVar(&o.report, "report", "", "h1: report id")
	fs.StringVar(&o.target, "target", "", "h1: target alias")
	fs.StringVar(&o.sessionID, "session-id", "", "agent session id to record (or --clear)")
	fs.BoolVar(&o.clear, "clear", false, "clear the stored session id")
	fs.StringVar(&o.agent, "agent", "", "claude | codex")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if msg := validateSessionOptions(o); msg != "" {
		fmt.Fprintln(stderr, "km-slack session: "+msg)
		return 2
	}
	sandboxID, bridgeURL := os.Getenv("KM_SANDBOX_ID"), os.Getenv("KM_SLACK_BRIDGE_URL")
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if sandboxID == "" || bridgeURL == "" || region == "" {
		fmt.Fprintln(stderr, "km-slack session: KM_SANDBOX_ID, KM_SLACK_BRIDGE_URL and AWS_REGION must be set")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	priv, err := loadPrivateKey(ctx, region, sandboxID)
	if err != nil {
		fmt.Fprintf(stderr, "km-slack session: load signing key: %v\n", err)
		return 1
	}
	if err := runSessionWith(ctx, priv, sandboxID, bridgeURL, o); err != nil {
		fmt.Fprintf(stderr, "km-slack session: %v\n", err)
		return 1
	}
	fmt.Fprintln(stderr, "km-slack: session recorded")
	return 0
}

func validateSessionOptions(o sessionOptions) string {
	switch o.source {
	case "slack":
		if o.channel == "" || o.thread == "" {
			return "--source slack needs --channel and --thread"
		}
	case "github":
		if o.repo == "" || o.number == 0 {
			return "--source github needs --repo and --number"
		}
	case "h1":
		if o.report == "" || o.target == "" {
			return "--source h1 needs --report and --target"
		}
	default:
		return "--source must be slack, github or h1"
	}
	if o.agent != "claude" && o.agent != "codex" {
		return "--agent must be claude or codex"
	}
	if o.clear && o.sessionID != "" {
		return "--clear and --session-id are mutually exclusive"
	}
	if !o.clear && o.sessionID == "" {
		return "one of --session-id or --clear is required"
	}
	return ""
}

func buildSessionEnvelope(sandboxID string, o sessionOptions) (*slack.SlackEnvelope, error) {
	env, err := slack.BuildEnvelope(slack.ActionSession, sandboxID, o.channel, "", "", o.thread)
	if err != nil {
		return nil, err
	}
	env.Source, env.Repo, env.Number, env.ReportID, env.Target, env.AgentType = o.source, o.repo, o.number, o.report, o.target, o.agent
	if !o.clear {
		env.SessionID = o.sessionID
	}
	return env, nil
}

func runSessionWith(ctx context.Context, priv ed25519.PrivateKey, sandboxID, bridgeURL string, o sessionOptions) error {
	env, err := buildSessionEnvelope(sandboxID, o)
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
		return errors.New("bridge returned not-ok: " + resp.Error)
	}
	return nil
}
