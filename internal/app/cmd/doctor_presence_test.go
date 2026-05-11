package cmd

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// =============================================================================
// Test fakes
// =============================================================================

type fakeCWLogsFilter struct {
	events []cwltypes.FilteredLogEvent
	err    error

	// Captured input from the last FilterLogEvents call — used to assert
	// LogGroupName and FilterPattern shape from regression tests.
	gotLogGroup     string
	gotFilterPattern string
}

func (f *fakeCWLogsFilter) FilterLogEvents(ctx context.Context, in *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error) {
	if in.LogGroupName != nil {
		f.gotLogGroup = *in.LogGroupName
	}
	if in.FilterPattern != nil {
		f.gotFilterPattern = *in.FilterPattern
	}
	return &cloudwatchlogs.FilterLogEventsOutput{Events: f.events}, f.err
}

type fakeRunningSandboxLister struct {
	ids []string
	err error
}

func (f *fakeRunningSandboxLister) ListRunningSandboxIDs(ctx context.Context) ([]string, error) {
	return f.ids, f.err
}

// =============================================================================
// Failing stubs — Plan 79-04 turns these green by implementing the real check.
// =============================================================================

// TestDoctor_PresenceDaemonHealthy_OK expects CheckOK when the CW client
// returns at least one recent presence heartbeat for each sandbox.
// Wave 0: FAILS because the stub always returns CheckWarn.
func TestDoctor_PresenceDaemonHealthy_OK(t *testing.T) {
	msg := "sb-abc123"
	cw := &fakeCWLogsFilter{
		events: []cwltypes.FilteredLogEvent{
			{Message: &msg},
		},
	}
	lister := &fakeRunningSandboxLister{ids: []string{"sb-abc123"}}
	got := checkPresenceDaemonHealthy(context.Background(), cw, lister, "/km/audit")
	if got.Status != CheckOK {
		t.Fatalf("expected CheckOK, got %s: %s", got.Status, got.Message)
	}
}

// TestDoctor_PresenceDaemonHealthy_Stale expects CheckWarn containing the
// sandbox ID when the CW client returns no recent events for that sandbox.
// Wave 0: this test may coincidentally pass (stub returns WARN) but the
// Message check will fail because the stub doesn't embed the sandbox ID.
func TestDoctor_PresenceDaemonHealthy_Stale(t *testing.T) {
	cw := &fakeCWLogsFilter{events: nil}
	lister := &fakeRunningSandboxLister{ids: []string{"sb-stale01"}}
	got := checkPresenceDaemonHealthy(context.Background(), cw, lister, "/km/audit")
	if got.Status != CheckWarn {
		t.Fatalf("expected CheckWarn for stale sandbox, got %s", got.Status)
	}
	if got.Message == "" {
		t.Fatalf("expected WARN message to identify the stale sandbox")
	}
}

// TestDoctor_PresenceDaemonHealthy_Skipped expects CheckSkipped when the CW
// client is nil (Slack/CW not configured).
// Wave 0: FAILS because the stub always returns CheckWarn.
func TestDoctor_PresenceDaemonHealthy_Skipped(t *testing.T) {
	lister := &fakeRunningSandboxLister{ids: []string{"sb-abc123"}}
	got := checkPresenceDaemonHealthy(context.Background(), nil, lister, "/km/audit")
	if got.Status != CheckSkipped {
		t.Fatalf("expected CheckSkipped when CW client is nil, got %s", got.Status)
	}
}

// TestDoctor_PresenceDaemonHealthy_LogGroupAndFilterShape is a UAT-discovered
// regression test: the live km doctor check returned WARN for a sandbox that
// had emitted a valid presence event because (1) the log group concat dropped
// the trailing "/" required by CloudWatch (treated "/km/sandboxes/X" and
// "/km/sandboxes/X/" as different groups) and (2) the filter pattern
// `"source":"presence"` was rejected by the API as InvalidParameterException —
// CloudWatch Logs requires JSON-object syntax `{ $.source = "presence" }` for
// field matches.
func TestDoctor_PresenceDaemonHealthy_LogGroupAndFilterShape(t *testing.T) {
	cw := &fakeCWLogsFilter{}
	lister := &fakeRunningSandboxLister{ids: []string{"sb-xyz789"}}

	_ = checkPresenceDaemonHealthy(context.Background(), cw, lister, "/km/sandboxes/")

	wantLogGroup := "/km/sandboxes/sb-xyz789/"
	if cw.gotLogGroup != wantLogGroup {
		t.Errorf("log group: got %q, want %q (trailing slash is significant in CloudWatch)", cw.gotLogGroup, wantLogGroup)
	}

	wantFilterPattern := `{ $.source = "presence" }`
	if cw.gotFilterPattern != wantFilterPattern {
		t.Errorf("filter pattern: got %q, want %q (CW rejects bare \"source\":\"presence\")", cw.gotFilterPattern, wantFilterPattern)
	}
}
