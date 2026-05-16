package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// slackInboundProfileWithPrefix returns a SandboxProfile with NotifySlackInboundEnabled=true
// for use with prefix-aware table-name tests.
func slackInboundProfileWithPrefix(t *testing.T) *profile.SandboxProfile {
	t.Helper()
	slackEnabled := true
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{
		NotifySlackEnabled:        &slackEnabled,
		NotifySlackPerSandbox:     true,
		NotifySlackInboundEnabled: true,
	}
	return p
}

// slackTranscriptProfileWithPrefix returns a SandboxProfile with
// NotifySlackTranscriptEnabled=true for use with stream-messages table-name tests.
func slackTranscriptProfileWithPrefix(t *testing.T) *profile.SandboxProfile {
	t.Helper()
	slackEnabled := true
	p := baseProfile()
	p.Spec.CLI = &profile.CLISpec{
		NotifySlackEnabled:            &slackEnabled,
		NotifySlackPerSandbox:         true,
		NotifySlackTranscriptEnabled:  true,
	}
	return p
}

// TestCompile_SlackInboundTableName verifies that when KM_RESOURCE_PREFIX=rg,
// generateUserData emits "rg-slack-threads" (not "km-slack-threads") in the
// userdata output for profiles with NotifySlackInboundEnabled=true.
func TestCompile_SlackInboundTableName(t *testing.T) {
	t.Setenv("KM_RESOURCE_PREFIX", "rg")
	// Unset KM_SLACK_THREADS_TABLE to force the prefix-derived path.
	t.Setenv("KM_SLACK_THREADS_TABLE", "")

	p := slackInboundProfileWithPrefix(t)
	out, err := generateUserData(p, "sb-82-02-inbound", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	if !strings.Contains(out, "rg-slack-threads") {
		t.Errorf("expected 'rg-slack-threads' in userdata when KM_RESOURCE_PREFIX=rg\n--- first 500 bytes ---\n%s", abbreviateUD(out))
	}
	if strings.Contains(out, "km-slack-threads") {
		t.Errorf("expected NO 'km-slack-threads' literal when KM_RESOURCE_PREFIX=rg; found one\n--- first 500 bytes ---\n%s", abbreviateUD(out))
	}
}

// TestCompile_SlackStreamTableName verifies that when KM_RESOURCE_PREFIX=rg,
// generateUserData emits "rg-slack-stream-messages" (not "km-slack-stream-messages")
// for profiles with NotifySlackTranscriptEnabled=true.
func TestCompile_SlackStreamTableName(t *testing.T) {
	t.Setenv("KM_RESOURCE_PREFIX", "rg")
	// Unset KM_SLACK_STREAM_TABLE to force the prefix-derived path.
	t.Setenv("KM_SLACK_STREAM_TABLE", "")

	p := slackTranscriptProfileWithPrefix(t)
	out, err := generateUserData(p, "sb-82-02-stream", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData failed: %v", err)
	}

	if !strings.Contains(out, "rg-slack-stream-messages") {
		t.Errorf("expected 'rg-slack-stream-messages' in userdata when KM_RESOURCE_PREFIX=rg\n--- first 500 bytes ---\n%s", abbreviateUD(out))
	}
	if strings.Contains(out, "km-slack-stream-messages") {
		t.Errorf("expected NO 'km-slack-stream-messages' literal when KM_RESOURCE_PREFIX=rg; found one\n--- first 500 bytes ---\n%s", abbreviateUD(out))
	}
}
