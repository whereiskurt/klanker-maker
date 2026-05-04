---
phase: 69
slug: aws-api-scp-style-allow-deny-via-sigv4-inspection
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-04
---

# Phase 69 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` package (stdlib) |
| **Config file** | none — Go test runner auto-discovers `*_test.go` |
| **Quick run command** | `go test ./pkg/profile/... ./sidecars/http-proxy/httpproxy/... ./internal/app/cmd/... -run AWS -count=1` |
| **Full suite command** | `make build && go test ./...` |
| **Estimated runtime** | ~30s quick, ~120s full |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/profile/... ./sidecars/http-proxy/httpproxy/... ./internal/app/cmd/... -run AWS -count=1`
- **After every plan wave:** Run `make build && go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green AND manual UAT for the four-flow demo storyboard captured in `69-VERIFY.md`
- **Max feedback latency:** ~30 seconds (quick) / ~120 seconds (full)

---

## Per-Task Verification Map

This map will be filled in as plans are written. Initial seed by success criterion (from `69-CONTEXT.md`):

| Success Criterion | Behavior | Test Type | Automated Command | File Exists | Status |
|---|---|---|---|---|---|
| SC-1 | enforce + `["*"]` → `aws_api_allowed`; AWS CLI succeeds | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestAWSAllowlist` | ❌ Wave 0 | ⬜ pending |
| SC-2a | enforce + `[]` → 403 + `aws_api_blocked` | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestAWSAllowlist_Empty` | ❌ Wave 0 | ⬜ pending |
| SC-2b | platform-uid calls bypass gate + emit `aws_api_platform` | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestAWSAllowlist_PlatformUID` | ❌ Wave 0 | ⬜ pending |
| SC-3 | observe + `[]` → pass-through + blocked events | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestAWSObserve` | ❌ Wave 0 | ⬜ pending |
| SC-4 | `km shell --learn` generates `inspection: observe` + correct allowlist | unit | `go test ./internal/app/cmd/... -run TestCollectDockerObservations_AWS` | ❌ Wave 0 | ⬜ pending |
| SC-5a | validate rejects enforce + AI budget + missing `bedrock-runtime` | unit | `go test ./pkg/profile/... -run TestValidateSemantic_AWSBedrockCrossCheck` | ❌ Wave 0 | ⬜ pending |
| SC-5b | validate rejects wildcard mixed with explicit entries | unit | `go test ./pkg/profile/... -run TestValidateSemantic_AWSWildcardMixing` | ❌ Wave 0 | ⬜ pending |
| SC-6a | `km doctor aws_inspection_uid_map` returns OK on configured sandbox | unit (mocked SSM) | `go test ./internal/app/cmd/... -run TestDoctor_AWSInspectionUIDMap` | ❌ Wave 0 | ⬜ pending |
| SC-6b | `km doctor aws_allowlist_known_services` returns WARN on unknown slug | unit (mocked SSM) | `go test ./internal/app/cmd/... -run TestDoctor_AWSAllowlistKnownServices` | ❌ Wave 0 | ⬜ pending |
| SC-7 | `aws_api_allowed`/`_blocked`/`_platform` events emitted with full field set | unit (zerolog capture) | `go test ./sidecars/http-proxy/httpproxy/... -run TestAWSAuditEvents` | ❌ Wave 0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

The planner will refine this map per-task as plans are produced. Each plan's tasks must reference one or more rows above.

---

## Wave 0 Requirements

- [ ] `sidecars/http-proxy/httpproxy/aws_test.go` — stubs for SC-1, SC-2a, SC-2b, SC-3, SC-7 (allowlist gate, audit emitters, SigV4 parser, host regex)
- [ ] `pkg/profile/validate_aws_test.go` — stubs for SC-5a, SC-5b (Bedrock cross-check + wildcard-mixing rules)
- [ ] `internal/app/cmd/shell_learn_aws_test.go` — stubs for SC-4 (AWS service collection from proxy logs in learn mode)
- [ ] `internal/app/cmd/doctor_aws_test.go` — stubs for SC-6a, SC-6b (mock doctor checks, dry-run output formatting)
- [ ] No new framework install needed — Go `testing` is the standard

These four test files are stubbed in Wave 0 with `t.Skip("Wave 0: implementation pending")` markers so the green-baseline test surface exists before downstream plans depend on it.

---

## Manual-Only Verifications

The four-flow demo storyboard from `69-CONTEXT.md` Specifics requires a real EC2 sandbox. These are not automatable in CI:

| Behavior | Success Criterion | Why Manual | Test Instructions |
|---|---|---|---|
| Wide-open profile lets AWS CLI work end-to-end | SC-1 | Requires real EC2 sandbox + AWS credentials + SigV4 traffic | Profile A: `inspection: enforce`, `awsAllowlist: ["*"]`. Inside sandbox: `aws sts get-caller-identity`, `aws s3 ls`, `aws dynamodb list-tables`. All succeed. CloudWatch Logs shows `aws_api_allowed` for each. Capture log line. |
| Locked-down profile blocks AWS CLI but platform sidecars still work | SC-2 | Same | Profile B: same as A but `awsAllowlist: []`. Same three commands all return 403. CloudWatch shows `aws_api_blocked reason=empty_allowlist`. Concurrently `km email read <sandbox>` succeeds and emits `aws_api_platform` events. Capture both. |
| Observe mode lets all calls through but logs them as blocked | SC-3 | Same | Profile C: `inspection: observe`, `awsAllowlist: []`. AWS CLI succeeds end-to-end; CloudWatch shows everything as blocked with `mode=observe`. Capture log. |
| Learn mode generates correct allowlist from observed AWS calls | SC-4 | Requires real `km shell --learn` flow + S3 round-trip | Permissive profile with broad egress. `km shell --learn <sandbox>`, run `aws s3 ls`, `aws dynamodb list-tables`, `aws sts get-caller-identity`, exit. Generated YAML contains `inspection: observe` + `allowlist: [dynamodb, s3, sts]` (alphabetized). Capture YAML. |

These four captures live in `69-VERIFY.md` (created by `/gsd:verify-work`). They are mandatory for phase sign-off but not automatable.

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify references or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all four MISSING test files (`aws_test.go`, `validate_aws_test.go`, `shell_learn_aws_test.go`, `doctor_aws_test.go`)
- [ ] No watch-mode flags in test commands
- [ ] Feedback latency < 30s quick / 120s full
- [ ] `nyquist_compliant: true` set in frontmatter when planner-driven test stubs exist and pass with skip markers
- [ ] Manual UAT captures recorded in `69-VERIFY.md` for SC-1 through SC-4 demo flows

**Approval:** pending
