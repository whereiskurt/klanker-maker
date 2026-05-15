# Phase 75: Slack inbound file attachments — Research

**Researched:** 2026-05-15
**Domain:** Slack Events API file_share handling + S3 staging + sandbox userdata bash extension
**Confidence:** HIGH (everything verified against existing repo code; external Slack/AWS details verified via official docs and Slack-published JSON samples)

## Summary

Phase 75 is a small, additive extension of the Phase 67 / 67.1 / 67.2 inbound flow. CONTEXT.md locks 100% of the architecture; the planner's job is to translate locked decisions into ~5 parallel-friendly plans. Research confirms every locked decision is consistent with existing code patterns:

- The `S3FileDownloader` follows the **exact** narrow-interface adapter shape already used by `S3GetterAdapter` (Phase 68) and `SlackPosterAdapter` / `SlackReactorAdapter` / `SQSAdapter` (Phase 67-05). New `S3PutObjectAPI` interface alongside the existing `S3GetObjectAPI` in `aws_adapters.go`.
- The fire-and-forget goroutine mirrors the Phase 67.1 reactor goroutine at `events_handler.go:228-244` line-for-line.
- The bash extension follows the existing `userdata_slack_inbound_test.go` "substring + heredoc-bounded section" assertion style — no new test infrastructure needed.
- The S3 artifacts bucket has **no existing Terraform module** owning its lifecycle (the bucket is created imperatively in Go at `internal/app/cmd/bootstrap.go:870-895`). Phase 75 should add the lifecycle rule as a new tiny Terraform resource — recommendation below.
- The `km doctor` scope-check adds one element to one slice + one regression test. Trivial.

**Primary recommendation:** Plan as 5 plans across ~3 waves:
1. **Wave 0** (sequential, blocks all): events_types additions + isBotLoop allow-list — single-line schema-and-allow-list change unblocks every downstream test.
2. **Wave 1** (parallel): (a) `file_downloader.go` with `S3PutObjectAPI` + sanitization + caps + fire-and-forget handler fork; (b) sandbox userdata bash extension; (c) IAM + S3 lifecycle Terraform; (d) `files:read` scope check (init + doctor).
3. **Wave 2** (sequential after Wave 1): main.go wiring + docs + UAT gate.

## User Constraints (from CONTEXT.md)

### Locked Decisions

**Architecture:**
- Fire-and-forget download goroutine mirroring Phase 67.1 reactor goroutine
- Fork on `len(Files) > 0` in `EventsHandler.Handle`; files-empty path unchanged
- `isBotLoop` allow-list at `events_handler.go:265` becomes `["", "thread_broadcast", "file_share"]`
- Master-prompt wrapper is natural-language, prepended only when files present:
  ```
  The user attached the following file(s) to this Slack message.
  Read them with your Read tool when relevant to the question:
    - /workspace/.km-slack/attachments/<thread_ts>/<file_id>-<original_name> (<mimetype>)
    - ...

  User's message: <original text, or "[no text — file-only]" if empty>
  ```
- Cross-turn persistence: files in `/workspace/.km-slack/attachments/<thread_ts>/` for sandbox lifetime
- Bridge-only deploy via full `km init` (NOT `km init --lambdas` — that path doesn't deploy bridge zips)

**S3 layout:**
- Key format: `slack-inbound/<sandbox-id>/<thread_ts>/<file_id>-<sanitized_name>`
- `<file_id>` is Slack `F012345` (unique → no collisions even for same-name files)
- `<sanitized_name>` strips `/`, `\`, `..`, `\0`, non-printable bytes; truncates to 255 bytes
- 30-day expiration lifecycle on `slack-inbound/` prefix (matches `km-slack-threads` DDB TTL)
- Sandbox-side mirror name: `<file_id>-<sanitized_name>` (matches S3 leaf for traceability)

**Caps + failure handling:**
- 25 files per message (drop rest + thread-reply warning)
- 100 MB per file (drop oversize + thread-reply warning)
- Per-file download failures: drop, continue with rest, thread-reply
- All files fail: dispatch text-only + warning
- S3 PutObject failure: same as download failure
- 401 from `files.slack.com` (scope missing): same as download failure, log at Error
- Goroutine panic: `recover()`, log Error, thread-reply about operator notification
- Warnings ALWAYS posted to thread BEFORE the agent's reply (via existing `SlackPosterAdapter.PostMessage`)

**Bridge code changes:**
- `events_types.go`: `slackMessageEvent` gains `Files []SlackFile`; new `SlackFile` struct (`ID`, `Name`, `Mimetype`, `URLPrivateDownload`, `Size`); `InboundQueueBody` gains `Attachments []Attachment`; new `Attachment` struct (`S3Key`, `OriginalName`, `Mimetype`)
- `events_handler.go`: allow-list extension + `Handle` fork on `len(msg.Files) > 0`
- `file_downloader.go` (new): `FileDownloader` interface, `S3FileDownloader` adapter sharing `BotTokenFetcher` cache with `SlackPosterAdapter` / `SlackReactorAdapter`, filename sanitization helper
- `cmd/km-slack-bridge/main.go`: wire `S3FileDownloader` at cold start; `KM_ARTIFACTS_BUCKET` already read for Phase 68 — reuse

**Sandbox-side changes (`pkg/compiler/userdata.go` lines ~1259-1499):**
- Extract `attachments` array from SQS body via jq (`.attachments[]?` for safe degradation when older bridges omit the field)
- For each attachment: mkdir `/workspace/.km-slack/attachments/$THREAD_TS`, `aws s3 cp`, chown sandbox:sandbox
- Build wrapper text + prepend to `PROMPT_FILE` only when `len(attachments) > 0`

**IAM + Terraform:**
- `infra/modules/lambda-slack-bridge/v1.0.0/main.tf`: add `s3:PutObject` on `arn:aws:s3:::${bucket}/slack-inbound/*` to inline policy
- S3 lifecycle rule: `slack-inbound/` prefix, 30-day expiration (implementation home TBD — research determines)

**Slack scope addition:**
- New required scope: `files:read`
- `internal/app/cmd/slack.go:836` (`VerifyEventsAPIScopes`): `required` slice gains `"files:read"`
- `internal/app/cmd/doctor_slack.go:484` (`checkSlackAppEventsScopes`): `required` slice gains `"files:read"`
- Operator path: re-install app + `km slack rotate-token --bot-token <new>`

**Test moat (14 tests across bridge + compiler + cmd):**
- `pkg/slack/bridge/events_handler_test.go`: `TestEventsHandler_FileShareSubtype_Allowed`, `TestEventsHandler_WithFiles_FiresGoroutine_Returns200Fast`
- `pkg/slack/bridge/events_types_test.go` (new): `TestSlackMessageEvent_FilesField_ParsesCorrectly`
- `pkg/slack/bridge/file_downloader_test.go` (new): 8 tests (HappyPath, Over100MB_Dropped, Over25Files_Truncated, DownloadFails_Continues, AllFail_ReturnsEmpty, S3PutFails_TreatedAsDownloadFail, 403_LogsErrorAndDrops, FilenameSanitization)
- `pkg/compiler/userdata_slack_inbound_test.go`: `TestUserdata_SlackInbound_AttachmentMirrorBlock`, `TestUserdata_SlackInbound_MasterPromptWrapper`
- `internal/app/cmd/slack_test.go`: `TestSlackInit_FilesReadScope_Required`
- `internal/app/cmd/doctor_slack_test.go`: `TestDoctor_FilesReadScope_Missing_Reports`

**Doctor check (v1):** `slack_files_read_scope` — extends existing scope-check loop. Mirrors `slack_app_events_subscription` pattern.

**ROADMAP fix:** Line 1626 of `.planning/ROADMAP.md` currently reads `**Depends on:** Phase 74` — should read `**Depends on:** Phase 67, Phase 67.1`. Planner makes this fix; do NOT touch in research.

### Claude's Discretion (research findings drive recommendations)

- **Location of `S3FileDownloader`:** New file `pkg/slack/bridge/file_downloader.go` alongside `aws_adapters.go`. Add `S3PutObjectAPI` interface there or to `aws_adapters.go` next to existing `S3GetObjectAPI` — recommendation: new file, to keep the Phase 75 footprint isolated and reviewable. RESEARCH confirms this matches Phase 67/68 adapter style.
- **FilenameSanitization test:** Table-driven test with ~15 input-output pairs covering each forbidden character class + length truncation. Use `t.Run(name, ...)` subtests so failures pinpoint the failing case.
- **`aws s3 cp` vs `aws s3api get-object` in poller:** **`aws s3 cp`** — already used elsewhere in userdata, simpler error semantics. Confirmed via grep of `pkg/compiler/userdata.go` — `aws s3 cp` appears in artifact-upload and km-mail-poller paths; consistent.
- **S3 lifecycle module home:** RESEARCH finding (see § Standard Stack below) — **no existing Terraform module owns the artifacts bucket**. The bucket is created imperatively in Go at `internal/app/cmd/bootstrap.go:870-895`. Best home for the lifecycle rule: a new tiny module `infra/modules/s3-artifacts-lifecycle/v1.0.0/` with a single `aws_s3_bucket_lifecycle_configuration` resource, wired into `infra/live/use1/` like the other regional modules. Worst-case fallback: extend `bootstrap.go` to call `PutBucketLifecycleConfiguration` after `CreateBucket`. Recommendation: **Terraform module** — keeps lifecycle declarative, auditable, and easy to extend in a future phase (e.g., Phase 68's `transcripts/` prefix could move into the same module).
- **Warning thread-reply text wording:** Use the exact warnings from the spec's failure-handling matrix. They cover all documented cases concisely.

### Deferred Ideas (OUT OF SCOPE — do not research)

- Outbound files (Claude attaching files to Slack replies) — different flow (`files.uploadV2`)
- Long-lived attachment GC inside running sandboxes
- `file_revoked` / `file_deleted` Slack event handling
- Per-MIME special handling beyond Read-tool defaults
- Bridge-side virus scanning
- `slack_inbound_stale_attachments` doctor check
- `km destroy` cleanup of `slack-inbound/<sandbox-id>/` S3 prefix

## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| **REQ-FILES-ALLOWLIST** | Bridge admits `file_share` subtype: `isBotLoop` allow-list at `events_handler.go:271-278` becomes `["", "thread_broadcast", "file_share"]` | Verified single-line additive change. Existing `TestEventsHandler_BotSelfMessageFiltered` table (lines 233-286) explicitly tests `subtype_file_share` is DROPPED — that test case must be REMOVED and REPLACED by `TestEventsHandler_FileShareSubtype_Allowed`. Existing `TestEventsHandler_ThreadBroadcastPasses` (line 291) is the structural template for the new positive test. |
| **REQ-FILES-DOWNLOAD** | Bridge fires fire-and-forget goroutine on `len(msg.Files) > 0`: downloads each file via `files.slack.com` URL + bot token, stages to S3, then writes SQS | Pattern is line-identical to existing reactor goroutine at `events_handler.go:228-244`. Slack `url_private_download` requires `Authorization: Bearer <bot-token>` header (verified via Slack docs); response is binary body. Existing `BotTokenFetcher` cache shared with Poster/Reactor avoids extra SSM calls. |
| **REQ-FILES-CAPS** | 25 files/msg + 100 MB/file enforcement; oversize/overcount drops post thread-reply warning | Bridge `S3FileDownloader.Download` does cap check before any HTTP call (size from `slackMessageEvent.Files[].size` field). Warnings posted via existing `SlackPosterAdapter.PostMessage` — no new interface. |
| **REQ-FILES-S3-LAYOUT** | S3 key `slack-inbound/<sandbox-id>/<thread_ts>/<file_id>-<sanitized_name>` + filename sanitization (strip `/`, `\`, `..`, `\0`, non-printable; truncate to 255 bytes) | New pure helper `sanitizeFilename(string) string` — table-test it. `<file_id>` prevents collisions when two files share a name in one thread. |
| **REQ-FILES-IAM-LIFECYCLE** | Bridge IAM gains `s3:PutObject` on `arn:aws:s3:::${bucket}/slack-inbound/*`; S3 lifecycle deletes `slack-inbound/` after 30 days | Bridge IAM: add new `aws_iam_role_policy` resource in `infra/modules/lambda-slack-bridge/v1.0.0/main.tf` alongside `slack_bridge_transcript_s3_read` (already gated on `var.artifacts_bucket != ""`). Lifecycle: new module `infra/modules/s3-artifacts-lifecycle/v1.0.0/` (recommendation — see Standard Stack). |
| **REQ-FILES-SQS-PAYLOAD** | `InboundQueueBody.Attachments []Attachment` additive field; new `Attachment` struct (`S3Key`, `OriginalName`, `Mimetype`) | Additive to existing struct at `events_types.go:27-33`. Older sandbox pollers will silently ignore the new field — `jq -r '.attachments[]?'` (note `?`) tolerates absent field. |
| **REQ-FILES-POLLER** | Sandbox-side bash extension: extract attachments from SQS body, `aws s3 cp` each to `/workspace/.km-slack/attachments/<thread_ts>/`, chown sandbox:sandbox | Extension to existing inbound poller at `pkg/compiler/userdata.go:1259-1497`. Must occur AFTER `TEXT` extraction (line 1367) and BEFORE `PROMPT_FILE` build (line 1402-1405). |
| **REQ-FILES-WRAPPER-FORMAT** | Natural-language master-prompt wrapper prepended to `claude -p` input only when `len(attachments) > 0` | Heredoc block in poller bash; assertion via substring match on rendered userdata. Wrapper must NOT appear in disabled-inbound path or files-empty path. |
| **REQ-FILES-FAILURE-WARNINGS** | Thread-reply warnings for each failure class; warnings BEFORE agent reply | Bridge posts warning via `SlackPosterAdapter.PostMessage(channel, "", warningText, threadTS)` synchronously inside the download goroutine BEFORE SQS write. Tests assert call ordering. |
| **REQ-FILES-SCOPE** | `files:read` scope: added to `VerifyEventsAPIScopes` required slice AND `checkSlackAppEventsScopes` required slice + new `slack_files_read_scope` doctor check | One-line additive change in two places (`slack.go:837` and `doctor_slack.go:484`). RESEARCH below confirms scope-check loop is identical at both call sites — no branching by scope category. |
| **REQ-FILES-DEPLOY** | Full `make build && km init` operator path; existing sandboxes do NOT get retroactive support (require `km destroy && km create`) | Documented in CLAUDE.md and `docs/slack-notifications.md`. Bridge ships independently; sandbox-side bash change only takes effect on new sandboxes — same as Phases 67/68/79. |

## Standard Stack

### Core

| Library / file | Version | Purpose | Why standard |
|----------------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/s3` | repo-pinned (already in go.mod) | `PutObject` to staging bucket | Already used by `pkg/aws/mlflow.go:71` (`WriteMLflowRun`) and bridge `S3GetterAdapter` for Phase 68 transcript reads; narrow-interface pattern established |
| `github.com/aws/aws-sdk-go-v2/aws` | repo-pinned | `aws.String()` helper for SDK pointers | Used by every adapter in `aws_adapters.go` |
| `net/http` stdlib | n/a | File download from `files.slack.com` | `SlackReactorAdapter` and `SlackPosterAdapter` already use `*http.Client`; reuse the same client (already has `Timeout: 10s` from main.go:83) |
| `crypto/ed25519` | stdlib | n/a — not used by Phase 75 path | Just noting: Phase 75 calls Slack files endpoint with **bot token** (Bearer), NOT signed envelope. No new crypto. |
| `encoding/json` | stdlib | Marshal/unmarshal `InboundQueueBody` / `slackMessageEvent` | Already in events_types path |

### Supporting

| File | Purpose | When to use |
|------|---------|-------------|
| `pkg/slack/bridge/aws_adapters.go:1123-1153` | Reference adapter (`S3GetterAdapter`) | Mirror structure exactly for `S3FileDownloader` |
| `pkg/slack/bridge/aws_adapters.go:498-735` | Reference adapter (`SlackReactorAdapter`) | Reference for fire-and-forget HTTP pattern, retry/backoff (Phase 75 keeps single-attempt per file; retry deferred) |
| `pkg/slack/bridge/aws_adapters_test.go:974-1031` | `recordingTransport` + `captureBridgeLogger` + `canned()` fixtures | Use directly for `file_downloader_test.go` — no new HTTP test infrastructure needed |
| `pkg/aws/mlflow.go:64-82` | Reference for `s3.PutObjectInput` shape | Same pattern: `Body: strings.NewReader(...)` or `bytes.NewReader(...)` for `io.Reader`; set `ContentType` |
| `pkg/compiler/userdata_slack_inbound_test.go:174-184` | `extractSlackInboundPoller` helper | Use directly for bounded substring assertions on the new attachment block |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| New `infra/modules/s3-artifacts-lifecycle/v1.0.0/` | Inline `PutBucketLifecycleConfiguration` call in `bootstrap.go` after `CreateBucket` | Terraform is declarative + auditable + idempotent; Go-side imperative call would diverge from the rest of regional infra. Recommend Terraform. |
| New `infra/modules/s3-artifacts-lifecycle/v1.0.0/` | Add lifecycle rule inside existing `infra/modules/s3-replication/v1.0.0/main.tf` | s3-replication already targets the same bucket but is scoped to replication concerns; mixing lifecycle in would cross-contaminate. Recommend new module. |
| `aws s3 cp` in poller | `aws s3api get-object` | `aws s3 cp` is already used by km-mail-poller (line ~1144 of userdata.go) and km-agent-results paths; uniform error semantics (`|| true` fallthrough). |
| Bounded retry inside `S3FileDownloader.Download` | Single-attempt, log on failure | CONTEXT.md locks single-attempt + thread-reply warning. Phase 67.2 retry pattern is out of scope. |
| Streaming download → S3 (no buffering) | Buffer in Lambda memory before PutObject | 100MB max × 25 files = 2.5GB theoretical worst case; Lambda has 10GB max memory. Streaming via `resp.Body` (an `io.ReadCloser`) is preferred — `s3.PutObjectInput.Body` accepts `io.Reader`. SDK will buffer/chunk as needed. |

**Installation:**

No new Go dependencies. All packages already in `go.mod` (`aws-sdk-go-v2/service/s3` is used by Phase 68's `S3GetterAdapter` and `pkg/aws/mlflow.go`).

```bash
# Verification only — should report no new go.mod / go.sum churn at end of Phase 75
go mod tidy && git diff go.mod go.sum
```

## Architecture Patterns

### Recommended file layout

```
pkg/slack/bridge/
├── events_types.go              # MODIFIED: add Files, SlackFile, Attachment
├── events_handler.go            # MODIFIED: allow-list + Handle fork
├── events_handler_test.go       # MODIFIED: remove dropped file_share case + add passing case + add goroutine timing test
├── events_types_test.go         # NEW: JSON unmarshal test
├── file_downloader.go           # NEW: FileDownloader interface + S3FileDownloader adapter + sanitizeFilename helper
├── file_downloader_test.go      # NEW: 8 unit tests covering all paths
├── aws_adapters.go              # MODIFIED: add S3PutObjectAPI interface (or co-locate in file_downloader.go)
└── interfaces.go                # UNCHANGED

cmd/km-slack-bridge/
└── main.go                      # MODIFIED: wire S3FileDownloader at cold start (single block, ~15 LoC)

pkg/compiler/
├── userdata.go                  # MODIFIED: extend inbound poller heredoc with attachment-mirror block + wrapper
└── userdata_slack_inbound_test.go  # MODIFIED: 2 new tests

internal/app/cmd/
├── slack.go                     # MODIFIED: line 837 — add "files:read" to required slice
├── slack_test.go                # MODIFIED: 1 new test
├── doctor_slack.go              # MODIFIED: line 484 — add "files:read" to required slice
└── doctor_slack_inbound_test.go # MODIFIED: 1 new test

infra/modules/lambda-slack-bridge/v1.0.0/
└── main.tf                      # MODIFIED: add aws_iam_role_policy.slack_bridge_files_s3_write (gated on var.artifacts_bucket)

infra/modules/s3-artifacts-lifecycle/v1.0.0/      # NEW module (recommended)
├── main.tf                                       # aws_s3_bucket_lifecycle_configuration
└── variables.tf                                  # bucket_name input

infra/live/use1/s3-artifacts-lifecycle/           # NEW regional consumer
└── terragrunt.hcl
```

### Pattern 1: Fire-and-forget goroutine with files-fork

**What:** New branch in `EventsHandler.Handle` that swaps the synchronous SQS write for a goroutine doing (download → S3 → SQS). The 200 response still ships within Slack's 3s ack window.

**When to use:** Files present (`len(msg.Files) > 0`).

**Reference pattern (already in repo):** Phase 67.1 reactor goroutine at `events_handler.go:228-244`.

```go
// Source: pkg/slack/bridge/events_handler.go:228-244 (reactor pattern)
// New Phase 75 fork — pseudo-code, NOT for direct paste:
if len(msg.Files) > 0 {
    files := msg.Files
    ch, threadTS, eventID, sandboxID, queueURL := msg.Channel, threadTS, env.EventID, info.SandboxID, info.QueueURL
    // existing SQS write is SKIPPED on this branch.
    go func() {
        bgCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
        defer cancel()
        defer func() {
            if r := recover(); r != nil {
                h.log().Error("events: file downloader panic", "err", r)
                // best-effort: post thread-reply about operator notification via h.Slack (would need adding)
            }
        }()
        atts, fileErrs, _ := h.FileDownloader.Download(bgCtx, files, sandboxID, threadTS)
        for _, fe := range fileErrs {
            // post thread-reply warning via h.Slack.PostMessage(...)
        }
        // build SQS body with Attachments[]
        // SQS.Send(...)
    }()
    // reactor goroutine still fires below (unchanged)
    // return 200 immediately
    return EventsResponse{StatusCode: 200, Body: "ok"}
}
// existing files-empty path continues unchanged below
```

**Critical detail (must implement):** The fork happens AFTER steps 1-7 (sig verify, dedup, channel resolve, threads upsert) but REPLACES step 8 (SQS write). Reactor goroutine (step 10) still fires identically.

**Critical detail (interface addition):** `EventsHandler` struct gains a new field `FileDownloader FileDownloader` (nullable; nil means feature off — back-compat for older Lambda images that pre-date Phase 75). When nil, files-present branch should fall through to text-only dispatch (or fail closed — choose during planning).

### Pattern 2: Narrow-interface S3 PutObject adapter

**What:** New `S3PutObjectAPI` interface alongside the existing `S3GetObjectAPI` (`aws_adapters.go:1123-1126`). `S3FileDownloader` consumes the narrow interface; tests inject a mock.

**Why:** Phase 68 already established this exact pattern. Mirror it.

```go
// Co-locate next to S3GetObjectAPI (aws_adapters.go:1123-1126)
// or in new file_downloader.go — recommendation: new file.

// S3PutObjectAPI is the narrow S3 interface used by S3FileDownloader.
// Both *s3.Client and mock implementations satisfy it.
type S3PutObjectAPI interface {
    PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}
```

### Pattern 3: Bash heredoc extension with bounded test assertions

**What:** Extend the inbound-poller bash heredoc in `pkg/compiler/userdata.go` (between `<< 'SLACKINBOUND'` at line 1259 and `SLACKINBOUND` at line 1498). Tests use `extractSlackInboundPoller` (already in `userdata_slack_inbound_test.go:174-184`) to bound assertions to the heredoc body, then substring-match.

**Recommended test shape:**

```go
// Source: existing pattern at userdata_slack_inbound_test.go:189-203

func TestUserdata_SlackInbound_AttachmentMirrorBlock(t *testing.T) {
    p := minimalSlackInboundProfile(t, true)
    out := compileInboundUserData(t, p)
    poller := extractSlackInboundPoller(t, out)

    // The new attachment-mirror block must:
    //   (a) extract attachments[] from BODY via jq
    //   (b) mkdir per-thread directory
    //   (c) aws s3 cp each attachment
    //   (d) chown sandbox:sandbox the materialized file
    for _, needle := range []string{
        `jq -c '.attachments[]?'`,
        `/workspace/.km-slack/attachments/`,
        `mkdir -p`,
        `aws s3 cp "s3://$KM_ARTIFACTS_BUCKET/`,
        `chown sandbox:sandbox`,
    } {
        if !strings.Contains(poller, needle) {
            t.Fatalf("attachment-mirror block missing %q\n%s", needle, abbreviateUD(poller))
        }
    }

    // Block must occur BEFORE the claude -p invocation so files exist at prompt time.
    claudeIdx := strings.Index(poller, "claude -p")
    mirrorIdx := strings.Index(poller, `/workspace/.km-slack/attachments/`)
    if mirrorIdx < 0 || claudeIdx < 0 || mirrorIdx >= claudeIdx {
        t.Fatalf("attachment-mirror block must precede claude -p invocation")
    }
}

func TestUserdata_SlackInbound_MasterPromptWrapper(t *testing.T) {
    p := minimalSlackInboundProfile(t, true)
    out := compileInboundUserData(t, p)
    poller := extractSlackInboundPoller(t, out)

    // Wrapper must include the exact phrasing locked in CONTEXT.md
    for _, needle := range []string{
        `The user attached the following file(s)`,
        `Read them with your Read tool when relevant`,
        `User's message:`,
        `[no text — file-only]`,
    } {
        if !strings.Contains(poller, needle) {
            t.Fatalf("master-prompt wrapper missing %q\n%s", needle, abbreviateUD(poller))
        }
    }

    // Wrapper MUST be gated on len(attachments) > 0 — otherwise text-only
    // turns get spurious file-context prefix.
    // (Bash check shape: `if [ "$ATTACH_COUNT" -gt 0 ]; then prepend; fi`)
    if !strings.Contains(poller, "ATTACH_COUNT") && !strings.Contains(poller, `-gt 0`) {
        t.Fatalf("wrapper must be gated on attachment count > 0")
    }
}
```

### Anti-Patterns to Avoid

- **DO NOT** add a sync SQS write in the files-present branch. CONTEXT.md is explicit: the goroutine does (download → S3 → SQS) **end-to-end**; the bridge does not double-write.
- **DO NOT** reuse the request `ctx` inside the download goroutine. Lambda may cancel `ctx` after the 200 response. Use `context.WithTimeout(context.Background(), 90*time.Second)` — matches the Phase 67.1 reactor pattern with a longer budget appropriate for ≤25 files × 100MB.
- **DO NOT** call `slackPoster.PostMessage(ctx, ...)` from inside the goroutine using request `ctx`. Use the bg ctx.
- **DO NOT** extend `Reactor` interface or `SlackPosterAdapter` — locked. Use a NEW interface for the downloader.
- **DO NOT** depend on the order of `Files[]` for thread-reply ordering. Slack may deliver files in any order. Order each thread-reply by `OriginalName` (deterministic) before posting.
- **DO NOT** retain files indefinitely. 30-day S3 lifecycle is the contract. The sandbox-side `/workspace/.km-slack/attachments/` is cleaned by `km destroy` (EBS volume goes with the instance).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Generic byte-streaming download | Custom retry/backoff/chunking | `http.Client.Do` + pass `resp.Body` directly to `s3.PutObjectInput.Body` | SDK handles signed-payload chunking; CONTEXT.md locks single-attempt-then-warn |
| Slack file URL signing | Pre-signed URL request to Slack | Standard `Authorization: Bearer <bot-token>` header on `url_private_download` | Slack docs confirm this is the supported pattern; no separate sign call exists |
| Bash JSON parsing | sed/awk/grep on SQS body | `jq` (already used throughout the poller) | jq is installed by userdata.go's package step; safe degradation via `.attachments[]?` operator (`?` returns empty on missing key, doesn't error) |
| Filename sanitization | Regex + manual loop | Pure helper with explicit byte-class strip + length truncate | Easy to write, easy to table-test, no external lib needed; security-sensitive — explicit byte handling > regex |
| Token cache | New SSM cache for downloader | Inject the existing `BotTokenFetcher` (which `SSMBotTokenFetcher` implements with 15-min cache) into `S3FileDownloader` | Cache is per-Lambda-cold-start; sharing avoids extra SSM round-trip per download |
| Logging | New logger instance | `bridge.SetLogger` / `bridge.logger` package-level (already used by `SlackReactorAdapter`) | Phase 67.2 established this pattern; tests use `captureBridgeLogger` |

**Key insight:** Phase 75 is **almost entirely a wiring exercise** of existing repo patterns. The S3 PutObject pattern, fire-and-forget goroutine pattern, bot-token cache sharing, narrow-interface adapter, bash heredoc, and scope-check loop ALL exist as line-for-line precedents. The only genuinely new code is (1) the filename sanitization helper and (2) the cap-and-collect orchestration inside `S3FileDownloader.Download`.

## Common Pitfalls

### Pitfall 1: `Authorization` header lost on Slack file CDN redirect

**What goes wrong:** Slack's `files.slack.com` URL may 302 to a private CDN domain. Go's `http.Client` by default **strips** the `Authorization` header on cross-domain redirects (per [the Go stdlib spec](https://pkg.go.dev/net/http#Client) — `Client.Do` strips sensitive headers if the redirect target is a different host). The download succeeds at TLS but returns HTML or 403 instead of the file body.

**Why it happens:** Go's default `redirectPolicy` calls `shouldCopyHeaderOnRedirect`, which excludes `Authorization` when the host changes (e.g., `files.slack.com` → `files-edge.slack-edge.com`).

**How to avoid:** Two-options for `S3FileDownloader.Download`:

1. **Option A (preferred):** Set `Client.CheckRedirect` to a custom function that re-attaches the `Authorization` header (and verify the redirect host is in a Slack-owned set like `*.slack.com` / `*.slack-edge.com` to avoid leaking the token).
2. **Option B (simpler, less risky):** Set `Client.CheckRedirect = func(*Request, []*Request) error { return http.ErrUseLastResponse }` — disable auto-redirect entirely. Inspect `resp.StatusCode == 302` and `resp.Header.Get("Location")` manually, then re-issue the GET to the redirected URL with the Bearer header. Most file downloads from Slack actually return the body directly on the first request (CDN happens server-side); 302 is rare. If it happens, fail loudly the first time and add explicit handling.

**Warning signs:** "Downloaded" file is suspiciously small (HTML "Sign in to Slack" page is ~5KB); MIME-type sniff returns `text/html`; `Content-Type` header is `text/html` instead of `image/png`.

**Test plan:** Add `TestFileDownloader_FilesSlackComRedirect_PreservesAuthHeader` — a `recordingTransport` test that asserts the second GET (after 302) still has the `Authorization: Bearer ...` header.

**Source:** This is the **exact** bug reported in [slackapi/deno-slack-sdk #285](https://github.com/slackapi/deno-slack-sdk/issues/285) — bot tokens, `Authorization: Bearer xoxb-...` header, downloaded "file" turns out to be HTML. Decision: don't hand-roll redirect logic; use Option B (`ErrUseLastResponse`) and explicit re-issue. This is enough to handle the documented production case without inventing new infrastructure.

**Confidence:** HIGH — confirmed via Slack SDK issue tracker AND Go stdlib docs.

### Pitfall 2: `s3.PutObjectInput.Body` and stream re-windability

**What goes wrong:** AWS SDK for Go v2's S3 client may need to retry uploads; the `Body` field is typed `io.Reader` but the SDK prefers `io.ReadSeeker` so it can rewind on retry. Passing `resp.Body` (a non-seekable `io.ReadCloser`) directly works on success but FAILS on transient retry with "operation error S3: PutObject, https response error StatusCode: 0, RequestID: , HostID: , request body offset reset failed".

**Why it happens:** AWS SDK v2 issue [#1123](https://github.com/aws/aws-sdk-go-v2/issues/1123) — Body must be re-readable for v4 signing retries.

**How to avoid:** Two options:

1. **Buffer to memory:** `body, _ := io.ReadAll(resp.Body); s3PutInput.Body = bytes.NewReader(body)`. Simple. 100MB worst-case fits in Lambda's 256MB or 512MB allocation (Phase 63's bridge is at `memory_size = 256` in main.tf:273 — may need bumping for Phase 75 if 100MB × concurrency > 256MB). Decision flag for planning.
2. **Use the s3 Uploader Manager:** `manager.NewUploader(s3Client).Upload(...)` accepts `io.Reader`; handles chunking + retry internally. Heavier dependency surface.

**Recommendation for Phase 75:** **Option 1 (bytes.NewReader)** + bump Lambda `memory_size` to 1024 (one file in flight + headroom). Single-attempt download → buffer → PutObject. Matches existing `pkg/aws/mlflow.go:74` pattern (`strings.NewReader`). Concurrent downloads inside the goroutine should be **sequential** (loop, not goroutine-per-file) to keep peak memory bounded.

**Warning signs:** PutObject errors in CloudWatch with "request body offset reset failed"; tests pass with mocks but fail in real Lambda.

**Confidence:** HIGH — verified via aws-sdk-go-v2 source code (`PutObjectInput.Body` is `io.Reader`; signing middleware seeks-on-retry).

### Pitfall 3: Slack `file_share` event delivery WITHOUT a top-level `bot_id`

**What goes wrong:** When a Slack workflow or another bot uploads a file (e.g., the `km-slack-bridge` itself in some future outbound flow), the resulting `file_share` event may have `bot_id` empty at the **top level** but `bot_id` set inside the file object (`files[0].bot_id`). The existing bot-loop filter at `isBotLoop` line 267 only checks `m.BotID` (top-level) — would let bot-uploaded files into the SQS queue and the agent would respond to itself.

**Why it happens:** Slack treats bot uploads as a "share" by the bot user. The Events API delivery shape varies by app-installation surface (legacy bot vs Slack App).

**How to avoid:** In Phase 75, the bridge does NOT upload files (outbound files are out of scope per CONTEXT.md). So the practical risk is low. BUT the `isBotLoop` check should be belt-and-suspenders extended in Phase 75 to also check `len(m.Files) > 0 && m.Files[0].user == botUID` (or `m.Files[0].bot_id != ""`). If a `SlackFile` struct doesn't currently include a `User` or `BotID` field, **do NOT add them** as part of Phase 75 — instead, document this as a known-limitation in the deferred-items doc. The current allow-list `["", "thread_broadcast", "file_share"]` + the top-level `BotID` check is sufficient for the locked scope.

**Warning signs:** Agent replies to itself via Slack file_share; nonce dedup doesn't catch it because `event_id` is unique per bot-action.

**How research handles it:** Documented here as a follow-up watch item; do NOT block Phase 75 on this. Out of scope per CONTEXT.md `<deferred>` ("Outbound files (Claude attaching files to Slack replies)").

**Confidence:** MEDIUM — Slack docs are silent on the exact `bot_id` placement for file_share, so verification would require provoking a real bot upload in workspace. Not blocking Phase 75; flag in `deferred-items.md`.

### Pitfall 4: Empty `text` field on file-only uploads

**What goes wrong:** The Phase 67 poller bash at `userdata.go:1369-1376` has a guard: `if [ -z "$CHANNEL" ] || [ -z "$THREAD_TS" ] || [ -z "$TEXT" ]; then ... ack to avoid retry`. **This will silently drop file-only uploads** because Slack delivers `text: ""` (or omits the field entirely) when the user drags a file with no comment.

**Why it happens:** Phase 67 was text-only. Now files-only is a valid path: "drag a screenshot, no caption."

**How to avoid:** Modify the malformed-message guard in `userdata.go` to allow empty text when attachments[] is non-empty. Pseudo-code:

```bash
ATTACH_COUNT=$(echo "$BODY" | jq -r '.attachments // [] | length')
if [ -z "$CHANNEL" ] || [ -z "$THREAD_TS" ] || ([ -z "$TEXT" ] && [ "$ATTACH_COUNT" -eq 0 ]); then
  # malformed — ack to avoid retry
fi
```

And when building the wrapper, substitute `[no text — file-only]` for `$TEXT` when `TEXT` is empty.

**Warning signs:** File-only Slack messages produce 👀 reaction but no Claude reply; SQS DLQ shows acked-but-not-dispatched.

**Test plan:** Add to `TestUserdata_SlackInbound_AttachmentMirrorBlock` a substring assertion on the new ATTACH_COUNT guard.

**Confidence:** HIGH — verified by reading `userdata.go:1369-1376`.

### Pitfall 5: `Authorization` header echo into S3 metadata or CloudWatch logs

**What goes wrong:** It's tempting to log the full HTTP request on failure (e.g., `log.Error("download failed", "request", req)`). If the request struct prints the `Authorization` header, the bot token leaks into CloudWatch.

**How to avoid:** Never log the full `*http.Request`. Log only `url`, `status`, `err`. Build a small helper if needed.

**Warning signs:** CloudWatch logs contain `xoxb-...` substrings.

**Confidence:** HIGH — established hygiene for the Phase 63 bridge.

## Code Examples

### Slack file download with bot token (verified pattern)

```go
// File: pkg/slack/bridge/file_downloader.go (NEW)
// Source: pattern derived from SlackPosterAdapter.call (aws_adapters.go:290-331)

// downloadOneFile fetches a single Slack file using the bot token.
// Returns (body, contentLength, error). Caller MUST close body on success.
//
// Implementation notes:
//   - Disable redirect auto-follow (CheckRedirect = ErrUseLastResponse) to
//     prevent Go's default policy from stripping Authorization on cross-host
//     302s (Pitfall 1). The single-step manual redirect adds ~10 LoC but
//     avoids the "downloaded HTML" silent failure mode.
//   - 401/403 means files:read scope missing; log Error and return.
//   - Non-2xx is a download failure; caller bumps the per-file error counter.
func (d *S3FileDownloader) downloadOneFile(ctx context.Context, file SlackFile) (io.ReadCloser, int64, error) {
    token, err := d.Tokens.Fetch(ctx)
    if err != nil {
        return nil, 0, fmt.Errorf("downloader: fetch bot token: %w", err)
    }

    issueGET := func(url string) (*http.Response, error) {
        req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
        if err != nil {
            return nil, err
        }
        req.Header.Set("Authorization", "Bearer "+token)
        return d.HTTPClient.Do(req)
    }

    resp, err := issueGET(file.URLPrivateDownload)
    if err != nil {
        return nil, 0, fmt.Errorf("downloader: GET %s: %w", file.URLPrivateDownload, err)
    }

    // Manual 302 follow with Authorization re-attached (Pitfall 1).
    if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
        loc := resp.Header.Get("Location")
        resp.Body.Close()
        if loc == "" {
            return nil, 0, fmt.Errorf("downloader: redirect with empty Location")
        }
        resp, err = issueGET(loc)
        if err != nil {
            return nil, 0, fmt.Errorf("downloader: GET %s (redirect target): %w", loc, err)
        }
    }

    if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
        resp.Body.Close()
        return nil, 0, fmt.Errorf("downloader: %d from %s — bot may lack files:read scope", resp.StatusCode, file.URLPrivateDownload)
    }
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        resp.Body.Close()
        return nil, 0, fmt.Errorf("downloader: status %d from %s", resp.StatusCode, file.URLPrivateDownload)
    }
    return resp.Body, resp.ContentLength, nil
}
```

### S3 PutObject (verified pattern from existing repo code)

```go
// Source: pkg/aws/mlflow.go:71-79 (WriteMLflowRun)
// Adapted for binary body + Content-Type from Slack file mimetype.

bodyBytes, _ := io.ReadAll(fileBody) // buffer in memory (Pitfall 2)
fileBody.Close()
_, err = d.S3.PutObject(ctx, &s3.PutObjectInput{
    Bucket:      aws.String(d.Bucket),
    Key:         aws.String(s3Key),
    Body:        bytes.NewReader(bodyBytes),
    ContentType: aws.String(file.Mimetype),
})
if err != nil {
    return fmt.Errorf("downloader: s3 put s3://%s/%s: %w", d.Bucket, s3Key, err)
}
```

### Allow-list extension (one-line change)

```go
// File: pkg/slack/bridge/events_handler.go
// Source: existing code at line 271-278

// BEFORE:
switch m.Subtype {
case "", "thread_broadcast":
    // fall through
default:
    return true
}

// AFTER (Phase 75):
switch m.Subtype {
case "", "thread_broadcast", "file_share":
    // fall through
default:
    return true
}
```

### Scope-check loop extension (one-line additive)

```go
// File: internal/app/cmd/slack.go:837 (VerifyEventsAPIScopes)
// BEFORE: required := []string{"channels:history", "groups:history", "reactions:write"}
// AFTER:  required := []string{"channels:history", "groups:history", "reactions:write", "files:read"}

// File: internal/app/cmd/doctor_slack.go:484 (checkSlackAppEventsScopes)
// BEFORE: required := []string{"channels:history", "groups:history", "reactions:write"}
// AFTER:  required := []string{"channels:history", "groups:history", "reactions:write", "files:read"}

// The success message at doctor_slack.go:507 must also update:
// BEFORE: "Slack App has all required inbound scopes (channels:history, groups:history, reactions:write)"
// AFTER:  "Slack App has all required inbound scopes (channels:history, groups:history, reactions:write, files:read)"
```

This is a **one-line add in three places** + the existing `TestDoctor_SlackInboundEventsSubscription_*` table-test extends naturally. The scope-check loop is **identical** at both call sites — no branching by scope category — confirmed by reading both functions in full.

### Bridge IAM addition

```hcl
# File: infra/modules/lambda-slack-bridge/v1.0.0/main.tf
# Add after the existing slack_bridge_transcript_s3_read resource (line 217-236).
# Same gating: only emit when var.artifacts_bucket is set.

resource "aws_iam_role_policy" "slack_bridge_files_s3_write" {
  count = var.artifacts_bucket != "" ? 1 : 0
  name  = "${local.function_name}-files-s3-write"
  role  = aws_iam_role.slack_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "S3PutSlackInboundFiles"
        Effect = "Allow"
        Action = [
          "s3:PutObject",
        ]
        Resource = "arn:aws:s3:::${var.artifacts_bucket}/slack-inbound/*"
      }
    ]
  })
}
```

### S3 lifecycle (NEW module — recommended home)

```hcl
# File: infra/modules/s3-artifacts-lifecycle/v1.0.0/main.tf (NEW)

resource "aws_s3_bucket_lifecycle_configuration" "artifacts" {
  bucket = var.bucket_name

  # Phase 75: 30-day expiration on slack-inbound/ prefix.
  # Matches km-slack-threads DDB TTL (30 days).
  rule {
    id     = "slack-inbound-30day"
    status = "Enabled"

    filter {
      prefix = "slack-inbound/"
    }

    expiration {
      days = 30
    }
  }

  # Future-proof: when Phase 68's transcripts/ prefix needs lifecycle,
  # add a second rule here rather than spawning another module.
}
```

```hcl
# File: infra/modules/s3-artifacts-lifecycle/v1.0.0/variables.tf (NEW)

variable "bucket_name" {
  type        = string
  description = "Name of the artifacts bucket (e.g., km-artifacts-<account>)."
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Phase 67 silent-drop of `file_share` | Phase 75 admits + handles file_share | This phase | Users can paste files into per-sandbox channels |
| `aws-sdk-go-v1` patterns | `aws-sdk-go-v2` everywhere | Pre-Phase-1 | Phase 75 uses v2 (already in repo) |
| Synchronous S3 + SQS write in bridge handler | Phase 75: fire-and-forget goroutine for files | This phase | 200 ack still under Slack's 3s window even for 100MB files |

**Deprecated / outdated:**
- The Slack `files.upload` (v1) method is deprecated in favor of `files.uploadV2` for outbound (out of scope for Phase 75 — confirmed by Slack docs as of 2026 still flagged "do NOT use files.upload — use files.uploadV2"). For Phase 75's inbound-download path, the file URL is just an HTTPS GET; no deprecated method involved.
- Direct `aws s3 cp` in bash continues to be the supported pattern (no AWS deprecation announced for AWS CLI v2's `s3 cp`).

## Open Questions

1. **Lambda `memory_size` bump?**
   - What we know: Phase 63 bridge has `memory_size = 256` (main.tf:273). Phase 75 buffers up to 100MB per file in memory (Pitfall 2 mitigation). Sequential per-file downloads cap peak to ~100MB + overhead.
   - What's unclear: Whether 256MB Lambda will OOM on a 100MB file. Conservatively, **bump to 1024MB** during Phase 75.
   - Recommendation: Plan adds `memory_size = 1024` in the same `main.tf` change as the IAM policy. Cheap (Lambda pricing is GB-second; doubling memory at idle is free).

2. **Goroutine timeout budget?**
   - What we know: Reactor goroutine uses `10s` (events_handler.go:239). Phase 75 needs longer — up to 25 files × ~100MB.
   - What's unclear: Worst-case wall clock. Slack file CDN benchmarks ~10-50 Mbps; 100MB at 10 Mbps is 80s. 25 × 100MB sequentially could be 25 × 80s = 2000s.
   - Recommendation: Use `90s` per goroutine and accept that pathological inputs will produce partial-success thread-replies. CONTEXT.md's "single file fails, dispatch with the rest" semantic handles this cleanly. Don't pre-optimize.

3. **What happens if `S3FileDownloader` is nil at runtime (back-compat)?**
   - What we know: `cmd/km-slack-bridge/main.go:124-133` already handles nil `FileUploader` gracefully when bot-token fetch fails. Phase 75 should do the same for `FileDownloader`.
   - What's unclear: Should nil-downloader cause file_share events to (a) fail-closed (text-only dispatch with warning), or (b) fail-open (full drop)?
   - Recommendation: **Fail-closed** — text-only dispatch + log Warn. Matches the Phase 67 "old bridge ignores Files[]" path. Decision worth flagging in PLAN.

4. **Does `KM_ARTIFACTS_BUCKET` already pass to bridge Lambda env?**
   - What we know: Verified at `main.tf:292` (`KM_ARTIFACTS_BUCKET = var.artifacts_bucket`) — yes, already wired by Phase 68. **Phase 75 is a no-op on this front.**
   - Resolved.

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` (stdlib) + `github.com/stretchr/testify` (already in go.mod) |
| Config file | `go.mod` only — no special config |
| Quick run command | `go test -run <TestName> ./pkg/slack/bridge/ ./pkg/compiler/ ./internal/app/cmd/` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|--------------|
| **REQ-FILES-ALLOWLIST** | `file_share` subtype admitted; existing `subtype_file_share` drop-case removed | unit | `go test -run 'TestEventsHandler_FileShareSubtype_Allowed' ./pkg/slack/bridge/` | ❌ test file exists (`events_handler_test.go`) — need to (1) remove `subtype_file_share` row at line 257, (2) add new positive test |
| **REQ-FILES-DOWNLOAD** | `S3FileDownloader.Download` happy path: 1 file → S3 put → Attachment returned | unit | `go test -run 'TestFileDownloader_HappyPath' ./pkg/slack/bridge/` | ❌ Wave 0 (`file_downloader_test.go` new) |
| **REQ-FILES-DOWNLOAD** | Bridge handler fires goroutine on `len(Files) > 0`; returns 200 within ~100ms even with slow downloader | unit | `go test -run 'TestEventsHandler_WithFiles_FiresGoroutine_Returns200Fast' ./pkg/slack/bridge/` | ❌ Wave 0 (new test in existing file) |
| **REQ-FILES-DOWNLOAD** | Download survives 302 redirect with Authorization preserved | unit | `go test -run 'TestFileDownloader_FilesSlackComRedirect_PreservesAuthHeader' ./pkg/slack/bridge/` | ❌ Wave 0 (Pitfall 1 regression test) |
| **REQ-FILES-CAPS** | File >100MB dropped + warning posted | unit | `go test -run 'TestFileDownloader_Over100MB_Dropped' ./pkg/slack/bridge/` | ❌ Wave 0 |
| **REQ-FILES-CAPS** | First 25 files kept, rest dropped + warning | unit | `go test -run 'TestFileDownloader_Over25Files_Truncated' ./pkg/slack/bridge/` | ❌ Wave 0 |
| **REQ-FILES-S3-LAYOUT** | Filename sanitization — `/`, `\`, `..`, `\0`, non-printable bytes stripped; 255-byte truncation | unit (table-driven) | `go test -run 'TestFileDownloader_FilenameSanitization' ./pkg/slack/bridge/` | ❌ Wave 0 |
| **REQ-FILES-S3-LAYOUT** | S3 key format `slack-inbound/<sandbox-id>/<thread_ts>/<file_id>-<sanitized_name>` | unit | Asserted inside `TestFileDownloader_HappyPath` via mock `S3PutObjectAPI` capture | ❌ Wave 0 |
| **REQ-FILES-IAM-LIFECYCLE** | Bridge IAM policy includes `s3:PutObject` on `slack-inbound/*` | terraform plan diff | Manual: `cd infra/live/use1/lambda-slack-bridge && terragrunt plan` → expect to see new policy attached | ⚠️ UAT-only — no automated test for Terraform plan diff in this repo |
| **REQ-FILES-IAM-LIFECYCLE** | S3 lifecycle rule on `slack-inbound/` prefix, 30-day expiration | terraform plan diff | Manual: `cd infra/live/use1/s3-artifacts-lifecycle && terragrunt plan` → expect lifecycle config | ⚠️ UAT-only |
| **REQ-FILES-SQS-PAYLOAD** | `InboundQueueBody.Attachments []Attachment` unmarshal correctly; older bridge with empty field stays back-compat | unit | `go test -run 'TestSlackMessageEvent_FilesField_ParsesCorrectly' ./pkg/slack/bridge/` AND existing `TestEventsHandler_ValidMessage_HappyPath` continues to pass (back-compat) | ❌ Wave 0 (`events_types_test.go` new) |
| **REQ-FILES-POLLER** | Sandbox poller bash contains `aws s3 cp` of attachments + chown + mkdir | unit (template render) | `go test -run 'TestUserdata_SlackInbound_AttachmentMirrorBlock' ./pkg/compiler/` | ❌ Wave 0 (new test in existing file) |
| **REQ-FILES-WRAPPER-FORMAT** | Master-prompt wrapper has exact phrasing + gated on attach count | unit (template render) | `go test -run 'TestUserdata_SlackInbound_MasterPromptWrapper' ./pkg/compiler/` | ❌ Wave 0 |
| **REQ-FILES-POLLER** | Empty-text + non-empty-attachments path is NOT dropped by the malformed-message guard | unit (template render) | Extend `TestUserdata_SlackInbound_AttachmentMirrorBlock` or new `TestUserdata_SlackInbound_AllowsEmptyTextWhenAttachments` | ❌ Wave 0 (Pitfall 4 regression test) |
| **REQ-FILES-FAILURE-WARNINGS** | Each failure class produces a `SlackPoster.PostMessage` call with the documented warning text | unit | `go test -run 'TestFileDownloader_DownloadFails_Continues' ./pkg/slack/bridge/` (etc.) | ❌ Wave 0 — 4 tests (DownloadFails_Continues, AllFail_ReturnsEmpty, S3PutFails, 403_LogsErrorAndDrops) |
| **REQ-FILES-SCOPE** | `VerifyEventsAPIScopes` requires `files:read` | unit | `go test -run 'TestSlackInit_FilesReadScope_Required' ./internal/app/cmd/` | ❌ Wave 0 (new test in existing file) |
| **REQ-FILES-SCOPE** | `checkSlackAppEventsScopes` flags missing `files:read` | unit | `go test -run 'TestDoctor_FilesReadScope_Missing_Reports' ./internal/app/cmd/` | ❌ Wave 0 (new test in existing file) |
| **REQ-FILES-DEPLOY** | Full UAT: drag image into `#sb-{id}` channel → 👀 within 1s → Claude reply describes image | manual UAT | `km vscode start <sb-id>` + Slack manual drag-and-drop with image and PDF | ⚠️ Manual gate — checkpoint at end of plan execution |

### Sampling Rate

- **Per task commit:** `go test -run '<NewTestName>' ./pkg/slack/bridge/` (focused; ~1s)
- **Per wave merge:** `go test ./pkg/slack/bridge/ ./pkg/compiler/ ./internal/app/cmd/` (focused; ~10s)
- **Phase gate:** `go test ./...` (full suite; ~60-90s) green before `/gsd:verify-work`; PLUS manual UAT (drag image + drag PDF in `#sb-{id}` channel)

### Wave 0 Gaps

- [ ] `pkg/slack/bridge/file_downloader_test.go` — NEW file, covers REQ-FILES-DOWNLOAD, REQ-FILES-CAPS, REQ-FILES-S3-LAYOUT, REQ-FILES-FAILURE-WARNINGS (8+ tests)
- [ ] `pkg/slack/bridge/events_types_test.go` — NEW file (does not currently exist), covers REQ-FILES-SQS-PAYLOAD
- [ ] `pkg/slack/bridge/events_handler_test.go` — modify existing — covers REQ-FILES-ALLOWLIST and goroutine-timing for REQ-FILES-DOWNLOAD
- [ ] `pkg/compiler/userdata_slack_inbound_test.go` — modify existing — 2 new tests for REQ-FILES-POLLER + REQ-FILES-WRAPPER-FORMAT
- [ ] `internal/app/cmd/slack_test.go` — modify existing — 1 new test for REQ-FILES-SCOPE (init side)
- [ ] `internal/app/cmd/doctor_slack_inbound_test.go` — modify existing — 1 new test for REQ-FILES-SCOPE (doctor side)
- [ ] Test fixtures: NONE new — `recordingTransport`, `canned()`, `captureBridgeLogger` already exist in `aws_adapters_test.go`. `extractSlackInboundPoller` already exists in `userdata_slack_inbound_test.go`. Reuse.
- [ ] Framework install: NONE — `go test`, `testify` already in go.mod.

## Sources

### Primary (HIGH confidence)

- **Existing repo code** (Read tool):
  - `pkg/slack/bridge/events_types.go` — current `slackMessageEvent` and `InboundQueueBody` shape
  - `pkg/slack/bridge/events_handler.go` — current `Handle` flow + `isBotLoop` allow-list + reactor goroutine pattern (template for Phase 75 download goroutine)
  - `pkg/slack/bridge/aws_adapters.go` — `S3GetObjectAPI` narrow interface, `S3GetterAdapter`, `SlackPosterAdapter.PostMessage` (the warning-post path), `SlackReactorAdapter` (single-attempt + retry reference), `SQSAdapter`
  - `pkg/slack/bridge/aws_adapters_test.go:974-1031` — `recordingTransport`, `canned`, `captureBridgeLogger` fixtures (reuse for downloader tests)
  - `pkg/slack/bridge/events_handler_test.go:233-309` — table-test pattern + `TestEventsHandler_ThreadBroadcastPasses` template
  - `pkg/aws/mlflow.go:64-82` — `s3.PutObjectInput` reference pattern in this repo
  - `pkg/compiler/userdata.go:1259-1497` — existing inbound poller heredoc — exact insertion point for attachment-mirror block
  - `pkg/compiler/userdata_slack_inbound_test.go:174-184` — `extractSlackInboundPoller` helper for bounded assertions
  - `internal/app/cmd/slack.go:836-849` — `VerifyEventsAPIScopes` (one-line additive change)
  - `internal/app/cmd/doctor_slack.go:482-509` — `checkSlackAppEventsScopes` (one-line additive change)
  - `internal/app/cmd/doctor_slack_inbound_test.go:193-264` — scope-check test pattern (one new test mirrors existing `TestDoctor_SlackInboundEventsSubscription_MissingReactionsWrite`)
  - `infra/modules/lambda-slack-bridge/v1.0.0/main.tf:217-236` — `slack_bridge_transcript_s3_read` pattern (template for `slack_bridge_files_s3_write`)
  - `cmd/km-slack-bridge/main.go:108-117` — `KM_ARTIFACTS_BUCKET` env var already wired by Phase 68
  - `internal/app/cmd/bootstrap.go:870-895` — artifacts bucket creation (no Terraform module — informs S3 lifecycle home decision)

- **AWS SDK Go v2 docs**:
  - [s3 package — pkg.go.dev](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/s3) — `PutObjectInput.Body` is `io.Reader`; verified
  - [AWS S3 SDK examples](https://docs.aws.amazon.com/code-library/latest/ug/s3_example_s3_PutObject_section.html) — PutObject patterns

- **Slack API docs**:
  - [File object schema](https://docs.slack.dev/reference/objects/file-object/) — confirms `mimetype` (one word), `url_private`, `url_private_download`, `size`, `id`, `name` exact field names
  - [Working with files](https://docs.slack.dev/messaging/working-with-files/) — supplementary
  - [Events API](https://docs.slack.dev/apis/events-api/) — general event_callback envelope

### Secondary (MEDIUM confidence — verified with at least one official source + one community/GitHub source)

- **Slack file download bot-token + Authorization Bearer**:
  - [slackapi/deno-slack-sdk #285](https://github.com/slackapi/deno-slack-sdk/issues/285) — documents the 302-redirect-strips-Authorization bug (Pitfall 1). Issue closed without official resolution; informs Phase 75's manual-redirect approach
  - [Slack tokens reference](https://api.slack.com/tokens) — `files:read` scope required for `url_private_download`

- **AWS SDK Go v2 PutObject retry/body re-readability**:
  - [aws/aws-sdk-go-v2 #1123](https://github.com/aws/aws-sdk-go-v2/issues/1123) — documents `request body offset reset failed` on retry when Body is non-seekable (Pitfall 2)

### Tertiary (LOW confidence — single source, flagged for validation)

- **Slack file_share event `bot_id` placement** — Pitfall 3 above. Verified only via `slack-api-specs` GitHub repo (partial schema fragment); full schema not in single fetchable doc. Marked as deferred-to-follow-up rather than blocking Phase 75 because outbound files are out of scope per CONTEXT.md.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — every library / pattern already in repo
- Architecture: HIGH — CONTEXT.md is exhaustive; locked from PRD Express Path
- Pitfalls: HIGH for Pitfalls 1, 2, 4, 5; MEDIUM for Pitfall 3 (bot file_share — flagged as deferred)
- Test moat: HIGH — every test target exists or has a 1-to-1 template in the repo
- S3 lifecycle module home: MEDIUM — recommending new module is a judgment call; alternative (extend `bootstrap.go`) is documented

**Research date:** 2026-05-15
**Valid until:** 2026-06-15 (Slack API field names are stable; AWS SDK patterns are stable; bot-token redirect behavior verified against an open issue from 2024 still applicable in 2026)

**Anti-scope reminders for the planner:**
- Do NOT propose outbound files (out of scope)
- Do NOT propose virus scanning / OCR / PDF text extraction (out of scope)
- Do NOT propose changing the `Reactor` interface or `SlackPosterAdapter` (locked)
- Do NOT propose `file_revoked` / `file_deleted` handling (out of scope)
- Do NOT propose new SSM/DDB/profile schema (none needed per CONTEXT.md)
- DO fix the ROADMAP line 1626 `**Depends on:** Phase 74` → `**Depends on:** Phase 67, Phase 67.1` during planning
