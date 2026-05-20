package cmd_test

// Phase 86 test suite for the prompt-queue helpers (PQ-01..PQ-08).
//
// Wave 1 (Plan 86-02) implements:
//   - PQ-01: --prompt and --wait flags registered on NewCreateCmd
//   - PQ-02: resolvePrompts (@file, @@, missing-file)
//   - PQ-03: --prompt + --docker hard-fail before provisioning
//   - PQ-04: pushQueueFiles SSM batch push structure
//
// Wave 1 (Plan 86-04) will implement PQ-05, PQ-06 (--wait polling).
// Wave 2 (Plan 86-03) will implement PQ-08 (runner state machine).
// Wave 3 (Plan 86-05) will implement PQ-07 (agent list --queue) via agent_test.go.

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// ---- PQ-01: --prompt flag registration on km create ----
//
// Asserts that NewCreateCmd registers --prompt as a StringArrayVar (repeatable)
// and --wait as a BoolVar.

func TestCreatePromptFlag(t *testing.T) {
	cfg := &config.Config{}
	createCmd := cmd.NewCreateCmd(cfg)

	// PQ-01a: --prompt flag exists and is a StringArrayVar
	promptFlag := createCmd.Flags().Lookup("prompt")
	if promptFlag == nil {
		t.Fatal("--prompt flag not registered on km create")
	}
	if promptFlag.Value.Type() != "stringArray" {
		t.Errorf("--prompt flag type = %q, want %q", promptFlag.Value.Type(), "stringArray")
	}
	if promptFlag.Shorthand != "" {
		t.Errorf("--prompt shorthand = %q, want empty (no shorthand)", promptFlag.Shorthand)
	}
	// Default value for a nil StringArrayVar is "[]" in pflag
	if promptFlag.DefValue != "[]" {
		t.Errorf("--prompt default = %q, want %q", promptFlag.DefValue, "[]")
	}

	// PQ-01b: --wait flag exists and is a BoolVar with default false
	waitFlag := createCmd.Flags().Lookup("wait")
	if waitFlag == nil {
		t.Fatal("--wait flag not registered on km create")
	}
	if waitFlag.Value.Type() != "bool" {
		t.Errorf("--wait flag type = %q, want %q", waitFlag.Value.Type(), "bool")
	}
	if waitFlag.DefValue != "false" {
		t.Errorf("--wait default = %q, want %q", waitFlag.DefValue, "false")
	}
}

// ---- PQ-02: resolvePrompts — @file reads, @@ escape, missing-file error ----
//
// Asserts the resolvePrompts helper processes @file, @@escape, and literal
// strings correctly.

func TestResolvePrompts(t *testing.T) {
	// Write a temp file to test @file reading.
	tmpFile, err := os.CreateTemp("", "wave1-test-*.txt")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.WriteString("hello from file"); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	tmpFile.Close()

	cases := []struct {
		name    string
		input   []string
		want    []string
		wantErr bool
		errMust string // substring in error message
	}{
		{
			name:  "literal passthrough",
			input: []string{"literal"},
			want:  []string{"literal"},
		},
		{
			name:  "@@ escape to literal @",
			input: []string{"@@literal"},
			want:  []string{"@literal"},
		},
		{
			name:  "@@x returns @x (multi-char after escape)",
			input: []string{"@@foo@bar"},
			want:  []string{"@foo@bar"},
		},
		{
			name:  "@file reads file content",
			input: []string{"@" + tmpFile.Name()},
			want:  []string{"hello from file"},
		},
		{
			name:    "@missing-file returns error with path",
			input:   []string{"@/does/not/exist/wave1-test"},
			wantErr: true,
			errMust: "/does/not/exist/wave1-test",
		},
		{
			name:  "mixed: literal + @@ + @file",
			input: []string{"literal", "@@x", "@" + tmpFile.Name()},
			want:  []string{"literal", "@x", "hello from file"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cmd.ResolvePrompts(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errMust)
				}
				if !strings.Contains(err.Error(), tc.errMust) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.errMust)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len(got) = %d, want %d", len(got), len(tc.want))
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// ---- PQ-03: --prompt + --docker hard-fail before provisioning ----
//
// Asserts that combining --prompt with --docker (or --substrate=docker)
// returns an error containing "queue requires EC2" before any AWS call.

func TestCreatePromptDockerReject(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "--docker + --prompt rejects",
			args:    []string{"--docker", "--prompt", "x", "fake.yaml"},
			wantErr: "queue requires EC2",
		},
		{
			name:    "--substrate=docker + --prompt rejects",
			args:    []string{"--substrate", "docker", "--prompt", "x", "fake.yaml"},
			wantErr: "queue requires EC2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			createCmd := cmd.NewCreateCmd(cfg)
			createCmd.SetArgs(tc.args)
			err := createCmd.Execute()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// ---- PQ-04: pushQueueFiles SSM batch push structure ----
//
// Asserts that pushQueueFiles sends exactly ONE SSM SendCommand call with
// the correct document, base64-encoded prompt content, and meta.json structure.

func TestPushQueueFiles(t *testing.T) {
	mockSSM := &mockAgentSSM{
		sendOutput: &ssm.SendCommandOutput{
			Command: &ssmtypes.Command{
				CommandId: awssdk.String("cmd-push-test"),
			},
		},
		invocations: []*ssm.GetCommandInvocationOutput{
			{
				Status:                ssmtypes.CommandInvocationStatusSuccess,
				StandardOutputContent: awssdk.String(""),
			},
		},
	}

	ctx := context.Background()
	err := cmd.PushQueueFiles(ctx, mockSSM, "i-abc123", []string{"first prompt", "second prompt"}, true)
	if err != nil {
		t.Fatalf("pushQueueFiles: %v", err)
	}

	// PQ-04a: exactly one SendCommand call
	if len(mockSSM.sendCalls) != 1 {
		t.Fatalf("expected 1 SendCommand call, got %d", len(mockSSM.sendCalls))
	}

	call := mockSSM.sendCalls[0]

	// PQ-04b: document is AWS-RunShellScript
	if awssdk.ToString(call.DocumentName) != "AWS-RunShellScript" {
		t.Errorf("DocumentName = %q, want %q", awssdk.ToString(call.DocumentName), "AWS-RunShellScript")
	}

	body := strings.Join(call.Parameters["commands"], "\n")

	// PQ-04c: body contains 001.prompt and 002.prompt filenames
	if !strings.Contains(body, "001.prompt") {
		t.Error("body missing 001.prompt")
	}
	if !strings.Contains(body, "002.prompt") {
		t.Error("body missing 002.prompt")
	}

	// PQ-04d: body contains 001.meta.json and 002.meta.json
	if !strings.Contains(body, "001.meta.json") {
		t.Error("body missing 001.meta.json")
	}
	if !strings.Contains(body, "002.meta.json") {
		t.Error("body missing 002.meta.json")
	}

	// PQ-04e: body contains base64-encoded prompt texts
	b64First := base64.StdEncoding.EncodeToString([]byte("first prompt"))
	b64Second := base64.StdEncoding.EncodeToString([]byte("second prompt"))
	if !strings.Contains(body, b64First) {
		t.Errorf("body missing base64 for 'first prompt' (%s)", b64First)
	}
	if !strings.Contains(body, b64Second) {
		t.Errorf("body missing base64 for 'second prompt' (%s)", b64Second)
	}

	// PQ-04f: meta.json contains no_bedrock:true and status:pending (via base64)
	// Decode all base64 chunks and check for the meta JSON patterns.
	foundNoBedrock := false
	foundStatusPending := false
	for _, chunk := range strings.Fields(body) {
		decoded, decErr := base64.StdEncoding.DecodeString(chunk)
		if decErr != nil {
			continue
		}
		s := string(decoded)
		if strings.Contains(s, `"no_bedrock":true`) {
			foundNoBedrock = true
		}
		if strings.Contains(s, `"status":"pending"`) {
			foundStatusPending = true
		}
	}
	if !foundNoBedrock {
		t.Error("meta.json missing \"no_bedrock\":true")
	}
	if !foundStatusPending {
		t.Error("meta.json missing \"status\":\"pending\"")
	}

	// PQ-04g: body ends with chown sandbox:sandbox
	if !strings.Contains(body, "chown -R sandbox:sandbox /workspace/.km-agent/queue") {
		t.Error("body missing chown -R sandbox:sandbox /workspace/.km-agent/queue")
	}

	// PQ-04h: body includes chmod 0700 on queue dir and chmod 0600 on files
	if !strings.Contains(body, "chmod 0700 /workspace/.km-agent/queue") {
		t.Error("body missing chmod 0700 on queue dir")
	}
	if !strings.Contains(body, "chmod 0600") {
		t.Error("body missing chmod 0600 on queue files")
	}
}

// ---- PQ-05: waitForQueueDrain — all done exits 0 ----
//
// (Plan 86-04 territory — --wait polling)

func TestCreatePromptWait(t *testing.T) {
	t.Skip("Wave 1 (Plan 86-04): waitForQueueDrain not yet implemented")
	// When Plan 86-04 lands, remove the t.Skip above.
	// Expected signature:
	//   func waitForQueueDrain(ctx context.Context, ssmClient SSMSendAPI, instanceID string, expectedCount int) (exitCode int, err error)

	// Sequence: pending|pending -> running|pending -> done|done
	mockSSM := &mockAgentSSM{
		invocations: []*ssm.GetCommandInvocationOutput{
			{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: awssdk.String("pending|pending")},
			{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: awssdk.String("running|pending")},
			{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: awssdk.String("done|done")},
		},
	}

	ctx := context.Background()
	_ = ctx
	_ = mockSSM

	// exitCode, err := waitForQueueDrain(ctx, mockSSM, "i-abc", 2)
	// if err != nil { t.Fatalf("waitForQueueDrain: %v", err) }
	// if exitCode != 0 { t.Errorf("exitCode = %d, want 0", exitCode) }
}

// ---- PQ-06: waitForQueueDrain — first failed exits non-zero ----
//
// (Plan 86-04 territory — --wait polling)

func TestCreatePromptWaitFail(t *testing.T) {
	t.Skip("Wave 1 (Plan 86-04): waitForQueueDrain failure path not yet implemented")
	// When Plan 86-04 lands, remove the t.Skip above.

	// Sequence: first poll returns failed|skipped
	mockSSM := &mockAgentSSM{
		invocations: []*ssm.GetCommandInvocationOutput{
			{Status: ssmtypes.CommandInvocationStatusSuccess, StandardOutputContent: awssdk.String("failed|skipped")},
		},
	}

	ctx := context.Background()
	_ = ctx
	_ = mockSSM

	// exitCode, err := waitForQueueDrain(ctx, mockSSM, "i-abc", 2)
	// if exitCode == 0 { t.Error("expected non-zero exit code on first-prompt failure") }
	// if err == nil { t.Error("expected non-nil error describing the failure") }
}

// ---- PQ-08: Queue runner state machine (Go-side mirror) ----
//
// (Plan 86-03 / Wave 2 territory — runner state machine)

func TestQueueRunnerStateMachine(t *testing.T) {
	// Wave 2 (86-03): ReconcileMetaStatus implemented in create_prompt.go.
	// This is the Go-side mirror of the bash runner's startup reconcile step:
	// "running" → "pending" on every start; all other statuses are idempotent.
	cases := []struct {
		input string
		want  string
		desc  string
	}{
		{"running", "pending", "running resets to pending on startup (reboot recovery)"},
		{"pending", "pending", "pending stays pending (no change at probe time)"},
		{"done", "done", "done is idempotent"},
		{"failed", "failed", "failed is idempotent"},
		{"skipped", "skipped", "skipped is idempotent"},
	}

	for _, tc := range cases {
		t.Run(tc.input+"->"+tc.want, func(t *testing.T) {
			got := cmd.ReconcileMetaStatus(tc.input)
			if got != tc.want {
				t.Errorf("ReconcileMetaStatus(%q) = %q, want %q (%s)", tc.input, got, tc.want, tc.desc)
			}
		})
	}
}

// ---- compile-time interface check ----
// Ensure mockAgentSSM (from agent_test.go, same package) satisfies SSMSendAPI.
// This is already asserted in agent_test.go; repeated here for clarity.
var _ cmd.SSMSendAPI = (*mockAgentSSM)(nil)
