---
phase: 84
slug: ses-per-install-rule-namespacing-via-operator-address-prefix
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-16
---

# Phase 84 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Derived from `84-RESEARCH.md` § Validation Architecture (researcher-confirmed test framework + sampling rate + Wave 0 gaps).

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go standard `testing` package + `aws-sdk-go-v2` narrow-interface mocks |
| **Config file** | `go.mod` (existing — no new test framework) |
| **Quick run command** | `go test ./internal/app/cmd/... ./cmd/email-create-handler/... ./pkg/compiler/... ./pkg/aws/... -short -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | quick ~30s · full ~2-5min |

---

## Sampling Rate

- **After every task commit:** Run quick command (under 30s)
- **After every plan wave:** Run full suite command
- **Before `/gsd:verify-work`:** Full Go suite green + manual `terragrunt plan` against two-prefix scenario
- **Max feedback latency:** 30s for quick, 5min for full

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 84-W0-01 | Wave 0 | 0 | SES-CONFIGURE-WIRING | unit (stub) | `go test ./internal/app/cmd/ -run TestConfigure_DerivesOperatorEmailFromPrefix -count=1` | ❌ W0 | ⬜ pending |
| 84-W0-02 | Wave 0 | 0 | SES-CONFIGURE-WIRING | unit (stub) | `go test ./internal/app/cmd/ -run TestConfigure_BlankOperatorEmail_DerivesFromPrefix -count=1` | ❌ W0 | ⬜ pending |
| 84-W0-03 | Wave 0 | 0 | SES-CONFIGURE-WIRING | unit (stub) | `go test ./internal/app/cmd/ -run TestConfigure_ResetPrefix_ClearsOperatorEmail -count=1` | ❌ W0 | ⬜ pending |
| 84-W0-04 | Wave 0 | 0 | SES-HANDLER-LOOKUP | unit (stub) | `go test ./cmd/email-create-handler/ -run TestHandle_OperatorAddress_OwnPrefix -count=1` | ❌ W0 | ⬜ pending |
| 84-W0-05 | Wave 0 | 0 | SES-HANDLER-LOOKUP | unit (stub) | `go test ./cmd/email-create-handler/ -run TestHandle_OperatorAddress_ForeignPrefix_Drops -count=1` | ❌ W0 | ⬜ pending |
| 84-W0-06 | Wave 0 | 0 | SES-DOCTOR-ORPHANS | unit (stub) | `go test ./internal/app/cmd/ -run TestCheckSESRules_AllOwn -count=1` | ❌ W0 | ⬜ pending |
| 84-W0-07 | Wave 0 | 0 | SES-DOCTOR-ORPHANS | unit (stub) | `go test ./internal/app/cmd/ -run TestCheckSESRules_Orphans -count=1` | ❌ W0 | ⬜ pending |
| 84-W0-08 | Wave 0 | 0 | SES-DOCTOR-ORPHANS | unit infra (NEW FILE) | `go test ./internal/app/cmd/ -run TestCheckSESRules -count=1` | ❌ W0 — `doctor_ses_rules_test.go` | ⬜ pending |
| 84-W0-09 | Wave 0 | 0 | SES-PREFIX-ADDRESS | unit (NEW FILE) | `go test ./pkg/compiler/ -run TestUserdata_KmSendOperatorAddressUsesEnvVar -count=1` | ❌ W0 — `userdata_84_test.go` | ⬜ pending |
| 84-W0-10 | Wave 0 | 0 | SES-PREFIX-ADDRESS | unit (stub) | `go test ./pkg/aws/ -run TestSendCreateNotification_OperatorAddressUsesPrefix -count=1` | ❌ W0 — extend `ses_test.go` | ⬜ pending |
| 84-W0-11 | Wave 0 | 0 | SES-82.1-REMOVAL | grep-based CI | `! grep -rn "KM_SES_ACTIVATE_RULESET\\|activate_rule_set" infra/ internal/ pkg/ cmd/ OPERATOR-GUIDE.md CLAUDE.md` | ❌ W0 — Makefile target | ⬜ pending |
| 84-XX-XX | (planner assigns) | 1+ | SES-PREFIX-ADDRESS | unit | reuse W0-01..03,09,10 stubs | (covered) | ⬜ pending |
| 84-XX-XX | (planner assigns) | 1+ | SES-CONFIGURE-WIRING | unit | reuse W0-01..03 stubs | (covered) | ⬜ pending |
| 84-XX-XX | (planner assigns) | 1+ | SES-HANDLER-LOOKUP | unit | reuse W0-04..05 stubs | (covered) | ⬜ pending |
| 84-XX-XX | (planner assigns) | 1+ | SES-DOCTOR-ORPHANS | unit | reuse W0-06..08 stubs | (covered) | ⬜ pending |
| 84-XX-XX | (planner assigns) | 1+ | SES-82.1-REMOVAL | grep CI | reuse W0-11 | (covered) | ⬜ pending |
| 84-XX-XX | (planner assigns) | 1+ | SES-SHARED-RULESET | manual UAT | `cd infra/live/use1/ses-shared-rule-set && terragrunt plan` — verify resources planned | manual | ⬜ pending |
| 84-XX-XX | (planner assigns) | 1+ | SES-PER-INSTALL-RULES | manual UAT | `cd infra/live/use1/ses && terragrunt plan` — assert no rule_set + active_rule_set | manual | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

*Note: Wave 0 (W0-01..11) stubs are the failing-test infrastructure. Implementation tasks in Waves 1+ turn each stub green by adding production code; the planner maps each implementation task to one or more of these stubs in the per-task `<automated>` blocks.*

---

## Wave 0 Requirements

- [ ] `internal/app/cmd/configure_test.go` — add 3 stubs: `TestConfigure_DerivesOperatorEmailFromPrefix`, `TestConfigure_BlankOperatorEmail_DerivesFromPrefix`, `TestConfigure_ResetPrefix_ClearsOperatorEmail`
- [ ] `cmd/email-create-handler/main_test.go` — add 2 stubs: `TestHandle_OperatorAddress_OwnPrefix`, `TestHandle_OperatorAddress_ForeignPrefix_Drops`
- [ ] `internal/app/cmd/doctor_test.go` — add 2 stubs: `TestCheckSESRules_AllOwn`, `TestCheckSESRules_Orphans`
- [ ] `internal/app/cmd/doctor_ses_rules_test.go` — NEW FILE with `mockSESReceiptRuleAPI` interface mock (pattern from `doctor_slack_inbound_test.go`)
- [ ] `pkg/compiler/userdata_84_test.go` — NEW FILE asserting generated userdata references `${KM_OPERATOR_EMAIL}` not `operator@` literal (pattern from `userdata_82_02_test.go`)
- [ ] `pkg/aws/ses_test.go` — extend with `TestSendCreateNotification_OperatorAddressUsesPrefix`
- [ ] `Makefile` — add `test-no-82.1-leftovers` target with grep-based CI check: `! grep -rn "KM_SES_ACTIVATE_RULESET\|activate_rule_set" infra/ internal/ pkg/ cmd/ OPERATOR-GUIDE.md CLAUDE.md || (echo "Phase 82.1 leftovers found"; exit 1)`

*Production wiring of `checkSESRules` requires `aws-sdk-go-v2/service/ses` (classic v1, distinct from existing `sesv2`). Test code gates behind a narrow interface so test files do not pull the dependency until the implementation lands.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Foundation module plan is idempotent on second `km bootstrap` against same account/region | SES-SHARED-RULESET | Requires real AWS account state; tested as part of operator UAT | 1. `km bootstrap --shared-ses` on a fresh account → verify resources planned-create<br>2. Re-run same command → verify `register_shared_rule_set=false`, `register_domain_identity=false`, plan shows no-op |
| Two installs in same account/region coexist without colliding | End-to-end | Requires AWS account, two distinct `resource_prefix` configs, real email send | 1. `km init` under prefix `kph` → verify shared rule set + 2 rules created<br>2. `km init` under prefix `rg` → verify 2 more rules added, no churn on existing rules<br>3. `aws ses describe-receipt-rule-set --rule-set-name sandbox-email-shared` → 4 rules<br>4. Send `operator-kph@` → routes to kph S3<br>5. Send `operator-rg@` → routes to rg S3 |
| `km uninit` on one prefix leaves sibling install's rules intact | SES-PER-INSTALL-RULES | Real Terraform state teardown | 1. After two-install setup above, `terragrunt destroy` in `kph` install's regional `ses` dir<br>2. `aws ses describe-receipt-rule-set --rule-set-name sandbox-email-shared` → still 2 rules (rg's pair)<br>3. Verify rule set itself + domain identity persist |
| Foundation module remains intact when last install runs `km uninit` | SES-SHARED-RULESET | Foundation lifecycle is separate from regional | After all installs destroyed, foundation `sandbox-email-shared` + domain identity still present. Operator must explicitly `terragrunt destroy` foundation to fully tear down. |
| `km doctor` WARN surfaces orphans | SES-DOCTOR-ORPHANS | Requires real SES state with rules from un-configured prefixes | 1. Add a synthetic rule named `xx-operator-inbound` via AWS CLI<br>2. `km doctor` → expect `⚠ orphan SES rules: xx-operator-inbound` |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (11 stubs enumerated above)
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s for quick path, < 5min for full
- [ ] `nyquist_compliant: true` set in frontmatter once planner has mapped each implementation task to a stub

**Approval:** pending
