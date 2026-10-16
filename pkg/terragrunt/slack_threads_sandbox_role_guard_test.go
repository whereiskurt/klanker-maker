package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
)

// The Slack bridge's outbound authorizer (threadRoutedToSender in
// pkg/slack/bridge/handler.go) trusts a km-slack-threads row as proof that a
// sandbox may post into a thread. That is only sound while NOTHING running on
// the sandbox can write that table: DynamoDB IAM cannot scope a grant by the
// row's sandbox_id attribute, so ANY sandbox-role write grant on the table lets
// every sandbox forge a routed-thread row and post into every thread.
//
// This guard reads every Terraform file of every module whose directory starts
// with "ec2" (the sandbox instance role lives there) and fails on any Allow
// statement that names the threads table, or that grants DynamoDB writes on a
// wildcard resource. It is name-agnostic: a new ec2spot version is covered
// without an edit.
//
// KNOWN EXCEPTION, pinned rather than hidden: aws_iam_role_policy
// "ec2spot_region_lock" is an Allow of Action "*" on Resource "*" conditioned
// on aws:RequestedRegion, created whenever a profile sets iam.allowedRegions
// (profiles/base/platform.yaml does). It DOES reach km-slack-threads. It is
// the open hole recorded in docs/slack-notifications.md § Replies land in the
// originating thread; it is allowlisted here by resource name so that no
// SECOND path can appear silently. Removing it from this list is the goal.
var slackThreadsGuardKnownWildcardPolicies = map[string]bool{
	"ec2spot_region_lock": true,
}

var (
	tfCommentRe      = regexp.MustCompile(`(?m)^\s*(#|//).*$`)
	tfActionKeyRe    = regexp.MustCompile(`\b(Not)?Action\s*=`)
	tfResourceDeclRe = regexp.MustCompile(`resource\s+"aws_iam_[a-z_]*policy[a-z_]*"\s+"([A-Za-z0-9_]+)"`)
	threadsTableRe   = regexp.MustCompile(`(?i)slack[-_]threads|table/[^"]*threads`)
	ddbWriteRe       = regexp.MustCompile(`"(\*|dynamodb:\*|dynamodb:(PutItem|UpdateItem|BatchWriteItem|DeleteItem|PartiQL[A-Za-z]*|TransactWriteItems)[^"]*)"`)
	wildcardResRe    = regexp.MustCompile(`"\*"|:table/[^/"]*\*`) // a "*" inside the TABLE-NAME segment; ".../index/*" on a named table is fine
)

// enclosingBlock returns the innermost {...} containing pos.
func enclosingBlock(s string, pos int) (int, int) {
	depth := 0
	start := -1
	for i := pos; i >= 0; i-- {
		switch s[i] {
		case '}':
			depth++
		case '{':
			if depth == 0 {
				start = i
			} else {
				depth--
			}
		}
		if start != -1 {
			break
		}
	}
	if start == -1 {
		return -1, -1
	}
	depth = 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return start, i + 1
			}
		}
	}
	return start, len(s)
}

var tfStmtKeyRe = regexp.MustCompile(`\b(Sid|Effect|Action|NotAction|Resource|NotResource|Condition|Principal)\s*=`)

// stmtField returns the raw text of one key's value in a statement (from
// after "key =" up to the next statement key), or "" when absent.
func stmtField(stmt string, key string) string {
	locs := tfStmtKeyRe.FindAllStringSubmatchIndex(stmt, -1)
	for i, m := range locs {
		if stmt[m[2]:m[3]] != key {
			continue
		}
		end := len(stmt)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		return stmt[m[1]:end]
	}
	return ""
}

func owningPolicy(s string, pos int) string {
	name := ""
	for _, m := range tfResourceDeclRe.FindAllStringSubmatchIndex(s, -1) {
		if m[0] > pos {
			break
		}
		name = s[m[2]:m[3]]
	}
	return name
}

func TestSandboxRole_HasNoWriteGrantOnSlackThreadsTable(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "infra", "modules", "ec2*", "v*", "*.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no infra/modules/ec2*/v*/*.tf found; the guard is reading the wrong place")
	}
	sort.Strings(files)
	why := "the Slack bridge's outbound authorizer (threadRoutedToSender) trusts km-slack-threads rows; " +
		"DynamoDB IAM cannot scope by sandbox_id, so a sandbox-role grant on that table lets every " +
		"sandbox forge a routed-thread row and post into every thread. Never grant the sandbox role " +
		"access to km-slack-threads."
	sawKnown := false
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := tfCommentRe.ReplaceAllString(string(raw), "")
		seen := map[int]bool{}
		for _, m := range tfActionKeyRe.FindAllStringIndex(s, -1) {
			a, b := enclosingBlock(s, m[0])
			if a == -1 || seen[a] {
				continue
			}
			seen[a] = true
			stmt := s[a:b]
			if regexp.MustCompile(`Effect\s*=\s*"Deny"`).MatchString(stmt) {
				continue
			}
			rel, _ := filepath.Rel(filepath.Join("..", ".."), f)
			policy := owningPolicy(s, a)
			if threadsTableRe.MatchString(stmt) {
				t.Errorf("%s (policy %q): an Allow statement names the slack threads table: %s", rel, policy, why)
				continue
			}
			actions := stmtField(stmt, "Action") + stmtField(stmt, "NotAction")
			resources := stmtField(stmt, "Resource") + stmtField(stmt, "NotResource")
			if ddbWriteRe.MatchString(actions) && wildcardResRe.MatchString(resources) {
				if slackThreadsGuardKnownWildcardPolicies[policy] {
					sawKnown = true
					t.Logf("%s (policy %q): KNOWN open hole — Allow-* reaches km-slack-threads on profiles with iam.allowedRegions", rel, policy)
					continue
				}
				t.Errorf("%s (policy %q): an Allow statement grants DynamoDB writes on a wildcard resource, which reaches km-slack-threads: %s", rel, policy, why)
			}
		}
	}
	if !sawKnown {
		t.Log("the ec2spot_region_lock exception no longer matches anything; remove it from slackThreadsGuardKnownWildcardPolicies")
	}
}
