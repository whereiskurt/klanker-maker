# Phase 119 — Deferred / out-of-scope items

## Pre-existing test failure (NOT a Phase 119 regression)

**`TestDestroyCmd_InvalidSandboxID`** (`internal/app/cmd/destroy_test.go:186`) fails on the
full-suite gate. **Confirmed pre-existing:** it also FAILs on commit `8b288a2a` (the Phase
119-04 completion commit, before any 119-05 work was done) when run in isolation
(`go test ./internal/app/cmd/ -run TestDestroyCmd_InvalidSandboxID` → FAIL, exit 1).

- **Symptom:** every sub-case (`sb-12345678x`, `sb-1234567`, `sb-123456789`,
  `not-a-sandbox-id`, `ABC-abc12345`, `abc12345`) expects "error about invalid sandbox ID
  format" but `km destroy` returns an empty error.
- **Scope:** the `km destroy` command's sandbox-ID format validation — **unrelated** to Phase
  119 (Slack inbound parallelism). Phase 119 touched the bridge `events_handler.go`,
  `pkg/aws/sqs.go` queue attrs, `pkg/compiler/userdata.go` poller, profile schema, and the two
  `create_{slack,github}_inbound_test.go` files — none of which intersect `destroy`.
- **Action:** logged here per the SCOPE BOUNDARY rule (do not auto-fix pre-existing failures in
  unrelated files). Recommend a small standalone cleanup (mirror the
  `project_cmd_suite_pre_existing_failures` reconciliation pattern) to either fix the validation
  or reconcile the stale test assertion. Not blocking Phase 119.

## Cosmetic poller noise (low priority)

`[: : integer expression expected` at the inbound poller's empty-batch `COUNT` check
(`[ "$COUNT" -eq 0 ]`) when SQS long-poll returns no messages and `jq` yields an empty
string. Pre-existing; harmless (the `|| continue` loop proceeds correctly). One-line fix
candidate: `[ "${COUNT:-0}" -eq 0 ]`.

## Infra hardening (separate from Phase 119)

Cold-create (`km create` via the create-handler Lambda) intermittently fails with
`Failed to install provider hashicorp/aws v6.46.0 ... registry.terraform.io ...
Client.Timeout` — the provider plugin is not pre-cached in the Lambda's `infra.tar.gz`, so a
slow/unreachable registry stalls provisioning (6 retries then fail). A retry succeeds. Candidate:
pre-bundle the provider plugin cache into the toolchain tarball so cold-create never reaches the
public registry.

## Deferred feature (documented in slack-notifications.md § Phase 119)

**Per-thread git-worktree isolation.** Parallel turns share `/workspace`; concurrent
repo-mutating turns can race. v1 ships conversational/read-mostly fan-out only (cap=1 for
mutation workloads). Per-thread worktree isolation is the documented follow-up.
