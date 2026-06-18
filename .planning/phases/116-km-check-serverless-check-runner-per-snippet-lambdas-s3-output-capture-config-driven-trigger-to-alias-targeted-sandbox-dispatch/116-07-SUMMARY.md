# 116-07 SUMMARY — doctor check group + github.events pre-filter + example checks + docs

**Plan:** 116-07
**Status:** complete (executor overflowed context before writing SUMMARY/committing examples; orchestrator finished the wrap-up)
**Tasks:** 4/4

## What shipped

1. **`km doctor` check group** (`internal/app/cmd/doctor.go`): `GetChecksTableName()` adapter + a check-group that reports the `{prefix}-checks` table, orphan `{prefix}-check-*` Lambdas not in the DDB table, EventBridge Scheduler entries referencing missing check Lambdas, and per-check `KM_CHECK_TRIGGER` drift (baked env vs current config, reusing `pkg/check` bake + source_hash). Dormant: skips silently when no checks/table exist.
2. **`github.events` `check:` pre-filter** (additive, dormant-by-default):
   - `GithubEventRule.Check string` (`config.go:191`, yaml `check`). Absent ⇒ byte-identical to Phase 115.
   - `pkg/github/bridge/interfaces.go` `CheckInvoker.InvokeCheck(ctx, name, payload) (triggered bool, err error)`; `handleEventRoute` (`webhook_handler.go:795+`) synchronously invokes `{prefix}-check-<name>` before dispatch when `rule.Check != "" && CheckInvoker != nil`, and gates dispatch on `triggered`. **Fail-CLOSED** on invoke error (documented in `event_router.go:61`).
   - Bootstrap was NOT modified — 116-04's `handler` already returns `{"triggered","reason","output_key"}` on every path, so the contract held with byte-identity intact.
3. **Example checks** (`profiles/checks/`):
   - `qotd/snippet.py` (+ `requirements.txt`) — stdlib QOTD internet fetch → JSON.
   - `wiz-intel/snippet.py` — simulated Wiz advisories + affected-system counts → JSON; `checks.triggers.example.yaml` with a threshold `when_py`, `alias`, `on_absent`/`cooldown_seconds` (snake_case per the 116-03 viper finding).
4. **Docs** — `docs/check-runner.md` operator runbook (deploy/run/schedule/sync/rm, when_py/@file model, zip vs `--image`, open-egress posture, doctor checks, examples).

## Commits
- `39372f1b` feat(116-07): km doctor check group — table/orphan-Lambda/schedule/drift checks
- `579ca8f4` feat(116-07): github.events check: pre-filter gate — synchronous invoke before dispatch
- `7d5755e9` feat(116-07): QOTD + Wiz-intel example checks + operator runbook + gitignore py caches

## Verification
- `go build ./...` — EXIT 0.
- `go test ./internal/app/config/ ./pkg/check/... ./pkg/github/...` — EXIT 0 (all `ok`).
- `go test ./internal/app/cmd/ -run 'Check|Doctor'` (new tests isolated) — EXIT 0, 6.0s.
- Bootstrap copies byte-identical (`AssertBootstrapByteIdentity` guard intact).
- Example snippets parse (`ast.parse` qotd + wiz OK); pytest bootstrap 3/3 green.
- Full `internal/app/cmd` suite: the 300s run timed out cumulatively (suite grew); re-run at the VALIDATION-specified 600s — see phase-level gate.

## Deviations / notes
- Executor died with "Prompt is too long" after committing tasks 1–2; orchestrator committed the (already-authored) examples + docs, added a `__pycache__`/`*.pyc` `.gitignore` entry (first Python in repo), and wrote this SUMMARY.
- `rm -rf` for pycache cleanup tripped the operator's `~/.zshrc` `rm()` guard (memory `feedback_rm_guard_use_find_delete`); redone with `find -delete`/`find -exec`.
