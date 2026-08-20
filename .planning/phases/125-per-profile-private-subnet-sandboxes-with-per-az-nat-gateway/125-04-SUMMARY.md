---
phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway
plan: 04
subsystem: aws
tags: [dynamodb, sandbox-metadata, round-trip, lifecycle]

requires:
  - phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway (plan 03)
    provides: spec.network.privateSubnet per-profile field (the source that will eventually populate this attribute at km create time)
provides:
  - "SandboxMetadata.NetworkPlacement string field (json:\"network_placement,omitempty\")"
  - "network_placement DynamoDB attribute (type S), written by marshalSandboxItem, read by unmarshalNetworkFields"
  - "unmarshalNetworkFields(item, meta) helper wired into all three raw-item-to-SandboxMetadata entry points"
affects: [125-06, 125-07, resume, extend, ttl-handler, km-doctor, km-init]

tech-stack:
  added: []
  patterns:
    - "Schema-on-write DynamoDB attribute (no table migration): guard the write on non-empty, add a small unmarshalXFields(item, meta) helper, wire it into every place a raw item becomes a SandboxMetadata — mirrors slack_allow/action_limits"

key-files:
  created:
    - pkg/aws/sandbox_dynamo_network_test.go
  modified:
    - pkg/aws/metadata.go
    - pkg/aws/sandbox_dynamo.go

key-decisions:
  - "unmarshalNetworkFields is wired into all THREE raw-item-unmarshal call sites (ReadSandboxMetadataDynamo, ListAllSandboxesByDynamo, ListAllSandboxMetadataDynamo), not the two the plan's read_first section named. The plan's line-number references (toSandboxMetadata/unmarshalSandboxItem) describe the sandboxItemDynamo struct-conversion primitives, which are always called together and have no direct access to the raw item map at their call boundary — they are not the actual chokepoint that Slack/GitHub/Failure/Frozen-style helpers hook into. The real convention already in this file (grep confirmed) is 3 external call sites, and the plan's own success criterion (\"survives a round trip through EVERY unmarshal entry point\") requires covering all 3, not 2. See Deviations."
  - "NetworkPlacement is intentionally NOT added to SandboxRecord/metadataToRecord in this plan (out of scope per files_modified). It mirrors the existing precedent of SlackMentionOnly/SlackReactAlways/SlackAllow, which are also absent from the lossy SandboxRecord type; Plan 06/07 guards are expected to consume it via the richer ListAllSandboxMetadataDynamo (same pattern as the existing Slack doctor checks, per that function's own doc comment)."

requirements-completed: [REQ-125-GUARD]

coverage:
  - id: D1
    description: "A sandbox's network placement is recorded on its DynamoDB row and survives every read-modify-write lifecycle path (pause, resume, extend, ttl-handler) because the write and all three read entry points share the single marshalSandboxItem/unmarshalNetworkFields chokepoint that resume.go, extend.go, and cmd/ttl-handler already funnel through unmodified"
    requirement: "REQ-125-GUARD"
    verification:
      - kind: unit
        ref: "pkg/aws/sandbox_dynamo_network_test.go#TestNetworkPlacement_SurvivesRemarshal"
        status: pass
      - kind: unit
        ref: "pkg/aws/sandbox_dynamo_network_test.go#TestNetworkPlacement_ListAllSandboxMetadataDynamo_RoundTrip"
        status: pass
    human_judgment: false
  - id: D2
    description: "A pre-125 sandbox row with no placement attribute unmarshals to an empty string rather than an error, and is treated as public"
    requirement: "REQ-125-GUARD"
    verification:
      - kind: unit
        ref: "pkg/aws/sandbox_dynamo_network_test.go#TestNetworkPlacement_AbsentAttributeIsEmpty"
        status: pass
    human_judgment: false
  - id: D3
    description: "The attribute is written only when non-empty, so rows for sandboxes that never set it stay byte-identical"
    requirement: "REQ-125-GUARD"
    verification:
      - kind: unit
        ref: "pkg/aws/sandbox_dynamo_network_test.go#TestNetworkPlacement_OmittedWhenEmpty"
        status: pass
    human_judgment: false

duration: 25min
completed: 2026-08-19
status: complete
---

# Phase 125 Plan 04: SandboxMetadata.NetworkPlacement DynamoDB chokepoint Summary

**Adds `network_placement` as a schema-on-write DynamoDB attribute on the `{prefix}-sandboxes` row, wired through the shared marshal/unmarshal chokepoint at all three raw-item-to-SandboxMetadata entry points so it survives every pause/resume/extend/ttl-handler full-row `PutItem`.**

## Performance

- **Duration:** ~25 min
- **Tasks:** 2/2 completed
- **Files modified:** 3 (1 created, 2 modified)

## Accomplishments
- `SandboxMetadata.NetworkPlacement string` (`json:"network_placement,omitempty"`) added next to the other per-sandbox lifecycle attributes in `pkg/aws/metadata.go`, with a round-trip warning comment citing `project_sandboxmetadata_lossy_roundtrip` by name, matching the `SlackAllow`/`GithubInboundQueueURL` comment convention.
- `marshalSandboxItem` writes `network_placement` as an `AttributeValueMemberS`, guarded on `meta.NetworkPlacement != ""` — the same shape as the `slack_allow` guard.
- New `unmarshalNetworkFields(item, meta)` helper, next to `unmarshalSlackFields`, called from all three places in `pkg/aws/sandbox_dynamo.go` that turn a raw DynamoDB item into a `*SandboxMetadata`: `ReadSandboxMetadataDynamo`, `ListAllSandboxesByDynamo`, and `ListAllSandboxMetadataDynamo`. This is a deliberate extension beyond the plan's literal "call it from both toSandboxMetadata() and unmarshalSandboxItem" instruction — see Deviations below.
- No DynamoDB table migration (schema-on-write, like `slack_allow`/`action_limits`). `resume.go`, `extend.go`, and `cmd/ttl-handler/main.go` are unmodified — the single chokepoint is what makes them preserve the field automatically.
- Six round-trip tests in the new `pkg/aws/sandbox_dynamo_network_test.go`, covering both values, the write-omission guard, the pre-125 absent-attribute read, the remarshal-survival guard (asserted on the *second* marshal), and the third unmarshal entry point.

## Task Commits

1. **Task 1: Add SandboxMetadata.NetworkPlacement and wire it through marshal/unmarshal**
   - `0c17169b` feat(125-04): add SandboxMetadata.NetworkPlacement and wire DynamoDB chokepoint
2. **Task 2: Add the network_placement round-trip test file**
   - `07cc3e22` test(125-04): add network_placement round-trip test file

Both tasks are marked `tdd="true"` in the plan but the plan itself sequences implementation (Task 1) before the dedicated test file (Task 2), rather than a single RED-then-GREEN cycle within one task — followed as written.

## Files Created/Modified
- `pkg/aws/metadata.go` — `NetworkPlacement string` field on `SandboxMetadata`
- `pkg/aws/sandbox_dynamo.go` — write guard in `marshalSandboxItem`; new `unmarshalNetworkFields` helper; wired into `ReadSandboxMetadataDynamo`, `ListAllSandboxesByDynamo`, `ListAllSandboxMetadataDynamo`
- `pkg/aws/sandbox_dynamo_network_test.go` (new) — 6 tests: `TestNetworkPlacement_PrivateRoundTrip`, `_PublicRoundTrip`, `_OmittedWhenEmpty`, `_AbsentAttributeIsEmpty`, `_SurvivesRemarshal`, `_ListAllSandboxMetadataDynamo_RoundTrip`

## Decisions Made
- Wired `unmarshalNetworkFields` into all three real external unmarshal call sites rather than the two the plan named — see Deviations.
- Left `NetworkPlacement` off `SandboxRecord`/`metadataToRecord` (out of scope for this plan's `files_modified`; mirrors the existing Slack tri-state fields, which are also `SandboxMetadata`-only).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug in plan's own call-graph description] `unmarshalNetworkFields` wired into 3 call sites, not the 2 the plan's read_first section named**
- **Found during:** Task 1, while reading `pkg/aws/sandbox_dynamo.go` lines 79-100 (`toSandboxMetadata`) and 174-200 (`unmarshalSandboxItem`) as instructed.
- **Issue:** The plan's `<read_first>` and acceptance criteria describe `toSandboxMetadata()` and `unmarshalSandboxItem` as "the two entry points every unmarshal helper must be called from," and ask for `grep -c 'unmarshalNetworkFields'` to print exactly `3` (definition + 2 calls). In the actual codebase, `unmarshalSandboxItem` builds an intermediate `sandboxItemDynamo` struct from the raw item (no access to `meta`), and the method `(d *sandboxItemDynamo) toSandboxMetadata()` builds `*SandboxMetadata` from that struct (no access to the raw item map) — neither has the signature needed to call an `unmarshalXFields(item, meta)`-shaped helper directly. The actual, already-established convention this file uses for exactly this shape of field (`unmarshalSlackFields`, `unmarshalGitHubFields`, `unmarshalFailureFields`, `unmarshalFrozenFields`) is to call the helper from the **three** external functions that consume both the raw item and the built `meta`: `ReadSandboxMetadataDynamo`, `ListAllSandboxesByDynamo`, and `ListAllSandboxMetadataDynamo` (verified by grep count on the four existing helpers before making any change).
- **Fix:** Wired `unmarshalNetworkFields(item, meta)` into all three call sites, matching the existing convention exactly and satisfying the plan's own top-level success criterion ("provably survives a marshal -> unmarshal round trip through EVERY unmarshal entry point"). Had I followed the literal "2 call sites" instruction, `ListAllSandboxMetadataDynamo` — the function Plan 06/07's guards are expected to use, per that function's own doc comment about doctor-style checks — would have silently dropped `NetworkPlacement`, defeating the plan's stated purpose.
- **Files modified:** `pkg/aws/sandbox_dynamo.go`
- **Commit:** `0c17169b`
- **Consequence for the plan's literal acceptance criteria:** `grep -c 'network_placement' pkg/aws/sandbox_dynamo.go` prints `4` (not "at least 2", still satisfies "at least"). `grep -c 'unmarshalNetworkFields' pkg/aws/sandbox_dynamo.go` prints `6` (1 definition + 2 doc-comment mentions + 3 calls), not the plan's literal `3`. All other stated acceptance criteria (field count, `go test` green, forbidden-files untouched) hold exactly as specified.

## Issues Encountered
None beyond the plan/codebase mismatch documented above.

## User Setup Required
None. No deploy action needed for this plan alone — `network_placement` is not yet written by anything (that's a later plan, per `affects`); this plan only builds the durable chokepoint those plans will write and read through.

## Self-Check: PASSED
- `pkg/aws/metadata.go` — FOUND
- `pkg/aws/sandbox_dynamo.go` — FOUND
- `pkg/aws/sandbox_dynamo_network_test.go` — FOUND
- `0c17169b` — FOUND in git log
- `07cc3e22` — FOUND in git log
- `go test ./pkg/aws/... -count=1` — ok (own exit status, not piped)
- `git status --porcelain internal/app/cmd/resume.go internal/app/cmd/extend.go cmd/ttl-handler/main.go` — empty

## Next Phase Readiness
`SandboxMetadata.NetworkPlacement` is durable and round-trip-safe. Plan 05 (or whichever plan wires the create-time write) can set it once at `km create`; Plan 06's `km init` refuse-to-disable-NAT guard and Plan 07's `km doctor` checks can read it via `ListAllSandboxMetadataDynamo` with the guarantee that pause/resume/extend/ttl-handler will never silently strip it. No blockers.

---
*Phase: 125-per-profile-private-subnet-sandboxes-with-per-az-nat-gateway*
*Completed: 2026-08-19*
