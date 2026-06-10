// Package h1_e2e contains opt-in live E2E tests for the Phase 103 HackerOne
// comment-trigger bridge. Tests are gated behind RUN_H1_E2E=1 so the default
// "go test ./..." never runs them — mirrors test/e2e/slack (RUN_SLACK_E2E).
//
// SAFETY: the live target MUST be a free HackerOne **Sandbox program**
// (https://hackerone.com/teams/new/sandbox). The Sandbox supports the same
// webhooks (X-H1-Signature) + customer API and lets you submit your OWN
// fake/test reports, so posting comments, changing state, and exercising
// /reply_to_researcher touch NOBODY real. NEVER point this at a production
// HackerOne program — that messages a real company's reports and real
// researchers. The Sandbox's own API token is the Basic-Auth credential for
// `km h1 init`. Production programs are read-only fixtures only.
//
// The safety-critical reply-VISIBILITY verification (internal-by-default,
// researcher-visible only when allowlisted + /reply_to_researcher) is observable
// ONLY in the live HackerOne UI and therefore cannot be asserted here — it lives
// in the manual UAT runbook (103-UAT.md). Manual UAT is acceptable per the
// Phase-97 / SLCK-09 precedent; this harness is the gated skeleton + the
// automatable-with-live-creds slices (km h1 status, km-h1 read).
//
// To run against a live HackerOne Sandbox program:
//
//	RUN_H1_E2E=1 \
//	  KM_H1_E2E_PROGRAM=prodsec_klanker_maker_test_h1b \
//	  KM_H1_E2E_REPORT=12345 \
//	  KM_H1_E2E_REGION=us-east-1 \
//	  go test ./test/e2e/h1/... -v -timeout 30m
//
// KM_H1_E2E_REPORT is an existing test-report id in the Sandbox program used to
// exercise the read path. The webhook-delivery + comment-visibility checks remain
// manual (103-UAT.md) because visibility is only observable in the H1 UI.
package h1_e2e

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMain is the opt-in gate. When RUN_H1_E2E is not "1", the process exits 0
// immediately so "go test ./..." stays clean and CI never makes live HackerOne
// calls. Identical posture to test/e2e/slack TestMain.
func TestMain(m *testing.M) {
	if os.Getenv("RUN_H1_E2E") != "1" {
		fmt.Fprintln(os.Stderr, "RUN_H1_E2E not set; skipping live HackerOne E2E tests")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// h1E2EConfig holds resolved env config for a live run.
type h1E2EConfig struct {
	Program  string // HackerOne Sandbox program handle (NEVER a production program)
	ReportID string // existing test-report id in the Sandbox program
	Region   string // AWS region
	KMBinary string // path to km binary (default: ./km)
}

// loadH1E2EConfig reads required env vars; calls t.Skip when any are missing so
// a partially-configured run degrades to a clean skip rather than a failure.
func loadH1E2EConfig(t *testing.T) h1E2EConfig {
	t.Helper()
	cfg := h1E2EConfig{
		Program:  os.Getenv("KM_H1_E2E_PROGRAM"),
		ReportID: os.Getenv("KM_H1_E2E_REPORT"),
		Region:   os.Getenv("KM_H1_E2E_REGION"),
		KMBinary: os.Getenv("KM_KM_BINARY"),
	}
	if cfg.KMBinary == "" {
		cfg.KMBinary = "./km"
	}
	if cfg.Program == "" || cfg.Region == "" {
		t.Skip("KM_H1_E2E_PROGRAM and KM_H1_E2E_REGION are required for live H1 E2E; skipping")
	}
	// Guardrail: the program handle must look like a Sandbox/test program. This is
	// a tripwire against accidentally pointing the live comment path at production.
	if !strings.Contains(strings.ToLower(cfg.Program), "test") &&
		!strings.Contains(strings.ToLower(cfg.Program), "sandbox") {
		t.Fatalf("KM_H1_E2E_PROGRAM=%q does not look like a HackerOne Sandbox/test program; refusing to run live H1 E2E against a possibly-production program", cfg.Program)
	}
	return cfg
}

// runKM runs the km binary with args and returns combined output + exit code.
func runKM(t *testing.T, cfg h1E2EConfig, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(cfg.KMBinary, args...)
	cmd.Env = append(os.Environ(), "AWS_REGION="+cfg.Region)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return string(out), code
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 1: km h1 status reflects the configured install (covers: H1-CLI-INIT-STATUS)
// ─────────────────────────────────────────────────────────────────────────────

// TestE2EH1_Status_ShowsConfiguredProgram verifies `km h1 status` runs against a
// live install and references the configured program handle. This is the
// automatable slice of H1-CLI-INIT-STATUS; the secret-redaction visual check is
// documented in 103-UAT.md.
func TestE2EH1_Status_ShowsConfiguredProgram(t *testing.T) {
	cfg := loadH1E2EConfig(t)

	out, code := runKM(t, cfg, "h1", "status")
	if code != 0 {
		t.Fatalf("km h1 status failed (exit=%d):\n%s", code, out)
	}
	if !strings.Contains(out, cfg.Program) {
		t.Errorf("km h1 status output does not reference configured program %q:\n%s", cfg.Program, out)
	}
	// Defense-in-depth: status must NEVER print a raw Basic-Auth token. The CLI
	// redacts it; assert the redaction marker is present and no obvious token is.
	if strings.Contains(strings.ToLower(out), "api token:") && !strings.Contains(out, "***") {
		t.Errorf("km h1 status appears to print an unredacted token; output:\n%s", out)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Scenario 2: km-h1 read against the Sandbox program (covers: H1-HELPER-KM-H1 read path)
// ─────────────────────────────────────────────────────────────────────────────

// TestE2EH1_HelperRead_FetchesReport drives the read verb of the sandbox-side
// km-h1 helper against an existing test-report in the Sandbox program. This
// confirms the live Basic-Auth path end-to-end WITHOUT posting anything (read is
// non-mutating, so it is safe to automate). Skips when KM_H1_E2E_REPORT is unset.
func TestE2EH1_HelperRead_FetchesReport(t *testing.T) {
	cfg := loadH1E2EConfig(t)
	if cfg.ReportID == "" {
		t.Skip("KM_H1_E2E_REPORT not set; skipping live km-h1 read (non-mutating) check")
	}
	bin := os.Getenv("KM_H1_HELPER_BINARY")
	if bin == "" {
		t.Skip("KM_H1_HELPER_BINARY not set (path to km-h1); skipping helper read check — covered by 103-UAT.md")
	}

	cmd := exec.Command(bin, "read", "--report", cfg.ReportID)
	cmd.Env = append(os.Environ(), "AWS_REGION="+cfg.Region)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("km-h1 read --report %s failed: %v\n%s", cfg.ReportID, err, out)
	}
	if !strings.Contains(string(out), cfg.ReportID) && !strings.Contains(string(out), "\"id\"") {
		t.Errorf("km-h1 read output does not look like a report JSON for %s:\n%s", cfg.ReportID, out)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Manual-only scenario documentation (mirrors test/e2e/slack manual notes)
// ─────────────────────────────────────────────────────────────────────────────

// NOTE: The safety-critical reply-VISIBILITY verifications are MANUAL-ONLY
// because visibility (internal vs researcher-visible) is observable only in the
// live HackerOne UI. They are documented step-by-step in 103-UAT.md:
//
//  1. Webhook "Test request" → 200 in Recent-Deliveries (valid X-H1-Signature).
//  2. report_created auto-triage → an INTERNAL triage comment (NOT researcher-visible).
//  3. Allowlisted "@handle /reply_to_researcher ..." → a RESEARCHER-VISIBLE reply,
//     posted exactly ONCE despite N fanout targets (primary target only).
//  4. NON-allowlisted "@handle /reply_to_researcher ..." → DOWNGRADED to internal
//     (or blocked) — NOT researcher-visible. (Any researcher-visible reply here is
//     a P0 safety bug.)
//  5. Loop-guard sanity: a report does NOT accrue repeated identical internal
//     comments (Pitfall 4).
//  6. Rate-limit watch: bursts surface a 429 with backoff, not a wedge (Pitfall 5).
//
// These cannot be reasonably scripted (live UI + real visibility semantics) and
// are run as UAT against the operator's HackerOne Sandbox account only.
