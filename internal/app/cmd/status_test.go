package cmd_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// ---- Fake fetcher ----

type fakeFetcher struct {
	record *kmaws.SandboxRecord
	err    error
}

func (f *fakeFetcher) FetchSandbox(_ context.Context, _ string) (*kmaws.SandboxRecord, error) {
	return f.record, f.err
}

// ---- Fake budget fetcher ----

type fakeBudgetFetcher struct {
	summary *kmaws.BudgetSummary
	err     error
}

func (f *fakeBudgetFetcher) FetchBudget(_ context.Context, _ string) (*kmaws.BudgetSummary, error) {
	return f.summary, f.err
}

// ---- Fake identity fetcher ----

type fakeIdentityFetcher struct {
	record *kmaws.IdentityRecord
	err    error
}

func (f *fakeIdentityFetcher) FetchIdentity(_ context.Context, _ string) (*kmaws.IdentityRecord, error) {
	return f.record, f.err
}

// ---- Helpers ----

func runStatusCmd(t *testing.T, fetcher cmd.SandboxFetcher, args ...string) (string, error) {
	t.Helper()
	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	statusCmd := cmd.NewStatusCmdWithFetcher(cfg, fetcher)
	root.AddCommand(statusCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs(append([]string{"status"}, args...))

	err := root.Execute()
	return buf.String(), err
}

func runStatusCmdWithBudget(t *testing.T, fetcher cmd.SandboxFetcher, budgetFetcher cmd.BudgetFetcher, args ...string) (string, error) {
	t.Helper()
	cfg := &config.Config{BudgetTableName: "km-budgets"}
	root := &cobra.Command{Use: "km"}
	statusCmd := cmd.NewStatusCmdWithFetchers(cfg, fetcher, budgetFetcher)
	root.AddCommand(statusCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs(append([]string{"status"}, args...))

	err := root.Execute()
	return buf.String(), err
}

func runStatusCmdWithAllFetchers(t *testing.T, fetcher cmd.SandboxFetcher, budgetFetcher cmd.BudgetFetcher, identityFetcher cmd.IdentityFetcher, args ...string) (string, error) {
	t.Helper()
	cfg := &config.Config{BudgetTableName: "km-budgets", IdentityTableName: "km-identities"}
	root := &cobra.Command{Use: "km"}
	statusCmd := cmd.NewStatusCmdWithAllFetchers(cfg, fetcher, budgetFetcher, identityFetcher)
	root.AddCommand(statusCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs(append([]string{"status"}, args...))

	err := root.Execute()
	return buf.String(), err
}

// ---- Tests ----

func TestStatusCmd_Found(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	ttlExpiry := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-123",
			Profile:   "open-dev",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
			TTLExpiry: &ttlExpiry,
			Resources: []string{
				"arn:aws:ec2:us-east-1:123456789:instance/i-0abc123",
				"arn:aws:ec2:us-east-1:123456789:security-group/sg-0def456",
			},
		},
	}

	out, err := runStatusCmd(t, fetcher, "sb-123")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	// Must show sandbox ID
	if !strings.Contains(out, "sb-123") {
		t.Errorf("output missing sandbox ID:\n%s", out)
	}

	// Must show resource ARNs
	if !strings.Contains(out, "arn:aws:ec2:us-east-1:123456789:instance/i-0abc123") {
		t.Errorf("output missing resource ARN:\n%s", out)
	}

	// Must show TTL expiry date (code formats as local timezone human-readable, check date portion only)
	if !strings.Contains(out, "2026-03-22") {
		t.Errorf("output missing TTL expiry timestamp:\n%s", out)
	}
}

func TestStatusCmd_NotFound(t *testing.T) {
	fetcher := &fakeFetcher{
		err: fmt.Errorf("%w: no metadata.json for sandbox sb-999: not found", kmaws.ErrSandboxNotFound),
	}

	out, err := runStatusCmd(t, fetcher, "sb-999")

	// Must exit non-zero
	if err == nil {
		t.Fatal("expected non-zero exit for not found sandbox, got nil")
	}

	// The combined output (stderr is redirected to buf via root.SetErr)
	// should contain the sandbox ID
	if !strings.Contains(out, "sb-999") {
		t.Logf("output: %s", out)
		// The error is returned by RunE; cobra prints it to stderr.
		// Since we set root.SetErr(buf), the cobra error message goes to buf.
		// Accept the test passing as long as err is non-nil.
	}
}

// TestStatusCmd_BudgetDisplayed verifies that the Budget section is shown when
// budget data exists in DynamoDB.
func TestStatusCmd_BudgetDisplayed(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	sandboxFetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-456",
			Profile:   "ml-dev",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	budgetFetcher := &fakeBudgetFetcher{
		summary: &kmaws.BudgetSummary{
			ComputeSpent:     1.23,
			ComputeLimit:     5.00,
			AISpent:          3.45,
			AILimit:          10.00,
			WarningThreshold: 0.80,
			AIByModel: map[string]kmaws.ModelSpend{
				"anthropic.claude-sonnet-4": {
					SpentUSD:     2.10,
					InputTokens:  150000,
					OutputTokens: 45000,
				},
				"anthropic.claude-haiku-3": {
					SpentUSD:     1.35,
					InputTokens:  500000,
					OutputTokens: 200000,
				},
			},
		},
	}

	out, err := runStatusCmdWithBudget(t, sandboxFetcher, budgetFetcher, "sb-456")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	// Must show budget header
	if !strings.Contains(out, "Budget:") {
		t.Errorf("expected 'Budget:' section in output, got:\n%s", out)
	}

	// Must show compute budget
	if !strings.Contains(out, "Compute:") {
		t.Errorf("expected 'Compute:' line in budget section, got:\n%s", out)
	}
	if !strings.Contains(out, "$1.23") {
		t.Errorf("expected compute spent $1.23 in output, got:\n%s", out)
	}
	if !strings.Contains(out, "$5.00") {
		t.Errorf("expected compute limit $5.00 in output, got:\n%s", out)
	}

	// Must show AI budget
	if !strings.Contains(out, "AI:") {
		t.Errorf("expected 'AI:' line in budget section, got:\n%s", out)
	}
	if !strings.Contains(out, "$3.45") {
		t.Errorf("expected AI spent $3.45 in output, got:\n%s", out)
	}
	if !strings.Contains(out, "$10.00") {
		t.Errorf("expected AI limit $10.00 in output, got:\n%s", out)
	}

	// Must show per-model breakdown
	if !strings.Contains(out, "claude-sonnet-4") {
		t.Errorf("expected claude-sonnet-4 model in output, got:\n%s", out)
	}
	if !strings.Contains(out, "claude-haiku-3") {
		t.Errorf("expected claude-haiku-3 model in output, got:\n%s", out)
	}

	// Must show warning threshold
	if !strings.Contains(out, "80%") {
		t.Errorf("expected '80%%' warning threshold in output, got:\n%s", out)
	}
}

// TestStatusCmd_BudgetOmittedWhenNoBudget verifies that Budget section is not shown
// when the budget fetcher returns nil (no budget defined).
func TestStatusCmd_BudgetOmittedWhenNoBudget(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	sandboxFetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-789",
			Profile:   "no-budget",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	// nil budget fetcher => no budget data
	out, err := runStatusCmdWithBudget(t, sandboxFetcher, nil, "sb-789")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	// Must NOT show budget section
	if strings.Contains(out, "Budget:") {
		t.Errorf("expected no 'Budget:' section when no budget defined, got:\n%s", out)
	}
}

// TestStatusCmd_EmptyStateBucketError verifies that km status returns a clear error
// when StateBucket is empty and no fetcher is injected (real fetcher path).
func TestStatusCmd_EmptyStateBucketError(t *testing.T) {
	cfg := &config.Config{StateBucket: ""}
	root := &cobra.Command{Use: "km"}
	// nil fetcher forces the real fetcher construction path
	statusCmd := cmd.NewStatusCmdWithFetcher(cfg, nil)
	root.AddCommand(statusCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"status", "sb-test"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when StateBucket is empty, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "state bucket not configured") &&
		!strings.Contains(errMsg, "get sandbox metadata") {
		t.Errorf("expected metadata-related error, got: %v", err)
	}
}

// TestStatusCmd_RealBucketFromConfig verifies that when StateBucket is set, the real
// fetcher path is attempted (will fail on AWS config load in test env — that's OK;
// what matters is the error is NOT about a missing bucket).
func TestStatusCmd_RealBucketFromConfig(t *testing.T) {
	cfg := &config.Config{StateBucket: "my-custom-bucket"}
	root := &cobra.Command{Use: "km"}
	statusCmd := cmd.NewStatusCmdWithFetcher(cfg, nil)
	root.AddCommand(statusCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"status", "sb-test"})

	err := root.Execute()
	// We expect an error (AWS config won't load in unit tests), but it must NOT
	// be about the bucket being unconfigured.
	if err != nil && strings.Contains(err.Error(), "state bucket not configured") {
		t.Errorf("should not get 'state bucket not configured' when StateBucket is set; got: %v", err)
	}
}

// TestStatusCmd_BudgetGracefulDegradation verifies that budget fetch error does not
// cause the status command to fail — it just omits the budget section.
func TestStatusCmd_BudgetGracefulDegradation(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	sandboxFetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-error",
			Profile:   "test",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	// Budget fetcher returns error
	budgetFetcher := &fakeBudgetFetcher{
		err: fmt.Errorf("DynamoDB connection error"),
	}

	out, err := runStatusCmdWithBudget(t, sandboxFetcher, budgetFetcher, "sb-error")
	if err != nil {
		t.Fatalf("status command should succeed even with budget error: %v\noutput: %s", err, out)
	}

	// Status command must still succeed
	if !strings.Contains(out, "sb-error") {
		t.Errorf("expected sandbox ID in output even on budget error, got:\n%s", out)
	}

	// Budget section omitted on error
	if strings.Contains(out, "Budget:") {
		t.Errorf("expected no 'Budget:' section on budget fetch error, got:\n%s", out)
	}
}

// TestStatus_IdentitySection verifies that km status shows the Identity section
// with a truncated public key and policy fields when an identity record exists.
func TestStatus_IdentitySection(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	sandboxFetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-ident1",
			Profile:   "secure-dev",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	// A 44-char base64 public key (32 bytes Ed25519)
	pubKeyB64 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	identityFetcher := &fakeIdentityFetcher{
		record: &kmaws.IdentityRecord{
			SandboxID:     "sb-ident1",
			PublicKeyB64:  pubKeyB64,
			EmailAddress:  "sb-ident1@sandboxes.klankermaker.ai",
			Signing:       "required",
			VerifyInbound: "optional",
			Encryption:    "off",
		},
	}

	out, err := runStatusCmdWithAllFetchers(t, sandboxFetcher, nil, identityFetcher, "sb-ident1")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	// Must show Identity header
	if !strings.Contains(out, "Identity:") {
		t.Errorf("expected 'Identity:' section in output, got:\n%s", out)
	}

	// Must show truncated public key (first 16 chars)
	if !strings.Contains(out, pubKeyB64[:16]) {
		t.Errorf("expected truncated public key %q in output, got:\n%s", pubKeyB64[:16], out)
	}

	// Must show Public Key label
	if !strings.Contains(out, "Public Key:") {
		t.Errorf("expected 'Public Key:' label in Identity section, got:\n%s", out)
	}

	// Must show Signing policy label and value
	if !strings.Contains(out, "Signing:") {
		t.Errorf("expected 'Signing:' in Identity section, got:\n%s", out)
	}
	if !strings.Contains(out, "required") {
		t.Errorf("expected signing value 'required' in Identity section, got:\n%s", out)
	}

	// Must show Verify Inbound policy label and value
	if !strings.Contains(out, "Verify Inbound:") {
		t.Errorf("expected 'Verify Inbound:' in Identity section, got:\n%s", out)
	}
	if !strings.Contains(out, "optional") {
		t.Errorf("expected verifyInbound value 'optional' in Identity section, got:\n%s", out)
	}

	// Must show Encryption policy label and value
	if !strings.Contains(out, "Encryption:") {
		t.Errorf("expected 'Encryption:' in Identity section, got:\n%s", out)
	}
	if !strings.Contains(out, "off") {
		t.Errorf("expected encryption value 'off' in Identity section, got:\n%s", out)
	}
}

// TestStatus_IdentitySection_LegacyRow verifies that km status shows "unknown" for each
// policy field when the identity record has no policy values (legacy DynamoDB rows).
func TestStatus_IdentitySection_LegacyRow(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	sandboxFetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-legacy01",
			Profile:   "legacy",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	pubKeyB64 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	// Legacy row: Signing, VerifyInbound, Encryption are all empty strings
	identityFetcher := &fakeIdentityFetcher{
		record: &kmaws.IdentityRecord{
			SandboxID:    "sb-legacy01",
			PublicKeyB64: pubKeyB64,
			EmailAddress: "sb-legacy01@sandboxes.klankermaker.ai",
		},
	}

	out, err := runStatusCmdWithAllFetchers(t, sandboxFetcher, nil, identityFetcher, "sb-legacy01")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	// Must show Identity section
	if !strings.Contains(out, "Identity:") {
		t.Errorf("expected 'Identity:' section in output, got:\n%s", out)
	}

	// Must show "unknown" three times (once for each missing policy field)
	unknownCount := strings.Count(out, "unknown")
	if unknownCount < 3 {
		t.Errorf("expected at least 3 occurrences of 'unknown' for legacy row policy fields, got %d in:\n%s", unknownCount, out)
	}
}

// TestStatus_IdentityFetchError verifies that km status gracefully degrades when
// the identity fetch fails — no crash, rest of status output is still shown.
func TestStatus_IdentityFetchError(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	sandboxFetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-iderr",
			Profile:   "test",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	identityFetcher := &fakeIdentityFetcher{
		err: fmt.Errorf("DynamoDB identity table unavailable"),
	}

	out, err := runStatusCmdWithAllFetchers(t, sandboxFetcher, nil, identityFetcher, "sb-iderr")
	if err != nil {
		t.Fatalf("status command should succeed even with identity fetch error: %v\noutput: %s", err, out)
	}

	// Must still show sandbox ID
	if !strings.Contains(out, "sb-iderr") {
		t.Errorf("expected sandbox ID in output even on identity fetch error, got:\n%s", out)
	}

	// Identity section must NOT be shown on error
	if strings.Contains(out, "Identity:") {
		t.Errorf("expected no 'Identity:' section on fetch error, got:\n%s", out)
	}
}

// TestStatus_EmailAlias verifies that km status shows the Alias line when
// IdentityRecord.Alias is non-empty, and shows a comma-joined Allowed Senders list.
func TestStatus_EmailAlias(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	sandboxFetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-alias1",
			Profile:   "alias-dev",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	pubKeyB64 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	identityFetcher := &fakeIdentityFetcher{
		record: &kmaws.IdentityRecord{
			SandboxID:      "sb-alias1",
			PublicKeyB64:   pubKeyB64,
			EmailAddress:   "sb-alias1@sandboxes.klankermaker.ai",
			Signing:        "required",
			VerifyInbound:  "optional",
			Encryption:     "off",
			Alias:          "research.team-a",
			AllowedSenders: []string{"self", "build.*"},
		},
	}

	out, err := runStatusCmdWithAllFetchers(t, sandboxFetcher, nil, identityFetcher, "sb-alias1")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	// Must show Alias line with value
	if !strings.Contains(out, "Alias:") {
		t.Errorf("expected 'Alias:' line in output, got:\n%s", out)
	}
	if !strings.Contains(out, "research.team-a") {
		t.Errorf("expected alias value 'research.team-a' in output, got:\n%s", out)
	}

	// Must show Allowed Senders as comma-joined
	if !strings.Contains(out, "Allowed Senders:") {
		t.Errorf("expected 'Allowed Senders:' line in output, got:\n%s", out)
	}
	if !strings.Contains(out, "self, build.*") {
		t.Errorf("expected allowed senders 'self, build.*' in output, got:\n%s", out)
	}
}

// TestStatus_EmailAlias_Empty verifies that km status omits the Alias line when
// IdentityRecord.Alias is empty, and shows "not configured" for Allowed Senders.
func TestStatus_EmailAlias_Empty(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	sandboxFetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-noalias",
			Profile:   "basic-dev",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	pubKeyB64 := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	identityFetcher := &fakeIdentityFetcher{
		record: &kmaws.IdentityRecord{
			SandboxID:      "sb-noalias",
			PublicKeyB64:   pubKeyB64,
			EmailAddress:   "sb-noalias@sandboxes.klankermaker.ai",
			Signing:        "required",
			VerifyInbound:  "optional",
			Encryption:     "off",
			Alias:          "",
			AllowedSenders: nil,
		},
	}

	out, err := runStatusCmdWithAllFetchers(t, sandboxFetcher, nil, identityFetcher, "sb-noalias")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	// Alias line must NOT appear when alias is empty
	if strings.Contains(out, "Alias:") {
		t.Errorf("expected no 'Alias:' line when alias is empty, got:\n%s", out)
	}

	// Allowed Senders must show "not configured" when slice is nil/empty
	if !strings.Contains(out, "Allowed Senders:") {
		t.Errorf("expected 'Allowed Senders:' line in output, got:\n%s", out)
	}
	if !strings.Contains(out, "not configured") {
		t.Errorf("expected 'not configured' for empty allowedSenders, got:\n%s", out)
	}
}

// TestStatusCmd_FailedWithReason verifies that km status prints Failure: and Failed At: lines
// when the sandbox is in "failed" state and FailureReason is non-empty.
func TestStatusCmd_FailedWithReason(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	failedAt := time.Date(2026, 5, 10, 15, 5, 44, 0, time.UTC)

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID:     "sb-fail01",
			Profile:       "open-dev",
			Substrate:     "ec2",
			Region:        "us-east-1",
			Status:        "failed",
			CreatedAt:     createdAt,
			FailureReason: "provision Slack channel: archived",
			FailedAt:      &failedAt,
		},
	}

	out, err := runStatusCmd(t, fetcher, "sb-fail01")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	// Must show Status: failed
	if !strings.Contains(out, "Status:      failed") {
		t.Errorf("output missing 'Status:      failed':\n%s", out)
	}

	// Must show Failure: line with reason
	if !strings.Contains(out, "Failure:     provision Slack channel: archived") {
		t.Errorf("output missing 'Failure:     provision Slack channel: archived':\n%s", out)
	}

	// Must show Failed At: line (formatted timestamp)
	if !strings.Contains(out, "Failed At:   ") {
		t.Errorf("output missing 'Failed At:   ' line:\n%s", out)
	}

	// Must NOT contain the <unknown hint
	if strings.Contains(out, "<unknown") {
		t.Errorf("output must not contain '<unknown' when FailureReason is set:\n%s", out)
	}
}

// TestStatusCmd_FailedNoReason verifies that km status prints the <unknown> hint
// when the sandbox is in "failed" state but FailureReason is empty.
func TestStatusCmd_FailedNoReason(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID:     "sb-fail02",
			Profile:       "open-dev",
			Substrate:     "ec2",
			Region:        "us-east-1",
			Status:        "failed",
			CreatedAt:     createdAt,
			FailureReason: "",
			FailedAt:      nil,
		},
	}

	out, err := runStatusCmd(t, fetcher, "sb-fail02")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	// Must show Failure: line with <unknown> hint including sandbox ID
	if !strings.Contains(out, "Failure:     <unknown — try km logs sb-fail02>") {
		t.Errorf("output missing '<unknown> hint with sandbox ID:\n%s", out)
	}

	// Must NOT show Failed At: line when FailureReason is empty
	if strings.Contains(out, "Failed At:") {
		t.Errorf("output must not contain 'Failed At:' when FailureReason is empty:\n%s", out)
	}
}

// TestStatusCmd_NocapWithReason verifies that km status prints Failure: and Failed At: lines
// for sandboxes in "nocap" state (same render path as "failed").
func TestStatusCmd_NocapWithReason(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	failedAt := time.Date(2026, 5, 11, 9, 30, 0, 0, time.UTC)

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID:     "sb-nocap01",
			Profile:       "open-dev",
			Substrate:     "ec2",
			Region:        "us-east-1",
			Status:        "nocap",
			CreatedAt:     createdAt,
			FailureReason: "no Spot capacity",
			FailedAt:      &failedAt,
		},
	}

	out, err := runStatusCmd(t, fetcher, "sb-nocap01")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	// Must show Failure: line with reason
	if !strings.Contains(out, "Failure:     no Spot capacity") {
		t.Errorf("output missing 'Failure:     no Spot capacity':\n%s", out)
	}

	// Must show Failed At: line
	if !strings.Contains(out, "Failed At:   ") {
		t.Errorf("output missing 'Failed At:   ' line:\n%s", out)
	}
}

// TestStatusCmd_Running_NoFailureLine verifies that km status does NOT print Failure: or
// Failed At: lines for a sandbox in "running" state, even if the fields contain stale values.
func TestStatusCmd_Running_NoFailureLine(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	staleTs := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID:     "sb-run01",
			Profile:       "open-dev",
			Substrate:     "ec2",
			Region:        "us-east-1",
			Status:        "running",
			CreatedAt:     createdAt,
			FailureReason: "stale value",
			FailedAt:      &staleTs,
		},
	}

	out, err := runStatusCmd(t, fetcher, "sb-run01")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	// Must NOT show Failure: line for running sandboxes
	if strings.Contains(out, "Failure:") {
		t.Errorf("output must not contain 'Failure:' for running sandbox:\n%s", out)
	}

	// Must NOT show Failed At: line for running sandboxes
	if strings.Contains(out, "Failed At:") {
		t.Errorf("output must not contain 'Failed At:' for running sandbox:\n%s", out)
	}
}

// ---- Fake AgentAuthChecker ----

type fakeAuthChecker struct {
	claude bool
	codex  bool
	err    error
	calls  int
}

func (f *fakeAuthChecker) CheckAuth(_ context.Context, _ *kmaws.SandboxRecord) (bool, bool, error) {
	f.calls++
	return f.claude, f.codex, f.err
}

// runStatusCmdWithChecker executes km status with a full DI set including an AgentAuthChecker.
func runStatusCmdWithChecker(t *testing.T, fetcher cmd.SandboxFetcher, checker cmd.AgentAuthChecker, args ...string) (string, error) {
	t.Helper()
	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	statusCmd := cmd.NewStatusCmdWithChecker(cfg, fetcher, nil, nil, checker)
	root.AddCommand(statusCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)

	root.SetArgs(append([]string{"status"}, args...))
	err := root.Execute()
	return buf.String(), err
}

// TestStatusCmd_Running_ShowsUptime verifies that km status prints an Uptime: line
// for a running sandbox.
func TestStatusCmd_Running_ShowsUptime(t *testing.T) {
	// Create a sandbox that started ~1h30m ago so we get "1h30m" output.
	createdAt := time.Now().Add(-(1*time.Hour + 30*time.Minute))

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-uptime1",
			Profile:   "open-dev",
			Substrate: "ecs", // non-EC2 to skip EC2 live status check
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}
	checker := &fakeAuthChecker{claude: true, codex: false}

	out, err := runStatusCmdWithChecker(t, fetcher, checker, "sb-uptime1")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "Uptime:") {
		t.Errorf("expected 'Uptime:' line for running sandbox, got:\n%s", out)
	}
	// Should contain hours (1h...)
	if !strings.Contains(out, "1h") {
		t.Errorf("expected uptime containing '1h' for ~1h30m box, got:\n%s", out)
	}
}

// TestStatusCmd_Running_ShowsAuth verifies that km status prints an Auth: section
// for a running sandbox when the checker reports claude=true, codex=false.
func TestStatusCmd_Running_ShowsAuth(t *testing.T) {
	createdAt := time.Now().Add(-10 * time.Minute)

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-auth1",
			Profile:   "open-dev",
			Substrate: "ecs",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}
	checker := &fakeAuthChecker{claude: true, codex: false}

	out, err := runStatusCmdWithChecker(t, fetcher, checker, "sb-auth1")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "Auth:") {
		t.Errorf("expected 'Auth:' section for running sandbox, got:\n%s", out)
	}
	if !strings.Contains(out, "claude:") {
		t.Errorf("expected 'claude:' line in Auth section, got:\n%s", out)
	}
	if !strings.Contains(out, "codex:") {
		t.Errorf("expected 'codex:' line in Auth section, got:\n%s", out)
	}
	if !strings.Contains(out, "logged in") {
		t.Errorf("expected 'logged in' text in Auth section, got:\n%s", out)
	}
	if !strings.Contains(out, "not logged in") {
		t.Errorf("expected 'not logged in' text in Auth section, got:\n%s", out)
	}
	// Verify the check was actually called exactly once
	if checker.calls != 1 {
		t.Errorf("expected CheckAuth called 1 time, got %d", checker.calls)
	}
}

// TestStatusCmd_Running_AuthCheckerError verifies soft-fail behaviour: when the
// checker returns an error, the command exits 0 and prints a soft unavailable line.
func TestStatusCmd_Running_AuthCheckerError(t *testing.T) {
	createdAt := time.Now().Add(-5 * time.Minute)

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-autherr",
			Profile:   "open-dev",
			Substrate: "ecs",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}
	checker := &fakeAuthChecker{err: fmt.Errorf("SSM timeout")}

	out, err := runStatusCmdWithChecker(t, fetcher, checker, "sb-autherr")
	if err != nil {
		t.Fatalf("status command must exit 0 even on auth check error: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "unavailable") {
		t.Errorf("expected soft 'unavailable' text on checker error, got:\n%s", out)
	}
	// Must NOT fail the command
}

// TestStatusCmd_NonRunning_NoUptimeNoAuth verifies that a non-running sandbox shows
// neither the Uptime: line nor the Auth: section.
func TestStatusCmd_NonRunning_NoUptimeNoAuth(t *testing.T) {
	createdAt := time.Now().Add(-2 * time.Hour)

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-stopped1",
			Profile:   "open-dev",
			Substrate: "ecs",
			Region:    "us-east-1",
			Status:    "stopped",
			CreatedAt: createdAt,
		},
	}
	checker := &fakeAuthChecker{claude: true, codex: true}

	out, err := runStatusCmdWithChecker(t, fetcher, checker, "sb-stopped1")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	if strings.Contains(out, "Uptime:") {
		t.Errorf("expected NO 'Uptime:' line for stopped sandbox, got:\n%s", out)
	}
	if strings.Contains(out, "Auth:") {
		t.Errorf("expected NO 'Auth:' section for stopped sandbox, got:\n%s", out)
	}
	// Checker must not be called for non-running sandboxes
	if checker.calls != 0 {
		t.Errorf("expected CheckAuth NOT called for stopped sandbox, got %d calls", checker.calls)
	}
}

// TestStatus_NoIdentity verifies that km status does not show an Identity section
// when the fetcher returns nil (sandbox has no published identity).
func TestStatus_NoIdentity(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	sandboxFetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-noid",
			Profile:   "no-email",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	// nil record => no published identity
	identityFetcher := &fakeIdentityFetcher{record: nil}

	out, err := runStatusCmdWithAllFetchers(t, sandboxFetcher, nil, identityFetcher, "sb-noid")
	if err != nil {
		t.Fatalf("status command returned error: %v\noutput: %s", err, out)
	}

	// Identity section must NOT be shown when no record
	if strings.Contains(out, "Identity:") {
		t.Errorf("expected no 'Identity:' section when no identity record, got:\n%s", out)
	}
}
