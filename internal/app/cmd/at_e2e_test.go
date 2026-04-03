//go:build e2e

package cmd_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// e2eState holds shared state across the sequential subtests in TestAtE2E.
type e2eState struct {
	km        string
	sandboxID string
	testID    string
}

// runKM executes the km binary with the given args, capturing stdout and stderr separately.
// Each invocation times out after 30 seconds. Logs full command and output on failure.
func runKM(t *testing.T, km string, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, km, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		t.Logf("runKM command: %s %s", km, strings.Join(args, " "))
		t.Logf("runKM stdout: %s", stdout.String())
		t.Logf("runKM stderr: %s", stderr.String())
	}
	return stdout.String(), stderr.String(), err
}

// extractSandboxIDs parses the output of "km list --wide" and returns the sandbox IDs
// from the first column, skipping header and empty lines.
func extractSandboxIDs(listOutput string) []string {
	var ids []string
	lines := strings.Split(listOutput, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || i == 0 {
			// skip empty lines and header
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			ids = append(ids, fields[0])
		}
	}
	return ids
}

// contains returns true if item is in slice.
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// waitForSandboxState polls "km list --wide" every 15 seconds until the line for sandboxID
// contains wantState, or until timeout expires. Returns true if the state was reached.
func waitForSandboxState(t *testing.T, km string, sandboxID string, wantState string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		stdout, _, _ := runKM(t, km, "list", "--wide")
		lines := strings.Split(stdout, "\n")
		for _, line := range lines {
			if strings.Contains(line, sandboxID) {
				if strings.Contains(line, wantState) {
					return true
				}
			}
		}
		time.Sleep(15 * time.Second)
	}
	return false
}

// waitForSandboxGone polls "km list --wide" every 15 seconds until sandboxID no longer
// appears in the output. Returns true when gone, false on timeout.
func waitForSandboxGone(t *testing.T, km string, sandboxID string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		stdout, _, _ := runKM(t, km, "list", "--wide")
		if !strings.Contains(stdout, sandboxID) {
			return true
		}
		time.Sleep(15 * time.Second)
	}
	return false
}

// TestAtE2E exercises the full km at scheduling lifecycle against real AWS infrastructure.
// It schedules creates, pause/resume/extend/kill operations, and validates schedule management
// (km at list, km at cancel) and recurring (--cron) schedules.
//
// Run with: KM_E2E=1 go test -tags e2e ./internal/app/cmd/ -run TestAtE2E -timeout 15m -v
func TestAtE2E(t *testing.T) {
	if os.Getenv("KM_E2E") == "" {
		t.Skip("set KM_E2E=1 to run E2E tests")
	}

	km := buildKM(t)
	state := &e2eState{
		km:     km,
		testID: fmt.Sprintf("e2e-%s", time.Now().Format("0102-150405")),
	}

	// Safety cleanup: destroy the sandbox we created if the test fails or panics.
	t.Cleanup(func() {
		if state.sandboxID != "" {
			t.Logf("cleanup: destroying sandbox %s", state.sandboxID)
			runKM(t, km, "destroy", state.sandboxID, "--remote", "--yes") //nolint:errcheck
		}
	})

	// Capture the existing sandbox IDs before the test starts so we can diff after.
	preList, _, _ := runKM(t, km, "list", "--wide")
	existingIDs := extractSandboxIDs(preList)

	// --- Step 1: Schedule a one-time create 2 minutes from now ---
	t.Run("01-schedule-create", func(t *testing.T) {
		name := state.testID + "-create"
		stdout, stderr, err := runKM(t, km, "at", "in 2 minutes", "create", "profiles/sealed.yaml", "--remote", "--name", name)
		require.NoError(t, err, "km at create failed: stdout=%s stderr=%s", stdout, stderr)
		require.Contains(t, stdout, "Scheduled:", "expected 'Scheduled:' in output")
		t.Logf("scheduled create: %s", stdout)
	})

	// --- Step 2: Verify the schedule appears in km at list ---
	t.Run("02-at-list-shows-schedule", func(t *testing.T) {
		stdout, _, err := runKM(t, km, "at", "list")
		require.NoError(t, err)
		require.Contains(t, stdout, state.testID+"-create", "schedule name should appear in km at list")
		t.Logf("km at list output:\n%s", stdout)
	})

	// --- Step 3: Wait for the scheduled create to fire (up to 4 minutes) ---
	t.Run("03-wait-sandbox-created", func(t *testing.T) {
		var found string
		deadline := time.Now().Add(4 * time.Minute)
		for time.Now().Before(deadline) {
			stdout, _, _ := runKM(t, km, "list", "--wide")
			newIDs := extractSandboxIDs(stdout)
			for _, id := range newIDs {
				if !contains(existingIDs, id) {
					found = id
					break
				}
			}
			if found != "" {
				break
			}
			t.Logf("waiting for scheduled create to fire... (%s remaining)", time.Until(deadline).Round(time.Second))
			time.Sleep(15 * time.Second)
		}
		require.NotEmpty(t, found, "scheduled create did not produce a new sandbox within 4 minutes")
		state.sandboxID = found
		t.Logf("scheduled create produced sandbox: %s", found)
	})

	// --- Step 4: Schedule a one-time pause 1 minute from now ---
	t.Run("04-schedule-pause", func(t *testing.T) {
		require.NotEmpty(t, state.sandboxID, "sandboxID must be set from step 3")
		name := state.testID + "-pause"
		stdout, stderr, err := runKM(t, km, "at", "in 1 minute", "pause", state.sandboxID, "--name", name)
		require.NoError(t, err, "km at pause failed: stdout=%s stderr=%s", stdout, stderr)
		require.Contains(t, stdout, "Scheduled:")
		t.Logf("scheduled pause: %s", stdout)
	})

	// --- Step 5: Wait for the sandbox to reach stopped/paused state ---
	t.Run("05-wait-paused", func(t *testing.T) {
		require.NotEmpty(t, state.sandboxID, "sandboxID must be set from step 3")
		// "pause" maps to event_type "stop" internally, so the state may be "stopped" or "paused".
		// Poll for either.
		deadline := time.Now().Add(3 * time.Minute)
		ok := false
		for time.Now().Before(deadline) {
			stdout, _, _ := runKM(t, km, "list", "--wide")
			lines := strings.Split(stdout, "\n")
			for _, line := range lines {
				if strings.Contains(line, state.sandboxID) {
					if strings.Contains(line, "stopped") || strings.Contains(line, "paused") {
						ok = true
					}
				}
			}
			if ok {
				break
			}
			t.Logf("waiting for sandbox %s to reach stopped/paused state...", state.sandboxID)
			time.Sleep(15 * time.Second)
		}
		require.True(t, ok, "sandbox %s did not reach stopped/paused state within 3 minutes", state.sandboxID)
		t.Logf("sandbox %s is paused/stopped", state.sandboxID)
	})

	// --- Step 6: Schedule a one-time resume 1 minute from now ---
	t.Run("06-schedule-resume", func(t *testing.T) {
		require.NotEmpty(t, state.sandboxID, "sandboxID must be set from step 3")
		name := state.testID + "-resume"
		stdout, stderr, err := runKM(t, km, "at", "in 1 minute", "resume", state.sandboxID, "--name", name)
		require.NoError(t, err, "km at resume failed: stdout=%s stderr=%s", stdout, stderr)
		require.Contains(t, stdout, "Scheduled:")
		t.Logf("scheduled resume: %s", stdout)
	})

	// --- Step 7: Wait for the sandbox to resume (running state) ---
	t.Run("07-wait-resumed", func(t *testing.T) {
		require.NotEmpty(t, state.sandboxID, "sandboxID must be set from step 3")
		ok := waitForSandboxState(t, km, state.sandboxID, "running", 3*time.Minute)
		require.True(t, ok, "sandbox %s did not resume to running state within 3 minutes", state.sandboxID)
		t.Logf("sandbox %s is running again", state.sandboxID)
	})

	// --- Step 8: Schedule a one-time extend (just verify scheduling and dispatch, no wait) ---
	t.Run("08-schedule-extend", func(t *testing.T) {
		require.NotEmpty(t, state.sandboxID, "sandboxID must be set from step 3")
		name := state.testID + "-extend"
		stdout, stderr, err := runKM(t, km, "at", "in 1 minute", "extend", state.sandboxID, "--name", name)
		require.NoError(t, err, "km at extend failed: stdout=%s stderr=%s", stdout, stderr)
		require.Contains(t, stdout, "Scheduled:")
		t.Logf("scheduled extend: %s", stdout)
		// We don't wait for extend to fire — it just pushes TTL forward and we verify the
		// schedule was created without error. The TTL Lambda handles the actual extend.
	})

	// --- Step 9: Test km at cancel (schedule something then cancel it before it fires) ---
	t.Run("09-at-cancel", func(t *testing.T) {
		require.NotEmpty(t, state.sandboxID, "sandboxID must be set from step 3")
		cancelName := state.testID + "-cancel-test"

		// Schedule a stop 10 minutes from now (so it won't fire before we cancel it)
		stdout, stderr, err := runKM(t, km, "at", "in 10 minutes", "stop", state.sandboxID, "--name", cancelName)
		require.NoError(t, err, "km at stop (to cancel) failed: stdout=%s stderr=%s", stdout, stderr)
		require.Contains(t, stdout, "Scheduled:")

		// Verify it appears in km at list
		listOut, _, err := runKM(t, km, "at", "list")
		require.NoError(t, err)
		require.Contains(t, listOut, cancelName, "newly created schedule should appear in km at list")

		// Cancel it
		stdout, stderr, err = runKM(t, km, "at", "cancel", cancelName)
		require.NoError(t, err, "km at cancel failed: stdout=%s stderr=%s", stdout, stderr)
		require.Contains(t, stdout, "Cancelled:", "expected 'Cancelled:' in cancel output")

		// Verify it is gone from km at list
		listOut, _, err = runKM(t, km, "at", "list")
		require.NoError(t, err)
		require.NotContains(t, listOut, cancelName, "cancelled schedule should not appear in km at list")
		t.Logf("cancelled schedule %s successfully", cancelName)
	})

	// --- Step 10: Test --cron flag for a recurring schedule (create + immediate cancel) ---
	t.Run("10-cron-recurring", func(t *testing.T) {
		require.NotEmpty(t, state.sandboxID, "sandboxID must be set from step 3")
		cronName := state.testID + "-cron"

		// Schedule a recurring kill far in the future (year 2099) so it won't fire during the test
		stdout, stderr, err := runKM(t, km, "at", "--cron", "cron(0 0 1 1 ? 2099)", "kill", state.sandboxID, "--remote", "--name", cronName)
		require.NoError(t, err, "km at --cron kill failed: stdout=%s stderr=%s", stdout, stderr)
		require.Contains(t, stdout, "Scheduled:", "expected 'Scheduled:' in cron output")

		// Verify it appears in km at list
		listOut, _, err := runKM(t, km, "at", "list")
		require.NoError(t, err)
		require.Contains(t, listOut, cronName, "cron schedule should appear in km at list")

		// Cancel the recurring schedule immediately — we don't want it to fire
		stdout, stderr, err = runKM(t, km, "at", "cancel", cronName)
		require.NoError(t, err, "km at cancel (cron) failed: stdout=%s stderr=%s", stdout, stderr)
		require.Contains(t, stdout, "Cancelled:")

		// Verify it is gone from km at list
		listOut, _, err = runKM(t, km, "at", "list")
		require.NoError(t, err)
		require.NotContains(t, listOut, cronName, "cancelled cron schedule should not appear in km at list")
		t.Logf("recurring cron schedule %s created and cancelled successfully", cronName)
	})

	// --- Step 11: Schedule a one-time kill to clean up the sandbox ---
	t.Run("11-schedule-kill", func(t *testing.T) {
		require.NotEmpty(t, state.sandboxID, "sandboxID must be set from step 3")
		name := state.testID + "-kill"
		stdout, stderr, err := runKM(t, km, "at", "in 1 minute", "kill", state.sandboxID, "--remote", "--name", name)
		require.NoError(t, err, "km at kill failed: stdout=%s stderr=%s", stdout, stderr)
		require.Contains(t, stdout, "Scheduled:")
		t.Logf("scheduled kill for sandbox %s: %s", state.sandboxID, stdout)
	})

	// --- Step 12: Wait for the sandbox to be destroyed ---
	t.Run("12-wait-destroyed", func(t *testing.T) {
		require.NotEmpty(t, state.sandboxID, "sandboxID must be set from step 3")
		ok := waitForSandboxGone(t, km, state.sandboxID, 4*time.Minute)
		require.True(t, ok, "sandbox %s was not destroyed within 4 minutes after scheduled kill", state.sandboxID)
		t.Logf("sandbox %s is gone", state.sandboxID)
		// Clear sandboxID so the t.Cleanup safety net does not try to destroy it again.
		state.sandboxID = ""
	})
}
