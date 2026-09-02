<!--
  MAINTAINER NOTE — update this file BEFORE tagging each release.

  Its contents are injected VERBATIM into every GitHub release's notes,
  between the install header and goreleaser's auto-generated changelog.
  Wiring: .github/workflows/release.yml ("Load release highlights" step) →
  $KM_RELEASE_HIGHLIGHTS → .goreleaser.yaml `release.header` template.

  Keep it to the few MAJOR, human-curated additions for THIS release (the
  auto-changelog already lists every commit). HTML comments like this one
  are hidden in GitHub's rendered view. If this file is empty/absent the
  section is omitted gracefully.
-->

## 🛑 A sandbox could be stopped as "idle" while you were working in it

An interactive desktop sandbox was stopped 2 minutes after its last presence heartbeat,
against a 30-minute `idleTimeout`. Nothing about the sandbox was idle.

The idle detector polls the sandbox's CloudWatch audit stream for its most recent event.
When that read failed — or returned one of the empty pages `GetLogEvents` is documented to
return even for a stream that has events — it fell back to *"has the detector been running
longer than the timeout?"*, measured from detector start. So once a box had been up longer
than its own idle window, **a single bad poll fired the reap**, no matter how recent the
real activity. `OnIdle` is one-shot, so there was no second look, and the failed read was
swallowed with no log line, which is why nothing on the box explained it afterwards.

A degraded read is now the absence of evidence, not evidence of absence: the detector
remembers the newest event it has actually seen and uses that as the baseline when a poll
goes bad. Only a detector that has *never* observed an event falls back to its start time,
so a sandbox that generates nothing at all is still reaped and nothing leaks. Failed and
empty reads are now logged.

## 🔐 Three DynamoDB tables the ttl-handler was never granted

Found while tracing the stop above. The Lambda is handed four table names as environment
variables and had IAM for one and a half of them:

- **`{prefix}-budgets`** — `RecordPauseStart` has been 403ing on every stop, pause and
  idle-stop since it shipped. Its error is a `log.Warn "(non-fatal)"`, so the only symptom
  was compute budgets quietly counting paused time as running time. `RecordResumeClose`
  (resume) and `handleBudgetAdd` (`km at … budget-add`) were denied too.
- **`{prefix}-schedules`** — `handleCreate` records a scheduled `km at <time> create` here
  so it shows in `km at list`. The call discarded its own error, so the schedule fired
  correctly and was simply invisible.

`handleBudgetAdd` was broken twice over: past the missing grant it hand-rolled an
`UpdateItem` against key `sandbox_id`/`sk` and attributes `compute_limit`/`ai_limit` — none
of which exist on that table, whose key schema is `PK`/`SK` with the limits on
`SK=BUDGET#limits`. It now goes through the shared `AddBudgetLimits` helper, and a test
pins the item shape so a second copy cannot drift again.

A guard test now compares the set of tables the module hands the Lambda as environment
against the set named in its IAM resource ARNs, so the next table added is covered without
anyone remembering to.

## 📋 Upgrading

`make build` + `make build-lambdas` + `km init --dry-run=false`. Not `--sidecars` — the two
new IAM policies need a full terragrunt apply. The idle-detector fix rides in the
`km-audit-log` sidecar, which is fetched at boot, so **existing sandboxes keep the old
detector until `km destroy && km create`**. The IAM and ttl-handler fixes take effect
immediately for every sandbox, new or existing.
