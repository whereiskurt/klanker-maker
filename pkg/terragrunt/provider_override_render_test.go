// Package terragrunt_test — Phase 126 Plan 02: end-to-end render regression
// guard for the sandbox template's cross-account provider override
// (REQ-126-LAUNCH).
//
// Unlike provider_override_test.go (pure Go, always runs), this file shells
// out to the real terragrunt binary and is gated on it being present on
// PATH — it t.Skips when absent, naming the pinned version the behaviour was
// verified against and noting the structural guard in
// provider_override_test.go still ran regardless.
//
// It automates the last two RESEARCH.md Pattern 1 fixtures (variant 2: the
// real sandbox shape with launch_account both set and empty) by building
// real temp sandbox directories (via the same pkg/terragrunt.CreateSandboxDir
// / PopulateSandboxDir the production `km create` path uses) plus a
// root-only unit with no child generate block at all, and comparing their
// `terragrunt render --json` output.
package terragrunt_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/terragrunt"
)

// renderTestRegionLabel matches an existing infra/live/<region>/ directory so
// find_in_parent_folders("root.hcl") and read_terragrunt_config(site.hcl)
// resolve exactly as they would for a real sandbox.
const renderTestRegionLabel = "use1"

// terragruntRenderResult is the subset of `terragrunt render --json` output
// this test cares about — the generate-block map, keyed by block label.
type terragruntRenderResult struct {
	Generate map[string]struct {
		Contents string `json:"contents"`
	} `json:"generate"`
}

// runTerragruntRenderJSON runs `terragrunt render --json --non-interactive
// --working-dir dir` and parses the result. Fails the test (with stderr
// attached) on a non-zero exit or unparseable output — a duplicate-generate
// collision surfaces exactly this way.
func runTerragruntRenderJSON(t *testing.T, terragruntPath, dir string) terragruntRenderResult {
	t.Helper()

	cmd := exec.Command(terragruntPath, "render", "--json", "--non-interactive", "--working-dir", dir) // #nosec G204 -- terragruntPath resolved via exec.LookPath, dir is a test-owned temp path
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("terragrunt render --json --working-dir %s failed: %v\n--- stderr ---\n%s", dir, err, stderr.String())
	}

	var result terragruntRenderResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("parse terragrunt render --json output for %s: %v\n--- stdout ---\n%s", dir, err, stdout.String())
	}
	return result
}

// TestSandboxProviderRender is the end-to-end render regression guard. It
// proves, against the real pinned terragrunt binary:
//
//  1. a root-only unit (no child generate block) renders cleanly;
//  2. the sandbox template WITHOUT a launch_account local renders a
//     generate.provider.contents that is byte-identical to the root-only
//     unit's — the dormancy byte-identity guarantee;
//  3. the sandbox template WITH all three launch-account locals set renders
//     an assume_role block carrying the synthetic role_arn/external_id
//     values, and still carries the provider version pins;
//  4. neither render returns a terragrunt error (a duplicate-generate
//     collision surfaces as a non-zero exit naming the colliding label).
func TestSandboxProviderRender(t *testing.T) {
	terragruntPath, err := exec.LookPath("terragrunt")
	if err != nil {
		t.Skip("terragrunt binary not found on PATH — skipping end-to-end render test " +
			"(the structural guard in provider_override_test.go still ran unconditionally). " +
			"This repo pins terragrunt v0.99.1 (.goreleaser.yaml); install that version to " +
			"run this test locally.")
	}

	verOut, verErr := exec.Command(terragruntPath, "--version").CombinedOutput() // #nosec G204 -- terragruntPath resolved via exec.LookPath
	if verErr == nil {
		t.Logf("terragrunt --version: %s", strings.TrimSpace(string(verOut)))
	}

	repoRoot := findRepoRoot(t)

	// --- Fixture 1: root-only unit (no child generate block at all) ---
	// Baseline for the dormancy byte-identity assertion: this is exactly
	// what root.hcl alone generates, with nothing overriding it.
	rootOnlyDir := filepath.Join(repoRoot, "infra", "live", renderTestRegionLabel, "sandboxes", "rendertest-126-02-rootonly")
	if err := os.MkdirAll(rootOnlyDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rootOnlyDir, err)
	}
	defer func() { _ = os.RemoveAll(rootOnlyDir) }()

	rootOnlyHCL := "include \"root\" {\n  path = find_in_parent_folders(\"root.hcl\")\n}\n\ninputs = {}\n"
	if err := os.WriteFile(filepath.Join(rootOnlyDir, "terragrunt.hcl"), []byte(rootOnlyHCL), 0o644); err != nil {
		t.Fatalf("write root-only terragrunt.hcl: %v", err)
	}

	// --- Fixture 2: the real sandbox template WITHOUT a launch_account local ---
	noAccountID := "rendertest-126-02-noaccount"
	noAccountDir, err := terragrunt.CreateSandboxDir(repoRoot, renderTestRegionLabel, noAccountID)
	if err != nil {
		t.Fatalf("CreateSandboxDir (no-account): %v", err)
	}
	defer func() { _ = terragrunt.CleanupSandboxDir(noAccountDir) }()

	noAccountSvcHCL := fmt.Sprintf(`locals {
  sandbox_id       = %q
  region_label     = %q
  region_full      = "us-east-1"
  substrate_module = "ec2spot"
  module_inputs    = {}
}
`, noAccountID, renderTestRegionLabel)
	if err := terragrunt.PopulateSandboxDir(noAccountDir, noAccountSvcHCL, ""); err != nil {
		t.Fatalf("PopulateSandboxDir (no-account): %v", err)
	}

	// --- Fixture 3: the real sandbox template WITH all three launch-account locals ---
	withAccountID := "rendertest-126-02-withaccount"
	withAccountDir, err := terragrunt.CreateSandboxDir(repoRoot, renderTestRegionLabel, withAccountID)
	if err != nil {
		t.Fatalf("CreateSandboxDir (with-account): %v", err)
	}
	defer func() { _ = terragrunt.CleanupSandboxDir(withAccountDir) }()

	const synthLaunchAccount = "test-launch-account"
	const synthRoleARN = "arn:aws:iam::999988887777:role/test-gpu-launcher"
	const synthExternalID = "test-external-id-9f3E"
	withAccountSvcHCL := fmt.Sprintf(`locals {
  sandbox_id           = %q
  region_label         = %q
  region_full          = "us-east-1"
  substrate_module     = "ec2spot"
  launch_account       = %q
  launcher_role_arn    = %q
  launcher_external_id = %q
  module_inputs        = {}
}
`, withAccountID, renderTestRegionLabel, synthLaunchAccount, synthRoleARN, synthExternalID)
	if err := terragrunt.PopulateSandboxDir(withAccountDir, withAccountSvcHCL, ""); err != nil {
		t.Fatalf("PopulateSandboxDir (with-account): %v", err)
	}

	// --- Render all three ---
	rootOnlyResult := runTerragruntRenderJSON(t, terragruntPath, rootOnlyDir)
	noAccountResult := runTerragruntRenderJSON(t, terragruntPath, noAccountDir)
	withAccountResult := runTerragruntRenderJSON(t, terragruntPath, withAccountDir)

	rootOnlyContents := rootOnlyResult.Generate["provider"].Contents
	noAccountContents := noAccountResult.Generate["provider"].Contents
	withAccountContents := withAccountResult.Generate["provider"].Contents

	if rootOnlyContents == "" {
		t.Fatal("root-only render produced an empty generate.provider.contents — fixture or terragrunt render --json schema drifted")
	}

	// Dormancy byte-identity: the no-launch-account render must equal root's
	// own output exactly — this is the strongest form of the assertion,
	// expressed against root's own live output rather than a hardcoded
	// expected string that would drift.
	if noAccountContents != rootOnlyContents {
		t.Errorf("no-launch-account render's generate.provider.contents is NOT byte-identical to the root-only render's.\n--- no-account (%d bytes) ---\n%s\n--- root-only (%d bytes) ---\n%s",
			len(noAccountContents), noAccountContents, len(rootOnlyContents), rootOnlyContents)
	}

	// Active render: assume_role present with the synthetic values, and the
	// provider version pins are still intact (deep merge didn't drop them).
	for _, want := range []string{
		"assume_role",
		synthRoleARN,
		synthExternalID,
		`version = "6.46.0"`,
		`version = "4.3.0"`,
	} {
		if !strings.Contains(withAccountContents, want) {
			t.Errorf("with-launch-account render's generate.provider.contents is missing %q:\n%s", want, withAccountContents)
		}
	}
}
