---
phase: 68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload
plan: 02
subsystem: slack/payload
tags: [slack, envelope, canonical-json, ed25519, abi-extension, tdd]
dependency-graph:
  requires:
    - "Phase 63 SlackEnvelope struct + CanonicalJSON + Sign/Verify (pkg/slack/payload.go)"
    - "Plan 68-00 Wave 0 stub seeding (payload_transcript_test.go skeleton)"
  provides:
    - "ActionUpload constant for Plan 04 UploadFile + Plan 05 km-slack upload + Plan 08 bridge handler"
    - "BuildEnvelopeUpload constructor with named errors (ErrUploadFilenameInvalid / ErrUploadS3KeyEmpty / ErrUploadSizeInvalid / ErrUploadChannelEmpty)"
    - "Extended SlackEnvelope shape (4 additive fields: ContentType, Filename, S3Key, SizeBytes) usable by every Phase 68 component"
  affects:
    - "pkg/slack/bridge (test fixtures may want to exercise upload-action paths in Plan 08)"
    - "cmd/km-slack (Plan 05 will import BuildEnvelopeUpload directly)"
tech-stack:
  added: []
  patterns:
    - "Additive ABI extension preserving EnvelopeVersion=1 (zero-valued new fields on legacy actions)"
    - "Named sentinel errors for cross-package matching (errors.Is)"
    - "Canonical JSON via alphabetical struct-tag ordering (no sort-on-marshal cost)"
key-files:
  created: []
  modified:
    - "pkg/slack/payload.go"
    - "pkg/slack/payload_test.go"
    - "pkg/slack/payload_transcript_test.go"
    - ".planning/phases/68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload/deferred-items.md"
decisions:
  - "Reuse EnvelopeVersion=1 instead of bumping to 2: new fields are additive at canonical-tail-friendly alphabetical positions and serialize as deterministic zero values for legacy post/archive/test actions, so a v1 verifier built before Phase 68 still validates extended-shape envelopes byte-identically once the struct is updated everywhere"
  - "Body field stays empty for ActionUpload: uploads carry payload via S3, MaxBodyBytes (40KB) cap is bypassed, and bridge logic ignores body for action=upload"
  - "Filename validation enforced client-side (≤255 bytes, no '/', no NUL); content-type allow-list deferred to bridge to keep the Go API permissive and the trust boundary at the network edge"
  - "Named errors (ErrUpload*) instead of generic errors.New so Plan 05/08 callers can errors.Is-match precise failure modes and surface friendly CLI messages"
metrics:
  duration: "4min"
  completed: "2026-05-03"
---

# Phase 68 Plan 02: Slack envelope ABI extension for ActionUpload Summary

Extended `SlackEnvelope` with four additive fields (`ContentType`, `Filename`, `S3Key`, `SizeBytes`) plus an `ActionUpload` constant and a `BuildEnvelopeUpload` constructor — landing the bridge ABI surface every other Phase 68 component depends on without bumping `EnvelopeVersion`.

## Tasks Completed

| Task | Name                                                                  | Commit    | Files                                                                       |
| ---- | --------------------------------------------------------------------- | --------- | --------------------------------------------------------------------------- |
| 1    | Extend SlackEnvelope + add ActionUpload const + BuildEnvelopeUpload   | `7496367` | `pkg/slack/payload.go` (pre-committed under Plan 68-01's commit; see notes) |
| 2    | Promote Wave 0 stubs to canonical-JSON forward+backward compat tests  | `78955b8` | `pkg/slack/payload_transcript_test.go`, `pkg/slack/payload_test.go`         |

## Verification

`go build ./...` — clean
`go test ./pkg/slack/... -count=1` — `ok pkg/slack` + `ok pkg/slack/bridge`
Plan 68-02's four target tests — all PASS (zero SKIPs):
- `TestCanonicalJSON_ActionUpload` (key ordering + round-trip)
- `TestCanonicalJSON_PostUnchangedAfterAdditiveFields` (backwards compat with zero values)
- `TestBuildEnvelopeUpload_ValidatesRequired` (8 sub-cases: empty channel, empty s3key, bad filename ×4, size zero, size negative)
- `TestBuildEnvelopeUpload_RoundTripCanonical` (deterministic across Marshal→Unmarshal→Marshal)

`grep '"size_bytes"' pkg/slack/payload.go` — matches
`grep ActionUpload pkg/slack/payload.go` — matches

## Implementation Notes

### Struct shape

The new `SlackEnvelope` has 13 fields in strict alphabetical-by-JSON-tag order:

```
action, body, channel, content_type, filename, nonce, s3_key,
sender_id, size_bytes, subject, thread_ts, timestamp, version
```

Any deviation breaks Ed25519 signature verification on either end of the wire because `CanonicalJSON` relies on Go's struct-field-order serialization (no manual sort). The test `TestCanonicalJSON_FieldOrderAlphabetical` (in `payload_test.go`) re-asserts this order with a hard-pinned golden constant; that golden was updated as part of this plan to include the new zero-valued fields.

### Backwards compatibility

The four new fields are NOT `omitempty` — they always serialize, even with zero values. This is the load-bearing decision behind keeping `EnvelopeVersion=1`: every envelope now produces a 13-field canonical document, and a Phase 63 client that signs a `post` envelope through this updated struct produces a byte-identical document to a Phase 68 bridge verifying it. The bridge ignores `s3_key` / `filename` / `content_type` / `size_bytes` for non-upload actions.

### Validation

`BuildEnvelopeUpload` rejects:
- `channel == ""` → `ErrUploadChannelEmpty`
- `s3Key == ""` → `ErrUploadS3KeyEmpty`
- filename violations (empty / >255 bytes / contains `/` / contains NUL) → `ErrUploadFilenameInvalid`
- `sizeBytes <= 0` → `ErrUploadSizeInvalid`

`contentType` is NOT validated client-side; the bridge will enforce an allow-list in Plan 08 to keep the validation boundary at the network edge.

## Deviations from Plan

### Task 1 pre-commit (process accident, not plan deviation)

**Found during:** Task 1 staging (`git status`)
**Issue:** `pkg/slack/payload.go` had been edited and committed under commit `7496367` ("feat(68-01): add NotifySlackTranscriptEnabled profile field + JSON schema") in a prior session. The slack-payload changes (which are Plan 68-02's Task 1 scope) were bundled into a Plan 68-01 commit.
**Fix:** Verified the in-tree implementation matches Plan 68-02's spec exactly (struct shape, constants, constructor signature, named errors, validation rules) — no rework needed. Recorded the cross-plan commit reference in the Tasks Completed table so the audit trail still resolves.
**Commit:** `7496367` (existing)

### Plan 68-01 validation tests bundled into Task 2 commit (worktree-staging accident)

**Found during:** Task 2 commit (`git show --stat 78955b8`)
**Issue:** While running `git add pkg/slack/payload_test.go pkg/slack/payload_transcript_test.go`, a third file (`pkg/profile/validate_slack_transcript_test.go`) that had been modified in a prior session was already-staged in the index from before my reset and got swept into the commit. That file contains Plan 68-01 follow-up work (real assertions for `notifySlackTranscriptEnabled` validation rules, expecting validation logic that Plan 68-01 has not yet delivered to `pkg/profile/validate.go`).
**Impact:** Three tests in `pkg/profile/...` now fail (`TestValidate_SlackTranscript_RequiresSlackEnabled`, `TestValidate_SlackTranscript_RequiresPerSandbox`, `TestValidate_SlackTranscript_IncompatibleWithChannelOverride`). These failures are NOT caused by Plan 68-02 changes and are out-of-scope per the GSD scope-boundary rule.
**Disposition:** Logged in `deferred-items.md` for Plan 68-01's executor to pick up when adding the validation rules. Plan 68-02's actual scope (`pkg/slack/...`) remains 100% green.
**Commit:** `78955b8`

## Deferred Issues

See `.planning/phases/68-slack-transcript-streaming-per-turn-chat-and-gzipped-jsonl-upload/deferred-items.md` § "Plan 68-02 follow-up" for the three pre-existing-Plan-68-01 validation test failures bundled into commit `78955b8`.

## Self-Check: PASSED

- `pkg/slack/payload.go` — FOUND (contains `ActionUpload`, `ContentType string`, `BuildEnvelopeUpload`, `ErrUpload*`)
- `pkg/slack/payload_test.go` — FOUND (golden constant updated for 4 new fields; field-order list updated)
- `pkg/slack/payload_transcript_test.go` — FOUND (4 stub bodies replaced with real assertions; zero `t.Skip` calls remain)
- Commit `7496367` — FOUND (`git log` shows it on branch)
- Commit `78955b8` — FOUND (`git log` shows it on branch)
- Plan 68-02 target tests — 4/4 PASS (verified with `-v` run)
- `go build ./...` — clean
- Plan's primary verification scope (`go test ./pkg/slack/...`) — 100% PASS
