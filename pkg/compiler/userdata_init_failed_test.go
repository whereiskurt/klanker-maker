package compiler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// initBlock extracts the rendered "7.5. Profile init" bootstrap block — from
// the KM_INIT_BUCKET= assignment through the closing fi — so the tests below
// run the EXACT bash a boot runs, under the same `set -euo pipefail`.
func initBlock(t *testing.T, out string) string {
	t.Helper()
	start := strings.Index(out, "KM_INIT_BUCKET=")
	if start < 0 {
		t.Fatal("7.5 block not rendered")
	}
	tail := strings.Index(out[start:], "No init script found in S3 (skipped)\"\nfi\n")
	if tail < 0 {
		t.Fatal("7.5 block terminator not found")
	}
	return out[start : start+tail+len("No init script found in S3 (skipped)\"\nfi\n")]
}

// runInitBlock executes the block with three path substitutions and two fake
// tools, all of which exist only because a test cannot write /var/lib/km or
// /run/km/audit-pipe: the marker dir, the audit pipe (a plain file here), and
// `aws s3 cp` (writes initScript to the destination) plus `timeout` (macOS
// has none; ours drops its first argument and execs the rest).
func runInitBlock(t *testing.T, block, initScript string) (stdout, stderr, audit, marker string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(dir, "audit-pipe")
	block = strings.ReplaceAll(block, "/var/lib/km", dir)
	block = strings.ReplaceAll(block, "/run/km/audit-pipe", pipe)
	block = strings.ReplaceAll(block, "/tmp/km-init.sh", filepath.Join(dir, "km-init.sh"))

	awsBody := "#!/bin/sh\nexit 1\n"
	if initScript != "" {
		awsBody = "#!/bin/sh\n# aws s3 cp <src> <dst>: deliver the prepared init script\ncat > \"$4\" <<'KMEOF'\n" + initScript + "\nKMEOF\n"
	}
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte(awsBody), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "timeout"), []byte("#!/bin/sh\nshift\nexec \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-euo", "pipefail", "-c", block)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin"}
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("the bootstrap block must never abort the boot over an init failure, but exited: %v\nstderr:\n%s", err, se.String())
	}
	a, _ := os.ReadFile(pipe)
	m, _ := os.ReadFile(filepath.Join(dir, "init-failed"))
	return so.String(), se.String(), string(a), string(m)
}

func initProfile() *profile.SandboxProfile {
	p := baseProfile()
	p.Spec.Execution.InitCommands = []string{"true"}
	return p
}

// TestUserdataInitBlock_FailureIsReportedNotSwallowed pins the caller side of
// the 2026-09-17 fix. The old block was `aws s3 cp ... && { ...; /tmp/km-init.sh;
// echo "Init complete"; } || echo "No init script"` — errexit is suspended
// inside an && list, so a failed init printed "Init complete" and the boot went
// on to SANDBOX_READY with ten initCommands silently dropped. Now: no "Init
// complete", a WARNING naming the step and the count skipped, an init_failed
// audit event — and the boot still continues.
func TestUserdataInitBlock_FailureIsReportedNotSwallowed(t *testing.T) {
	out, err := generateUserData(initProfile(), "sb-init", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// What buildInitScript's ERR trap writes when step 3 of 13 dies.
	// The script lands at <dir>/km-init.sh and the marker dir is the same
	// <dir> (runInitBlock substitutes both), so $0's directory is the marker dir.
	failing := "#!/bin/bash\n" +
		"printf 'exit=1\\nstep=3\\ntotal=13\\ncommand=npm install -g \"@anthropic-ai/claude-code@2.1.171\"\\n' > \"$(dirname \"$0\")/init-failed\"\n" +
		"exit 1\n"
	stdout, stderr, audit, marker := runInitBlock(t, initBlock(t, out), failing)
	if strings.Contains(stdout, "Init complete") {
		t.Errorf("\"Init complete\" must not be printed after a failed init; stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "WARNING: profile init FAILED (exit 1) at step 3/13: npm install -g \"@anthropic-ai/claude-code@2.1.171\"") {
		t.Errorf("stderr must name the failing step; got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "10 later initCommand(s) were NOT run") {
		t.Errorf("stderr must say how many steps were abandoned; got:\n%s", stderr)
	}
	if marker == "" {
		t.Error("marker file should have been left in place for km shell --root")
	}
	var ev struct {
		EventType string `json:"event_type"`
		Source    string `json:"source"`
		SandboxID string `json:"sandbox_id"`
		Detail    struct {
			ExitCode string `json:"exit_code"`
			Step     string `json:"step"`
			Total    string `json:"total"`
			Skipped  string `json:"skipped"`
			Command  string `json:"command"`
		} `json:"detail"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(audit)), &ev); err != nil {
		t.Fatalf("audit event is not valid JSON (%v):\n%s", err, audit)
	}
	if ev.EventType != "init_failed" || ev.Source != "bootstrap" || ev.SandboxID != "sb-init" {
		t.Errorf("audit envelope wrong: %+v", ev)
	}
	if ev.Detail.Step != "3" || ev.Detail.Total != "13" || ev.Detail.Skipped != "10" || ev.Detail.ExitCode != "1" {
		t.Errorf("audit detail wrong: %+v", ev.Detail)
	}
	if ev.Detail.Command != `npm install -g "@anthropic-ai/claude-code@2.1.171"` {
		t.Errorf("command must survive JSON escaping intact: %q", ev.Detail.Command)
	}
}

// TestUserdataInitBlock_SuccessAndAbsentUnchanged: the two paths that already
// worked keep their exact lines, and neither emits an audit event.
func TestUserdataInitBlock_SuccessAndAbsentUnchanged(t *testing.T) {
	out, err := generateUserData(initProfile(), "sb-init", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	block := initBlock(t, out)

	stdout, _, audit, _ := runInitBlock(t, block, "#!/bin/bash\necho '[km-init] Profile init complete'\n")
	if !strings.Contains(stdout, "[km-bootstrap] Init complete") || audit != "" {
		t.Errorf("success path: want Init complete and no audit event; stdout:\n%s\naudit: %q", stdout, audit)
	}

	stdout, _, audit, _ = runInitBlock(t, block, "")
	if !strings.Contains(stdout, "[km-bootstrap] No init script found in S3 (skipped)") || audit != "" {
		t.Errorf("absent path: want the skipped line and no audit event; stdout:\n%s\naudit: %q", stdout, audit)
	}
}
