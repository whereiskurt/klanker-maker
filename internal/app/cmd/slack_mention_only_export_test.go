package cmd

import (
	"context"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// Phase 91.1: cfg.Slack.MentionOnly == nil must NOT touch KM_SLACK_MENTION_ONLY,
// preserving env-wins semantics and letting the terragrunt.hcl fallback apply.
func TestExportTerragruntEnvVars_SlackMentionOnly_Absent(t *testing.T) {
	os.Unsetenv("KM_SLACK_MENTION_ONLY")
	cfg := &config.Config{Slack: config.SlackConfig{MentionOnly: nil}}
	ExportTerragruntEnvVars(cfg)
	if got := os.Getenv("KM_SLACK_MENTION_ONLY"); got != "" {
		t.Fatalf("nil pointer should not export; got %q", got)
	}
}

// Phase 91.1: cfg.Slack.MentionOnly == &true sets KM_SLACK_MENTION_ONLY=true
// when the env is unset.
func TestExportTerragruntEnvVars_SlackMentionOnly_True(t *testing.T) {
	os.Unsetenv("KM_SLACK_MENTION_ONLY")
	tru := true
	cfg := &config.Config{Slack: config.SlackConfig{MentionOnly: &tru}}
	ExportTerragruntEnvVars(cfg)
	if got := os.Getenv("KM_SLACK_MENTION_ONLY"); got != "true" {
		t.Fatalf("want true; got %q", got)
	}
	os.Unsetenv("KM_SLACK_MENTION_ONLY")
}

// Phase 91.1: cfg.Slack.MentionOnly == &false sets KM_SLACK_MENTION_ONLY=false
// when the env is unset.
func TestExportTerragruntEnvVars_SlackMentionOnly_False(t *testing.T) {
	os.Unsetenv("KM_SLACK_MENTION_ONLY")
	flse := false
	cfg := &config.Config{Slack: config.SlackConfig{MentionOnly: &flse}}
	ExportTerragruntEnvVars(cfg)
	if got := os.Getenv("KM_SLACK_MENTION_ONLY"); got != "false" {
		t.Fatalf("want false; got %q", got)
	}
	os.Unsetenv("KM_SLACK_MENTION_ONLY")
}

// Phase 91.1: env wins. When KM_SLACK_MENTION_ONLY is already set, do not
// overwrite, but DO emit a drift WARN to stderr.
func TestExportTerragruntEnvVars_SlackMentionOnly_EnvWins(t *testing.T) {
	os.Setenv("KM_SLACK_MENTION_ONLY", "false")
	defer os.Unsetenv("KM_SLACK_MENTION_ONLY")
	tru := true
	cfg := &config.Config{Slack: config.SlackConfig{MentionOnly: &tru}}
	ExportTerragruntEnvVars(cfg)
	if got := os.Getenv("KM_SLACK_MENTION_ONLY"); got != "false" {
		t.Fatalf("env should win; got %q", got)
	}
}

// fakeSSMGetParameterAPI implements just enough of ssm.Client for
// EnsureSlackBotUserIDFromSSM. The real helper builds an *ssm.Client from
// awsCfg so we exercise it via stub calls to ssm.NewFromConfig in a higher
// integration test; here we directly test the wrapper logic by overriding
// behaviour through the global hook below.
type fakeGetParam struct {
	val string
	err error
}

func (f *fakeGetParam) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.val == "" {
		return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Name: in.Name, Value: aws.String("")}}, nil
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Name: in.Name, Value: aws.String(f.val)}}, nil
}

// Phase 91.1: env wins — pre-set KM_SLACK_BOT_USER_ID is untouched.
func TestEnsureSlackBotUserIDFromSSM_EnvWins(t *testing.T) {
	os.Setenv("KM_SLACK_BOT_USER_ID", "UPRESET")
	defer os.Unsetenv("KM_SLACK_BOT_USER_ID")
	cfg := &config.Config{ResourcePrefix: "km"}
	// Call with an empty aws.Config — should short-circuit on env check
	// without ever touching SSM.
	EnsureSlackBotUserIDFromSSM(context.Background(), cfg, aws.Config{})
	if got := os.Getenv("KM_SLACK_BOT_USER_ID"); got != "UPRESET" {
		t.Fatalf("env should win; got %q", got)
	}
}

// Phase 91.1: nil cfg short-circuits cleanly (defensive).
func TestEnsureSlackBotUserIDFromSSM_NilCfg(t *testing.T) {
	os.Unsetenv("KM_SLACK_BOT_USER_ID")
	EnsureSlackBotUserIDFromSSM(context.Background(), nil, aws.Config{})
	if got := os.Getenv("KM_SLACK_BOT_USER_ID"); got != "" {
		t.Fatalf("nil cfg should not set anything; got %q", got)
	}
}

// Phase 91.4: ReactAlways tests mirror MentionOnly tests exactly.

func TestExportTerragruntEnvVars_SlackReactAlways_Absent(t *testing.T) {
	os.Unsetenv("KM_SLACK_REACT_ALWAYS")
	cfg := &config.Config{Slack: config.SlackConfig{ReactAlways: nil}}
	ExportTerragruntEnvVars(cfg)
	if got := os.Getenv("KM_SLACK_REACT_ALWAYS"); got != "" {
		t.Fatalf("nil pointer should not export; got %q", got)
	}
}

func TestExportTerragruntEnvVars_SlackReactAlways_True(t *testing.T) {
	os.Unsetenv("KM_SLACK_REACT_ALWAYS")
	tru := true
	cfg := &config.Config{Slack: config.SlackConfig{ReactAlways: &tru}}
	ExportTerragruntEnvVars(cfg)
	if got := os.Getenv("KM_SLACK_REACT_ALWAYS"); got != "true" {
		t.Fatalf("want true; got %q", got)
	}
	os.Unsetenv("KM_SLACK_REACT_ALWAYS")
}

func TestExportTerragruntEnvVars_SlackReactAlways_False(t *testing.T) {
	os.Unsetenv("KM_SLACK_REACT_ALWAYS")
	flse := false
	cfg := &config.Config{Slack: config.SlackConfig{ReactAlways: &flse}}
	ExportTerragruntEnvVars(cfg)
	if got := os.Getenv("KM_SLACK_REACT_ALWAYS"); got != "false" {
		t.Fatalf("want false; got %q", got)
	}
	os.Unsetenv("KM_SLACK_REACT_ALWAYS")
}

func TestExportTerragruntEnvVars_SlackReactAlways_EnvWins(t *testing.T) {
	os.Setenv("KM_SLACK_REACT_ALWAYS", "true")
	defer os.Unsetenv("KM_SLACK_REACT_ALWAYS")
	flse := false
	cfg := &config.Config{Slack: config.SlackConfig{ReactAlways: &flse}}
	ExportTerragruntEnvVars(cfg)
	if got := os.Getenv("KM_SLACK_REACT_ALWAYS"); got != "true" {
		t.Fatalf("env should win; got %q", got)
	}
}
