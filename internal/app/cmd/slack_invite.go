package cmd

// slack_invite.go — Phase 72 Plan 72-05: km slack invite command.
//
// RunSlackInvite handles `km slack invite <email>`. It resolves the channel,
// defensively self-joins the bot (Pitfall 2 mitigation), then calls the
// Phase 72-04 EnsureMemberByEmail orchestrator.
//
// Exit-code contract (honoured by Execute in root.go via *ExitCodeError):
//   0  → Invited{Direct,Connect} or AlreadyMember
//   1  → Failed (plain error; cobra default)
//   2  → SkippedExternal (returned as *ExitCodeError{Code: 2})

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmslack "github.com/whereiskurt/klanker-maker/pkg/slack"
)

// SlackInviteOpts collects flags + positional args for `km slack invite`.
type SlackInviteOpts struct {
	Email    string
	Channel  string // channel name (e.g. "km-notifications") or ID (e.g. "C012ABCDE3F")
	External bool   // --external: skip lookup, force Slack Connect invite
	DryRun   bool   // --dry-run: read-only classify; print action that WOULD be taken; send nothing
}

// channelIDPattern matches Slack channel IDs (start with C, uppercase + digits).
var channelIDPattern = regexp.MustCompile(`^C[A-Z0-9]+$`)

// ConnectFallbackPrompter is a stdin-backed kmslack.Prompter used by
// `km slack invite` interactive sessions. It reads a y/Y confirmation from
// stdin, defaulting to N (no).
//
// When Inner is non-nil (injected in tests or from RunSlackInit via the cmd
// layer's SlackPrompter), ConfirmConnect delegates to Inner.PromptString so
// tests can supply deterministic answers without requiring a real TTY.
//
// The prompt text explains that the email is not a workspace member and that
// a Pro Slack workspace is required for the Connect invite.
type ConnectFallbackPrompter struct {
	// Inner is an optional cmd-layer prompter. When set, ConfirmConnect uses
	// Inner.PromptString instead of reading from os.Stdin. This makes the
	// RunSlackInit path testable without a real TTY.
	Inner SlackPrompter
}

// ConfirmConnect implements kmslack.Prompter. Prints the prompt to stderr and
// reads a y/Y line from stdin (or from Inner when set). Any input other than
// y/Y → false.
func (p *ConnectFallbackPrompter) ConfirmConnect(email string) (bool, error) {
	msg := fmt.Sprintf(
		"%s is not a member of this Slack workspace.\n"+
			"Send a Slack Connect invite (requires Pro Slack workspace)? [y/N]: ",
		email,
	)
	if p.Inner != nil {
		// Delegate to the injected prompter (tests or RunSlackInit cmd path).
		resp, err := p.Inner.PromptString(msg)
		if err != nil {
			return false, err
		}
		resp = strings.TrimSpace(resp)
		return resp == "y" || resp == "Y", nil
	}
	fmt.Fprint(os.Stderr, msg)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false, scanner.Err()
	}
	resp := strings.TrimSpace(scanner.Text())
	return resp == "y" || resp == "Y", nil
}

// RunSlackInvite is the exported business logic for `km slack invite`.
//
// It:
//  1. Resolves the channel ID (ID passthrough / name lookup / SSM default).
//  2. Defensively self-joins the bot (Pitfall 2 — handles `not_in_channel`), unless --dry-run.
//  3. Calls EnsureMemberByEmail with Interactive=true on a real TTY, DryRun for probe.
//  4. Returns nil on 0-exit outcomes (Invited*/AlreadyMember/DryRun classification).
//     Returns *ExitCodeError{Code:2} for SkippedExternal.
//     Returns a plain error (exit code 1) for Failed.
func RunSlackInvite(ctx context.Context, d *SlackCmdDeps, cfg *config.Config, opts SlackInviteOpts) error {
	if opts.Email == "" {
		return fmt.Errorf("email is required")
	}

	// Lazy-init the full-capability Slack client from the SSM-stored bot token.
	// In production buildSlackCmdDeps returns deps with d.Slack == nil because the
	// token is in SSM rather than the constructor arguments — RunSlackInit follows
	// the same pattern (see slack.go ~line 222). In tests d.Slack is pre-set with
	// a recording fake so this branch short-circuits.
	if d.Slack == nil {
		token, err := d.SSM.Get(ctx, d.SsmPrefix+"slack/bot-token", true)
		if err != nil {
			return fmt.Errorf("read Slack bot token from SSM (%sslack/bot-token): %w; run `km slack init` first", d.SsmPrefix, err)
		}
		if token == "" {
			return fmt.Errorf("Slack bot token not configured in SSM (%sslack/bot-token); run `km slack init` first", d.SsmPrefix)
		}
		d.Slack = kmslack.NewClient(token, nil)
	}

	// Resolve channel: ID format → use directly; name → FindChannelByName; empty → SSM default.
	channelID, err := resolveInviteChannel(ctx, d, opts.Channel)
	if err != nil {
		return fmt.Errorf("resolve channel %q: %w", opts.Channel, err)
	}

	// Defensive bot self-join (Pitfall 2). JoinChannel is idempotent — safe to
	// call even when the bot is already a member. Skipped under --dry-run (the
	// probe must perform zero writes).
	if !opts.DryRun {
		if err := d.Slack.JoinChannel(ctx, channelID); err != nil {
			// Non-fatal warning — if join fails for permission reasons, the invite
			// call below will surface a clearer error.
			fmt.Fprintf(os.Stderr, "[warn] could not join channel %s: %v\n", channelID, err)
		}
	}

	res, inviteErr := kmslack.EnsureMemberByEmail(ctx, d.Slack, channelID, opts.Email, kmslack.EnsureMemberOpts{
		ForceExternal: opts.External,
		Interactive:   isStdinInteractive() && !opts.DryRun,
		AutoConnect:   false,
		DryRun:        opts.DryRun,
		Prompter:      &ConnectFallbackPrompter{},
	})

	// --dry-run: render the classification (no write was performed) and exit 0.
	if opts.DryRun {
		switch res {
		case kmslack.InvitedDirect:
			fmt.Fprintf(os.Stderr, "[dry-run] %s is a workspace member — would invite via conversations.invite to %s\n", opts.Email, channelID)
		case kmslack.InvitedConnect:
			fmt.Fprintf(os.Stderr, "[dry-run] would send a Slack Connect invite to %s for %s (--external)\n", opts.Email, channelID)
		case kmslack.SkippedExternal:
			fmt.Fprintf(os.Stderr, "[dry-run] %s is NOT a workspace member — would require a Slack Connect invite (re-run with --external, without --dry-run, to send)\n", opts.Email)
		case kmslack.Failed:
			return fmt.Errorf("dry-run lookup failed: %w", inviteErr)
		}
		return nil
	}

	switch res {
	case kmslack.InvitedDirect:
		fmt.Fprintf(os.Stderr, "✓ Invited %s to %s\n", opts.Email, channelID)
		return nil
	case kmslack.InvitedConnect:
		fmt.Fprintf(os.Stderr, "✓ Sent Slack Connect invite to %s for %s\n", opts.Email, channelID)
		return nil
	case kmslack.AlreadyMember:
		fmt.Fprintf(os.Stderr, "✓ %s is already a member of %s\n", opts.Email, channelID)
		return nil
	case kmslack.SkippedExternal:
		fmt.Fprintf(os.Stderr,
			"[skip] %s is not in the workspace; not sending Connect invite. To send one, re-run with --external:\n  km slack invite --external %s --channel %s\n",
			opts.Email, opts.Email, channelID,
		)
		return &ExitCodeError{Code: 2, Inner: fmt.Errorf("skipped external invite for %s", opts.Email)}
	case kmslack.Failed:
		return fmt.Errorf("invite failed: %w", inviteErr)
	default:
		return fmt.Errorf("unexpected result %v: %v", res, inviteErr)
	}
}

// resolveInviteChannel returns the channel ID for the requested name/ID/empty.
func resolveInviteChannel(ctx context.Context, d *SlackCmdDeps, want string) (string, error) {
	if want == "" {
		return d.SSM.Get(ctx, d.SsmPrefix+"slack/shared-channel-id", false)
	}
	if channelIDPattern.MatchString(want) {
		return want, nil
	}
	// Name → ID lookup. FindChannelByName returns "" when not found.
	id, err := d.Slack.FindChannelByName(ctx, strings.TrimPrefix(want, "#"), 1000)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("channel %q not found", want)
	}
	return id, nil
}

// isStdinInteractive reports whether stdin is a TTY (so we can prompt).
func isStdinInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// newSlackInviteCmd is the cobra wiring for `km slack invite`.
func newSlackInviteCmd(cfg *config.Config, sharedDeps *SlackCmdDeps) *cobra.Command {
	var (
		channelArg string
		external   bool
		dryRun     bool
	)
	c := &cobra.Command{
		Use:   "invite <email>",
		Short: "Invite an email address to a Slack channel (auto-detects native vs Connect)",
		Long: `Invite a person to a Slack channel by email address.

By default, looks up the email in the workspace and sends a regular channel
invite if found. On miss, prompts to send a Slack Connect invite (Pro tier
required). Use --external to skip the lookup and force a Connect invite.

Examples:
  km slack invite alice@example.com
  km slack invite alice@example.com --channel km-notifications
  km slack invite bob@external.com --external
  km slack invite charlie@example.com --channel C012ABCDE3F
  km slack invite alice@example.com --dry-run   # classify only, send nothing`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			deps := sharedDeps
			if deps == nil {
				var err error
				deps, err = buildSlackCmdDeps(cfg)
				if err != nil {
					return err
				}
			}
			return RunSlackInvite(ctx, deps, cfg, SlackInviteOpts{
				Email:    args[0],
				Channel:  channelArg,
				External: external,
				DryRun:   dryRun,
			})
		},
	}
	c.Flags().StringVar(&channelArg, "channel", "", "Channel name (e.g. km-notifications) or ID (e.g. C012ABCDE3F); default: SSM-stored shared channel")
	c.Flags().BoolVar(&external, "external", false, "Skip lookup; send Slack Connect invite directly (no prompt)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Read-only: look up the email and print whether it would be invited natively or via Slack Connect; send nothing")
	return c
}
