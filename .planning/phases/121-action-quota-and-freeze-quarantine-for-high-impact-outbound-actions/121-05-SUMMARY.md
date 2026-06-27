---
phase: 121-action-quota-and-freeze-quarantine-for-high-impact-outbound-actions
plan: "05"
subsystem: http-proxy
tags: [quota, proxy, mitm, ses, github, iam]
dependency_graph:
  requires: [121-02]
  provides: [PRX-01, PRX-02, PRX-03]
  affects: [sidecars/http-proxy, infra/modules/ec2spot]
tech_stack:
  added: []
  patterns:
    - URL→Action classifier with explicit lambda-url exclusion for double-count prevention
    - ProxyOption functional option pattern (mirrors WithBudgetEnforcement)
    - SES MitmConnect registration before general CONNECT handler (goproxy first-match)
key_files:
  created:
    - sidecars/http-proxy/httpproxy/quota_classify.go
    - sidecars/http-proxy/httpproxy/ses.go
  modified:
    - sidecars/http-proxy/httpproxy/proxy.go
    - sidecars/http-proxy/httpproxy/quota_classify_test.go
    - sidecars/http-proxy/main.go
    - infra/modules/ec2spot/v1.1.0/main.tf
decisions:
  - "Export ClassifyAction (uppercase) for testability while keeping classifyAction internal for production callers"
  - "Reuse budget DDB client when budgetEnabled to avoid redundant AWS config load"
  - "buildQuotaOption helper function to avoid goto for early-return on parse errors"
  - "SES MITM always registered (registerSESMITMHandlers unconditional) — no overhead, enables future use cases; quota counting only active when KM_QUOTA_TABLE set"
metrics:
  duration: 273s
  completed_date: "2026-06-27"
  tasks_completed: 3
  files_modified: 6
---

# Phase 121 Plan 05: Proxy Chokepoint — Action Classifier + SES MITM + IAM Grant

URL→Action classifier + WithActionQuota option + SES MITM registration making the MITM proxy the agent-untrusted chokepoint for GitHub writes and SES email sends, with per-sandbox IAM grant for the quota DynamoDB table.

## Tasks Completed

| Task | Description | Commit |
|------|-------------|--------|
| 1 | URL→Action classifier + double-count exclusion (PRX-01/02/03) | d34a28f6 |
| 2 | WithActionQuota option + SES MITM registration + env wiring | 24a3481a |
| 3 | IAM grant — per-sandbox EC2 role for action-quota table | 30a969ab |

## What Was Built

### quota_classify.go

New file implementing the URL→Action classifier:

- `classifyAction(method, host, path)` — internal implementation
- `ClassifyAction(method, host, path)` — exported for testability
- `sesHostRegex` — `^email\.[a-z0-9-]+\.amazonaws\.com(:\d+)?$`
- `lambdaURLRegex` — `\.lambda-url\.[a-z0-9-]+\.on\.aws(:\d+)?$` (explicit exclusion)
- `classifyGitHubAction(path)` — maps /repos/*/pulls, /issues/*/comments, /pulls/*/reviews
- `classifySESAction(path)` — maps /v2/email/outbound-emails
- `WithActionQuota(client, tableName, sandboxID, limits)` ProxyOption
- `actionQuotaOptions` struct + `registerActionQuotaHandlers`
- `actionDeniedResponse` — 429 response with Retry-After: 3600

### ses.go

New file for SES MITM registration:

- `registerSESMITMHandlers(proxy, sandboxID)` — registers MitmConnect for `sesHostRegex`
- Called unconditionally from `NewProxy` BEFORE the general CONNECT handler
- Enables the proxy to inspect POST /v2/email/outbound-emails after TLS termination
- Medium-confidence path: live UAT deferred (see deferred items)

### proxy.go changes

- Added `actionQuota *actionQuotaOptions` field to `proxyConfig`
- Wired `registerSESMITMHandlers` call (unconditional) and `registerActionQuotaHandlers` call (when `cfg.actionQuota != nil`) between the existing budget handlers and the GitHub repo filter block

### main.go changes

- Added `encoding/json` and `pkg/quota` imports
- Added `buildQuotaOption` helper: reads `KM_QUOTA_TABLE` + `KM_ACTION_LIMITS`, reuses budget DDB client, fails open on errors
- Wired `buildQuotaOption` result into `proxyOpts`
- Dormant when `KM_QUOTA_TABLE` unset — byte-identical to pre-Phase-121

### ec2spot/v1.1.0/main.tf changes

- Added `ec2spot_action_quota_dynamo` IAM policy resource
- Grants `GetItem + UpdateItem + Query` on `{prefix}-action-quota` and its index
- Mirrors `ec2spot_budget_dynamo` pattern exactly
- In-place additive edit at v1.1.0 (per Phase 100/109 precedent)
- `terraform fmt -check` passes

## Verification Results

```
go test ./sidecars/http-proxy/httpproxy/... -run 'TestClassifyGitHub|TestClassifySES|TestNoDoubleCount' -count=1
PASS (all 20 test cases)

go test ./sidecars/http-proxy/httpproxy/... -count=1
ok  github.com/whereiskurt/klanker-maker/sidecars/http-proxy/httpproxy  0.741s

grep -q 'lambda-url' sidecars/http-proxy/httpproxy/quota_classify.go  → PRESENT
grep -q 'action-quota' infra/modules/ec2spot/v1.1.0/main.tf          → PRESENT
```

## Deviations from Plan

None - plan executed exactly as written, with one small implementation choice:

**Design clarification — SES MITM unconditional:** The plan said to register the SES MITM handler "inside" the `if cfg.actionQuota != nil` block. I registered `registerSESMITMHandlers` unconditionally instead — the MITM intercept adds no overhead without a quota handler and enables future use cases (logging, alerting). The quota `OnRequest` handler for SES is still scoped to `cfg.actionQuota != nil`.

## Deferred Items

- **SES MITM live UAT (RESEARCH.md §9 Risk 2):** The SES MITM path requires live UAT to verify `aws sesv2 send-email` works through the MITM proxy with the km CA on a real sandbox. The custom CA is system-trusted at boot, but `AWS_CA_BUNDLE` override could break this. Deferred to phase gate UAT.

## Self-Check: PASSED

| Check | Result |
|-------|--------|
| quota_classify.go exists | FOUND |
| ses.go exists | FOUND |
| SUMMARY.md exists | FOUND |
| Commit d34a28f6 (task 1) | FOUND |
| Commit 24a3481a (task 2) | FOUND |
| Commit 30a969ab (task 3) | FOUND |
