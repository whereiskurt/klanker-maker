package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// minimalWebhookInboundProfile returns a SandboxProfile with the minimum fields
// required for webhook inbound tests. inbound controls
// NotificationWebhookInboundSpec.Enabled. A non-nil spec.cli block is included
// deliberately — poller/notify.env emission historically gated on Spec.CLI != nil
// (project_notify_setup_gated_on_spec_cli); a fixture without it would fail for a
// reason unrelated to the feature under test.
func minimalWebhookInboundProfile(t *testing.T, inbound bool) *profile.SandboxProfile {
	t.Helper()
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{}
	p.Spec.Notification = &profile.NotificationSpec{
		Webhook: &profile.NotificationWebhookSpec{
			Inbound: &profile.NotificationWebhookInboundSpec{Enabled: boolPtr(inbound)},
		},
	}
	return p
}

// compileWebhookInboundUserData is a thin wrapper around generateUserData for webhook inbound tests.
func compileWebhookInboundUserData(t *testing.T, p *profile.SandboxProfile) string {
	t.Helper()
	out, err := generateUserData(p, "sb-webhook-test", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}
	return out
}

// extractWebhookInboundPoller returns the WEBHOOKINBOUND heredoc body from rendered userdata.
func extractWebhookInboundPoller(t *testing.T, out string) string {
	t.Helper()
	startMarker := "<< 'WEBHOOKINBOUND'"
	endMarker := "\nWEBHOOKINBOUND\n"
	start := strings.Index(out, startMarker)
	end := strings.Index(out, endMarker)
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("WEBHOOKINBOUND heredoc markers not found in rendered userdata\n--- excerpt ---\n%s", abbreviateUD(out))
	}
	return out[start:end]
}

// TestUserData_WebhookPollerEmittedWhenEnabled verifies that when webhook-inbound
// is enabled the user-data string contains the poller, its SSM fallback path, the
// queue-url env var, and the source-aware preamble.
func TestUserData_WebhookPollerEmittedWhenEnabled(t *testing.T) {
	p := minimalWebhookInboundProfile(t, true)
	out := compileWebhookInboundUserData(t, p)

	if !strings.Contains(out, "km-webhook-inbound-poller") {
		t.Fatal("poller must be installed when webhook.inbound.enabled")
	}
	if !strings.Contains(out, "webhook-inbound-queue-url") {
		t.Error("poller must carry the SSM fallback path")
	}
	if !strings.Contains(out, "KM_WEBHOOK_INBOUND_QUEUE_URL") {
		t.Error("env file must export KM_WEBHOOK_INBOUND_QUEUE_URL")
	}
	poller := extractWebhookInboundPoller(t, out)
	if !strings.Contains(poller, "[Webhook Trigger]") {
		t.Error("poller must render the source-aware preamble")
	}
}

// TestUserData_WebhookPollerAbsentWhenDisabled verifies dormancy: a disabled
// webhook-inbound block emits none of the poller/unit/env-var machinery.
func TestUserData_WebhookPollerAbsentWhenDisabled(t *testing.T) {
	p := minimalWebhookInboundProfile(t, false)
	out := compileWebhookInboundUserData(t, p)

	forbidden := []string{
		"km-webhook-inbound-poller",
		"KM_WEBHOOK_INBOUND_QUEUE_URL",
		"km-webhook-inbound-poller.service",
		"<< 'WEBHOOKINBOUND'",
		"<< 'WEBHOOKINBOUNDUNIT'",
	}
	for _, s := range forbidden {
		if strings.Contains(out, s) {
			t.Fatalf("disabled webhook inbound must emit no poller (dormancy); found %q\n--- excerpt ---\n%s", s, abbreviateUD(out))
		}
	}
}

// TestUserData_WebhookPoller_EnvelopeFields verifies the poller parses every
// QueueEnvelope field the Task 6 bridge marshals (pkg/webhook/bridge/handler.go):
// source, type, id, severity, title, url, prompt, raw.
func TestUserData_WebhookPoller_EnvelopeFields(t *testing.T) {
	p := minimalWebhookInboundProfile(t, true)
	out := compileWebhookInboundUserData(t, p)
	poller := extractWebhookInboundPoller(t, out)

	envFields := []string{
		`.source`,
		`.type`,
		`.id`,
		`.severity`,
		`.title`,
		`.url`,
		`.prompt`,
	}
	for _, f := range envFields {
		if !strings.Contains(poller, f) {
			t.Fatalf("poller missing envelope field %q\n%s", f, abbreviateUD(poller))
		}
	}
}

// TestUserData_WebhookPoller_Dispatch verifies the poller dispatches a fresh
// (no-resume) agent turn — a webhook trigger has no thread continuity, so there
// must be no --resume/session-lookup machinery anywhere in this poller.
func TestUserData_WebhookPoller_Dispatch(t *testing.T) {
	p := minimalWebhookInboundProfile(t, true)
	out := compileWebhookInboundUserData(t, p)
	poller := extractWebhookInboundPoller(t, out)

	if !strings.Contains(poller, "claude -p") {
		t.Fatalf("poller missing claude -p dispatch\n%s", abbreviateUD(poller))
	}
	if !strings.Contains(poller, "cd /workspace") {
		t.Fatalf("poller missing cd /workspace before dispatch\n%s", abbreviateUD(poller))
	}
	if strings.Contains(poller, "--resume") {
		t.Errorf("webhook turns have no thread continuity; poller must not pass --resume\n%s", abbreviateUD(poller))
	}
}

// TestUserData_WebhookPoller_QueueDrain verifies the poller drains the FIFO
// queue and acks (deletes) only after a successful turn.
func TestUserData_WebhookPoller_QueueDrain(t *testing.T) {
	p := minimalWebhookInboundProfile(t, true)
	out := compileWebhookInboundUserData(t, p)
	poller := extractWebhookInboundPoller(t, out)

	must := []string{
		"aws sqs receive-message",
		"aws sqs delete-message",
		"QUEUE_URL",
		"RECEIPT",
	}
	for _, s := range must {
		if !strings.Contains(poller, s) {
			t.Fatalf("poller missing queue drain subprocess %q\n%s", s, abbreviateUD(poller))
		}
	}

	// Ack-after-completion: the delete-message call for a successful turn must be
	// textually AFTER the agent dispatch, never before.
	dispatchIdx := strings.Index(poller, "claude -p")
	deleteIdx := strings.LastIndex(poller, "aws sqs delete-message")
	if dispatchIdx < 0 || deleteIdx < 0 || deleteIdx < dispatchIdx {
		t.Fatalf("delete-message must occur after the agent dispatch (ack-after-completion)\n%s", abbreviateUD(poller))
	}
}

// TestUserData_WebhookPoller_SSMFallback verifies the poller falls back to SSM
// Parameter Store when KM_WEBHOOK_INBOUND_QUEUE_URL is empty.
func TestUserData_WebhookPoller_SSMFallback(t *testing.T) {
	p := minimalWebhookInboundProfile(t, true)
	out := compileWebhookInboundUserData(t, p)
	poller := extractWebhookInboundPoller(t, out)

	if !strings.Contains(poller, "webhook-inbound-queue-url") {
		t.Fatalf("poller missing SSM fallback path webhook-inbound-queue-url\n%s", abbreviateUD(poller))
	}
	if !strings.Contains(poller, "attempt") {
		t.Fatalf("poller missing SSM retry loop\n%s", abbreviateUD(poller))
	}
}

// TestUserData_WebhookPoller_SystemdUnit verifies the systemd unit is emitted
// when webhook-inbound is enabled, and its EnvironmentFile/ExecStart lines.
func TestUserData_WebhookPoller_SystemdUnit(t *testing.T) {
	p := minimalWebhookInboundProfile(t, true)
	out := compileWebhookInboundUserData(t, p)

	unitStart := strings.Index(out, "<< 'WEBHOOKINBOUNDUNIT'")
	if unitStart < 0 {
		t.Fatalf("WEBHOOKINBOUNDUNIT systemd unit heredoc not found")
	}
	unitEnd := strings.Index(out[unitStart:], "WEBHOOKINBOUNDUNIT\n")
	if unitEnd < 0 {
		t.Fatalf("WEBHOOKINBOUNDUNIT unit block has no closing delimiter")
	}
	unit := out[unitStart : unitStart+unitEnd]

	if !strings.Contains(unit, "EnvironmentFile=-/etc/km/notify.env") {
		t.Fatalf("km-webhook-inbound-poller.service must reference EnvironmentFile=-/etc/km/notify.env\n%s", unit)
	}
	if !strings.Contains(unit, "ExecStart=/opt/km/bin/km-webhook-inbound-poller") {
		t.Fatalf("km-webhook-inbound-poller.service must ExecStart the poller binary\n%s", unit)
	}
}

// TestUserData_WebhookPoller_SystemctlEnable verifies that when webhook-inbound
// is enabled the systemctl enable line contains km-webhook-inbound-poller.
func TestUserData_WebhookPoller_SystemctlEnable(t *testing.T) {
	p := minimalWebhookInboundProfile(t, true)
	out := compileWebhookInboundUserData(t, p)

	found := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "systemctl enable") &&
			strings.Contains(line, "km-webhook-inbound-poller") {
			if strings.Contains(line, "  ") {
				t.Fatalf("malformed systemctl line (double space): %q", line)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("did not find systemctl enable line containing km-webhook-inbound-poller\n%s", abbreviateUD(out))
	}
}

// TestUserData_WebhookPoller_ExportsAWSRegion verifies the poller exports
// AWS_REGION before the while-loop so subprocesses inherit it.
func TestUserData_WebhookPoller_ExportsAWSRegion(t *testing.T) {
	p := minimalWebhookInboundProfile(t, true)
	out := compileWebhookInboundUserData(t, p)
	poller := extractWebhookInboundPoller(t, out)

	if !strings.Contains(poller, `export AWS_REGION="$REGION"`) {
		t.Fatalf("poller missing 'export AWS_REGION=$REGION'\n%s", abbreviateUD(poller))
	}
	loopIdx := strings.Index(poller, "while true")
	if loopIdx < 0 {
		t.Fatalf("poller while-loop not found")
	}
	if !strings.Contains(poller[:loopIdx], `export AWS_REGION="$REGION"`) {
		t.Fatalf("AWS_REGION export must occur BEFORE while-loop (startup, not per-turn)")
	}
}

// TestUserData_WebhookPoller_NoReplyBack verifies the one-way-source invariant:
// this poller must never invoke any of the reply-back helper binaries the other
// source-aware pollers use (km-github, km-slack, km-h1) — a webhook trigger has
// no back-channel to post to.
func TestUserData_WebhookPoller_NoReplyBack(t *testing.T) {
	p := minimalWebhookInboundProfile(t, true)
	out := compileWebhookInboundUserData(t, p)
	poller := extractWebhookInboundPoller(t, out)

	forbidden := []string{"km-github ", "km-slack ", "km-h1 ", "reply_to", "reply-to"}
	for _, s := range forbidden {
		if strings.Contains(poller, s) {
			t.Errorf("webhook poller is one-way (no reply-back leg); must not reference %q\n%s", s, abbreviateUD(poller))
		}
	}
}

// TestUserData_SlackPollerUnaffectedByWebhookInbound verifies that enabling
// webhook-inbound does NOT affect the Slack poller (dormant byte-identity for
// Slack when not configured).
func TestUserData_SlackPollerUnaffectedByWebhookInbound(t *testing.T) {
	p := minimalWebhookInboundProfile(t, true)
	out := compileWebhookInboundUserData(t, p)

	if strings.Contains(out, "<< 'SLACKINBOUND'") {
		t.Fatalf("Slack poller heredoc (SLACKINBOUND) must not be emitted when only webhook-inbound is enabled\n%s", abbreviateUD(out))
	}
	if strings.Contains(out, "<< 'SLACKINBOUNDUNIT'") {
		t.Fatalf("Slack poller systemd unit (SLACKINBOUNDUNIT) must not be emitted when only webhook-inbound is enabled\n%s", abbreviateUD(out))
	}
}

// TestUserData_WebhookPoller_EnvVar verifies KM_WEBHOOK_INBOUND_QUEUE_URL is
// emitted only when webhook-inbound is enabled.
func TestUserData_WebhookPoller_EnvVar(t *testing.T) {
	p := minimalWebhookInboundProfile(t, true)
	out := compileWebhookInboundUserData(t, p)
	if !strings.Contains(out, "KM_WEBHOOK_INBOUND_QUEUE_URL=") {
		t.Fatalf("env file must export KM_WEBHOOK_INBOUND_QUEUE_URL when webhook-inbound enabled\n%s", abbreviateUD(out))
	}

	p2 := minimalWebhookInboundProfile(t, false)
	out2 := compileWebhookInboundUserData(t, p2)
	if strings.Contains(out2, "KM_WEBHOOK_INBOUND_QUEUE_URL") {
		t.Fatalf("disabled webhook-inbound must not export KM_WEBHOOK_INBOUND_QUEUE_URL")
	}
}

// TestUserData_WebhookPoller_NoCLIBlockStillEmits verifies the Phase 120 fix
// (notifyConfigured broadened beyond Spec.CLI != nil) also covers webhook: a
// profile with notification.webhook.inbound.enabled but NO spec.cli block must
// still get the poller + the queue-url env slot (project_notify_setup_gated_on_spec_cli
// footgun — "👀 but never replies" for GitHub/Slack/H1; the equivalent gap here would
// be "queue fills up and nothing ever drains it").
func TestUserData_WebhookPoller_NoCLIBlockStillEmits(t *testing.T) {
	p := baseProfile()
	p.Spec.Notification = &profile.NotificationSpec{
		Webhook: &profile.NotificationWebhookSpec{
			Inbound: &profile.NotificationWebhookInboundSpec{Enabled: boolPtr(true)},
		},
	}
	out := compileWebhookInboundUserData(t, p)
	if !strings.Contains(out, "km-webhook-inbound-poller") {
		t.Fatal("webhook poller must be emitted even without a spec.cli block")
	}
	if !strings.Contains(out, "KM_WEBHOOK_INBOUND_QUEUE_URL=") {
		t.Fatal("KM_WEBHOOK_INBOUND_QUEUE_URL must be emitted even without a spec.cli block")
	}
}
