package compiler

import (
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
	if len(out) == 0 {
		t.Fatalf("no `aws dynamodb %s` against a *THREADS_TABLE found in the template", verb)
	}
	return out
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
