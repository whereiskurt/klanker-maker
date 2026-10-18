package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Slack bridge's `session` action (2026-10-18) records GitHub and H1 agent
// sessions on the pollers' behalf, because ec2spot v1.9.0 gave the sandbox role
// no grant on any threads table. Three things must agree or the action 403s or
// 503s at runtime with nothing else noticing: the module hands each table to
// the Lambda as env, the role may GetItem+UpdateItem that same table, and the
// live unit resolves the table name from the real table unit.
func TestSlackBridgeModule_SessionTablesEnvIAMAndDependency(t *testing.T) {
	root := findRepoRoot(t)
	mainTF := filepath.Join(root, "infra/modules/lambda-slack-bridge/v1.0.0/main.tf")
	raw, err := os.ReadFile(mainTF)
	if err != nil {
		t.Fatal(err)
	}
	tf := tfCommentRe.ReplaceAllString(string(raw), "")

	liveTF := filepath.Join(root, "infra/live/use1/lambda-slack-bridge/terragrunt.hcl")
	liveRaw, err := os.ReadFile(liveTF)
	if err != nil {
		t.Fatal(err)
	}
	live := tfCommentRe.ReplaceAllString(string(liveRaw), "")

	// The session statement: GetItem + UpdateItem over the local ARN list.
	sid := regexp.MustCompile(`Sid\s*=\s*"DDBOtherThreadsSession"`).FindStringIndex(tf)
	if sid == nil {
		t.Fatalf("%s: no DDBOtherThreadsSession statement", mainTF)
	}
	a, b := enclosingBlock(tf, sid[0])
	stmt := tf[a:b]
	for _, act := range []string{`"dynamodb:GetItem"`, `"dynamodb:UpdateItem"`} {
		if !strings.Contains(stmtField(stmt, "Action"), act) {
			t.Errorf("DDBOtherThreadsSession lacks %s", act)
		}
	}
	if !regexp.MustCompile(`^\s*local\.other_threads_table_arns\s*\}?\s*$`).MatchString(stmtField(stmt, "Resource")) {
		t.Errorf("DDBOtherThreadsSession Resource should be local.other_threads_table_arns, got %q", stmtField(stmt, "Resource"))
	}
	lm := regexp.MustCompile(`(?s)other_threads_table_arns\s*=\s*compact\(\[(.*?)\]\)`).FindStringSubmatch(tf)
	if lm == nil {
		t.Fatalf("%s: no other_threads_table_arns local", mainTF)
	}

	for _, c := range []struct{ env, v, dep, unit string }{
		{"KM_GITHUB_THREADS_TABLE", "github_threads_table_name", "github_threads", "dynamodb-github-threads"},
		{"KM_H1_THREADS_TABLE", "h1_threads_table_name", "h1_threads", "dynamodb-h1-threads"},
	} {
		if !regexp.MustCompile(c.env + `\s*=\s*var\.` + c.v + `\b`).MatchString(tf) {
			t.Errorf("env %s is not wired to var.%s", c.env, c.v)
		}
		if !strings.Contains(lm[1], ":table/${var."+c.v+"}") {
			t.Errorf("var.%s has no DynamoDB ARN in other_threads_table_arns", c.v)
		}
		depRe := regexp.MustCompile(`(?s)dependency\s+"` + c.dep + `"\s*\{[^}]*config_path\s*=\s*"\.\./` + c.unit + `"`)
		if !depRe.MatchString(live) {
			t.Errorf("%s: no dependency %q on ../%s", liveTF, c.dep, c.unit)
		}
		inRe := regexp.MustCompile(c.v + `\s*=\s*dependency\.` + c.dep + `\.outputs\.table_name\b`)
		if !inRe.MatchString(live) {
			t.Errorf("%s: input %s is not dependency.%s.outputs.table_name", liveTF, c.v, c.dep)
		}
	}
}
