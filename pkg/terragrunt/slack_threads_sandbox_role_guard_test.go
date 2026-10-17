package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The Slack bridge's outbound authorizer (threadRoutedToSender in
// pkg/slack/bridge/handler.go) still trusts a km-slack-threads row as proof
// that a sandbox may post into a thread. What protects it now is that the
// sandbox role can write the threads tables only under a dynamodb:Attributes
// condition naming an attribute set that excludes sandbox_id, so a row the box
// creates has no sandbox_id and LookupSandbox reads it as unrouted.
//
// The guard reads the PINNED ec2spot module only (older versions stay on disk
// as history) and fails on any Allow naming a threads table without that
// condition, on a set containing sandbox_id, and on DynamoDB writes against a
// wildcard resource.

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

// TestSandboxRole_ThreadsWriteGrantExcludesSandboxID replaces the pre-v1.8.0
// "no grant at all" guard. The sandbox role MAY read and update the threads
// tables for session continuity, but only under a dynamodb:Attributes
// condition that names each attribute it may touch, and sandbox_id (the
// bridge's authorization attribute) must not be in that set. Any Allow on a
// threads table without the condition, any set containing sandbox_id, and any
// Allow of DynamoDB writes on a wildcard resource fail the build.
func TestSandboxRole_ThreadsWriteGrantExcludesSandboxID(t *testing.T) {
	dir := filepath.Join(findRepoRoot(t), filepath.FromSlash(pinnedEC2SpotModuleDir(t)))
	files, err := filepath.Glob(filepath.Join(dir, "*.tf"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no .tf files under %s (err=%v)", dir, err)
	}
	sort.Strings(files)
	attrsRe := regexp.MustCompile(`"dynamodb:Attributes"\s*=\s*\[([^\]]*)\]`)
	sawThreads := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(findRepoRoot(t), f)
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
			policy := owningPolicy(s, a)
			actions := stmtField(stmt, "Action") + stmtField(stmt, "NotAction")
			resources := stmtField(stmt, "Resource") + stmtField(stmt, "NotResource")
			if ddbWriteRe.MatchString(actions) && wildcardResRe.MatchString(resources) {
				t.Errorf("%s (policy %q): DynamoDB write on a wildcard resource reaches every threads table", rel, policy)
			}
			if !threadsTableRe.MatchString(stmt) {
				continue
			}
			sawThreads++
			am := attrsRe.FindStringSubmatch(stmt)
			if am == nil {
				t.Errorf("%s (policy %q): an Allow on a threads table has no dynamodb:Attributes condition; a box could write sandbox_id and forge a routed-thread row", rel, policy)
				continue
			}
			if strings.Contains(am[1], `"sandbox_id"`) {
				t.Errorf("%s (policy %q): sandbox_id is in the attribute set; the bridge trusts that attribute to authorize posts", rel, policy)
			}
			// Presence check only: IAM evaluates these keys as StringEqualsIfExists, so
			// their absence on the request side is not something the guard can see.
			if !strings.Contains(stmt, "dynamodb:ReturnValues") || !strings.Contains(stmt, "dynamodb:Select") {
				t.Errorf("%s (policy %q): dynamodb:Attributes needs ReturnValues and Select pinned or the condition is bypassable", rel, policy)
			}
		}
	}
	if sawThreads < 3 {
		t.Fatalf("only %d Allow statements name a threads table, want >= 3 (slack, github, h1); the table regex drifted or a grant was dropped", sawThreads)
	}
}
