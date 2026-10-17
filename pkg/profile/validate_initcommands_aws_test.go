package profile

import (
	"strings"
	"testing"
)

func TestValidateInitCommandAWSCalls(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		otp  []string
		warn bool
	}{
		{"no aws", "npm install -g @anthropic-ai/claude-code", nil, false},
		{"caller identity", "aws sts get-caller-identity", nil, false},
		{"models sync is granted", "aws s3 sync s3://${KM_ARTIFACTS_BUCKET}/models/qwen/ /data/models", nil, false},
		{"binaries cp is granted", `aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/binaries/herdr" /usr/local/bin/herdr`, nil, false},
		{"foreign bucket", "aws s3 cp s3://some-other-bucket/tool.tgz /tmp/", nil, true},
		{"own bucket, ungranted prefix", "aws s3 ls s3://${KM_ARTIFACTS_BUCKET}/sandboxes/", nil, true},
		{"ssm path in allowedSecretPaths", "aws ssm get-parameter --name {{prefix}}/wiz/client-id", nil, false},
		{"ssm path not listed", "aws ssm get-parameter --name /km/config/slack/bot-token", nil, true},
		{"otp path listed", "aws ssm get-parameter --name /km/sandbox/x/otp/tok", []string{"/km/sandbox/x/otp/tok"}, false},
		{"dynamodb anything", "aws dynamodb scan --table-name km-sandboxes", nil, true},
		{"aws inside a pipeline", "curl x | aws s3 cp - s3://b/k", nil, true},
		{"word aws in text", "echo 'uses aws-cli later'", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &SandboxProfile{}
			p.Spec.Execution.InitCommands = []string{c.cmd}
			p.Spec.IAM.AllowedSecretPaths = []string{"{{prefix}}/wiz/client-id"}
			if c.otp != nil {
				p.Spec.OTP = &OTPSpec{Secrets: c.otp}
			}
			errs := validateInitCommandAWSCalls(p)
			if c.warn && len(errs) == 0 {
				t.Fatalf("expected a warning for %q", c.cmd)
			}
			if !c.warn && len(errs) != 0 {
				t.Fatalf("unexpected warning for %q: %v", c.cmd, errs)
			}
			for _, e := range errs {
				if !e.IsWarning || !strings.HasPrefix(e.Path, "spec.execution.initCommands[") {
					t.Errorf("want a warning at spec.execution.initCommands[i], got %+v", e)
				}
			}
		})
	}
}

func TestValidateInitCommandAWSCalls_CoversInitCommandsAppend(t *testing.T) {
	p := &SandboxProfile{}
	p.Spec.Execution.InitCommandsAppend = []string{"aws dynamodb put-item --table-name t --item {}"}
	errs := validateInitCommandAWSCalls(p)
	if len(errs) != 1 || errs[0].Path != "spec.execution.initCommandsAppend[0]" {
		t.Fatalf("got %+v", errs)
	}
}

func TestValidateInitCommandAWSCalls_VariableIndirectedS3TargetIsNotFlagged(t *testing.T) {
	p := &SandboxProfile{}
	p.Spec.Execution.InitCommands = []string{"SRC=\"s3://${B}/models/x/\"\naws s3 sync \"$SRC\" \"$DEST\" --only-show-errors"}
	if errs := validateInitCommandAWSCalls(p); len(errs) != 0 {
		t.Fatalf("got %+v", errs)
	}
}

func TestValidateInitCommandAWSCalls_PerCommandBoundary(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want int
	}{
		{"granted then ungranted on next line", "aws s3 cp s3://b/models/x /y\naws dynamodb scan --table-name t", 1},
		{"identity then iam", "aws sts get-caller-identity\naws iam list-roles", 1},
		{"ungranted && granted", "aws s3 cp s3://other/k /t && aws s3 cp s3://b/models/y /t", 1},
		{"name= listed", "aws ssm get-parameter --name={{prefix}}/wiz/client-id", 0},
		{"name= unlisted", "aws ssm get-parameter --name=/km/other", 1},
		{"quoted name listed", `aws ssm get-parameter --name "{{prefix}}/wiz/client-id"`, 0},
		{"quoted name unlisted", `aws ssm get-parameter --name "/km/other"`, 1},
		{"continued ungranted bucket", "aws s3 cp \\\n  s3://other-bucket/k /tmp/k", 1},
		{"continued granted prefix", "aws s3 cp \\\n  s3://${KM_ARTIFACTS_BUCKET}/models/x /data", 0},
		{"command substitution", `X=$(aws dynamodb get-item --table-name t --key {})`, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &SandboxProfile{}
			p.Spec.Execution.InitCommands = []string{c.cmd}
			p.Spec.IAM.AllowedSecretPaths = []string{"{{prefix}}/wiz/client-id"}
			if got := len(validateInitCommandAWSCalls(p)); got != c.want {
				t.Fatalf("%q: got %d warnings, want %d", c.cmd, got, c.want)
			}
		})
	}
}
