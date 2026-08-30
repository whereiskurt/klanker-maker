package compiler_test

import (
	"os"
	"strings"
	"testing"
)

// ec2spotModuleDir is the repo-relative path from pkg/compiler to the ec2spot TF module.
//
// This MUST track local.substrate_module_versions["ec2spot"] in
// infra/templates/sandbox/terragrunt.hcl — the version a sandbox actually renders against.
// It pointed at v1.2.0 from Phase 125 (which bumped the live pin to v1.3.0) until 2026-08-25,
// so every assertion below was being made against a module no sandbox used: the tests passed
// while the live module went unchecked, and a change to v1.3.0 could not fail them.
// TestEC2SpotModuleDir_TracksLivePin now guards that drift directly — and did its job again
// when the live pin moved v1.3.0 -> v1.4.0 (secret_paths IAM grant), same day, and again
// when it moved v1.4.0 -> v1.5.0 (captures/ + learn/ S3 grants).
const ec2spotModuleDir = "../../infra/modules/ec2spot/v1.5.0"

// sandboxTemplatePath is the terragrunt template carrying the authoritative pin.
const sandboxTemplatePath = "../../infra/templates/sandbox/terragrunt.hcl"

// extractResourceBlock returns the text of the named top-level resource block, from its
// declaration up to the next top-level declaration. Isolating the block is what stops a
// timeouts block belonging to a DIFFERENT resource from satisfying an assertion here.
func extractResourceBlock(t *testing.T, content, marker string) string {
	t.Helper()
	idx := strings.Index(content, marker)
	if idx < 0 {
		t.Fatalf("cannot find %q in main.tf", marker)
	}
	after := content[idx:]
	end := len(after)
	// Any top-level declaration terminates the block. Listed generically rather than by
	// specific resource type so that inserting a new resource between blocks cannot
	// silently widen the extracted window.
	for _, m := range []string{"\nresource \"", "\ndata \"", "\nlocals {", "\noutput \"", "\nmodule \""} {
		if i := strings.Index(after[1:], m); i >= 0 && i+1 < end {
			end = i + 1
		}
	}
	return after[:end]
}

// TestEC2SpotModuleDir_TracksLivePin catches the drift that made every other assertion in
// this file inert: a module-version constant here that no longer matches the version sandboxes
// render against. A stale constant does not fail anything — it just quietly stops testing the
// live module, which is strictly worse than having no test at all, because the green result
// reads as coverage.
func TestEC2SpotModuleDir_TracksLivePin(t *testing.T) {
	data, err := os.ReadFile(sandboxTemplatePath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", sandboxTemplatePath, err)
	}
	content := string(data)

	idx := strings.Index(content, "ec2spot = ")
	if idx < 0 {
		t.Fatalf("cannot find the ec2spot pin in %s", sandboxTemplatePath)
	}
	line := content[idx:]
	if nl := strings.Index(line, "\n"); nl >= 0 {
		line = line[:nl]
	}
	// line looks like: ec2spot = "v1.4.0"
	open := strings.Index(line, `"`)
	close := strings.LastIndex(line, `"`)
	if open < 0 || close <= open {
		t.Fatalf("could not parse the ec2spot pin from %q", line)
	}
	pinned := line[open+1 : close]

	if !strings.HasSuffix(ec2spotModuleDir, "/"+pinned) {
		t.Errorf("ec2spotModuleDir (%s) does not match the live pin (%s) in %s — the "+
			"assertions in this file are being made against a module no sandbox renders",
			ec2spotModuleDir, pinned, sandboxTemplatePath)
	}
}

// TestEC2ServiceHCL_SpotTimeout verifies the spot request declares a bounded create waiter
// (Phase 124), and that variables.tf defaults it to 3m so a 4-AZ sweep fits the Lambda 900s
// budget.
func TestEC2ServiceHCL_SpotTimeout(t *testing.T) {
	mainData, err := os.ReadFile(ec2spotModuleDir + "/main.tf")
	if err != nil {
		t.Fatalf("cannot read main.tf: %v", err)
	}
	spotBlock := extractResourceBlock(t, string(mainData), `resource "aws_spot_instance_request" "ec2spot"`)

	if !strings.Contains(spotBlock, "timeouts {") {
		t.Errorf("aws_spot_instance_request block missing 'timeouts {'")
	}
	if !strings.Contains(spotBlock, "create = var.spot_create_timeout") {
		t.Errorf("aws_spot_instance_request timeouts missing 'create = var.spot_create_timeout'")
	}

	varsData, err := os.ReadFile(ec2spotModuleDir + "/variables.tf")
	if err != nil {
		t.Fatalf("cannot read variables.tf: %v", err)
	}
	if !strings.Contains(string(varsData), "spot_create_timeout") {
		t.Errorf("variables.tf missing 'spot_create_timeout' declaration")
	}
}

// TestEC2ServiceHCL_OnDemandBoundedTimeout is the INVERSE of the assertion that stood here
// until 2026-08-25.
//
// The previous test, TestEC2ServiceHCL_OnDemandNoTimeout, required that the on-demand
// resource have NO timeouts block, on the stated rationale that "on-demand errors fast on
// InsufficientCapacityException already" (124-RESEARCH Pitfall 7). That rationale is
// empirically false and the test was codifying the defect:
//
// Observed live 2026-08-24 (126-UAT.md Finding L2) — three consecutive on-demand creates
// against a capacity-dry AZ. CloudTrail shows RunInstances refused with
// Server.InsufficientInstanceCapacity repeatedly over 9+ minutes per attempt; the AWS
// provider retried internally for its 10m default rather than erroring fast. km therefore
// never received the error, capacity.ClassifyError was never reached, sweepDecision was never
// consulted, and the sweep could not rotate — maxAttempts was 4 and the apply never advanced
// past attempt 1. Each remote create was then killed at the Lambda's 900s ceiling mid-apply,
// leaving a stale state lock and an untracked 300GB volume (Finding L5).
//
// It went unnoticed because a cross-account launch is on-demand-ONLY: the launcher role
// grants no ec2:RequestSpotInstances and no ec2:CreateFleet (both verified implicitDeny), so
// the one path able to reach a scarce-capacity account was the one path with no bounded
// waiter.
func TestEC2ServiceHCL_OnDemandBoundedTimeout(t *testing.T) {
	mainData, err := os.ReadFile(ec2spotModuleDir + "/main.tf")
	if err != nil {
		t.Fatalf("cannot read main.tf: %v", err)
	}
	onDemandBlock := extractResourceBlock(t, string(mainData), `resource "aws_instance" "ec2_ondemand"`)

	if !strings.Contains(onDemandBlock, "timeouts {") {
		t.Errorf("aws_instance (on-demand) MUST declare a 'timeouts {' block — without one the " +
			"AWS provider retries InsufficientInstanceCapacity internally and the AZ sweep " +
			"can never rotate off a dry AZ")
	}
	if !strings.Contains(onDemandBlock, "create = var.ondemand_create_timeout") {
		t.Errorf("aws_instance (on-demand) timeouts missing 'create = var.ondemand_create_timeout'")
	}

	varsData, err := os.ReadFile(ec2spotModuleDir + "/variables.tf")
	if err != nil {
		t.Fatalf("cannot read variables.tf: %v", err)
	}
	varsContent := string(varsData)
	if !strings.Contains(varsContent, "ondemand_create_timeout") {
		t.Errorf("variables.tf missing 'ondemand_create_timeout' declaration")
	}

	// Both waiters must stay inside the Lambda budget: 4 AZs x 3m = 12m < 900s. A longer
	// default would reintroduce the timeout-mid-apply failure this fix exists to prevent.
	if !strings.Contains(varsContent, `default     = "3m"`) && !strings.Contains(varsContent, `default = "3m"`) {
		t.Errorf("expected a 3m default for the bounded create waiters so a 4-AZ sweep fits the 900s budget")
	}
}
