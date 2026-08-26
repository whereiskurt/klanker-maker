// Package terragrunt_test — Phase 125 Plan 02: per-substrate module version pin
// regression test.
//
// The sandbox terragrunt template (infra/templates/sandbox/terragrunt.hcl) is
// copied VERBATIM by pkg/terragrunt.CreateSandboxDir for both `km create` and
// the `km destroy` local-directory-missing fallback — it is the single live
// version pin for every new sandbox. Before Phase 125 it hardcoded a single
// shared "/v1.2.0" literal for every substrate, which silently pointed the ECS
// substrate at a nonexistent infra/modules/ecs/v1.2.0 directory (REQ-125-SUBPIN).
//
// TestSubstrateVersionPinResolvesPerSubstrate asserts the template declares a
// substrate-to-version map with the expected pins, and that terraform.source
// interpolates the resolved local rather than a hardcoded version literal.
//
// TestSubstrateVersionPinPointsAtExistingModules is the general invariant: for
// EVERY substrate/version pair declared in the map,
// infra/modules/<substrate>/<version>/ must exist on disk and contain at least
// one .tf file. This is what would have caught the pre-existing ECS break.
package terragrunt_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

const sandboxTemplateRelPath = "infra/templates/sandbox/terragrunt.hcl"

// parseSandboxTemplate reads and parses the sandbox terragrunt template,
// returning its top-level hclsyntax.Body.
func parseSandboxTemplate(t *testing.T, repoRoot string) *hclsyntax.Body {
	t.Helper()
	path := filepath.Join(repoRoot, filepath.FromSlash(sandboxTemplateRelPath))
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, diags := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parse %s: %v", path, diags)
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		t.Fatalf("%s: body is not hclsyntax.Body", path)
	}
	return body
}

// hclLiteralString extracts the literal string value of a static HCL
// expression, whether the parser represented it as a bare LiteralValueExpr
// or (the common case for quoted strings) a TemplateExpr wrapping a single
// LiteralValueExpr part. Returns "", false if expr is not a static string.
func hclLiteralString(expr hclsyntax.Expression) (string, bool) {
	switch e := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		if e.Val.Type().FriendlyName() == "string" {
			return e.Val.AsString(), true
		}
	case *hclsyntax.TemplateExpr:
		if len(e.Parts) == 1 {
			if lit, ok := e.Parts[0].(*hclsyntax.LiteralValueExpr); ok {
				return lit.Val.AsString(), true
			}
		}
	}
	return "", false
}

// hclObjectKeyName extracts the string key name from an object-literal item
// key expression, handling both bare-identifier keys (ec2spot = "v1.3.0")
// and quoted keys ("ec2spot" = "v1.3.0").
func hclObjectKeyName(keyExpr hclsyntax.Expression) (string, bool) {
	keyWrap, isKeyExpr := keyExpr.(*hclsyntax.ObjectConsKeyExpr)
	if !isKeyExpr {
		return hclLiteralString(keyExpr)
	}
	if lit, isLit := hclLiteralString(keyWrap.Wrapped); isLit {
		return lit, true
	}
	// Bare identifier key: Wrapped is a single-traversal ScopeTraversalExpr
	// whose root name IS the key (HCL's "naked identifier as object key" rule).
	if trav, isTrav := keyWrap.Wrapped.(*hclsyntax.ScopeTraversalExpr); isTrav && len(trav.Traversal) == 1 {
		if root, isRoot := trav.Traversal[0].(hcl.TraverseRoot); isRoot {
			return root.Name, true
		}
	}
	return "", false
}

// extractSubstrateVersionMap finds locals.substrate_module_versions in the
// parsed template and returns it as a plain map[string]string. Fails the test
// (via t.Fatal) if the map is missing or any entry is not a static string
// literal — a dynamic/computed pin would defeat the purpose of this test.
func extractSubstrateVersionMap(t *testing.T, body *hclsyntax.Body) map[string]string {
	t.Helper()

	var localsBody *hclsyntax.Body
	for _, block := range body.Blocks {
		if block.Type == "locals" {
			localsBody = block.Body
			break
		}
	}
	if localsBody == nil {
		t.Fatal("no locals block found in sandbox template")
	}

	attr, ok := localsBody.Attributes["substrate_module_versions"]
	if !ok {
		t.Fatal("locals.substrate_module_versions not found — expected a per-substrate version map")
	}
	obj, ok := attr.Expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		t.Fatalf("locals.substrate_module_versions is not an object literal (got %T)", attr.Expr)
	}

	result := map[string]string{}
	for _, item := range obj.Items {
		key, ok := hclObjectKeyName(item.KeyExpr)
		if !ok {
			t.Fatalf("could not resolve a static key name in substrate_module_versions map item %#v", item.KeyExpr)
		}
		val, ok := hclLiteralString(item.ValueExpr)
		if !ok {
			t.Fatalf("substrate_module_versions[%q] value is not a static string literal", key)
		}
		result[key] = val
	}
	return result
}

// TestSubstrateVersionPinResolvesPerSubstrate covers Tests 1, 2, and 4 from
// the plan: ec2spot pins to v1.4.0, ecs pins to v1.0.0, and terraform.source
// interpolates the resolved local (not a bare version literal), so a future
// bump cannot reintroduce the shared-literal bug by editing only the source
// line.
func TestSubstrateVersionPinResolvesPerSubstrate(t *testing.T) {
	repoRoot := findRepoRoot(t)
	body := parseSandboxTemplate(t, repoRoot)
	versions := extractSubstrateVersionMap(t, body)

	if got := versions["ec2spot"]; got != "v1.4.0" {
		t.Errorf("substrate_module_versions[ec2spot] = %q, want v1.4.0", got)
	}
	if got := versions["ecs"]; got != "v1.0.0" {
		t.Errorf("substrate_module_versions[ecs] = %q, want v1.0.0", got)
	}

	var tfBlock *hclsyntax.Block
	for _, block := range body.Blocks {
		if block.Type == "terraform" {
			tfBlock = block
			break
		}
	}
	if tfBlock == nil {
		t.Fatal("no terraform block found in sandbox template")
	}
	srcAttr, ok := tfBlock.Body.Attributes["source"]
	if !ok {
		t.Fatal("terraform.source attribute not found")
	}
	tmpl, ok := srcAttr.Expr.(*hclsyntax.TemplateExpr)
	if !ok {
		t.Fatalf("terraform.source is not a template expression (got %T)", srcAttr.Expr)
	}

	referencesResolvedLocal := false
	for _, part := range tmpl.Parts {
		trav, ok := part.(*hclsyntax.ScopeTraversalExpr)
		if !ok {
			continue
		}
		if len(trav.Traversal) < 2 {
			continue
		}
		root, rOK := trav.Traversal[0].(hcl.TraverseRoot)
		attr, aOK := trav.Traversal[1].(hcl.TraverseAttr)
		if rOK && aOK && root.Name == "local" && attr.Name == "substrate_module_version" {
			referencesResolvedLocal = true
		}
	}
	if !referencesResolvedLocal {
		t.Error("terraform.source does not reference local.substrate_module_version — a bare version literal would silently break per-substrate resolution")
	}

	// Guard against a literal version segment sneaking back into source
	// (e.g. "/v1.2.0") alongside the local reference.
	for _, part := range tmpl.Parts {
		if lit, ok := hclLiteralString(part); ok && strings.Contains(lit, "v1.") {
			t.Errorf("terraform.source contains a hardcoded version literal %q — versions must come only from local.substrate_module_version", lit)
		}
	}
}

// TestSubstrateVersionPinPointsAtExistingModules is the general invariant
// that would have caught the pre-existing ECS break: for EVERY substrate the
// template declares a version for, infra/modules/<substrate>/<version>/ must
// exist on disk and contain at least one .tf file.
func TestSubstrateVersionPinPointsAtExistingModules(t *testing.T) {
	repoRoot := findRepoRoot(t)
	body := parseSandboxTemplate(t, repoRoot)
	versions := extractSubstrateVersionMap(t, body)

	if len(versions) == 0 {
		t.Fatal("substrate_module_versions map is empty")
	}

	for substrate, version := range versions {
		modDir := filepath.Join(repoRoot, "infra", "modules", substrate, version)
		entries, err := os.ReadDir(modDir)
		if err != nil {
			t.Errorf("substrate %q pinned at %q, but infra/modules/%s/%s does not exist: %v", substrate, version, substrate, version, err)
			continue
		}
		hasTF := false
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".tf") {
				hasTF = true
				break
			}
		}
		if !hasTF {
			t.Errorf("infra/modules/%s/%s exists but contains no .tf files", substrate, version)
		}
	}
}
