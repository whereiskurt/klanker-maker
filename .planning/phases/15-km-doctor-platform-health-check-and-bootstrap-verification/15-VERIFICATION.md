---
phase: 15-km-doctor-platform-health-check-and-bootstrap-verification
verified: 2026-03-23T00:00:00Z
status: passed
score: 10/10 must-haves verified
re_verification: false
---

# Phase 15: km doctor — Platform Health Check and Bootstrap Verification

**Phase Goal:** `km doctor` command that validates the entire platform setup — config, AWS credentials, bootstrap resources, per-region infrastructure, and active sandboxes — and outputs a structured health report with actionable remediation for any issues found. Also includes `km configure github --setup` manifest flow for one-click GitHub App creation.
**Verified:** 2026-03-23
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `km doctor` runs all platform checks and prints a structured health report with pass/fail/warn/skip symbols | VERIFIED | `internal/app/cmd/doctor.go` line 601: `formatCheckLine` prints ✓/✗/⚠/- symbols; 9 check functions implemented and wired via `buildChecks` |
| 2 | `km doctor --json` outputs a valid JSON array of check results | VERIFIED | Line 588-593: `json.NewEncoder(cmd.OutOrStdout()).Encode(toEncode)` on `[]CheckResult`; `TestDoctorCmd_JSONOutput` passes |
| 3 | `km doctor --quiet` suppresses OK results, showing only failures and warnings | VERIFIED | Line 597-600: `if quietMode && (r.Status == CheckOK || r.Status == CheckSkipped) { continue }`; `TestDoctorCmd_QuietMode` and `TestDoctorCmd_JSONQuiet` pass |
| 4 | `km doctor` exits with code 1 if any check is ERROR, 0 otherwise | VERIFIED | Line 617-619: `return fmt.Errorf("platform health check failed: %d error(s) found", errorCount)`; `TestDoctorCmd_AnyCheckError_ExitOne` and `TestDoctorCmd_AllChecksPass_ExitZero` pass |
| 5 | Each check is independent — a failed AWS call does not prevent other checks from running | VERIFIED | `runChecks` goroutines run independently; every check function has nil-client guard returning `CheckSkipped`; `TestRunChecks_Parallel` confirms all results collected regardless of individual failures |
| 6 | Checks run in parallel for speed | VERIFIED | Lines 447-468: `runChecks` uses `sync.WaitGroup` + goroutine per check + `sync.Mutex`-protected results slice, sorted by Name for stable output |
| 7 | `km configure github --setup` opens browser to GitHub App manifest creation page | VERIFIED | Lines 487-497 in `configure_github.go`: constructs `https://github.com/settings/apps/new?manifest=...`, calls `openBrowser`; `TestConfigureGitHubSetup_FlagRegistered` passes |
| 8 | After operator clicks 'Create GitHub App', the CLI exchanges the code for App credentials automatically | VERIFIED | `ExchangeManifestCode` (line 374): POST to `{baseURL}/app-manifests/{code}/conversions`; `TestExchangeManifestCode_Success` and `TestRunSetup_FullFlow` pass |
| 9 | App credentials (App ID, private key, installation ID) are stored in SSM without manual copy-paste | VERIFIED | `RunConfigureGitHubSetup` (lines 533-560): calls `putSSMParam` for `/km/config/github/app-client-id`, `/km/config/github/private-key`, `/km/config/github/installation-id`; `TestRunSetup_FullFlow` verifies 3 SSM writes |
| 10 | If no installations exist after creation, operator gets clear instructions to install the App first | VERIFIED | Lines 562-567: prints install instructions with URL and `km configure github --installation-id <ID>` command; `TestRunSetup_NoInstallations` passes |

**Score:** 10/10 truths verified

---

### Required Artifacts

| Artifact | Min Lines | Actual Lines | Status | Details |
|----------|-----------|--------------|--------|---------|
| `internal/app/cmd/doctor.go` | 300 | 797 | VERIFIED | CheckStatus/CheckResult types, 7 DI interfaces, DoctorConfigProvider, DoctorDeps, 9 check functions, runChecks (WaitGroup+Mutex), formatCheckLine, filterNonOK, NewDoctorCmd, NewDoctorCmdWithDeps, runDoctor, buildChecks, initRealDeps |
| `internal/app/cmd/doctor_test.go` | 200 | 727 | VERIFIED | 7 mock AWS clients, 31 unit tests — 24 for check functions, 7 for command shape/flags/output/exit code |
| `internal/app/cmd/help/doctor.txt` | 10 | 78 | VERIFIED | Check categories, --json/--quiet flags, symbols table, exit code, CI usage documented |
| `internal/app/cmd/configure_github.go` | 100 | 571 | VERIFIED | --setup flag, BuildManifestJSON, openBrowser, ReceiveManifestCode/WithPortCb, ExchangeManifestCode, fetchInstallations, RunConfigureGitHubSetup, runConfigureGitHubSetupInteractive |
| `internal/app/cmd/configure_github_test.go` | 80 | 603 | VERIFIED | 8 new tests for manifest flow: flag registration, JSON structure, callback server, code exchange, full flow, no-installation case |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/app/cmd/root.go` | `internal/app/cmd/doctor.go` | `root.AddCommand(NewDoctorCmd(cfg))` | WIRED | Confirmed at root.go line 52 |
| `internal/app/cmd/doctor.go` | `internal/app/config` | Config struct fields via DoctorConfigProvider interface | WIRED | appConfigAdapter wraps *config.Config at lines 112-124; GetStateBucket, GetManagementAccountID, GetPrimaryRegion etc. all delegate to cfg fields |
| `internal/app/cmd/configure_github.go` | `runConfigureGitHub()` (via RunConfigureGitHubSetup) | After manifest exchange, writes SSM params using same putSSMParam helper | WIRED | RunConfigureGitHubSetup (line 519) calls putSSMParam directly — same helper as runConfigureGitHub |
| `internal/app/cmd/configure_github.go` | `https://api.github.com/app-manifests/{code}/conversions` | `ExchangeManifestCode` POST call via `githubManifestBaseURL` | WIRED | Line 377: `apiURL := fmt.Sprintf("%s/app-manifests/%s/conversions", baseURL, code)`; package-level `githubManifestBaseURL` for test injection |

---

### Requirements Coverage

The requirement IDs listed in plan frontmatter differ from the prompt-supplied IDs. Both sets map to the same ROADMAP Phase 15 requirements. The REQUIREMENTS.md does not track DOCTOR-* or GITHUB-MANIFEST-* IDs — these are phase-internal identifiers defined in the ROADMAP requirements list. No DOCTOR-* or GITHUB-MANIFEST-* IDs appear in REQUIREMENTS.md, and no phase 15 entries exist in the requirements traceability table. This is not a gap — Phase 15 represents new operator-tooling capability beyond the v1 core requirements.

**Plan 01 requirements (frontmatter):** DOCTOR-CMD, DOCTOR-CHECKS, DOCTOR-OUTPUT, DOCTOR-JSON, DOCTOR-QUIET, DOCTOR-EXIT, DOCTOR-PARALLEL

**Plan 02 requirements (frontmatter):** DOCTOR-GITHUB-SETUP, DOCTOR-MANIFEST-FLOW

**Prompt-supplied requirement IDs mapped to implementation:**

| Requirement ID | ROADMAP Description | Implementation | Status |
|----------------|---------------------|----------------|--------|
| DOCTOR-CMD | km doctor Cobra command with colored output | `NewDoctorCmd` registered in root.go | SATISFIED |
| DOCTOR-CONFIG | Config check — domain, account IDs, SSO URL, primary region | `checkConfig` (line 146) | SATISFIED |
| DOCTOR-CREDS | AWS credential check via STS GetCallerIdentity | `checkCredential` (line 182) | SATISFIED |
| DOCTOR-BOOTSTRAP | S3 state bucket, DynamoDB lock table, KMS key | `checkStateBucket`, `checkDynamoTable`, `checkKMSKey` | SATISFIED |
| DOCTOR-SCP | SCP policy check via Organizations ListPoliciesForTarget | `checkSCP` (line 295) — skipped when no mgmt account ID | SATISFIED |
| DOCTOR-GITHUB | GitHub App SSM parameter check — warn if missing, not error | `checkGitHubConfig` (line 335) — returns CheckWarn on ParameterNotFound | SATISFIED |
| DOCTOR-REGION | Per-region VPC check via EC2 DescribeVpcs with km:managed tag | `checkRegionVPC` (line 371) | SATISFIED |
| DOCTOR-SANDBOXES | Active sandbox count via SandboxLister | `checkSandboxSummary` (line 406) | SATISFIED |
| DOCTOR-EXIT | Exit code 1 on any ERROR, 0 otherwise | `runDoctor` line 617-619 | SATISFIED |
| DOCTOR-JSON | --json flag outputs JSON array of CheckResult | `runDoctor` lines 588-593 | SATISFIED |
| DOCTOR-QUIET | --quiet suppresses OK/Skipped results | `runDoctor` lines 597-600; `filterNonOK` | SATISFIED |
| DOCTOR-INDEPENDENT | Each check non-fatal, failures don't block others | nil-client guards on every check; goroutine isolation in runChecks | SATISFIED |
| DOCTOR-PARALLEL | Checks run in parallel via goroutines | `runChecks` WaitGroup + Mutex (lines 447-468) | SATISFIED |
| GITHUB-MANIFEST | Browser-based manifest flow opens GitHub App creation page | `runConfigureGitHubSetupInteractive` + `openBrowser` | SATISFIED |
| GITHUB-MANIFEST-PREFILL | Manifest JSON pre-fills name, permissions, no webhook | `BuildManifestJSON` (line 273): name="klanker-maker-sandbox", contents="write", hook_attributes.active=false | SATISFIED |
| GITHUB-MANIFEST-STORE | After exchange, stores App ID + private key + installation ID in SSM | `RunConfigureGitHubSetup` (lines 533-560): 3 putSSMParam calls | SATISFIED |

**Orphaned requirements:** None — no DOCTOR-* or GITHUB-MANIFEST-* IDs appear in REQUIREMENTS.md that are unaccounted for by the plans.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/app/cmd/doctor.go` | 706 | Comment: "No EC2 clients — add a skipped placeholder for primary region." | Info | Code comment explaining legitimate skip behavior (nil EC2 clients → CheckSkipped). Not a stub — the check closure is fully implemented and returns a valid CheckResult. |

No TODO/FIXME/HACK markers found. No empty return stubs. No console.log-only implementations.

---

### Human Verification Required

#### 1. Terminal Color Output

**Test:** Run `km doctor` in a real terminal (not piped).
**Expected:** Colored symbols — green ✓ for OK, yellow ⚠ for WARN, red ✗ for ERROR — with colored summary line.
**Why human:** Color rendering depends on TTY detection (`isTerminal`); programmatic verification cannot test ANSI rendering in a real terminal.

#### 2. Browser Open Behavior

**Test:** Run `km configure github --setup` and verify the browser opens.
**Expected:** Default browser opens to `https://github.com/settings/apps/new?manifest=...` with pre-filled manifest JSON.
**Why human:** `openBrowser` uses `exec.Command("open", url).Start()` — cannot verify OS-level browser launch programmatically without a real display.

#### 3. Full Manifest Round-Trip Against Real GitHub

**Test:** Run `km configure github --setup` against a real GitHub account.
**Expected:** After clicking "Create GitHub App", CLI receives callback code, exchanges for credentials, and writes 3 SSM parameters without any manual copy-paste.
**Why human:** End-to-end flow requires real GitHub API interaction and OAuth callback; no programmatic test can cover this path against production GitHub.

#### 4. CI Gating Pattern

**Test:** Run `km doctor && echo "proceed"` in an environment with a misconfigured credential.
**Expected:** `km doctor` exits with code 1, `echo "proceed"` does not run.
**Why human:** Requires a real misconfigured AWS environment to confirm shell exit code propagation in CI context.

---

### Gaps Summary

No gaps found. All 10 observable truths are verified against the codebase:

- All 9 check functions exist and are substantively implemented (797-line file, 31 passing tests)
- Parallel execution is real (WaitGroup + Mutex), not simulated
- JSON and quiet modes work via tested code paths
- Exit code 1 is returned via `fmt.Errorf` from RunE (Cobra propagates as exit 1)
- Every check has a nil-client guard (independence requirement)
- The `--setup` manifest flow is fully implemented with local callback server, code exchange, and SSM storage
- All 4 key links are wired: root.go registers doctor, doctor uses config adapter, configure_github hits the GitHub API and writes SSM
- Build and vet pass; all 31 doctor tests and all 8 manifest flow tests pass

The only items not programmatically verifiable are visual (color output), OS-level (browser open), and require real external services (GitHub API, real AWS credentials).

---

_Verified: 2026-03-23_
_Verifier: Claude (gsd-verifier)_
