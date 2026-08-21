// Package terragrunt_test — structural regression tests over the ttl-handler
// module's teardown IAM policy.
//
// These read infra/modules/ttl-handler/v1.0.0/main.tf as plain text and assert
// that every resource family the Lambda's SDK cleanup tries to delete is
// actually granted. They never shell out to terraform (see
// network_module_v110_test.go for why that matters).
//
// Why this test exists: cleanupGitHubToken in cmd/ttl-handler/main.go deletes
// the github-token submodule's schedule, Lambda and IAM roles, and logs every
// failure as NON-FATAL. When the policy did not cover those ARNs, every call
// 403'd, the teardown silently left live resources behind, and the only trace
// was a warn line in CloudWatch. The Go code and the IAM grant have to be kept
// in lockstep, and nothing else checks that.
package terragrunt_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readTTLHandlerMainTF(t *testing.T) string {
	t.Helper()
	root := findRepoRoot(t)
	path := filepath.Join(root, "infra", "modules", "ttl-handler", "v1.0.0", "main.tf")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// Each entry is an ARN fragment that must appear in the teardown policy because
// cmd/ttl-handler/main.go's cleanup functions delete a resource matching it.
var teardownGrants = []struct {
	name     string
	fragment string
	why      string
}{
	{
		name:     "github-token lambda",
		fragment: "${var.resource_prefix}-github-token-refresher-*",
		why:      "cleanupGitHubToken deletes the {prefix}-github-token-refresher-{id} function",
	},
	{
		name:     "github-token schedule",
		fragment: "schedule/default/${var.resource_prefix}-github-token-*",
		why:      "cleanupGitHubToken deletes the {prefix}-github-token-{id} schedule",
	},
	{
		name:     "github-token scheduler role",
		fragment: "role/${var.resource_prefix}-github-token-scheduler-*",
		why:      "cleanupGitHubToken deletes the {prefix}-github-token-scheduler-{id} role",
	},
	{
		name:     "github-token refresher role",
		fragment: "role/${var.resource_prefix}-github-token-refresher-*",
		why:      "cleanupGitHubToken deletes the {prefix}-github-token-refresher-{id} role",
	},
}

func TestTTLHandlerPolicy_CoversGitHubTokenTeardown(t *testing.T) {
	body := readTTLHandlerMainTF(t)

	for _, g := range teardownGrants {
		t.Run(g.name, func(t *testing.T) {
			if !strings.Contains(body, g.fragment) {
				t.Errorf("ttl-handler teardown policy is missing %q\n  %s\n"+
					"  Without it the Lambda 403s and logs the failure as non-fatal, "+
					"leaving the resource live after km destroy.", g.fragment, g.why)
			}
		})
	}
}

// The budget-enforcer grants are the working reference the github-token ones
// were modelled on. If these ever disappear the test above would still pass
// while teardown regressed, so pin them too.
func TestTTLHandlerPolicy_StillCoversBudgetEnforcerTeardown(t *testing.T) {
	body := readTTLHandlerMainTF(t)

	for _, fragment := range []string{
		"${var.resource_prefix}-budget-enforcer-*",
		"schedule/default/${var.resource_prefix}-budget-*",
	} {
		if !strings.Contains(body, fragment) {
			t.Errorf("ttl-handler teardown policy lost the budget-enforcer grant %q", fragment)
		}
	}
}

// The TTL handler deletes the sandbox's SOPS bundle from the artifacts bucket.
// The artifacts policy granted Get/Put/List but not Delete, so that call 403'd
// on every teardown and fell back to the bucket lifecycle rule.
func TestTTLHandlerPolicy_CanDeleteFromArtifactsBucket(t *testing.T) {
	body := readTTLHandlerMainTF(t)

	idx := strings.Index(body, `"${var.resource_prefix}-ttl-handler-s3"`)
	if idx < 0 {
		t.Fatal("could not find the ttl-handler-s3 policy resource")
	}
	// Scope the search to that policy block so a DeleteObject grant on the
	// STATE bucket elsewhere in the file cannot satisfy this assertion.
	end := strings.Index(body[idx:], "\n}\n")
	if end < 0 {
		t.Fatal("could not find the end of the ttl-handler-s3 policy resource")
	}
	block := body[idx : idx+end]

	if !strings.Contains(block, "s3:DeleteObject") {
		t.Error("the ttl-handler artifacts policy cannot delete objects, so the " +
			"per-sandbox SOPS bundle is left behind on every teardown")
	}
}
