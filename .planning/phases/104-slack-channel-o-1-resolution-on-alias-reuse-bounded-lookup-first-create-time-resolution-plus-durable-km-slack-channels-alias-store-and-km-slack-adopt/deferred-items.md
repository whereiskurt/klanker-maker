# Deferred Items (out of scope for Phase 104)

## Pre-existing pkg/hygiene TestGoSourceNamesUseResourcePrefix failure

5 hardcoded `km-` name-construction sites flagged by the hygiene test (pre-existing, not introduced by Phase 104):
- `internal/app/cmd/doctor_artifacts.go:351`: hardcoded `"km-doctor-expire-"` 
- `internal/app/cmd/doctor_log_groups.go:62`: hardcoded `"/aws/lambda/km-budget-enforcer-"`
- `internal/app/cmd/doctor_log_groups.go:68`: hardcoded `"/aws/lambda/km-github-token-refresher-"`
- `internal/app/cmd/doctor_log_groups.go:74`: hardcoded `"/km/sandboxes/"`
- `internal/app/cmd/doctor_log_groups.go:80`: hardcoded `"/km/sidecars/"`

Fix: add `allowlist.txt` entries (if genuinely benign) OR derive from `resourcePrefix()` / `cfg.GetResourcePrefix()`.
Confirmed pre-existing: same failure present before any Phase 104 changes (verified via `git stash` test).
