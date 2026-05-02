---
created: 2026-05-02T04:01:16.448Z
title: km doctor stale SSM parameter cleanup
area: cli
files:
  - internal/app/cmd/doctor.go
phase_hint: 64 (CLEAN-6)
---

## Problem

`km doctor` already detects stale resources via the established `--dry-run=true` lists / `--dry-run=false` deletes pattern (Stale AMIs, Stale IAM Roles, Stale Schedules, Stale KMS Keys, Stale Slack channels). SSM Parameter Store entries don't have an equivalent check — orphans accumulate from failed `km destroy` runs and from sandboxes provisioned before identity tracking was robust.

Operator noticed the buildup during Phase 65 UAT (2026-05-02). The strongest candidate path is `/sandbox/{id}/*` where `{id}` is no longer present in the `km-sandboxes` DynamoDB table — those are typically Ed25519 signing keys at `/sandbox/{id}/signing-key` (KMS-encrypted SecureString). Lower-priority candidates: `/km/config/github/installations/*` for GitHub App installations that no longer exist on the GitHub side.

## Solution

Add a new doctor check `checkStaleSSMParameters` mirroring the existing stale-X pattern:

1. **Dry-run (default):** scan `/sandbox/*` via `ssm.GetParametersByPath`, group by sandbox ID, list any sandbox IDs whose row is absent from the `km-sandboxes` DynamoDB table. Print count, list of orphan IDs (or first ~10 with "and N more"), and the parameter paths under each. Severity: WARN (mirrors Stale IAM Roles / Stale Schedules).
2. **`--dry-run=false`:** delete each orphan parameter via `ssm.DeleteParameter`, log per-deletion.

### Safety considerations

- Per-sandbox SSM entries are mostly Ed25519 signing keys. Deletion is irreversible (KMS-encrypted SecureString, no undelete). Once gone, historical emails signed by that sandbox cannot be re-verified by anyone re-running `km email read`. Acceptable for stale sandboxes (the receipts likely don't matter), but the dry-run output should explicitly call out "deleting these means signature verification of historical emails by these sandboxes will fail."
- The "stale" criterion must be: **DynamoDB authoritatively says the sandbox does not exist.** Don't infer staleness from EC2 instance state — a paused/stopped sandbox still has a DynamoDB row.
- Don't touch `/km/slack/*`, `/km/config/remote-create/*`, `/sandbox/operator/*` — those are platform-level, not per-sandbox.

### Lower-priority extension

`/km/config/github/installations/{owner}` — would require calling the GitHub API to confirm the App is still installed for that owner. More moving parts; leave for a follow-up unless the operator says it's also accumulating.

### Phase landing

Fits Phase 64's km create reliability and doctor cleanup hardening as **CLEAN-6**, alongside CLEAN-2 (`km doctor --auto-fix` for orphan resources). Could share infrastructure with CLEAN-2's auto-fix path so operators get a single switch.

### Test pattern

Mirror `doctor_stale_ami_test.go` / `doctor_test.go` patterns: stub `ssmClient.GetParametersByPath` + `dynamoClient.GetItem` and assert the orphan-detection logic. Don't hit live AWS in unit tests.
