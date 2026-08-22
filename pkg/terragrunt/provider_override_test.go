// Package terragrunt_test — Phase 126 Plan 02: structural regression guard for
// the sandbox template's cross-account provider override (REQ-126-LAUNCH).
//
// This file is the always-on layer (no external binary, no t.Skip path). It
// parses infra/templates/sandbox/terragrunt.hcl and infra/live/root.hcl with
// hclsyntax and asserts, at the source-structure level:
//
//  1. include "root" declares merge_strategy = "deep" — REQUIRED once the
//     template declares its own generate "provider" block, because a
//     same-labeled generate block in parent and child is a hard error under
//     terragrunt's default (shallow) merge strategy (verified by direct
//     execution against the pinned terragrunt v0.99.1 — see
//     126-RESEARCH.md Pattern 1).
//  2. the template declares exactly one generate block labeled "provider".
//  3. every provider source/version pin present in root.hcl's generate
//     block also appears in the sandbox template's generate block — the
//     guard against the deep merge silently dropping root's version pins.
//  4. the template carries a conditional guard on local.launch_account,
//     and role_arn/external_id are present in the branch that fires when
//     launch_account is set.
//
// Implementation note on (4): the plan's initial design (see 126-PATTERNS.md
// / 126-RESEARCH.md Pattern 1) called for an inline `%{ if local.launch_account
// != "" ~} ... %{ endif ~}` control sequence directly inside the generate
// block's <<- heredoc. Building that shape and rendering it for real against
// the pinned terragrunt v0.99.1 (this plan's own Task 3(B) render test)
// showed it does NOT achieve the dormant byte-identity this plan's
// must_haves.truths requires: the %{ if }/%{ endif } markers interact with
// the heredoc's automatic dedent-margin calculation and either leave stray
// trailing whitespace on the blank line above default_tags, or disable
// dedent for the entire heredoc, depending on exactly which trim markers
// are used. The shipped implementation instead computes the role-assumption
// stanza as a conditional (ternary) local — `local.assume_role_block` —
// outside the heredoc, and interpolates it with a plain `${...}` reference
// inside the heredoc. Plain interpolation does not participate in the
// heredoc's static dedent-margin calculation, so it sidesteps the bug
// entirely; Task 3(B)'s render test proves the resulting dormant output is
// byte-identical to root's own. This test is written against that shipped
// shape: the conditional guard lives in the locals block (not inside the
// generate block's raw contents), and the generate block's raw contents
// reference it by name.
package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

const rootHCLRelPath = "infra/live/root.hcl"

// sliceRaw returns the raw source bytes covered by rng, as a string.
func sliceRaw(src []byte, rng hcl.Range) string {
	return string(src[rng.Start.Byte:rng.End.Byte])
}

// parseHCLFile reads and parses an arbitrary HCL file (relative to repoRoot),
// returning its top-level hclsyntax.Body and the raw source bytes.
func parseHCLFile(t *testing.T, repoRoot, relPath string) (*hclsyntax.Body, []byte) {
	t.Helper()
	path := filepath.Join(repoRoot, filepath.FromSlash(relPath))
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
	return body, src
}

// findBlock returns the first top-level block of the given type (and, if
// labels is non-empty, matching labels) in body. Fails the test if none found.
func findBlock(t *testing.T, body *hclsyntax.Body, blockType string, labels ...string) *hclsyntax.Block {
	t.Helper()
	for _, block := range body.Blocks {
		if block.Type != blockType {
			continue
		}
		if len(labels) == 0 {
			return block
		}
		if len(block.Labels) == len(labels) {
			match := true
			for i, l := range labels {
				if block.Labels[i] != l {
					match = false
					break
				}
			}
			if match {
				return block
			}
		}
	}
	t.Fatalf("no %s block (labels %v) found", blockType, labels)
	return nil
}

// countBlocks returns the number of top-level blocks of the given type
// (and, if labels is non-empty, matching labels) in body.
func countBlocks(body *hclsyntax.Body, blockType string, labels ...string) int {
	count := 0
	for _, block := range body.Blocks {
		if block.Type != blockType {
			continue
		}
		if len(labels) == 0 {
			count++
			continue
		}
		if len(block.Labels) != len(labels) {
			continue
		}
		match := true
		for i, l := range labels {
			if block.Labels[i] != l {
				match = false
				break
			}
		}
		if match {
			count++
		}
	}
	return count
}

// TestSandboxTemplateProviderOverride_MergeStrategyDeep asserts include "root"
// declares merge_strategy = "deep". Without it, terragrunt hard-errors on
// EVERY sandbox render (not only cross-account ones) the instant this
// template declares its own generate "provider" block — a same-labeled
// generate block in parent (root.hcl) and child is a hard error under
// terragrunt's default (shallow) merge strategy, verified by direct
// execution against the pinned terragrunt v0.99.1.
func TestSandboxTemplateProviderOverride_MergeStrategyDeep(t *testing.T) {
	repoRoot := findRepoRoot(t)
	body := parseSandboxTemplate(t, repoRoot)

	includeBlock := findBlock(t, body, "include", "root")
	attr, ok := includeBlock.Body.Attributes["merge_strategy"]
	if !ok {
		t.Fatal(`include "root" has no merge_strategy attribute — a same-labeled ` +
			`generate "provider" block in this template collides with root.hcl's ` +
			`under terragrunt's default (shallow) merge strategy, breaking every ` +
			`sandbox render (verified against pinned terragrunt v0.99.1)`)
	}
	val, ok := hclLiteralString(attr.Expr)
	if !ok || val != "deep" {
		t.Errorf(`include "root".merge_strategy = %q, want "deep"`, val)
	}
}

// TestSandboxTemplateProviderOverride_ExactlyOneGenerateProvider asserts the
// template declares exactly one generate block labeled "provider".
func TestSandboxTemplateProviderOverride_ExactlyOneGenerateProvider(t *testing.T) {
	repoRoot := findRepoRoot(t)
	body := parseSandboxTemplate(t, repoRoot)

	if got := countBlocks(body, "generate", "provider"); got != 1 {
		t.Errorf(`generate "provider" block count = %d, want 1`, got)
	}
}

// providerSourceVersionPairs extracts every source/version pair from a raw
// generate-block contents string (e.g. "source = \"hashicorp/aws\"\n version
// = \"6.46.0\"" -> {"hashicorp/aws": "6.46.0"}).
func providerSourceVersionPairs(raw string) map[string]string {
	re := regexp.MustCompile(`source\s*=\s*"([^"]+)"\s*\n\s*version\s*=\s*"([^"]+)"`)
	matches := re.FindAllStringSubmatch(raw, -1)
	result := make(map[string]string, len(matches))
	for _, m := range matches {
		result[m[1]] = m[2]
	}
	return result
}

// generateProviderContentsRaw returns the raw source text of the generate
// "provider" block's `contents` attribute expression in body, extracted via
// its source range (NOT evaluated) — the contents heredoc contains terragrunt
// template directives / interpolations that are not meaningful to evaluate
// statically.
func generateProviderContentsRaw(t *testing.T, body *hclsyntax.Body, src []byte) string {
	t.Helper()
	genBlock := findBlock(t, body, "generate", "provider")
	attr, ok := genBlock.Body.Attributes["contents"]
	if !ok {
		t.Fatal(`generate "provider" block has no contents attribute`)
	}
	return sliceRaw(src, attr.Expr.Range())
}

// TestSandboxTemplateProviderOverride_VersionPinsPreserved asserts every
// provider source/version pin present in root.hcl's generate block also
// appears in the sandbox template's generate block — the guard against the
// deep merge silently dropping root's version pins (T-126-06). Losing a pin
// here would silently un-pin the AWS/TLS providers for every sandbox.
func TestSandboxTemplateProviderOverride_VersionPinsPreserved(t *testing.T) {
	repoRoot := findRepoRoot(t)

	rootBody, rootSrc := parseHCLFile(t, repoRoot, rootHCLRelPath)
	rootRaw := generateProviderContentsRaw(t, rootBody, rootSrc)
	rootPins := providerSourceVersionPairs(rootRaw)
	if len(rootPins) == 0 {
		t.Fatal("no provider source/version pairs found in root.hcl's generate \"provider\" block — regex or fixture drifted")
	}

	tmplBody := parseSandboxTemplate(t, repoRoot)
	tmplSrc, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(sandboxTemplateRelPath)))
	if err != nil {
		t.Fatalf("read %s: %v", sandboxTemplateRelPath, err)
	}
	tmplRaw := generateProviderContentsRaw(t, tmplBody, tmplSrc)
	tmplPins := providerSourceVersionPairs(tmplRaw)

	for source, version := range rootPins {
		got, ok := tmplPins[source]
		if !ok {
			t.Errorf("sandbox template generate \"provider\" block is missing provider source %q (root.hcl pins it at %q)", source, version)
			continue
		}
		if got != version {
			t.Errorf("sandbox template pins provider %q at %q, but root.hcl pins it at %q — deep merge must reproduce root's pins exactly", source, got, version)
		}
	}
}

// TestSandboxTemplateProviderOverride_LaunchAccountGuard asserts the template
// carries a conditional guard on local.launch_account, and that role_arn and
// external_id are both present in the branch that fires when launch_account
// is set (T-126-08). See the file-level doc comment for why this guard lives
// in the locals block (a conditional local) rather than inline inside the
// generate block's heredoc contents.
func TestSandboxTemplateProviderOverride_LaunchAccountGuard(t *testing.T) {
	repoRoot := findRepoRoot(t)
	body := parseSandboxTemplate(t, repoRoot)
	src, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(sandboxTemplateRelPath)))
	if err != nil {
		t.Fatalf("read %s: %v", sandboxTemplateRelPath, err)
	}

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

	attr, ok := localsBody.Attributes["assume_role_block"]
	if !ok {
		t.Fatal("locals.assume_role_block not found — expected a conditional local guarding role-assumption emission on local.launch_account")
	}

	cond, ok := attr.Expr.(*hclsyntax.ConditionalExpr)
	if !ok {
		t.Fatalf("locals.assume_role_block is not a conditional (ternary) expression (got %T) — expected a guard shaped like local.launch_account != \"\" ? ... : \"\"", attr.Expr)
	}

	condRaw := sliceRaw(src, cond.Condition.Range())
	if !strings.Contains(condRaw, "launch_account") {
		t.Errorf("locals.assume_role_block's condition %q does not reference local.launch_account", condRaw)
	}

	trueRaw := sliceRaw(src, cond.TrueResult.Range())
	if !strings.Contains(trueRaw, "role_arn") {
		t.Errorf("locals.assume_role_block's non-empty branch %q does not contain role_arn", trueRaw)
	}
	if !strings.Contains(trueRaw, "external_id") {
		t.Errorf("locals.assume_role_block's non-empty branch %q does not contain external_id", trueRaw)
	}
	if !strings.Contains(trueRaw, "assume_role") {
		t.Errorf("locals.assume_role_block's non-empty branch %q does not contain an assume_role block", trueRaw)
	}

	// And the generate block must actually consume this local — otherwise the
	// guard exists but is never wired into the rendered provider.tf.
	genRaw := generateProviderContentsRaw(t, body, src)
	if !strings.Contains(genRaw, "assume_role_block") {
		t.Error(`generate "provider" block's contents do not reference local.assume_role_block — the guard is defined but never interpolated into the rendered provider`)
	}
}
