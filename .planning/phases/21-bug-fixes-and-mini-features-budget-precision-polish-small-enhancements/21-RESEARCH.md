# Phase 21: Bug Fixes and Mini-Features — Budget Precision, Polish, Small Enhancements

**Researched:** 2026-03-25
**Domain:** Go CLI polish, CloudWatch Logs export, sidecar E2E verification, email mechanics, AWS SDK v2
**Confidence:** HIGH (codebase is fully auditable; all findings are code-verified)

---

## Summary

Phase 21 is a broad polish-and-verification phase with nine distinct work items. They fall into three
categories: (1) formatting fixes with existing infrastructure (budget precision), (2) new capability
additions that integrate into existing patterns (CloudWatch log export on teardown, OTP/credential sync),
and (3) end-to-end validation tests that verify already-implemented features work correctly under live
conditions (sidecar E2E, GitHub repo cloning, inter-sandbox email, allow-list enforcement, safe-phrase
override, email action approval).

The codebase is fully green (`go test ./...` — all packages pass as of 2026-03-25). Every feature
referenced in Phase 21 has at least the underlying SDK plumbing present in Go modules (`aws-sdk-go-v2`),
even if not yet called. No new external libraries are needed for any item.

**Primary recommendation:** Plan Phase 21 as three self-contained plan files: (A) budget precision
formatting + CloudWatch log export on teardown, (B) E2E sidecar + GitHub repo cloning verification
tests, and (C) email feature group (inter-sandbox send/receive, allow-list enforcement, safe-phrase
override, email action approval, OTP sync). Items in (A) are pure code changes with no infrastructure;
items in (B) are test harness additions; items in (C) are the highest complexity and may require new
Lambda or mailbox-reader wiring.

---

## Standard Stack

### Core (already in go.mod — no new dependencies needed)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs` | v1.64.1 | CreateExportTask API for log archival | Already in go.mod; `CWLogsAPI` interface in `pkg/aws/cloudwatch.go` needs one new method |
| `github.com/aws/aws-sdk-go-v2/service/s3` | v1.97.1 | Export destination bucket already used for artifacts | Already in go.mod, patterns established in `pkg/aws/artifacts.go` |
| `github.com/aws/aws-sdk-go-v2/service/sesv2` | v1.60.1 | SES receive/send for email test scenarios | Already in go.mod, used in `pkg/aws/ses.go` |
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | v1.57.0 | Identity/mailbox read patterns, OTP storage | Already in go.mod, used throughout |
| `encoding/mime/multipart`, `net/mail` | stdlib | Email parsing in mailbox.go tests | Already used in `pkg/aws/mailbox.go` |

### No New Imports Required

All nine Phase 21 items use libraries already present. Do not add new go.mod entries.

---

## Architecture Patterns

### Recommended Approach by Work Item

#### Item 1: Budget Display Precision (4 decimal places)

**What needs changing:**

| File | Line(s) | Current | Change to |
|------|---------|---------|-----------|
| `internal/app/cmd/status.go` | 287, 296, 308 | `%.2f` | `%.4f` |
| `internal/app/cmd/budget.go` | 208 | `%.2f` | `%.4f` |
| `internal/app/cmd/create.go` | 456 | `%.2f` | `%.4f` |
| `cmd/configui/handlers_budget.go` | 67 | `$%.2f` | `$%.4f` |

The budget-enforcer Lambda already uses `%.4f` in its internal log messages
(`cmd/budget-enforcer/main.go:163,186`). The bedrock proxy already uses `%.4f` in its error strings
(`sidecars/http-proxy/httpproxy/bedrock.go:69`). Making user-facing CLI output consistent with the
internal representation closes a visual discrepancy where "$0.00" displayed even as sub-penny charges
accumulated.

**Test impact:** `status_test.go` and `budget_test.go` may have hardcoded `$0.00`-style strings in
`strings.Contains` assertions. These will NOT break on `%.4f` change if they check for partial strings
like `"$0."` rather than `"$0.00"`. Verify each test before submitting.

**Pattern:** Change format verbs only. No logic change, no struct change, no interface change.

#### Item 2: CloudWatch Log Export on Teardown

**What exists:**
- `pkg/aws/cloudwatch.go` has `CWLogsAPI` interface + `DeleteSandboxLogGroup()`
- `internal/app/cmd/destroy.go` Step 13 calls `DeleteSandboxLogGroup` at teardown
- CloudWatch Logs SDK `CreateExportTask` is available in `aws-sdk-go-v2/service/cloudwatchlogs`

**What does not exist:**
- No `CreateExportTask` call anywhere in the codebase (verified: grep finds zero matches)
- No `ExportSandboxLogs()` function in `pkg/aws/cloudwatch.go`

**Pattern to follow:**
```go
// Add to CWLogsAPI interface in pkg/aws/cloudwatch.go
CreateExportTask(ctx context.Context, params *cloudwatchlogs.CreateExportTaskInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.CreateExportTaskOutput, error)

// New function signature
func ExportSandboxLogs(ctx context.Context, client CWLogsAPI, sandboxID, destBucket, destPrefix string) error
```

**Integration point:** In `destroy.go`, call `ExportSandboxLogs` BEFORE `DeleteSandboxLogGroup` (Step 13),
so logs are archived before deletion. Make it non-fatal (log.Warn + continue), consistent with all other
Step 12/13 cleanup steps. The destination bucket is `artifactsBucket` (already available in the destroy
path).

**AWS behavior note:** `CreateExportTask` is asynchronous — it returns a task ID immediately; completion
takes seconds to minutes. The export task runs in the background; destroy proceeds without waiting.
Idempotency: no guard needed since the log group is deleted immediately after (cannot export the same
group twice).

**IAM requirement:** The Lambda teardown path (`cmd/ttl-handler/main.go`) also calls destroy-like
cleanup. It uses `DestroySandboxResources` which calls `DeleteSandboxLogGroup`. The TTL handler should
also get the export step. The IAM role for the TTL handler will need `logs:CreateExportTask` +
`s3:PutObject` on the artifacts bucket (add to `infra/modules/ttl-handler/` IAM policy).

#### Item 3: E2E Sidecar Verification

**What exists:**
- All four sidecar binaries build and pass unit tests:
  - `sidecars/dns-proxy/dnsproxy` — DNS filtering (test package exists)
  - `sidecars/http-proxy/httpproxy` — HTTP MITM + budget metering (full test suite)
  - `sidecars/audit-log` — command/network audit logging (test package exists)
  - `sidecars/tracing/config.yaml` — OTel collector configuration (no Go tests, pure YAML)
- `make sidecars` cross-compiles and uploads to S3
- `make ecr-push` builds Docker images and pushes to ECR

**What does not exist:**
- No E2E integration test file that spins up sidecars and verifies behavior end-to-end
- No `TestE2E*` or `integration_test.go` pattern in the codebase

**Recommended approach:** Since real ECS/EC2 provisioning is required for true E2E, this item is
primarily a **verification checklist** rather than an automated test. Create a test script or structured
manual test procedure that documents:
1. DNS proxy: `nslookup blocked.example.com` fails; `nslookup allowed.example.com` succeeds
2. HTTP proxy: `curl https://blocked-host.com` returns proxy error; allowed host succeeds
3. Audit log: CloudWatch log group `/km/sandboxes/{id}/` contains entries after commands run
4. OTel tracing: Traces appear in the configured endpoint after sandbox workload runs

If automated unit-level tests are needed, mock-based integration tests within the `httpproxy` package
already exercise budget enforcement behavior. The "sidecar E2E verification" scope item means documenting
what to check, not adding new unit tests.

#### Item 4: GitHub Repo Cloning / Locking Validation

**What exists:**
- `pkg/github/token.go` — JWT generation, installation token exchange
- `cmd/github-token-refresher/` — Lambda for token refresh
- `pkg/compiler/userdata.go` — GIT_ASKPASS credential helper injected at boot
- `pkg/compiler/service_hcl.go` — `github_token_inputs` emitted in service.hcl

**What does not exist:**
- No integration test verifying that a sandbox can actually `git clone` a private repo
- No test of ref enforcement (push to non-allowed refs should fail)

**Recommended approach:** This is a live-AWS validation item. The planner should treat it as a
verification checklist task: (a) confirm `km configure github` stores credentials, (b) `km doctor`
GitHub check passes, (c) provision an ECS sandbox with `sourceAccess.github` configured, (d) inside
sandbox verify `git clone` works for allowed repo, fails for non-allowed repo.

#### Item 5: Inter-Sandbox Email Send/Receive Test

**What exists:**
- `pkg/aws/ses.go` — `SendSandboxEmail`, `SendSignedEmail`, `SendLifecycleNotification`
- `pkg/aws/identity.go` — `SignEmailBody`, `VerifyEmailSignature`, `SendSignedEmail`
- `pkg/aws/mailbox.go` — `ListMailboxMessages`, `ReadMessage`, `ParseSignedMessage`
- SES receipt rule stores inbound email to S3 at `mail/{recipient-sandbox-id}/`

**What does not exist:**
- No end-to-end test sending email from sandbox A and reading it from sandbox B
- No Lambda handler that processes inbound email (Phase 14 notes explicitly deferred this)

**Pattern for verification:** Two live sandbox instances with email configured; sandbox A calls
`SendSignedEmail` to sandbox B's address; sandbox B calls `ListMailboxMessages` + `ReadMessage` +
`ParseSignedMessage`; verify `SignatureOK == true`.

**Key constraint from Phase 14:** "Phase 14 provides the verification library only; wiring into an SES
receipt handler to enforce rejection at delivery time requires a future phase." This means inter-sandbox
email can be sent and stored in S3; reading it from S3 works via `mailbox.go`; but there is no live
Lambda that actively rejects malformed emails. Keep scope to send/read/verify, not reject-on-receive.

#### Item 6: Email Allow-List Enforcement Test

**What exists:**
- `pkg/aws/identity.go` — `MatchesAllowList()` exported, used in `ParseSignedMessage`
- `MatchesAllowList` handles: `"self"`, exact IDs, alias patterns with `*`, `"*"` wildcard
- `ParseSignedMessage` enforces allow-list before signature verification (Phase 17-02 decision)

**Pattern:** Unit-testable via `MatchesAllowList`. For E2E: provision sandbox with restricted
`allowedSenders` (e.g. `["self"]`), attempt to send from a third sandbox, verify
`ParseSignedMessage` sets `SignatureOK = false` or returns rejection. No new code needed if this is
a verification/test-writing task.

#### Item 7: Safe Phrase Email Override

**What does not exist:** No implementation of safe-phrase parsing or email override logic anywhere in
the codebase. This is new behavior.

**Recommended pattern:**
```go
// pkg/aws/mailbox.go — add to ParsedMessage struct
SafePhrase  string // extracted if message body contains "KM-AUTH: <phrase>"
SafePhraseOK bool  // true if phrase matches expected value
```

The "safe phrase" is an operator-configured secret embedded in email body to authorize override actions.
Use a simple `strings.Contains` or regex scan in `ParseSignedMessage`. Store the expected phrase in SSM
at `/sandbox/{sandbox-id}/safe-phrase` alongside other per-sandbox secrets. Check phrase at email
processing time. This builds on existing SSM patterns from Phase 6.

**Configuration:** Add `spec.email.safePhrase` (optional string) to `EmailSpec` in
`pkg/profile/types.go`. At `km create` time, store phrase in SSM. At email-processing time, read from
SSM and compare.

#### Item 8: Klanker Action Approval via Email

**What does not exist:** No action approval workflow. This is new behavior.

**Recommended pattern:** When a klanker needs approval before executing an action, it emails
`whereiskurt+klankerqq@gmail.com` (the operator's address pattern) with a structured subject like
`[KM-APPROVAL-REQUEST] <sandbox-id> <action>`. The operator replies; a reply-listener Lambda or
next-poll in `ListMailboxMessages` reads the reply and checks for approval/denial keywords. This
pattern is consistent with the MAIL requirements (MAIL-05: cross-account orchestration via email).

**Implementation path:** This requires a mailbox-polling pattern. Sandbox sends approval request →
operator receives, replies with `APPROVED` or `DENIED` → sandbox polls its own mailbox (self-mail is
always permitted per Phase 17) for replies → sandbox reads response via `ListMailboxMessages`.

**Key dependency:** The operator's outbound email to `{sandbox-id}@sandboxes.{domain}` must route
through the SES receipt rule → S3. This is already configured. Self-mail is already permitted
unconditionally by `MatchesAllowList` (`"self"` sentinel). The sandbox can read its own mailbox via
`mailbox.go`.

#### Item 9: One-Time Password Sync

**What does not exist:** No OTP/credential sync mechanism. This is new behavior.

**Recommended pattern:** At sandbox creation, operator pre-loads a secret into SSM at a well-known
path. The OTP sync injects this into the sandbox environment via the existing SSM secret injection
pattern in `pkg/compiler/userdata.go` (already handles `spec.network.secretAllowList`). For a true
one-time-use pattern, the secret is deleted from SSM after first read. An alternative: use SSM
SecureString with a deletion Lambda or a post-read cleanup step in user-data.

**Simplest implementation:** Add an optional `spec.otp.secrets` list to the profile that works like
`secretAllowList` but adds a `--delete-after-read` flag in user-data. The sandbox reads the OTP
secret from SSM at boot, uses it once, then a cleanup script deletes the SSM parameter. No new
infrastructure needed — this uses the existing SSM client in user-data.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Budget amount formatting | Custom format function | `fmt.Sprintf("$%.4f", ...)` | Already the Go idiom used everywhere in the codebase |
| CloudWatch export polling | Custom S3 poll loop | `CreateExportTask` (fire-and-forget) | AWS handles async export; destroy proceeds without waiting |
| Email body parsing | Custom MIME parser | `net/mail` stdlib + existing `ParseSignedMessage` | `mailbox.go` already uses stdlib mime parsing |
| OTP storage | Custom vault | SSM Parameter Store + KMS | Already used for signing keys, GitHub tokens |
| Safe-phrase storage | In-memory or env var | SSM Parameter Store | SSM is already the secret store for all per-sandbox secrets |

---

## Common Pitfalls

### Pitfall 1: Test String Assertions Break on %.4f Change
**What goes wrong:** `status_test.go` uses `strings.Contains(out, "$0.00")` or exact dollar strings;
changing to `%.4f` generates `"$0.0000"` and breaks those assertions.
**Why it happens:** Tests written when `%.2f` was the format.
**How to avoid:** Before changing format verbs, grep all test files for hardcoded dollar strings.
Update expectations to `"$0.0"` prefix checks or `"$0.0000"` exact matches.
**Warning signs:** `go test` failures on `internal/app/cmd` package.

### Pitfall 2: CloudWatch CreateExportTask IAM Gap
**What goes wrong:** `ExportSandboxLogs` call succeeds in CLI path but fails in TTL Lambda because
the Lambda's IAM role lacks `logs:CreateExportTask`.
**Why it happens:** Lambda IAM policy was written before this feature existed.
**How to avoid:** Update `infra/modules/ttl-handler/main.tf` IAM policy inline document to add
`logs:CreateExportTask` and the S3 put permission on the artifacts bucket.
**Warning signs:** TTL Lambda produces `AccessDenied` in CloudWatch on teardown.

### Pitfall 3: CloudWatch Export Task Timing vs. Log Group Deletion
**What goes wrong:** `DeleteSandboxLogGroup` fires immediately after `CreateExportTask` which cancels
the export.
**Why it happens:** Export task is async; deletion happens before export completes.
**How to avoid:** Do NOT wait for export task completion (it can take minutes). Instead, accept that
export is best-effort and some recent logs may not be captured. Document this behavior. Delete the log
group regardless — it has a 7-day retention policy so the export window is always short anyway.

### Pitfall 4: SES Receipt to S3 Path Does Not Match Mailbox Reader
**What goes wrong:** Allow-list enforcement test fails because `ListMailboxMessages` finds no messages.
**Why it happens:** SES receipt rule stores mail at `mail/{recipient-email-address}/` but
`ListMailboxMessages` lists `mail/{sandbox-id}/`. If the SES rule uses email address as prefix, not
sandbox ID, messages are stored under the wrong prefix.
**How to avoid:** Verify the SES receipt rule prefix in `infra/modules/ses/` matches the `mail/` prefix
pattern expected by `mailbox.go`. Phase 17-02 decision: "ListMailboxMessages uses `mail/` prefix flat
(Option A per research); no per-recipient subdirectory filtering."

### Pitfall 5: Safe Phrase in Clear Text in Profile YAML
**What goes wrong:** Operator defines safe phrase in `spec.email.safePhrase` in the profile YAML,
which is stored in the Git repo.
**Why it happens:** Convenience of inline configuration.
**How to avoid:** The safe phrase field in the profile YAML should be a reference to an SSM path, not
the phrase itself. Use the same pattern as `spec.network.secretAllowList` (SSM path reference).
Alternatively, generate the phrase at `km create` time and never put it in the profile YAML.

### Pitfall 6: Email Action Approval — Operator Reply Address
**What goes wrong:** Operator reply arrives at `whereiskurt+klankerqq@gmail.com` but the sandbox polls
its own mailbox at `{sandbox-id}@sandboxes.{domain}` — these are different inboxes.
**Why it happens:** The approval request is sent to an external Gmail address, not to the SES domain.
Replies from Gmail go back to the From address of the original request, which must be the sandbox's own
SES address.
**How to avoid:** The approval request email must set `From: {sandbox-id}@sandboxes.{domain}` so
operator replies route to the sandbox's SES address. The SES receipt rule then delivers the reply to
S3 at `mail/{sandbox-id}/`. The sandbox polls its own mailbox.

---

## Code Examples

Verified patterns from existing codebase:

### Budget Format Change (status.go)
```go
// BEFORE (current):
fmt.Fprintf(out, "  Compute: $%.2f / $%.2f (%s)\n",
    budget.ComputeSpent, budget.ComputeLimit, colorPercent(pct, isTTY))

// AFTER (Phase 21):
fmt.Fprintf(out, "  Compute: $%.4f / $%.4f (%s)\n",
    budget.ComputeSpent, budget.ComputeLimit, colorPercent(pct, isTTY))
```

### CloudWatch Log Export (new function, pkg/aws/cloudwatch.go)
```go
// Add CreateExportTask to CWLogsAPI interface, then:
func ExportSandboxLogs(ctx context.Context, client CWLogsAPI, sandboxID, destBucket string) error {
    logGroup := "/km/sandboxes/" + sandboxID + "/"
    destPrefix := "logs/" + sandboxID
    now := time.Now().UnixMilli()
    from := now - int64(7*24*time.Hour.Milliseconds()) // last 7 days (matches retention)

    _, err := client.CreateExportTask(ctx, &cloudwatchlogs.CreateExportTaskInput{
        LogGroupName:        aws.String(logGroup),
        Destination:         aws.String(destBucket),
        DestinationPrefix:   aws.String(destPrefix),
        From:                aws.Int64(from),
        To:                  aws.Int64(now),
    })
    if err != nil {
        var notFound *types.ResourceNotFoundException
        if errors.As(err, &notFound) {
            return nil // no logs group = nothing to export
        }
        return fmt.Errorf("export log group %q to s3://%s/%s: %w", logGroup, destBucket, destPrefix, err)
    }
    return nil
}
```

### destroy.go integration (non-fatal, before deletion)
```go
// Step 13a: Export CloudWatch logs to S3 before deleting the log group.
cwClient := cloudwatchlogs.NewFromConfig(awsCfg)
if artifactsBucket != "" {
    if exportErr := awspkg.ExportSandboxLogs(ctx, cwClient, sandboxID, artifactsBucket); exportErr != nil {
        log.Warn().Err(exportErr).Str("sandbox_id", sandboxID).
            Msg("failed to export sandbox logs to S3 (non-fatal)")
    } else {
        log.Info().Str("sandbox_id", sandboxID).Msg("sandbox logs export task created")
    }
}
// Step 13b: Delete CloudWatch log group.
if cwErr := awspkg.DeleteSandboxLogGroup(ctx, cwClient, sandboxID); cwErr != nil {
    log.Warn().Err(cwErr).Msg("failed to delete sandbox log group (non-fatal)")
}
```

### MatchesAllowList pattern (already in pkg/aws/identity.go)
```go
// Exported and unit-testable without AWS:
result := awspkg.MatchesAllowList([]string{"self", "build.*"}, "sb-xyz123", "sb-xyz123") // true (self)
result = awspkg.MatchesAllowList([]string{"self"}, "sb-xyz123", "sb-aaa111") // false
```

### Safe phrase storage pattern (mirrors existing SSM secret pattern)
```go
// km create: store safe phrase in SSM if spec.email.safePhrase is set
if prof.Spec.Email != nil && prof.Spec.Email.SafePhrase != "" {
    paramPath := "/sandbox/" + sandboxID + "/safe-phrase"
    _, err := ssmClient.PutParameter(ctx, &ssm.PutParameterInput{
        Name:      aws.String(paramPath),
        Value:     aws.String(prof.Spec.Email.SafePhrase),
        Type:      ssm.ParameterTypeSecureString,
        KeyId:     aws.String(kmsKeyARN),
        Overwrite: aws.Bool(true),
    })
    // non-fatal, warn-and-continue per established pattern
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `%.2f` for all spend display | `%.4f` needed for sub-penny | Phase 21 | Shows `$0.0012` instead of `$0.00` |
| Delete log group at teardown | Export then delete | Phase 21 | Artifacts bucket gets audit trail |
| Validation library only for email verification | Library + E2E test | Phase 21 | Confirmed correct in production |

**Deprecated/outdated:**
- `%.2f` budget format strings: all five occurrences should become `%.4f` in Phase 21

---

## Open Questions

1. **CloudWatch Export Task — S3 bucket policy**
   - What we know: `CreateExportTask` requires the destination S3 bucket to have a bucket policy
     granting `logs.amazonaws.com` the `s3:PutObject` permission.
   - What's unclear: Does the existing artifacts bucket (provisioned in Phase 5/9) have this policy?
   - Recommendation: Check `infra/modules/s3-artifacts/` bucket policy. If missing, add a resource
     block granting `logs.amazonaws.com` `s3:PutObject` on the `logs/` prefix.

2. **OTP Sync — scope definition**
   - What we know: No implementation exists; SSM secret injection exists in user-data.
   - What's unclear: "Bootstrap credential/secret sync" could mean (a) one-time SSM parameter read at
     boot with post-read deletion, or (b) something more complex like a time-based OTP (TOTP).
   - Recommendation: Treat as (a) — a profile field `spec.otp.secrets` listing SSM paths that are
     read once at boot and then deleted via SSM `DeleteParameter`. Simpler, consistent with existing
     patterns, and auditable.

3. **Email action approval — reply routing confirmation**
   - What we know: SES receipt rule is configured; inbound mail routes to S3 at `mail/` prefix.
   - What's unclear: Does the SES receipt rule accept mail from external (Gmail) domains to the
     `{sandbox-id}@sandboxes.{domain}` address? SES receipt rules can be configured to accept only
     specific senders.
   - Recommendation: Verify `infra/live/use1/ses/terragrunt.hcl` receipt rule has no sender
     filtering. If it does, add an exception for the operator's Gmail address.

4. **Safe phrase — profile YAML vs. generated secret**
   - What we know: Phase 17 stores `alias` and `allowedSenders` in the profile YAML.
   - What's unclear: Should safe phrase be in the profile YAML (operator-defined) or generated
     at `km create` time (more secure, operator-delivered out-of-band)?
   - Recommendation: Generated at `km create` time, displayed once in CLI output (similar to SSH
     key display patterns), stored in SSM. Never in profile YAML.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing stdlib |
| Config file | none — `go test ./...` from repo root |
| Quick run command | `go test ./internal/app/cmd/... ./pkg/aws/... -run TestBudget -v` |
| Full suite command | `go test ./...` |

### Phase Requirements to Test Map

| Item | Behavior | Test Type | Automated Command | File Exists? |
|------|----------|-----------|-------------------|-------------|
| Budget precision | `km status` shows `$0.0000` not `$0.00` | unit | `go test ./internal/app/cmd/... -run TestStatus_Budget -v` | Update `status_test.go` |
| Budget precision | `km budget add` output uses 4 decimals | unit | `go test ./internal/app/cmd/... -run TestBudget -v` | Update `budget_test.go` |
| Budget precision | ConfigUI `formatMoney` helper uses 4 decimals | unit | `go test ./cmd/configui/... -run TestFormatMoney -v` | Add test to `handlers_budget_test.go` |
| CW log export | `ExportSandboxLogs` calls CreateExportTask | unit | `go test ./pkg/aws/... -run TestExportSandboxLogs -v` | Add to `cloudwatch_test.go` |
| CW log export | destroy step calls export before delete | unit | `go test ./internal/app/cmd/... -run TestDestroy -v` | Update `destroy_test.go` |
| Allow-list enforcement | `MatchesAllowList` cases | unit | `go test ./pkg/aws/... -run TestMatchesAllowList -v` | Already in `identity_test.go` |
| Safe phrase | SSM read/compare in `ParseSignedMessage` | unit | `go test ./pkg/aws/... -run TestSafePhrase -v` | Add to `mailbox_test.go` |
| Sidecar E2E | DNS proxy, HTTP proxy, audit, OTel | manual | n/a — live AWS required | Manual checklist |
| GitHub cloning | `git clone` works in sandbox | manual | n/a — live AWS required | Manual checklist |
| Inter-sandbox email | Send + receive + verify signature | manual | n/a — live SES required | Manual checklist |
| Email action approval | Request → reply → parse approval | manual | n/a — live SES required | Manual checklist |
| OTP sync | SSM param read once + delete | unit | `go test ./internal/app/cmd/... -run TestOTP -v` | Add to `create_test.go` |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/... ./pkg/aws/...`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/aws/cloudwatch_test.go` — add `TestExportSandboxLogs` (covers CW export feature)
- [ ] `cmd/configui/handlers_budget_test.go` — add `TestFormatMoney_FourDecimal` (covers precision)
- [ ] `pkg/aws/mailbox_test.go` — add `TestSafePhraseExtraction` (covers safe-phrase parsing)

---

## Sources

### Primary (HIGH confidence)
- Source code: `internal/app/cmd/status.go`, `budget.go`, `create.go` — all `%.2f` occurrences verified by grep
- Source code: `pkg/aws/cloudwatch.go` — `CWLogsAPI` interface, `DeleteSandboxLogGroup` — read directly
- Source code: `pkg/aws/mailbox.go`, `identity.go` — `ParseSignedMessage`, `MatchesAllowList` — read directly
- Source code: `pkg/aws/ses.go`, `pkg/aws/identity.go` — email send/sign patterns — read directly
- Source code: `sidecars/http-proxy/httpproxy/proxy.go` — Anthropic metering (Phase 20) confirmed complete
- `go test ./...` output — all packages green as of 2026-03-25
- `.planning/STATE.md` Accumulated Context section — all Phase decisions cross-referenced

### Secondary (MEDIUM confidence)
- AWS SDK v2 `cloudwatchlogs.CreateExportTask` — present in `aws-sdk-go-v2/service/cloudwatchlogs v1.64.1`
  per go.mod; IAM/bucket-policy requirements are standard CloudWatch Logs behavior
- AWS docs: CloudWatch Logs export tasks require `s3:PutObject` bucket policy for `logs.amazonaws.com`

### Tertiary (LOW confidence)
- Email approval via reply routing: exact SES receipt rule behavior for external-to-SES-domain replies
  not verified against live infrastructure

---

## Metadata

**Confidence breakdown:**
- Budget precision formatting: HIGH — exact files and line numbers identified from source
- CloudWatch log export: HIGH — pattern is clear, `CreateExportTask` is SDK standard
- Email mechanics (allow-list, safe phrase, action approval): HIGH for library patterns; MEDIUM for E2E live behavior
- Sidecar E2E: HIGH for what exists (all sidecars build and unit-test pass); MEDIUM for live verification
- OTP sync: MEDIUM — pattern is clear but exact spec (delete-after-read vs. TOTP) needs planner decision

**Research date:** 2026-03-25
**Valid until:** 2026-04-25 (stable domain; only risk is AWS SDK minor version changes)
