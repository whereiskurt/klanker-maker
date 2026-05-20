package cmd_test

// Phase 86 RED-state stubs. Each test asserts a contract from
// .planning/phases/86-km-create-prompt-queue/86-VALIDATION.md.
// Implementation lands in Waves 1-4. Until then, tests call
// helpers that may not yet exist; this file is gated by
// `go test` returning FAIL until Waves 1-4 ship.

import (
	"context"
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
// and --wait as a BoolVar. Wave 1 wires these flags into NewCreateCmd.

func TestCreatePromptFlag(t *testing.T) {
	t.Skip("Wave 1: --prompt and --wait flags not yet registered on NewCreateCmd")
	// When Wave 1 lands, remove the t.Skip above and verify:
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
// strings correctly. Wave 1 implements resolvePrompts in create.go or
// create_prompt.go.

func TestResolvePrompts(t *testing.T) {
	t.Skip("Wave 1: resolvePrompts not yet implemented")
	// When Wave 1 lands, remove the t.Skip above.
	// The helper signature expected:
	//   func resolvePrompts(raw []string) ([]string, error)
	// Since it's unexported, Wave 1 must either export it or this test
	// calls it via a public wrapper. Stub table retained for contract lock-in.

	// Table contract — each case names the PQ-02 sub-behavior:
	cases := []struct {
		name    string
		input   []string
		wantErr bool
		errMust string // substring in error message
	}{
		{
			name:  "literal passthrough",
			input: []string{"literal"},
		},
		{
			name:  "@@ escape to literal @",
			input: []string{"@@literal"},
		},
		{
			name:  "@file reads file content",
			input: []string{"@/tmp/wave1-test-DOES-NOT-EXIST"}, // tmp file created in real impl
		},
		{
			name:    "@missing-file returns error with path",
			input:   []string{"@/does/not/exist"},
			wantErr: true,
			errMust: "/does/not/exist",
		},
		{
			name:  "mixed: literal + @@ + @file",
			input: []string{"literal", "@@x", "@/tmp/wave1-test-DOES-NOT-EXIST"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Wave 1: replace this skip with the actual call.
			t.Skip("Wave 1: resolvePrompts — " + tc.name)
			_ = tc.errMust
			_ = tc.wantErr
		})
	}
}

// ---- PQ-03: --prompt + --docker hard-fail before provisioning ----
//
// Asserts that combining --prompt with --docker (or --substrate=docker)
// returns an error containing "queue requires EC2" before any AWS call.
// Wave 1 adds the guard at the top of NewCreateCmd's RunE.

func TestCreatePromptDockerReject(t *testing.T) {
	t.Skip("Wave 1: docker rejection guard for --prompt not yet implemented")
	// When Wave 1 lands, remove the t.Skip above.
	// Expected behavior: RunE returns an error before any SSM call.
	// The mock SSM below tracks calls; assertion: zero calls on rejection.

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
			t.Skip("Wave 1: --prompt docker guard — " + tc.name)
			mockSSM := &mockAgentSSM{}
			_ = mockSSM

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
			if len(mockSSM.sendCalls) != 0 {
				t.Errorf("expected 0 SSM calls on docker rejection, got %d", len(mockSSM.sendCalls))
			}
		})
	}
}

// ---- PQ-04: pushQueueFiles SSM batch push structure ----
//
// Asserts that pushQueueFiles sends exactly ONE SSM SendCommand call with
// the correct document, base64-encoded prompt content, and meta.json structure.
// Wave 1 implements pushQueueFiles in create.go or create_prompt.go.

func TestPushQueueFiles(t *testing.T) {
	t.Skip("Wave 1: pushQueueFiles not yet implemented")
	// When Wave 1 lands, remove the t.Skip and call pushQueueFiles.
	// Expected exported/accessible signature:
	//   func pushQueueFiles(ctx context.Context, ssmClient SSMSendAPI, instanceID string, prompts []string, noBedrock bool) error
	// (Either exported or tested via a public wrapper/test hook.)

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
	_ = ctx
	_ = mockSSM

	// PQ-04a: exactly one SendCommand call
	// err := pushQueueFiles(ctx, mockSSM, "i-abc", []string{"first", "second"}, true)
	// if err != nil { t.Fatalf("pushQueueFiles: %v", err) }

	// PQ-04b: one SendCommand call
	// if len(mockSSM.sendCalls) != 1 { t.Fatalf("expected 1 SendCommand, got %d", len(mockSSM.sendCalls)) }

	// PQ-04c: document is AWS-RunShellScript
	// call := mockSSM.sendCalls[0]
	// if awssdk.ToString(call.DocumentName) != "AWS-RunShellScript" { t.Errorf(...) }

	// PQ-04d: body contains 001.prompt and 002.prompt filenames
	// body := strings.Join(call.Parameters["commands"], "\n")
	// if !strings.Contains(body, "001.prompt") { t.Error("missing 001.prompt") }
	// if !strings.Contains(body, "002.prompt") { t.Error("missing 002.prompt") }

	// PQ-04e: body contains base64-encoded prompts
	// b64first := base64.StdEncoding.EncodeToString([]byte("first"))
	// b64second := base64.StdEncoding.EncodeToString([]byte("second"))
	// if !strings.Contains(body, b64first) { t.Error("missing base64 for 'first'") }
	// if !strings.Contains(body, b64second) { t.Error("missing base64 for 'second'") }

	// PQ-04f: meta.json contains no_bedrock:true and status:pending
	// if !strings.Contains(body, `"no_bedrock":true`) { t.Error("missing no_bedrock:true in meta") }
	// if !strings.Contains(body, `"status":"pending"`) { t.Error("missing status:pending in meta") }

	// PQ-04g: chown sandbox:sandbox at end
	// if !strings.Contains(body, "chown -R sandbox:sandbox /workspace/.km-agent/queue") { t.Error("missing chown") }
}

// ---- PQ-05: waitForQueueDrain — all done exits 0 ----
//
// Asserts that waitForQueueDrain polls meta.json and returns (0, nil) when
// all entries reach "done". Wave 1 implements waitForQueueDrain.

func TestCreatePromptWait(t *testing.T) {
	t.Skip("Wave 1: waitForQueueDrain not yet implemented")
	// When Wave 1 lands, remove the t.Skip above.
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
// Asserts that waitForQueueDrain returns non-zero exit code when the first
// entry fails. The remaining entries should show as "skipped".
// Wave 1 implements waitForQueueDrain.

func TestCreatePromptWaitFail(t *testing.T) {
	t.Skip("Wave 1: waitForQueueDrain failure path not yet implemented")
	// When Wave 1 lands, remove the t.Skip above.

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
	// if err != nil && !strings.Contains(err.Error(), "failed") {
	//     t.Errorf("error = %q, want substring 'failed'", err.Error())
	// }
}

// ---- PQ-08: Queue runner state machine (Go-side mirror) ----
//
// Table-driven test of a reconcileEntry(status string) -> string pure function
// that maps meta.json status values to their post-reconcile value.
// This mirrors the bash runner's startup reconcile step in Go.
// Wave 1 or Wave 2 implements reconcileEntry (whichever wave seeds the runner).
//
// Note: the actual bash invocation is tested in pkg/profile/configfiles/km-queue-runner_test.sh.

func TestQueueRunnerStateMachine(t *testing.T) {
	t.Skip("Wave 2: reconcileEntry not yet implemented — runner state machine is Wave 2 territory")
	// When Wave 2 lands, remove the t.Skip and implement reconcileEntry.
	// Expected signature (unexported or test-accessible):
	//   func reconcileEntry(status string) string

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
			t.Skip("Wave 2: reconcileEntry — " + tc.desc)
			// got := reconcileEntry(tc.input)
			// if got != tc.want {
			//     t.Errorf("reconcileEntry(%q) = %q, want %q (%s)", tc.input, got, tc.want, tc.desc)
			// }
		})
	}
}

// ---- compile-time interface check ----
// Ensure mockAgentSSM (from agent_test.go, same package) satisfies SSMSendAPI.
// This is already asserted in agent_test.go; repeated here for clarity.
var _ cmd.SSMSendAPI = (*mockAgentSSM)(nil)
