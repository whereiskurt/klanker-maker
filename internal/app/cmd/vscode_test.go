// Package cmd — vscode_test.go
// Failing-stub tests for km vscode start / km vscode status.
// Wave 3 Plan 73-06 removes the t.Skip calls and implements the commands.
package cmd

import (
	"testing"
)

// TestVSCodeStart_MissingPrivateKey verifies that vscode start returns a
// helpful error mentioning "private key" and "different machine" when the
// local private key file is absent.
func TestVSCodeStart_MissingPrivateKey(t *testing.T) {
	t.Skip("TODO Wave 3 (Plan 73-06): implement km vscode start/status")
	// dir := t.TempDir()
	// cfg := &config.Config{KMDir: dir}
	// fetcher := &mockVSCodeFetcher{
	// 	sandbox: &kmaws.SandboxRecord{
	// 		SandboxID:  "sb-abc123",
	// 		InstanceID: "i-0abc123",
	// 	},
	// }
	// // No private key file exists in dir/keys/sb-abc123.
	// var execCmds []*exec.Cmd
	// execFn := func(c *exec.Cmd) error { execCmds = append(execCmds, c); return nil }
	// cmd := newVSCodeCmdInternal(cfg, fetcher, execFn, nil)
	// cmd.SetArgs([]string{"start", "sb-abc123"})
	// err := cmd.Execute()
	// if err == nil {
	// 	t.Fatal("expected error for missing private key, got nil")
	// }
	// if !strings.Contains(err.Error(), "private key") {
	// 	t.Errorf("error missing 'private key': %v", err)
	// }
	// if !strings.Contains(err.Error(), "different machine") {
	// 	t.Errorf("error missing 'different machine' hint: %v", err)
	// }
}

// TestVSCodeStart_BuildsPortForwardArgs verifies that vscode start invokes the
// AWS CLI session-manager with the correct port-forwarding arguments.
func TestVSCodeStart_BuildsPortForwardArgs(t *testing.T) {
	t.Skip("TODO Wave 3 (Plan 73-06): implement km vscode start/status")
	// dir := t.TempDir()
	// // Write a fake private key so the "missing key" check passes.
	// keyDir := filepath.Join(dir, "keys")
	// _ = os.MkdirAll(keyDir, 0700)
	// _ = os.WriteFile(filepath.Join(keyDir, "sb-abc123"), []byte("fake-priv-key"), 0600)
	//
	// cfg := &config.Config{KMDir: dir}
	// fetcher := &mockVSCodeFetcher{
	// 	sandbox: &kmaws.SandboxRecord{
	// 		SandboxID:  "sb-abc123",
	// 		InstanceID: "i-0abc123",
	// 		Region:     "us-east-1",
	// 	},
	// }
	// var capturedCmd *exec.Cmd
	// execFn := func(c *exec.Cmd) error { capturedCmd = c; return nil }
	// cmd := newVSCodeCmdInternal(cfg, fetcher, execFn, nil)
	// cmd.SetArgs([]string{"start", "sb-abc123"})
	// _ = cmd.Execute()
	//
	// if capturedCmd == nil {
	// 	t.Fatal("execFn was not called")
	// }
	// args := strings.Join(capturedCmd.Args, " ")
	// if !strings.Contains(args, "ssm") {
	// 	t.Errorf("expected 'ssm' in args; got: %s", args)
	// }
	// if !strings.Contains(args, "start-session") {
	// 	t.Errorf("expected 'start-session' in args; got: %s", args)
	// }
	// if !strings.Contains(args, "AWS-StartPortForwardingSession") {
	// 	t.Errorf("expected 'AWS-StartPortForwardingSession' document; got: %s", args)
	// }
	// if !strings.Contains(args, "localPortNumber=2222") {
	// 	t.Errorf("expected 'localPortNumber=2222' in args; got: %s", args)
	// }
	// if !strings.Contains(args, "portNumber=22") {
	// 	t.Errorf("expected 'portNumber=22' in args; got: %s", args)
	// }
}

// TestVSCodeStart_OutputContainsHostAlias verifies that vscode start prints
// helpful user-facing instructions including the host alias, VS Code hints, etc.
func TestVSCodeStart_OutputContainsHostAlias(t *testing.T) {
	t.Skip("TODO Wave 3 (Plan 73-06): implement km vscode start/status")
	// dir := t.TempDir()
	// keyDir := filepath.Join(dir, "keys")
	// _ = os.MkdirAll(keyDir, 0700)
	// _ = os.WriteFile(filepath.Join(keyDir, "sb-abc123"), []byte("fake-priv-key"), 0600)
	//
	// cfg := &config.Config{KMDir: dir}
	// fetcher := &mockVSCodeFetcher{
	// 	sandbox: &kmaws.SandboxRecord{
	// 		SandboxID:  "sb-abc123",
	// 		InstanceID: "i-0abc123",
	// 		Region:     "us-east-1",
	// 	},
	// }
	// execFn := func(c *exec.Cmd) error { return nil }
	// var buf bytes.Buffer
	// cobraCmd := newVSCodeCmdInternal(cfg, fetcher, execFn, nil)
	// cobraCmd.SetOut(&buf)
	// cobraCmd.SetArgs([]string{"start", "sb-abc123"})
	// _ = cobraCmd.Execute()
	//
	// out := buf.String()
	// if !strings.Contains(out, "Host: km-sb-abc123") {
	// 	t.Errorf("output missing 'Host: km-sb-abc123'; got: %s", out)
	// }
	// if !strings.Contains(out, "F1") {
	// 	t.Errorf("output missing 'F1' VS Code hint; got: %s", out)
	// }
	// if !strings.Contains(out, "Remote-SSH") {
	// 	t.Errorf("output missing 'Remote-SSH'; got: %s", out)
	// }
	// if !strings.Contains(out, "Ctrl-C") {
	// 	t.Errorf("output missing 'Ctrl-C' exit hint; got: %s", out)
	// }
}

// TestVSCodeStatus_SSHDInactive verifies that vscode status returns a non-nil
// error when sshd reports "inactive" but authorized_keys exist (implies sshd crashed).
func TestVSCodeStatus_SSHDInactive(t *testing.T) {
	t.Skip("TODO Wave 3 (Plan 73-06): implement km vscode start/status")
	// mockSSM := &mockVSCodeSSM{
	// 	output: "=== sshd ===\ninactive\n=== authkeys exists ===\nyes\n",
	// }
	// cfg := &config.Config{}
	// fetcher := &mockVSCodeFetcher{
	// 	sandbox: &kmaws.SandboxRecord{SandboxID: "sb-abc123", InstanceID: "i-0abc123"},
	// }
	// cmd := newVSCodeCmdInternal(cfg, fetcher, nil, mockSSM)
	// cmd.SetArgs([]string{"status", "sb-abc123"})
	// err := cmd.Execute()
	// if err == nil {
	// 	t.Fatal("expected non-nil error for inactive sshd")
	// }
}

// TestVSCodeStatus_PrePhase73 verifies that vscode status returns an error
// containing a "vscodeEnabled" hint when sshd is inactive AND authorized_keys
// are absent (sandbox predates Phase 73).
func TestVSCodeStatus_PrePhase73(t *testing.T) {
	t.Skip("TODO Wave 3 (Plan 73-06): implement km vscode start/status")
	// mockSSM := &mockVSCodeSSM{
	// 	output: "=== sshd ===\ninactive\n=== authkeys exists ===\nno\n",
	// }
	// cfg := &config.Config{}
	// fetcher := &mockVSCodeFetcher{
	// 	sandbox: &kmaws.SandboxRecord{SandboxID: "sb-abc123", InstanceID: "i-0abc123"},
	// }
	// cmd := newVSCodeCmdInternal(cfg, fetcher, nil, mockSSM)
	// cmd.SetArgs([]string{"status", "sb-abc123"})
	// err := cmd.Execute()
	// if err == nil {
	// 	t.Fatal("expected non-nil error for pre-Phase-73 sandbox")
	// }
	// if !strings.Contains(err.Error(), "vscodeEnabled") {
	// 	t.Errorf("error missing 'vscodeEnabled' hint; got: %v", err)
	// }
}

// TestVSCodeStatus_Healthy verifies that vscode status returns nil and prints
// an OK message when sshd is active and authorized_keys are present.
func TestVSCodeStatus_Healthy(t *testing.T) {
	t.Skip("TODO Wave 3 (Plan 73-06): implement km vscode start/status")
	// mockSSM := &mockVSCodeSSM{
	// 	output: "=== sshd ===\nactive\n=== authkeys exists ===\nyes\n=== authkeys content ===\nssh-ed25519 AAAA... km-sb-abc123\n",
	// }
	// cfg := &config.Config{}
	// fetcher := &mockVSCodeFetcher{
	// 	sandbox: &kmaws.SandboxRecord{SandboxID: "sb-abc123", InstanceID: "i-0abc123"},
	// }
	// var buf bytes.Buffer
	// cobraCmd := newVSCodeCmdInternal(cfg, fetcher, nil, mockSSM)
	// cobraCmd.SetOut(&buf)
	// cobraCmd.SetArgs([]string{"status", "sb-abc123"})
	// if err := cobraCmd.Execute(); err != nil {
	// 	t.Fatalf("expected nil error for healthy status; got: %v", err)
	// }
	// if !strings.Contains(buf.String(), "OK") {
	// 	t.Errorf("expected 'OK' in output; got: %s", buf.String())
	// }
}
