package cmd_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/whereiskurt/klankrmkr/internal/app/cmd"
	"github.com/whereiskurt/klankrmkr/internal/app/config"
	"github.com/whereiskurt/klankrmkr/pkg/allowlistgen"
	kmaws "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// ---- Helpers ----

func runShellCmd(t *testing.T, fetcher cmd.SandboxFetcher, capturedArgs *[]string, args ...string) error {
	t.Helper()
	cfg := &config.Config{}
	root := &cobra.Command{Use: "km"}
	shellCmd := cmd.NewShellCmdWithFetcher(cfg, fetcher, func(c *exec.Cmd) error {
		*capturedArgs = c.Args
		return nil
	})
	root.AddCommand(shellCmd)

	root.SetArgs(append([]string{"shell"}, args...))

	return root.Execute()
}

// ---- Tests ----

// TestShellCmd_EC2 verifies that an EC2 sandbox dispatches to
// `aws ssm start-session --target <instance-id> --region <region>`.
func TestShellCmd_EC2(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-ec2",
			Profile:   "open-dev",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
			Resources: []string{
				"arn:aws:ec2:us-east-1:123456789012:instance/i-0abc123def456",
				"arn:aws:ec2:us-east-1:123456789012:security-group/sg-0def456",
			},
		},
	}

	var capturedArgs []string
	err := runShellCmd(t, fetcher, &capturedArgs, "sb-ec2")
	if err != nil {
		t.Fatalf("shell command returned error: %v", err)
	}

	// Must include SSM start-session
	if len(capturedArgs) < 4 {
		t.Fatalf("expected at least 4 args, got %d: %v", len(capturedArgs), capturedArgs)
	}

	fullCmd := strings.Join(capturedArgs, " ")
	if !strings.Contains(fullCmd, "ssm") {
		t.Errorf("expected 'ssm' in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "start-session") {
		t.Errorf("expected 'start-session' in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "--target") {
		t.Errorf("expected '--target' in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "i-0abc123def456") {
		t.Errorf("expected instance ID 'i-0abc123def456' in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "--region") {
		t.Errorf("expected '--region' in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "us-east-1") {
		t.Errorf("expected region 'us-east-1' in command, got: %s", fullCmd)
	}
}

// TestShellCmd_ECS verifies that an ECS sandbox dispatches to
// `aws ecs execute-command --cluster <arn> --task <arn> --interactive --command /bin/bash --region <region>`.
func TestShellCmd_ECS(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-ecs",
			Profile:   "ecs-dev",
			Substrate: "ecs",
			Region:    "us-west-2",
			Status:    "running",
			CreatedAt: createdAt,
			Resources: []string{
				"arn:aws:ecs:us-west-2:123456789012:cluster/sb-ecs-cluster",
				"arn:aws:ecs:us-west-2:123456789012:task/sb-ecs-cluster/abc123task456",
				"arn:aws:ecs:us-west-2:123456789012:service/sb-ecs-cluster/sb-ecs-service",
			},
		},
	}

	var capturedArgs []string
	err := runShellCmd(t, fetcher, &capturedArgs, "sb-ecs")
	if err != nil {
		t.Fatalf("shell command returned error: %v", err)
	}

	fullCmd := strings.Join(capturedArgs, " ")
	if !strings.Contains(fullCmd, "ecs") {
		t.Errorf("expected 'ecs' in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "execute-command") {
		t.Errorf("expected 'execute-command' in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "--cluster") {
		t.Errorf("expected '--cluster' in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "sb-ecs-cluster") {
		t.Errorf("expected cluster ARN in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "--task") {
		t.Errorf("expected '--task' in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "abc123task456") {
		t.Errorf("expected task ARN in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "--interactive") {
		t.Errorf("expected '--interactive' in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "--command") {
		t.Errorf("expected '--command' in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "/bin/bash") {
		t.Errorf("expected '/bin/bash' in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "--region") {
		t.Errorf("expected '--region' in command, got: %s", fullCmd)
	}
	if !strings.Contains(fullCmd, "us-west-2") {
		t.Errorf("expected region 'us-west-2' in command, got: %s", fullCmd)
	}
}

// TestShellCmd_StoppedSandbox verifies that a stopped sandbox returns an error.
func TestShellCmd_StoppedSandbox(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-stopped",
			Profile:   "open-dev",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "stopped",
			CreatedAt: createdAt,
			Resources: []string{
				"arn:aws:ec2:us-east-1:123456789012:instance/i-0abc123def456",
			},
		},
	}

	var capturedArgs []string
	err := runShellCmd(t, fetcher, &capturedArgs, "sb-stopped")
	if err == nil {
		t.Fatal("expected error for stopped sandbox, got nil")
	}

	if !strings.Contains(err.Error(), "stopped") {
		t.Errorf("expected error to contain 'stopped', got: %v", err)
	}
}

// TestShellCmd_UnknownSubstrate verifies that an unsupported substrate returns an error.
func TestShellCmd_UnknownSubstrate(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-k8s",
			Profile:   "k8s-dev",
			Substrate: "k8s",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
		},
	}

	var capturedArgs []string
	err := runShellCmd(t, fetcher, &capturedArgs, "sb-k8s")
	if err == nil {
		t.Fatal("expected error for unknown substrate, got nil")
	}

	if !strings.Contains(err.Error(), "k8s") {
		t.Errorf("expected error to mention substrate 'k8s', got: %v", err)
	}
}

// TestShellCmd_MissingInstanceID verifies that an EC2 sandbox with no instance ARN returns an error.
func TestShellCmd_MissingInstanceID(t *testing.T) {
	createdAt := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	fetcher := &fakeFetcher{
		record: &kmaws.SandboxRecord{
			SandboxID: "sb-noinstance",
			Profile:   "open-dev",
			Substrate: "ec2",
			Region:    "us-east-1",
			Status:    "running",
			CreatedAt: createdAt,
			Resources: []string{
				// Only a security group, no instance ARN
				"arn:aws:ec2:us-east-1:123456789012:security-group/sg-0def456",
			},
		},
	}

	var capturedArgs []string
	err := runShellCmd(t, fetcher, &capturedArgs, "sb-noinstance")
	if err == nil {
		t.Fatal("expected error for missing instance ID, got nil")
	}
}

// Compile-time check that fakeFetcher (defined in status_test.go) implements SandboxFetcher.
var _ cmd.SandboxFetcher = (*fakeFetcher)(nil)

// Compile-time check that the ShellExecFunc type is correct.
var _ cmd.ShellExecFunc = func(c *exec.Cmd) error { return nil }

// fakeShellFetcher provides a sandbox record inline for shell tests that need
// a standalone fetcher (avoids conflict with fakeFetcher in status_test.go).
type fakeShellFetcher struct {
	record *kmaws.SandboxRecord
	err    error
}

func (f *fakeShellFetcher) FetchSandbox(_ context.Context, _ string) (*kmaws.SandboxRecord, error) {
	return f.record, f.err
}

// TestLearnObservedStateJSONRoundTrip verifies that learnObservedState round-trips
// through JSON marshal/unmarshal with the Commands field preserved.
func TestLearnObservedStateJSONRoundTrip(t *testing.T) {
	data := []byte(`{
		"dns": ["github.com"],
		"hosts": ["api.github.com"],
		"repos": ["github.com/foo/bar"],
		"refs": ["abc123"],
		"commands": ["git clone https://github.com/foo/bar", "make build"]
	}`)

	// GenerateProfileFromJSON uses learnObservedState internally; we verify
	// that commands survive the JSON round-trip by checking they appear in the YAML output.
	yamlBytes, err := cmd.GenerateProfileFromJSON(data, "")
	if err != nil {
		t.Fatalf("GenerateProfileFromJSON returned error: %v", err)
	}
	yaml := string(yamlBytes)
	if !strings.Contains(yaml, "git clone https://github.com/foo/bar") {
		t.Errorf("expected first command in generated YAML, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "make build") {
		t.Errorf("expected second command in generated YAML, got:\n%s", yaml)
	}
}

// TestGenerateProfileFromJSONWithCommands verifies that commands in the JSON blob
// are fed into the Recorder and appear in initCommands of the generated profile.
func TestGenerateProfileFromJSONWithCommands(t *testing.T) {
	data := []byte(`{
		"dns": ["example.com"],
		"hosts": [],
		"repos": [],
		"commands": ["apt-get install -y jq", "curl https://example.com/setup.sh | bash"]
	}`)

	yamlBytes, err := cmd.GenerateProfileFromJSON(data, "")
	if err != nil {
		t.Fatalf("GenerateProfileFromJSON returned error: %v", err)
	}
	yaml := string(yamlBytes)
	if !strings.Contains(yaml, "apt-get install -y jq") {
		t.Errorf("expected apt-get command in initCommands section, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "curl https://example.com/setup.sh | bash") {
		t.Errorf("expected curl command in initCommands section, got:\n%s", yaml)
	}
}

// TestGenerateProfileFromJSONNoCommands verifies that a JSON blob without commands
// generates a valid profile without errors (commands field is optional/omitempty).
func TestGenerateProfileFromJSONNoCommands(t *testing.T) {
	data := []byte(`{
		"dns": ["example.com"],
		"hosts": [],
		"repos": []
	}`)

	yamlBytes, err := cmd.GenerateProfileFromJSON(data, "")
	if err != nil {
		t.Fatalf("GenerateProfileFromJSON returned error: %v", err)
	}
	if len(yamlBytes) == 0 {
		t.Error("expected non-empty YAML output")
	}
}

// TestParseAuditLogCommands verifies that ParseAuditLogCommands parses JSON-lines
// with event_type=="command", records them into the Recorder, and ignores non-command events.
func TestParseAuditLogCommands(t *testing.T) {
	input := strings.Join([]string{
		`{"timestamp":"2026-01-01T00:00:01Z","event_type":"command","detail":{"command":"git clone https://github.com/foo/bar"}}`,
		`{"timestamp":"2026-01-01T00:00:02Z","event_type":"heartbeat","detail":{}}`,
		`{"timestamp":"2026-01-01T00:00:03Z","event_type":"command","detail":{"command":"make build"}}`,
		`{"timestamp":"2026-01-01T00:00:04Z","event_type":"command","detail":{"command":"git clone https://github.com/foo/bar"}}`,
	}, "\n")

	rec := allowlistgen.NewRecorder()
	cmd.ParseAuditLogCommands(strings.NewReader(input), rec)

	cmds := rec.Commands()
	if len(cmds) != 2 {
		t.Fatalf("expected 2 unique commands, got %d: %v", len(cmds), cmds)
	}
	if cmds[0] != "git clone https://github.com/foo/bar" {
		t.Errorf("expected first command 'git clone https://github.com/foo/bar', got %q", cmds[0])
	}
	if cmds[1] != "make build" {
		t.Errorf("expected second command 'make build', got %q", cmds[1])
	}
}

// TestParseAuditLogCommands_MalformedLines verifies that malformed JSON lines are
// silently skipped and do not cause errors.
func TestParseAuditLogCommands_MalformedLines(t *testing.T) {
	input := strings.Join([]string{
		`not valid json`,
		`{"timestamp":"2026-01-01T00:00:01Z","event_type":"command","detail":{"command":"echo hello"}}`,
		`{broken`,
		`{"event_type":"command","detail":{"command":""}}`,
	}, "\n")

	rec := allowlistgen.NewRecorder()
	cmd.ParseAuditLogCommands(strings.NewReader(input), rec)

	cmds := rec.Commands()
	// Only "echo hello" should be recorded; empty command and malformed lines skip.
	if len(cmds) != 1 {
		t.Fatalf("expected 1 command, got %d: %v", len(cmds), cmds)
	}
	if cmds[0] != "echo hello" {
		t.Errorf("expected 'echo hello', got %q", cmds[0])
	}
}

// TestDefaultLearnFilename verifies the default output name for `km shell --learn`
// embeds the sandbox ID and a second-precision timestamp so multiple sessions
// against different sandboxes don't collide.
func TestDefaultLearnFilename(t *testing.T) {
	ts := time.Date(2026, 4, 19, 14, 15, 23, 0, time.UTC)

	tests := []struct {
		name      string
		sandboxID string
		want      string
	}{
		{
			name:      "typical sandbox id",
			sandboxID: "leaner-abc12345",
			want:      "learned.leaner-abc12345.20260419141523.yaml",
		},
		{
			name:      "empty sandbox id falls back to timestamp only",
			sandboxID: "",
			want:      "learned.20260419141523.yaml",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cmd.DefaultLearnFilename(tc.sandboxID, ts)
			if got != tc.want {
				t.Errorf("DefaultLearnFilename(%q) = %q, want %q", tc.sandboxID, got, tc.want)
			}
		})
	}
}

// TestCollectDockerObservationsWithAuditLogs verifies that CollectDockerObservations
// accepts an auditLogs reader and includes parsed commands in the output JSON.
func TestCollectDockerObservationsWithAuditLogs(t *testing.T) {
	auditLog := strings.NewReader(strings.Join([]string{
		`{"event_type":"command","detail":{"command":"npm install"}}`,
		`{"event_type":"command","detail":{"command":"npm test"}}`,
	}, "\n"))

	data, err := cmd.CollectDockerObservations("sb-test", nil, nil, auditLog)
	if err != nil {
		t.Fatalf("CollectDockerObservations returned error: %v", err)
	}
	output := string(data)
	if !strings.Contains(output, "npm install") {
		t.Errorf("expected 'npm install' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "npm test") {
		t.Errorf("expected 'npm test' in output, got:\n%s", output)
	}
}
