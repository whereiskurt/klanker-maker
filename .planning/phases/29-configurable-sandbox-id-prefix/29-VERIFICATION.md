---
phase: 29-configurable-sandbox-id-prefix
verified: 2026-03-29T03:20:38Z
status: gaps_found
score: 12/13 must-haves verified
gaps:
  - truth: "km create --remote --alias orc stores alias in metadata"
    status: partial
    reason: "runCreateRemote does not pass aliasOverride to SandboxMetadata; --alias flag is silently dropped when combined with --remote"
    artifacts:
      - path: "internal/app/cmd/create.go"
        issue: "runCreateRemote (line ~994) builds SandboxMetadata without Alias field; aliasOverride is not propagated to runCreateRemote signature"
    missing:
      - "Pass aliasOverride through runCreateRemote's function signature (currently ignores it)"
      - "Auto-generate alias from resolvedProfile.Metadata.Alias in runCreateRemote the same way runCreate does"
      - "Set meta.Alias = sandboxAlias in the SandboxMetadata struct at line ~1004"
human_verification:
  - test: "km create profiles/claude-dev.yaml --alias orc"
    expected: "Sandbox created with ID claude-XXXXXXXX and alias 'orc' stored in S3 metadata; output includes '(alias: orc)'"
    why_human: "Requires live AWS credentials and S3 bucket to confirm metadata write"
  - test: "km status orc (after creating sandbox with alias orc)"
    expected: "Alias 'orc' resolves to the sandbox ID and status is shown"
    why_human: "Requires live S3 scan to confirm ResolveSandboxAlias returns correct ID"
  - test: "km create profiles/claude-dev.yaml (no --alias, profile has metadata.alias: claude)"
    expected: "Sandbox created with auto-generated alias 'claude-1'; second create produces 'claude-2'"
    why_human: "Auto-increment logic requires live S3 scan of existing sandboxes"
  - test: "km destroy claude-XXXXXXXX (after creating with alias)"
    expected: "Output includes 'Alias freed: claude-1' before teardown"
    why_human: "Requires live sandbox to confirm alias-freed log message"
---

# Phase 29: Configurable Sandbox ID Prefix Verification Report

**Phase Goal:** Sandbox ID prefix is configurable per profile via `metadata.prefix` and sandboxes can be addressed by human-friendly aliases. Operators define meaningful prefixes (e.g. `claude`, `build`, `research`) that replace hardcoded `sb-` prefix, and can assign aliases (e.g. `orc`, `wrkr`) via `--alias` flag or profile-level `metadata.alias` template with auto-incrementing suffix. Profiles without `metadata.prefix` default to `sb` for backwards compatibility.
**Verified:** 2026-03-29T03:20:38Z
**Status:** gaps_found (1 gap — remote create path does not propagate alias)
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Profile with `metadata.prefix: claude` passes km validate | VERIFIED | `TestValidateSchema_MetadataPrefix/valid_claude` passes; schema pattern `^[a-z][a-z0-9]{0,11}$` |
| 2 | Profile with invalid prefix fails km validate with clear error | VERIFIED | `TestValidateSchema_MetadataPrefix/invalid_*` cases all pass |
| 3 | Profile without `metadata.prefix` passes km validate unchanged | VERIFIED | `TestValidateSchema_MetadataPrefix/valid_no_prefix_omitted` passes |
| 4 | `GenerateSandboxID("claude")` returns `claude-{8hex}` | VERIFIED | `sandbox_id.go` line 17-26 confirmed; compiler tests pass |
| 5 | `GenerateSandboxID("")` returns `sb-{8hex}` for backwards compat | VERIFIED | Default branch `prefix = "sb"` at line 18-20 |
| 6 | `km create` with `prefix: claude` generates a `claude-*` sandbox ID | VERIFIED | `create.go` line 170: `compiler.GenerateSandboxID(resolvedProfile.Metadata.Prefix)` in both `runCreate` and `runCreateRemote` (line 891) |
| 7 | `km destroy claude-abc12345` is accepted as a valid sandbox ID | VERIFIED | `destroy.go` line 35: `^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$`; `TestDestroyCmd_GeneralizedPatternAcceptsCustomPrefix` passes |
| 8 | `ResolveSandboxID` recognizes `claude-abc12345` as an ID | VERIFIED | `sandbox_ref.go` uses `sandboxIDLike` regex; `TestResolveSandboxID_CustomPrefix/claude-abc12345` passes |
| 9 | Email handler `extractSandboxID("status claude-abc12345")` returns `"claude-abc12345"` (full ID) | VERIFIED | `main.go` line 112: `(?i)\b([a-z][a-z0-9]{0,11}-[0-9a-f]{8})\b`; `TestExtractSandboxID_NoPrefixRepair` passes |
| 10 | Email handler does not prepend `sb-` to IDs with custom prefixes | VERIFIED | `sb-` prefix repair block removed; confirmed in `extractSandboxID` function |
| 11 | `claude-dev.yaml` built-in profile has `metadata.prefix: claude` and `metadata.alias: claude` | VERIFIED | `profiles/claude-dev.yaml` lines 9-10 confirmed |
| 12 | `km create --alias orc` stores alias in S3 metadata (local create path) | VERIFIED | `create.go` lines 437-465; `SandboxMetadata.Alias` populated; `TestListCmd_AliasColumn` passes |
| 13 | `km create --remote --alias orc` stores alias in S3 metadata | FAILED | `runCreateRemote` (line 994) builds `SandboxMetadata` without `Alias` field; `--alias` flag is accepted but silently ignored when `--remote` is set |

**Score:** 12/13 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `pkg/profile/types.go` | `Metadata.Prefix` and `Metadata.Alias` fields | VERIFIED | Lines 24-25: `Prefix string yaml:"prefix,omitempty"` and `Alias string yaml:"alias,omitempty"` |
| `pkg/profile/schemas/sandbox_profile.schema.json` | `prefix` and `alias` properties in metadata schema | VERIFIED | Lines 37-46: both properties with pattern validation |
| `pkg/compiler/sandbox_id.go` | `GenerateSandboxID(prefix string)` and `IsValidSandboxID` | VERIFIED | Full implementations at lines 17-37 |
| `internal/app/cmd/destroy.go` | Generalized pattern `[a-z][a-z0-9]{0,11}-[a-f0-9]{8}` | VERIFIED | Line 35: exact pattern confirmed |
| `internal/app/cmd/sandbox_ref.go` | Generalized ID detection + alias resolution path | VERIFIED | Lines 17 and 36: `sandboxIDLike` regex and `ResolveSandboxAlias` call |
| `cmd/email-create-handler/main.go` | Full ID extraction without `sb-` repair | VERIFIED | Line 112: captures full `{prefix}-{8hex}` in group 1; repair block absent |
| `profiles/claude-dev.yaml` | `prefix: claude` and `alias: claude` in metadata | VERIFIED | Lines 9-10 confirmed |
| `pkg/aws/metadata.go` | `Alias` field in `SandboxMetadata` | VERIFIED | Line 22: `Alias string json:"alias,omitempty"` |
| `pkg/aws/sandbox.go` | `ResolveSandboxAlias` and `NextAliasFromTemplate` functions | VERIFIED | Lines 268-292 and 297-320; all tests pass |
| `internal/app/cmd/list.go` | Alias column in `km list` output | VERIFIED | Lines 134 and 140-148: `ALIAS` header and `r.Alias` populated |
| `internal/app/cmd/create.go` | `--alias` flag, profile alias template, `SandboxMetadata.Alias` set | PARTIAL | Local `runCreate` path fully wired; `runCreateRemote` does not set `Alias` |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/app/cmd/create.go` | `pkg/compiler/sandbox_id.go` | `compiler.GenerateSandboxID(resolvedProfile.Metadata.Prefix)` | WIRED | Lines 170 and 891; both local and remote paths |
| `pkg/profile/types.go` | `sandbox_profile.schema.json` | `Metadata.Prefix` field matches `prefix` schema property | WIRED | Both use pattern `^[a-z][a-z0-9]{0,11}$` |
| `internal/app/cmd/destroy.go` | pattern matches `pkg/compiler/sandbox_id.go` | Same generalized regex | WIRED | Both use `^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$` |
| `internal/app/cmd/sandbox_ref.go` | `pkg/aws/sandbox.go` | `ResolveSandboxAlias(ctx, client, bucket, alias)` | WIRED | Line 36 calls `kmaws.ResolveSandboxAlias` |
| `internal/app/cmd/create.go` (local) | `pkg/aws/metadata.go` | `SandboxMetadata.Alias` field populated from `--alias` flag or profile template | WIRED | Lines 437-465; `meta.Alias = sandboxAlias` |
| `internal/app/cmd/create.go` (remote) | `pkg/aws/metadata.go` | `SandboxMetadata.Alias` field populated | NOT WIRED | `runCreateRemote` SandboxMetadata at line 994 omits `Alias` field entirely |
| `cmd/email-create-handler/main.go` | `extractSandboxID` function | Regex captures full prefix-hex ID without repair | WIRED | Group 1 capture; `sb-` repair block removed |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| PREFIX-01 | 29-01 | Profile schema supports optional `metadata.prefix` with validation | SATISFIED | JSON Schema `^[a-z][a-z0-9]{0,11}$`; 9 table-driven tests pass |
| PREFIX-02 | 29-01 | `GenerateSandboxID()` accepts prefix parameter, generates `{prefix}-{8hex}` | SATISFIED | `sandbox_id.go` lines 17-26; `GenerateSandboxID("claude")` → `claude-XXXXXXXX` |
| PREFIX-03 | 29-02 | All sandbox ID validation/matching patterns accept any valid prefix | SATISFIED | `destroy.go`, `sandbox_ref.go`, email handler all use generalized regex |
| PREFIX-04 | 29-02 | No component hardcodes `sb-` prefix | SATISFIED | `sb-` repair block removed; all patterns use `[a-z][a-z0-9]{0,11}` |
| PREFIX-05 | 29-01, 29-02 | Profiles without `metadata.prefix` default to `sb` | SATISFIED | `sandbox_id.go` line 18-20; empty prefix defaults to `"sb"` |
| ALIAS-01 | 29-03 | `km create --alias <name>` stores alias in S3 metadata.json; all commands resolve alias | PARTIAL | Local create path stores alias; `--remote` path silently drops `--alias` flag |
| ALIAS-02 | 29-03 | Profile-level `metadata.alias` template auto-generates `{alias}-1`, `{alias}-2` etc. | SATISFIED | `NextAliasFromTemplate` in `pkg/aws/sandbox.go`; create.go lines 438-450 |
| ALIAS-03 | 29-03 | `--alias` flag overrides profile-level template; alias freed on destroy | SATISFIED | `create.go` line 437: `sandboxAlias := aliasOverride` (takes priority); `destroy.go` lines 377-381: "Alias freed" log |
| ALIAS-04 | 29-03 | `km list` displays alias column; `ResolveSandboxRef` resolves aliases | SATISFIED | `list.go` lines 134/140-148 ALIAS column; `sandbox_ref.go` line 36 alias resolution fallback |

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `pkg/aws/sandbox.go` | 270 | `TODO: For O(1) lookup, add alias to DynamoDB km-identities table with a GSI.` | Info | Intentional; documented in plan as accepted tech debt |

No blocker or warning anti-patterns found. All critical functions are fully implemented. The one TODO is a known future optimization, not a stub.

### Pre-existing Test Failures Resolved

The `deferred-items.md` documented pre-existing failures in `TestBudgetAdd_*`, `TestShellCmd_*`, and `TestStatus_*` due to short sandbox ID fixtures. All 17 packages now pass `go test ./...` cleanly — these were resolved before phase completion.

### Human Verification Required

**1. km create local path with --alias flag**

**Test:** `km create profiles/claude-dev.yaml --alias orc`
**Expected:** Sandbox provisioned with ID `claude-XXXXXXXX`; S3 metadata.json contains `"alias": "orc"`; output line shows `(alias: orc)`
**Why human:** Requires live AWS credentials and S3 bucket

**2. Alias resolution via km status**

**Test:** After creating sandbox with `--alias orc`, run `km status orc`
**Expected:** Resolves alias to sandbox ID via S3 scan; status shown for correct sandbox
**Why human:** Requires live S3 metadata to confirm `ResolveSandboxAlias` path

**3. Profile alias template auto-increment**

**Test:** `km create profiles/claude-dev.yaml` twice (profile has `metadata.alias: claude`)
**Expected:** First create auto-generates alias `claude-1`, second generates `claude-2`
**Why human:** Requires live S3 listing of existing sandboxes for `NextAliasFromTemplate`

**4. Alias freed on destroy**

**Test:** `km destroy claude-XXXXXXXX` after creating with alias `claude-1`
**Expected:** Output line `Alias freed: claude-1` printed before teardown completes
**Why human:** Requires live sandbox with alias in S3 metadata

### Gaps Summary

One gap found: the `--remote` create path (`runCreateRemote`) does not propagate the `--alias` flag or the profile-level `metadata.alias` template to `SandboxMetadata`. The plan task (29-03 Task 2 action item) explicitly required "Apply the same logic in `runCreateRemote`" but this was not implemented.

**Impact:** `km create --remote --alias orc` silently ignores the alias. The core `km create --alias orc` (without `--remote`) works correctly. ALIAS-01 is partially satisfied — local create works, remote create does not.

**Fix:** Pass `aliasOverride` through `runCreateRemote`'s function signature, add alias determination logic mirroring `runCreate` lines 436-450, and set `meta.Alias = sandboxAlias` in the `SandboxMetadata` struct at line ~1004.

---

_Verified: 2026-03-29T03:20:38Z_
_Verifier: Claude (gsd-verifier)_
