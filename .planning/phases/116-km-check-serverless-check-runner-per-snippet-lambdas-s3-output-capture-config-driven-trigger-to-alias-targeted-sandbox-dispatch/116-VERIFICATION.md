---
phase: 116-km-check-serverless-check-runner
verified: 2026-06-18T19:13:07Z
status: passed
score: 8/8 success criteria delivered (7 live-proven, 1 delivered+unit-tested)
human_verification:
  - test: "Re-fire the wiz-intel check above threshold and confirm a fresh cold-create box runs the delivered prompt end-to-end on a clean account"
    expected: "chk-* box cold-created from check-triage, prompt delivered to /workspace/.km-agent/queue, agent installs + executes, CHECK-TRIAGE OK output.json"
    why_human: "Requires live AWS (EC2 spend, SSM, EventBridge); already proven once in 116-UAT.md but is not a repeatable automated gate"
notes_open_followups:
  - "Bug M: km start <alias> stopped-instance lookup differs from km start <localnum> (km:sandbox-id tag namespace); self-heals — KNOWN, documented, not a phase-116 dispatch defect"
  - "Bug J: create-handler EventBridge retry can re-provision a duplicate chk- box (idempotency guard) — KNOWN follow-up"
  - "github-review profile lacks an explicit agent-install initCommand — worth confirming how its claude path works — KNOWN question, not a phase-116 deliverable"
  - "toolchain/km canonical S3 re-upload: replace the surgical linux-binary s3 cp with km init --sidecars — KNOWN housekeeping"
---

# Phase 116: km check Serverless Check Runner — Verification Report

**Phase Goal:** Operators author small Python "check" snippets, deploy each as its own SDK-provisioned AWS Lambda (no terragrunt-per-check), run them on a schedule / on demand / as a `github.events` pre-filter, capture JSON output to the S3 artifact bucket, and — under a config-driven `when_py` predicate — fire an alias-targeted sandbox prompt (resume-or-cold-create) carrying an expanded prompt template. Ships with two working example checks (QOTD; simulated Wiz Threat Intel), deployed and demonstrated end-to-end.

**Verified:** 2026-06-18T19:13:07Z
**Status:** passed (PASS-WITH-DOCUMENTED-FOLLOWUPS)
**Re-verification:** No — initial verification

## Goal Achievement

### Success Criteria (ROADMAP Phase 116)

| # | Criterion | Verdict | Evidence |
| --- | --- | --- | --- |
| 1 | `km check deploy` provisions per-check Lambda via SDK (shared `{prefix}-check-runner` role) + `{prefix}-checks` DDB row, no terragrunt-per-check | delivered+live-proven | `pkg/check/{lambda,ddb}.go` SDK CRUD; `km check deploy` subcommand (`check.go:118`); UAT: `km-check-qotd`, `km-check-wiz-intel` created live, `km-checks` rows written, shared `km-check-runner` role ARN confirmed |
| 2 | Deployed check runs (EventBridge Scheduler or `km check run`), executes snippet w/ injected env, writes stdout to `s3://{artifacts}/check-runs/<name>/<ts>/output.json` | delivered+live-proven | `_km_check_bootstrap.py` (subprocess-exec + verbatim S3 write); `check schedule` subcommand; UAT: QOTD wrote real quote JSON to `s3://km-artifacts-12345/check-runs/qotd/<ts>/output.json` (proves open egress + `requests` packaging) |
| 3 | `checks.triggers` `when_py` predicate (inline or `@file`) eval'd inline → truthy emits `CheckDispatch` → `ttl-handler` alias-targeted resume-or-cold-create with expanded prompt (cooldown-guarded) | delivered+live-proven | `config.CheckTrigger` w/ `WhenPy`/`Profile`/`OnAbsent`/cooldown; bootstrap `_eval_predicate` + `CheckDispatch` PutEvents; `ttl-handler` `handleCheckDispatch`; UAT: above-threshold `triggered:true` → `check_dispatch_cold_create` w/ `profile:github-review` → create-handler provisioned; below-threshold `triggered:false` (negative path); **cold-create prompt DELIVERY proven** (`001.prompt` w/ `{{reason}}`/`{{out.max_affected}}` substituted) after SCP G2 applied; full agent EXECUTION proven (`CHECK-TRIAGE OK` output) |
| 4 | Shared `pkg/dispatch` (alias-resolve + resume/cold decision); warm=`handleAgentRun` SSM, cold=`PutSandboxCreate`; bridges NOT modified | delivered+live-proven | `pkg/dispatch.ResumeOrCreate` reuses `ResolveByAliasWithStatus`; 5 unit tests (Running/Stopped/AbsentCold/AbsentSkip/Cooldown) green; `ttlAgentRunSink`/`ttlColdCreateSink` in `check_dispatch.go`; **verified no bridge imports `pkg/dispatch`** (github/slack/h1 unchanged → no parity risk); cold + below-threshold live |
| 5 | Two scaffolding modules (`dynamodb-checks` + `check-runner-role`); `km init` deploys them + CheckDispatch rule + widened ttl-handler IAM/env | delivered+live-proven | `infra/modules/{dynamodb-checks,check-runner-role}/v1.0.0`; live units `infra/live/use1/{...}/terragrunt.hcl`; `init.go` regionalModules (22→24, ordered table-before-role); UAT: `km init --dry-run=false` applied both + CheckDispatch rule ENABLED + IAM widened, no destroy-class gate trip |
| 6 | Packaging: zip + arch-correct wheels default; `--image` container opt-in w/ lazily SDK-created shared ECR repo | delivered (zip live; --image unit-only) | `package.go` `BuildZip` (`--platform manylinux2014_aarch64 --only-binary :all:`, S3 over 50MB); `ecr.go` lazy repo create; UAT: zip+arm64 wheels live (QOTD `requests`); `--image` not live-tested (path exists, unit-covered) |
| 7 | `km check ls\|get\|logs\|schedule\|rm\|sync`; `km doctor` reports orphan Lambdas/schedules + config drift | delivered+live-proven | all 8 subcommands present (`check.go`); `doctor_checks.go` 4 sub-checks (Checks Table / Orphan Check Lambdas / Orphan Check Schedules / Check Trigger Drift); UAT: ls/get/rm/deploy live; Bug F FIXED (`DoctorDeps.ChecksDDBClient` wired) → all four doctor lines report live |
| 8 | Two `profiles/checks/` examples deployed live + demonstrated: QOTD fetches a quote; Wiz emits advisories+counts and (w/ trigger) fires a sandbox | delivered+live-proven | `profiles/checks/{qotd,wiz-intel}` + `checks.triggers.example.yaml`; UAT: QOTD fully live (real quote off internet); Wiz full chain proven — check fires → CheckDispatch → cold-create → prompt delivered → **agent executed the prompt** (`CHECK-TRIAGE OK` Log4Shell containment answer) |

**Score:** 8/8 criteria delivered. 7 live-proven end-to-end; SC#6 delivered with the zip path live-proven and `--image` unit-tested only (explicitly an opt-in path, not gated to "live" by the criterion).

### must_haves (116-08-PLAN.md) cross-check

| Truth | Status | Evidence |
| --- | --- | --- |
| Scaffolding deployed live via make build + km init | ✓ VERIFIED | UAT Task 1: both modules + CheckDispatch rule + widened IAM applied; `km-checks` table ACTIVE; `km-check-runner` role ARN |
| QOTD deploys, runs, writes quote to S3 | ✓ VERIFIED | UAT: real quote JSON at `check-runs/qotd/<ts>/output.json` |
| Wiz above-threshold emits CheckDispatch → ttl-handler resume/cold-create w/ expanded prompt; below-threshold NO dispatch | ✓ VERIFIED | UAT: `triggered:true`→cold-create+prompt delivered; `triggered:false` below-threshold |
| km doctor reports check group correctly against live fleet | ✓ VERIFIED | Bug F fixed; all 4 doctor sub-checks report live |

### Code Anchors

| Anchor | Status | Detail |
| --- | --- | --- |
| `pkg/dispatch` (ResumeOrCreate) | ✓ VERIFIED | `dispatch.go:41` `ResumeOrCreate`, `interfaces.go` `ResolveByAliasWithStatus`, 5 tests |
| `pkg/check` (CLI/lambda CRUD/packaging/bake) | ✓ VERIFIED | `lambda.go`, `ddb.go`, `package.go`, `ecr.go`, `trigger.go` (`BakeTrigger`+sourceHash), `bootstrap.go`, `_km_check_bootstrap.py` |
| `cmd/ttl-handler/check_dispatch.go` (CheckDispatch/check-run + 3 sinks) | ✓ VERIFIED | `handleCheckDispatch`, `handleCheckRun`, `ttlAgentRunSink`/`ttlColdCreateSink`/skip + `ttlLambdaInvoker` |
| Python bootstrap `profiles/checks/_bootstrap/` | ✓ VERIFIED | `_km_check_bootstrap.py` + `test_bootstrap.py` (3 pytest pass) + `KM_CHECK_TRIGGER.schema.md` |
| 2 scaffolding modules | ✓ VERIFIED | `infra/modules/dynamodb-checks/v1.0.0` + `check-runner-role/v1.0.0` + live units |
| config `ChecksConfig`/`CheckTrigger` | ✓ VERIFIED | `config.go:378/394`, merge-list + getters; `Profile` field present (Bug A fix) |
| SCP carve-out (scp/v2.0.0 + bootstrap.go trustedSSM) | ✓ VERIFIED | `main.tf:58` + `bootstrap.go:808` both add `*-create-handler` to SSM trust (G2, applied live) |
| Example checks (qotd + wiz-intel + check-triage.yaml) | ✓ VERIFIED | `profiles/checks/{qotd,wiz-intel}`; `profiles/check-triage.yaml` (validates; installs agent CLI via initCommands — Bug K fix) |

### Key Link Verification

| From | To | Via | Status |
| --- | --- | --- | --- |
| `km check run wiz-intel` (above threshold) | alias box dispatched | `CheckDispatch` → ttl-handler `handleCheckDispatch` → ResumeOrCreate | WIRED (live-proven cold path; warm/resume path code-complete via `handleAgentRun`, unit-tested) |
| bootstrap predicate truthy | `CheckDispatch` event | `_eval_predicate` → PutEvents `km.sandbox` bus | WIRED (live: `triggered:true`) |
| cold-create | prompt on box | `SandboxCreateDetail.prompt` → create-handler `km create --prompt` → `001.prompt` | WIRED (live: Bug E/G1/G2/I fixed, delivery proven) |

## Build / Test Results

| Command | Exit | Result |
| --- | --- | --- |
| `go build ./...` | 0 | clean |
| `go test ./pkg/dispatch/... ./pkg/check/... ./internal/app/config/ ./cmd/ttl-handler/...` | 0 | dispatch ok, config ok, ttl-handler ok; pkg/check [no Go test files — bootstrap covered by pytest] |
| `go test ./pkg/compiler/` (goldens) | 0 | ok 5.451s (no golden regression from Bug K queue-runner change) |
| `go test ./internal/app/cmd/ -run 'Check\|Doctor\|SCP'` | 0 | ok 8.323s |
| `pytest profiles/checks/_bootstrap/` | 0 | 3 passed |
| `km validate profiles/check-triage.yaml` | 0 | valid |

All exit codes read from the command's own `$?` / FAIL-or-ok summary line, not the pipe.

### Anti-Patterns

None blocking. The phase's UAT surfaced ~13 real issues during live execution and **fixed** them in-tree (Bug A profile field, B 409, C profile-stage, E prompt-delivery, G1 SSM-IAM-tag, G2 SCP carve-out, I nil-ptr, K agent-install, F doctor-wiring, 409 wait-for-Active, module count 22→24). Git history (`4fbe51bd`…`875526eb`) confirms each fix landed.

### Open Follow-ups (KNOWN — documented in 116-UAT.md, NOT phase gaps)

These are explicitly recorded in 116-UAT.md as out-of-scope follow-ups; the phase does not claim them done:

1. **Bug M** — `km start <alias>` stopped-instance lookup differs from `km start <localnum>` (likely `km:sandbox-id` tag namespace); self-heals. Orthogonal to check dispatch.
2. **Bug J** — create-handler EventBridge retry can re-provision a duplicate `chk-` box (needs an idempotency guard on retries).
3. **github-review agent-install question** — that profile lacks an explicit agent-install initCommand; worth confirming how its claude path works. Not a phase-116 deliverable.
4. **toolchain/km canonical re-upload** — replace the surgical linux-binary `s3 cp` with `km init --sidecars`. Housekeeping.

### Gaps Summary

No goal-blocking gaps. Every Success Criterion is delivered in code and, for 7 of 8, demonstrated live end-to-end per 116-UAT.md — including the hardest link: check fires → CheckDispatch → cold-create from a SOPS-free `check-triage` box → expanded prompt delivered to `/workspace/.km-agent/queue` → on-box agent installed → prompt **executed** → a correct `CHECK-TRIAGE OK` triage answer. SC#6's `--image` container path is delivered + unit-tested but not live-exercised (the criterion only requires zip-default live; `--image` is opt-in). The four open items are real but explicitly documented as follow-ups outside this phase's dispatch logic (Bug M/J are platform-layer; the github-review and toolchain items are housekeeping/questions).

---

_Verified: 2026-06-18T19:13:07Z_
_Verifier: Claude (gsd-verifier)_
