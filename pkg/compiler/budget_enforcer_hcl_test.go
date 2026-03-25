package compiler_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klankrmkr/pkg/compiler"
)

// TestGenerateBudgetEnforcerHCL_EC2 verifies that the EC2 budget enforcer HCL contains
// the expected source path, sandbox_id, substrate, spot_rate, and lambda_zip_path fields.
func TestGenerateBudgetEnforcerHCL_EC2(t *testing.T) {
	hcl, err := compiler.GenerateBudgetEnforcerHCL("sb-ec2test1")
	if err != nil {
		t.Fatalf("GenerateBudgetEnforcerHCL() error = %v", err)
	}

	checks := []string{
		"budget-enforcer/v1.0.0",
		"sb-ec2test1",                    // sandbox_id in state key
		"lambda_zip_path",                // required module input
		"build/budget-enforcer.zip",      // path matches Makefile build-lambdas output
		"budget_table_arn",               // constructed from accounts.application
		"budget_enforcer_inputs",         // reads inputs from service.hcl
		"read_terragrunt_config",         // standard Terragrunt pattern
		"find_in_parent_folders",         // standard Terragrunt include pattern
		"remote_state",                   // S3 backend declaration
		"sandboxes/sb-ec2test1/budget-enforcer/terraform.tfstate", // per-sandbox state key
		"role_arn",                       // IAM role ARN constructed from sandbox_id + region
		"km-ec2spot-ssm",                // role name pattern in constructed ARN
	}
	for _, want := range checks {
		if !strings.Contains(hcl, want) {
			t.Errorf("GenerateBudgetEnforcerHCL() missing %q\nGot:\n%s", want, hcl)
		}
	}
}

// TestGenerateBudgetEnforcerHCL_ECS verifies that the ECS budget enforcer HCL is
// functionally identical (template is substrate-agnostic; substrate comes from
// service.hcl's budget_enforcer_inputs block at apply time).
func TestGenerateBudgetEnforcerHCL_ECS(t *testing.T) {
	hcl, err := compiler.GenerateBudgetEnforcerHCL("sb-ecstest2")
	if err != nil {
		t.Fatalf("GenerateBudgetEnforcerHCL(ECS) error = %v", err)
	}

	// Same template is used regardless of substrate.
	// Substrate is read from service.hcl's budget_enforcer_inputs at apply time.
	checks := []string{
		"budget-enforcer/v1.0.0",
		"sb-ecstest2",
		"budget_enforcer_inputs",
		"build/budget-enforcer.zip",      // path matches Makefile build-lambdas output
		"sandboxes/sb-ecstest2/budget-enforcer/terraform.tfstate",
		"role_arn",                       // IAM role ARN constructed
	}
	for _, want := range checks {
		if !strings.Contains(hcl, want) {
			t.Errorf("GenerateBudgetEnforcerHCL(ECS) missing %q\nGot:\n%s", want, hcl)
		}
	}
}

// TestCompileBudgetEnforcerHCL_WithBudget verifies that Compile() with a budget-enabled
// profile returns a non-empty BudgetEnforcerHCL.
func TestCompileBudgetEnforcerHCL_WithBudget(t *testing.T) {
	p := loadTestProfile(t, "ec2-with-budget.yaml")
	id := "sb-betest01"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(budget enabled) error = %v", err)
	}

	if artifacts.BudgetEnforcerHCL == "" {
		t.Error("Compile() BudgetEnforcerHCL should be non-empty when profile has spec.budget")
	}
	if !strings.Contains(artifacts.BudgetEnforcerHCL, "budget-enforcer/v1.0.0") {
		t.Errorf("BudgetEnforcerHCL missing budget-enforcer/v1.0.0 source\nGot:\n%s", artifacts.BudgetEnforcerHCL)
	}
	if !strings.Contains(artifacts.BudgetEnforcerHCL, id) {
		t.Errorf("BudgetEnforcerHCL missing sandbox_id %q\nGot:\n%s", id, artifacts.BudgetEnforcerHCL)
	}
}

// TestCompileBudgetEnforcerHCL_NoBudget verifies that Compile() with a profile that
// has no spec.budget returns an empty BudgetEnforcerHCL.
func TestCompileBudgetEnforcerHCL_NoBudget(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	id := "sb-nobetest"

	artifacts, err := compiler.Compile(p, id, false, testNetwork())
	if err != nil {
		t.Fatalf("Compile(no budget) error = %v", err)
	}

	if artifacts.BudgetEnforcerHCL != "" {
		t.Errorf("Compile() BudgetEnforcerHCL should be empty when profile has no spec.budget\nGot:\n%s", artifacts.BudgetEnforcerHCL)
	}
}
