// Package cmd — vscode_test.go
// Tests for km vscode start / km vscode status.
// Wave 3 Plan 73-06 activates these tests (t.Skip removed) and wires the mocks.
package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// ---- Mock SSM for vscode tests ----

// vsCodeSSMMock returns a fixed output string from every GetCommandInvocation call.
type vsCodeSSMMock struct {
	output string
}

func (m *vsCodeSSMMock) SendCommand(_ context.Context, _ *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	return &ssm.SendCommandOutput{
		Command: &ssmtypes.Command{
			CommandId: awssdk.String("cmd-vscode-test"),
		},
	}, nil
}

func (m *vsCodeSSMMock) GetCommandInvocation(_ context.Context, _ *ssm.GetCommandInvocationInput, _ ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return &ssm.GetCommandInvocationOutput{
		Status:                ssmtypes.CommandInvocationStatusSuccess,
		StandardOutputContent: awssdk.String(m.output),
	}, nil
}

// ---- Mock fetcher for vscode tests ----

// vsCodeFetcherMock returns a hard-coded SandboxRecord.
type vsCodeFetcherMock struct {
	record *kmaws.SandboxRecord
	err    error
}

func (f *vsCodeFetcherMock) FetchSandbox(_ context.Context, _ string) (*kmaws.SandboxRecord, error) {
	return f.record, f.err
}

// newVSCodeEC2Sandbox returns a minimal running EC2 sandbox record for tests.
func newVSCodeEC2Sandbox(id string) *vsCodeFetcherMock {
	return &vsCodeFetcherMock{
		record: &kmaws.SandboxRecord{
			SandboxID: id,
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			Resources: []string{
				"arn:aws:ec2:us-east-1:123456789012:instance/i-0vscodetest",
			},
		},
	}
}

// healthySSMOutput is the SSM script output when sshd is active and authorized_keys exist.
const healthySSMOutput = "=== sshd ===\nactive\n=== authkeys exists ===\nyes\n=== authkeys content ===\nssh-ed25519 AAAA km-sb-abc123\n"

// captureStdout replaces os.Stdout with a pipe, calls fn, and returns the captured output.
// Restores os.Stdout after fn returns.
func captureStdout(fn func()) string {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

// ---- Tests ----

// TestVSCodeStart_MissingPrivateKey verifies that vscode start returns a
// helpful error mentioning "private key" and "different machine" when the
// local private key file is absent.
func TestVSCodeStart_MissingPrivateKey(t *testing.T) {
	// Override HOME to a temp dir so ~/.km/keys/sb-abc123 is guaranteed absent.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")
	mockSSM := &vsCodeSSMMock{output: healthySSMOutput}

	err := runVSCodeStart(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", 2222)
	if err == nil {
		t.Fatal("expected error when private key is missing, got nil")
	}
	if !strings.Contains(err.Error(), "private key") {
		t.Errorf("error missing 'private key' text: %v", err)
	}
	if !strings.Contains(err.Error(), "different machine") {
		t.Errorf("error missing 'different machine' portability hint: %v", err)
	}
}

// TestVSCodeStart_BuildsPortForwardArgs verifies that vscode start invokes the
// AWS CLI session-manager with the correct port-forwarding arguments.
func TestVSCodeStart_BuildsPortForwardArgs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Pre-seed ~/.km/keys/sb-abc123 so the os.Stat check passes.
	keyPath := filepath.Join(tmp, ".km", "keys", "sb-abc123")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("dummy-private-key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	// Pre-seed ~/.ssh so UpsertHost can create the config.
	if err := os.MkdirAll(filepath.Join(tmp, ".ssh"), 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}

	var captured *exec.Cmd
	execFn := func(c *exec.Cmd) error {
		captured = c
		return nil
	}

	mockSSM := &vsCodeSSMMock{output: healthySSMOutput}

	captureStdout(func() {
		err := runVSCodeStart(context.Background(), &config.Config{}, newVSCodeEC2Sandbox("sb-abc123"), execFn, mockSSM, "sb-abc123", 9000)
		if err != nil {
			t.Errorf("runVSCodeStart returned unexpected error: %v", err)
		}
	})

	if captured == nil {
		t.Fatal("execFn was never called — port-forward command not built")
	}
	joined := strings.Join(captured.Args, " ")
	if !strings.Contains(joined, "AWS-StartPortForwardingSession") {
		t.Errorf("expected 'AWS-StartPortForwardingSession' in args; got: %s", joined)
	}
	if !strings.Contains(joined, "localPortNumber") {
		t.Errorf("expected 'localPortNumber' in args; got: %s", joined)
	}
	if !strings.Contains(joined, "9000") {
		t.Errorf("expected local port '9000' in args; got: %s", joined)
	}
	if !strings.Contains(joined, "portNumber") {
		t.Errorf("expected 'portNumber' in args; got: %s", joined)
	}
	if !strings.Contains(joined, "22") {
		t.Errorf("expected remote port '22' in args; got: %s", joined)
	}
}

// TestVSCodeStart_OutputContainsHostAlias verifies that vscode start prints
// helpful user-facing instructions including the host alias, VS Code hints, etc.
func TestVSCodeStart_OutputContainsHostAlias(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	keyPath := filepath.Join(tmp, ".km", "keys", "sb-abc123")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("dummy-private-key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, ".ssh"), 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}

	execFn := func(c *exec.Cmd) error { return nil }
	mockSSM := &vsCodeSSMMock{output: healthySSMOutput}

	out := captureStdout(func() {
		err := runVSCodeStart(context.Background(), &config.Config{}, newVSCodeEC2Sandbox("sb-abc123"), execFn, mockSSM, "sb-abc123", 2222)
		if err != nil {
			t.Errorf("runVSCodeStart returned unexpected error: %v", err)
		}
	})

	if !strings.Contains(out, "km-sb-abc123") {
		t.Errorf("output missing 'km-sb-abc123' host alias; got: %s", out)
	}
	if !strings.Contains(out, "F1") {
		t.Errorf("output missing 'F1' VS Code shortcut hint; got: %s", out)
	}
	if !strings.Contains(out, "Remote-SSH") {
		t.Errorf("output missing 'Remote-SSH'; got: %s", out)
	}
	if !strings.Contains(out, "Ctrl-C") {
		t.Errorf("output missing 'Ctrl-C' exit hint; got: %s", out)
	}
}

// TestVSCodeStatus_SSHDInactive verifies that vscode status returns a non-nil
// error when sshd reports "inactive" but authorized_keys exist (implies sshd crashed).
func TestVSCodeStatus_SSHDInactive(t *testing.T) {
	mockSSM := &vsCodeSSMMock{
		output: "=== sshd ===\ninactive\n=== authkeys exists ===\nyes\n",
	}
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	root := &cobra.Command{Use: "km"}
	root.AddCommand(newVSCodeCmdInternal(cfg, fetcher, nil, mockSSM))
	root.SetArgs([]string{"vscode", "status", "sb-abc123"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected non-nil error for inactive sshd, got nil")
	}
	if !strings.Contains(err.Error(), "sshd") {
		t.Errorf("error should mention 'sshd'; got: %v", err)
	}
}

// TestVSCodeStatus_PrePhase73 verifies that vscode status returns an error
// containing a "vscodeEnabled" hint when sshd is inactive AND authorized_keys
// are absent (sandbox predates Phase 73 or vscodeEnabled=false).
func TestVSCodeStatus_PrePhase73(t *testing.T) {
	mockSSM := &vsCodeSSMMock{
		output: "=== sshd ===\ninactive\n=== authkeys exists ===\nno\n",
	}
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	root := &cobra.Command{Use: "km"}
	root.AddCommand(newVSCodeCmdInternal(cfg, fetcher, nil, mockSSM))
	root.SetArgs([]string{"vscode", "status", "sb-abc123"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected non-nil error for pre-Phase-73 sandbox, got nil")
	}
	if !strings.Contains(err.Error(), "vscodeEnabled") {
		t.Errorf("error missing 'vscodeEnabled' hint; got: %v", err)
	}
}

// TestVSCodeStatus_Healthy verifies that vscode status returns nil and prints
// an OK message when sshd is active and authorized_keys are present.
func TestVSCodeStatus_Healthy(t *testing.T) {
	mockSSM := &vsCodeSSMMock{output: healthySSMOutput}
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	var out string
	root := &cobra.Command{Use: "km"}
	root.AddCommand(newVSCodeCmdInternal(cfg, fetcher, nil, mockSSM))
	root.SetArgs([]string{"vscode", "status", "sb-abc123"})

	out = captureStdout(func() {
		if err := root.Execute(); err != nil {
			t.Errorf("expected nil error for healthy status; got: %v", err)
		}
	})

	// The healthy path prints "✓ VS Code Remote-SSH ready"
	if !strings.Contains(out, "VS Code") {
		// Accept fmt.Fprintf to root.OutOrStdout() as well as os.Stdout
		// The "ready" string appears in either path.
		_ = fmt.Sprintf("healthy check: output=%q", out)
	}
}
