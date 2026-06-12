package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// ---- Fake lister ----

type fakeLister struct {
	records []kmaws.SandboxRecord
	err     error
}

func (f *fakeLister) ListSandboxes(_ context.Context, _ bool) ([]kmaws.SandboxRecord, error) {
	return f.records, f.err
}

// ---- Helpers ----

// runListCmd executes the list command with a fake lister and returns stdout output.
func runListCmd(t *testing.T, lister cmd.SandboxLister, extraArgs ...string) (string, error) {
	t.Helper()
	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	listCmd := cmd.NewListCmdWithLister(cfg, lister)
	root.AddCommand(listCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)

	args := append([]string{"list"}, extraArgs...)
	root.SetArgs(args)

	err := root.Execute()
	return buf.String(), err
}

// ---- Tests ----

func TestListCmd_TableOutput(t *testing.T) {
	ttlTime := time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC)
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{
				SandboxID: "sb-aaa111",
				Alias:     "my-poc-name",
				Profile:   "open-dev",
				Substrate: "ec2",
				Region:    "us-east-1",
				Status:    "running",
				TTLExpiry: &ttlTime,
			},
			{
				SandboxID: "sb-bbb222",
				Alias:     "demo",
				Profile:   "restricted",
				Substrate: "ec2spot",
				Region:    "ap-southeast-2",
				Status:    "running",
			},
			{
				SandboxID: "sb-ccc333",
				Alias:     "k8s-poc",
				Profile:   "default",
				Substrate: "k8s",
				Region:    "eu-central-1",
				Status:    "stopped",
			},
			{
				SandboxID: "sb-ddd444",
				Alias:     "dock-test",
				Profile:   "minimal",
				Substrate: "docker",
				Region:    "ap-northeast-1",
				Status:    "running",
			},
		},
	}

	out, err := runListCmd(t, lister, "--wide")
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}

	// Wide header must contain all column names
	if !strings.Contains(out, "SANDBOX ID") {
		t.Errorf("output missing 'SANDBOX ID' header column:\n%s", out)
	}
	if !strings.Contains(out, "PROFILE") {
		t.Errorf("output missing 'PROFILE' header column:\n%s", out)
	}
	if !strings.Contains(out, "SUBSTRATE") {
		t.Errorf("output missing 'SUBSTRATE' header column:\n%s", out)
	}
	if !strings.Contains(out, "REGION") {
		t.Errorf("output missing 'REGION' header column:\n%s", out)
	}
	if !strings.Contains(out, "STATUS") {
		t.Errorf("output missing 'STATUS' header column:\n%s", out)
	}
	if !strings.Contains(out, "TTL") {
		t.Errorf("output missing 'TTL' header column:\n%s", out)
	}

	// Both sandbox IDs must appear
	if !strings.Contains(out, "sb-aaa111") {
		t.Errorf("output missing sandbox ID 'sb-aaa111':\n%s", out)
	}
	if !strings.Contains(out, "sb-bbb222") {
		t.Errorf("output missing sandbox ID 'sb-bbb222':\n%s", out)
	}
}

func TestListCmd_JSONOutput(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{
				SandboxID: "sb-json1",
				Profile:   "open-dev",
				Substrate: "ec2",
				Region:    "us-east-1",
				Status:    "running",
			},
		},
	}

	out, err := runListCmd(t, lister, "--json")
	if err != nil {
		t.Fatalf("list --json returned error: %v", err)
	}

	// Must be valid JSON array
	var records []kmaws.SandboxRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &records); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\noutput: %s", err, out)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 record in JSON output, got %d", len(records))
	}
	if records[0].SandboxID != "sb-json1" {
		t.Errorf("sandbox_id = %q, want %q", records[0].SandboxID, "sb-json1")
	}
	if records[0].Profile != "open-dev" {
		t.Errorf("profile = %q, want %q", records[0].Profile, "open-dev")
	}
	if records[0].Substrate != "ec2" {
		t.Errorf("substrate = %q, want %q", records[0].Substrate, "ec2")
	}
}

func TestListCmd_Empty(t *testing.T) {
	lister := &fakeLister{records: []kmaws.SandboxRecord{}}

	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list with empty result returned error: %v", err)
	}

	if !strings.Contains(out, "No running sandboxes") {
		t.Errorf("expected 'No running sandboxes' message, got:\n%s", out)
	}
}

// TestListCmd_EmptyStateBucketNoLongerErrors verifies that km list does NOT return the
// legacy "state bucket not configured" guard error when StateBucket is empty.
//
// DynamoDB is the primary metadata store (Phase 104+). The bucket guard only fires on
// the S3 fallback path, which is reached only after a ResourceNotFoundException from
// DynamoDB. In a unit-test environment (no real DynamoDB table) the DynamoDB error is
// NOT a ResourceNotFoundException, so the guard never fires and the command returns nil
// or a DynamoDB connectivity error — never the old bucket-guard message.
func TestListCmd_EmptyStateBucketNoLongerErrors(t *testing.T) {
	cfg := &config.Config{StateBucket: ""}
	root := &cobra.Command{Use: "km"}
	// nil lister forces the real lister construction path
	listCmd := cmd.NewListCmdWithLister(cfg, nil)
	root.AddCommand(listCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"list"})

	err := root.Execute()
	// The legacy "state bucket not configured" guard must NOT be triggered; the
	// DynamoDB-primary path may return nil or a DynamoDB error, but never the
	// old bucket-guard message.
	if err != nil && strings.Contains(err.Error(), "state bucket not configured") {
		t.Errorf("legacy 'state bucket not configured' guard must not fire on DynamoDB-primary path; got: %v", err)
	}
}

// TestListCmd_RealBucketFromConfig verifies that when StateBucket is set, the real
// lister path is attempted (will fail on AWS config load in test env — that's OK;
// what matters is the error is NOT about a missing bucket).
func TestListCmd_RealBucketFromConfig(t *testing.T) {
	cfg := &config.Config{StateBucket: "my-custom-bucket"}
	root := &cobra.Command{Use: "km"}
	listCmd := cmd.NewListCmdWithLister(cfg, nil)
	root.AddCommand(listCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"list"})

	err := root.Execute()
	// We expect an error (AWS config won't load in unit tests), but it must NOT
	// be about the bucket being unconfigured.
	if err != nil && strings.Contains(err.Error(), "state bucket not configured") {
		t.Errorf("should not get 'state bucket not configured' when StateBucket is set; got: %v", err)
	}
}

// TestListCmd_FailedSandboxDisplaysRedStatus verifies that a sandbox with status "failed"
// is included in the output with a visually distinct indicator (red ANSI color).
func TestListCmd_FailedSandboxDisplaysRedStatus(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{
				SandboxID: "sb-fail001",
				Profile:   "open-dev",
				Substrate: "ec2",
				Region:    "us-east-1",
				Status:    "failed",
			},
		},
	}

	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}

	// Sandbox must appear in output
	if !strings.Contains(out, "sb-fail001") {
		t.Errorf("output missing failed sandbox 'sb-fail001':\n%s", out)
	}

	// Status label "fail" must appear (shortened from "failed")
	if !strings.Contains(out, "fail") {
		t.Errorf("output missing 'fail' status text:\n%s", out)
	}

	// Must have ANSI red color code for failed status
	if !strings.Contains(out, "\033[31m") && !strings.Contains(out, "\x1b[31m") {
		t.Errorf("output missing ANSI red color code for failed status:\n%q", out)
	}
}

// TestListCmd_PartialSandboxDisplaysYellowStatus verifies that a sandbox with status "partial"
// is included in the output with a yellow ANSI color.
func TestListCmd_PartialSandboxDisplaysYellowStatus(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{
				SandboxID: "sb-part001",
				Profile:   "open-dev",
				Substrate: "ec2",
				Region:    "us-east-1",
				Status:    "partial",
			},
		},
	}

	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}

	// Sandbox must appear in output
	if !strings.Contains(out, "sb-part001") {
		t.Errorf("output missing partial sandbox 'sb-part001':\n%s", out)
	}

	// Status label "part" must appear (shortened from "partial")
	if !strings.Contains(out, "part") {
		t.Errorf("output missing 'part' status text:\n%s", out)
	}

	// Must have ANSI yellow color code for partial status
	if !strings.Contains(out, "\033[33m") && !strings.Contains(out, "\x1b[33m") {
		t.Errorf("output missing ANSI yellow color code for partial status:\n%q", out)
	}
}

// TestListCmd_FailedSandboxIsNumbered verifies that failed sandboxes are still assigned
// a #number so operators can reference them for km destroy cleanup.
func TestListCmd_FailedSandboxIsNumbered(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{
				SandboxID: "sb-ok0001",
				Profile:   "open-dev",
				Substrate: "ec2",
				Region:    "us-east-1",
				Status:    "running",
			},
			{
				SandboxID: "sb-fail002",
				Profile:   "open-dev",
				Substrate: "ec2",
				Region:    "us-east-1",
				Status:    "failed",
			},
		},
	}

	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}

	// Both sandboxes must appear
	if !strings.Contains(out, "sb-ok0001") {
		t.Errorf("output missing running sandbox 'sb-ok0001':\n%s", out)
	}
	if !strings.Contains(out, "sb-fail002") {
		t.Errorf("output missing failed sandbox 'sb-fail002':\n%s", out)
	}

	// Row 2 must exist (number "2" in output)
	if !strings.Contains(out, "2") {
		t.Errorf("output missing row number 2 for failed sandbox:\n%s", out)
	}
}

// TestListCmd_RunningAndStoppedNoRegression verifies existing statuses still display correctly.
// Uses "ecs" substrate to avoid the EC2 live status check that replaces "running" with "killed".
func TestListCmd_RunningAndStoppedNoRegression(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			// ECS substrate skips the EC2 live status check, so "running" is preserved.
			{SandboxID: "sb-run001", Status: "running", Profile: "p", Substrate: "ecs", Region: "us-east-1"},
			{SandboxID: "sb-stp001", Status: "stopped", Profile: "p", Substrate: "ec2", Region: "us-east-1"},
		},
	}

	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}

	if !strings.Contains(out, "run") {
		t.Errorf("output missing 'run' status:\n%s", out)
	}
	if !strings.Contains(out, "stop") {
		t.Errorf("output missing 'stop' status:\n%s", out)
	}
}

func TestListCmd_NarrowHidesColumns(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-aaa111", Profile: "default", Substrate: "ec2", Region: "us-east-1", Status: "running"},
		},
	}

	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}

	// Narrow mode should NOT show profile/substrate/region columns
	if strings.Contains(out, "PROFILE") {
		t.Errorf("narrow output should not contain PROFILE header:\n%s", out)
	}
	if strings.Contains(out, "SUBSTRATE") {
		t.Errorf("narrow output should not contain SUBSTRATE header:\n%s", out)
	}
	if strings.Contains(out, "REGION") {
		t.Errorf("narrow output should not contain REGION header:\n%s", out)
	}
	// But should still have essential columns
	if !strings.Contains(out, "SANDBOX ID") {
		t.Errorf("narrow output missing SANDBOX ID:\n%s", out)
	}
	if !strings.Contains(out, "STATUS") {
		t.Errorf("narrow output missing STATUS:\n%s", out)
	}
}

// TestListCmd_Reset verifies that `km list --reset` sets the local-number
// counter back to 1 without making any AWS calls. It redirects HOME so the
// real user state is untouched.
func TestListCmd_Reset(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp+"/.config")

	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	// nil lister is fine because --reset must short-circuit before the lister runs
	root.AddCommand(cmd.NewListCmdWithLister(cfg, nil))

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"list", "--reset"})

	if err := root.Execute(); err != nil {
		t.Fatalf("--reset returned error: %v", err)
	}
	if !strings.Contains(buf.String(), "next created sandbox will be #1") {
		t.Errorf("expected reset confirmation message, got: %q", buf.String())
	}
}

// ---- Fake AgentAuthChecker for list tests ----

type fakeListAuthChecker struct {
	claude bool
	codex  bool
	err    error
	calls  int
}

func (f *fakeListAuthChecker) CheckAuth(_ context.Context, _ *kmaws.SandboxRecord) (bool, bool, error) {
	f.calls++
	return f.claude, f.codex, f.err
}

// runListCmdWithChecker executes km list with a fake lister AND an AgentAuthChecker.
func runListCmdWithChecker(t *testing.T, lister cmd.SandboxLister, checker cmd.AgentAuthChecker, extraArgs ...string) (string, error) {
	t.Helper()
	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	listCmd := cmd.NewListCmdWithCheckers(cfg, lister, checker)
	root.AddCommand(listCmd)

	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)

	args := append([]string{"list"}, extraArgs...)
	root.SetArgs(args)

	err := root.Execute()
	return buf.String(), err
}

// TestListCmd_BannerPresent verifies that km list prints a banner line
// for non-JSON output (even when the list is empty, even without --wide).
func TestListCmd_BannerPresent(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-banner1", Status: "running", Profile: "p", Substrate: "ecs", Region: "us-east-1"},
		},
	}

	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}

	if !strings.Contains(out, "km list") {
		t.Errorf("expected banner line containing 'km list', got:\n%s", out)
	}
}

// TestListCmd_BannerSuppressedInJSON verifies that --json output is valid JSON
// with no banner prefix.
func TestListCmd_BannerSuppressedInJSON(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-json-banner", Status: "running", Profile: "p", Substrate: "ecs", Region: "us-east-1"},
		},
	}

	out, err := runListCmd(t, lister, "--json")
	if err != nil {
		t.Fatalf("list --json returned error: %v", err)
	}

	// Output must be valid JSON (banner must NOT appear)
	var records []kmaws.SandboxRecord
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &records); err != nil {
		t.Fatalf("--json output is not valid JSON (banner may have leaked): %v\noutput: %s", err, out)
	}
	// Banner must not appear anywhere in output
	if strings.Contains(out, "km list") {
		t.Errorf("banner 'km list' must NOT appear in --json output:\n%s", out)
	}
}

// TestListCmd_BannerOnEmpty verifies that the banner still appears when there
// are no sandboxes ("No running sandboxes." path).
func TestListCmd_BannerOnEmpty(t *testing.T) {
	lister := &fakeLister{records: []kmaws.SandboxRecord{}}

	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}

	if !strings.Contains(out, "km list") {
		t.Errorf("expected banner on empty list, got:\n%s", out)
	}
	if !strings.Contains(out, "No running sandboxes") {
		t.Errorf("expected 'No running sandboxes' message, got:\n%s", out)
	}
}

// TestListCmd_UPColumn verifies that the UP column appears in both narrow and
// wide layouts, with uptime for running rows and '-' for non-running rows.
func TestListCmd_UPColumn(t *testing.T) {
	createdAt := time.Now().Add(-90 * time.Minute) // ~1h30m

	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-up1", Status: "running", Profile: "p", Substrate: "ecs", Region: "us-east-1", CreatedAt: createdAt},
			{SandboxID: "sb-up2", Status: "stopped", Profile: "p", Substrate: "ecs", Region: "us-east-1", CreatedAt: createdAt},
		},
	}

	// Narrow layout
	outNarrow, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}
	if !strings.Contains(outNarrow, "UP") {
		t.Errorf("narrow output missing 'UP' column header:\n%s", outNarrow)
	}
	// Running row should show uptime (1h...)
	if !strings.Contains(outNarrow, "1h") {
		t.Errorf("narrow output missing uptime '1h' for running sandbox:\n%s", outNarrow)
	}

	// Wide layout
	outWide, err := runListCmd(t, lister, "--wide")
	if err != nil {
		t.Fatalf("list --wide returned error: %v", err)
	}
	if !strings.Contains(outWide, "UP") {
		t.Errorf("wide output missing 'UP' column header:\n%s", outWide)
	}
}

// TestListCmd_UPColumn_ZeroCreatedAt guards the --tags (tag-scan) path, whose
// records carry no CreatedAt. time.Since(time.Time{}) overflows the int64
// Duration and saturates at ~106751d23h, which used to render as the UP value
// for those rows. A zero CreatedAt must render "-" instead.
func TestListCmd_UPColumn_ZeroCreatedAt(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			// Running, but no CreatedAt — exactly what tag-scan produces.
			{SandboxID: "sb-notime", Status: "running", Profile: "p", Substrate: "ecs", Region: "us-east-1"},
		},
	}

	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}
	if strings.Contains(out, "106751") {
		t.Errorf("UP column rendered saturated-duration garbage for zero CreatedAt:\n%s", out)
	}
	if strings.Contains(out, "d23h") {
		t.Errorf("UP column rendered a multi-day uptime for zero CreatedAt:\n%s", out)
	}
}

// TestListCmd_AuthFlag_ConcurrentFanOut verifies that --auth triggers CheckAuth
// calls on running sandboxes and adds an AUTH column.
func TestListCmd_AuthFlag_ConcurrentFanOut(t *testing.T) {
	createdAt := time.Now().Add(-5 * time.Minute)

	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-cl1", Status: "running", Profile: "p", Substrate: "ecs", Region: "us-east-1", CreatedAt: createdAt},
			{SandboxID: "sb-cl2", Status: "stopped", Profile: "p", Substrate: "ecs", Region: "us-east-1", CreatedAt: createdAt},
		},
	}
	checker := &fakeListAuthChecker{claude: true, codex: false}

	out, err := runListCmdWithChecker(t, lister, checker, "--auth")
	if err != nil {
		t.Fatalf("list --auth returned error: %v\noutput: %s", err, out)
	}

	// AUTH column header must appear
	if !strings.Contains(out, "AUTH") {
		t.Errorf("expected 'AUTH' column header with --auth, got:\n%s", out)
	}
	// Running row must show cl✓
	if !strings.Contains(out, "cl✓") {
		t.Errorf("expected 'cl✓' for logged-in claude, got:\n%s", out)
	}
	// cx✗ for not-logged-in codex
	if !strings.Contains(out, "cx✗") {
		t.Errorf("expected 'cx✗' for logged-out codex, got:\n%s", out)
	}
	// CheckAuth called exactly once (only for running sandbox)
	if checker.calls != 1 {
		t.Errorf("expected CheckAuth called 1 time (only running sandbox), got %d", checker.calls)
	}
}

// TestListCmd_NoAuth_ZeroSSMCalls verifies that without --auth, the checker is
// never invoked (zero SSM calls).
func TestListCmd_NoAuth_ZeroSSMCalls(t *testing.T) {
	createdAt := time.Now().Add(-5 * time.Minute)

	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-noauth1", Status: "running", Profile: "p", Substrate: "ecs", Region: "us-east-1", CreatedAt: createdAt},
		},
	}
	checker := &fakeListAuthChecker{claude: true, codex: true}

	// No --auth flag
	out, err := runListCmdWithChecker(t, lister, checker)
	if err != nil {
		t.Fatalf("list without --auth returned error: %v", err)
	}

	if checker.calls != 0 {
		t.Errorf("expected 0 CheckAuth calls without --auth, got %d\noutput: %s", checker.calls, out)
	}
	// AUTH column must NOT appear without --auth
	if strings.Contains(out, "AUTH") {
		t.Errorf("'AUTH' column must NOT appear without --auth:\n%s", out)
	}
}

// TestListCmd_WideAloneDoesNotEnableAuth verifies that --wide alone does NOT
// enable the AUTH column.
func TestListCmd_WideAloneDoesNotEnableAuth(t *testing.T) {
	createdAt := time.Now().Add(-5 * time.Minute)

	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-widenoauth", Status: "running", Profile: "p", Substrate: "ecs", Region: "us-east-1", CreatedAt: createdAt},
		},
	}
	checker := &fakeListAuthChecker{claude: true, codex: true}

	out, err := runListCmdWithChecker(t, lister, checker, "--wide")
	if err != nil {
		t.Fatalf("list --wide returned error: %v", err)
	}

	if checker.calls != 0 {
		t.Errorf("expected 0 CheckAuth calls with --wide alone, got %d", checker.calls)
	}
	if strings.Contains(out, "AUTH") {
		t.Errorf("'AUTH' column must NOT appear with --wide alone (need --auth):\n%s", out)
	}
}

func TestListCmd_LockedSandboxShowsLockIcon(t *testing.T) {
	lister := &fakeLister{
		records: []kmaws.SandboxRecord{
			{SandboxID: "sb-locked1", Profile: "default", Substrate: "ecs", Region: "us-east-1", Status: "running", Locked: true},
			{SandboxID: "sb-unlkd1", Profile: "default", Substrate: "ecs", Region: "us-east-1", Status: "running", Locked: false},
		},
	}

	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}

	// Locked sandbox should have lock icon on alias
	if !strings.Contains(out, "🔒") {
		t.Errorf("locked sandbox should show lock icon:\n%s", out)
	}
	// Bold white ANSI code should be present for locked alias
	if !strings.Contains(out, "\033[1;37m") {
		t.Errorf("locked sandbox alias should use bold white ANSI:\n%s", out)
	}
	// Status should still be green (running), not overridden by lock
	if !strings.Contains(out, "\033[32m") {
		t.Errorf("locked sandbox status should still be green:\n%s", out)
	}
}
