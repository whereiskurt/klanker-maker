package terragrunt_test

import (
	"regexp"
	"strings"
	"testing"

	slackbridge "github.com/whereiskurt/klanker-maker/pkg/slack/bridge"
)

// The channel route table reaches the bridge as SSM /{prefix}/slack/routes.
// Three things must agree: the env var naming the path, the IAM grant on
// exactly that path, and the hash env var fed from get_env by the live unit.
func TestSlackBridgeModule_ChannelRoutesParamEnvIAMAndHash(t *testing.T) {
	main := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/main.tf")
	vars := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/variables.tf")
	live := repoFile(t, "infra/live/use1/lambda-slack-bridge/terragrunt.hcl")

	// Derived from the Go writer's own path function, so the parameter km init
	// writes and the ARN the bridge is granted cannot drift apart.
	path := slackbridge.RoutesParamPath("${var.resource_prefix}")
	if !regexp.MustCompile(`KM_SLACK_ROUTES_PARAM\s*=\s*"` + regexp.QuoteMeta(path) + `"`).MatchString(main) {
		t.Errorf("main.tf must set KM_SLACK_ROUTES_PARAM = %q", path)
	}
	if !regexp.MustCompile(`KM_SLACK_ROUTES_SHA256\s*=\s*var\.slack_routes_sha256`).MatchString(main) {
		t.Error("main.tf must set KM_SLACK_ROUTES_SHA256 = var.slack_routes_sha256")
	}
	if !strings.Contains(vars, `variable "slack_routes_sha256"`) {
		t.Error("variables.tf missing slack_routes_sha256")
	}
	if !regexp.MustCompile(`slack_routes_sha256\s+=\s+get_env\("KM_SLACK_ROUTES_SHA256", ""\)`).MatchString(live) {
		t.Error("live terragrunt.hcl must feed slack_routes_sha256 from get_env(\"KM_SLACK_ROUTES_SHA256\", \"\")")
	}
	i := strings.Index(main, `Sid    = "SSMChannelRoutes"`)
	if i == -1 {
		t.Fatal("SSMChannelRoutes statement not found")
	}
	stmt := main[i:]
	stmt = stmt[:strings.Index(stmt, "}\n")]
	if !strings.Contains(stmt, `"ssm:GetParameter"`) {
		t.Error("SSMChannelRoutes needs ssm:GetParameter")
	}
	if !strings.Contains(stmt, ":parameter"+path) {
		t.Errorf("SSMChannelRoutes must name exactly parameter%s", path)
	}
	if strings.Contains(stmt, "*") {
		t.Error("SSMChannelRoutes must be an exact ARN, no wildcard")
	}
}
