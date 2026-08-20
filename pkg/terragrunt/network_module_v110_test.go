// Package terragrunt_test — Phase 125 Plan 01: structural regression test over
// infra/modules/network/v1.1.0, the per-AZ NAT gateway module version.
//
// These tests read the module's .tf files (and the live terragrunt unit that
// sources them) as plain text and assert structural invariants with
// strings.Contains / regexp — they do not shell out to terraform, and they
// must run clean on a machine with no terraform binary at all. Running
// terraform inside a module source directory would create the gitignored
// .terraform.lock.hcl / .terraform/ lock-drift artifacts described in
// 125-01-PLAN.md Task 1, so this package deliberately never does that.
package terragrunt_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// natResourceCounters lists the resource type/name pairs in v1.1.0 whose
// count expression must scale with the number of AZs (private subnets)
// rather than being fixed at 1.
var natResourceCounters = []struct {
	resourceType string
	resourceName string
}{
	{"aws_eip", "nat"},
	{"aws_nat_gateway", "nat"},
	{"aws_route", "private_nat_gateway"},
}

// movedBlockPairs lists the singleton-to-indexed transitions that v1.1.0
// must declare via `moved` blocks so the first plan against an existing
// install shows moves, not destroy+create.
var movedBlockPairs = []struct {
	from string
	to   string
}{
	{"aws_route_table.private", "aws_route_table.private[0]"},
	{"aws_eip.nat", "aws_eip.nat[0]"},
	{"aws_nat_gateway.nat", "aws_nat_gateway.nat[0]"},
}

// TestNetworkModuleV110PerAZNAT covers behaviours 1 and 2 from 125-01-PLAN.md
// Task 3: every NAT-class resource (aws_eip.nat, aws_nat_gateway.nat,
// aws_route.private_nat_gateway) counts off length(var.vpc.private_subnets_cidr)
// instead of a fixed 1, and aws_route_table.private is likewise counted so
// private route tables are per-AZ.
func TestNetworkModuleV110PerAZNAT(t *testing.T) {
	t.Parallel()

	mainPath := v110ModulePath(t, "main.tf")
	src := readFileString(t, mainPath)

	t.Run("NATResourcesScaleWithAZCount", func(t *testing.T) {
		for _, r := range natResourceCounters {
			block := extractResourceBlock(t, src, r.resourceType, r.resourceName)
			if !strings.Contains(block, "count") {
				t.Errorf("resource %q %q has no count meta-argument — cannot scale per-AZ", r.resourceType, r.resourceName)
				continue
			}
			if !strings.Contains(block, "length(var.vpc.private_subnets_cidr)") {
				t.Errorf("resource %q %q count does not reference length(var.vpc.private_subnets_cidr): %s", r.resourceType, r.resourceName, block)
			}
		}
	})

	t.Run("PrivateRouteTableIsPerAZ", func(t *testing.T) {
		block := extractResourceBlock(t, src, "aws_route_table", "private")
		if !strings.Contains(block, "count") {
			t.Errorf("aws_route_table.private has no count meta-argument — a single shared route table cannot hold one 0.0.0.0/0 route per AZ")
			return
		}
		if !strings.Contains(block, "length(var.vpc.private_subnets_cidr)") {
			t.Errorf("aws_route_table.private count does not reference length(var.vpc.private_subnets_cidr): %s", block)
		}
	})

	t.Run("NATPlacedInOwnAZPublicSubnet", func(t *testing.T) {
		block := extractResourceBlock(t, src, "aws_nat_gateway", "nat")
		if !strings.Contains(block, "aws_subnet.public_subnet[*].id, count.index") {
			t.Errorf("aws_nat_gateway.nat.subnet_id does not index by count.index — each NAT must sit in the public subnet of the AZ it serves: %s", block)
		}
	})
}

// TestNetworkModuleV110MovedBlocks covers behaviour 3: the three moved blocks
// covering the singleton-to-indexed transitions are present, so the first
// apply against an existing install is a move rather than a destroy+create.
func TestNetworkModuleV110MovedBlocks(t *testing.T) {
	t.Parallel()

	mainPath := v110ModulePath(t, "main.tf")
	src := readFileString(t, mainPath)

	gotCount := strings.Count(src, "moved {")
	wantCount := len(movedBlockPairs)
	if gotCount != wantCount {
		t.Errorf("expected %d moved blocks in main.tf, found %d", wantCount, gotCount)
	}

	for _, p := range movedBlockPairs {
		if !hasMovedBlock(src, p.from, p.to) {
			t.Errorf("missing moved block: from = %s -> to = %s", p.from, p.to)
		}
	}
}

// TestNetworkModuleV110Outputs covers behaviour 4: outputs.tf exposes the
// three new plural outputs, and still exposes the two keys
// infra/live/use1/efs/terragrunt.hcl consumes from this module.
func TestNetworkModuleV110Outputs(t *testing.T) {
	t.Parallel()

	outputsPath := v110ModulePath(t, "outputs.tf")
	src := readFileString(t, outputsPath)

	mustHave := []string{
		`output "private_route_table_ids"`,
		`output "nat_gateway_ids"`,
		`output "nat_eip_public_ips"`,
		`output "vpc_id"`,
		`output "public_subnets"`,
	}
	for _, want := range mustHave {
		if !strings.Contains(src, want) {
			t.Errorf("outputs.tf missing expected output declaration: %s", want)
		}
	}

	// The old singular outputs must be gone in v1.1.0 (v1.0.0 keeps them
	// unchanged) — a future accidental re-add would silently reintroduce a
	// null-on-multi-AZ output. Exact quoted match so the check does not
	// false-positive on the plural forms above.
	mustNotHave := []string{
		`output "private_route_table_id"`,
		`output "nat_gateway_id"`,
		`output "nat_eip_public_ip"`,
	}
	for _, unwanted := range mustNotHave {
		if strings.Contains(src, unwanted) {
			t.Errorf("outputs.tf still contains old singular output %s — v1.1.0 must replace it with the plural form", unwanted)
		}
	}
}

// TestNetworkModuleV110IsAdditive covers behaviour 5: the bump is additive —
// infra/modules/network/v1.0.0 still exists on disk untouched, and the live
// terragrunt unit sources v1.1.0 (not an in-place edit of v1.0.0).
func TestNetworkModuleV110IsAdditive(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)

	v100Dir := filepath.Join(repoRoot, "infra", "modules", "network", "v1.0.0")
	for _, f := range []string{"main.tf", "variables.tf", "outputs.tf"} {
		p := filepath.Join(v100Dir, f)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("infra/modules/network/v1.0.0/%s missing — the v1.1.0 bump must be additive, not an in-place edit: %v", f, err)
		}
	}

	liveUnitPath := filepath.Join(repoRoot, "infra", "live", "use1", "network", "terragrunt.hcl")
	liveSrc := readFileString(t, liveUnitPath)
	if !strings.Contains(liveSrc, "infra/modules/network/v1.1.0") {
		t.Errorf("infra/live/use1/network/terragrunt.hcl does not source infra/modules/network/v1.1.0")
	}
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// v110ModulePath returns the absolute path to a file inside
// infra/modules/network/v1.1.0, resolving the repo root via findRepoRoot
// (shared with modulehygiene_test.go in this package).
func v110ModulePath(t *testing.T, file string) string {
	t.Helper()
	repoRoot := findRepoRoot(t)
	return filepath.Join(repoRoot, "infra", "modules", "network", "v1.1.0", file)
}

// readFileString reads path and returns its contents as a string, failing
// the test on any read error.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(b)
}

// extractResourceBlock returns the full text of a single
// `resource "type" "name" { ... }` block from src, matching braces so that
// nested blocks (e.g. tags = merge(..., { ... })) don't truncate the match
// early. Fails the test if the resource is not found or has no matching
// closing brace.
func extractResourceBlock(t *testing.T, src, resourceType, resourceName string) string {
	t.Helper()

	marker := fmt.Sprintf(`resource "%s" "%s" {`, resourceType, resourceName)
	idx := strings.Index(src, marker)
	if idx < 0 {
		t.Fatalf("resource %q %q not found in module source", resourceType, resourceName)
	}

	openBrace := idx + len(marker) - 1
	depth := 0
	for i := openBrace; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[idx : i+1]
			}
		}
	}
	t.Fatalf("resource %q %q: no matching closing brace found starting at offset %d", resourceType, resourceName, idx)
	return ""
}

// hasMovedBlock reports whether src contains a `moved { from = X to = Y }`
// block for the given from/to addresses, tolerant of whitespace/newline
// layout between the braces and the from/to lines.
func hasMovedBlock(src, from, to string) bool {
	pattern := regexp.MustCompile(
		`moved\s*\{\s*from\s*=\s*` + regexp.QuoteMeta(from) + `\s*to\s*=\s*` + regexp.QuoteMeta(to) + `\s*\}`,
	)
	return pattern.MatchString(src)
}
