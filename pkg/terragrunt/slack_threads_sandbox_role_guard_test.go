package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// The Slack bridge's outbound authorizer (threadRoutedToSender in
// pkg/slack/bridge/handler.go) trusts a km-slack-threads row as proof that a
// sandbox may post into a thread. That trust holds only if nothing but
// bridge-side components can write the threads tables. Since 2026-10-18 the
// pollers keep their session state in local files on the box and record agent
// sessions through the signed `km-slack session` bridge action, so threads rows
// are born and updated only in the bridge (events handler, create-handler, the
// session action) and the sandbox role needs no grant on any threads table.
//
// The guard reads the PINNED ec2spot module only (older versions stay on disk
// as history) and fails on any Allow naming a threads table and on DynamoDB
// writes against a wildcard resource.

var (
	tfCommentRe      = regexp.MustCompile(`(?m)^\s*(#|//).*$`)
	tfActionKeyRe    = regexp.MustCompile(`\b(Not)?Action\s*=`)
	tfResourceDeclRe = regexp.MustCompile(`resource\s+"aws_iam_[a-z_]*policy[a-z_]*"\s+"([A-Za-z0-9_]+)"`)
	threadsTableRe   = regexp.MustCompile(`(?i)(slack|github|h1)[-_]threads|table/[^"]*threads`)
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

// pinnedEC2SpotModuleDir returns the repo-relative directory of the ec2spot
// module version sandboxes render against today. Older versions stay on disk
// as history and are deliberately out of scope: the invariant is about the
// role a NEW sandbox gets.
func pinnedEC2SpotModuleDir(t *testing.T) string {
	t.Helper()
	versions := extractSubstrateVersionMap(t, parseSandboxTemplate(t, findRepoRoot(t)))
	pin, ok := versions["ec2spot"]
	if !ok {
		t.Fatal("sandbox template has no ec2spot pin")
	}
	return "infra/modules/ec2spot/" + pin
}

// TestSandboxRole_HasNoGrantOnThreadsTables — the original invariant, true
// again since the pollers moved their session state to local files and the
// km-slack session bridge action (2026-10-18): no Allow statement in the pinned
// ec2spot module may name a threads table, and none may grant DynamoDB writes
// on a wildcard resource.
func TestSandboxRole_HasNoGrantOnThreadsTables(t *testing.T) {
	dir := filepath.Join(findRepoRoot(t), filepath.FromSlash(pinnedEC2SpotModuleDir(t)))
	files, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
	if len(files) == 0 {
		t.Fatalf("no .tf under %s", dir)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := tfCommentRe.ReplaceAllString(string(raw), "")
		rel, _ := filepath.Rel(findRepoRoot(t), f)
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
			if threadsTableRe.MatchString(stmt) {
				t.Errorf("%s (policy %q): an Allow names a threads table; the sandbox role must have none (session state is local + km-slack session)", rel, owningPolicy(s, a))
			}
			actions := stmtField(stmt, "Action") + stmtField(stmt, "NotAction")
			resources := stmtField(stmt, "Resource") + stmtField(stmt, "NotResource")
			if ddbWriteRe.MatchString(actions) && wildcardResRe.MatchString(resources) {
				t.Errorf("%s (policy %q): DynamoDB write on a wildcard resource reaches the threads tables", rel, owningPolicy(s, a))
			}
		}
	}
}
