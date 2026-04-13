---
phase: 52-clone-sandbox
verified: 2026-04-13T17:32:07Z
status: passed
score: 10/10 must-haves verified
re_verification: false
---

# Phase 52: km clone — Sandbox Duplication Verification Report

**Phase Goal:** Add `km clone <source> [new-alias]` command that creates a new sandbox from an existing one's profile, copies /workspace and rsyncFileList paths via SSM+rsync through S3 staging, and provisions a fully independent identity (new sandbox ID, email, keys, budget, TTL). Supports `--no-copy` for fresh-from-same-profile, `--count N` for multi-clone with auto-suffixed aliases, and `--alias` flag as alternative to positional alias. Source must be running; live copy (no freeze). Clone metadata includes `cloned_from` field for lineage tracking.
**Verified:** 2026-04-13T17:32:07Z
**Status:** PASSED
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | ClonedFrom field round-trips through DynamoDB marshal/unmarshal | VERIFIED | 6 TestClonedFrom_* tests pass; marshalSandboxItem and unmarshalSandboxItem both handle cloned_from |
| 2 | km list --wide shows CLONED FROM column with source sandbox ID | VERIFIED | list.go:196 header includes "CLONED FROM"; list.go:237-245 renders `truncCol(clonedFrom, 14)` per row |
| 3 | Empty ClonedFrom is omitted from DynamoDB (no empty-string GSI pollution) | VERIFIED | marshalSandboxItem:237-239 conditionally sets cloned_from; TestClonedFrom_MarshalOmitsWhenEmpty passes |
| 4 | km clone provisions a new independent sandbox from source's stored profile | VERIFIED | clone.go:runClone fetches profile via fetchStoredProfile, writes to temp file, calls runCreate |
| 5 | Workspace and rsyncFileList paths are staged through S3 and downloaded at boot | VERIFIED | BuildWorkspaceStagingCmd constructs tar+S3 cp command; downloadWorkspaceToClone sends post-provision SSM download |
| 6 | --no-copy flag skips workspace staging and creates a fresh sandbox from same profile | VERIFIED | clone.go:136 `if !noCopy` guard; --no-copy flag registered on cmd line:81 |
| 7 | --count N creates N clones with auto-suffixed aliases (e.g., wrkr-1, wrkr-2) | VERIFIED | GenerateCloneAliases exported; TestClone_AliasGeneration_Multi passes; count=1 returns alias as-is |
| 8 | Source sandbox must be running; non-running source returns error with km resume suggestion | VERIFIED | clone.go:118-121 checks status != "running"; TestClone_SourceNotRunning_ReturnsError passes (error contains "not running" + "km resume") |
| 9 | Clone metadata includes cloned_from pointing to source sandbox ID | VERIFIED | updateClonedFrom in clone.go:387-419 reads and rewrites DynamoDB metadata with ClonedFrom=sourceID |
| 10 | ECS substrate clones return a clear error for workspace copy (SSM not available) | VERIFIED | clone.go:124-126 checks substrate == "ecs"; TestClone_ECSWithWorkspaceCopy_ReturnsError passes |

**Score:** 10/10 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/aws/metadata.go` | ClonedFrom field on SandboxMetadata | VERIFIED | Line 23: `ClonedFrom string \`json:"cloned_from,omitempty"\`` |
| `pkg/aws/sandbox.go` | ClonedFrom field on SandboxRecord | VERIFIED | Line 32: `ClonedFrom string \`json:"cloned_from,omitempty"\`` |
| `pkg/aws/sandbox_dynamo.go` | cloned_from in struct + all 4 marshal/unmarshal/convert locations | VERIFIED | sandboxItemDynamo:56, unmarshalSandboxItem:180-184, marshalSandboxItem:237-239, toSandboxMetadata:83, metadataToRecord:120 |
| `internal/app/cmd/list.go` | CLONED FROM column in --wide output | VERIFIED | Header line:196 and row render:237-245; shows "-" when empty |
| `pkg/aws/sandbox_dynamo_test.go` | 6 TestClonedFrom_* tests | VERIFIED | All 6 pass: Marshal/Unmarshal/ToSandboxMetadata/MetadataToRecord variants |
| `internal/app/cmd/clone.go` | NewCloneCmd, runClone, BuildWorkspaceStagingCmd, GenerateCloneAliases | VERIFIED | All 4 present and substantive (495 lines); exported functions for testability |
| `internal/app/cmd/clone_test.go` | 9 TestClone_* tests | VERIFIED | All 9 pass covering flags, error paths, alias generation, staging cmd structure |
| `internal/app/cmd/root.go` | NewCloneCmd registered | VERIFIED | Line 79: `root.AddCommand(NewCloneCmd(cfg))` |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| sandbox_dynamo.go:marshalSandboxItem | metadata.go:SandboxMetadata.ClonedFrom | manual AttributeValueMemberS construction | WIRED | Lines 237-239: `if meta.ClonedFrom != "" { item["cloned_from"] = ... }` |
| sandbox_dynamo.go:unmarshalSandboxItem | sandboxItemDynamo.ClonedFrom | manual field extraction | WIRED | Lines 180-184: `if v, ok := item["cloned_from"]; ok { ... d.ClonedFrom = sv.Value }` |
| list.go:printSandboxTable | sandbox.go:SandboxRecord.ClonedFrom | wide format row rendering | WIRED | Lines 237-245: `clonedFrom := r.ClonedFrom` rendered with truncCol |
| clone.go:runClone | sandbox_dynamo.go:ReadSandboxMetadataDynamo | source status validation via fetcher.FetchSandbox | WIRED | Lines 99-115: fetcher.FetchSandbox calls ReadSandboxMetadataDynamo |
| clone.go:runClone | create.go:runCreate | provisioning pipeline reuse | WIRED | Line 192: `runCreate(cfg, tmpFile.Name(), false, false, "", verbose, "", cloneAlias, "", "", "")` |
| clone.go:buildWorkspaceStagingCmd | S3 staging key | SSM SendCommand tar + S3 cp | WIRED | BuildWorkspaceStagingCmd builds `cd / && tar czf ... && aws s3 cp ... s3://{bucket}/{stagingKey}` |
| clone.go | metadata.go:SandboxMetadata.ClonedFrom | DynamoDB metadata write after provision | WIRED | Lines 387-419: updateClonedFrom reads meta, sets ClonedFrom=sourceID, writes back |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| CLONE-01 | 52-02 | `km clone <source> [new-alias]` CLI command — resolves source, provisions clone sandbox | SATISFIED | NewCloneCmd/runClone in clone.go; ResolveSandboxID called; runCreate invoked for provisioning |
| CLONE-02 | 52-02 | Workspace copy via SSM tar → S3 staging → clone boot download | SATISFIED | BuildWorkspaceStagingCmd + sendSSMCommand + downloadWorkspaceToClone; post-provision SSM download pattern |
| CLONE-03 | 52-02 | --no-copy, --count N, --alias flags with multi-clone alias auto-suffixing | SATISFIED | All 4 flags registered; GenerateCloneAliases handles single/multi cases; --no-copy guard in runClone |
| CLONE-04 | 52-01 | cloned_from field added to DynamoDB metadata; visible in km list --wide | SATISFIED | Field in all 3 structs, all 4 DynamoDB locations; CLONED FROM column in list --wide |
| CLONE-05 | 52-02 | Source sandbox state validation (must be running; clear error if paused/stopped) | SATISFIED | clone.go:118-121 status check; error includes "km resume" suggestion |

Note: CLONE-01 through CLONE-05 are defined in 52-RESEARCH.md and referenced in ROADMAP.md Phase 52 entry, but are NOT present as formally-tracked entries in REQUIREMENTS.md. They are phase-local requirement IDs scoped to this phase. No orphaned requirements detected — all 5 IDs are claimed by the two plans (52-01 claims CLONE-04; 52-02 claims CLONE-01, CLONE-02, CLONE-03, CLONE-05).

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| clone.go | 387-391 | `if ssmClient != nil { return nil }` test-mode skip in updateClonedFrom | Info | updateClonedFrom is no-op in test mode; production path is exercised manually or E2E; unit tests can't cover DynamoDB lineage write path |
| clone.go | 374-383 | `if ssmClient != nil { return "sb-testclone", nil }` test-mode stub in resolveCloneID | Info | resolveCloneID returns hardcoded ID in test mode; acceptable for unit isolation |

No blocker or warning-level anti-patterns. The test-mode shortcuts are intentional dependency injection patterns (established pattern in agent.go) and do not affect production paths.

### Human Verification Required

The following behaviors require a live environment to fully validate:

#### 1. End-to-end workspace copy

**Test:** `km clone <running-ec2-sandbox> --alias test-clone` against a real EC2 sandbox with content in /workspace
**Expected:** Clone provisions successfully; /workspace on clone contains files from source; `km list --wide` shows source sandbox ID in CLONED FROM column for new clone
**Why human:** Requires running EC2 instance with SSM agent, real S3 bucket, real DynamoDB table, and real AWS credentials

#### 2. --count multi-clone

**Test:** `km clone <running-sandbox> --alias wrkr --count 3` 
**Expected:** Three sandboxes created with aliases wrkr-1, wrkr-2, wrkr-3; each has cloned_from set; each has independent identity (sandbox ID, email, keys)
**Why human:** Requires real provisioning pipeline; cannot be unit tested end-to-end

#### 3. --no-copy with ECS substrate

**Test:** `km clone <running-ecs-sandbox> --no-copy --alias ecs-clone`
**Expected:** Clone provisions from same profile without any SSM/workspace staging; no error
**Why human:** No unit test covers the --no-copy + ECS success path (only the --no-copy + EC2 path succeeds in unit tests via the noCopy guard)

#### 4. km list --wide CLONED FROM column display

**Test:** Run `km list --wide` after creating a clone
**Expected:** Clone row shows source sandbox ID in CLONED FROM column; non-clone rows show "-"
**Why human:** Visual table alignment and truncation behavior under real terminal widths

### Gaps Summary

No gaps. All automated checks pass. All 10 truths verified, all 8 artifacts substantive and wired, all 5 key links confirmed, all 5 CLONE-0x requirements satisfied.

Build: `go build ./cmd/km/` — PASS
Tests: `go test ./pkg/aws/ -run TestClonedFrom` — 6/6 PASS
Tests: `go test ./internal/app/cmd/ -run TestClone` — 9/9 PASS
Vet: `go vet ./internal/app/cmd/ ./pkg/aws/` — clean

---

_Verified: 2026-04-13T17:32:07Z_
_Verifier: Claude (gsd-verifier)_
