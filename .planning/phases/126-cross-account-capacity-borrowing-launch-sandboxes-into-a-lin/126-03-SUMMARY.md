---
phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin
plan: 03
subsystem: aws-sdk
tags: [go, sts, assume-role, dynamodb, capacity, cross-account]

requires:
  - phase: 126-01
    provides: launch_accounts config block + Config.GetLaunchAccount(s) getters, RuntimeSpec.LaunchAccount schema field, ValidateLaunchAccountLink
  - phase: 126-02
    provides: conditional cross-account provider override in the sandbox terragrunt template + service.hcl launch_account locals
provides:
  - "pkg/aws.AssumeRoleConfig(ctx, base, roleARN, externalID, region) (aws.Config, error) — fail-closed, cache-wrapped, region-pinned cross-account credential helper; first stscreds use in the repo"
  - "pkg/aws.AssumeRoleSTSAPI narrow interface + pkg/aws.NewAssumeRoleSTSClient exported test seam"
  - "pkg/aws.SandboxMetadata.LaunchAccount string (launch_account DynamoDB attribute), wired into all three unmarshal chokepoints and the symmetric write-side emission"
  - "pkg/capacity.DynamoCapacityStore.accountNS + NewDynamoCapacityStore's third constructor argument; itemKey converted to a method that namespaces the hash-key VALUE only"
affects: [126-06, 126-08]

tech-stack:
  added:
    - "github.com/aws/aws-sdk-go-v2/credentials/stscreds (promoted from indirect to direct dependency; already hash-pinned in go.sum, no new resolution)"
  patterns:
    - "Package-level var of func type (not a func declaration) as the seam for both the operation itself (AssumeRoleConfig) and its STS-client constructor (NewAssumeRoleSTSClient), matching the existing LoadAWSConfig/LoadAWSConfigInRegion convention in client.go — lets external _test packages substitute a stub without touching call sites"
    - "Per-concern dedicated round-trip test file (sandbox_dynamo_launch_account_test.go mirrors sandbox_dynamo_network_test.go) for a DynamoDB attribute that must survive a full-row PutItem read-modify-write cycle"
    - "Namespace the DynamoDB hash-key VALUE inside the store, never the domain argument (instanceType) that other call sites (EC2 offerings filter, GPU-family check) also consume"

key-files:
  created:
    - pkg/aws/assume.go
    - pkg/aws/assume_test.go
    - pkg/aws/sandbox_dynamo_launch_account_test.go
  modified:
    - pkg/aws/metadata.go
    - pkg/aws/sandbox_dynamo.go
    - pkg/capacity/store.go
    - pkg/capacity/store_test.go
    - internal/app/cmd/create.go
    - internal/app/cmd/capacity.go
    - go.mod

key-decisions:
  - "AssumeRoleConfig's STS-client construction is a second, separate package-level var (NewAssumeRoleSTSClient) rather than a parameter on AssumeRoleConfig itself — keeps the public signature exactly as specified (ctx, base, roleARN, externalID, region) while still giving external _test packages (aws_test) a seam to inject a stub STS client and assert on the real Retrieve()/cache/ExternalId logic, not a bypassed test double."
  - "RoleSessionName is a fixed constant (\"km-cross-account-capacity\") and Duration is set explicitly (15 minutes) rather than left at the SDK default, so both are documented in one place per the plan's action text."
  - "Both NewDynamoCapacityStore call sites (create.go, capacity.go) pass accountNS=\"\" in this plan, with a comment pointing at plan 06 as the place a resolved link's account id gets threaded through — this plan only lands the primitive, not its home-account-vs-linked-account wiring."

requirements-completed: [REQ-126-CAPACITY]

coverage:
  - id: T1
    description: "pkg/aws.AssumeRoleConfig fails closed (no fallback to caller's own credentials), caches credentials, sets ExternalId only when non-empty, and is unit-tested against a stub STS client"
    requirement: "REQ-126-CAPACITY"
    tests: [TestAssumeRoleConfig_Success_PreservesBaseAndSetsRegion, TestAssumeRoleConfig_Failure_SurfacesErrorNoFallback, TestAssumeRoleConfig_ExternalID, TestAssumeRoleConfig_CachesCredentials, TestAssumeRoleConfig_RoleSessionNameIdentifiesKM, TestAssumeRoleConfig_EmptyArgsFailClosed]
  - id: T2
    description: "SandboxMetadata.LaunchAccount round-trips through marshal/unmarshal, including a double round trip simulating resume-then-extend, and a pre-126 row with no launch_account attribute unmarshals to empty with no panic"
    requirement: "REQ-126-CAPACITY"
    tests: [TestLaunchAccount_RoundTrip, TestLaunchAccount_UnmarshalFromItem, TestLaunchAccount_OmittedWhenEmpty, TestLaunchAccount_AbsentAttributeIsEmpty, TestLaunchAccount_SurvivesDoubleRoundTrip, TestLaunchAccount_ListAllSandboxMetadataDynamo_RoundTrip]
  - id: T3
    description: "DynamoCapacityStore keys are namespaced only inside the constructor; the home account's empty namespace produces the pre-Phase-126 bare key unchanged; the instanceType argument fed to the EC2 offerings filter and GPU-family check is never rewritten"
    requirement: "REQ-126-CAPACITY"
    tests: [TestCapacityStore_AccountNamespace_EmptyProducesBareKey, TestCapacityStore_AccountNamespace_NonEmptyPrefixesKey, TestCapacityStore_AccountNamespace_AllMethodsRouteThroughNamespacedKey, TestCapacityStore_SatisfiesCapacityStoreInterface]

metrics:
  duration: "~1h"
  completed: 2026-08-22
status: complete
---

# Phase 126 Plan 03: Cross-account primitives — fail-closed AssumeRole, LaunchAccount round-trip, namespaced capacity store Summary

Lands three pure-library, mocked-test primitives that every later Phase 126 plan builds on: a fail-closed `stscreds`-based cross-account credential helper in `pkg/aws`, a `LaunchAccount` DynamoDB attribute that survives the sandbox metadata round trip, and a `DynamoCapacityStore` constructor argument that namespaces capacity rows per account without ever touching the instance-type string the EC2/quota lookups consume.

## What Was Built

**Task 1 — `pkg/aws.AssumeRoleConfig`** (`pkg/aws/assume.go`, `pkg/aws/assume_test.go`): the first use of `stscreds` in this repo. Constructs an STS client from the base config via an exported, overridable var (`NewAssumeRoleSTSClient`), wraps `stscreds.NewAssumeRoleProvider` in `aws.NewCredentialsCache`, sets `RoleSessionName` to a fixed km identifier and `Duration` to 15 minutes explicitly, sets `ExternalID` only when the argument is non-empty (a nil pointer, not empty string — AWS rejects the latter), validates `roleARN`/`region` are non-empty up front, and returns a copied base config with `Credentials`/`Region` swapped. The doc comment explicitly calls out the anti-pattern it does NOT copy: `internal/app/cmd/uninit.go`'s existing AssumeRole path falls back to the caller's own credentials on failure, which is correct for best-effort SCP cleanup and would be a serious bug here — a failed AssumeRole into the linked account must fail the capacity check, not silently report the home account's quota.

Six tests cover: base-config preservation + region pinning on success, error surfacing with no silent fallback to base credentials (asserted by inequality, not just non-nil error), ExternalId pointer semantics (set when non-empty, nil when empty), credential caching (two `Retrieve()` calls within the credential lifetime issue exactly one `AssumeRole` call against the stub), a non-empty km-identifying `RoleSessionName`, and argument-validation fail-closed behavior for empty `roleARN`/`region`.

**Task 2 — `SandboxMetadata.LaunchAccount`** (`pkg/aws/metadata.go`, `pkg/aws/sandbox_dynamo.go`, `pkg/aws/sandbox_dynamo_launch_account_test.go`): a new `launch_account` DynamoDB attribute, doc-commented with the same round-trip discipline as the Phase 125 `NetworkPlacement` field it sits beside — naming every read-modify-write path (resume, extend, ttl-handler) and the concrete failure mode (a stripped field strands a running linked-account instance that neither `km destroy` nor the ttl-handler can reach). A new `unmarshalLaunchAccountField` helper (kept separate from `unmarshalNetworkFields` per the plan's instruction — different phases, different concerns, honest call-site grep) is wired into the same three entry points: `ReadSandboxMetadataDynamo`, `ListAllSandboxesByDynamo`, and `ListAllSandboxMetadataDynamo`. The symmetric write-side emission sits next to `network_placement`'s, guarded on non-empty so home-account rows stay byte-identical.

Six tests mirror `sandbox_dynamo_network_test.go`'s list: single-item round trip (with attribute-type assertion), unmarshal-only, omitted-when-empty, absent-attribute-reads-as-empty (the pre-126-row guard), a double round trip (marshal → unmarshal → marshal, simulating the resume-then-extend PutItem sequence), and the list-path (`ListAllSandboxMetadataDynamo`) entry point.

**Task 3 — namespaced capacity store key** (`pkg/capacity/store.go`, `pkg/capacity/store_test.go`, `internal/app/cmd/create.go`, `internal/app/cmd/capacity.go`): `DynamoCapacityStore` gains an `accountNS string` field; `NewDynamoCapacityStore` gains a third `accountNS` parameter. The free function `itemKey` became a method `(*DynamoCapacityStore).itemKey` that prefixes the hash-key *value* with `"{accountNS}#"` when `accountNS` is non-empty, otherwise returns the bare instance type — exactly the pre-Phase-126 key. The struct's table-schema doc comment now records both the namespaced key form and, per the plan's Pitfall 5 warning, why the home account's namespace must stay the empty string forever: success rows carry no TTL and persist indefinitely, so namespacing home "for symmetry" would orphan every accumulated sticky-AZ record with no recovery path. Both existing construction call sites (`create.go`'s AZ-ranking block, `capacity.go`'s `runCapacity`) now pass `""` with a comment pointing at plan 06 as the place a resolved link's account id gets threaded through. The `CapacityStore` interface itself is untouched.

New tests: empty-namespace-produces-bare-key (locks in the pre-change behavior as a named test so a future refactor can't silently namespace home), non-empty-namespace-prefixes-key (asserting the az range key is untouched), all three interface methods (`RecordICE`/`RecordSuccess`/`Get`) route through the namespaced key against a stub DDB client, and a compile-time assertion that `*DynamoCapacityStore` still satisfies `CapacityStore`.

## Verification

- `go test ./pkg/aws/... -run 'AssumeRole' -count=1` — PASS (6/6 subtests)
- `go test ./pkg/aws/... -run 'LaunchAccount' -count=1` — PASS (6/6 subtests)
- `go test ./pkg/aws/... -count=1` — PASS, no existing metadata test regressed
- `go test ./pkg/capacity/... -count=1` — PASS, including the new `AccountNamespace`/`SatisfiesCapacityStoreInterface` tests
- `go build ./...` — exits 0
- `go vet ./pkg/aws/... ./pkg/capacity/... ./internal/app/cmd/...` — clean
- `git diff --stat go.mod go.sum` — `credentials` moved from `// indirect` to a direct require line; no new hash lines (already pinned)
- `grep -c 'stscreds' pkg/aws/assume.go` = 3 (≥ 2 required)
- Doc comment on `AssumeRoleConfig` contains "FAIL-CLOSED" (case-insensitive match on both `fail` and `closed`) and names the wrong-account-quota consequence
- `grep -c 'launch_account' pkg/aws/sandbox_dynamo.go` = 5 (≥ 3 required)
- `unmarshalNetworkFields(` and `unmarshalLaunchAccountField(` call-site counts both = 4 (1 definition + 3 call sites each) — equal, as required
- `grep -c 'NewDynamoCapacityStore(' internal/app/cmd/create.go internal/app/cmd/capacity.go` = 1 each, both passing three arguments

## Deviations from Plan

None — plan executed exactly as written. One clarification on the STS-client injection mechanism: the plan's Task 1 action text describes constructing the STS client "from `base`" inside `AssumeRoleConfig` without specifying how a stub STS client reaches the tests, since `AssumeRoleConfig`'s signature takes only `base aws.Config`, not an injectable client. Resolved by adding a second, separate exported package-level var (`NewAssumeRoleSTSClient`) as the STS-client constructor seam — consistent with the plan's own framing ("the new helper follows the same seam convention" as `LoadAWSConfig`) and the read_first pointer at `client.go`'s var-not-func pattern. This lets `aws_test` (an external test package, matching this package's existing test-file convention) exercise the real `Retrieve()`/cache/`ExternalId` logic against a stub, rather than bypassing `AssumeRoleConfig`'s internals entirely.

### Pre-existing, unrelated test failure observed during verification

`go test ./internal/app/cmd/... -timeout 600s -count=1` fails 5 subtests: `TestBootstrapSCPApplyPath`, `TestBootstrapSCPSkipped_OrganizationBlank`, `TestClusterAdd`, `TestClusterRm`, `TestClusterAddPersistFailure`. These are pre-existing and environmental, not a regression from this plan:
- This plan's diff touches only `pkg/aws/{assume.go,assume_test.go,metadata.go,sandbox_dynamo.go,sandbox_dynamo_launch_account_test.go}`, `pkg/capacity/{store.go,store_test.go}`, `internal/app/cmd/{create.go,capacity.go}`, and `go.mod` — never `bootstrap.go` or `cluster.go`.
- `internal/app/cmd/main_test.go`'s `TestMain` globally overrides the package's `LoadAWSConfig` seam so every command test gets a credential provider that errors instantly (`"test: no real AWS credentials (fast-fail seam)"`) rather than resolving real SSO/instance-role credentials. Bootstrap and Cluster paths are among the small set of commands that still need to resolve real credentials for parts of their flow, so they fail in this local environment regardless of what else changed — matching the existing project note that "Bootstrap/Cluster/Configure fail locally as a result" of this seam.
- The team-lead's stated known-preexisting-failure (`TestRunner_Apply_ContextCancellationKillsSubprocess` in `pkg/terragrunt`) is a different package and was not hit; this is a second, separately-known local-environment gap in the same excluded-from-`make-test` package.

## Self-Check: PASSED

- `pkg/aws/assume.go` — FOUND
- `pkg/aws/assume_test.go` — FOUND
- `pkg/aws/sandbox_dynamo_launch_account_test.go` — FOUND
- Commit `576b6327` (Task 1) — FOUND in `git log --oneline --all`
- Commit `4702ffeb` (Task 2) — FOUND in `git log --oneline --all`
- Commit `52174291` (Task 3) — FOUND in `git log --oneline --all`
