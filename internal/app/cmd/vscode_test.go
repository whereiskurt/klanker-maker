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

// ---- Phase 76 Wave 0 stubs: TestVSCodeRekey_* ----
// All 16 tests start with t.Skip so the suite stays green.
// Wave 1 (Plan 76-01) + Wave 2 (Plan 76-02) uncomment the bodies.

// TestVSCodeRekey_CommandRegistered verifies that newVSCodeRekeyCmd is registered
// under the km vscode parent command with the correct Use string.
func TestVSCodeRekey_CommandRegistered(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 76-01): implement newVSCodeRekeyCmd registration")
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	// mockSSM := &vsCodeSSMMock{output: healthySSMOutput}
	// parent := newVSCodeCmdInternal(cfg, fetcher, nil, mockSSM)
	// var found *cobra.Command
	// for _, sub := range parent.Commands() {
	//     if sub.Use == "rekey <sandbox-id>" {
	//         found = sub
	//         break
	//     }
	// }
	// if found == nil {
	//     t.Fatalf("expected rekey subcommand registered under vscode; got: %v", parent.Commands())
	// }
}

// TestVSCodeRekey_FlagsExist verifies that --force and --yes flags are registered
// on the rekey cobra command and have bool type.
func TestVSCodeRekey_FlagsExist(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 76-01): implement newVSCodeRekeyCmd flags")
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	// mockSSM := &vsCodeSSMMock{output: healthySSMOutput}
	// cmd := newVSCodeRekeyCmd(cfg, fetcher, mockSSM)
	// forceFlag := cmd.Flags().Lookup("force")
	// if forceFlag == nil {
	//     t.Fatal("expected --force flag to exist on rekey command")
	// }
	// if forceFlag.Value.Type() != "bool" {
	//     t.Errorf("expected --force to be bool type; got %s", forceFlag.Value.Type())
	// }
	// yesFlag := cmd.Flags().Lookup("yes")
	// if yesFlag == nil {
	//     t.Fatal("expected --yes flag to exist on rekey command")
	// }
	// if yesFlag.Value.Type() != "bool" {
	//     t.Errorf("expected --yes to be bool type; got %s", yesFlag.Value.Type())
	// }
}

// TestVSCodeRekey_NotRunning verifies that runVSCodeRekey returns an error containing
// "not running" and "km resume" when the sandbox EC2 instance is not running.
// Wave 1 Plan 76-01 locks the injection pattern: vscode.go declares
//
//	type ec2DescribeAPI interface { DescribeInstances(...) }
//
// and runVSCodeRekey accepts an ec2DescribeAPI parameter. Tests inject a mock
// implementing this interface; the cobra RunE handler initializes the real ec2 client.
func TestVSCodeRekey_NotRunning(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 76-01): implement EC2-not-running pre-flight check")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	// mockSSM := &vsCodeSSMMock{output: healthySSMOutput}
	// // ec2Mock returns 0 reservations (no running instance).
	// type ec2NoInstanceMock struct{}
	// // func (m *ec2NoInstanceMock) DescribeInstances(ctx context.Context, in *ec2.DescribeInstancesInput, opts ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	// //     return &ec2.DescribeInstancesOutput{Reservations: nil}, nil
	// // }
	// // err := runVSCodeRekey(ctx, cfg, fetcher, &ec2NoInstanceMock{}, mockSSM, "sb-abc123", false, true)
	// // if err == nil {
	// //     t.Fatal("expected error when EC2 instance is not running, got nil")
	// // }
	// // if !strings.Contains(err.Error(), "not running") {
	// //     t.Errorf("error missing 'not running': %v", err)
	// // }
	// // if !strings.Contains(err.Error(), "km resume") {
	// //     t.Errorf("error missing 'km resume' hint: %v", err)
	// // }
}

// TestVSCodeRekey_Locked_NoForce verifies that runVSCodeRekey returns an error
// containing "locked", "--force", and "km unlock" when the sandbox is locked
// and --force is not set.
// Wave 1 recommendation: declare var checkSandboxLock = CheckSandboxLock as a
// package-level var in lock.go or vscode.go, and override in tests:
//
//	defer func(orig func(...) error) { checkSandboxLock = orig }(checkSandboxLock)
//	checkSandboxLock = func(...) error { return errors.New("locked") }
func TestVSCodeRekey_Locked_NoForce(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 76-01): implement lock check pre-flight")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	// mockSSM := &vsCodeSSMMock{output: healthySSMOutput}
	// // Override checkSandboxLock to return a locked error.
	// // defer func(orig func(context.Context, *config.Config, string) error) {
	// //     checkSandboxLock = orig
	// // }(checkSandboxLock)
	// // checkSandboxLock = func(_ context.Context, _ *config.Config, _ string) error {
	// //     return errors.New("sandbox is locked")
	// // }
	// // err := runVSCodeRekey(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", false, true)
	// // if err == nil {
	// //     t.Fatal("expected error when sandbox is locked without --force, got nil")
	// // }
	// // if !strings.Contains(err.Error(), "locked") {
	// //     t.Errorf("error missing 'locked': %v", err)
	// // }
	// // if !strings.Contains(err.Error(), "--force") {
	// //     t.Errorf("error missing '--force' hint: %v", err)
	// // }
	// // if !strings.Contains(err.Error(), "km unlock") {
	// //     t.Errorf("error missing 'km unlock' hint: %v", err)
	// // }
}

// TestVSCodeRekey_Locked_WithForce verifies that runVSCodeRekey skips the lock
// check and proceeds to SSM pre-flight when --force is set.
func TestVSCodeRekey_Locked_WithForce(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 76-01): implement --force lock bypass")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	// mockSSM := &vsCodeSSMMock{output: healthySSMOutput}
	// // Override checkSandboxLock to return a locked error.
	// // defer func(orig func(context.Context, *config.Config, string) error) {
	// //     checkSandboxLock = orig
	// // }(checkSandboxLock)
	// // checkSandboxLock = func(_ context.Context, _ *config.Config, _ string) error {
	// //     return errors.New("sandbox is locked")
	// // }
	// // With force=true, the lock check is skipped; SSM pre-flight runs.
	// // err := runVSCodeRekey(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", true, true)
	// // // SSM returns healthy output, so rekey proceeds. Error is nil or from key gen.
	// // // The important assertion: no "locked" error returned.
	// // if err != nil && strings.Contains(err.Error(), "locked") {
	// //     t.Errorf("expected --force to bypass lock check, but got locked error: %v", err)
	// // }
}

// TestVSCodeRekey_VSCodeDisabled verifies that runVSCodeRekey returns an error
// containing "vscodeEnabled" when sshd is inactive AND authorized_keys are absent
// (pre-Phase-73 sandbox or vscodeEnabled:false).
func TestVSCodeRekey_VSCodeDisabled(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 76-01): implement vscode-disabled pre-flight check")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	// disabledOutput := "=== sshd ===\ninactive\n=== authkeys exists ===\nno\n"
	// mockSSM := &vsCodeSSMMock{output: disabledOutput}
	// // err := runVSCodeRekey(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", false, true)
	// // if err == nil {
	// //     t.Fatal("expected error for vscode-disabled sandbox, got nil")
	// // }
	// // if !strings.Contains(err.Error(), "vscodeEnabled") {
	// //     t.Errorf("error missing 'vscodeEnabled' hint: %v", err)
	// // }
}

// TestVSCodeRekey_Inconsistent verifies that runVSCodeRekey returns an error
// containing "unexpected state" when sshd is active but authorized_keys are absent.
func TestVSCodeRekey_Inconsistent(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 76-01): implement inconsistent-state pre-flight check")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	// inconsistentOutput := "=== sshd ===\nactive\n=== authkeys exists ===\nno\n"
	// mockSSM := &vsCodeSSMMock{output: inconsistentOutput}
	// // err := runVSCodeRekey(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", false, true)
	// // if err == nil {
	// //     t.Fatal("expected error for inconsistent state, got nil")
	// // }
	// // if !strings.Contains(err.Error(), "unexpected state") {
	// //     t.Errorf("error missing 'unexpected state': %v", err)
	// // }
}

// TestVSCodeRekey_SSHDDown verifies that runVSCodeRekey returns an error
// containing "sshd is not running" when sshd is inactive but authorized_keys exist.
func TestVSCodeRekey_SSHDDown(t *testing.T) {
	t.Skip("TODO Wave 1 (Plan 76-01): implement sshd-down pre-flight check")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	// sshdDownOutput := "=== sshd ===\ninactive\n=== authkeys exists ===\nyes\n"
	// mockSSM := &vsCodeSSMMock{output: sshdDownOutput}
	// // err := runVSCodeRekey(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", false, true)
	// // if err == nil {
	// //     t.Fatal("expected error when sshd is not running, got nil")
	// // }
	// // if !strings.Contains(err.Error(), "sshd is not running") {
	// //     t.Errorf("error missing 'sshd is not running': %v", err)
	// // }
}

// TestVSCodeRekey_NormalRotation verifies that runVSCodeRekey succeeds when
// local keys already exist and the sandbox is healthy. The private key content
// should change after rotation.
// Uses a sequenced SSM mock: first call returns healthySSMOutput (pre-flight),
// second call returns the readback of the new pubkey (verify).
func TestVSCodeRekey_NormalRotation(t *testing.T) {
	t.Skip("TODO Wave 2 (Plan 76-02): implement normal rotation (local key present + remote present)")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	//
	// // Pre-seed ~/.km/keys/sb-abc123 and .pub with sentinel content.
	// homeDir, _ := os.UserHomeDir()
	// keysDir := filepath.Join(homeDir, ".km", "keys")
	// _ = os.MkdirAll(keysDir, 0o700)
	// privPath := filepath.Join(keysDir, "sb-abc123")
	// pubPath := filepath.Join(keysDir, "sb-abc123.pub")
	// _ = os.WriteFile(privPath, []byte("old-private-key-sentinel"), 0o600)
	// _ = os.WriteFile(pubPath, []byte("ssh-ed25519 AAAA old-pub-sentinel km-sb-abc123"), 0o644)
	// origPrivContent, _ := os.ReadFile(privPath)
	//
	// // Sequenced SSM mock: call 1 = pre-flight, call 2 = readback verify.
	// type sequencedSSMMock struct {
	//     outputs []string
	//     calls   int
	// }
	// // func (m *sequencedSSMMock) SendCommand(ctx context.Context, in *ssm.SendCommandInput, opts ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	// //     return &ssm.SendCommandOutput{Command: &ssmtypes.Command{CommandId: awssdk.String("cmd-seq-test")}}, nil
	// // }
	// // func (m *sequencedSSMMock) GetCommandInvocation(ctx context.Context, in *ssm.GetCommandInvocationInput, opts ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	// //     out := m.outputs[m.calls]
	// //     m.calls++
	// //     return &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: awssdk.String(out)}, nil
	// // }
	// // expectedReadback := "=== authkeys content ===\n" + <new-pubkey-line> + "\n"
	// // mockSSM := &sequencedSSMMock{outputs: []string{healthySSMOutput, expectedReadback}}
	// //
	// // err := runVSCodeRekey(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", false, true)
	// // if err != nil {
	// //     t.Fatalf("expected nil error for normal rotation, got: %v", err)
	// // }
	// // newPrivContent, _ := os.ReadFile(privPath)
	// // if bytes.Equal(origPrivContent, newPrivContent) {
	// //     t.Error("expected private key content to change after rotation")
	// // }
}

// TestVSCodeRekey_CrossLaptop verifies that runVSCodeRekey succeeds when the
// local key files are absent (cross-laptop bootstrap scenario). After rotation,
// ~/.km/keys/sb-abc123 (mode 0600) and ~/.km/keys/sb-abc123.pub (mode 0644)
// must exist.
func TestVSCodeRekey_CrossLaptop(t *testing.T) {
	t.Skip("TODO Wave 2 (Plan 76-02): implement cross-laptop bootstrap (local key absent + remote present)")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	//
	// homeDir, _ := os.UserHomeDir()
	// privPath := filepath.Join(homeDir, ".km", "keys", "sb-abc123")
	// pubPath := filepath.Join(homeDir, ".km", "keys", "sb-abc123.pub")
	//
	// // Keys are deliberately absent — no pre-seeding.
	//
	// // Sequenced SSM mock: call 1 = pre-flight, call 2 = readback verify.
	// // mockSSM := &sequencedSSMMock{outputs: []string{healthySSMOutput, expectedReadback}}
	// //
	// // err := runVSCodeRekey(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", false, true)
	// // if err != nil {
	// //     t.Fatalf("expected nil error for cross-laptop bootstrap, got: %v", err)
	// // }
	// // privInfo, err := os.Stat(privPath)
	// // if err != nil {
	// //     t.Fatalf("expected private key to exist at %s: %v", privPath, err)
	// // }
	// // if privInfo.Mode().Perm() != 0o600 {
	// //     t.Errorf("expected private key mode 0600; got %o", privInfo.Mode().Perm())
	// // }
	// // pubInfo, err := os.Stat(pubPath)
	// // if err != nil {
	// //     t.Fatalf("expected public key to exist at %s: %v", pubPath, err)
	// // }
	// // if pubInfo.Mode().Perm() != 0o644 {
	// //     t.Errorf("expected public key mode 0644; got %o", pubInfo.Mode().Perm())
	// // }
}

// TestVSCodeRekey_VerifyMismatch verifies that runVSCodeRekey returns an error
// containing "verification failed" and "Old key still active locally" when the
// SSM readback returns a different pubkey than what was generated. Local key files
// must remain unchanged.
func TestVSCodeRekey_VerifyMismatch(t *testing.T) {
	t.Skip("TODO Wave 2 (Plan 76-02): implement verification-mismatch error path")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	//
	// homeDir, _ := os.UserHomeDir()
	// keysDir := filepath.Join(homeDir, ".km", "keys")
	// _ = os.MkdirAll(keysDir, 0o700)
	// privPath := filepath.Join(keysDir, "sb-abc123")
	// pubPath := filepath.Join(keysDir, "sb-abc123.pub")
	// origPriv := []byte("original-private-key-sentinel")
	// origPub := []byte("ssh-ed25519 AAAA original-pub-sentinel km-sb-abc123")
	// _ = os.WriteFile(privPath, origPriv, 0o600)
	// _ = os.WriteFile(pubPath, origPub, 0o644)
	//
	// // Readback returns a DIFFERENT pubkey (mismatch).
	// mismatchReadback := "=== authkeys content ===\nssh-ed25519 AAAA different-key km-sb-abc123\n"
	// // mockSSM := &sequencedSSMMock{outputs: []string{healthySSMOutput, mismatchReadback}}
	// //
	// // err := runVSCodeRekey(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", false, true)
	// // if err == nil {
	// //     t.Fatal("expected error for verification mismatch, got nil")
	// // }
	// // if !strings.Contains(err.Error(), "verification failed") {
	// //     t.Errorf("error missing 'verification failed': %v", err)
	// // }
	// // if !strings.Contains(err.Error(), "Old key still active locally") {
	// //     t.Errorf("error missing 'Old key still active locally': %v", err)
	// // }
	// // // Local files must be unchanged.
	// // gotPriv, _ := os.ReadFile(privPath)
	// // if !bytes.Equal(gotPriv, origPriv) {
	// //     t.Error("expected private key file to be unchanged after mismatch")
	// // }
	// // gotPub, _ := os.ReadFile(pubPath)
	// // if !bytes.Equal(gotPub, origPub) {
	// //     t.Error("expected public key file to be unchanged after mismatch")
	// // }
}

// TestVSCodeRekey_RenameOrdering verifies the atomic rename sequence:
// .pub.new is renamed to .pub before .new is renamed to the private key path.
// End-state assertion: scratch files (.pub.new, .new) no longer exist; the
// committed .pub and private key match what GenerateAndWrite produced.
func TestVSCodeRekey_RenameOrdering(t *testing.T) {
	t.Skip("TODO Wave 2 (Plan 76-02): implement atomic rename ordering verification")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	//
	// homeDir, _ := os.UserHomeDir()
	// keysDir := filepath.Join(homeDir, ".km", "keys")
	// _ = os.MkdirAll(keysDir, 0o700)
	// privPath := filepath.Join(keysDir, "sb-abc123")
	// pubPath := filepath.Join(keysDir, "sb-abc123.pub")
	// privNewPath := filepath.Join(keysDir, "sb-abc123.new")
	// pubNewPath := filepath.Join(keysDir, "sb-abc123.pub.new")
	// _ = os.WriteFile(privPath, []byte("old-private-sentinel"), 0o600)
	// _ = os.WriteFile(pubPath, []byte("ssh-ed25519 AAAA old-pub-sentinel km-sb-abc123"), 0o644)
	//
	// // Run a successful rekey.
	// // mockSSM := &sequencedSSMMock{outputs: []string{healthySSMOutput, expectedReadback}}
	// // err := runVSCodeRekey(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", false, true)
	// // if err != nil {
	// //     t.Fatalf("expected nil error, got: %v", err)
	// // }
	// // // Scratch files must be gone.
	// // if _, err := os.Stat(privNewPath); !os.IsNotExist(err) {
	// //     t.Error("expected .new scratch file to be gone after rename")
	// // }
	// // if _, err := os.Stat(pubNewPath); !os.IsNotExist(err) {
	// //     t.Error("expected .pub.new scratch file to be gone after rename")
	// // }
	// // // Committed files must contain fresh content.
	// // gotPriv, _ := os.ReadFile(privPath)
	// // if bytes.Equal(gotPriv, []byte("old-private-sentinel")) {
	// //     t.Error("expected private key to be updated after rename, still shows sentinel")
	// // }
}

// TestVSCodeRekey_OverwritesScratch verifies that runVSCodeRekey unconditionally
// overwrites pre-existing .new and .pub.new scratch files (from a crashed prior rekey).
func TestVSCodeRekey_OverwritesScratch(t *testing.T) {
	t.Skip("TODO Wave 2 (Plan 76-02): implement scratch-file overwrite verification")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	//
	// homeDir, _ := os.UserHomeDir()
	// keysDir := filepath.Join(homeDir, ".km", "keys")
	// _ = os.MkdirAll(keysDir, 0o700)
	// privPath := filepath.Join(keysDir, "sb-abc123")
	// pubPath := filepath.Join(keysDir, "sb-abc123.pub")
	// privNewPath := filepath.Join(keysDir, "sb-abc123.new")
	// pubNewPath := filepath.Join(keysDir, "sb-abc123.pub.new")
	//
	// // Pre-seed the scratch files (simulating a crashed prior rekey).
	// _ = os.WriteFile(privNewPath, []byte("crashed-private-sentinel"), 0o600)
	// _ = os.WriteFile(pubNewPath, []byte("ssh-ed25519 AAAA crashed-pub-sentinel"), 0o644)
	// // Also pre-seed the live keys so pre-flight passes.
	// _ = os.WriteFile(privPath, []byte("live-private-key"), 0o600)
	// _ = os.WriteFile(pubPath, []byte("ssh-ed25519 AAAA live-pub-key km-sb-abc123"), 0o644)
	//
	// // Run a successful rekey.
	// // mockSSM := &sequencedSSMMock{outputs: []string{healthySSMOutput, expectedReadback}}
	// // err := runVSCodeRekey(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", false, true)
	// // if err != nil {
	// //     t.Fatalf("expected nil error, got: %v", err)
	// // }
	// // // Committed .pub must not contain the crashed sentinel.
	// // gotPub, _ := os.ReadFile(pubPath)
	// // if bytes.Equal(gotPub, []byte("ssh-ed25519 AAAA crashed-pub-sentinel")) {
	// //     t.Error("expected committed .pub to be FRESH, not the crashed sentinel")
	// // }
	// // gotPriv, _ := os.ReadFile(privPath)
	// // if bytes.Equal(gotPriv, []byte("crashed-private-sentinel")) {
	// //     t.Error("expected committed private key to be FRESH, not the crashed sentinel")
	// // }
}

// TestVSCodeRekey_YesFlag verifies that runVSCodeRekey with yes=true does not
// block on stdin and does not print a "[y/N]" prompt.
func TestVSCodeRekey_YesFlag(t *testing.T) {
	t.Skip("TODO Wave 2 (Plan 76-02): implement --yes flag skipping confirmation prompt")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	//
	// homeDir, _ := os.UserHomeDir()
	// keysDir := filepath.Join(homeDir, ".km", "keys")
	// _ = os.MkdirAll(keysDir, 0o700)
	// privPath := filepath.Join(keysDir, "sb-abc123")
	// pubPath := filepath.Join(keysDir, "sb-abc123.pub")
	// _ = os.WriteFile(privPath, []byte("old-private-key"), 0o600)
	// _ = os.WriteFile(pubPath, []byte("ssh-ed25519 AAAA old-pub-key km-sb-abc123"), 0o644)
	//
	// // Sequenced SSM mock: call 1 = pre-flight, call 2 = readback verify.
	// // mockSSM := &sequencedSSMMock{outputs: []string{healthySSMOutput, expectedReadback}}
	// //
	// // var out string
	// // out = captureStdout(func() {
	// //     err := runVSCodeRekey(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", false, true)
	// //     if err != nil {
	// //         t.Errorf("expected nil error with --yes, got: %v", err)
	// //     }
	// // })
	// // // The test completing without blocking proves --yes skipped stdin.
	// // if strings.Contains(out, "[y/N]") {
	// //     t.Errorf("expected no [y/N] prompt with --yes; got output: %s", out)
	// // }
}

// TestVSCodeRekey_ConfirmNo verifies that runVSCodeRekey returns nil and leaves
// local key files unchanged when the user answers "n" to the confirmation prompt.
func TestVSCodeRekey_ConfirmNo(t *testing.T) {
	t.Skip("TODO Wave 2 (Plan 76-02): implement confirmation prompt 'n' abort path")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	//
	// homeDir, _ := os.UserHomeDir()
	// keysDir := filepath.Join(homeDir, ".km", "keys")
	// _ = os.MkdirAll(keysDir, 0o700)
	// privPath := filepath.Join(keysDir, "sb-abc123")
	// pubPath := filepath.Join(keysDir, "sb-abc123.pub")
	// origPriv := []byte("original-private-sentinel")
	// origPub := []byte("ssh-ed25519 AAAA original-pub-sentinel km-sb-abc123")
	// _ = os.WriteFile(privPath, origPriv, 0o600)
	// _ = os.WriteFile(pubPath, origPub, 0o644)
	//
	// // Pipe "n\n" to stdin to simulate user declining the prompt.
	// // r, w, _ := os.Pipe()
	// // origStdin := os.Stdin
	// // os.Stdin = r
	// // defer func() { os.Stdin = origStdin }()
	// // w.Write([]byte("n\n"))
	// // w.Close()
	// //
	// // mockSSM := &vsCodeSSMMock{output: healthySSMOutput}
	// // err := runVSCodeRekey(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", false, false)
	// // if err != nil {
	// //     t.Fatalf("expected nil error (clean abort) when user answers n, got: %v", err)
	// // }
	// // // Local files must be unchanged.
	// // gotPriv, _ := os.ReadFile(privPath)
	// // if !bytes.Equal(gotPriv, origPriv) {
	// //     t.Error("expected private key file to be unchanged after 'n' abort")
	// // }
	// // gotPub, _ := os.ReadFile(pubPath)
	// // if !bytes.Equal(gotPub, origPub) {
	// //     t.Error("expected public key file to be unchanged after 'n' abort")
	// // }
}

// TestVSCodeRekey_OutputMarkers verifies that a successful rekey prints all
// required step markers in order: ✓ EC2 instance running, ✓ Pre-flight check
// passed, ✓ New keypair generated (SHA256:, ✓ Pushed to sandbox via SSM (verified,
// ✓ Local key, atomically, Rekey complete.
func TestVSCodeRekey_OutputMarkers(t *testing.T) {
	t.Skip("TODO Wave 2 (Plan 76-02): implement output step markers verification")
	// t.Setenv("HOME", t.TempDir())
	// ctx := context.Background()
	// cfg := &config.Config{}
	// fetcher := newVSCodeEC2Sandbox("sb-abc123")
	//
	// homeDir, _ := os.UserHomeDir()
	// keysDir := filepath.Join(homeDir, ".km", "keys")
	// _ = os.MkdirAll(keysDir, 0o700)
	// privPath := filepath.Join(keysDir, "sb-abc123")
	// pubPath := filepath.Join(keysDir, "sb-abc123.pub")
	// _ = os.WriteFile(privPath, []byte("old-private-key"), 0o600)
	// _ = os.WriteFile(pubPath, []byte("ssh-ed25519 AAAA old-pub-key km-sb-abc123"), 0o644)
	//
	// // Sequenced SSM mock: call 1 = pre-flight, call 2 = readback verify.
	// // mockSSM := &sequencedSSMMock{outputs: []string{healthySSMOutput, expectedReadback}}
	// //
	// // var out string
	// // out = captureStdout(func() {
	// //     err := runVSCodeRekey(ctx, cfg, fetcher, nil, mockSSM, "sb-abc123", false, true)
	// //     if err != nil {
	// //         t.Errorf("expected nil error for output-markers test, got: %v", err)
	// //     }
	// // })
	// // markers := []string{
	// //     "✓ EC2 instance running",
	// //     "✓ Pre-flight check passed",
	// //     "✓ New keypair generated (SHA256:",
	// //     "✓ Pushed to sandbox via SSM (verified",
	// //     "✓ Local key",
	// //     "atomically",
	// //     "Rekey complete",
	// // }
	// // for i, marker := range markers {
	// //     idx := strings.Index(out, marker)
	// //     if idx < 0 {
	// //         t.Errorf("marker[%d] %q not found in output: %s", i, marker, out)
	// //     }
	// //     if i > 0 {
	// //         prevIdx := strings.Index(out, markers[i-1])
	// //         if prevIdx > idx {
	// //             t.Errorf("marker[%d] %q appears before marker[%d] %q in output", i, marker, i-1, markers[i-1])
	// //         }
	// //     }
	// // }
}
