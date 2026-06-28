---
phase: 124
slug: platform-wide-az-failover-and-capacity-feasibility-for-ec2-launches
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-28
---

# Phase 124 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Derived from `124-RESEARCH.md` § Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go standard `testing` package |
| **Config file** | none — in-tree Go tests |
| **Quick run command** | `go test ./pkg/capacity/... -timeout 60s` |
| **Full suite command** | `go test ./... -timeout 600s` |
| **Estimated runtime** | ~10s quick · ~5-8min full suite |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/capacity/... -timeout 60s` (or the touched package's quick test)
- **After every plan wave:** Run `go test ./... -timeout 600s`
- **Before `/gsd:verify-work`:** Full suite green + `scripts/validate-all-profiles.sh` (20/20)
- **Max feedback latency:** ~60 seconds

> NB (project memory): capture the command's OWN exit code, not a piped `| tail` — `go test | tail` masks FAIL. Use `-timeout 600s` for the full suite.

---

## Per-Task Verification Map

| Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|------|------|-------------|-----------|-------------------|-------------|--------|
| 01 | 0 | REQ-124-CLASSIFY | unit | `go test ./pkg/capacity/... -run TestClassifyError` | ❌ W0 | ⬜ pending |
| 01 | 0 | REQ-124-STORE | unit | `go test ./pkg/capacity/... -run TestCapacityStore_RecordICE` | ❌ W0 | ⬜ pending |
| 01 | 0 | REQ-124-STORE | unit | `go test ./pkg/capacity/... -run TestCapacityStore_RecordSuccess` | ❌ W0 | ⬜ pending |
| 01 | 0 | REQ-124-SURFACE | golden | `go test ./pkg/compiler/... -run TestEC2ServiceHCL_AZPreferenceAbsent` | ❌ W0 | ⬜ pending |
| 01 | 0 | REQ-124-SURFACE | unit | `go test ./internal/app/cmd/... -run TestRunInitPlan_ModuleOrder` (26→27) | ✅ update | ⬜ pending |
| 01 | 0 | (deps) | build | `go get .../servicequotas` + `go build ./...` | ❌ W0 | ⬜ pending |
| 02 | 1 | REQ-124-CLASSIFY | unit | `go test ./cmd/create-handler/... -run TestNocapClassifier` | ❌ W0 | ⬜ pending |
| 02 | 1 | REQ-124-SWEEP | unit | `go test ./internal/app/cmd/... -run TestAZSweepLoop` | ❌ W0 | ⬜ pending |
| 03 | 1 | REQ-124-STORE | unit | `go test ./pkg/capacity/... -run TestCapacityStore` | ❌ W0 | ⬜ pending |
| 03 | 1 | REQ-124-WAITER | golden | `go test ./pkg/compiler/... -run TestEC2ServiceHCL_SpotTimeout` | ❌ W0 | ⬜ pending |
| 03 | 1 | REQ-124-WAITER | golden | `go test ./pkg/compiler/... -run TestEC2ServiceHCL_OnDemandNoTimeout` | ❌ W0 | ⬜ pending |
| 04 | 2 | REQ-124-RANK | unit | `go test ./pkg/capacity/... -run TestRankAZs_DropsNonOffering` | ❌ W0 | ⬜ pending |
| 04 | 2 | REQ-124-RANK | unit | `go test ./pkg/capacity/... -run TestRankAZs_GPUQuotaBlock` | ❌ W0 | ⬜ pending |
| 04 | 2 | REQ-124-RANK | unit | `go test ./pkg/capacity/... -run TestRankAZs_AZPreference` | ❌ W0 | ⬜ pending |
| 04 | 2 | REQ-124-RANK | unit | `go test ./pkg/capacity/... -run TestRankAZs_ICEStickySuccess` | ❌ W0 | ⬜ pending |
| 04 | 2 | REQ-124-SWEEP | unit | `go test ./pkg/capacity/... -run TestRankAZs` | ❌ W0 | ⬜ pending |
| 04 | 2 | REQ-124-SURFACE | unit | `go test ./pkg/profile/... -run TestValidate_AZPreference` | ❌ W0 | ⬜ pending |
| 04 | 2 | REQ-124-CAPCMD | smoke | `km capacity --type g6e.12xlarge` (stub asserts 5-col report) | ❌ W0 | ⬜ pending |
| 05 | 3 | REQ-124-SURFACE | unit | `go test ./internal/app/cmd/... -run TestNewCreateCmd_WaitForCapacityFlag` | ❌ W0 | ⬜ pending |
| 05 | 3 | REQ-124-SURFACE | unit | `go test ./internal/app/cmd/... -run TestDoctor_CapacityChecks` | ❌ W0 | ⬜ pending |
| 06 | 4 | REQ-124-UAT | live | manual UAT gates (see Manual-Only) | n/a | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/capacity/classifier.go` + `classifier_test.go` — `ClassifyError` + `ErrorClass` consts; 15+ real AWS error-string fixtures
- [ ] `pkg/capacity/store.go` + `store_test.go` — `CapacityStore` interface + DynamoDB impl + `CapacityEntry`; TTL write assertions
- [ ] `pkg/capacity/rankaz.go` + `rankaz_test.go` — `RankAZs`, `EC2OfferingsAPI`, `ServiceQuotasAPI` (mocked offerings + quota + store)
- [ ] `infra/modules/dynamodb-capacity/v1.0.0/{main,variables,outputs}.tf` — new DDB module
- [ ] `infra/live/use1/dynamodb-capacity/terragrunt.hcl` — live unit
- [ ] `go get github.com/aws/aws-sdk-go-v2/service/servicequotas` — add to go.mod/go.sum
- [ ] `init_plan_test.go` — bump module-order assertion 26→27 + add `dynamodb-capacity` to `allModuleNames` mock dir list (else plan tests fail on missing dir)

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| GPU sandbox launch fails over 1a→1c (g6e/g6 12xlarge) | REQ-124-UAT | needs live GPU capacity + G-vCPU quota (`L-DB2E81BA`) | `km create profiles/gpu-qwen-12x-l4.yaml` from an account with quota; observe sweep skips 1a, lands 1c; sandbox boots + serves |
| quota=0 produces fail-fast naming `L-DB2E81BA` | REQ-124-UAT | requires a 0-quota account/region to trigger genuinely | run create in a region with G-quota 0; assert immediate fail-fast + quota-named remediation, NOT a 4-AZ sweep |
| `km capacity` per-AZ report accuracy | REQ-124-UAT, REQ-124-CAPCMD | depends on live `DescribeInstanceTypeOfferings` + Service Quotas | `km capacity --type g6e.12xlarge`; cross-check offered/not-offered against AWS console |
| all 4 public subnets provisioned | REQ-124-UAT | infra assertion against live VPC | `aws ec2 describe-subnets --filters Name=vpc-id,Values=$VPC_ID` → 4 AZs |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references (pkg/capacity, dynamodb-capacity module, servicequotas SDK)
- [ ] No watch-mode flags
- [ ] Feedback latency < 60s
- [ ] `nyquist_compliant: true` set in frontmatter (set by planner once Wave 0 stubs land)

**Approval:** pending
