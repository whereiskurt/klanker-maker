// Tests for checkStaleLambdas — orphan per-sandbox Lambda detection +
// optional cleanup gated on --delete-lambdas.
package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// fakeLambdaCleanup is a minimal LambdaCleanupAPI implementation for the
// tests. Returns the configured names from ListFunctions and records every
// DeleteFunction call. Per-name DeleteFunction errors via deleteErrFor.
type fakeLambdaCleanup struct {
	functionNames []string
	listErr       error
	deleteErr     error
	deleteErrFor  map[string]bool
	deleted       []string
	deleteCalls   []string
}

var _ LambdaCleanupAPI = (*fakeLambdaCleanup)(nil)

func (f *fakeLambdaCleanup) ListFunctions(_ context.Context, _ *lambda.ListFunctionsInput, _ ...func(*lambda.Options)) (*lambda.ListFunctionsOutput, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := &lambda.ListFunctionsOutput{}
	for _, n := range f.functionNames {
		out.Functions = append(out.Functions, lambdatypes.FunctionConfiguration{
			FunctionName: awssdk.String(n),
		})
	}
	return out, nil
}

func (f *fakeLambdaCleanup) DeleteFunction(_ context.Context, in *lambda.DeleteFunctionInput, _ ...func(*lambda.Options)) (*lambda.DeleteFunctionOutput, error) {
	name := awssdk.ToString(in.FunctionName)
	f.deleteCalls = append(f.deleteCalls, name)
	if f.deleteErr != nil && (f.deleteErrFor == nil || f.deleteErrFor[name]) {
		return nil, f.deleteErr
	}
	f.deleted = append(f.deleted, name)
	return &lambda.DeleteFunctionOutput{}, nil
}

// =============================================================================
// classifyLambda — pure naming-pattern parser
// =============================================================================

// TestClassifyLambda_KnownComponents verifies that the two per-sandbox
// component prefixes are recognized and the sandbox ID extracted correctly.
func TestClassifyLambda_KnownComponents(t *testing.T) {
	cases := []struct {
		name             string
		fnName           string
		resourcePrefix   string
		wantOK           bool
		wantComponent    string
		wantSandboxID    string
	}{
		{"budget-enforcer", "km-budget-enforcer-sb-abc123", "km", true, "budget-enforcer", "sb-abc123"},
		{"github-token-refresher", "km-github-token-refresher-sb-xyz789", "km", true, "github-token-refresher", "sb-xyz789"},
		{"non-default prefix", "kph-budget-enforcer-sb-aaa", "kph", true, "budget-enforcer", "sb-aaa"},
		{"sandbox ID with dashes (alias)", "km-budget-enforcer-my-poc-12345", "km", true, "budget-enforcer", "my-poc-12345"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, ok := classifyLambda(tc.fnName, tc.resourcePrefix)
			if ok != tc.wantOK {
				t.Fatalf("ok: got %v, want %v", ok, tc.wantOK)
			}
			if l.component != tc.wantComponent {
				t.Errorf("component: got %q, want %q", l.component, tc.wantComponent)
			}
			if l.sandboxID != tc.wantSandboxID {
				t.Errorf("sandboxID: got %q, want %q", l.sandboxID, tc.wantSandboxID)
			}
		})
	}
}

// TestClassifyLambda_PlatformLambdas verifies the safety property: platform
// (non-per-sandbox) Lambdas like {prefix}-create-handler, {prefix}-ttl-handler,
// {prefix}-slack-bridge must NOT be classified as per-sandbox. The check uses
// an allowlist (perSandboxLambdaComponents), not a denylist, precisely so a
// future platform Lambda can't be accidentally flagged stale.
func TestClassifyLambda_PlatformLambdas(t *testing.T) {
	platform := []string{
		"km-create-handler",
		"km-ttl-handler",
		"km-slack-bridge",
		"km-ecs-spot-handler",
		"km-email-create-handler",
		// Future platform Lambda that doesn't exist yet — must still be left alone.
		"km-some-future-platform-thing",
	}
	for _, fn := range platform {
		t.Run(fn, func(t *testing.T) {
			if _, ok := classifyLambda(fn, "km"); ok {
				t.Errorf("platform Lambda %q must NOT be classified as per-sandbox", fn)
			}
		})
	}
}

// TestClassifyLambda_NonKMPrefix verifies that Lambdas not owned by km (e.g.
// AWS service Lambdas, third-party functions, customer Lambdas in the same
// account) are left alone regardless of name shape.
func TestClassifyLambda_NonKMPrefix(t *testing.T) {
	nonKM := []string{
		"my-app-budget-enforcer-sb-abc",
		"prod-github-token-refresher-sb-xyz",
		"random-function",
	}
	for _, fn := range nonKM {
		t.Run(fn, func(t *testing.T) {
			if _, ok := classifyLambda(fn, "km"); ok {
				t.Errorf("non-km-prefixed Lambda %q must NOT be classified as per-sandbox", fn)
			}
		})
	}
}

// TestClassifyLambda_MissingSandboxID verifies that names matching a component
// prefix but with an empty trailing segment (e.g. "km-budget-enforcer-")
// are not classified — there's no sandbox ID to flag.
func TestClassifyLambda_MissingSandboxID(t *testing.T) {
	if _, ok := classifyLambda("km-budget-enforcer-", "km"); ok {
		t.Error("Lambda with empty sandbox ID suffix must NOT be classified")
	}
}

// =============================================================================
// checkStaleLambdas
// =============================================================================

func TestCheckStaleLambdas_NilClient_Skipped(t *testing.T) {
	r := checkStaleLambdas(context.Background(), nil, nil, true, false, "km")
	if r.Status != CheckSkipped {
		t.Fatalf("expected SKIPPED for nil client, got %s: %s", r.Status, r.Message)
	}
}

// TestCheckStaleLambdas_OnlyPlatformLambdas_OK: the account has only
// platform Lambdas (no per-sandbox ones). Result is OK, no work to do.
func TestCheckStaleLambdas_OnlyPlatformLambdas_OK(t *testing.T) {
	cli := &fakeLambdaCleanup{
		functionNames: []string{
			"km-create-handler",
			"km-ttl-handler",
			"km-slack-bridge",
		},
	}
	r := checkStaleLambdas(context.Background(), cli, &fakeSandboxLister{}, true, false, "km")
	if r.Status != CheckOK {
		t.Fatalf("expected OK with only platform Lambdas, got %s: %s", r.Status, r.Message)
	}
}

// TestCheckStaleLambdas_AllAlive_OK: every per-sandbox Lambda corresponds
// to a live sandbox in DDB.
func TestCheckStaleLambdas_AllAlive_OK(t *testing.T) {
	cli := &fakeLambdaCleanup{
		functionNames: []string{
			"km-budget-enforcer-sb-a",
			"km-github-token-refresher-sb-a",
			"km-budget-enforcer-sb-b",
		},
	}
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{
		{SandboxID: "sb-a"}, {SandboxID: "sb-b"},
	}}
	r := checkStaleLambdas(context.Background(), cli, lister, true, false, "km")
	if r.Status != CheckOK {
		t.Fatalf("expected OK when every Lambda has a live sandbox, got %s: %s", r.Status, r.Message)
	}
}

// TestCheckStaleLambdas_OrphanFound_DryRun_NoDestructiveCalls: orphan
// per-sandbox Lambda detected, dryRun=true → WARN, no DeleteFunction call.
func TestCheckStaleLambdas_OrphanFound_DryRun_NoDestructiveCalls(t *testing.T) {
	cli := &fakeLambdaCleanup{
		functionNames: []string{
			"km-budget-enforcer-sb-alive",
			"km-budget-enforcer-sb-ghost",
			"km-github-token-refresher-sb-ghost",
			"km-create-handler", // platform — must stay untouched
		},
	}
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{{SandboxID: "sb-alive"}}}
	r := checkStaleLambdas(context.Background(), cli, lister, true, true, "km")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "km-budget-enforcer-sb-ghost") || !strings.Contains(r.Message, "km-github-token-refresher-sb-ghost") {
		t.Errorf("expected both orphan Lambda names in message, got: %s", r.Message)
	}
	if strings.Contains(r.Message, "km-create-handler") {
		t.Errorf("platform Lambda km-create-handler must NOT appear in stale list, got: %s", r.Message)
	}
	if len(cli.deleteCalls) != 0 {
		t.Errorf("dryRun=true must NOT call DeleteFunction; saw %v", cli.deleteCalls)
	}
	if !strings.Contains(r.Remediation, "--dry-run=false --delete-lambdas") {
		t.Errorf("expected dry-run remediation to point at --dry-run=false --delete-lambdas, got: %s", r.Remediation)
	}
}

// TestCheckStaleLambdas_DryRunFalseWithoutDeleteLambdas_NoDestructiveCalls
// verifies the explicit-opt-in property: --dry-run=false alone is NOT enough
// to delete Lambdas — the operator must also pass --delete-lambdas. Same
// pattern as --delete-ebs/--delete-sqs/--delete-s3.
func TestCheckStaleLambdas_DryRunFalseWithoutDeleteLambdas_NoDestructiveCalls(t *testing.T) {
	cli := &fakeLambdaCleanup{
		functionNames: []string{"km-budget-enforcer-sb-ghost"},
	}
	r := checkStaleLambdas(context.Background(), cli, &fakeSandboxLister{}, false, false, "km")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	if len(cli.deleteCalls) != 0 {
		t.Errorf("--dry-run=false alone (without --delete-lambdas) must NOT call DeleteFunction; saw %v", cli.deleteCalls)
	}
	if !strings.Contains(r.Remediation, "--delete-lambdas") {
		t.Errorf("expected remediation to mention --delete-lambdas, got: %s", r.Remediation)
	}
	if strings.Contains(r.Remediation, "--dry-run=false") {
		t.Errorf("remediation in --dry-run=false mode shouldn't repeat the flag, got: %s", r.Remediation)
	}
}

// TestCheckStaleLambdas_DeleteLambdas_DeletesOrphans: --dry-run=false
// --delete-lambdas → orphan Lambdas are deleted; live and platform Lambdas
// are left alone.
func TestCheckStaleLambdas_DeleteLambdas_DeletesOrphans(t *testing.T) {
	cli := &fakeLambdaCleanup{
		functionNames: []string{
			"km-budget-enforcer-sb-alive",         // keep — alive
			"km-budget-enforcer-sb-ghost",         // delete — orphan
			"km-github-token-refresher-sb-ghost",  // delete — orphan
			"km-create-handler",                   // keep — platform
		},
	}
	lister := &fakeSandboxLister{records: []kmaws.SandboxRecord{{SandboxID: "sb-alive"}}}
	r := checkStaleLambdas(context.Background(), cli, lister, false, true, "km")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	if len(cli.deleted) != 2 {
		t.Errorf("expected 2 successful deletes, got %d: %v", len(cli.deleted), cli.deleted)
	}
	for _, n := range cli.deleted {
		if !strings.Contains(n, "sb-ghost") {
			t.Errorf("only sb-ghost Lambdas should be deleted, got: %s", n)
		}
	}
	if !strings.Contains(r.Message, "2 deleted, 0 failed") {
		t.Errorf("expected '2 deleted, 0 failed' summary, got: %s", r.Message)
	}
}

// TestCheckStaleLambdas_DeleteFailure_ReportedInline: DeleteFunction fails
// for one Lambda → tally records 1 failed; per-row marker shows the error.
// Doctor must not abort on per-Lambda failure.
func TestCheckStaleLambdas_DeleteFailure_ReportedInline(t *testing.T) {
	cli := &fakeLambdaCleanup{
		functionNames: []string{
			"km-budget-enforcer-sb-ok",
			"km-budget-enforcer-sb-fail",
		},
		deleteErr:    errors.New("ResourceConflictException"),
		deleteErrFor: map[string]bool{"km-budget-enforcer-sb-fail": true},
	}
	r := checkStaleLambdas(context.Background(), cli, &fakeSandboxLister{}, false, true, "km")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN, got %s: %s", r.Status, r.Message)
	}
	if len(cli.deleted) != 1 || cli.deleted[0] != "km-budget-enforcer-sb-ok" {
		t.Errorf("expected sb-ok to be the only successful delete, got: %v", cli.deleted)
	}
	if !strings.Contains(r.Message, "1 deleted, 1 failed") {
		t.Errorf("expected '1 deleted, 1 failed' summary, got: %s", r.Message)
	}
	if !strings.Contains(r.Message, "delete failed: ResourceConflictException") {
		t.Errorf("expected per-row failure marker, got: %s", r.Message)
	}
}

// TestCheckStaleLambdas_ListFunctionsError_Warn: ListFunctions returns
// AccessDenied → WARN with the error surfaced; doctor doesn't fail.
func TestCheckStaleLambdas_ListFunctionsError_Warn(t *testing.T) {
	cli := &fakeLambdaCleanup{listErr: errors.New("AccessDenied")}
	r := checkStaleLambdas(context.Background(), cli, nil, true, false, "km")
	if r.Status != CheckWarn {
		t.Fatalf("expected WARN on ListFunctions error, got %s: %s", r.Status, r.Message)
	}
	if !strings.Contains(r.Message, "AccessDenied") {
		t.Errorf("expected error text in message, got: %s", r.Message)
	}
}
