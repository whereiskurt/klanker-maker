---
phase: 126
slug: cross-account-capacity-borrowing-launch-sandboxes-into-a-linked-capacity-account-gpu-motivated
status: approved
nyquist_compliant: true
wave_0_complete: false
created: 2026-08-22
---

# Phase 126 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Derived from `126-RESEARCH.md` § Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` (stdlib) — no external test framework |
| **Config file** | none — plain `go test` |
| **Quick run command** | `go test ./pkg/capacity/... ./pkg/aws/... ./pkg/profile/... ./internal/app/config/...` |
| **Full suite command** | `make test` **plus** `go test ./internal/app/cmd/... -timeout 600s` **plus** `go test ./cmd/ttl-handler/... -timeout 600s` |
| **Estimated runtime** | ~30s quick; ~10 min full (the two excluded packages dominate) |

**⚠ Makefile exclusion trap (research finding — load-bearing for this phase).**
`Makefile:76`'s `test` target excludes `cmd/ttl-handler`, `internal/app/cmd`, `cmd/km-slack`,
and `pkg/compiler` from `go test ./...`:

```
test:
	go test $$(go list ./... | grep -v 'cmd/km-slack' | grep -v 'cmd/ttl-handler' | grep -v 'internal/app/cmd' | grep -v 'pkg/compiler')
```

**Nearly all of this phase's new and changed Go code lives in exactly those excluded packages**
(`internal/app/cmd/account.go`, `create.go`, `capacity.go`, `destroy.go`, `doctor.go`;
`cmd/ttl-handler/main.go`). `make test` alone is therefore **not** a valid phase gate here — it
would report green while covering almost none of the phase. Both extra invocations are mandatory,
with the extended timeout (the AWS-config fast-fail seams make some of these slow in isolation).

**Exit-code discipline:** per `[[feedback_check_go_test_exit_not_pipe]]`, every verification step
must check the `go test` command's own exit code. `go test ... | tail` returns tail's `0` and
masks a FAIL.

---

## Sampling Rate

- **After every task commit:** package-scoped `go test ./<changed-package>/...`
- **After every plan wave:** `make test` + `go test ./internal/app/cmd/... -timeout 600s` +
  `go test ./cmd/ttl-handler/... -timeout 600s`, each with an explicit exit-code check
- **Before `/gsd-verify-work`:** all three commands green
- **Max feedback latency:** ~30s per task; ~10 min per wave

---

## Per-Task Verification Map

*Populated by the planner — one row per task once PLAN.md files exist. Requirement→test mapping
below is the contract those rows must satisfy.*

| Req ID | Behavior | Test Type | Automated Command | File Exists |
|--------|----------|-----------|-------------------|-------------|
| REQ-126-CFG | `launch_accounts` merge-list entry + unmarshal round-trip | unit | `go test ./internal/app/config/... -run TestLoad` | ❌ W0 (new subtest, existing file) |
| REQ-126-CFG | `km validate` rejects unknown link name; rejects `launchAccount` + `privateSubnet` | unit | `go test ./pkg/profile/... -run TestValidateLaunchAccount` | ❌ W0 (new file) |
| REQ-126-ENROLL | launcher/box role + boundary HCL generation; three trust principals present | unit | `go test ./internal/app/cmd/... -run TestAccountAdd -timeout 600s` | ❌ W0 |
| REQ-126-REGISTER | link persisted to km-config.yaml; artifacts grant statement added/removed | unit | `go test ./internal/app/cmd/... -run TestAccountRegister -timeout 600s` | ❌ W0 |
| REQ-126-LAUNCH | provider override renders with **and** without `launch_account` | integration (terragrunt render, no AWS) | Go test shelling out to `terragrunt render --json` against the real template, asserting on JSON — mirrors `pkg/terragrunt/substrate_version_pin_test.go` | ❌ W0 — **no existing automated coverage for terragrunt HCL behavior** |
| REQ-126-CAPACITY | `AssumeRoleConfig` composes; store namespacing | unit | `go test ./pkg/aws/... -run TestAssumeRoleConfig` / `go test ./pkg/capacity/... -run TestNamespacedItemKey` | ❌ W0 |
| REQ-126-TEARDOWN | `SandboxMetadata.LaunchAccount` round-trip | unit | `go test ./pkg/aws/... -run TestLaunchAccount_RoundTrip` (mirrors `sandbox_dynamo_network_test.go`) | ❌ W0 |
| REQ-126-TEARDOWN | cold-clone path reads DDB **before** the account-scoped tag-scan | unit (mocked DynamoClient/TagAPI) | `go test ./internal/app/cmd/... -run TestDestroyColdClone -timeout 600s` | ❌ W0 |
| REQ-126-DOCTOR | link reachable / launcher assumable checks | unit (mocked STS) | `go test ./internal/app/cmd/... -run TestDoctorAccountLink -timeout 600s` | ❌ W0 |
| REQ-126-UAT | live launcher containment proof | manual-only | `aws iam simulate-principal-policy` 8-row matrix (RESEARCH § Security Domain) | N/A |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/profile/validate_launch_account_test.go` — new file, REQ-126-CFG unknown-link rejection
- [ ] `pkg/aws/sandbox_dynamo_launch_account_test.go` — new file, REQ-126-TEARDOWN metadata round-trip
- [ ] `pkg/aws/assume_test.go` — new file, REQ-126-CAPACITY `AssumeRoleConfig` helper
      (mockable via a fake `AssumeRoleAPIClient` — the interface-narrowing pattern already used
      throughout `pkg/aws` / `pkg/capacity`)
- [ ] A terragrunt-render test harness for REQ-126-LAUNCH — the one genuinely new *kind* of test
      in this phase. Without it, the provider-override mechanism has no regression guard, and it
      is the mechanism the whole phase rests on.
- [ ] No new test framework or config needed — Go stdlib `testing` covers everything

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Launcher containment ("can do nothing but the bounded GPU launch") | REQ-126-UAT | Needs a real role in account B; `simulate-principal-policy` evaluates against live IAM | 8-row allow/deny matrix in `126-RESEARCH.md` § Security Domain |
| GPU box actually launches in B and is reachable | REQ-126-UAT | Real EC2 spend (~$10.49/hr), real cross-account assume | `km capacity <gpu-profile>` must report B's 768-vCPU headroom, not A's 64; then `km create` local **and** remote; reach via `km shell` / `km model start` |
| Unattended TTL reap of a B-hosted box | REQ-126-UAT | Requires waiting out a real TTL with the ttl-handler Lambda deployed | Create with a short TTL, confirm the instance is gone from B and the DDB row is cleaned in A |
| `--enable-bedrock` model access in B | REQ-126-ENROLL | Bedrock model-access enablement is partly console-gated | Out of scope for automated verification; `km account add` prints the reminder |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references — all four Wave-0 test files are delivered inside
      Wave-1 plans' TDD tasks
- [x] No watch-mode flags
- [x] Feedback latency < 600s
- [x] Every verification step checks the `go test` exit code directly, never through a pipe
      (plan-checker confirmed zero pipe-masked verify blocks across all ten plans)
- [x] Wave gates run the excluded packages explicitly, not just `make test`
- [x] `nyquist_compliant: true` set in frontmatter
- [ ] `wave_0_complete` — stays false until the Wave-1 plans actually execute and the four
      test files exist on disk. This is an execution-time flag, not a planning-time one.

**Approval:** approved 2026-08-22 (plan-checker: VERIFICATION PASSED, 0 blockers)

**Gate correction (post-planning):** the planner extended the three-command gate to **four** —
`make test` also excludes `pkg/compiler`, which plan 02 modifies (`service_hcl.go`). Plan 10's
phase gate is seven commands: `make test`, the three excluded packages, `validate-all-profiles.sh`,
`terraform validate`/`fmt` on both modules, and `go build ./...`.
