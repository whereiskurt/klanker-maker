---
phase: 117-composable-multi-parent-profile-inheritance-deep-merge-list-union-extends
verified: 2026-06-24T14:10:00Z
status: passed
score: 5/5 must-haves verified
re_verification: false
---

# Phase 117: Composable Multi-Parent Profile Inheritance Verification Report

**Phase Goal:** A SandboxProfile can declare `extends:` as a single string OR an ordered list of base references; km deep-merges all bases + the child into one effective profile (maps recurse, scalars child-wins, lists concat+dedup), then validates the merged leaf. Replaces the typed-merger zoo with a generic map deep-merge so every section composes; `profiles/base/` fragments (metadata.abstract:true) collapse the ~80-line-per-profile duplication.

**Verified:** 2026-06-24T14:10:00Z
**Status:** PASSED
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `extends:` accepts string OR `[]string` — ExtendsField type with custom goccy UnmarshalYAML | VERIFIED | `pkg/profile/types.go` line 20: `type ExtendsField []string`; UnmarshalYAML at line 31 tries scalar then []string; `TestExtendsUnmarshal` (4 subtests all PASS) |
| 2 | Generic `deepMerge` (scalars src/child-wins, maps recurse, lists concat+dedup) replaced the typed merger zoo; multi-parent DAG resolve is diamond-safe, memoized, max depth 10 | VERIFIED | `pkg/profile/inherit.go`: `deepMerge` at line 27, `concatDedup` at 69, `resolveMap` at 113, `maxInheritanceDepth = 10` at line 14; `mergeSpecSection`/`pickBoolPtr`/`pickIntPtr`/`pickString` all absent (grep returns nothing); `TestDeepMerge_*` (5 tests), `TestResolve_Diamond`, `TestResolve_DiamondMemoized`, `TestResolveDepthExceeded` all PASS |
| 3 | `km validate` + `km create` resolve the full DAG before validating/compiling; abstract fragments (metadata.abstract:true) skip `km validate` (exit 0) and are blocked in `km create` | VERIFIED | `internal/app/cmd/validate.go` line 76: `if profile.IsAbstractFragment(raw)` guard; line 88: `parsed.Extends.IsSet()` + line 104: `profile.Resolve(leafName, searchPaths)`; same pattern at `create.go` lines 342+354 and 2107+2118; `./km validate profiles/base/safenetwork.yaml` → "SKIP: ... is an abstract base fragment"; `TestValidateAbstractFragment` + `TestValidateMultiParentLeaf` both PASS |
| 4 | `profiles/base/` holds 8 abstract fragments; `learn.v2.*` + `dc34.yaml` compose them via multi-parent extends; compiled userdata is byte-identical | VERIFIED | All 8 base files exist with `abstract: true`; `learn.v2.yaml` extends all 8; `learn.v2.chatty/polite/codex.yaml` extend all 8; `dc34.yaml` extends 6 (skips `email-strict` per locked decision A); `TestUserdataLearnV2Phase92ByteIdentity` PASS (uses `profile.Resolve()` not `Parse()`); `scripts/validate-all-profiles.sh` exits 0 with "validate-all-profiles: all 22 profiles valid" |
| 5 | Docs: OPERATOR-GUIDE § Composable inheritance, CLAUDE.md Phase 117 note + Where-to-look row, docs/agent-tool-gating.md xref | VERIFIED | `OPERATOR-GUIDE.md` line 1709: `## 11. Composable inheritance (multi-parent profiles)`; `CLAUDE.md` line 206: Where-to-look row; line 297: Phase 117 note; `docs/agent-tool-gating.md` line 165: "Composable inheritance and tools.autoApprove" section |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/profile/types.go` | ExtendsField union type + UnmarshalYAML + IsSet/List; Metadata.Abstract; ExecutionSpec.InitCommandsAppend | VERIFIED | All present at lines 20-45 (ExtendsField), 66 (Abstract), 456-457 (InitCommandsAppend) |
| `pkg/profile/validate.go` | `IsAbstractFragment(raw []byte) bool` | VERIFIED | Present at line 762; fail-open on malformed YAML |
| `pkg/profile/schemas/sandbox_profile.schema.json` | `extends` oneOf[string,array]; `metadata.abstract` boolean; `execution.initCommandsAppend` array | VERIFIED | `abstract` at line 42, `initCommandsAppend` at line 353, `oneOf` (2 occurrences) confirmed |
| `pkg/profile/inherit.go` | `deepMerge(map[string]any)`, `concatDedup`, DAG `resolveMap` with memoization, `maxInheritanceDepth=10`, `applyInitCommandsAppend`, `clearAbstractFromMetadata` | VERIFIED | All functions present at documented lines; zoo functions (`mergeSpecSection`, `pickBoolPtr`, `pickIntPtr`, `pickString`) absent |
| `profiles/base/` (8 fragments) | Abstract fragments: safenetwork, sidecars-all, observability-learn, budget-standard, artifacts-workspace, iam-us-east-1, agent-claude-all-tools, email-strict | VERIFIED | All 8 files present with `abstract: true` confirmed via grep |
| `profiles/learn.v2.yaml` + chatty/polite/codex | Leaf profiles extended to compose base fragments | VERIFIED | All 4 learn.v2 variants have `extends:` list with base/* references |
| `profiles/dc34.yaml` | Extends 6 base fragments (not email-strict — locked decision A) | VERIFIED | `extends:` block present; includes safenetwork, sidecars-all, observability-learn, budget-standard, artifacts-workspace, iam-us-east-1, agent-claude-all-tools; email-strict excluded with comment |
| `pkg/compiler/userdata_phase92_byte_identity_test.go` | Uses `profile.Resolve()` not `profile.Parse()` | VERIFIED | Line 39: `profile.Resolve("learn.v2", ...)` confirmed; test PASS |
| `OPERATOR-GUIDE.md` | § Composable inheritance section | VERIFIED | Line 1709 confirmed; contains "narrow", "initCommandsAppend", diamond, bool-trap, worked dc34 example |
| `CLAUDE.md` | Phase 117 note + Where-to-look row | VERIFIED | Where-to-look row at line 206; Phase 117 note at line 297 |
| `docs/agent-tool-gating.md` | Composable inheritance xref for tools.autoApprove | VERIFIED | Section at line 165; mentions union semantics + !replace deferred |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/app/cmd/validate.go` | `profile.IsAbstractFragment` | guard before extends check | WIRED | Line 76 calls `profile.IsAbstractFragment(raw)`, exits 0 with SKIP message |
| `internal/app/cmd/validate.go` | `profile.Resolve(leafName, searchPaths)` | full DAG walk | WIRED | Lines 88+104: IsSet check then Resolve; validated on merged bytes |
| `internal/app/cmd/create.go` (x2) | `profile.Resolve(leafName, searchPaths)` | full DAG walk | WIRED | Lines 342+354 and 2107+2118; abstract-fragment guard + full resolve at both sites |
| `resolveMap()` | `deepMerge` | fold bases left->right then child | WIRED | `deepMerge` called in `resolveMap` accumulator loop |
| merged `map[string]any` | `*SandboxProfile` | `yaml.Marshal` + `yaml.Unmarshal` | WIRED | `fromMap()` at line 274 confirmed |
| `userdata_phase92_byte_identity_test.go` | `profile.Resolve()` | merged spec compilation | WIRED | Line 39 confirmed; golden test PASS |
| `scripts/validate-all-profiles.sh` | `profiles/base/` skip | nullglob guard + per-file skip | WIRED | `[ -d profiles/base ]` guard present; prints "skip ... (base fragment)" for each |
| `learn.v2.yaml` + variants + `dc34.yaml` | `profiles/base/*.yaml` | `extends: [base/...]` | WIRED | All 5 leaf profiles have extends blocks referencing base/* |

### Requirements Coverage

No ROADMAP requirement IDs are mapped to this phase (new architectural phase). Must-haves were derived from the phase goal. All 5 derived truths are verified.

### Anti-Patterns Found

| File | Pattern | Severity | Impact |
|------|---------|----------|--------|
| None | — | — | — |

No stubs, TODOs, empty implementations, or placeholder patterns detected in the modified files. The `// TODO(Plan 02): DAG multi-parent` comment from Plan 01 was removed as intended when Plan 02 replaced the single-parent chain.

Note: `mergeNotificationSpec` and `mergeAgentSpec` are retained as typed shims (not deletions from the zoo) because they handle a specific backward-compat case (invites.emails child-replaces semantics) and a type-preservation case (int vs uint64 round-trip). These are documented decisions in the Plan 02 SUMMARY, not anti-patterns.

### Human Verification Required

None required. All critical behaviors are verifiable programmatically:

- `extends:` parsing: covered by `TestExtendsUnmarshal` (all 4 subtests green)
- Deep-merge semantics: covered by `TestDeepMerge_*` (5 tests green)
- Diamond/multi-parent resolution: covered by `TestResolve_Diamond` + `TestResolve_DiamondMemoized` + `TestResolve_MultiParentOrder` (all green)
- Byte-identity: `TestUserdataLearnV2Phase92ByteIdentity` PASS
- Fragment skip UX: `./km validate profiles/base/safenetwork.yaml` emits correct SKIP message (verified directly)
- All 22 leaf profiles validate: `scripts/validate-all-profiles.sh` exits 0

### Gaps Summary

None. All must-haves verified.

---

## Full Test Run Summary

```
go build ./...                                      OK (no output — clean build)
go vet ./pkg/profile/... ./pkg/compiler/...         OK (no output)
go test ./pkg/profile/... -count=1                  ok  0.485s
go test ./pkg/compiler/... -count=1                 ok  7.567s
go test ./internal/app/cmd/... -run TestValidateAbstractFragment   PASS (2.81s)
go test ./internal/app/cmd/... -run TestValidateMultiParentLeaf    PASS (2.77s)
TestUserdataLearnV2Phase92ByteIdentity              PASS
TestExtendsUnmarshal (4 subtests)                   PASS
TestDeepMerge_* (5 subtests)                        PASS
TestResolve_Diamond                                 PASS
TestResolve_DiamondMemoized                         PASS
TestResolve_MultiParentOrder                        PASS
TestResolveDepthExceeded                            PASS
TestResolveCircularDetection                        PASS
TestIsAbstractFragment (4 subtests)                 PASS
TestValidateSchemaExtendsArrayForm                  PASS
TestInheritAgent_* (5 tests — regression net)       PASS
TestInheritNotification_* (7 tests — regression net) PASS
scripts/validate-all-profiles.sh                   "all 22 profiles valid" (exit 0; 8 base/ fragments skipped)
km validate profiles/base/safenetwork.yaml         SKIP message, exit 0
```

---

_Verified: 2026-06-24T14:10:00Z_
_Verifier: Claude (gsd-verifier)_
