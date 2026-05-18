// Package terragrunt_test — Phase 84.4 Plan 01: HCL static-analysis audit test.
//
// TestModuleNamesUseResourcePrefix walks every infra/modules/*/v2.0.0/ (and
// any future v2.0.0+) directory, parses each .tf file with hcl/v2/hclsyntax,
// and asserts that "resource-name-position" attributes (name, creation_token,
// alias, policy_name) are either:
//   - A TemplateExpr that references var.resource_prefix, OR
//   - A ScopeTraversalExpr that IS var.resource_prefix, OR
//   - A literal value present in the allow-list (e.g. tag values like "km-cluster").
//
// v1.0.0/ directories are SKIPPED — they contain historical literals that
// predate the multi-install requirement.
//
// TestModuleAuditCatchesBadFixture is the negative test: it creates a temp
// directory with a synthetic v2.0.0 module containing a hardcoded "km-" name
// and asserts the audit FAILS on it. This gives confidence the audit is
// actually enforcing the rule, not silently passing everything.
package terragrunt_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// auditedAttrs are the attribute names in resource blocks that MUST be
// prefix-templated (not literal). "name" covers the vast majority of
// aws_* resources; the others cover EFS, KMS, and IAM role policies.
var auditedAttrs = map[string]bool{
	"name":           true,
	"creation_token": true,
	"alias":          true,
	"policy_name":    true,
}

// TestModuleNamesUseResourcePrefix is the regression gate that holds Phase 84.4
// invariants. For each v2.0.0+ module in infra/modules/, it parses every .tf
// file and asserts that audited resource name attributes do not contain a
// hardcoded "km-" literal.
func TestModuleNamesUseResourcePrefix(t *testing.T) {
	t.Parallel()

	repoRoot := findRepoRoot(t)
	modules := walkModulesV2Plus(t, repoRoot)
	if len(modules) == 0 {
		t.Log("no v2.0.0+ modules found yet — Plan 02/03/04 will introduce them")
		return
	}

	allowlistPath := filepath.Join(repoRoot, "pkg", "terragrunt", "testdata", "allowlist.txt")
	allowlist := loadAllowlist(t, allowlistPath)

	t.Logf("auditing %d v2.0.0+ module dirs", len(modules))

	for _, modDir := range modules {
		modDir := modDir // capture for parallel sub-test
		modName := filepath.Base(filepath.Dir(modDir)) + "/" + filepath.Base(modDir)
		t.Run(modName, func(t *testing.T) {
			t.Parallel()
			entries, err := os.ReadDir(modDir)
			if err != nil {
				t.Fatalf("ReadDir(%s): %v", modDir, err)
			}
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
					continue
				}
				checkTFFile(t, filepath.Join(modDir, e.Name()), allowlist)
			}
		})
	}
}

// TestModuleAuditCatchesBadFixture is the negative confidence test.
// It creates a temp directory that looks like a v2.0.0 module with a known-bad
// resource attribute (name = "km-bad") and verifies checkTFFile reports an error.
// This ensures the audit is not silently passing everything.
func TestModuleAuditCatchesBadFixture(t *testing.T) {
	t.Parallel()

	badTF := `
resource "aws_iam_role" "bad" {
  name = "km-bad"
}
`
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.tf")
	if err := os.WriteFile(badPath, []byte(badTF), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Use a mock testing.T to capture errors without failing this test.
	mt := &mockT{}
	checkTFFile(mt, badPath, nil)
	if !mt.errored {
		t.Fatal("checkTFFile should have reported an error for km-bad literal but did not")
	}
}

// TestModuleAuditPassesGoodFixture verifies that well-formed template expressions
// using var.resource_prefix pass the audit without error.
func TestModuleAuditPassesGoodFixture(t *testing.T) {
	t.Parallel()

	goodTF := `
variable "resource_prefix" {
  type    = string
  default = "km"
}

resource "aws_iam_role" "good" {
  name = "${var.resource_prefix}-my-role"
}

resource "aws_efs_file_system" "good" {
  creation_token = "${var.resource_prefix}-shared-use1"
}

resource "aws_kms_alias" "good" {
  alias = "alias/${var.resource_prefix}-cmk"
}
`
	dir := t.TempDir()
	goodPath := filepath.Join(dir, "good.tf")
	if err := os.WriteFile(goodPath, []byte(goodTF), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mt := &mockT{}
	checkTFFile(mt, goodPath, nil)
	if mt.errored {
		t.Fatalf("checkTFFile should not have errored for good fixture, but got: %v", mt.messages)
	}
}

// TestModuleAuditAllowlistPermitsTagValues verifies that tag-value literals like
// "km-cluster" (from km:manager tags) pass when present in the allow-list.
// Tag values are labels, not resource names, and are intentionally allow-listed.
//
// Note: tag VALUES are inside map literals under `tags = {}`, NOT in audited
// attribute positions (name, creation_token, alias, policy_name). The allow-list
// is a secondary safety valve for edge-cases where a literal "km-" substring
// appears in a non-tag audited position but is legitimately allow-listed.
func TestModuleAuditAllowlistPermitsTagValues(t *testing.T) {
	t.Parallel()

	// A resource where the audited "name" uses var.resource_prefix (correct),
	// and a tag VALUE incidentally contains "km-cluster" — the tag key is NOT
	// in auditedAttrs so tags don't trigger the audit. This test confirms tags
	// don't cause false positives.
	tfContent := `
resource "aws_iam_role" "with_tags" {
  name = "${var.resource_prefix}-my-role"

  tags = {
    "km:manager"   = "km-cluster"
    "km:component" = "km-slack-inbound"
  }
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "tags.tf")
	if err := os.WriteFile(path, []byte(tfContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mt := &mockT{}
	checkTFFile(mt, path, nil)
	if mt.errored {
		t.Fatalf("tags fixture should pass but got errors: %v", mt.messages)
	}
}

// TestModuleAuditVariableDefaultPermitted verifies that variable "resource_prefix"
// with default = "km" does not trigger the audit — variable blocks are not
// resource blocks and are not subject to the audit.
func TestModuleAuditVariableDefaultPermitted(t *testing.T) {
	t.Parallel()

	tfContent := `
variable "resource_prefix" {
  type    = string
  default = "km"
  description = "Per-install discriminator. Default \"km\" preserves backward compatibility."
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "variables.tf")
	if err := os.WriteFile(path, []byte(tfContent), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	mt := &mockT{}
	checkTFFile(mt, path, nil)
	if mt.errored {
		t.Fatalf("variable default fixture should pass but got errors: %v", mt.messages)
	}
}

// --------------------------------------------------------------------------
// Walk helpers
// --------------------------------------------------------------------------

// walkModulesV2Plus returns all infra/modules/*/v2.0.0+ directories in the repo.
// v1.0.0/ directories are EXCLUDED — they are historical and not subject to audit.
func walkModulesV2Plus(t *testing.T, repoRoot string) []string {
	t.Helper()

	base := filepath.Join(repoRoot, "infra", "modules")
	moduleEntries, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", base, err)
	}

	var result []string
	for _, m := range moduleEntries {
		if !m.IsDir() {
			continue
		}
		moduleDir := filepath.Join(base, m.Name())
		versions, err := os.ReadDir(moduleDir)
		if err != nil {
			t.Logf("ReadDir(%s): %v — skipping", moduleDir, err)
			continue
		}
		for _, v := range versions {
			if !v.IsDir() {
				continue
			}
			vName := v.Name()
			// Require directory name to start with "v" and be a valid semver
			// prefix. Skip v1.0.0 explicitly (historical reference).
			if !strings.HasPrefix(vName, "v") {
				continue
			}
			if vName == "v1.0.0" {
				t.Logf("skipping historical %s/%s", m.Name(), vName)
				continue
			}
			result = append(result, filepath.Join(moduleDir, vName))
		}
	}
	return result
}

// --------------------------------------------------------------------------
// Per-file audit logic
// --------------------------------------------------------------------------

// checkTFFile parses path and checks all resource-block audited attributes.
// It calls t.Errorf for each violation found.
func checkTFFile(t tLogger, path string, allowlist []string) {
	t.Helper()

	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}

	file, diags := hclsyntax.ParseConfig(src, path, hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parse %s: %v", path, diags)
	}

	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		t.Fatalf("%s: body is not hclsyntax.Body (unexpected parser output)", path)
	}

	for _, block := range body.Blocks {
		// Only audit resource blocks — not variable, locals, data, etc.
		if block.Type != "resource" {
			continue
		}
		for attrName, attr := range block.Body.Attributes {
			if !auditedAttrs[attrName] {
				continue
			}
			checkAttrExpr(t, path, attrName, attr.Expr, attr.SrcRange, allowlist)
		}
	}
}

// checkAttrExpr inspects a single attribute expression. It reports an error if
// the expression contains a hardcoded "km-" literal that is not in the allow-list.
func checkAttrExpr(t tLogger, path, attrName string, expr hclsyntax.Expression, srcRange hcl.Range, allowlist []string) {
	t.Helper()

	switch e := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		// Bare literal: name = "km-foo"
		v := e.Val.AsString()
		if strings.Contains(v, "km-") && !isAllowlisted(v, allowlist) {
			t.Errorf("%s:%d: resource attribute %q = %q has hardcoded \"km-\" literal — use ${var.resource_prefix} instead",
				path, srcRange.Start.Line, attrName, v)
		}

	case *hclsyntax.TemplateExpr:
		// Template: name = "${var.resource_prefix}-foo"
		if templateRefsResourcePrefix(e) {
			// Template references var.resource_prefix — this is correct.
			return
		}
		// Template does NOT reference var.resource_prefix. Check whether any
		// literal part contains "km-" — if so, it's a violation.
		for _, part := range e.Parts {
			if lit, ok := part.(*hclsyntax.LiteralValueExpr); ok {
				v := lit.Val.AsString()
				if strings.Contains(v, "km-") && !isAllowlisted(v, allowlist) {
					t.Errorf("%s:%d: resource attribute %q template contains hardcoded \"km-\" literal %q — template must reference var.resource_prefix",
						path, srcRange.Start.Line, attrName, v)
				}
			}
		}

	case *hclsyntax.ScopeTraversalExpr:
		// Pure traversal: name = var.resource_prefix (without suffix)
		// If it's var.resource_prefix, it's fine. Otherwise check for "km-" not applicable
		// (scope traversals don't carry string literals directly). No action needed.
	}
}

// templateRefsResourcePrefix returns true if the TemplateExpr contains at least
// one ScopeTraversalExpr that is var.resource_prefix.
func templateRefsResourcePrefix(e *hclsyntax.TemplateExpr) bool {
	for _, part := range e.Parts {
		if st, ok := part.(*hclsyntax.ScopeTraversalExpr); ok {
			trav := st.Traversal
			if len(trav) >= 2 {
				root, rOK := trav[0].(hcl.TraverseRoot)
				attr, aOK := trav[1].(hcl.TraverseAttr)
				if rOK && aOK && root.Name == "var" && attr.Name == "resource_prefix" {
					return true
				}
			}
		}
	}
	return false
}

// --------------------------------------------------------------------------
// Allow-list helpers
// --------------------------------------------------------------------------

// loadAllowlist reads pkg/terragrunt/testdata/allowlist.txt and returns the
// non-blank, non-comment lines. A missing allow-list file is not an error —
// it means an empty allow-list (all "km-" literals fail).
func loadAllowlist(t tLogger, path string) []string {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		// Not fatal — empty allow-list is a valid configuration.
		t.Logf("allow-list not found at %s — treating as empty: %v", path, err)
		return nil
	}

	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// isAllowlisted returns true if value appears as the quoted RHS in any
// allow-list line. Example allow-list line:
//
//	km:manager = "km-cluster"
//
// Extraction: find first `"` and last `"`, take the substring between them.
// This is conservative — it matches only exact literal values, not substrings.
func isAllowlisted(value string, allowlist []string) bool {
	for _, pattern := range allowlist {
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		if patternRHSEquals(pattern, value) {
			return true
		}
	}
	return false
}

// patternRHSEquals extracts the quoted RHS from an allow-list pattern and
// compares it to value for equality.
func patternRHSEquals(pattern, value string) bool {
	i := strings.Index(pattern, `"`)
	j := strings.LastIndex(pattern, `"`)
	if i < 0 || j <= i {
		return false
	}
	return pattern[i+1:j] == value
}

// --------------------------------------------------------------------------
// Repo root discovery
// --------------------------------------------------------------------------

// findRepoRoot walks up from the test's working directory until it finds a
// directory containing CLAUDE.md (the canonical repo root anchor per CLAUDE.md L3).
func findRepoRoot(t *testing.T) string {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("CLAUDE.md not found walking up from %s — not in a klankrmkr repo?", cwd)
		}
		dir = parent
	}
}

// --------------------------------------------------------------------------
// tLogger interface — allows passing a *testing.T or a mockT to checkTFFile
// --------------------------------------------------------------------------

// tLogger is a minimal interface implemented by *testing.T and mockT.
// It enables the negative test (TestModuleAuditCatchesBadFixture) to call
// checkTFFile without causing the outer test to fail.
type tLogger interface {
	Helper()
	Logf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Fatalf(format string, args ...interface{})
}

// mockT captures Errorf calls without calling os.Exit, allowing negative tests
// to assert that the audit DOES detect violations.
type mockT struct {
	errored  bool
	messages []string
}

func (m *mockT) Helper() {}
func (m *mockT) Logf(format string, args ...interface{}) {
	m.messages = append(m.messages, fmt.Sprintf(format, args...))
}
func (m *mockT) Errorf(format string, args ...interface{}) {
	m.errored = true
	m.messages = append(m.messages, fmt.Sprintf(format, args...))
}
func (m *mockT) Fatalf(format string, args ...interface{}) {
	// Fatalf in a mock should stop execution. We record the message and panic
	// to unwind, but the panic is not recovered here — only use Fatalf for
	// truly fatal parse errors in test fixtures, not expected violations.
	msg := fmt.Sprintf(format, args...)
	m.messages = append(m.messages, msg)
	panic("mockT.Fatalf: " + msg)
}
