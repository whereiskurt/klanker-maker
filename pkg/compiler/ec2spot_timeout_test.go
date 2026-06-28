package compiler_test

import (
	"os"
	"strings"
	"testing"
)

// ec2spotModuleDir is the repo-relative path from pkg/compiler to the ec2spot TF module.
const ec2spotModuleDir = "../../infra/modules/ec2spot/v1.2.0"

// TestEC2ServiceHCL_SpotTimeout verifies that:
//  1. The aws_spot_instance_request "ec2spot" resource declares a timeouts block with
//     create = var.spot_create_timeout (Phase 124 bounded waiter).
//  2. variables.tf declares spot_create_timeout with a default of "3m" so a 4-AZ sweep
//     fits within the Lambda 900s budget.
func TestEC2ServiceHCL_SpotTimeout(t *testing.T) {
	mainPath := ec2spotModuleDir + "/main.tf"
	mainData, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", mainPath, err)
	}
	mainContent := string(mainData)

	// Locate the spot instance request block.
	spotMarker := `resource "aws_spot_instance_request" "ec2spot"`
	spotIdx := strings.Index(mainContent, spotMarker)
	if spotIdx < 0 {
		t.Fatalf("cannot find %q in main.tf", spotMarker)
	}

	// Extract the spot block: from the resource declaration to the next top-level
	// resource/data/locals declaration (or end of file). This isolates the block
	// so we don't accidentally match a timeouts block in a different resource.
	afterSpot := mainContent[spotIdx:]
	// Find the next top-level resource/data/output declaration after the spot block.
	nextResourceMarkers := []string{
		"\nresource \"aws_ec2_tag\"",
		"\nresource \"aws_instance\"",
		"\nresource \"aws_ebs_volume\"",
		"\ndata \"",
		"\nlocals {",
		"\noutput \"",
	}
	spotBlockEnd := len(afterSpot)
	for _, marker := range nextResourceMarkers {
		idx := strings.Index(afterSpot[1:], marker) // skip the first char to avoid self-match
		if idx >= 0 && idx+1 < spotBlockEnd {
			spotBlockEnd = idx + 1
		}
	}
	spotBlock := afterSpot[:spotBlockEnd]

	// Assert timeouts block is present inside the spot instance request resource.
	if !strings.Contains(spotBlock, "timeouts {") {
		t.Errorf("aws_spot_instance_request block missing 'timeouts {'\nBlock:\n%s", spotBlock)
	}
	if !strings.Contains(spotBlock, "create = var.spot_create_timeout") {
		t.Errorf("aws_spot_instance_request timeouts block missing 'create = var.spot_create_timeout'\nBlock:\n%s", spotBlock)
	}

	// Assert variables.tf declares spot_create_timeout with default "3m".
	varsPath := ec2spotModuleDir + "/variables.tf"
	varsData, err := os.ReadFile(varsPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", varsPath, err)
	}
	varsContent := string(varsData)

	if !strings.Contains(varsContent, "spot_create_timeout") {
		t.Errorf("variables.tf missing 'spot_create_timeout' variable declaration")
	}
	if !strings.Contains(varsContent, `default     = "3m"`) && !strings.Contains(varsContent, `default = "3m"`) {
		t.Errorf("variables.tf spot_create_timeout missing default = \"3m\"")
	}
}

// TestEC2ServiceHCL_OnDemandNoTimeout verifies that the aws_instance "ec2_ondemand"
// (on-demand) resource does NOT declare a timeouts block. On-demand errors fast on
// InsufficientCapacityException already; a bounded waiter is spot-only (Phase 124,
// 124-RESEARCH Pitfall 7).
func TestEC2ServiceHCL_OnDemandNoTimeout(t *testing.T) {
	mainPath := ec2spotModuleDir + "/main.tf"
	mainData, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v", mainPath, err)
	}
	mainContent := string(mainData)

	// Locate the on-demand instance block.
	onDemandMarker := `resource "aws_instance" "ec2_ondemand"`
	onDemandIdx := strings.Index(mainContent, onDemandMarker)
	if onDemandIdx < 0 {
		t.Fatalf("cannot find %q in main.tf", onDemandMarker)
	}

	// Extract the on-demand block: from the resource declaration to the next
	// top-level resource/data declaration (or end of file).
	afterOnDemand := mainContent[onDemandIdx:]
	nextResourceMarkers := []string{
		"\nresource \"aws_ebs_volume\"",
		"\nresource \"aws_volume_attachment\"",
		"\ndata \"",
		"\nlocals {",
		"\noutput \"",
	}
	onDemandBlockEnd := len(afterOnDemand)
	for _, marker := range nextResourceMarkers {
		idx := strings.Index(afterOnDemand[1:], marker)
		if idx >= 0 && idx+1 < onDemandBlockEnd {
			onDemandBlockEnd = idx + 1
		}
	}
	onDemandBlock := afterOnDemand[:onDemandBlockEnd]

	// Assert no timeouts block in the on-demand resource.
	if strings.Contains(onDemandBlock, "timeouts {") {
		t.Errorf("aws_instance (on-demand) block must NOT have a 'timeouts {' block\nBlock:\n%s", onDemandBlock)
	}
}
