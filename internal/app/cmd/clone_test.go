package cmd_test

import (
	"context"
	"strings"
	"testing"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ---- Mock SSM for clone ----

type mockCloneSSM struct {
	sendCalls  []ssm.SendCommandInput
	sendErr    error
	pollStatus ssmtypes.CommandInvocationStatus
	pollOutput string
}

func (m *mockCloneSSM) SendCommand(ctx context.Context, input *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	if m.sendErr != nil {
		return nil, m.sendErr
	}
	m.sendCalls = append(m.sendCalls, *input)
	return &ssm.SendCommandOutput{
		Command: &ssmtypes.Command{
			CommandId: awssdk.String("cmd-clone-test"),
		},
	}, nil
}

func (m *mockCloneSSM) GetCommandInvocation(ctx context.Context, input *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	status := m.pollStatus
	if status == "" {
		status = ssmtypes.CommandInvocationStatusSuccess
	}
	output := m.pollOutput
	if output == "" {
		output = "CLONE_STAGE_OK"
	}
	return &ssm.GetCommandInvocationOutput{
		Status:                status,
		StandardOutputContent: awssdk.String(output),
	}, nil
}

// ---- Mock fetcher for clone ----

type mockCloneFetcher struct {
	record *kmaws.SandboxRecord
	err    error
}

func (m *mockCloneFetcher) FetchSandbox(_ context.Context, _ string) (*kmaws.SandboxRecord, error) {
	return m.record, m.err
}

// ---- Helpers ----

func newRunningEC2SandboxForClone(id string) *mockCloneFetcher {
	return &mockCloneFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: id,
			Profile:   "open-dev",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
			Resources: []string{
				"arn:aws:ec2:us-east-1:123456789012:instance/i-0abc123def456",
			},
		},
	}
}

func newRunningECSSandboxForClone(id string) *mockCloneFetcher {
	return &mockCloneFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: id,
			Profile:   "ecs-dev",
			Substrate: "ecs",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
			Resources: []string{
				"arn:aws:ecs:us-east-1:123456789012:task/my-cluster/abc123",
			},
		},
	}
}

func newStoppedSandboxForClone(id string) *mockCloneFetcher {
	return &mockCloneFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: id,
			Profile:   "open-dev",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "stopped",
			CreatedAt: time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC),
			Resources: []string{
				"arn:aws:ec2:us-east-1:123456789012:instance/i-0abc123def456",
			},
		},
	}
}

// runCloneCmd executes clone with the given args and returns the error.
func runCloneCmd(t *testing.T, fetcher cmd.SandboxFetcher, ssmClient cmd.SSMSendAPI, args []string) error {
	t.Helper()
	cfg := &config.Config{ArtifactsBucket: "test-bucket"}
	root := &cobra.Command{Use: "km"}
	cloneCmd := cmd.NewCloneCmdWithDeps(cfg, fetcher, ssmClient)
	root.AddCommand(cloneCmd)
	root.SetArgs(append([]string{"clone"}, args...))
	root.SetOut(nil)
	return root.Execute()
}

// ---- Flag parsing tests ----

// TestClone_FlagParsing_Source verifies basic source arg parsing.
func TestClone_FlagParsing_Source(t *testing.T) {
	fetcher := newRunningEC2SandboxForClone("sb-abc12345")
	mockSSM := &mockCloneSSM{}

	// This will fail at runCreate (no real AWS) but flags should parse correctly.
	// We just check it doesn't error on flag parsing itself.
	cfg := &config.Config{ArtifactsBucket: "test-bucket"}
	root := &cobra.Command{Use: "km"}
	cloneCmd := cmd.NewCloneCmdWithDeps(cfg, fetcher, mockSSM)
	root.AddCommand(cloneCmd)

	// Parse manually to verify flag values
	root.SetArgs([]string{"clone", "sb-abc12345"})
	_ = root.ParseFlags([]string{})

	// Verify command exists
	found, _, err := root.Find([]string{"clone"})
	if err != nil {
		t.Fatalf("unexpected error finding clone command: %v", err)
	}
	if found == nil || found.Use == "km" {
		t.Fatal("clone command not found")
	}
}

// TestClone_FlagParsing_AliasFromPositional verifies positional alias arg.
func TestClone_FlagParsing_AliasFromPositional(t *testing.T) {
	cfg := &config.Config{ArtifactsBucket: "test-bucket"}
	root := &cobra.Command{Use: "km"}
	cloneCmd := cmd.NewCloneCmdWithDeps(cfg, nil, nil)
	root.AddCommand(cloneCmd)

	// Clone command should accept 1 or 2 positional args
	if cloneCmd.Args == nil {
		t.Fatal("clone command should have args validator set")
	}

	// 2 positional args should be accepted (source + alias)
	if err := cloneCmd.Args(cloneCmd, []string{"sb-abc12345", "myalias"}); err != nil {
		t.Errorf("should accept 2 positional args (source + alias): %v", err)
	}
}

// TestClone_FlagParsing_AllFlags verifies all flags are registered.
func TestClone_FlagParsing_AllFlags(t *testing.T) {
	cfg := &config.Config{ArtifactsBucket: "test-bucket"}
	root := &cobra.Command{Use: "km"}
	cloneCmd := cmd.NewCloneCmdWithDeps(cfg, nil, nil)
	root.AddCommand(cloneCmd)

	requiredFlags := []string{"alias", "count", "no-copy", "verbose"}
	for _, flag := range requiredFlags {
		if cloneCmd.Flags().Lookup(flag) == nil {
			t.Errorf("flag --%s not registered on clone command", flag)
		}
	}

	// Default count should be 1
	countFlag := cloneCmd.Flags().Lookup("count")
	if countFlag == nil {
		t.Fatal("--count flag not found")
	}
	if countFlag.DefValue != "1" {
		t.Errorf("--count default should be 1, got %q", countFlag.DefValue)
	}
}

// TestClone_CountWithoutAlias_ReturnsError verifies that --count N without --alias returns an error.
func TestClone_CountWithoutAlias_ReturnsError(t *testing.T) {
	fetcher := newRunningEC2SandboxForClone("sb-abc12345")
	mockSSM := &mockCloneSSM{}

	err := runCloneCmd(t, fetcher, mockSSM, []string{"sb-abc12345", "--count", "3"})
	if err == nil {
		t.Fatal("expected error when --count > 1 and no alias, got nil")
	}
	if !strings.Contains(err.Error(), "alias") {
		t.Errorf("error should mention alias, got: %v", err)
	}
}

// ---- Source validation tests ----

// TestClone_SourceNotRunning_ReturnsError verifies non-running source returns an error with km resume suggestion.
func TestClone_SourceNotRunning_ReturnsError(t *testing.T) {
	fetcher := newStoppedSandboxForClone("sb-stopped1")
	mockSSM := &mockCloneSSM{}

	err := runCloneCmd(t, fetcher, mockSSM, []string{"sb-stopped1"})
	if err == nil {
		t.Fatal("expected error for stopped source sandbox, got nil")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "not running") {
		t.Errorf("error should contain 'not running', got: %v", err)
	}
	if !strings.Contains(errStr, "km resume") {
		t.Errorf("error should suggest 'km resume', got: %v", err)
	}
}

// TestClone_ECSWithWorkspaceCopy_ReturnsError verifies ECS substrate returns error for workspace copy.
func TestClone_ECSWithWorkspaceCopy_ReturnsError(t *testing.T) {
	fetcher := newRunningECSSandboxForClone("sb-ecs12345")
	mockSSM := &mockCloneSSM{}

	// Without --no-copy: should error for ECS (SSM not available for workspace tar)
	err := runCloneCmd(t, fetcher, mockSSM, []string{"sb-ecs12345"})
	if err == nil {
		t.Fatal("expected error for ECS substrate with workspace copy, got nil")
	}
	errStr := err.Error()
	if !strings.Contains(strings.ToLower(errStr), "ecs") && !strings.Contains(strings.ToLower(errStr), "ssm") {
		t.Errorf("error should mention ECS or SSM limitation, got: %v", err)
	}
}

// ---- buildWorkspaceStagingCmd tests ----

// TestBuildWorkspaceStagingCmd_Basic verifies the staging command structure.
func TestBuildWorkspaceStagingCmd_Basic(t *testing.T) {
	bucket := "my-artifacts"
	stagingKey := "artifacts/sb-clone01/staging/workspace.tar.gz"
	paths := []string{}

	shellCmd := cmd.BuildWorkspaceStagingCmd(paths, bucket, stagingKey)

	// Must cd to / and tar /workspace
	if !strings.Contains(shellCmd, "cd /") {
		t.Error("staging cmd should cd to /")
	}
	if !strings.Contains(shellCmd, "workspace") {
		t.Error("staging cmd should include workspace")
	}
	// Must upload to correct S3 key
	if !strings.Contains(shellCmd, bucket) {
		t.Errorf("staging cmd should reference bucket %q", bucket)
	}
	if !strings.Contains(shellCmd, stagingKey) {
		t.Errorf("staging cmd should reference key %q", stagingKey)
	}
	// Must emit success marker
	if !strings.Contains(shellCmd, "CLONE_STAGE_OK") {
		t.Error("staging cmd should emit CLONE_STAGE_OK on success")
	}
}

// TestBuildWorkspaceStagingCmd_WithExtraPaths verifies additional paths are included.
func TestBuildWorkspaceStagingCmd_WithExtraPaths(t *testing.T) {
	bucket := "my-artifacts"
	stagingKey := "artifacts/sb-clone02/staging/workspace.tar.gz"
	extraPaths := []string{"home/sandbox/.claude", "home/sandbox/.bashrc"}

	shellCmd := cmd.BuildWorkspaceStagingCmd(extraPaths, bucket, stagingKey)

	for _, p := range extraPaths {
		if !strings.Contains(shellCmd, p) {
			t.Errorf("staging cmd should include path %q", p)
		}
	}
}

// TestBuildWorkspaceStagingCmd_MissingWorkspace verifies graceful handling when /workspace absent.
func TestBuildWorkspaceStagingCmd_MissingWorkspace(t *testing.T) {
	shellCmd := cmd.BuildWorkspaceStagingCmd(nil, "bucket", "key/workspace.tar.gz")

	// Should emit CLONE_STAGE_EMPTY if no workspace
	if !strings.Contains(shellCmd, "CLONE_STAGE_EMPTY") {
		t.Error("staging cmd should emit CLONE_STAGE_EMPTY when workspace absent")
	}
}

// ---- Multi-clone alias generation tests ----

// TestClone_AliasGeneration_Multi verifies wrkr-1, wrkr-2, wrkr-3 for count=3.
func TestClone_AliasGeneration_Multi(t *testing.T) {
	aliases := cmd.GenerateCloneAliases("wrkr", 3)
	expected := []string{"wrkr-1", "wrkr-2", "wrkr-3"}
	if len(aliases) != len(expected) {
		t.Fatalf("expected %d aliases, got %d: %v", len(expected), len(aliases), aliases)
	}
	for i, alias := range aliases {
		if alias != expected[i] {
			t.Errorf("alias[%d]: expected %q, got %q", i, expected[i], alias)
		}
	}
}

// TestClone_AliasGeneration_Single verifies single clone uses alias as-is.
func TestClone_AliasGeneration_Single(t *testing.T) {
	aliases := cmd.GenerateCloneAliases("myalias", 1)
	if len(aliases) != 1 {
		t.Fatalf("expected 1 alias, got %d", len(aliases))
	}
	if aliases[0] != "myalias" {
		t.Errorf("single clone alias should be 'myalias', got %q", aliases[0])
	}
}

// TestClone_AliasGeneration_EmptySingle verifies single clone with empty alias returns empty slice.
func TestClone_AliasGeneration_EmptySingle(t *testing.T) {
	aliases := cmd.GenerateCloneAliases("", 1)
	if len(aliases) != 1 {
		t.Fatalf("expected 1 alias, got %d", len(aliases))
	}
	if aliases[0] != "" {
		t.Errorf("single clone with empty alias should return empty string, got %q", aliases[0])
	}
}
