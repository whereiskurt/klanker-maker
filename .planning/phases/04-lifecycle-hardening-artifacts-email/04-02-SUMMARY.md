---
phase: 04-lifecycle-hardening-artifacts-email
plan: "02"
subsystem: infra
tags: [ses, email, terraform, sesv2, route53, s3, tdd]

requires:
  - phase: 02-core-provisioning-security-baseline
    provides: km-sandbox-artifacts-ea554771 S3 bucket and klankermaker.ai Route53 zone
  - phase: 03-sidecar-enforcement-lifecycle-management
    provides: working sandbox runtime that this email layer sits on top of

provides:
  - SES Terraform module at infra/modules/ses/v1.0.0/ — domain identity, DKIM, MX record, active receipt rule set, inbound S3 rule
  - SESV2API narrow interface in pkg/aws/ses.go
  - ProvisionSandboxEmail, SendLifecycleNotification, CleanupSandboxEmail helpers
  - Full unit test coverage via mockSESV2API

affects:
  - 04-03-PLAN.md (wires SES helpers into compiler and create/destroy commands)
  - pkg/compiler (SES email address provisioned during km create)
  - internal/app/cmd/create.go (displays assigned email address to operator)
  - pkg/lifecycle/teardown.go (CleanupSandboxEmail called during destroy)

tech-stack:
  added:
    - github.com/aws/aws-sdk-go-v2/service/sesv2 v1.60.1
    - hashicorp/aws terraform provider v6.37.0 (SES resources)
  patterns:
    - Narrow SESV2API interface matching scheduler.go / mlflow.go pattern
    - TDD red-green cycle for pkg/aws helpers
    - Terraform SES module with DKIM CNAME x3, TXT verification, MX record

key-files:
  created:
    - infra/modules/ses/v1.0.0/main.tf
    - infra/modules/ses/v1.0.0/variables.tf
    - infra/modules/ses/v1.0.0/outputs.tf
    - pkg/aws/ses.go
    - pkg/aws/ses_test.go
  modified: []

key-decisions:
  - "SES receipt rule recipients set to [var.domain] not ['.'+var.domain] — SES catches all addresses at the domain including exact matches"
  - "S3 bucket policy uses aws:Referer condition with account ID to restrict PutObject to SES from the same account"
  - "Rule 3 auto-fix: artifacts.go already existed in pkg/aws — UploadArtifacts was pre-written; build compiled without modification"
  - "Terraform position attribute on aws_ses_receipt_rule removed — not supported in provider v6 (deviation fix)"
  - "data.aws_region.current.name deprecated; switched to .id attribute via local variable"

patterns-established:
  - "SES module pattern: domain identity + DKIM + DNS records + receipt rule set (activated) + receipt rule = one module"
  - "SESV2API narrow interface: three methods (CreateEmailIdentity, DeleteEmailIdentity, SendEmail)"
  - "CleanupSandboxEmail idempotent: swallows NotFoundException so km destroy is safe to retry"

requirements-completed: [MAIL-01, MAIL-02, MAIL-03, MAIL-04, MAIL-05]

duration: 3min
completed: 2026-03-22
---

# Phase 4 Plan 02: SES Email Infrastructure Summary

**SES Terraform module with DKIM/MX DNS records and active receipt rule, plus SESV2API Go interface with ProvisionSandboxEmail/SendLifecycleNotification/CleanupSandboxEmail helpers fully tested via mock.**

## Performance

- **Duration:** ~3 min
- **Started:** 2026-03-22T05:37:24Z
- **Completed:** 2026-03-22T05:40:34Z
- **Tasks:** 2
- **Files modified:** 5 created, 2 modified (go.mod, go.sum)

## Accomplishments

- SES Terraform module at `infra/modules/ses/v1.0.0/` validates with `terraform validate`; creates domain identity, DKIM (x3 CNAME), TXT verification, MX record, active receipt rule set, and S3-action receipt rule
- `SESV2API` narrow interface defined; `ProvisionSandboxEmail`, `SendLifecycleNotification`, `CleanupSandboxEmail` implemented in `pkg/aws/ses.go` following the same pattern as `scheduler.go`
- 7 TestSES_* unit tests pass via `mockSESV2API` with no real AWS credentials required

## Task Commits

1. **Task 1: SES Terraform module** — `186247f` (feat)
2. **Task 2 RED: Failing SES tests** — `10c8bf7` (test)
3. **Task 2 GREEN: ses.go implementation** — `1868a45` (feat)

## Files Created/Modified

- `infra/modules/ses/v1.0.0/variables.tf` — domain, route53_zone_id, artifact_bucket_name, artifact_bucket_arn variables
- `infra/modules/ses/v1.0.0/main.tf` — SES identity, DKIM, Route53 records, receipt rule set + active set, receipt rule with S3 action, S3 bucket policy
- `infra/modules/ses/v1.0.0/outputs.tf` — domain_identity_arn, domain_identity_verification_token, receipt_rule_set_name
- `pkg/aws/ses.go` — SESV2API interface, ProvisionSandboxEmail, SendLifecycleNotification, CleanupSandboxEmail
- `pkg/aws/ses_test.go` — 7 tests with mockSESV2API

## Decisions Made

- `aws_ses_receipt_rule` `position` attribute not supported in provider v6 — removed (deviation fix during Task 1 validation)
- `data.aws_region.current.name` deprecated in provider v6 — switched to `.id` via `local.region_name`
- `CleanupSandboxEmail` swallows `sesv2types.NotFoundException` for idempotency — `km destroy` may be retried
- Notifications From address fixed at `notifications@{domain}`, subject format `km sandbox {event}: {sandbox-id}`

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Terraform `position` attribute removed from `aws_ses_receipt_rule`**
- **Found during:** Task 1 (Terraform validate)
- **Issue:** AWS provider v6 does not support `position` on the rule resource (only on actions)
- **Fix:** Removed `position = 1` from the rule block; `position` on the `s3_action` block is correct
- **Files modified:** `infra/modules/ses/v1.0.0/main.tf`
- **Verification:** `terraform validate` passes with no errors
- **Committed in:** `186247f`

**2. [Rule 1 - Bug] `data.aws_region.current.name` deprecated**
- **Found during:** Task 1 (Terraform validate — warning)
- **Issue:** `.name` attribute deprecated in hashicorp/aws v6; replaced with `.id`
- **Fix:** Added `locals { region_name = data.aws_region.current.id }` and updated MX record reference
- **Files modified:** `infra/modules/ses/v1.0.0/main.tf`
- **Verification:** `terraform validate` reports `Success! The configuration is valid.`
- **Committed in:** `186247f`

**3. [Rule 3 - Blocking] `artifacts_test.go` pre-existed referencing `UploadArtifacts`**
- **Found during:** Task 2 GREEN (go build)
- **Issue:** `pkg/aws/artifacts_test.go` was a wave-0 stub pre-written before this plan; it references `kmaws.UploadArtifacts` which lives in `artifacts.go`. That file already existed — no action needed, it compiled cleanly.
- **Fix:** No fix required — `artifacts.go` was already present with matching `UploadArtifacts` signature
- **Files modified:** none
- **Verification:** `go test ./pkg/aws/... -count=1` passes (all tests including artifacts)

---

**Total deviations:** 3 noted (2 Terraform provider v6 fixes, 1 pre-existing stub that was already satisfied)
**Impact on plan:** All fixes necessary for correctness. No scope creep.

## User Setup Required

**External service requires manual configuration.** The SES module creates the infrastructure but two manual AWS steps are needed before email works end-to-end:

1. **Exit SES sandbox mode** — New AWS accounts start in SES sandbox (sending to verified addresses only). To send operator notifications to unverified addresses: AWS Console → SES → Account dashboard → Request production access (or open AWS support ticket).

2. **Set `KM_OPERATOR_EMAIL` environment variable** — Operator's email address for lifecycle notifications. Example: `export KM_OPERATOR_EMAIL=admin@company.com`

## Next Phase Readiness

- Plan 04-03 can now wire `ProvisionSandboxEmail` into `internal/app/cmd/create.go` and `CleanupSandboxEmail` into `pkg/lifecycle/teardown.go`
- The SES Terraform module is ready to be invoked as a one-time shared module (not per-sandbox) — Plan 04-03 adds the Terragrunt live config
- No blockers

## Self-Check: PASSED

- infra/modules/ses/v1.0.0/main.tf: FOUND
- infra/modules/ses/v1.0.0/variables.tf: FOUND
- infra/modules/ses/v1.0.0/outputs.tf: FOUND
- pkg/aws/ses.go: FOUND
- pkg/aws/ses_test.go: FOUND
- Commit 186247f: FOUND
- Commit 10c8bf7: FOUND
- Commit 1868a45: FOUND

---
*Phase: 04-lifecycle-hardening-artifacts-email*
*Completed: 2026-03-22*
