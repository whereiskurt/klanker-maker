---
phase: 124-platform-wide-az-failover-and-capacity-feasibility-for-ec2-launches
plan: "01"
subsystem: capacity
tags: [capacity, ec2, az-failover, dynamodb, classifier, schema]
dependency_graph:
  requires: []
  provides: [pkg/capacity (classifier+store+rankaz-stub), GetCapacityTableName, spec.runtime.azPreference]
  affects: [pkg/profile/types.go, pkg/profile/schemas, internal/app/config/config.go, go.mod]
tech_stack:
  added: [github.com/aws/aws-sdk-go-v2/service/servicequotas v1.35.7]
  patterns: [narrow-DDB-interface, TDD-red-green, table-driven-tests]
key_files:
  created:
    - pkg/capacity/classifier.go
    - pkg/capacity/classifier_test.go
    - pkg/capacity/store.go
    - pkg/capacity/store_test.go
    - pkg/capacity/rankaz.go
    - pkg/profile/azpreference_test.go
    - pkg/compiler/service_hcl_azpref_test.go
  modified:
    - go.mod
    - go.sum
    - pkg/profile/types.go
    - pkg/profile/schemas/sandbox_profile.schema.json
    - internal/app/config/config.go
key_decisions:
  - "servicequotas SDK added in Task 3 commit (not Task 1) because go mod tidy removed it when nothing imported it; rankaz.go import anchors it permanently"
  - "CapacityDDBClient narrow interface (UpdateItem+GetItem only) follows pkg/aws SlackChannelGetPutAPI pattern for testability"
  - "ICETTLSeconds exported as const (not unexported) so store_test.go can assert the 2700s value without duplicating it"
  - "RankAZs stub returns allAZs unchanged — full implementation deferred to plan 124-04 as designed"
  - "azPreference byte-identity guard: two compiler tests verify the field never leaks into service.hcl output"
metrics:
  duration: 768s
  completed: 2026-06-28T22:16:12Z
  tasks: 3
  files: 11
---

# Phase 124 Plan 01: Wave-0 Foundation (capacity package + azPreference field) Summary

pkg/capacity package established with fully-implemented error classifier + DynamoDB capacity store + rankaz interface surface (stub body); servicequotas SDK added to go.mod; spec.runtime.azPreference validated additive []string field.

## What Was Built

### Task 1: servicequotas SDK + pkg/capacity error classifier
- `pkg/capacity/classifier.go`: `ErrorClass` iota taxonomy (9 constants: Success/ICE/SpotPrice/SpotLimit/WaiterTimeout/Quota/Auth/Invalid/Unknown), `ClassifyError(stderr string, err error) ErrorClass` pure function, `ShouldIterate() bool` predicate (true for ICE/SpotPrice/SpotLimit/WaiterTimeout).
- `pkg/capacity/classifier_test.go`: 19 table-driven cases (`TestClassifyError`) covering all taxonomy branches including `context.DeadlineExceeded` wrapping; `TestErrorClass_ShouldIterate` contract tests.
- `go.mod`/`go.sum`: `github.com/aws/aws-sdk-go-v2/service/servicequotas v1.35.7` (note: anchored by rankaz.go import in Task 3).

### Task 2: DynamoDB capacity store + GetCapacityTableName
- `pkg/capacity/store.go`: `CapacityStore` interface (RecordICE/RecordSuccess/Get), `CapacityEntry` struct (InstanceType, AZ, LastICEAt/LastSuccessAt *time.Time), `DynamoCapacityStore` impl, `CapacityDDBClient` narrow interface (UpdateItem+GetItem), `ICETTLSeconds = 2700` exported const. RecordICE writes `last_ice_at` + `ttl=now+2700`; RecordSuccess writes `last_success_at` with no TTL.
- `pkg/capacity/store_test.go`: `TestCapacityStore_RecordICE` (TTL epoch assertion), `TestCapacityStore_RecordSuccess` (no `:ttl` expr val), `TestCapacityStore_Get` (nil/non-nil timestamps + error propagation), `TestCapacityStore_ICETTLSeconds` (constant guard).
- `internal/app/config/config.go`: `GetCapacityTableName()` after `GetSlackChannelsTableName`; nil → `"km-capacity"`, else `GetResourcePrefix() + "-capacity"`.

### Task 3: rankaz interface surface + azPreference profile field/schema
- `pkg/capacity/rankaz.go`: `EC2OfferingsAPI` (DescribeInstanceTypeOfferings), `ServiceQuotasAPI` (GetServiceQuota) narrow interfaces; `GPUVCPUQuotaCode = "L-DB2E81BA"`, `GPUQuotaServiceCode = "ec2"` consts; `QuotaError{QuotaCode, Headroom}` with `Error()` method; `RankAZs(...)` signature with stub body returning allAZs unchanged.
- `pkg/profile/types.go`: `AZPreference []string` field added to `RuntimeSpec` after `Desktop` (yaml/json tag `azPreference,omitempty`).
- `pkg/profile/schemas/sandbox_profile.schema.json`: `azPreference` array property added inside `runtime.properties` (items minLength:1; `additionalProperties:false` schema remains valid).
- `pkg/profile/azpreference_test.go`: TestValidate_AZPreference (set), EmptyList, Absent — all three green.
- `pkg/compiler/service_hcl_azpref_test.go`: TestEC2ServiceHCL_AZPreferenceAbsent (nil/empty azPreference → byte-identical output), TestEC2ServiceHCL_AZPreferenceDoesNotAppearInHCL (literal token guard).

## Commits

| Task | Hash | Files |
|------|------|-------|
| 1: classifier + (go.mod/go.sum in T3) | 3dfa26b8 | pkg/capacity/classifier.go, classifier_test.go |
| 2: store + GetCapacityTableName | f150148b | pkg/capacity/store.go, store_test.go, internal/app/config/config.go |
| 3: rankaz + azPreference + go.mod | e925d53a | pkg/capacity/rankaz.go, pkg/profile/types.go, schema.json, azpreference_test.go, service_hcl_azpref_test.go, go.mod, go.sum |

## Verification

All plan verification gates passed:
- `go test ./pkg/capacity/... -timeout 60s` — PASS (classifier 19 cases + store 4 tests)
- `go test ./pkg/profile/... -run TestValidate_AZPreference` — PASS (3 cases)
- `go test ./pkg/compiler/... -run TestEC2ServiceHCL_AZPreferenceAbsent` — PASS (byte-identity + token guard)
- `go build ./...` — clean
- `go test ./pkg/... -timeout 120s` — all 19 pkg packages PASS (no regressions)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] go mod tidy removed servicequotas before rankaz.go existed**
- **Found during:** Task 1 commit verification
- **Issue:** `go get servicequotas` + `go mod tidy` removed the SDK entry because nothing imported it yet; Task 1 files (classifier.go) only use stdlib. The committed go.mod had no servicequotas entry.
- **Fix:** Re-ran `go get servicequotas` in Task 3 after creating rankaz.go, which imports it. The `import "github.com/aws/aws-sdk-go-v2/service/servicequotas"` in rankaz.go anchors the dependency permanently.
- **Files modified:** go.mod, go.sum (committed in e925d53a)
- **Impact:** go.mod/go.sum changes grouped with Task 3 commit instead of Task 1 — functionally identical, ordering difference only.

## Self-Check: PASSED

All artifacts verified:
- pkg/capacity/classifier.go: FOUND
- pkg/capacity/store.go: FOUND
- pkg/capacity/rankaz.go: FOUND
- pkg/profile/azpreference_test.go: FOUND
- pkg/compiler/service_hcl_azpref_test.go: FOUND
- servicequotas in go.mod: FOUND
- AZPreference in types.go: FOUND
- azPreference in schema: FOUND
- GetCapacityTableName in config.go: FOUND
- Commits 3dfa26b8, f150148b, e925d53a: ALL FOUND
