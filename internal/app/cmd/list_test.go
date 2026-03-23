package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
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
				Profile:   "open-dev",
				Substrate: "ec2",
				Region:    "us-east-1",
				Status:    "running",
				TTLExpiry: &ttlTime,
			},
			{
				SandboxID: "sb-bbb222",
				Profile:   "restricted",
				Substrate: "ecs",
				Region:    "us-west-2",
				Status:    "running",
			},
		},
	}

	out, err := runListCmd(t, lister)
	if err != nil {
		t.Fatalf("list command returned error: %v", err)
	}

	// Header must contain all column names
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

// TestListCmd_EmptyStateBucketError verifies that km list returns a clear error
// when StateBucket is empty and no lister is injected (real lister path).
func TestListCmd_EmptyStateBucketError(t *testing.T) {
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
	if err == nil {
		t.Fatal("expected error when StateBucket is empty, got nil")
	}
	if !strings.Contains(err.Error(), "state bucket not configured") {
		t.Errorf("expected 'state bucket not configured' in error, got: %v", err)
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
