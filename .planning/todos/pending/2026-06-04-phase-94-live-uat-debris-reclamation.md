# Operator follow-up: Phase 94 live UAT — debris reclamation on the kph install

**Created:** 2026-06-04
**Source:** Phase 94 (km doctor leaked per-sandbox debris cleanup) — DBG-UAT, manual-only verification deferred at phase completion.
**Status:** pending operator action

Phase 94 shipped with 15/15 automated must-haves verified. The one remaining
requirement, DBG-UAT, is live reclamation against real AWS and cannot be mocked.

## Steps

```bash
# 1. Deploy the 94-05 root-cause prefix fix (TF modules + compiler).
#    Required because 94-05 changed budget-enforcer/github-token/create-handler
#    .tf modules — needs a full terragrunt apply, not --sidecars.
make build-lambdas            # clean rebuild so km init uploads fresh Lambda zips
AWS_PROFILE=klanker-application km init --dry-run=false

# 2. Dry-run report — confirm the new checks fire with real counts.
AWS_PROFILE=klanker-application km doctor

# 3. Reclaim + install guardrails.
AWS_PROFILE=klanker-application km doctor --dry-run=false \
    --delete-logs --delete-ddb-rows --set-log-retention --set-s3-lifecycle

# 4. Re-run to confirm clean.
AWS_PROFILE=klanker-application km doctor
```

## Expected

- ~271 orphaned CloudWatch log groups deleted (budget-enforcer, github-token-refresher,
  /km/sandboxes/, /km/sidecars/ families).
- Orphaned DynamoDB rows reclaimed across budgets / identities / slack-threads /
  status=failed sandboxes — **`BUDGET#ai#` rows preserved** (verify by scanning
  `{prefix}-budgets` after the purge).
- retentionInDays set on management + live-sandbox log groups.
- S3 lifecycle expiry rules installed on the artifacts bucket's transient prefixes.

## Notes

- The prefix fix is a no-op on the default `km` install; it only changes behavior
  for non-default-prefix installs (e.g. `kph`).
- Existing sandboxes keep their legacy `km-` log groups — those are reclaimed by the
  doctor `--delete-logs` both-names match, not retroactively renamed.
- Source-side leak is fixed only for NEW sandboxes created after the `km init` deploy.
