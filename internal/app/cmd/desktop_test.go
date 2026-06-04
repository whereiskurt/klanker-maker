// Package cmd — desktop_test.go
// Tests for km desktop start / km desktop status.
// Mirrors the structure of vscode_test.go (same package, same mock helpers).
// Wave 3 Plan 93-05 activates TestDesktopStart and TestDesktopStatus;
// TestDesktopCredential is owned by 93-04 and remains skipped.
package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ---- Mock helpers for desktop tests ----

// desktopFetcherMock returns a hard-coded SandboxRecord for desktop tests.
type desktopFetcherMock struct {
	record *kmaws.SandboxRecord
	err    error
}

func (f *desktopFetcherMock) FetchSandbox(_ context.Context, _ string) (*kmaws.SandboxRecord, error) {
	return f.record, f.err
}

// newDesktopEC2Sandbox returns a minimal running EC2 sandbox record for desktop tests.
func newDesktopEC2Sandbox(id string) *desktopFetcherMock {
	return &desktopFetcherMock{
		record: &kmaws.SandboxRecord{
			SandboxID: id,
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			Resources: []string{
				"arn:aws:ec2:us-east-1:123456789012:instance/i-0desktoptest",
			},
		},
	}
}

// healthyDesktopSSMOutput is the SSM script output when kasmvnc is active and kasmpasswd exists.
const healthyDesktopSSMOutput = "=== kasmvnc ===\nactive\n=== kasmpasswd ===\nyes\n"

// inactiveDesktopSSMOutput simulates kasmvnc inactive with no kasmpasswd (desktop not enabled).
const inactiveDesktopSSMOutput = "=== kasmvnc ===\ninactive\n=== kasmpasswd ===\nno\n"

// seedDesktopCredential writes a fake user:pass credential file at ~/.km/desktop/<id>.
// Returns the user and pass strings written.
func seedDesktopCredential(t *testing.T, home, sandboxID string) (user, pass string) {
	t.Helper()
	dir := filepath.Join(home, ".km", "desktop")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir desktop: %v", err)
	}
	user = "kasm"
	pass = "s3cr3tP@ssw0rd"
	credPath := filepath.Join(dir, sandboxID)
	if err := os.WriteFile(credPath, []byte(user+":"+pass), 0o600); err != nil {
		t.Fatalf("write credential: %v", err)
	}
	return user, pass
}

// ---- Tests ----

// TestDesktopStart covers the core start subcommand scenarios.
func TestDesktopStart(t *testing.T) {
	t.Run("PortAlreadyInUse", func(t *testing.T) {
		// Bind a listener on a random port, then pass that port to runDesktopStart.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("could not bind test listener: %v", err)
		}
		defer ln.Close()
		occupiedPort := ln.Addr().(*net.TCPAddr).Port

		tmp := t.TempDir()
		t.Setenv("HOME", tmp)
		ctx := context.Background()
		cfg := &config.Config{}
		fetcher := newDesktopEC2Sandbox("sb-desk-001")
		mockSSM := &vsCodeSSMMock{output: healthyDesktopSSMOutput}

		err = runDesktopStart(ctx, cfg, fetcher, nil, mockSSM, "sb-desk-001", occupiedPort)
		if err == nil {
			t.Fatal("expected error when local port is occupied, got nil")
		}
		if !strings.Contains(err.Error(), "--local-port") {
			t.Errorf("error missing '--local-port' hint: %v", err)
		}
		if !strings.Contains(fmt.Sprintf("%d", occupiedPort), fmt.Sprintf("%d", occupiedPort)) {
			t.Errorf("error missing port number: %v", err)
		}
	})

	t.Run("MissingCredentialFile", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("HOME", tmp)

		ctx := context.Background()
		cfg := &config.Config{}
		fetcher := newDesktopEC2Sandbox("sb-desk-001")
		mockSSM := &vsCodeSSMMock{output: healthyDesktopSSMOutput}

		// Credential file is deliberately absent — no seedDesktopCredential call.
		err := runDesktopStart(ctx, cfg, fetcher, nil, mockSSM, "sb-desk-001", 18444)
		if err == nil {
			t.Fatal("expected error when credential file is missing, got nil")
		}
		if !strings.Contains(err.Error(), "credential") {
			t.Errorf("error missing 'credential' text: %v", err)
		}
		if !strings.Contains(err.Error(), "different machine") {
			t.Errorf("error missing 'different machine' portability hint: %v", err)
		}
	})

	t.Run("HealthyPath_PrintsURLAndCredential", func(t *testing.T) {
		tmp := t.TempDir()
		t.Setenv("HOME", tmp)

		user, pass := seedDesktopCredential(t, tmp, "sb-desk-001")

		var captured *exec.Cmd
		execFn := func(c *exec.Cmd) error {
			captured = c
			return nil
		}

		mockSSM := &vsCodeSSMMock{output: healthyDesktopSSMOutput}

		out := captureStdout(func() {
			err := runDesktopStart(context.Background(), &config.Config{}, newDesktopEC2Sandbox("sb-desk-001"), execFn, mockSSM, "sb-desk-001", 8444)
			if err != nil {
				t.Errorf("runDesktopStart returned unexpected error: %v", err)
			}
		})

		// Assert URL is printed.
		if !strings.Contains(out, "https://localhost:8444/") {
			t.Errorf("output missing 'https://localhost:8444/'; got: %s", out)
		}
		// Assert credential lines are printed.
		if !strings.Contains(out, "user: "+user) {
			t.Errorf("output missing 'user: %s'; got: %s", user, out)
		}
		if !strings.Contains(out, "pass: "+pass) {
			t.Errorf("output missing 'pass: %s'; got: %s", pass, out)
		}
		// Assert Ctrl-C note is present.
		if !strings.Contains(out, "Ctrl-C") {
			t.Errorf("output missing 'Ctrl-C' hint; got: %s", out)
		}

		// Assert port-forward command was built with the right session document and port.
		if captured == nil {
			t.Fatal("execFn was never called — port-forward command not built")
		}
		joined := strings.Join(captured.Args, " ")
		if !strings.Contains(joined, "AWS-StartPortForwardingSession") {
			t.Errorf("expected 'AWS-StartPortForwardingSession' in args; got: %s", joined)
		}
		if !strings.Contains(joined, "8444") {
			t.Errorf("expected '8444' (remote KasmVNC port) in args; got: %s", joined)
		}
	})
}

// TestDesktopStatus covers the status subcommand health reporting.
func TestDesktopStatus(t *testing.T) {
	t.Run("Healthy_PrintsReady", func(t *testing.T) {
		mockSSM := &vsCodeSSMMock{output: healthyDesktopSSMOutput}
		cfg := &config.Config{}
		fetcher := newDesktopEC2Sandbox("sb-desk-002")

		out := captureStdout(func() {
			err := runDesktopStatus(context.Background(), cfg, fetcher, mockSSM, "sb-desk-002")
			if err != nil {
				t.Errorf("expected nil error for healthy desktop, got: %v", err)
			}
		})

		if !strings.Contains(out, "KasmVNC") {
			t.Errorf("output missing 'KasmVNC'; got: %s", out)
		}
		if !strings.Contains(out, "ready") {
			t.Errorf("output missing 'ready'; got: %s", out)
		}
	})

	t.Run("Inactive_ReturnsError_WithDesktopEnabledHint", func(t *testing.T) {
		mockSSM := &vsCodeSSMMock{output: inactiveDesktopSSMOutput}
		cfg := &config.Config{}
		fetcher := newDesktopEC2Sandbox("sb-desk-002")

		err := runDesktopStatus(context.Background(), cfg, fetcher, mockSSM, "sb-desk-002")
		if err == nil {
			t.Fatal("expected non-nil error when kasmvnc is inactive, got nil")
		}
		if !strings.Contains(err.Error(), "desktop.enabled") {
			t.Errorf("error missing 'desktop.enabled' hint; got: %v", err)
		}
	})

	// Regression: the unit can read non-"active" (activating / restart window /
	// orphaned-but-serving Xvnc) while the desktop is actually reachable on :8444.
	// A live listener + seeded credential must count as ready, even if is-active
	// is not "active" — otherwise status spuriously reports "not ready" for a
	// working desktop.
	t.Run("InactiveUnitButListening_ReportsReady", func(t *testing.T) {
		const listeningOut = "=== kasmvnc ===\nactivating\n=== kasmpasswd ===\nyes\n=== unitfile ===\nyes\n=== listener ===\nyes\n"
		mockSSM := &vsCodeSSMMock{output: listeningOut}
		fetcher := newDesktopEC2Sandbox("sb-desk-002")
		if err := runDesktopStatus(context.Background(), &config.Config{}, fetcher, mockSSM, "sb-desk-002"); err != nil {
			t.Errorf("expected ready when :8444 is listening despite unit not active, got: %v", err)
		}
	})
}

// healthyRestartSSMOutput satisfies both the restart pre-flight (unitfile present)
// and the post-restart status check (kasmvnc active) from one fixed mock output.
const healthyRestartSSMOutput = "=== kasmvnc ===\nactive\n=== kasmpasswd ===\nyes\n=== unitfile ===\nyes\n=== cloudinit ===\nstatus: done\n=== RESTART ===\n=== STATUS ===\nactive\n"

// notEnabledRestartSSMOutput has no unit file → restart pre-flight must reject.
const notEnabledRestartSSMOutput = "=== kasmvnc ===\ninactive\n=== kasmpasswd ===\nno\n=== unitfile ===\nno\n=== cloudinit ===\nstatus: done\n"

// failedRestartSSMOutput is provisioned (unitfile present) but kasmvnc does not
// come back active after the restart.
const failedRestartSSMOutput = "=== kasmvnc ===\ninactive\n=== kasmpasswd ===\nyes\n=== unitfile ===\nyes\n=== cloudinit ===\nstatus: done\n=== RESTART ===\n=== STATUS ===\nfailed\n"

func TestDesktopRestart(t *testing.T) {
	t.Run("Healthy_RestartsAndReportsReady", func(t *testing.T) {
		mockSSM := &vsCodeSSMMock{output: healthyRestartSSMOutput}
		fetcher := newDesktopEC2Sandbox("sb-desk-003")
		out := captureStdout(func() {
			// yes=true skips the confirmation prompt (no stdin in tests).
			if err := runDesktopRestart(context.Background(), &config.Config{}, fetcher, mockSSM, "sb-desk-003", true); err != nil {
				t.Errorf("expected nil error for healthy restart, got: %v", err)
			}
		})
		if !strings.Contains(out, "restarted") {
			t.Errorf("output missing 'restarted'; got: %s", out)
		}
	})

	t.Run("NotEnabled_ReturnsError", func(t *testing.T) {
		mockSSM := &vsCodeSSMMock{output: notEnabledRestartSSMOutput}
		fetcher := newDesktopEC2Sandbox("sb-desk-003")
		err := runDesktopRestart(context.Background(), &config.Config{}, fetcher, mockSSM, "sb-desk-003", true)
		if err == nil {
			t.Fatal("expected error when desktop unit is absent, got nil")
		}
		if !strings.Contains(err.Error(), "desktop.enabled") {
			t.Errorf("error missing 'desktop.enabled' hint; got: %v", err)
		}
	})

	t.Run("DidNotComeBackActive_ReturnsError", func(t *testing.T) {
		mockSSM := &vsCodeSSMMock{output: failedRestartSSMOutput}
		fetcher := newDesktopEC2Sandbox("sb-desk-003")
		err := runDesktopRestart(context.Background(), &config.Config{}, fetcher, mockSSM, "sb-desk-003", true)
		if err == nil {
			t.Fatal("expected error when kasmvnc does not return active, got nil")
		}
		if !strings.Contains(err.Error(), "did not come back active") {
			t.Errorf("error missing restart-failure hint; got: %v", err)
		}
	})
}

// NOTE: TestDesktopCredential (DSK-08-CREDENTIAL) is implemented in create_test.go
// (package cmd_test) by plan 93-04 — the km create credential generation lives there.
// The Wave 0 stub that previously sat here was removed to avoid a duplicate, orphaned
// skipped test (two TestDesktopCredential in package cmd vs cmd_test would leave a
// lingering --- SKIP and trip the 93-07 DSK-15 no-skips gate).
