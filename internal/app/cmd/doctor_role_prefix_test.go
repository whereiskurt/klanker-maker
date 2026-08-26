package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPlatformRolePrefixes_CoversEveryInfraRole is a drift guard for the
// stale-role sweep in `km doctor --dry-run=false`.
//
// Why this test exists: the sweep deletes every {prefix}- IAM role that is
// neither matched by platformRolePrefixes nor named after an ACTIVE sandbox.
// A long-lived platform role satisfies neither condition, so forgetting to add
// a new module's role to that list makes a routine `km doctor` cleanup delete a
// live platform role. That is exactly what happened to check-runner (Phase 116),
// quota-alerter (Phase 121) and webhook-bridge (Phase 127) — three roles that
// were silently deletable, with nothing red anywhere to show it.
//
// Rather than re-listing the roles by hand (which is the very thing that
// drifted), this test parses infra/modules/**/main.tf for real
// `resource "aws_iam_role"` names and asserts each resolved name is either
// covered by platformRolePrefixes or legitimately exempt.
func TestPlatformRolePrefixes_CoversEveryInfraRole(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	modulesDir := filepath.Join(repoRoot, "infra", "modules")
	if _, err := os.Stat(modulesDir); err != nil {
		t.Skipf("infra/modules not present (%v) — nothing to guard", err)
	}

	// Modules whose roles are provisioned into a LINKED TARGET account by
	// `km account add`, never into the home account the sweep lists.
	exemptModules := map[string]string{
		"gpu-launcher-account": "roles live in a linked target account (Phase 126); the home-account sweep never sees them",
	}

	roleRe := regexp.MustCompile(`(?s)resource\s+"aws_iam_role"\s+"[^"]+"\s*\{(.*?)\n\}`)
	nameRe := regexp.MustCompile(`(?m)^\s*name\s*=\s*(.+)$`)
	localFnRe := regexp.MustCompile(`function_name\s*=\s*"([^"]+)"`)
	varDefaultRe := regexp.MustCompile(`(?s)variable\s+"role_name"\s*\{.*?default\s*=\s*"([^"]+)"`)

	prefixes := platformRolePrefixes("km-")

	var checked int
	walkErr := filepath.Walk(modulesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(path) != "main.tf" {
			return nil
		}
		rel, _ := filepath.Rel(modulesDir, path)
		module := strings.Split(rel, string(os.PathSeparator))[0]
		if reason, ok := exemptModules[module]; ok {
			t.Logf("skipping module %s: %s", module, reason)
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Errorf("read %s: %v", rel, readErr)
			return nil
		}
		src := string(body)

		for _, block := range roleRe.FindAllStringSubmatch(src, -1) {
			m := nameRe.FindStringSubmatch(block[1])
			if m == nil {
				continue
			}
			raw := strings.TrimSpace(m[1])

			// Resolve the name expression to a concrete "km-..." string.
			name := strings.Trim(raw, `"`)
			if strings.Contains(name, "local.function_name") {
				fn := localFnRe.FindStringSubmatch(src)
				if fn == nil {
					t.Errorf("%s: role name uses local.function_name but no function_name local found", rel)
					continue
				}
				name = strings.Replace(name, "${local.function_name}", fn[1], 1)
			}
			if name == "var.role_name" {
				varsPath := filepath.Join(filepath.Dir(path), "variables.tf")
				vb, vErr := os.ReadFile(varsPath)
				if vErr != nil {
					t.Errorf("%s: role name is var.role_name but variables.tf unreadable: %v", rel, vErr)
					continue
				}
				d := varDefaultRe.FindStringSubmatch(string(vb))
				if d == nil {
					t.Errorf("%s: role name is var.role_name but no default found", rel)
					continue
				}
				name = d[1]
			}
			name = strings.ReplaceAll(name, "${var.resource_prefix}", "km")

			// Per-sandbox roles carry a sandbox id and are correctly swept once
			// that sandbox is gone — they must NOT be in platformRolePrefixes.
			if strings.Contains(name, "${var.sandbox_id}") {
				continue
			}
			if strings.Contains(name, "${") {
				t.Logf("%s: unresolved interpolation in %q — skipping", rel, name)
				continue
			}
			if !strings.HasPrefix(name, "km-") {
				t.Logf("%s: role %q is not km-prefixed; sweep only lists km- roles", rel, name)
				continue
			}

			checked++
			covered := false
			for _, p := range prefixes {
				if strings.HasPrefix(name, p) {
					covered = true
					break
				}
			}
			if !covered {
				t.Errorf("platform role %q (from infra/modules/%s) is NOT covered by platformRolePrefixes.\n"+
					"`km doctor --dry-run=false` would treat it as stale and DELETE it.\n"+
					"Add its prefix to platformRolePrefixes, or add the module to exemptModules with a reason.",
					name, rel)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk infra/modules: %v", walkErr)
	}

	if checked == 0 {
		t.Fatal("parsed zero platform roles from infra/modules — the parser has drifted and this guard is inert")
	}
	t.Logf("verified %d platform role name(s) against platformRolePrefixes", checked)
}

// TestPlatformRolePrefixes_RegressionRoles pins the three roles that were
// discovered unprotected on 2026-08-26, so a future refactor of the list cannot
// quietly drop them again.
func TestPlatformRolePrefixes_RegressionRoles(t *testing.T) {
	prefixes := platformRolePrefixes("km-")
	for _, role := range []string{
		"km-check-runner",        // Phase 116
		"km-quota-alerter-role",  // Phase 121
		"km-webhook-bridge-role", // Phase 127
	} {
		covered := false
		for _, p := range prefixes {
			if strings.HasPrefix(role, p) {
				covered = true
				break
			}
		}
		if !covered {
			t.Errorf("%s must be protected from the stale-role sweep", role)
		}
	}
}

// TestPlatformRolePrefixes_HonorsCustomPrefix ensures the protection is not
// hardcoded to the default "km" install — a second install with a different
// resource_prefix must be equally protected.
func TestPlatformRolePrefixes_HonorsCustomPrefix(t *testing.T) {
	prefixes := platformRolePrefixes("km2-")
	found := false
	for _, p := range prefixes {
		if p == "km2-webhook-bridge" {
			found = true
		}
		if strings.HasPrefix(p, "km-") {
			t.Errorf("prefix %q leaked the default install prefix into a km2- install", p)
		}
	}
	if !found {
		t.Error("expected km2-webhook-bridge to be protected for a km2 install")
	}
}
