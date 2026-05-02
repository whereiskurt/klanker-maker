---
phase: 66
slug: multi-instance-support-configurable-resource-prefix-and-email-subdomain
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-02
---

# Phase 66 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Derived from `66-RESEARCH.md` § Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib `go test`) |
| **Config file** | `internal/app/config/config_test.go` (existing — extend) |
| **Quick run command** | `go test ./internal/app/config/... -count=1 -v` |
| **Full suite command** | `go test ./internal/... ./pkg/... ./cmd/... -count=1` |
| **Estimated runtime** | ~30s quick / ~3-5 min full |

---

## Sampling Rate

- **After every task commit:** `go test ./internal/app/config/... -count=1`
- **After every plan wave:** `go test ./internal/... ./pkg/... -count=1`
- **Before `/gsd:verify-work`:** Full suite must be green (`go test ./...`)
- **Max feedback latency:** ~30s for config-only changes; ~3-5 min for cross-package waves

---

## Per-Task Verification Map

Tasks numbered as `66-{plan}-{task}`. Plan slicing per research:
- 01: Config struct + helpers + Wave 0 tests
- 02: Migrate Go call sites for `email_subdomain` (~30 sites → `Config.GetEmailDomain()`)
- 03: Migrate Lambda code (`cmd/*/main.go`) to env-var-driven names with NO hardcoded fallbacks
- 04: Thread prefix through Terragrunt site_vars + parameterize TF module `name` attributes
- 05: TF state backend + `km init` flow + `km doctor` checks + grep-audit gate

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 66-01-01 | 01 | 0 | REQ-CONFIG-EXTENSIBILITY | unit (stub) | `go test ./internal/app/config/... -run TestGetResourcePrefix_Default` | ❌ Wave 0 | ⬜ pending |
| 66-01-02 | 01 | 0 | REQ-CONFIG-EXTENSIBILITY | unit (stub) | `go test ./internal/app/config/... -run TestGetResourcePrefix_Custom` | ❌ Wave 0 | ⬜ pending |
| 66-01-03 | 01 | 0 | REQ-CONFIG-EXTENSIBILITY | unit (stub) | `go test ./internal/app/config/... -run TestGetEmailDomain_Default` | ❌ Wave 0 | ⬜ pending |
| 66-01-04 | 01 | 0 | REQ-CONFIG-EXTENSIBILITY | unit (stub) | `go test ./internal/app/config/... -run TestGetEmailDomain_Custom` | ❌ Wave 0 | ⬜ pending |
| 66-01-05 | 01 | 0 | REQ-CONFIG-EXTENSIBILITY | unit (stub) | `go test ./internal/app/config/... -run TestGetSsmPrefix` | ❌ Wave 0 | ⬜ pending |
| 66-01-06 | 01 | 0 | REQ-CONFIG-EXTENSIBILITY | unit | `go test ./internal/app/config/... -run TestLoadBackwardCompat` | ✅ (extend) | ⬜ pending |
| 66-01-07 | 01 | 1 | REQ-CONFIG-EXTENSIBILITY | impl + verify | `go test ./internal/app/config/... -count=1` | ❌ → ✅ | ⬜ pending |
| 66-02-01 | 02 | 2 | REQ-PLATFORM-MULTI-INSTANCE | unit | `go test ./internal/app/cmd/... -count=1 -run 'Email\|Domain'` | ✅ (extend) | ⬜ pending |
| 66-02-02 | 02 | 2 | REQ-PLATFORM-MULTI-INSTANCE | grep audit | `! grep -rn '"sandboxes\.' ./internal ./pkg --include='*.go' \| grep -v _test.go` | gate | ⬜ pending |
| 66-03-01 | 03 | 2 | REQ-PLATFORM-MULTI-INSTANCE | unit | `go test ./cmd/... -count=1` | ✅ (extend) | ⬜ pending |
| 66-03-02 | 03 | 2 | REQ-PLATFORM-MULTI-INSTANCE | grep audit | `! grep -rnE '"km-(budgets\|sandboxes\|identities\|schedules\|ttl-handler\|create-handler\|at)"' ./cmd --include='*.go' \| grep -v _test.go` | gate | ⬜ pending |
| 66-04-01 | 04 | 3 | REQ-PLATFORM-MULTI-INSTANCE | TF plan smoke | manual: `terragrunt run-all plan` shows zero replace/destroy on existing tables | manual | ⬜ pending |
| 66-04-02 | 04 | 3 | REQ-PLATFORM-MULTI-INSTANCE | unit | `go test ./pkg/compiler/... -count=1` | ✅ (extend) | ⬜ pending |
| 66-04-03 | 04 | 3 | REQ-PLATFORM-MULTI-INSTANCE | grep audit | `! grep -rn '"km-' ./infra --include='*.tf' --include='*.hcl' \| grep -v '\.terragrunt-cache'` | gate | ⬜ pending |
| 66-05-01 | 05 | 4 | REQ-PLATFORM-MULTI-INSTANCE | unit | `go test ./internal/app/cmd/... -run 'TestInit\|TestDoctor' -count=1` | ✅ (extend) | ⬜ pending |
| 66-05-02 | 05 | 4 | REQ-PLATFORM-MULTI-INSTANCE | grep audit (final gate) | `! grep -rn '"/km/' ./internal ./pkg ./cmd --include='*.go' \| grep -v _test.go \| grep -v 'km-config\|km-state\|/opt/km'` | gate | ⬜ pending |
| 66-05-03 | 05 | 4 | REQ-PLATFORM-MULTI-INSTANCE | full suite | `go test ./... -count=1` | ✅ | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/config/config_test.go` — extend with:
  - `TestGetResourcePrefix_Default` (asserts `"km"` when unset)
  - `TestGetResourcePrefix_Custom` (asserts custom value)
  - `TestGetEmailDomain_Default` (asserts `"sandboxes.{domain}"` when unset)
  - `TestGetEmailDomain_Custom` (asserts `"{custom}.{domain}"`)
  - `TestGetSsmPrefix` (asserts `"/km/"` default and `"/{prefix}/"` custom)
  - `TestLoadBackwardCompat` (asserts km-config.yaml without new fields loads + defaults populate)
  - `TestLoadResourcePrefix` / `TestLoadEmailSubdomain` (yaml override paths)
- [ ] No new framework install needed — Go stdlib `go test` already in use across the codebase

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Two installs coexist in one account | REQ-PLATFORM-MULTI-INSTANCE | Requires real AWS account + two separate `km init` runs with different prefixes | 1) `km init` with `resource_prefix=km` → create sandbox A. 2) Edit km-config.yaml to `resource_prefix=alt`. 3) `km init` again. 4) `km create` sandbox B. 5) Verify both sandboxes run, separate DynamoDB tables (`km-sandboxes` and `alt-sandboxes`), separate Lambdas, no IAM/EventBridge collisions. |
| Existing install upgrades zero-diff | REQ-CONFIG-EXTENSIBILITY | Requires existing-install env to confirm TF plan shows no replace/destroy | 1) Check out post-Phase-66 build against existing live install. 2) `terragrunt run-all plan` against `infra/live/use1/`. 3) Verify zero `replace`, zero `destroy`, only `update-in-place` on tag/description metadata if any. |
| SES domain identity continuity | REQ-PLATFORM-MULTI-INSTANCE | DNS round-trip + SES verification cannot be exercised in unit tests | Manual: existing `sandboxes.{domain}` SES identity unaffected when `email_subdomain` defaults to `"sandboxes"`. Custom `email_subdomain` requires fresh DNS/SES verification (documented operator caveat, not auto-verified). |
| EventBridge schedule group rename safety | REQ-PLATFORM-MULTI-INSTANCE | Schedules under `km-at` carry the group name; rename = recreate | Manual: confirm default `resource_prefix=km` keeps `km-at` group; document that changing prefix on running install is unsupported (operator chooses at `km init`). |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify (✓ — every plan has at least one unit test or grep gate)
- [ ] Wave 0 covers all MISSING references (config helpers + tests)
- [ ] No watch-mode flags (using `-count=1` to disable cache)
- [ ] Feedback latency < 30s for quick tier
- [ ] `nyquist_compliant: true` set in frontmatter (after Wave 0 completes)

**Approval:** pending
