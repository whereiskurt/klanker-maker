---
phase: 66-multi-instance-support-configurable-resource-prefix-and-email-subdomain
plan: 03
type: execute
status: complete
completed: 2026-05-04
requirements:
  - REQ-PLATFORM-MULTI-INSTANCE
---

# 66-03: Resource-name + SSM-path Sweep — SUMMARY

## one-liner

Migrated ~134 hardcoded `"km-..."` resource names and ~86 `/km/...` SSM paths across 38 files (operator cmd/, Lambda handlers, configui, pkg/aws, pkg/compiler, pkg/slack/bridge) to use `cfg.GetResourcePrefix()` / `cfg.GetSsmPrefix()` helpers (operator-side) or `KM_RESOURCE_PREFIX` / `KM_SSM_PREFIX` env vars with literal fallbacks (Lambda handlers + sidecars).

## what shipped

**Commit chain (5 commits across 3 logical tasks + 1 followup):**

| Commit | Scope |
|---|---|
| `d58e5fd` | Task 1: 22 files in `internal/app/cmd/` |
| `5164471` | Task 2: 5 Lambda handlers |
| `6c17721` | Task 3: configui + pkg/aws + pkg/compiler + pkg/slack/bridge |
| `1433e63` | Followup: agent.go, pause.go, sandbox_ref.go (residual SandboxTableName fallbacks) |

**New helper added in 66-01-extension** (committed with d58e5fd):
- `Config.GetSandboxTableName()` — returns `cfg.SandboxTableName` if non-empty, else `cfg.GetResourcePrefix() + "-sandboxes"`. Matches existing pattern of `GetSlackThreadsTableName()` / `GetSlackStreamMessagesTableName()` shipped in Phase 67.

**Phase 67/68 drift sites — all 6 fixed:**
1. ✓ `status.go:468` — `"km-slack-threads"` → `cfg.GetSlackThreadsTableName()`
2. ✓ `init.go:1724` — `ForceSlackBridgeColdStartWith` accepts `functionName string` param
3. ✓ `slack.go:759` — `PersistSigningSecret` accepts `ssmPrefix string` param
4. ✓ `doctor.go:2330,2341,2392` — `"km-sandboxes"` → `cfg.GetSandboxTableName()`
5. ✓ `create.go` — wires `KM_SLACK_THREADS_TABLE` and `KM_SLACK_STREAM_TABLE` env vars before invoking compiler
6. (Plan 04 scope) `infra/modules/lambda-slack-bridge/v1.0.0/main.tf:5` — handled in Wave 4

**Function signature changes (production + tests updated in lockstep):**
- `kmaws.DeleteTTLSchedule(ctx, api, sandboxID)` → `kmaws.DeleteTTLSchedule(ctx, api, sandboxID, prefix)`
- `compiler.BuildTTLScheduleInput(...)` → adds trailing `prefix string` arg
- `kmaws.WriteRotationAudit(ctx, api, event)` → adds trailing `prefix string` arg
- `cmd.checkGitHubConfig(ctx, client)` → adds trailing `ssmPrefix string` arg
- `cmd.checkCredentialRotationAge(ctx, client, days)` → adds trailing `ssmPrefix string` arg
- `cmd.ForceSlackBridgeColdStartWith(ctx, client)` → adds trailing `functionName string` arg
- `cmd.computeIdleRemaining(...)` → adds trailing `resourcePrefix string` arg
- `cmd.rotatePlatform(...)` / `rotateSandbox(...)` → add trailing prefix args
- `pkg/aws/ec2_ami.go AMIName()` → variadic `prefix string` (added in 66-02; reused here)

## key-files

### created
- (none — all changes are migrations of existing files)

### modified (38 files total — list elided; see commit diffs)
- 22 files in `internal/app/cmd/` (Task 1 + followup)
- 5 Lambda main.go files (Task 2)
- 11 files in `cmd/configui/`, `pkg/aws/`, `pkg/compiler/`, `pkg/slack/bridge/` (Task 3)

## verification

- ✓ `go build ./...` clean
- ✓ `go vet ./internal/... ./pkg/aws/... ./pkg/compiler/... ./cmd/...` clean (only pre-existing IPv6 lint in sidecars/http-proxy unrelated to Phase 66)
- ✓ `go test ./pkg/aws/... ./pkg/compiler/... ./internal/app/config/...` passes
- ✓ `go test ./internal/app/cmd/...` — 15 failures, ALL pre-existing on `main` (verified by checkout-and-rerun): expired SSO credentials, source-grep brittleness in `TestCreateDockerWritesComposeFile` / `TestApplyLifecycleOverrides_*`, env-write permission in `TestConfigureInteractivePromptsUseNewNames`. Zero new regressions.

## grep audit

`grep -rn '"km-' --include="*.go" internal/app/cmd/ pkg/ cmd/` returns ~15 residual sites — ALL of which are **explicitly out of scope** per the ROADMAP (`Phase 66 Out of scope` section): per-sandbox resource names already qualified by `{sandboxID}` or `{region}` (e.g. `km-budget-<sandboxID>`, `km-docker-<sandboxID>-<region>`, `km-github-token-refresher-<sandboxID>`). These are collision-free across installs because sandbox IDs are randomly generated.

`grep -rn '/km/' --include="*.go" internal/app/cmd/ pkg/ cmd/` returns only fallback-literal sites in Lambda handler helpers and pre-formatted strings inside `cfg.GetSsmPrefix()`-derived paths.

## deviations from plan

1. **`GetSandboxTableName()` helper added to config.go** — not in original Plan 01 scope (which intentionally minimized scope to EmailSubdomain + helpers). Added here because 5+ callers in cmd/ benefit from the shared helper rather than inline fallback closures. This is a clean extension, follows the established pattern, and makes Wave 3 cleaner. Plan 01's tests cover the underlying `GetResourcePrefix()` so no new tests were strictly required, but the helper is exercised by the Wave 3 changes.

2. **Two mid-execution agent crashes due to "Prompt is too long"** — the executor agent ran out of context after ~270 tool uses each time, leaving uncommitted work. The orchestrator (this session) recovered by reviewing the diff, committing logical groupings, fixing 1 production caller bug (`list.go:108` self-call lag), and writing this summary. The committed work is unchanged in semantics; the recovery only affected commit boundaries.

## what's enabled for Wave 4

All Go-side prefix/SSM/email migrations are done. Wave 4 (Plan 66-04) can now:
- Wire `KM_RESOURCE_PREFIX` and `KM_EMAIL_SUBDOMAIN` env vars in Terragrunt site.hcl + Lambda module env blocks
- Update 7 DynamoDB live configs (5 existing + 2 new from Phase 67/68: `dynamodb-slack-threads`, `dynamodb-slack-stream-messages`)
- Fix `infra/modules/lambda-slack-bridge/v1.0.0/main.tf:5` Lambda function_name
- Add the missing `dependency "slack_threads"` block in `lambda-slack-bridge/terragrunt.hcl`

The Go code is now prefix-aware; the TF/Terragrunt layer is the last hold-out.
