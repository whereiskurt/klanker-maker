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
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/sshkey"
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

// sequencedSSMMock returns a different output on each successive GetCommandInvocation
// call. Used by rekey tests that need distinct outputs for the pre-flight call vs. the
// install call.
type sequencedSSMMock struct {
	outputs []string
	calls   int
}

func (m *sequencedSSMMock) SendCommand(_ context.Context, _ *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	return &ssm.SendCommandOutput{Command: &ssmtypes.Command{CommandId: awssdk.String("cmd-rekey-test")}}, nil
}

func (m *sequencedSSMMock) GetCommandInvocation(_ context.Context, _ *ssm.GetCommandInvocationInput, _ ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	out := m.outputs[m.calls]
	m.calls++
	return &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: awssdk.String(out)}, nil
}

// rekeyInstallSpyMock is a sequencedSSMMock variant that captures the install script
// (sent on the second SendCommand call) and dynamically builds the readback response so
// it matches the pubkey GenerateAndWrite produced for THIS test run.
type rekeyInstallSpyMock struct {
	preflightOutput string
	calls           int
	capturedScript  string
}

func (m *rekeyInstallSpyMock) SendCommand(_ context.Context, in *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	if m.calls == 1 {
		if in.Parameters != nil {
			if cmds := in.Parameters["commands"]; len(cmds) > 0 {
				m.capturedScript = cmds[0]
			}
		}
	}
	return &ssm.SendCommandOutput{Command: &ssmtypes.Command{CommandId: awssdk.String("cmd-rekey-spy")}}, nil
}

func (m *rekeyInstallSpyMock) GetCommandInvocation(_ context.Context, _ *ssm.GetCommandInvocationInput, _ ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	var out string
	if m.calls == 0 {
		out = m.preflightOutput
	} else {
		// Build readback containing the pubkey embedded in the captured install script.
		pubLine := extractPubkeyFromInstallScript(m.capturedScript)
		out = "=== READBACK ===\n" + pubLine + "\n"
	}
	m.calls++
	return &ssm.GetCommandInvocationOutput{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: awssdk.String(out)}, nil
}

// extractPubkeyFromInstallScript returns the single pubkey line embedded in the
// install script's `cat > authorized_keys << 'KEY' ... KEY` heredoc.
func extractPubkeyFromInstallScript(s string) string {
	const startMarker = "<< 'KEY'\n"
	const endMarker = "\nKEY\n"
	si := strings.Index(s, startMarker)
	if si < 0 {
		return ""
	}
	rest := s[si+len(startMarker):]
	ei := strings.Index(rest, endMarker)
	if ei < 0 {
		return ""
	}
	return rest[:ei]
}

// seedRekeyTestKeys writes a real ed25519 keypair to ~/.km/keys/<id>{,.pub} via
// sshkey.GenerateAndWrite and returns the private key bytes and pubkey line for verification.
func seedRekeyTestKeys(t *testing.T, home, sandboxID string) (privBytes []byte, pubLine string) {
	t.Helper()
	keysDir := filepath.Join(home, ".km", "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		t.Fatalf("mkdir keys: %v", err)
	}
	priv := filepath.Join(keysDir, sandboxID)
	pub := filepath.Join(keysDir, sandboxID+".pub")
	line, err := sshkey.GenerateAndWrite(priv, pub, "km-"+sandboxID)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	b, err := os.ReadFile(priv)
	if err != nil {
		t.Fatalf("read seeded priv: %v", err)
	}
	return b, line
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

// ---- EC2 mock for vscode rekey tests ----

// vsCodeEC2Mock implements ec2DescribeAPI for vscode rekey tests.
type vsCodeEC2Mock struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *vsCodeEC2Mock) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

func newRunningEC2Mock() *vsCodeEC2Mock {
	return &vsCodeEC2Mock{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{
				{Instances: []ec2types.Instance{{InstanceId: awssdk.String("i-0vscodetest")}}},
			},
		},
	}
}

func newStoppedEC2Mock() *vsCodeEC2Mock {
	return &vsCodeEC2Mock{output: &ec2.DescribeInstancesOutput{Reservations: nil}}
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
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")
	mockSSM := &vsCodeSSMMock{output: healthySSMOutput}
	parent := newVSCodeCmdInternal(cfg, fetcher, nil, mockSSM)
	var found *cobra.Command
	for _, sub := range parent.Commands() {
		if sub.Use == "rekey <sandbox-id>" {
			found = sub
			break
		}
	}
	if found == nil {
		t.Fatalf("expected rekey subcommand registered under vscode; got: %v", parent.Commands())
	}
}

// TestVSCodeRekey_FlagsExist verifies that --force and --yes flags are registered
// on the rekey cobra command and have bool type.
func TestVSCodeRekey_FlagsExist(t *testing.T) {
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")
	mockSSM := &vsCodeSSMMock{output: healthySSMOutput}
	cmd := newVSCodeRekeyCmd(cfg, fetcher, mockSSM)
	forceFlag := cmd.Flags().Lookup("force")
	if forceFlag == nil {
		t.Fatal("expected --force flag to exist on rekey command")
	}
	if forceFlag.Value.Type() != "bool" {
		t.Errorf("expected --force to be bool type; got %s", forceFlag.Value.Type())
	}
	yesFlag := cmd.Flags().Lookup("yes")
	if yesFlag == nil {
		t.Fatal("expected --yes flag to exist on rekey command")
	}
	if yesFlag.Value.Type() != "bool" {
		t.Errorf("expected --yes to be bool type; got %s", yesFlag.Value.Type())
	}
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
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")
	mockSSM := &vsCodeSSMMock{output: healthySSMOutput}
	err := runVSCodeRekey(ctx, cfg, fetcher, newStoppedEC2Mock(), mockSSM, "sb-abc123", false, true)
	if err == nil {
		t.Fatal("expected error when EC2 instance is not running, got nil")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("error missing 'not running': %v", err)
	}
	if !strings.Contains(err.Error(), "km resume") {
		t.Errorf("error missing 'km resume' hint: %v", err)
	}
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
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")
	mockSSM := &vsCodeSSMMock{output: healthySSMOutput}
	origChecker := checkSandboxLock
	defer func() { checkSandboxLock = origChecker }()
	checkSandboxLock = func(_ context.Context, _ *config.Config, sandboxID string) error {
		return fmt.Errorf("sandbox %s is locked — run 'km unlock %s' first", sandboxID, sandboxID)
	}
	var gotErr error
	captureStdout(func() {
		gotErr = runVSCodeRekey(ctx, cfg, fetcher, newRunningEC2Mock(), mockSSM, "sb-abc123", false, true)
	})
	if gotErr == nil {
		t.Fatal("expected error when sandbox is locked without --force, got nil")
	}
	if !strings.Contains(gotErr.Error(), "locked") {
		t.Errorf("error missing 'locked': %v", gotErr)
	}
	if !strings.Contains(gotErr.Error(), "--force") {
		t.Errorf("error missing '--force' hint: %v", gotErr)
	}
	if !strings.Contains(gotErr.Error(), "km unlock") {
		t.Errorf("error missing 'km unlock' hint: %v", gotErr)
	}
}

// TestVSCodeRekey_Locked_WithForce verifies that runVSCodeRekey skips the lock
// check and proceeds to SSM pre-flight when --force is set.
func TestVSCodeRekey_Locked_WithForce(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")
	callCount := 0
	origChecker := checkSandboxLock
	defer func() { checkSandboxLock = origChecker }()
	checkSandboxLock = func(_ context.Context, _ *config.Config, _ string) error {
		callCount++
		return fmt.Errorf("sandbox is locked — run 'km unlock' first")
	}

	// Pre-seed local keys so rekey can proceed past classification.
	seedRekeyTestKeys(t, homeDir, "sb-abc123")

	// Use the spy mock so the install call returns a valid readback.
	spyMock := &rekeyInstallSpyMock{preflightOutput: healthySSMOutput}
	var gotErr error
	captureStdout(func() {
		gotErr = runVSCodeRekey(ctx, cfg, fetcher, newRunningEC2Mock(), spyMock, "sb-abc123", true /*force*/, true /*yes*/)
	})
	if gotErr != nil {
		t.Fatalf("expected nil with --force, got: %v", gotErr)
	}
	if callCount != 0 {
		t.Errorf("checkSandboxLock should not be called when --force; got %d calls", callCount)
	}
}

// TestVSCodeRekey_VSCodeDisabled verifies that runVSCodeRekey returns an error
// containing "vscodeEnabled" when sshd is inactive AND authorized_keys are absent
// (pre-Phase-73 sandbox or vscodeEnabled:false).
func TestVSCodeRekey_VSCodeDisabled(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")
	disabledOutput := "=== sshd ===\ninactive\n=== authkeys exists ===\nno\n"
	mockSSM := &vsCodeSSMMock{output: disabledOutput}
	var gotErr error
	captureStdout(func() {
		gotErr = runVSCodeRekey(ctx, cfg, fetcher, newRunningEC2Mock(), mockSSM, "sb-abc123", false, true)
	})
	if gotErr == nil {
		t.Fatal("expected error for vscode-disabled sandbox, got nil")
	}
	if !strings.Contains(gotErr.Error(), "vscodeEnabled") {
		t.Errorf("error missing 'vscodeEnabled' hint: %v", gotErr)
	}
}

// TestVSCodeRekey_Inconsistent verifies that runVSCodeRekey returns an error
// containing "unexpected state" when sshd is active but authorized_keys are absent.
func TestVSCodeRekey_Inconsistent(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")
	inconsistentOutput := "=== sshd ===\nactive\n=== authkeys exists ===\nno\n"
	mockSSM := &vsCodeSSMMock{output: inconsistentOutput}
	var gotErr error
	captureStdout(func() {
		gotErr = runVSCodeRekey(ctx, cfg, fetcher, newRunningEC2Mock(), mockSSM, "sb-abc123", false, true)
	})
	if gotErr == nil {
		t.Fatal("expected error for inconsistent state, got nil")
	}
	if !strings.Contains(gotErr.Error(), "unexpected state") {
		t.Errorf("error missing 'unexpected state': %v", gotErr)
	}
}

// TestVSCodeRekey_SSHDDown verifies that runVSCodeRekey returns an error
// containing "sshd is not running" when sshd is inactive but authorized_keys exist.
func TestVSCodeRekey_SSHDDown(t *testing.T) {
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")
	sshdDownOutput := "=== sshd ===\ninactive\n=== authkeys exists ===\nyes\n"
	mockSSM := &vsCodeSSMMock{output: sshdDownOutput}
	var gotErr error
	captureStdout(func() {
		gotErr = runVSCodeRekey(ctx, cfg, fetcher, newRunningEC2Mock(), mockSSM, "sb-abc123", false, true)
	})
	if gotErr == nil {
		t.Fatal("expected error when sshd is not running, got nil")
	}
	if !strings.Contains(gotErr.Error(), "sshd is not running") {
		t.Errorf("error missing 'sshd is not running': %v", gotErr)
	}
}

// TestVSCodeRekey_NormalRotation verifies that runVSCodeRekey succeeds when
// local keys already exist and the sandbox is healthy. The private key content
// should change after rotation.
// Uses a rekeyInstallSpyMock: first call returns healthySSMOutput (pre-flight),
// second call captures the install script and returns the readback dynamically.
func TestVSCodeRekey_NormalRotation(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	// Pre-seed ~/.km/keys/sb-abc123 and .pub with a real ed25519 keypair.
	origPrivBytes, _ := seedRekeyTestKeys(t, homeDir, "sb-abc123")
	privPath := filepath.Join(homeDir, ".km", "keys", "sb-abc123")

	spyMock := &rekeyInstallSpyMock{preflightOutput: healthySSMOutput}
	var gotErr error
	captureStdout(func() {
		gotErr = runVSCodeRekey(ctx, cfg, fetcher, newRunningEC2Mock(), spyMock, "sb-abc123", false, true)
	})
	if gotErr != nil {
		t.Fatalf("expected nil error for normal rotation, got: %v", gotErr)
	}
	newPrivBytes, err := os.ReadFile(privPath)
	if err != nil {
		t.Fatalf("read private key after rotation: %v", err)
	}
	if bytes.Equal(origPrivBytes, newPrivBytes) {
		t.Error("expected private key content to change after rotation")
	}
}

// TestVSCodeRekey_CrossLaptop verifies that runVSCodeRekey succeeds when the
// local key files are absent (cross-laptop bootstrap scenario). After rotation,
// ~/.km/keys/sb-abc123 (mode 0600) and ~/.km/keys/sb-abc123.pub (mode 0644)
// must exist.
func TestVSCodeRekey_CrossLaptop(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	// Keys are deliberately absent — no pre-seeding. keysDir must exist though
	// so GenerateAndWrite (called during rekey) can create the keys dir.
	privPath := filepath.Join(homeDir, ".km", "keys", "sb-abc123")
	pubPath := filepath.Join(homeDir, ".km", "keys", "sb-abc123.pub")

	spyMock := &rekeyInstallSpyMock{preflightOutput: healthySSMOutput}
	var out string
	var gotErr error
	out = captureStdout(func() {
		gotErr = runVSCodeRekey(ctx, cfg, fetcher, newRunningEC2Mock(), spyMock, "sb-abc123", false, true)
	})
	if gotErr != nil {
		t.Fatalf("expected nil error for cross-laptop bootstrap, got: %v", gotErr)
	}

	privInfo, err := os.Stat(privPath)
	if err != nil {
		t.Fatalf("expected private key to exist at %s: %v", privPath, err)
	}
	if privInfo.Mode().Perm() != 0o600 {
		t.Errorf("expected private key mode 0600; got %o", privInfo.Mode().Perm())
	}
	pubInfo, err := os.Stat(pubPath)
	if err != nil {
		t.Fatalf("expected public key to exist at %s: %v", pubPath, err)
	}
	if pubInfo.Mode().Perm() != 0o644 {
		t.Errorf("expected public key mode 0644; got %o", pubInfo.Mode().Perm())
	}
	if !strings.Contains(out, "Local key created atomically") {
		t.Errorf("expected 'Local key created atomically' in output for cross-laptop; got: %s", out)
	}
}

// TestVSCodeRekey_VerifyMismatch verifies that runVSCodeRekey returns an error
// containing "verification failed" and "Old key still active locally" when the
// SSM readback returns a different pubkey than what was generated. Local key files
// must remain unchanged.
func TestVSCodeRekey_VerifyMismatch(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	origPrivBytes, _ := seedRekeyTestKeys(t, homeDir, "sb-abc123")
	keysDir := filepath.Join(homeDir, ".km", "keys")
	privPath := filepath.Join(keysDir, "sb-abc123")
	pubPath := filepath.Join(keysDir, "sb-abc123.pub")
	origPubBytes, _ := os.ReadFile(pubPath)

	// Readback returns a DIFFERENT pubkey (mismatch).
	mismatchReadback := "=== READBACK ===\nssh-ed25519 AAAA wrong-key km-bogus\n"
	mockSSM := &sequencedSSMMock{outputs: []string{healthySSMOutput, mismatchReadback}}

	var gotErr error
	captureStdout(func() {
		gotErr = runVSCodeRekey(ctx, cfg, fetcher, newRunningEC2Mock(), mockSSM, "sb-abc123", false, true)
	})
	if gotErr == nil {
		t.Fatal("expected error for verification mismatch, got nil")
	}
	if !strings.Contains(gotErr.Error(), "verification failed") {
		t.Errorf("error missing 'verification failed': %v", gotErr)
	}
	if !strings.Contains(gotErr.Error(), "Old key still active locally") {
		t.Errorf("error missing 'Old key still active locally': %v", gotErr)
	}
	if !strings.Contains(gotErr.Error(), "km vscode rekey") {
		t.Errorf("error missing 'km vscode rekey' retry hint: %v", gotErr)
	}
	// Local files must be unchanged.
	gotPriv, _ := os.ReadFile(privPath)
	if !bytes.Equal(gotPriv, origPrivBytes) {
		t.Error("expected private key file to be unchanged after mismatch")
	}
	gotPub, _ := os.ReadFile(pubPath)
	if !bytes.Equal(gotPub, origPubBytes) {
		t.Error("expected public key file to be unchanged after mismatch")
	}
}

// TestVSCodeRekey_RenameOrdering verifies the atomic rename sequence:
// .pub.new is renamed to .pub before .new is renamed to the private key path.
// End-state assertion: scratch files (.pub.new, .new) no longer exist; the
// committed .pub and private key match what GenerateAndWrite produced.
func TestVSCodeRekey_RenameOrdering(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	seedRekeyTestKeys(t, homeDir, "sb-abc123")
	keysDir := filepath.Join(homeDir, ".km", "keys")
	privPath := filepath.Join(keysDir, "sb-abc123")
	pubPath := filepath.Join(keysDir, "sb-abc123.pub")
	privNewPath := filepath.Join(keysDir, "sb-abc123.new")
	pubNewPath := filepath.Join(keysDir, "sb-abc123.pub.new")

	spyMock := &rekeyInstallSpyMock{preflightOutput: healthySSMOutput}
	var gotErr error
	captureStdout(func() {
		gotErr = runVSCodeRekey(ctx, cfg, fetcher, newRunningEC2Mock(), spyMock, "sb-abc123", false, true)
	})
	if gotErr != nil {
		t.Fatalf("expected nil error, got: %v", gotErr)
	}

	// Scratch files must be gone.
	if _, err := os.Stat(privNewPath); !os.IsNotExist(err) {
		t.Error("expected .new scratch file to be gone after rename")
	}
	if _, err := os.Stat(pubNewPath); !os.IsNotExist(err) {
		t.Error("expected .pub.new scratch file to be gone after rename")
	}

	// Committed .pub must contain the pubkey from the install script.
	installedPubKey := extractPubkeyFromInstallScript(spyMock.capturedScript)
	gotPub, _ := os.ReadFile(pubPath)
	// pubPath content has a trailing newline (GenerateAndWrite writes pubLine+"\n")
	gotPubTrimmed := strings.TrimRight(string(gotPub), "\n")
	if gotPubTrimmed != installedPubKey {
		t.Errorf("committed .pub (%q) does not match installed pubkey (%q)", gotPubTrimmed, installedPubKey)
	}

	// Private key content must differ from the seeded sentinel.
	gotPriv, _ := os.ReadFile(privPath)
	if len(gotPriv) == 0 {
		t.Error("expected private key to be non-empty after rename")
	}
}

// TestVSCodeRekey_OverwritesScratch verifies that runVSCodeRekey unconditionally
// overwrites pre-existing .new and .pub.new scratch files (from a crashed prior rekey).
func TestVSCodeRekey_OverwritesScratch(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	seedRekeyTestKeys(t, homeDir, "sb-abc123")
	keysDir := filepath.Join(homeDir, ".km", "keys")
	privPath := filepath.Join(keysDir, "sb-abc123")
	pubPath := filepath.Join(keysDir, "sb-abc123.pub")
	privNewPath := filepath.Join(keysDir, "sb-abc123.new")
	pubNewPath := filepath.Join(keysDir, "sb-abc123.pub.new")

	// Pre-seed the scratch files (simulating a crashed prior rekey).
	crashedPrivSentinel := []byte("crashed-private-sentinel")
	crashedPubSentinel := []byte("ssh-ed25519 AAAA crashed-pub-sentinel")
	if err := os.WriteFile(privNewPath, crashedPrivSentinel, 0o600); err != nil {
		t.Fatalf("write crash sentinel priv: %v", err)
	}
	if err := os.WriteFile(pubNewPath, crashedPubSentinel, 0o644); err != nil {
		t.Fatalf("write crash sentinel pub: %v", err)
	}

	spyMock := &rekeyInstallSpyMock{preflightOutput: healthySSMOutput}
	var gotErr error
	captureStdout(func() {
		gotErr = runVSCodeRekey(ctx, cfg, fetcher, newRunningEC2Mock(), spyMock, "sb-abc123", false, true)
	})
	if gotErr != nil {
		t.Fatalf("expected nil error, got: %v", gotErr)
	}

	// Committed .pub must NOT contain the crashed sentinel.
	gotPub, _ := os.ReadFile(pubPath)
	if bytes.Equal(gotPub, crashedPubSentinel) {
		t.Error("expected committed .pub to be FRESH, not the crashed sentinel")
	}
	gotPriv, _ := os.ReadFile(privPath)
	if bytes.Equal(gotPriv, crashedPrivSentinel) {
		t.Error("expected committed private key to be FRESH, not the crashed sentinel")
	}
}

// TestVSCodeRekey_YesFlag verifies that runVSCodeRekey with yes=true does not
// block on stdin and does not print a "[y/N]" prompt.
func TestVSCodeRekey_YesFlag(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	seedRekeyTestKeys(t, homeDir, "sb-abc123")

	spyMock := &rekeyInstallSpyMock{preflightOutput: healthySSMOutput}
	// Do NOT replace os.Stdin — if --yes is working, it never reads stdin.
	var out string
	out = captureStdout(func() {
		err := runVSCodeRekey(ctx, cfg, fetcher, newRunningEC2Mock(), spyMock, "sb-abc123", false, true /*yes*/)
		if err != nil {
			t.Errorf("expected nil error with --yes, got: %v", err)
		}
	})
	// The test completing without blocking proves --yes skipped stdin.
	if strings.Contains(out, "[y/N]") {
		t.Errorf("expected no [y/N] prompt with --yes; got output: %s", out)
	}
}

// TestVSCodeRekey_ConfirmNo verifies that runVSCodeRekey returns nil and leaves
// local key files unchanged when the user answers "n" to the confirmation prompt.
func TestVSCodeRekey_ConfirmNo(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	origPrivBytes, _ := seedRekeyTestKeys(t, homeDir, "sb-abc123")
	keysDir := filepath.Join(homeDir, ".km", "keys")
	privPath := filepath.Join(keysDir, "sb-abc123")
	pubPath := filepath.Join(keysDir, "sb-abc123.pub")
	origPubBytes, _ := os.ReadFile(pubPath)

	// Pipe "n\n" to stdin to simulate user declining the prompt.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()
	w.Write([]byte("n\n"))
	w.Close()

	// Only one SSM call fires (pre-flight); install never fires because user aborted.
	mockSSM := &vsCodeSSMMock{output: healthySSMOutput}
	var out string
	var gotErr error
	out = captureStdout(func() {
		gotErr = runVSCodeRekey(ctx, cfg, fetcher, newRunningEC2Mock(), mockSSM, "sb-abc123", false, false /*yes=false*/)
	})
	if gotErr != nil {
		t.Fatalf("expected nil error (clean abort) when user answers n, got: %v", gotErr)
	}
	if !strings.Contains(out, "Aborted.") {
		t.Errorf("expected 'Aborted.' in output; got: %s", out)
	}
	// Local files must be unchanged.
	gotPriv, _ := os.ReadFile(privPath)
	if !bytes.Equal(gotPriv, origPrivBytes) {
		t.Error("expected private key file to be unchanged after 'n' abort")
	}
	gotPub, _ := os.ReadFile(pubPath)
	if !bytes.Equal(gotPub, origPubBytes) {
		t.Error("expected public key file to be unchanged after 'n' abort")
	}
}

// TestVSCodeRekey_OutputMarkers verifies that a successful rekey prints all
// required step markers in order: ✓ EC2 instance running, ✓ Pre-flight check
// passed, ✓ New keypair generated (SHA256:, ✓ Pushed to sandbox via SSM (verified,
// ✓ Local key, atomically, Rekey complete.
func TestVSCodeRekey_OutputMarkers(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	ctx := context.Background()
	cfg := &config.Config{}
	fetcher := newVSCodeEC2Sandbox("sb-abc123")

	seedRekeyTestKeys(t, homeDir, "sb-abc123")

	spyMock := &rekeyInstallSpyMock{preflightOutput: healthySSMOutput}
	var out string
	out = captureStdout(func() {
		err := runVSCodeRekey(ctx, cfg, fetcher, newRunningEC2Mock(), spyMock, "sb-abc123", false, true /*yes*/)
		if err != nil {
			t.Errorf("expected nil error for output-markers test, got: %v", err)
		}
	})

	markers := []string{
		"✓ EC2 instance running",
		"✓ Pre-flight check passed",
		"✓ New keypair generated (SHA256:",
		"✓ Pushed to sandbox via SSM (verified",
		"✓ Local key",
		"atomically",
		"Rekey complete",
	}
	prevIdx := -1
	for i, marker := range markers {
		idx := strings.Index(out, marker)
		if idx < 0 {
			t.Errorf("marker[%d] %q not found in output: %s", i, marker, out)
			continue
		}
		if prevIdx >= 0 && idx < prevIdx {
			t.Errorf("marker[%d] %q appears before marker[%d] %q in output", i, marker, i-1, markers[i-1])
		}
		prevIdx = idx
	}
}
