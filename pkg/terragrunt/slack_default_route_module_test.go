package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Every KM_SLACK_DEFAULT_ROUTE_* var the bridge reads must be set by the module
// from a variable the live unit feeds from get_env — all three, by name.
func TestSlackBridgeModule_DefaultRouteEnvIsPlumbedEndToEnd(t *testing.T) {
	main := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/main.tf")
	vars := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/variables.tf")
	live := repoFile(t, "infra/live/use1/lambda-slack-bridge/terragrunt.hcl")
	for _, suffix := range []string{"ALIAS", "PROFILE", "ALLOW"} {
		env := "KM_SLACK_DEFAULT_ROUTE_" + suffix
		v := "slack_default_route_" + strings.ToLower(suffix)
		if !regexp.MustCompile(env + `\s*=\s*var\.` + v).MatchString(main) {
			t.Errorf("main.tf must set %s = var.%s", env, v)
		}
		if !strings.Contains(vars, `variable "`+v+`"`) {
			t.Errorf("variables.tf missing %s", v)
		}
		// \s+ because terraform/terragrunt fmt aligns the = of adjacent attributes.
		if !regexp.MustCompile(v + `\s+=\s+get_env\("` + env + `", ""\)`).MatchString(live) {
			t.Errorf("live terragrunt.hcl must feed %s from get_env(%q, \"\")", v, env)
		}
	}
}

func TestSlackBridgeModule_DefaultRouteIAM(t *testing.T) {
	main := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/main.tf")
	if !strings.Contains(main, "/index/alias-index") {
		t.Error("bridge needs dynamodb:Query on the sandboxes alias-index GSI")
	}
	if !strings.Contains(main, `"events:PutEvents"`) {
		t.Error("bridge needs events:PutEvents for cold-create")
	}
	// Part B: MarkDispatched is a conditional UpdateItem on km-slack-threads.
	i := strings.Index(main, `Sid    = "DDBSlackThreads"`)
	if i == -1 {
		t.Fatal("DDBSlackThreads statement not found")
	}
	stmt := main[i:]
	stmt = stmt[:strings.Index(stmt, "Resource")]
	if !strings.Contains(stmt, `"dynamodb:UpdateItem"`) {
		t.Error("DDBSlackThreads needs dynamodb:UpdateItem for last_dispatch_ts (MarkDispatched)")
	}
}

func TestOperatorPolicy_CreateHandlerCanSendToSlackInbound(t *testing.T) {
	main := repoFile(t, "infra/modules/km-operator-policy/v1.0.0/main.tf")
	i := strings.Index(main, `Sid    = "SlackInboundQueueLifecycle"`)
	if i == -1 {
		t.Fatal("SlackInboundQueueLifecycle statement not found")
	}
	stmt := main[i:]
	stmt = stmt[:strings.Index(stmt, "Resource")]
	if !strings.Contains(stmt, `"sqs:SendMessage"`) {
		t.Error("the create-handler drains Slack cold-create envelopes into slack-inbound queues and needs sqs:SendMessage there (GitHub and H1 statements already have it)")
	}
}
