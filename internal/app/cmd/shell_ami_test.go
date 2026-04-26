// Package cmd — shell_ami_test.go
// Unit tests for the --ami flag on km shell --learn, the bake-before-flush
// ordering, and the updated GenerateProfileFromJSON signature.
// Uses package cmd (internal) to access runLearnPostExit and the injectable
// bakeFromSandboxFn / flushEC2ObservationsFn package-level variables.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ---- test helpers ----

// shellAMIFetcher is a minimal SandboxFetcher for shell AMI tests.
type shellAMIFetcher struct {
	record *kmaws.SandboxRecord
	err    error
}

func (f *shellAMIFetcher) FetchSandbox(_ context.Context, _ string) (*kmaws.SandboxRecord, error) {
	return f.record, f.err
}

// buildShellAMIFetcher returns a fetcher with an EC2 record by default.
func buildShellAMIFetcher(substrate string) *shellAMIFetcher {
	return &shellAMIFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-ami-test",
			Profile:   "open-dev",
			Substrate: substrate,
			Region:    "us-east-1",
			Status:    "running",
			Resources: []string{
				"arn:aws:ec2:us-east-1:123456789012:instance/i-0abc123learn",
			},
		},
	}
}

// writeObservedJSON writes a minimal observed-state JSON file to a temp dir and
// returns its path. Used to seed the S3-fetch mock.
func writeObservedJSON(t *testing.T) []byte {
	t.Helper()
	data, _ := json.Marshal(map[string]interface{}{
		"dns":   []string{"example.com"},
		"hosts": []string{},
		"repos": []string{},
	})
	return data
}

// ---- tests ----

// TestRunLearnPostExit_AMIFlag_BakesBeforeFlush verifies the CONTEXT.md locked
// decision: BakeFromSandbox is called BEFORE flushEC2Observations.
// Uses package-level function-variable injection to record call order.
func TestRunLearnPostExit_AMIFlag_BakesBeforeFlush(t *testing.T) {
	cfg := &config.Config{}

	fetcher := buildShellAMIFetcher("ec2")

	// Fake fetchEC2ObservedJSON via the underlying function. Since we cannot
	// inject fetchEC2ObservedJSON easily, we use a real temp file approach:
	// the function returns a valid JSON blob. We instead stub the whole S3 fetch
	// path by replacing the package-level vars and providing our own observed JSON.
	//
	// For call-order verification we only need bakeFromSandboxFn and
	// flushEC2ObservationsFn to record order — fetchEC2ObservedJSON will fail
	// because there is no real S3, but we can capture the call order before that.

	var calls []string

	oldBake := bakeFromSandboxFn
	oldFlush := flushEC2ObservationsFn
	t.Cleanup(func() {
		bakeFromSandboxFn = oldBake
		flushEC2ObservationsFn = oldFlush
	})

	bakeFromSandboxFn = func(_ context.Context, _ *config.Config, _ kmaws.SandboxRecord, _, _, _ string) (string, error) {
		calls = append(calls, "bake")
		return "ami-0order1111", nil
	}
	flushEC2ObservationsFn = func(_ context.Context, _ *config.Config, _ string) error {
		calls = append(calls, "flush")
		return nil
	}

	// runLearnPostExit will fail at fetchEC2ObservedJSON (no real S3) but the
	// call order for bake + flush will already be recorded.
	_ = runLearnPostExit(context.Background(), cfg, fetcher, "sb-ami-test", "", true)

	if len(calls) < 2 {
		t.Fatalf("expected at least 2 calls (bake + flush), got %d: %v", len(calls), calls)
	}
	if calls[0] != "bake" {
		t.Errorf("expected first call to be 'bake' (CONTEXT.md decision), got %q; full order: %v", calls[0], calls)
	}
	if calls[1] != "flush" {
		t.Errorf("expected second call to be 'flush', got %q; full order: %v", calls[1], calls)
	}
}

// TestRunLearnPostExit_AMIFlag_InjectsAMIIntoGeneratedYAML verifies that when the
// bake succeeds the generated profile file contains `ami: ami-0abc123`.
func TestRunLearnPostExit_AMIFlag_InjectsAMIIntoGeneratedYAML(t *testing.T) {
	cfg := &config.Config{}
	fetcher := buildShellAMIFetcher("ec2")
	observedJSON := writeObservedJSON(t)

	oldBake := bakeFromSandboxFn
	oldFlush := flushEC2ObservationsFn
	oldFetch := fetchEC2ObservedJSONFn
	t.Cleanup(func() {
		bakeFromSandboxFn = oldBake
		flushEC2ObservationsFn = oldFlush
		fetchEC2ObservedJSONFn = oldFetch
	})

	bakeFromSandboxFn = func(_ context.Context, _ *config.Config, _ kmaws.SandboxRecord, _, _, _ string) (string, error) {
		return "ami-0abc123inj", nil
	}
	flushEC2ObservationsFn = func(_ context.Context, _ *config.Config, _ string) error { return nil }
	fetchEC2ObservedJSONFn = func(_ context.Context, _ *config.Config, _ string) ([]byte, error) {
		return observedJSON, nil
	}

	outFile := filepath.Join(t.TempDir(), "learned-test.yaml")
	err := runLearnPostExit(context.Background(), cfg, fetcher, "sb-ami-test", outFile, true)
	if err != nil {
		t.Fatalf("runLearnPostExit returned error: %v", err)
	}

	content, readErr := os.ReadFile(outFile)
	if readErr != nil {
		t.Fatalf("could not read output file: %v", readErr)
	}
	if !strings.Contains(string(content), "ami-0abc123inj") {
		t.Errorf("expected ami-0abc123inj in generated profile, got:\n%s", string(content))
	}
}

// TestRunLearnPostExit_AMIFlag_BakeFailureWritesProfileWithoutAMI verifies that
// when the bake fails: profile is written without ami field, and exit is non-zero.
func TestRunLearnPostExit_AMIFlag_BakeFailureWritesProfileWithoutAMI(t *testing.T) {
	cfg := &config.Config{}
	fetcher := buildShellAMIFetcher("ec2")
	observedJSON := writeObservedJSON(t)

	oldBake := bakeFromSandboxFn
	oldFlush := flushEC2ObservationsFn
	oldFetch := fetchEC2ObservedJSONFn
	t.Cleanup(func() {
		bakeFromSandboxFn = oldBake
		flushEC2ObservationsFn = oldFlush
		fetchEC2ObservedJSONFn = oldFetch
	})

	bakeFromSandboxFn = func(_ context.Context, _ *config.Config, _ kmaws.SandboxRecord, _, _, _ string) (string, error) {
		return "", fmt.Errorf("simulated bake failure: quota exceeded")
	}
	flushEC2ObservationsFn = func(_ context.Context, _ *config.Config, _ string) error { return nil }
	fetchEC2ObservedJSONFn = func(_ context.Context, _ *config.Config, _ string) ([]byte, error) {
		return observedJSON, nil
	}

	outFile := filepath.Join(t.TempDir(), "learned-fail.yaml")
	err := runLearnPostExit(context.Background(), cfg, fetcher, "sb-ami-test", outFile, true)
	if err == nil {
		t.Fatal("expected non-nil error when bake fails, got nil")
	}
	if !strings.Contains(err.Error(), "AMI bake failed") {
		t.Errorf("expected error to contain 'AMI bake failed', got: %v", err)
	}

	// Profile should still be written (without ami field).
	content, readErr := os.ReadFile(outFile)
	if readErr != nil {
		t.Fatalf("expected profile file to be written even on bake failure: %v", readErr)
	}
	if strings.Contains(string(content), "ami:") {
		t.Errorf("expected profile to NOT contain 'ami:' when bake failed, got:\n%s", string(content))
	}
}

// TestRunLearnPostExit_NoAMIFlag_BehavesAsBefore verifies that bakeAMI=false
// leaves the bake function uncalled and produces a profile without an ami field.
func TestRunLearnPostExit_NoAMIFlag_BehavesAsBefore(t *testing.T) {
	cfg := &config.Config{}
	fetcher := buildShellAMIFetcher("ec2")
	observedJSON := writeObservedJSON(t)

	oldBake := bakeFromSandboxFn
	oldFlush := flushEC2ObservationsFn
	oldFetch := fetchEC2ObservedJSONFn
	t.Cleanup(func() {
		bakeFromSandboxFn = oldBake
		flushEC2ObservationsFn = oldFlush
		fetchEC2ObservedJSONFn = oldFetch
	})

	bakeCalled := false
	bakeFromSandboxFn = func(_ context.Context, _ *config.Config, _ kmaws.SandboxRecord, _, _, _ string) (string, error) {
		bakeCalled = true
		return "ami-shouldnotbecalled", nil
	}
	flushEC2ObservationsFn = func(_ context.Context, _ *config.Config, _ string) error { return nil }
	fetchEC2ObservedJSONFn = func(_ context.Context, _ *config.Config, _ string) ([]byte, error) {
		return observedJSON, nil
	}

	outFile := filepath.Join(t.TempDir(), "learned-noami.yaml")
	err := runLearnPostExit(context.Background(), cfg, fetcher, "sb-ami-test", outFile, false)
	if err != nil {
		t.Fatalf("runLearnPostExit returned error: %v", err)
	}
	if bakeCalled {
		t.Error("expected bake function NOT to be called when bakeAMI=false")
	}

	content, _ := os.ReadFile(outFile)
	if strings.Contains(string(content), "ami:") {
		t.Errorf("expected no 'ami:' field in profile when bakeAMI=false, got:\n%s", string(content))
	}
}

// TestRunLearnPostExit_AMIFlag_DockerSubstrate_SkippedWithWarning verifies that
// docker substrate with bakeAMI=true: bake is NOT called, profile generated without ami, success.
func TestRunLearnPostExit_AMIFlag_DockerSubstrate_SkippedWithWarning(t *testing.T) {
	cfg := &config.Config{}
	// Docker substrate fetcher — no instance ARN needed.
	fetcher := &shellAMIFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-docker-ami",
			Profile:   "open-dev",
			Substrate: "docker",
			Region:    "us-east-1",
			Status:    "running",
		},
	}

	oldBake := bakeFromSandboxFn
	t.Cleanup(func() { bakeFromSandboxFn = oldBake })

	bakeCalled := false
	bakeFromSandboxFn = func(_ context.Context, _ *config.Config, _ kmaws.SandboxRecord, _, _, _ string) (string, error) {
		bakeCalled = true
		return "ami-shouldnotbecalled", nil
	}

	// Docker substrate takes the docker path — CollectDockerObservations is called
	// with nil readers (no real containers) which returns valid empty JSON.
	outFile := filepath.Join(t.TempDir(), "learned-docker.yaml")
	err := runLearnPostExit(context.Background(), cfg, fetcher, "sb-docker-ami", outFile, true)
	// Docker path succeeds (empty observation JSON is valid).
	if err != nil {
		t.Fatalf("runLearnPostExit returned error for docker substrate: %v", err)
	}
	if bakeCalled {
		t.Error("expected bake function NOT to be called for docker substrate")
	}

	content, _ := os.ReadFile(outFile)
	if strings.Contains(string(content), "ami:") {
		t.Errorf("expected no 'ami:' field in docker profile, got:\n%s", string(content))
	}
}

// TestNewShellCmd_AMIFlagWithoutLearn_Errors verifies that passing --ami without
// --learn returns an error mentioning "--ami requires --learn".
func TestNewShellCmd_AMIFlagWithoutLearn_Errors(t *testing.T) {
	cfg := &config.Config{}
	fetcher := buildShellAMIFetcher("ec2")

	root := &cobra.Command{Use: "km"}
	shellCmd := NewShellCmdWithFetcher(cfg, fetcher, func(_ *exec.Cmd) error { return nil })
	root.AddCommand(shellCmd)
	root.SetArgs([]string{"shell", "--ami", "sb-ami-test"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when --ami used without --learn, got nil")
	}
	if !strings.Contains(err.Error(), "--ami requires --learn") {
		t.Errorf("expected error to contain '--ami requires --learn', got: %v", err)
	}
}

// TestGenerateProfileFromJSON_WithAMIID_EmitsRuntimeAMI is a direct unit test of
// the modified GenerateProfileFromJSON signature; passing amiID embeds spec.runtime.ami.
func TestGenerateProfileFromJSON_WithAMIID_EmitsRuntimeAMI(t *testing.T) {
	data := []byte(`{"dns":["example.com"],"hosts":[],"repos":[]}`)
	yamlBytes, err := GenerateProfileFromJSON(data, "", "ami-0xyz789")
	if err != nil {
		t.Fatalf("GenerateProfileFromJSON returned error: %v", err)
	}
	if !strings.Contains(string(yamlBytes), "ami-0xyz789") {
		t.Errorf("expected ami-0xyz789 in generated YAML, got:\n%s", string(yamlBytes))
	}
}
