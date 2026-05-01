package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/rs/zerolog/log"
	"github.com/whereiskurt/klankrmkr/pkg/profile"
	"github.com/whereiskurt/klankrmkr/pkg/slack"
)

// ssmParamStoreClient is the minimal SSM interface needed by productionSSMParamStore.
type ssmParamStoreClient interface {
	GetParameter(ctx context.Context, input *ssm.GetParameterInput, optFns ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// productionSSMParamStore adapts an SSM client to the SSMParamStore interface.
// Used by destroy.go and doctor.go to pass a real SSM client as SSMParamStore.
type productionSSMParamStore struct {
	client ssmParamStoreClient
}

func (s *productionSSMParamStore) Get(ctx context.Context, name string, withDecryption bool) (string, error) {
	out, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(name),
		WithDecryption: awssdk.Bool(withDecryption),
	})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			return "", nil // treat missing as empty
		}
		return "", err
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", nil
	}
	return *out.Parameter.Value, nil
}

// SlackAPI is the operator-side Slack client interface used during km create.
// *slack.Client satisfies this interface.
type SlackAPI interface {
	CreateChannel(ctx context.Context, name string) (string, error)
	InviteShared(ctx context.Context, channelID, email string) error
	ChannelInfo(ctx context.Context, channelID string) (memberCount int, isMember bool, err error)
}

// SSMParamStore is a narrow interface for reading SSM parameters. Used by
// resolveSlackChannel to fetch /km/slack/* config without importing the full
// SSM SDK into test files.
type SSMParamStore interface {
	Get(ctx context.Context, name string, withDecryption bool) (string, error)
}

// SSMRunner is a narrow interface for running shell commands on a sandbox
// instance via SSM SendCommand. Used by injectSlackEnvIntoSandbox.
type SSMRunner interface {
	RunShell(ctx context.Context, instanceID string, script string) error
}

var channelIDRe = regexp.MustCompile(`^C[A-Z0-9]+$`)

// resolveSlackChannel determines the Slack channel ID and per-sandbox flag for
// a sandbox being created. Returns ("", false, nil) when notifySlackEnabled is
// false or unset — no Slack work is done.
//
// Three modes (mutually exclusive per schema validation in Plan 01):
//
//   - Mode 1 (shared, default): NotifySlackPerSandbox=false AND
//     NotifySlackChannelOverride=="" → read /km/slack/shared-channel-id from
//     SSM; no Slack API calls.
//
//   - Mode 2 (per-sandbox): NotifySlackPerSandbox=true → sanitize
//     alias/sandboxID into a Slack-legal name; conversations.create;
//     conversations.inviteShared with /km/slack/invite-email; perSandbox=true.
//
//   - Mode 3 (override): NotifySlackChannelOverride != "" → validate the
//     channel ID format + confirm bot membership via ChannelInfo; perSandbox=false
//     (operator-controlled channel — do not archive at destroy).
func resolveSlackChannel(ctx context.Context, p *profile.SandboxProfile, sandboxID, alias string,
	api SlackAPI, ssmStore SSMParamStore) (channelID string, perSandbox bool, err error) {

	cli := p.Spec.CLI
	if cli == nil || cli.NotifySlackEnabled == nil || !*cli.NotifySlackEnabled {
		return "", false, nil
	}

	// Mode 3 — override: operator-controlled channel.
	if cli.NotifySlackChannelOverride != "" {
		if !channelIDRe.MatchString(cli.NotifySlackChannelOverride) {
			return "", false, fmt.Errorf("notifySlackChannelOverride %q does not match ^C[A-Z0-9]+$", cli.NotifySlackChannelOverride)
		}
		_, isMember, infoErr := api.ChannelInfo(ctx, cli.NotifySlackChannelOverride)
		if infoErr != nil {
			return "", false, fmt.Errorf("validate override channel %s: %w", cli.NotifySlackChannelOverride, infoErr)
		}
		if !isMember {
			return "", false, fmt.Errorf("bot is not a member of %s — invite km-bot to the channel first", cli.NotifySlackChannelOverride)
		}
		// perSandbox=false: operator-controlled channel should never be archived.
		return cli.NotifySlackChannelOverride, false, nil
	}

	// Mode 2 — per-sandbox: create a dedicated channel for this sandbox.
	if cli.NotifySlackPerSandbox {
		nameSeed := alias
		if nameSeed == "" {
			nameSeed = sandboxID
		}
		sanitized := sanitizeChannelName(nameSeed)
		if sanitized == "" {
			return "", false, fmt.Errorf("could not derive Slack channel name from alias/sandboxID %q", nameSeed)
		}
		// Always prefix per-sandbox channels with "sb-" to namespace them.
		// This matches the #sb-{alias} or #sb-{id} naming from CONTEXT.md.
		channelName := sanitized
		if !strings.HasPrefix(channelName, "sb-") {
			channelName = "sb-" + channelName
		}
		if len(channelName) > 80 {
			channelName = channelName[:80]
			channelName = strings.TrimRight(channelName, "-")
		}

		chID, createErr := api.CreateChannel(ctx, channelName)
		if createErr != nil {
			var apierr *slack.SlackAPIError
			if errors.As(createErr, &apierr) && apierr.Code == "name_taken" {
				return "", false, fmt.Errorf(
					"Slack channel #%s already exists (name_taken); choose a unique --alias or use notifySlackChannelOverride to reuse the existing channel",
					channelName,
				)
			}
			return "", false, fmt.Errorf("create channel #%s: %w", channelName, createErr)
		}

		// Fetch the invite email from SSM so the bot can receive cross-workspace invites.
		inviteEmail, ssmErr := ssmStore.Get(ctx, "/km/slack/invite-email", false)
		if ssmErr != nil || inviteEmail == "" {
			return "", false, fmt.Errorf("invite email not configured at /km/slack/invite-email — run km slack init first")
		}

		if inviteErr := api.InviteShared(ctx, chID, inviteEmail); inviteErr != nil {
			// Channel was created but invite failed.
			// We do NOT roll back the channel (option a per Plan 08 spec).
			// Operator can manually re-invite. Trade-off documented in SUMMARY.
			return "", false, fmt.Errorf("invite %s to channel %s: %w (channel was created; operator must invite manually)", inviteEmail, chID, inviteErr)
		}

		return chID, true, nil
	}

	// Mode 1 — shared (default): read channel ID from SSM.
	chID, ssmErr := ssmStore.Get(ctx, "/km/slack/shared-channel-id", false)
	if ssmErr != nil || chID == "" {
		return "", false, errors.New("/km/slack/shared-channel-id not set — run km slack init first")
	}
	return chID, false, nil
}

// sanitizeChannelName produces a Slack-legal channel name fragment from a
// free-form alias or sandbox ID. Slack rules: 1-80 chars, lowercase letters,
// digits, hyphens, underscores only.
//
// Transformations applied:
//   - Convert to lowercase.
//   - Replace any character that is not [a-z0-9_] with a hyphen.
//   - Collapse consecutive hyphens into a single hyphen.
//   - Trim leading/trailing hyphens.
//   - Cap at 80 characters (trimming trailing hyphens after truncation).
//
// Returns "" for unrecoverable inputs (empty after sanitization).
func sanitizeChannelName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
			prevHyphen = false
		} else if !prevHyphen {
			b.WriteRune('-')
			prevHyphen = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 80 {
		out = out[:80]
		out = strings.TrimRight(out, "-")
	}
	return out
}

// injectSlackEnvIntoSandbox runs an SSM SendCommand on instanceID that
// appends KM_SLACK_CHANNEL_ID and KM_SLACK_BRIDGE_URL to
// /etc/profile.d/km-notify-env.sh. Idempotent: uses grep/sed to avoid
// duplicate lines on re-runs. The export format matches the compile-time
// template in pkg/compiler/userdata.go.
func injectSlackEnvIntoSandbox(ctx context.Context, runner SSMRunner, instanceID, channelID, bridgeURL string) error {
	script := fmt.Sprintf(`ENV_FILE=/etc/profile.d/km-notify-env.sh
mkdir -p /etc/profile.d
touch "$ENV_FILE"
grep -q '^export KM_SLACK_CHANNEL_ID=' "$ENV_FILE" && sed -i 's|^export KM_SLACK_CHANNEL_ID=.*|export KM_SLACK_CHANNEL_ID="%s"|' "$ENV_FILE" || echo 'export KM_SLACK_CHANNEL_ID="%s"' >> "$ENV_FILE"
grep -q '^export KM_SLACK_BRIDGE_URL=' "$ENV_FILE" && sed -i 's|^export KM_SLACK_BRIDGE_URL=.*|export KM_SLACK_BRIDGE_URL="%s"|' "$ENV_FILE" || echo 'export KM_SLACK_BRIDGE_URL="%s"' >> "$ENV_FILE"
`, channelID, channelID, bridgeURL, bridgeURL)
	return runner.RunShell(ctx, instanceID, script)
}

// ssmSendCommandClient is the minimal SSM interface needed by productionSSMRunner.
type ssmSendCommandClient interface {
	SendCommand(ctx context.Context, input *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
}

// productionSSMRunner implements SSMRunner using AWS SSM SendCommand.
// Used by injectSlackEnvIntoSandbox to push env vars into a running sandbox.
type productionSSMRunner struct {
	client ssmSendCommandClient
}

func (r *productionSSMRunner) RunShell(ctx context.Context, instanceID string, script string) error {
	_, err := r.client.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: awssdk.String("AWS-RunShellScript"),
		Parameters: map[string][]string{
			"commands": {script},
		},
		TimeoutSeconds: awssdk.Int32(30),
	})
	return err
}

// runStep11dInject orchestrates the step 11d Slack env injection — extracted from
// create.go for testability. Reads bridge URL from ssmStore, terraform outputs from
// outputter, instance ID via extractor, and runs the bounded retry loop. Emits
// exactly one stderr line on every code path. Non-fatal: never returns an error.
//
// retryMax and retryDelay control the retry loop:
//   - Production: retryMax=6, retryDelay=5*time.Second (covers SSM agent boot window)
//   - Tests: retryDelay=time.Microsecond for fast wall-clock execution
func runStep11dInject(
	ctx context.Context,
	ssmStore SSMParamStore,
	runner SSMRunner,
	sandboxDir string,
	outputter func(ctx context.Context, dir string) (map[string]any, error),
	extractor func(map[string]any) string,
	sandboxID, slackChannelID string,
	retryMax int,
	retryDelay time.Duration,
) {
	bridgeURL, _ := ssmStore.Get(ctx, "/km/slack/bridge-url", false)
	if bridgeURL == "" {
		log.Warn().Str("sandbox_id", sandboxID).
			Msg("Step 11d: /km/slack/bridge-url not configured — Slack env not injected (run km slack init)")
		fmt.Fprintf(os.Stderr, "  ⚠ Slack: /km/slack/bridge-url not configured — env not injected (run km slack init)\n")
		return
	}

	outputs, outErr := outputter(ctx, sandboxDir)
	if outErr != nil {
		log.Warn().Err(outErr).Str("sandbox_id", sandboxID).
			Msg("Step 11d: failed to read sandbox outputs for Slack env inject (non-fatal)")
		fmt.Fprintf(os.Stderr, "  ⚠ Slack: failed to read terraform outputs — env not injected (non-fatal): %v\n", outErr)
		return
	}

	instanceID := extractor(outputs)
	if instanceID == "" {
		log.Warn().Str("sandbox_id", sandboxID).
			Msg("Step 11d: no EC2 instance ID in terraform outputs — Slack env not injected (docker/non-EC2 substrate)")
		fmt.Fprintf(os.Stderr, "  ⚠ Slack: no EC2 instance ID in outputs — env not injected (docker/non-EC2 substrate)\n")
		return
	}

	// Bounded retry loop: up to retryMax attempts with retryDelay between each.
	// Covers the SSM agent boot window — the agent may not be reachable when
	// runner.Output returns. Production: 6 × 5s = 30s max. Tests: Microsecond delay.
	var injectErr error
	for attempt := 1; attempt <= retryMax; attempt++ {
		injectErr = injectSlackEnvIntoSandbox(ctx, runner, instanceID, slackChannelID, bridgeURL)
		if injectErr == nil {
			break
		}
		if attempt < retryMax {
			time.Sleep(retryDelay)
		}
	}
	if injectErr != nil {
		log.Warn().Err(injectErr).Int("attempts", retryMax).Str("sandbox_id", sandboxID).
			Msg("Step 11d: failed to inject Slack env via SSM SendCommand after retries (non-fatal — sandbox is provisioned)")
		fmt.Fprintf(os.Stderr, "  ⚠ Slack: SSM SendCommand failed — env not injected (non-fatal): %v\n", injectErr)
		return
	}
	fmt.Fprintf(os.Stderr, "  ✓ Slack: channel %s wired into sandbox env\n", slackChannelID)
}
