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
// from a variable the live unit feeds from get_env — all four, by name.
// ALLOW is the install list; PROFILE_ALLOW the route profile's allowEmails.
func TestSlackBridgeModule_DefaultRouteEnvIsPlumbedEndToEnd(t *testing.T) {
	main := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/main.tf")
	vars := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/variables.tf")
	live := repoFile(t, "infra/live/use1/lambda-slack-bridge/terragrunt.hcl")
	for _, suffix := range []string{"ALIAS", "PROFILE", "ALLOW", "PROFILE_ALLOW"} {
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

// DDBThreadStore.Upsert is an UpdateItem (it fills sandbox_id on a row the
// poller created for a Phase 70 handoff thread), and it has two callers: the
// bridge (DDBSlackThreads) and the create-handler's "Sandbox Ready" anchor row
// (SlackThreadsTableUpsert). Without UpdateItem the create-handler's upsert is
// an AccessDenied that only logs a WARN, and the ready thread loses its row.
func TestThreadsTableUpsertCallersHaveUpdateItem(t *testing.T) {
	cases := []struct{ file, sid string }{
		{"infra/modules/lambda-slack-bridge/v1.0.0/main.tf", "DDBSlackThreads"},
		{"infra/modules/km-operator-policy/v1.0.0/main.tf", "SlackThreadsTableUpsert"},
	}
	for _, c := range cases {
		main := repoFile(t, c.file)
		i := strings.Index(main, `Sid    = "`+c.sid+`"`)
		if i == -1 {
			t.Fatalf("%s: statement %s not found", c.file, c.sid)
		}
		stmt := main[i:]
		stmt = stmt[:strings.Index(stmt, "Resource")]
		if !strings.Contains(stmt, `"dynamodb:UpdateItem"`) {
			t.Errorf("%s %s needs dynamodb:UpdateItem: DDBThreadStore.Upsert is an UpdateItem", c.file, c.sid)
		}
	}
	src := repoFile(t, "pkg/slack/bridge/aws_adapters.go")
	i := strings.Index(src, "func (s *DDBThreadStore) Upsert(")
	if i == -1 {
		t.Fatal("DDBThreadStore.Upsert not found")
	}
	body := src[i:]
	body = body[:strings.Index(body, "\n}\n")]
	if !strings.Contains(body, "s.Client.UpdateItem(") {
		t.Fatal("DDBThreadStore.Upsert no longer calls UpdateItem; revisit this guard")
	}
}

// KM_SLACK_REACT_QUIET_SECONDS must be plumbed module var → env block → live
// get_env with the "180" default, or km init's export is silently dropped and
// the bridge runs on its own default whatever km-config.yaml says.
func TestSlackBridgeModule_ReactQuietSecondsIsPlumbedEndToEnd(t *testing.T) {
	main := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/main.tf")
	vars := repoFile(t, "infra/modules/lambda-slack-bridge/v1.0.0/variables.tf")
	live := repoFile(t, "infra/live/use1/lambda-slack-bridge/terragrunt.hcl")
	if !regexp.MustCompile(`KM_SLACK_REACT_QUIET_SECONDS\s*=\s*var\.slack_react_quiet_seconds`).MatchString(main) {
		t.Error("main.tf must set KM_SLACK_REACT_QUIET_SECONDS = var.slack_react_quiet_seconds")
	}
	i := strings.Index(vars, `variable "slack_react_quiet_seconds"`)
	if i == -1 {
		t.Fatal("variables.tf missing slack_react_quiet_seconds")
	}
	block := vars[i:]
	block = block[:strings.Index(block, "\n}")]
	if !regexp.MustCompile(`default\s*=\s*"180"`).MatchString(block) {
		t.Errorf("slack_react_quiet_seconds must default to \"180\":\n%s", block)
	}
	if !regexp.MustCompile(`slack_react_quiet_seconds\s+=\s+get_env\("KM_SLACK_REACT_QUIET_SECONDS", "180"\)`).MatchString(live) {
		t.Error(`live terragrunt.hcl must feed slack_react_quiet_seconds from get_env("KM_SLACK_REACT_QUIET_SECONDS", "180")`)
	}
}
