---
phase: 17-sandbox-email-mailbox-access-control-aliases-allow-lists-self-mail-s3-reader
verified: 2026-03-23T00:00:00Z
status: passed
score: 20/20 must-haves verified
re_verification: false
---

# Phase 17: Sandbox Email Mailbox Access Control Verification Report

**Phase Goal:** Sandbox aliases (human-friendly dot-notation names like `research.team-a`), profile-driven email allow-lists controlling which sandboxes can send to this sandbox (even if they have valid public keys), implicit self-mail capability for long-term agent memory, and a Go library for reading/parsing raw MIME emails stored in S3 by the SES receipt rule.

**Verified:** 2026-03-23
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| #  | Truth | Status | Evidence |
|----|-------|--------|----------|
| 1  | EmailSpec struct has Alias and AllowedSenders fields | VERIFIED | `pkg/profile/types.go` lines 64–68: `Alias string yaml:"alias,omitempty"` and `AllowedSenders []string yaml:"allowedSenders,omitempty"` |
| 2  | JSON schema validates alias as lowercase dot-notation pattern | VERIFIED | `pkg/profile/schemas/sandbox_profile.schema.json` lines 79–83: pattern `^[a-z][a-z0-9-]*\\.[a-z][a-z0-9-]*$` present |
| 3  | JSON schema accepts allowedSenders as array of strings | VERIFIED | Schema lines 84–88: `allowedSenders` as `array` with `items: { type: string }` |
| 4  | DynamoDB identities module v1.1.0 has alias GSI | VERIFIED | `infra/modules/dynamodb-identities/v1.1.0/main.tf`: `alias-index` GSI with `hash_key = "alias"`, `projection_type = "ALL"` |
| 5  | Built-in profiles have correct allowedSenders defaults | VERIFIED | `open-dev.yaml` + `restricted-dev.yaml`: `["*"]`; `hardened.yaml` + `sealed.yaml`: `["self"]` |
| 6  | FetchPublicKeyByAlias resolves alias via alias-index GSI | VERIFIED | `identity.go` line 283: queries `IndexName: "alias-index"` with `alias = :alias`, returns `(nil, nil)` for unknown alias |
| 7  | matchesAllowList permits self-mail unconditionally | VERIFIED | `identity.go` line 368–370: `"self"` case returns true when `senderID == receiverSandboxID` |
| 8  | matchesAllowList supports *, exact sandbox ID, and wildcard alias patterns | VERIFIED | `identity.go` lines 362–384: `*`, `self`, exact ID, and `path.Match(p, senderAlias)` all handled |
| 9  | matchesAllowList rejects senders not on the list | VERIFIED | Returns `false` at line 383 when no pattern matched |
| 10 | ListMailboxMessages lists all S3 objects under mail/ prefix without filtering | VERIFIED | `mailbox.go` lines 56–83: prefix `"mail/"`, paginated, returns all keys without filtering |
| 11 | ReadMessage fetches raw MIME bytes from S3 | VERIFIED | `mailbox.go` lines 88–103: GetObject, io.ReadAll body, returns raw bytes |
| 12 | ParseSignedMessage parses MIME headers, verifies Ed25519 signature, enforces allow-list | VERIFIED | `mailbox.go` lines 120–182: `net/mail.ReadMessage`, header extraction, VerifyEmailSignature call, MatchesAllowList gate |
| 13 | ParseSignedMessage handles plaintext (unsigned) messages gracefully | VERIFIED | `mailbox.go` lines 162–163: sets `Plaintext=true` when `sigB64 == ""` |
| 14 | ParseSignedMessage permits self-mail regardless of allowedSenders | VERIFIED | `mailbox.go` lines 144–149: `isSelfMail` bypass before MatchesAllowList |
| 15 | PublishIdentity stores alias and allowed_senders in DynamoDB | VERIFIED | `identity.go` lines 183–188: `alias` as `AttributeValueMemberS`, `allowed_senders` as `AttributeValueMemberSS` |
| 16 | km create passes alias and allowedSenders from profile to PublishIdentity | VERIFIED | `create.go` lines 479–481: reads `resolvedProfile.Spec.Email.Alias` and `.AllowedSenders`, passes as trailing args |
| 17 | km status displays Alias when set on identity record | VERIFIED | `status.go` lines 346–348: conditional `identity.Alias != ""` guard, prints `Alias:` line |
| 18 | km status displays Allowed Senders summary alongside identity fields | VERIFIED | `status.go` lines 349–353: prints comma-joined list or "not configured" |
| 19 | km status omits Alias line when alias is empty | VERIFIED | `status.go` line 346: only prints when `identity.Alias != ""` |
| 20 | km status shows 'not configured' when AllowedSenders is empty | VERIFIED | `status.go` line 352: else branch prints "not configured" |

**Score:** 20/20 truths verified

---

### Required Artifacts

| Artifact | Provides | Status | Evidence |
|----------|----------|--------|----------|
| `pkg/profile/types.go` | EmailSpec with Alias and AllowedSenders fields | VERIFIED | Both fields present at lines 64–68; omitempty yaml tags correct |
| `pkg/profile/schemas/sandbox_profile.schema.json` | alias pattern validation and allowedSenders array | VERIFIED | Lines 79–88; pattern `^[a-z][a-z0-9-]*\\.[a-z][a-z0-9-]*$` enforced |
| `infra/modules/dynamodb-identities/v1.1.0/main.tf` | DynamoDB table with alias GSI | VERIFIED | `alias-index` GSI with ALL projection; Version tag "v1.1.0" |
| `infra/live/use1/dynamodb-identities/terragrunt.hcl` | Terragrunt config pointing to v1.1.0 | VERIFIED | Line 33: `source = ".../v1.1.0"` |
| `pkg/aws/identity.go` | IdentityQueryAPI, FetchPublicKeyByAlias, MatchesAllowList, extended PublishIdentity and IdentityRecord | VERIFIED | All four exported symbols present and substantive |
| `pkg/aws/mailbox.go` | MailboxS3API, MailboxMessage, ListMailboxMessages, ReadMessage, ParseSignedMessage | VERIFIED | All symbols present; ErrSenderNotAllowed sentinel defined |
| `pkg/aws/mailbox_test.go` | Unit tests for mailbox reader | VERIFIED | TestListMailboxMessages, TestReadMessage, TestParseSignedMessage (9+ test functions) |
| `pkg/aws/identity_test.go` | Tests for alias fetch, allow-list, extended PublishIdentity | VERIFIED | TestFetchPublicKeyByAlias (3 funcs), TestMatchesAllowList (7 funcs), TestPublishIdentity (3 funcs) |
| `pkg/profile/parse_test.go` | Schema + parse tests for alias/allowedSenders | VERIFIED | TestParse_EmailAlias, TestParse_EmailAlias_Empty, TestValidateSchema_EmailAlias_Invalid, TestValidateSchema_EmailAlias_NoDot |
| `internal/app/cmd/create.go` | PublishIdentity call with alias and allowedSenders from profile EmailSpec | VERIFIED | Line 481: all 12 params passed including `alias` and `allowedSenders` |
| `internal/app/cmd/status.go` | printSandboxStatus displaying Alias and Allowed Senders | VERIFIED | Lines 346–353: conditional Alias, always-shown Allowed Senders |
| `internal/app/cmd/status_test.go` | Tests for alias and allowedSenders display in km status | VERIFIED | TestStatus_EmailAlias and TestStatus_EmailAlias_Empty present |
| `internal/app/cmd/create_test.go` | Source-level tests for create.go alias wiring | VERIFIED | TestRunCreate_PublishIdentityAliasWiring and TestRunCreate_PublishIdentityAliasBackwardCompat present |

---

### Key Link Verification

| From | To | Via | Status | Evidence |
|------|----|-----|--------|----------|
| `pkg/profile/schemas/sandbox_profile.schema.json` | `pkg/profile/types.go` | Schema validates what types.go defines — alias pattern | WIRED | Schema pattern `^[a-z][a-z0-9-]*\\.[a-z][a-z0-9-]*$` corresponds to `Alias string` field in EmailSpec |
| `pkg/aws/mailbox.go` | `pkg/aws/identity.go` | ParseSignedMessage calls VerifyEmailSignature | WIRED | `mailbox.go` line 157: `VerifyEmailSignature(pubKeyB64, body, sigB64)` called |
| `pkg/aws/identity.go` | DynamoDB alias-index GSI | FetchPublicKeyByAlias queries alias-index | WIRED | `identity.go` line 283: `IndexName: aws.String("alias-index")` |
| `internal/app/cmd/create.go` | `pkg/aws/identity.go` | PublishIdentity call with alias + allowedSenders params | WIRED | `create.go` line 481: `awspkg.PublishIdentity(..., alias, allowedSenders)` |
| `internal/app/cmd/status.go` | `pkg/aws/identity.go` | IdentityRecord.Alias and .AllowedSenders displayed | WIRED | `status.go` lines 346, 349: `identity.Alias` and `identity.AllowedSenders` read |

---

### Requirements Coverage

The plan-internal requirement IDs (MAIL-ALIAS-SCHEMA, MAIL-ALLOWLIST-SCHEMA, MAIL-GSI-INFRA, MAIL-BUILTIN-DEFAULTS, MAIL-ALIAS-LOOKUP, MAIL-ALLOWLIST-ENFORCE, MAIL-SELFMAIL, MAIL-MAILBOX-READER, MAIL-STATUS-DISPLAY, MAIL-CREATE-WIRING) are local tracking identifiers used within Phase 17 plans. They are not registered in `.planning/REQUIREMENTS.md`, which covers Phases 1–7 only and stops with the note "Unmapped: 0" for its v1 requirement set. The ROADMAP entry for Phase 17 lists the feature requirements as prose (not table IDs). No orphaned REQUIREMENTS.md entries were found for Phase 17.

| Plan-internal ID | Plan | Feature | Status | Evidence |
|-----------------|------|---------|--------|----------|
| MAIL-ALIAS-SCHEMA | 17-01 | EmailSpec.Alias field + schema pattern | SATISFIED | types.go lines 64–65; schema lines 79–83 |
| MAIL-ALLOWLIST-SCHEMA | 17-01 | EmailSpec.AllowedSenders field + schema array | SATISFIED | types.go lines 66–68; schema lines 84–88 |
| MAIL-GSI-INFRA | 17-01 | DynamoDB v1.1.0 alias-index GSI | SATISFIED | v1.1.0/main.tf: alias-index GSI with ALL projection |
| MAIL-BUILTIN-DEFAULTS | 17-01 | hardened/sealed=["self"], open-dev/restricted-dev=["*"] | SATISFIED | All four profiles updated correctly |
| MAIL-ALIAS-LOOKUP | 17-02 | FetchPublicKeyByAlias via GSI | SATISFIED | identity.go lines 280–347 |
| MAIL-ALLOWLIST-ENFORCE | 17-02 | MatchesAllowList with *, self, exact, wildcard | SATISFIED | identity.go lines 362–384 |
| MAIL-SELFMAIL | 17-02 | Self-mail bypass in ParseSignedMessage | SATISFIED | mailbox.go lines 144–149 |
| MAIL-MAILBOX-READER | 17-02 | ListMailboxMessages, ReadMessage, ParseSignedMessage | SATISFIED | mailbox.go lines 55–182 |
| MAIL-STATUS-DISPLAY | 17-03 | km status shows Alias and Allowed Senders | SATISFIED | status.go lines 346–353 |
| MAIL-CREATE-WIRING | 17-03 | create.go threads alias/allowedSenders to PublishIdentity | SATISFIED | create.go line 481 |

---

### Anti-Patterns Found

No anti-patterns found. Scan of all phase-modified files:

- `pkg/profile/types.go` — no TODO/FIXME, no empty implementations
- `pkg/aws/identity.go` — no TODO/FIXME, no stub returns
- `pkg/aws/mailbox.go` — no TODO/FIXME, no stub returns
- `internal/app/cmd/create.go` — no TODO/FIXME in modified section
- `internal/app/cmd/status.go` — no TODO/FIXME in modified section

Note: `ParseSignedMessage` documents that NaCl decryption is caller responsibility (body contains ciphertext when `Encrypted=true`). This is a deliberate design decision documented in the plan and SUMMARY, not a stub — the function correctly sets `Encrypted=true` and the caller must invoke `DecryptFromSender` separately.

---

### Human Verification Required

None required. All behaviors are verifiable programmatically through code inspection and test coverage.

Items that would benefit from integration-test confirmation but are not blocking:

1. **End-to-end mailbox read flow** — `ListMailboxMessages` + `ReadMessage` + `ParseSignedMessage` chained together against a real S3 bucket with SES-deposited emails. Test pattern and unit coverage are thorough; the chain is inspectable from code.

2. **km validate with alias** — Running `km validate` against a profile with an alias value (e.g., `"Research.team-a"`) to confirm the JSON schema pattern enforcement works through the full CLI pipeline. The four schema tests in `parse_test.go` cover this at the unit level.

---

### Commits Verified

All 10 commits referenced across the three plan summaries exist in the repository:

| Commit | Plan | Description |
|--------|------|-------------|
| 19de269 | 17-01 | test: failing tests for EmailSpec alias and allowedSenders |
| 840db3a | 17-01 | feat: extend EmailSpec with Alias and AllowedSenders fields |
| f59317c | 17-01 | feat: DynamoDB v1.1.0 module + profile defaults |
| d423f58 | 17-02 | test: failing tests for identity extension |
| f06433e | 17-02 | feat: extend identity.go (IdentityQueryAPI, FetchPublicKeyByAlias, MatchesAllowList) |
| 52a8d4a | 17-02 | test: failing tests for mailbox reader |
| 0520186 | 17-02 | feat: create mailbox.go |
| 8b6d736 | 17-03 | feat: source-level tests for create.go alias wiring |
| 6afc9f4 | 17-03 | test: failing tests for km status alias display |
| ba37639 | 17-03 | feat: extend km status Alias and Allowed Senders display |

---

### Gaps Summary

No gaps. All 20 observable truths are verified. All artifacts exist, are substantive, and are wired. All key links are confirmed active in the code.

The v1.0.0 DynamoDB module is untouched (verified: `infra/modules/dynamodb-identities/v1.0.0/` still exists alongside v1.1.0). The `terragrunt.hcl` correctly points to v1.1.0. The `IdentityQueryAPI` uses the narrow-interface pattern (Query only, separate from IdentityTableAPI), consistent with the codebase convention.

---

_Verified: 2026-03-23_
_Verifier: Claude (gsd-verifier)_
