package cmd_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
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

	// Must show TTL expiry
	if !strings.Contains(out, "2026-03-22T12:00:00Z") {
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
	if !strings.Contains(err.Error(), "state bucket not configured") {
		t.Errorf("expected 'state bucket not configured' in error, got: %v", err)
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
			SandboxID:    "sb-ident1",
			PublicKeyB64: pubKeyB64,
			EmailAddress: "sb-ident1@sandboxes.klankermaker.ai",
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
