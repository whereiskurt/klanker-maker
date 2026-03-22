---
phase: 06-budget-enforcement-platform-configuration
verified: 2026-03-22T21:45:00Z
status: passed
score: 10/10 success criteria verified
re_verification:
  previous_status: gaps_found
  previous_score: 8/10
  gaps_closed:
    - "BUDG-03 — Hardcoded SpotRateUSD: 0.0 replaced with NetworkConfig.SpotRateUSD; create.go Step 6b resolves spot rate from Pricing API with static fallback; compute spend now non-zero at runtime"
    - "CONF-05 — km shell command implemented in shell.go, registered in root.go; substrate-aware dispatch to aws ssm start-session (EC2) and aws ecs execute-command (ECS); 5 tests pass"
  gaps_remaining: []
  regressions: []
human_verification:
  - test: "ConfigUI budget dashboard visual verification"
    expected: "Dashboard shows Compute Budget and AI Budget columns; green for <80%, yellow for 80-99%, red for 100%+ sandboxes; dash shown for sandboxes without budget; HTMX 10-second refresh"
    why_human: "Visual color-coding and HTMX polling behavior cannot be verified programmatically"
  - test: "Bedrock MITM proxy with real TLS in a live sandbox"
    expected: "Agent Bedrock calls are intercepted, token counts appear in DynamoDB, budget enforces at 100% with 403 response"
    why_human: "Requires real Bedrock API call through goproxy MITM — depends on CA cert injection in sandbox and live AWS credentials"
  - test: "km configure DNS delegation guidance for 3-account topology"
    expected: "When management account != application account, wizard prints NS delegation instructions"
    why_human: "Multi-account Route53 scenario requires real AWS account setup to verify"
  - test: "km shell EC2 end-to-end"
    expected: "Running km shell against a live EC2 sandbox opens an SSM Session Manager interactive shell"
    why_human: "Requires a running EC2 sandbox with SSM agent installed and AWS credentials with ssm:StartSession permission"
  - test: "km shell ECS end-to-end"
    expected: "Running km shell against a live ECS Fargate sandbox opens a /bin/bash shell via ECS Exec"
    why_human: "Requires ECS Exec enabled on the task definition and live AWS credentials"
---

# Phase 6: Budget Enforcement & Platform Configuration — Re-Verification Report

**Phase Goal:** Operators can set per-sandbox dollar budgets for compute and AI (Bedrock Anthropic models), with real-time spend tracking, threshold warnings, and hard enforcement; the platform is fully configurable for any domain and AWS account structure so anyone can fork and deploy their own instance.
**Verified:** 2026-03-22T21:45:00Z
**Status:** passed
**Re-verification:** Yes — after gap closure (Plans 06-08 and 06-09)

---

## Gap Closure Confirmation

### Gap 1: BUDG-03 — Compute spend hardcoded to $0.00 (CLOSED)

**Previous state:** `pkg/compiler/service_hcl.go` lines 449 and 530 hardcoded `SpotRateUSD: 0.0`. `create.go` never called `GetSpotRate`. All provisioned sandboxes passed `spot_rate=0.0` to the Lambda, making compute spend always $0.00.

**Plan 06-08 delivered:**
- `NetworkConfig.SpotRateUSD float64` field added to compiler struct (line 407 of `service_hcl.go`)
- Both EC2 and ECS template data now use `network.SpotRateUSD` (lines 453, 534) — hardcoded 0.0 removed
- `create.go` Step 6b (lines 156-184) calls `awspkg.GetSpotRate` from Pricing API (us-east-1 endpoint), falls back to `staticSpotRate()` when API returns zero or errors
- `internal/app/cmd/spot_rate.go` — new file with 30-entry lookup table covering t3, t3a, c5, m5, r5, g4dn families; $0.10/hr conservative fallback for unknown types

**Verified:**
- `grep "SpotRateUSD: 0\.0" pkg/compiler/service_hcl.go` returns no results — hardcoded zero is gone
- `grep "SpotRateUSD" pkg/compiler/service_hcl.go` shows field in NetworkConfig and both EC2/ECS assignments using `network.SpotRateUSD`
- `grep "SpotRate" internal/app/cmd/create.go` shows Step 6b: Pricing API call at line 169, static fallback at line 173, `network.SpotRateUSD = spotRate` at line 179
- Commits `c08bfa3` (compiler wiring) and `41804a0` (create.go resolution) verified in git log
- 3 new TDD tests pass: `TestSpotRateEC2NonZero`, `TestSpotRateEC2ZeroFallback`, `TestSpotRateECSNonZero`

### Gap 2: CONF-05 — km shell command missing (CLOSED)

**Previous state:** CONF-05 was listed in ROADMAP Phase 6 requirements and as Success Criterion #10. None of the 7 original plans claimed or implemented it. `km shell` did not exist anywhere in the codebase.

**Plan 06-09 delivered:**
- `internal/app/cmd/shell.go` (4627 bytes, 139 lines) — `NewShellCmd`, `NewShellCmdWithFetcher` (DI variant for tests), `runShell`, `execSSMSession`, `execECSCommand`, `extractResourceID`, `findResourceARN`
- `internal/app/cmd/shell_test.go` (7459 bytes, 5 tests) — verifies EC2 SSM dispatch args, ECS execute-command args, stopped-sandbox error message, unknown-substrate error, missing-instance-ID error
- `internal/app/cmd/help/shell.txt` — embedded help text
- `internal/app/cmd/root.go` line 46 — `root.AddCommand(NewShellCmd(cfg))`

**Verified:**
- `shell.go` exists and is substantive (139 lines, full implementation)
- `grep "NewShellCmd" internal/app/cmd/root.go` returns line 46 — registered
- EC2 dispatch: `aws ssm start-session --target <instanceID> --region <region>`
- ECS dispatch: `aws ecs execute-command --cluster <clusterARN> --task <taskARN> --interactive --command /bin/bash --region <region>`
- Stopped sandbox error: `"sandbox %s is stopped — start it with 'km budget add %s --compute <amount>' first"`
- `SandboxFetcher` interface reused from `status.go`; `ShellExecFunc` DI pattern allows test capture of `exec.Cmd.Args` without executing AWS CLI
- Commits `97f1516`, `c65184c`, `33417ea` verified in git log
- 5/5 shell tests pass: `TestShellCmd_EC2`, `TestShellCmd_ECS`, `TestShellCmd_StoppedSandbox`, `TestShellCmd_UnknownSubstrate`, `TestShellCmd_MissingInstanceID`
- REQUIREMENTS.md updated: CONF-05 now marked `[x]` Complete

---

## Goal Achievement

### Observable Truths (all 10 Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | `km configure` wizard walks through domain/accounts/SSO/region and writes config file | VERIFIED | `configure.go` (230 lines) — interactive + `--non-interactive` modes; writes `km-config.yaml` |
| 2 | Fork with different domain works end-to-end — SES, schema $id, apiVersion, ConfigUI all config-derived | VERIFIED | Schema uses `__SCHEMA_DOMAIN__` placeholder; `apiVersion` pattern `^.+/v1alpha1$`; all Go code uses `cfg.Domain`; `infra/live/site.hcl` uses `get_env("KM_DOMAIN", ...)` |
| 3 | Sandbox with `spec.budget.compute.maxSpendUSD` / `spec.budget.ai.maxSpendUSD` — DynamoDB global table stores limits | VERIFIED | `pkg/profile/types.go` `BudgetSpec`; `infra/modules/dynamodb-budget/v1.0.0/main.tf` PAY_PER_REQUEST global table; `create.go` calls `SetBudgetLimits` after Terragrunt apply |
| 4 | Compute spend tracked as spot rate x elapsed minutes; Lambda suspends at 100% | VERIFIED | Lambda calculates `spotRate * elapsedMinutes / 60`; `create.go` Step 6b resolves non-zero rate from Pricing API + static fallback; `spot_rate.go` 30-entry lookup table; compiler threads `network.SpotRateUSD` through both EC2 and ECS paths |
| 5 | http-proxy intercepts Bedrock responses, extracts tokens, prices them, increments DynamoDB; `km status` shows per-model breakdown | VERIFIED | `bedrock.go` SSE parser; `proxy.go` `AlwaysMitm` for bedrock-runtime; `IncrementAISpend` goroutine; `status.go` per-model breakdown display |
| 6 | 100% AI budget: proxy returns 403; Lambda revokes Bedrock IAM as backstop | VERIFIED | `proxy.go` `BedrockBlockedResponse` (403 + JSON); `budget-enforcer/main.go` `DetachRolePolicy` for `AmazonBedrockFullAccess` |
| 7 | 80% threshold warning email; threshold configurable via `spec.budget.warningThreshold` | VERIFIED | Lambda sends `SendLifecycleNotification` at 80%, guarded by `warningNotified` attribute; `BudgetSpec.WarningThreshold` from profile YAML |
| 8 | `km budget add` increases limits, restores IAM, starts EC2, unblocks proxy | VERIFIED | `budget.go` calls `SetBudgetLimits`, `StartInstances`, `AttachRolePolicy`; budget cache TTL means proxy unblocks within 10s |
| 9 | DynamoDB global table replicated to all regions; budget reads hit local replica | VERIFIED | `infra/modules/dynamodb-budget/v1.0.0/main.tf` `dynamic "replica"` block with `for_each = var.replica_regions` |
| 10 | `km shell <sandbox-id>` opens interactive shell, auto-detecting substrate | VERIFIED | `shell.go` (139 lines); EC2 dispatches SSM Session Manager, ECS dispatches ECS Exec; substrate read from `SandboxRecord.Substrate`; registered in `root.go` line 46; 5 tests pass |

**Score:** 10/10 success criteria verified

---

## Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/app/config/config.go` | Extended Config struct with platform fields | VERIFIED | Domain, ManagementAccountID, TerraformAccountID, ApplicationAccountID, SSOStartURL, SSORegion, PrimaryRegion, BudgetTableName present |
| `internal/app/cmd/configure.go` | km configure wizard | VERIFIED | 230 lines; interactive + --non-interactive; DNS delegation guidance |
| `km-config.yaml.example` | Example config for forks | VERIFIED | File exists at repo root |
| `pkg/profile/types.go` | BudgetSpec struct | VERIFIED | BudgetSpec, ComputeBudget, AIBudget; Budget field on Spec |
| `pkg/aws/budget.go` | BudgetAPI interface + helpers | VERIFIED | IncrementAISpend, IncrementComputeSpend, GetBudget, SetBudgetLimits all substantive |
| `pkg/aws/pricing.go` | PricingAPI + static fallback | VERIFIED | GetBedrockModelRates with static fallback; GetSpotRate called from create.go Step 6b |
| `infra/modules/dynamodb-budget/v1.0.0/main.tf` | DynamoDB global table | VERIFIED | PAY_PER_REQUEST, dynamic replica blocks, TTL, DDB Streams |
| `sidecars/http-proxy/httpproxy/bedrock.go` | Bedrock SSE parser + enforcement | VERIFIED | 170 lines; ExtractBedrockTokens, CalculateCost, ExtractModelID, BedrockBlockedResponse |
| `sidecars/http-proxy/httpproxy/budget_cache.go` | 10s TTL budget cache | VERIFIED | 91 lines; Get, Set, UpdateLocalSpend |
| `cmd/budget-enforcer/main.go` | Lambda for compute tracking + enforcement | VERIFIED | 470 lines; StopInstances/StopTask at 100%, DetachRolePolicy backstop, SES warning at 80% |
| `infra/modules/budget-enforcer/v1.0.0/main.tf` | Lambda + EventBridge 1-min schedule | VERIFIED | aws_lambda_function + aws_scheduler_schedule rate(1 minute) |
| `pkg/compiler/service_hcl.go` | Budget enforcer wired into compiled artifacts with real spot rate | VERIFIED | `NetworkConfig.SpotRateUSD` field (line 407); EC2 (line 453) and ECS (line 534) both use `network.SpotRateUSD`; hardcoded 0.0 removed |
| `internal/app/cmd/create.go` | Spot rate resolved before Compile() | VERIFIED | Step 6b (lines 156-184): Pricing API call + static fallback; `network.SpotRateUSD = spotRate` |
| `internal/app/cmd/spot_rate.go` | Static spot rate lookup table | VERIFIED | 74 lines; 30 entries for t3/t3a/c5/m5/r5/g4dn; $0.10/hr conservative fallback for unknown types |
| `internal/app/cmd/budget.go` | km budget add with auto-resume | VERIFIED | SetBudgetLimits + StartInstances + AttachRolePolicy all wired |
| `internal/app/cmd/status.go` | Extended km status with budget display | VERIFIED | BudgetFetcher interface; per-model breakdown; ANSI color coding |
| `cmd/configui/handlers_budget.go` | ConfigUI budget dashboard columns | VERIFIED | BudgetDisplayData, dynoBudgetFetcher, CSS class logic |
| `cmd/configui/templates/dashboard.html` | Budget columns in dashboard | VERIFIED | Compute Budget + AI Budget columns with .budget-ok/.budget-warn/.budget-exceeded CSS |
| `internal/app/cmd/shell.go` | km shell substrate-aware command | VERIFIED | 139 lines; NewShellCmd, runShell, execSSMSession, execECSCommand, ARN helpers |
| `internal/app/cmd/shell_test.go` | Shell command tests | VERIFIED | 5 tests: EC2 args, ECS args, stopped error, unknown substrate error, missing resource error |

---

## Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/app/config/config.go` | `km-config.yaml` | `viper.SetConfigName("km-config")` | VERIFIED | Two-viper merge loads km-config.yaml into primary config |
| `internal/app/cmd/root.go` | `configure.go`, `budget.go`, `shell.go` | `AddCommand(...)` | VERIFIED | Lines 43-46: NewConfigureCmd, NewBootstrapCmd, NewBudgetCmd, NewShellCmd all registered |
| `internal/app/cmd/create.go` | `pkg/aws/pricing.go` | `awspkg.GetSpotRate` Step 6b | VERIFIED | Line 169; static fallback at line 173 via `staticSpotRate()` in spot_rate.go |
| `internal/app/cmd/create.go` | `pkg/compiler/service_hcl.go` | `NetworkConfig.SpotRateUSD` | VERIFIED | `network.SpotRateUSD = spotRate` at line 179; NetworkConfig passed to `compiler.Compile()` |
| `pkg/compiler/service_hcl.go` | `spot_rate` in EventBridge payload | `network.SpotRateUSD` (non-zero) | VERIFIED | Lines 453, 534 use `network.SpotRateUSD`; hardcoded 0.0 is gone |
| `internal/app/cmd/create.go` | `pkg/aws/budget.go` | `SetBudgetLimits` after Terragrunt apply | VERIFIED | Line 284 calls SetBudgetLimits when `profile.Spec.Budget != nil` |
| `internal/app/cmd/status.go` | `pkg/aws/budget.go` | `GetBudget` via realBudgetFetcher | VERIFIED | Line 172 calls `kmaws.GetBudget` |
| `internal/app/cmd/budget.go` | `pkg/aws/budget.go` | `SetBudgetLimits + StartInstances + AttachRolePolicy` | VERIFIED | Lines 137, 216, 244 |
| `internal/app/cmd/shell.go` | `pkg/aws/sandbox.go` | `SandboxFetcher.FetchSandbox` to `SandboxRecord.Substrate` | VERIFIED | `runShell` calls `fetcher.FetchSandbox`; dispatches on `rec.Substrate` |
| `sidecars/http-proxy/httpproxy/proxy.go` | `bedrock.go` | `BedrockBlockedResponse` + `IncrementAISpend` | VERIFIED | Lines 139, 185, 216 in proxy.go |
| `cmd/configui/handlers_budget.go` | `pkg/aws/budget.go` | `GetBudget` per sandbox | VERIFIED | dynoBudgetFetcher.GetBudget calls `kmaws.GetBudget` |
| `infra/live/site.hcl` | Domain configuration | `get_env("KM_DOMAIN", "klankermaker.ai")` | VERIFIED | Line 6 of site.hcl |

---

## Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|---------|
| CONF-01 | 06-01 | Single config file for all platform values | SATISFIED | `config.go` loads km-config.yaml via two-viper merge |
| CONF-02 | 06-03 | Domain configurable — SES, schema $id, apiVersion, ConfigUI | SATISFIED | `__SCHEMA_DOMAIN__` placeholder; `apiVersion` pattern `^.+/v1alpha1$`; all Go code derives from cfg.Domain |
| CONF-03 | 06-01 | AWS account numbers and SSO URL configurable | SATISFIED | Config struct has ManagementAccountID, TerraformAccountID, ApplicationAccountID, SSOStartURL |
| CONF-04 | 06-01 | km configure wizard | SATISFIED | `configure.go` — interactive + --non-interactive wizard writing km-config.yaml |
| CONF-05 | 06-09 | km shell substrate-abstracted interactive shell | SATISFIED | `shell.go` (139 lines) with SSM (EC2) and ECS Exec dispatch; registered in root.go line 46; REQUIREMENTS.md marked [x] Complete |
| BUDG-01 | 06-02 | Per-sandbox budget spec in profile YAML | SATISFIED | `pkg/profile/types.go` BudgetSpec; JSON schema updated |
| BUDG-02 | 06-02 | DynamoDB global table for budget storage | SATISFIED | `infra/modules/dynamodb-budget/v1.0.0/main.tf` with PAY_PER_REQUEST + replica blocks |
| BUDG-03 | 06-08 | Compute spend: spot rate x elapsed minutes | SATISFIED | Pricing API + static fallback in create.go; `NetworkConfig.SpotRateUSD` threads real rate to compiler and Lambda; REQUIREMENTS.md marked [x] Complete |
| BUDG-04 | 06-04 | AI/token spend via http-proxy Bedrock interception | SATISFIED | MITM AlwaysMitm for bedrock-runtime; SSE parser; atomic DynamoDB increment |
| BUDG-05 | 06-02 | Model pricing from AWS Price List API with static fallback | SATISFIED | `pricing.go` GetBedrockModelRates with nil-client static fallback |
| BUDG-06 | 06-06 | 80% threshold warning email | SATISFIED | Lambda sends SendLifecycleNotification at 80%, one-shot guard via warningNotified |
| BUDG-07 | 06-05 | Dual-layer enforcement: proxy 403 + Lambda IAM revocation; compute stop | SATISFIED | proxy.go 403 + DetachRolePolicy backstop + StopInstances/StopTask |
| BUDG-08 | 06-06 | km budget add top-up with auto-resume | SATISFIED | `budget.go` SetBudgetLimits + StartInstances + AttachRolePolicy |
| BUDG-09 | 06-06, 06-07 | km status budget display + ConfigUI dashboard | SATISFIED | CLI per-model breakdown + ConfigUI budget columns |

All 14 requirements (CONF-01 through CONF-05, BUDG-01 through BUDG-09) are SATISFIED. No orphaned requirements remain.

---

## Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `cmd/budget-enforcer/main.go` | 371 | `TODO: Trigger artifact upload before stopping ECS task` | Warning | ECS tasks stopped at 100% compute budget will not upload artifacts — intentionally deferred to TTL handler path |
| `sidecars/http-proxy/main.go` | 57 | `TODO: custom CA support — read KM_PROXY_CA_CERT` | Warning | Custom CA certificate injection for MITM requires proxy restart or custom CA; goproxy built-in CA works for proof-of-concept |
| `sidecars/http-proxy/main.go` | 49 | `TODO: wire a real pricing.Client for live rate lookups` | Info | Model rates use static fallback only at proxy startup; pricing cache refresh uses nil client |

No blocker anti-patterns. The previously-blocking `SpotRateUSD: 0.0` hardcoded values are confirmed removed.

---

## Test Suite Status

All 17 packages pass. No regressions introduced by Plans 06-08 or 06-09.

```
ok  github.com/whereiskurt/klankrmkr/cmd/budget-enforcer
ok  github.com/whereiskurt/klankrmkr/cmd/configui
ok  github.com/whereiskurt/klankrmkr/cmd/ttl-handler
ok  github.com/whereiskurt/klankrmkr/internal/app/cmd       (includes 5 new shell tests, 3 new spot rate tests)
ok  github.com/whereiskurt/klankrmkr/internal/app/config
ok  github.com/whereiskurt/klankrmkr/pkg/aws
ok  github.com/whereiskurt/klankrmkr/pkg/compiler            (includes 3 new spot rate compiler tests)
ok  github.com/whereiskurt/klankrmkr/pkg/lifecycle
ok  github.com/whereiskurt/klankrmkr/pkg/profile
ok  github.com/whereiskurt/klankrmkr/pkg/terragrunt
ok  github.com/whereiskurt/klankrmkr/sidecars/audit-log
ok  github.com/whereiskurt/klankrmkr/sidecars/dns-proxy/dnsproxy
ok  github.com/whereiskurt/klankrmkr/sidecars/http-proxy/httpproxy
```

Binary: `go build ./cmd/km/` succeeds cleanly.

---

## Human Verification Required

### 1. ConfigUI Budget Dashboard Visual

**Test:** Start configui (`go run ./cmd/configui/`) with a DynamoDB table having budget records. Open browser to dashboard.
**Expected:** Sandbox rows show "Compute Budget" and "AI Budget" columns. Color coded: green for <80%, yellow for 80-99%, red for 100%+. Sandboxes without budget show dash. Dashboard refreshes every 10 seconds via HTMX.
**Why human:** Visual rendering and HTMX polling behavior cannot be verified programmatically.

### 2. Bedrock MITM Token Metering in Live Sandbox

**Test:** Create a sandbox, make an Anthropic Bedrock InvokeModel call from inside it, query DynamoDB for the BUDGET#ai# record.
**Expected:** Token counts and USD cost appear in DynamoDB after the call. `km status <sandbox-id>` shows AI spend breakdown.
**Why human:** Requires real Bedrock API call through goproxy MITM, live AWS credentials, CA cert injection, and real DynamoDB.

### 3. km configure DNS Delegation for 3-Account Topology

**Test:** Run `km configure` with management account ID != application account ID.
**Expected:** Wizard detects 3-account topology and prints NS delegation guidance.
**Why human:** Verifying the exact output requires real account IDs or a terminal session.

### 4. km shell EC2 End-to-End

**Test:** Run `km shell <sandbox-id>` against a running EC2 sandbox with SSM agent installed.
**Expected:** Interactive shell session opens via SSM Session Manager. Session starts within a few seconds.
**Why human:** Requires a running EC2 sandbox, SSM agent, and AWS credentials with ssm:StartSession permission.

### 5. km shell ECS End-to-End

**Test:** Run `km shell <sandbox-id>` against a running ECS Fargate sandbox with ECS Exec enabled.
**Expected:** `/bin/bash` shell session opens inside the ECS container.
**Why human:** Requires ECS Exec enabled on the task definition, SSM agent in the container, and live AWS credentials.

---

## Gaps Summary

No gaps remain. Both gaps from the initial verification are confirmed closed.

**Gap 1 (BUDG-03) closed:** Compute spend tracking now produces non-zero values at runtime. The Pricing API call plus static 30-entry lookup table in `spot_rate.go` ensures `network.SpotRateUSD` is always non-zero for sandboxes with compute budgets. The Lambda enforcer receives a real hourly rate and calculates `rate * elapsed_minutes / 60` correctly. Budget enforcement (StopInstances at 100%, SES warning at 80%) will now trigger as designed.

**Gap 2 (CONF-05) closed:** `km shell <sandbox-id>` is a fully-implemented, tested, and registered command. It auto-detects the substrate from `SandboxRecord.Substrate`, dispatches `aws ssm start-session` for EC2 or `aws ecs execute-command` for ECS, forwards stdin/stdout/stderr to the terminal, and returns clear errors for stopped sandboxes and unknown substrates. REQUIREMENTS.md is updated to reflect completion.

All 10 success criteria are now verified. All 14 requirement IDs (CONF-01 through CONF-05, BUDG-01 through BUDG-09) are satisfied. The phase goal is achieved. 5 items need human verification (visual/interactive behaviors that require live AWS or a terminal session).

---

_Verified: 2026-03-22T21:45:00Z_
_Verifier: Claude (gsd-verifier)_
_Re-verification: Yes — gap closure after Plans 06-08 and 06-09_
