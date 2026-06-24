---
phase: 117
slug: composable-multi-parent-profile-inheritance-deep-merge-list-union-extends
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-24
---

# Phase 117 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing stdlib |
| **Config file** | none — standard `go test` |
| **Quick run command** | `go test ./pkg/profile/... -count=1 -timeout 60s` |
| **Full suite command** | `go test ./pkg/profile/... ./pkg/compiler/... -count=1 -timeout 600s` |
| **Estimated runtime** | ~30s quick · ~3–5min full (compiler suite dominates) |

---

## Sampling Rate

- **After every task commit:** `go test ./pkg/profile/... -count=1 -timeout 60s`
- **After every plan wave:** `go test ./pkg/profile/... ./pkg/compiler/... -count=1 -timeout 600s`
- **Before `/gsd:verify-work`:** full suite green **+** `bash scripts/validate-all-profiles.sh` **+** `./km validate profiles/dc34.yaml` green
- **Max feedback latency:** ~30s (quick), ~5min (phase gate)

---

## Per-Task Verification Map

| Behavior | Plan | Test Type | Automated Command | File |
|----------|------|-----------|-------------------|------|
| `string \| []string` extends unmarshal | 01 | unit | `go test ./pkg/profile/... -run TestExtendsUnmarshal` | pkg/profile/inherit_test.go (add) |
| goccy custom UnmarshalYAML signature compile-check | 01 | unit | `go test ./pkg/profile/... -run TestExtendsUnmarshal` | pkg/profile/inherit_test.go (add) |
| JSON schema accepts string OR array `extends` | 01 | unit | `go test ./pkg/profile/... -run TestValidateSchema` | schemas/sandbox_profile.schema.json + validate_test.go |
| Fragment marker `metadata.abstract:true` detected | 01 | unit | `go test ./pkg/profile/... -run TestIsAbstractFragment` | pkg/profile/validate_test.go (add) |
| Scalar last-wins deep merge | 02 | unit | `go test ./pkg/profile/... -run TestDeepMerge_ScalarWins` | pkg/profile/inherit_test.go (add) |
| Nested map key-union deep merge | 02 | unit | `go test ./pkg/profile/... -run TestDeepMerge_MapUnion` | pkg/profile/inherit_test.go (add) |
| List concat+dedup (string lists) | 02 | unit | `go test ./pkg/profile/... -run TestDeepMerge_ListDedup` | pkg/profile/inherit_test.go (add) |
| List concat+dedup (object lists / additionalSnapshots) | 02 | unit | `go test ./pkg/profile/... -run TestDeepMerge_ObjectListDedup` | pkg/profile/inherit_test.go (add) |
| Multi-parent ordering (left→right→child) | 02 | unit | `go test ./pkg/profile/... -run TestResolve_MultiParentOrder` | pkg/profile/inherit_test.go (add) |
| Diamond inheritance idempotence | 02 | unit | `go test ./pkg/profile/... -run TestResolve_Diamond` | pkg/profile/inherit_test.go (add) |
| Diamond: base resolved once (memoization) | 02 | unit | `go test ./pkg/profile/... -run TestResolve_DiamondMemoized` | pkg/profile/inherit_test.go (add) |
| Cycle detection still catches true cycles (DAG) | 02 | unit | `go test ./pkg/profile/... -run TestResolveCircularDetection` | pkg/profile/inherit_test.go (existing) |
| Depth guard raised for DAG | 02 | unit | `go test ./pkg/profile/... -run TestResolveDepthExceeded` | pkg/profile/inherit_test.go (update limit) |
| Notification + Agent blocks still merge field-level | 02 | regression | `go test ./pkg/profile/... -run TestInherit` | inherit_notification_test.go / inherit_agent_test.go (must stay green) |
| `extends` resolved before validate at call sites | 03 | unit | `go test ./internal/app/cmd/... -run TestValidate` | validate.go / create.go (3 call-site fixes) |
| validate-all skips base/ fragments, leaves still valid | 03 | integration | `bash scripts/validate-all-profiles.sh` | scripts/validate-all-profiles.sh (add base/ skip) |
| `km validate profiles/dc34.yaml` multi-parent resolves | 04 | smoke | `./km validate profiles/dc34.yaml` | manual |
| learn.v2 byte-identity golden (refactor gate) | 04 | regression | `go test ./pkg/compiler/... -run TestUserdataLearnV2Phase92ByteIdentity` | userdata_phase92_byte_identity_test.go (helper → Resolve()) |
| Compiled-userdata equivalence after refactor | 04 | regression | userdata byte-diff before/after | manual + byte-diff |

*Status legend: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

New test scaffolding to write before/with implementation:
- [ ] `pkg/profile/inherit_test.go` — add `TestExtendsUnmarshal`, `TestDeepMerge_*` (ScalarWins/MapUnion/ListDedup/ObjectListDedup), `TestResolve_Diamond`, `TestResolve_DiamondMemoized`, `TestResolve_MultiParentOrder` (Plan 01/02)
- [ ] `pkg/profile/validate_test.go` — add `TestIsAbstractFragment` (Plan 01)
- [ ] `pkg/profile/testdata/profiles/{diamond-base,diamond-a,diamond-b,diamond-child}.yaml` — diamond fixtures (Plan 02)
- [ ] `pkg/profile/testdata/profiles/multi-parent-child.yaml` + `base-a.yaml` + `base-b.yaml` — multi-parent fixtures (Plan 02)

*The existing `go test ./pkg/profile/... -count=1` suite is currently green and MUST remain green through every plan (it is the regression net for the merger-zoo replacement).*

---

## Manual-Only Verifications

| Behavior | Plan | Why Manual | Test Instructions |
|----------|------|------------|-------------------|
| Compiled userdata equivalence after dc34/learn.v2.* refactor | 04 | Requires building km + compiling each profile and byte-diffing the generated userdata against the pre-refactor output | `make build`; for each refactored leaf, `km` compile path → capture userdata; `diff` vs pre-refactor capture; the `learn.v2` golden test automates the frozen reference, others are operator byte-diff |
| `km validate` UX on a partial fragment (helpful error / skip) | 03 | Human judgement on message clarity | `./km validate profiles/base/safenetwork.yaml` → expect a clear "abstract fragment, not independently valid" message, not a crash |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s (quick) / < 5min (gate)
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
