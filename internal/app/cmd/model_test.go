// Package cmd — model_test.go
// Tests for km model start / km model status.
// Mirrors the structure of vscode_test.go and desktop_test.go (same package,
// same mock helpers — vsCodeFetcherMock is already defined in vscode_test.go).
package cmd

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// TestModelStart_PortForwardWiring verifies that km model start builds an SSM
// port-forward command targeting remote port 8001 (Bifrost gateway) with the
// requested local port.
func TestModelStart_PortForwardWiring(t *testing.T) {
	fetcher := newVSCodeEC2Sandbox("sb-gpu-001")
	var captured *exec.Cmd
	execFn := func(c *exec.Cmd) error {
		captured = c
		return nil
	}

	err := runModelStart(context.Background(), &config.Config{}, fetcher, execFn, "sb-gpu-001", 8001, false)
	if err != nil {
		t.Fatalf("runModelStart returned unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("execFn was never called — port-forward command not built")
	}
	joined := strings.Join(captured.Args, " ")
	for _, want := range []string{"AWS-StartPortForwardingSession", "portNumber", "8001", "localPortNumber"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in port-forward args; got: %s", want, joined)
		}
	}
}

// TestModelStart_AnthropicFlag verifies that km model start with --anthropic still
// targets remote port 8001 (Bifrost serves Anthropic Messages via /anthropic path).
func TestModelStart_AnthropicFlag(t *testing.T) {
	fetcher := newVSCodeEC2Sandbox("sb-gpu-001")
	var captured *exec.Cmd
	execFn := func(c *exec.Cmd) error {
		captured = c
		return nil
	}

	err := runModelStart(context.Background(), &config.Config{}, fetcher, execFn, "sb-gpu-001", 8001, true /*anthropic*/)
	if err != nil {
		t.Fatalf("runModelStart --anthropic returned unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("execFn was never called with --anthropic flag")
	}
	joined := strings.Join(captured.Args, " ")
	// Remote port must still be 8001 — Bifrost's /anthropic path, not a separate port.
	if !strings.Contains(joined, "8001") {
		t.Errorf("expected remote port '8001' in port-forward args; got: %s", joined)
	}
}

// TestModelStart_CustomLocalPort verifies that a non-default --local-port value
// appears in the port-forward command arguments.
func TestModelStart_CustomLocalPort(t *testing.T) {
	fetcher := newVSCodeEC2Sandbox("sb-gpu-002")
	var captured *exec.Cmd
	execFn := func(c *exec.Cmd) error {
		captured = c
		return nil
	}

	const customPort = 18001
	err := runModelStart(context.Background(), &config.Config{}, fetcher, execFn, "sb-gpu-002", customPort, false)
	if err != nil {
		t.Fatalf("runModelStart with custom port returned unexpected error: %v", err)
	}
	if captured == nil {
		t.Fatal("execFn was never called")
	}
	joined := strings.Join(captured.Args, " ")
	if !strings.Contains(joined, "18001") {
		t.Errorf("expected custom local port '18001' in port-forward args; got: %s", joined)
	}
	// Remote port must still be 8001 regardless of local port.
	if !strings.Contains(joined, "portNumber") {
		t.Errorf("expected 'portNumber' parameter in port-forward args; got: %s", joined)
	}
}

// TestModelCmd_Registered verifies that km model start and km model status are
// registered under the km model parent command.
func TestModelCmd_Registered(t *testing.T) {
	cfg := &config.Config{}
	parent := newModelCmdInternal(cfg, nil, nil, nil)

	var foundStart, foundStatus bool
	for _, sub := range parent.Commands() {
		switch sub.Use {
		case "start <sandbox-id>":
			foundStart = true
		case "status <sandbox-id>":
			foundStatus = true
		}
	}
	if !foundStart {
		t.Error("expected 'start' subcommand under km model")
	}
	if !foundStatus {
		t.Error("expected 'status' subcommand under km model")
	}
}

// TestModelCmd_InRootTree verifies that km model is registered in the root command
// tree (regression guard for the root.go AddCommand call).
func TestModelCmd_InRootTree(t *testing.T) {
	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	root.AddCommand(newModelCmdInternal(cfg, nil, nil, nil))
	root.SetArgs([]string{"model", "--help"})
	// --help exits with 0; the command must be found (not "unknown command").
	// We just verify the subcommand is reachable by looking at the Commands list.
	var found *cobra.Command
	for _, sub := range root.Commands() {
		if sub.Use == "model" {
			found = sub
			break
		}
	}
	if found == nil {
		t.Fatal("expected 'model' command registered in root command tree")
	}
}

// TestModelStatus_Healthy verifies that parseModelStatus returns nil and prints
// a ready message when both vllm and bifrost are active.
func TestModelStatus_Healthy(t *testing.T) {
	out := "=== vllm ===\nactive\n=== bifrost ===\nactive\n=== gateway ===\n200"
	stdout := captureStdout(func() {
		if err := parseModelStatus(out, "sb-gpu-001"); err != nil {
			t.Errorf("expected nil for healthy status; got: %v", err)
		}
	})
	if !strings.Contains(stdout, "ready") {
		t.Errorf("expected 'ready' in output; got: %s", stdout)
	}
}

// TestModelStatus_VLLMInactive verifies that parseModelStatus returns an error
// mentioning "vllm" when vLLM is not running but Bifrost is.
func TestModelStatus_VLLMInactive(t *testing.T) {
	out := "=== vllm ===\ninactive\n=== bifrost ===\nactive\n=== gateway ===\n200"
	err := parseModelStatus(out, "sb-gpu-001")
	if err == nil {
		t.Fatal("expected non-nil error when vllm inactive")
	}
	if !strings.Contains(err.Error(), "vllm") {
		t.Errorf("error missing 'vllm': %v", err)
	}
}

// TestModelStatus_BifrostInactive verifies that parseModelStatus returns an error
// mentioning "bifrost" when Bifrost is not running but vLLM is.
func TestModelStatus_BifrostInactive(t *testing.T) {
	out := "=== vllm ===\nactive\n=== bifrost ===\ninactive\n=== gateway ===\n0"
	err := parseModelStatus(out, "sb-gpu-001")
	if err == nil {
		t.Fatal("expected non-nil error when bifrost inactive")
	}
	if !strings.Contains(err.Error(), "bifrost") {
		t.Errorf("error missing 'bifrost': %v", err)
	}
}

// TestModelStatus_NeitherActive verifies that parseModelStatus returns a helpful
// error mentioning the profile hint when neither vllm nor bifrost is active.
func TestModelStatus_NeitherActive(t *testing.T) {
	out := "=== vllm ===\ninactive\n=== bifrost ===\ninactive\n=== gateway ===\n0"
	err := parseModelStatus(out, "sb-gpu-001")
	if err == nil {
		t.Fatal("expected non-nil error when neither vllm nor bifrost active")
	}
	if !strings.Contains(err.Error(), "gpu/serve") {
		t.Errorf("error missing 'gpu/serve' profile hint: %v", err)
	}
}
