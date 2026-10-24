package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const slackBridgeModule = "../../infra/modules/lambda-slack-bridge/v1.0.0/main.tf"

// The bridge's env names the history table and its IAM grants it; the two
// must move together (the ttl-handler drift class, project_ttl_handler_submodule_teardown_iam).
func TestSlackBridgeModule_HistoryTableEnvHasIAMGrant(t *testing.T) {
	b, err := os.ReadFile(slackBridgeModule)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !regexp.MustCompile(`KM_SLACK_HISTORY_TABLE\s*=\s*var\.slack_history_table_name`).MatchString(s) {
		t.Fatal("env block lacks KM_SLACK_HISTORY_TABLE")
	}
	if !strings.Contains(s, "KM_SLACK_HISTORY_QUEUE_URL") {
		t.Fatal("env block lacks KM_SLACK_HISTORY_QUEUE_URL")
	}
	if !regexp.MustCompile(`table/\$\{var\.slack_history_table_name\}`).MatchString(s) {
		t.Fatal("no IAM statement names the history table")
	}
	i := strings.Index(s, "DDBSlackHistory")
	if i < 0 {
		t.Fatal("no DDBSlackHistory statement")
	}
	for _, a := range []string{`"dynamodb:Query"`, `"dynamodb:PutItem"`, `"dynamodb:UpdateItem"`, `"dynamodb:DeleteItem"`, `"dynamodb:BatchWriteItem"`} {
		if !strings.Contains(s[i:], a) {
			t.Errorf("DDBSlackHistory statement lacks %s", a)
		}
	}
	if !strings.Contains(s, `resource "aws_sqs_queue" "history_backfill"`) || !strings.Contains(s, `resource "aws_lambda_event_source_mapping" "history_backfill"`) {
		t.Fatal("backfill queue or event source mapping missing")
	}
	if !strings.Contains(s, `"ReportBatchItemFailures"`) {
		t.Error("event source mapping must enable ReportBatchItemFailures")
	}
	// BackfillStep returns its errors so SQS redelivers; without a redrive
	// policy a job that always fails would loop until retention expires.
	if !strings.Contains(s, `resource "aws_sqs_queue" "history_backfill_dlq"`) {
		t.Fatal("backfill dead-letter queue missing")
	}
	q := s[strings.Index(s, `resource "aws_sqs_queue" "history_backfill" {`):]
	q = q[:strings.Index(q, "\n}\n")]
	if !regexp.MustCompile(`redrive_policy\s*=\s*jsonencode\(\{[^}]*deadLetterTargetArn\s*=\s*aws_sqs_queue\.history_backfill_dlq\[0\]\.arn[^}]*maxReceiveCount\s*=\s*\d+`).MatchString(q) {
		t.Errorf("history_backfill must carry a redrive_policy to history_backfill_dlq with a maxReceiveCount:\n%s", q)
	}
}

// The sandbox role must never read the mirror: only the bridge does.
func TestSandboxRole_HasNoGrantOnHistoryTable(t *testing.T) {
	tmpl, err := os.ReadFile("../../infra/templates/sandbox/terragrunt.hcl")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`ec2spot\s*=\s*"(v[0-9.]+)"`).FindStringSubmatch(string(tmpl))
	if m == nil {
		t.Fatal("no ec2spot pin")
	}
	files, _ := filepath.Glob(filepath.Join("../../infra/modules/ec2spot", m[1], "*.tf"))
	if len(files) == 0 {
		t.Fatalf("no ec2spot %s module files found", m[1])
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "slack-history") {
			t.Fatalf("%s names the slack-history table; the sandbox role has no business there", f)
		}
	}
	// The table module itself exists and keeps PITR off and TTL on expires_at.
	tb, err := os.ReadFile("../../infra/modules/dynamodb-slack-history/v1.0.0/main.tf")
	if err != nil {
		t.Fatal(err)
	}
	ts := string(tb)
	for _, want := range []string{`enabled = false`, `attribute_name = "expires_at"`, `hash_key     = "channel"`, `range_key    = "ts"`} {
		if !strings.Contains(ts, want) {
			t.Errorf("history table module lacks %q", want)
		}
	}
}
