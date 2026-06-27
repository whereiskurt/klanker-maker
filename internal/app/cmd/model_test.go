// Package cmd — model_test.go
// Test scaffold for km model start / km model status.
// Mirrors the structure of vscode_test.go and desktop_test.go (same package,
// same mock helpers — vsCodeFetcherMock is already defined in vscode_test.go).
//
// PLAN 04 INSTRUCTIONS: un-skip TestModelStart_PortForwardWiring once model.go
// exists with runModelStart(ctx, cfg, fetcher, execFn, sandboxID, localPort int) error.
// Replace the t.Skip body with the full assertion block documented below.
// The mock types (vsCodeFetcherMock) live in vscode_test.go and are already
// accessible within this same package test.
//
// EXACT ASSERTIONS TO FILL IN (Plan 04):
//
//  1. Build a fake fetcher via newVSCodeEC2Sandbox("sb-gpu-001") — returns a
//     SandboxRecord with Resources = ["arn:aws:ec2:us-east-1:123456789012:instance/i-deadbeef"].
//
//  2. Inject a mock execFn capturing the *exec.Cmd without executing it (returns nil).
//
//  3. Call: runModelStart(context.Background(), &config.Config{}, fetcher, execFn, "sb-gpu-001", 8001)
//
//  4. Assert:
//     - captured != nil (execFn was called)
//     - strings.Contains(joined, "AWS-StartPortForwardingSession")
//     - strings.Contains(joined, "portNumber")   with value "8001" (Bifrost remote port)
//     - strings.Contains(joined, "localPortNumber")
//     - strings.Contains(joined, "8001") (default local port matches remote)
//
//  5. For --anthropic variant (future TestModelStart_AnthropicFlag):
//     - Call runModelStart with localPort=8001 and the --anthropic flag truthy.
//     - Assert remote port is 8001 (shim port, same as Bifrost gateway port).
//
// The DI seams are:
//   - fetcher: SandboxFetcher (defined in status.go, same package)
//   - execFn:  ShellExecFunc  (defined in shell.go, same package)
//   - These are the identical seams used by runVSCodeStart / runDesktopStart.
package cmd

import (
	"testing"
)

// TestModelStart_PortForwardWiring verifies that km model start builds an SSM
// port-forward command targeting remote port 8001 (Bifrost gateway) with the
// requested local port.
//
// Phase 122 Wave 0 scaffold: t.Skip until Plan 04 adds model.go.
// Plan 04 must:
//   1. Create internal/app/cmd/model.go with runModelStart + newModelStartCmd.
//   2. Un-skip this test and fill in the assertion block (see file-level comment).
//   3. Verify the test is GREEN.
func TestModelStart_PortForwardWiring(t *testing.T) {
	t.Skip("Plan 04: km model start not yet implemented — see file-level comment for exact assertions")

	// PLACEHOLDER — Plan 04 replaces the body below with the real assertions.
	//
	// fetcher := newVSCodeEC2Sandbox("sb-gpu-001")
	// var captured *exec.Cmd
	// execFn := func(c *exec.Cmd) error { captured = c; return nil }
	//
	// err := runModelStart(context.Background(), &config.Config{}, fetcher, execFn, "sb-gpu-001", 8001)
	// if err != nil {
	//     t.Fatalf("runModelStart returned unexpected error: %v", err)
	// }
	// if captured == nil {
	//     t.Fatal("execFn was never called — port-forward command not built")
	// }
	// joined := strings.Join(captured.Args, " ")
	// for _, want := range []string{"AWS-StartPortForwardingSession", "portNumber", "8001", "localPortNumber"} {
	//     if !strings.Contains(joined, want) {
	//         t.Errorf("expected %q in port-forward args; got: %s", want, joined)
	//     }
	// }
}
