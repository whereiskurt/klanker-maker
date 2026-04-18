---
phase: 54-multi-account-github-app-installations
verified: 2026-04-17T23:30:00Z
status: passed
score: 7/7 must-haves verified
---

# Phase 54: Multi-Account GitHub App Installations Verification Report

**Phase Goal:** Allow a single GitHub App to be installed on multiple GitHub accounts/orgs simultaneously. Change storage to per-account installation IDs, auto-resolve the correct installation at sandbox create time based on repo owner, and update discover/setup flows to store all installations.
**Verified:** 2026-04-17T23:30:00Z
**Status:** passed
**Re-verification:** No -- initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | km configure github --setup stores per-account installation keys for all discovered installations | VERIFIED | configure_github.go:680 writes `/km/config/github/installations/{account}` in loop; TestSetupInteractive_WritesPerAccountKeys passes |
| 2 | km configure github --discover stores ALL installations as per-account keys, not just the first | VERIFIED | configure_github.go:295 iterates all installations and writes per-account keys; TestDiscoverInstallation_MultipleInstallations_WritesPerAccountKeys passes |
| 3 | km configure github --installation-id with --account stores a per-account key | VERIFIED | configure_github.go:228-236 writes per-account key when --account provided; TestConfigureGitHub_ManualWithAccount_WritesPerAccountKey passes |
| 4 | Legacy /km/config/github/installation-id is still written for backward compatibility | VERIFIED | All three flows (manual:239, discover:305, setup:690) write legacy key; TestDiscoverInstallation_MultipleInstallations_WritesLegacyKey and TestConfigureGitHub_ManualWithoutAccount_LegacyOnly pass |
| 5 | Sandbox creation resolves the correct installation ID based on repo owner in the profile | VERIFIED | create.go:1803 resolveInstallationID extracts owner, tries per-account SSM first at line 1818; TestResolveInstallationID_PerAccountFound and TestGenerateAndStoreGitHubToken_UsesPerAccountSSMKey pass |
| 6 | Falls back to legacy /km/config/github/installation-id when per-account key not found | VERIFIED | create.go:1831-1833 falls back to legacy key; TestResolveInstallationID_FallbackToLegacy, TestResolveInstallationID_BareReposFallbackToLegacy, TestResolveInstallationID_WildcardFallbackToLegacy all pass |
| 7 | km doctor reports OK when at least one per-account installation exists | VERIFIED | doctor.go:412-460 uses GetParametersByPath to find per-account keys; TestCheckGitHubConfig_PerAccountOnly, TestCheckGitHubConfig_LegacyOnly, TestCheckGitHubConfig_NeitherPerAccountNorLegacy all pass |

**Score:** 7/7 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/cmd/configure_github.go` | Per-account SSM storage for GitHub App installations | VERIFIED | Contains `/km/config/github/installations/` at lines 230, 295, 680; all three flows write per-account keys |
| `internal/app/cmd/configure_github_test.go` | Tests for multi-installation storage flows | VERIFIED | TestDiscoverInstallation_MultipleInstallations_WritesPerAccountKeys at line 581; 8+ new tests |
| `internal/app/cmd/create.go` | Owner-based installation ID resolution | VERIFIED | extractRepoOwner (line 1785), resolveInstallationID (line 1803), HCL injection (lines 934-938) |
| `internal/app/cmd/create_github_test.go` | Tests for multi-installation resolution | VERIFIED | TestExtractRepoOwner, 7 TestResolveInstallationID variants, TestGenerateAndStoreGitHubToken_UsesPerAccountSSMKey |
| `internal/app/cmd/doctor.go` | Multi-installation-aware GitHub config health check | VERIFIED | GetParametersByPath on SSMReadAPI (line 118), checkGitHubConfig uses installations prefix (line 416, 442) |
| `internal/app/cmd/doctor_test.go` | Tests for multi-installation doctor check | VERIFIED | 6 TestCheckGitHubConfig scenarios covering per-account only, legacy only, both, neither, not configured |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| configure_github.go | SSM Parameter Store | putSSMParam with /km/config/github/installations/{account} | WIRED | Three call sites: manual (230), discover (295), setup (680) |
| create.go | SSM Parameter Store | GetParameter with /km/config/github/installations/{owner} | WIRED | resolveInstallationID at line 1818 reads per-account key |
| create.go | HCL output | strings.Replace injection of installation_id | WIRED | Lines 934-938 inject resolved ID into compiled HCL |
| doctor.go | SSM Parameter Store | GetParametersByPath for /km/config/github/installations/ | WIRED | Line 442 queries prefix path for discovery |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| GHMI-01 | 54-01 | Per-account SSM storage for installations | SATISFIED | /km/config/github/installations/{account} written by all three flows |
| GHMI-02 | 54-01 | Discover/setup flows store all installations | SATISFIED | Loop over all installations in discover (295) and setup (680) |
| GHMI-03 | 54-02 | Auto-resolve installation at create time based on repo owner | SATISFIED | resolveInstallationID extracts owner, looks up per-account key |
| GHMI-04 | 54-02 | Legacy fallback when per-account key not found | SATISFIED | Fallback to /km/config/github/installation-id at create.go:1831 |
| GHMI-05 | 54-03 | Doctor health check recognizes per-account installations | SATISFIED | GetParametersByPath check, 6 test scenarios |

Note: GHMI-01 through GHMI-05 are not defined in REQUIREMENTS.md (no GHMI entries found). They exist only in the ROADMAP.md phase definition and plan frontmatter. This is acceptable as they are internally consistent across all plans and summaries.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| None | - | - | - | No anti-patterns found in phase-modified files |

Pre-existing PLACEHOLDER references in create.go (lines 1256-1270) are for Docker compose template substitution and unrelated to this phase.

### Human Verification Required

### 1. Multi-account end-to-end flow

**Test:** Install the GitHub App on two different GitHub accounts/orgs, run `km configure github --discover`, then create sandboxes with repos from each account.
**Expected:** Each account's installation ID is stored separately; sandbox creation resolves the correct installation ID based on repo owner.
**Why human:** Requires real GitHub App installations and AWS SSM access.

### 2. km doctor output with multiple installations

**Test:** After storing multiple per-account installation keys, run `km doctor`.
**Expected:** GitHub check reports OK with count and account names (e.g., "2 installation(s) found (orgA, orgB)").
**Why human:** Requires real SSM state to verify formatting.

### Gaps Summary

No gaps found. All 7 observable truths are verified. All 6 artifacts pass all three levels (exists, substantive, wired). All 5 key links are wired. All 5 requirements are satisfied. 21 tests pass. Binary builds cleanly.

---

_Verified: 2026-04-17T23:30:00Z_
_Verifier: Claude (gsd-verifier)_
