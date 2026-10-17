package compiler

import (
	"regexp"
	"strings"
	"testing"
)

// The sandbox role's threads-table grants (ec2spot v1.8.0) carry a
// dynamodb:Attributes condition. That condition is only enforceable when every
// UpdateItem pins ReturnValues and every GetItem names its attributes, and it
// only closes the forged-row hole if no update ever names sandbox_id. These
// tests read the raw template so every poller (Slack, GitHub, H1, and any
// future one) is covered without an edit.
func threadsTableCalls(t *testing.T, verb string) []string {
	t.Helper()
	var out []string
	rest := userDataTemplate
	for {
		i := strings.Index(rest, "aws dynamodb "+verb)
		if i < 0 {
			break
		}
		block := rest[i:]
		end := strings.Index(block, "\n\n")
		if end > 0 {
			block = block[:end]
		}
		if strings.Contains(block, "THREADS_TABLE") {
			out = append(out, block)
		}
		rest = rest[i+len("aws dynamodb "+verb):]
	}
	min := map[string]int{"get-item": 3, "update-item": 5}[verb]
	if len(out) < min {
		t.Fatalf("found %d `aws dynamodb %s` calls against a *THREADS_TABLE, want >= %d (a renamed table variable would drop calls out of scope)", len(out), verb, min)
	}
	return out
}

// allowedThreadsAttrs mirrors the dynamodb:Attributes condition in
// infra/modules/ec2spot/v1.8.0/main.tf, keyed by the table variable.
var allowedThreadsAttrs = map[string]map[string]bool{
	"$THREADS_TABLE": setOf("channel_id", "thread_ts", "claude_session_id", "agent_type",
		"last_assistant_msg", "last_turn_ts", "last_processed_event_ts", "ttl_expiry"),
	"$GITHUB_THREADS_TABLE": setOf("repo", "number", "agent_session_id", "agent_type"),
	"$H1_THREADS_TABLE":     setOf("report_id", "target", "agent_session_id", "agent_type"),
}

func setOf(v ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range v {
		m[x] = true
	}
	return m
}

func tableVarOf(block string) string {
	switch {
	case strings.Contains(block, "$GITHUB_THREADS_TABLE"):
		return "$GITHUB_THREADS_TABLE"
	case strings.Contains(block, "$H1_THREADS_TABLE"):
		return "$H1_THREADS_TABLE"
	}
	return "$THREADS_TABLE"
}

var keyNameRe = regexp.MustCompile(`\\"(\w+)\\":\{\\"[SN]\\"`)

func flagValue(block, flag string) (string, bool) {
	for _, line := range strings.Split(block, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, flag+" ") {
			return strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(l, flag+" "), "\\")), true
		}
	}
	return "", false
}

// attrsOf returns every attribute name the call's key, expression and
// projection mention. Shell variables are values and never appear here.
func attrsOf(block string) []string {
	var names []string
	if v, ok := flagValue(block, "--key"); ok {
		for _, m := range keyNameRe.FindAllStringSubmatch(v, -1) {
			names = append(names, m[1])
		}
	}
	if v, ok := flagValue(block, "--projection-expression"); ok {
		for _, p := range strings.Split(strings.Trim(v, `"`), ",") {
			names = append(names, strings.TrimSpace(p))
		}
	}
	if v, ok := flagValue(block, "--update-expression"); ok {
		v = strings.Trim(v, `"`)
		for _, kw := range []string{"SET", "REMOVE"} {
			if !strings.HasPrefix(v, kw+" ") {
				continue
			}
			for _, p := range strings.Split(strings.TrimPrefix(v, kw+" "), ",") {
				names = append(names, strings.TrimSpace(strings.SplitN(p, " = ", 2)[0]))
			}
		}
	}
	return names
}

func TestThreadsTableCalls_OnlyTouchAttributesTheRoleAllows(t *testing.T) {
	for _, verb := range []string{"get-item", "update-item"} {
		for _, call := range threadsTableCalls(t, verb) {
			tv := tableVarOf(call)
			names := attrsOf(call)
			if len(names) == 0 {
				t.Errorf("extracted no attribute names from a %s call (parser out of date?):\n%s", verb, call)
			}
			for _, n := range names {
				if !allowedThreadsAttrs[tv][n] {
					t.Errorf("%s on %s names attribute %q, outside the role's dynamodb:Attributes set", verb, tv, n)
				}
			}
		}
	}
}

func TestThreadsTableUpdates_FitTheIAMCondition(t *testing.T) {
	for _, call := range threadsTableCalls(t, "update-item") {
		if strings.Contains(call, "sandbox_id") {
			t.Errorf("threads-table update names sandbox_id:\n%s", call)
		}
		if !strings.Contains(call, "--return-values NONE") {
			t.Errorf("threads-table update lacks --return-values NONE:\n%s", call)
		}
	}
}

func TestThreadsTableReads_ProjectOnlyOwnedAttributes(t *testing.T) {
	for _, call := range threadsTableCalls(t, "get-item") {
		if !strings.Contains(call, "--projection-expression") {
			t.Errorf("threads-table get-item has no --projection-expression (Select=SPECIFIC_ATTRIBUTES condition):\n%s", call)
		}
		if strings.Contains(call, "sandbox_id") {
			t.Errorf("threads-table get-item projects sandbox_id, which is not in the role's attribute set:\n%s", call)
		}
	}
}
