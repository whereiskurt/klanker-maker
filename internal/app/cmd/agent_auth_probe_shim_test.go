package cmd

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// recordingSSM captures the command text each probe actually sends.
type recordingSSM struct {
	stdout string
	sent   []string
}

func (r *recordingSSM) SendCommand(_ context.Context, in *ssm.SendCommandInput, _ ...func(*ssm.Options)) (*ssm.SendCommandOutput, error) {
	r.sent = append(r.sent, strings.Join(in.Parameters["commands"], "\n"))
	return &ssm.SendCommandOutput{Command: &ssmtypes.Command{CommandId: awssdk.String("cmd-1")}}, nil
}

func (r *recordingSSM) GetCommandInvocation(_ context.Context, _ *ssm.GetCommandInvocationInput, _ ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error) {
	return &ssm.GetCommandInvocationOutput{
		Status:                ssmtypes.CommandInvocationStatusSuccess,
		StandardOutputContent: awssdk.String(r.stdout),
	}, nil
}

// Every `claude auth status` probe must put /opt/km/shims FIRST on PATH
// explicitly, the way every dispatch_as_sandbox site in userdata does — never
// by trusting profile.d ordering. On a brokered-secrets box the shim is what
// injects ANTHROPIC_API_KEY (claude then reports loggedIn:true, authMethod
// api_key); the real binary reports loggedIn:false. A login shell puts nvm's
// bin ahead of profile.d's prepend (proven live on learn-ccd011a0 2026-09-20:
// `which claude` → ~/.nvm/.../bin/claude), so a probe without its own prepend
// reads the WRONG claude and km status / km list --auth report a working
// sandbox as logged out.
func TestClaudeAuthProbes_PrependShimDirExplicitly(t *testing.T) {
	const want = "PATH=/opt/km/shims:$PATH"

	r := &recordingSSM{stdout: `{"loggedIn": true}`}
	if _, _, err := checkAgentAuth(context.Background(), r, "i-0abc"); err != nil {
		t.Fatal(err)
	}
	r2 := &recordingSSM{stdout: `{"loggedIn": true}`}
	claudeAuthedNoBedrock(context.Background(), r2, "i-0abc")

	for name, sent := range map[string][]string{"checkAgentAuth": r.sent, "claudeAuthedNoBedrock": r2.sent} {
		if len(sent) != 1 {
			t.Fatalf("%s: sent %d commands, want 1", name, len(sent))
		}
		cmd := sent[0]
		i, j := strings.Index(cmd, want), strings.Index(cmd, "claude auth status")
		if i < 0 || j < 0 || i > j {
			t.Errorf("%s: probe must prepend %q before `claude auth status`; sent:\n%s", name, want, cmd)
		}
	}
}

// Name-agnostic guard over the package source: any non-test line that runs
// `claude auth status` in a sandbox login shell must carry the shim prepend on
// the same line, so a new probe site cannot ship trusting profile.d ordering.
func TestEveryClaudeAuthStatusInvocationPrependsShimDir(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// A real invocation is a shell string that runs a login shell AND asks for
	// auth status; comments that merely mention the command don't carry bash -lc.
	invoke := regexp.MustCompile(`bash -lc .*claude auth status`)
	var seen, bad []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(raw), "\n") {
			if !invoke.MatchString(line) {
				continue
			}
			seen = append(seen, f)
			if !strings.Contains(line, "PATH=/opt/km/shims:$PATH") {
				bad = append(bad, f+":"+strconv.Itoa(n+1))
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("found no `claude auth status` invocation — the regex has drifted from the code")
	}
	if len(bad) > 0 {
		t.Errorf("these `claude auth status` invocations do not prepend /opt/km/shims (use sandboxClaudeProbe): %v", bad)
	}
}
