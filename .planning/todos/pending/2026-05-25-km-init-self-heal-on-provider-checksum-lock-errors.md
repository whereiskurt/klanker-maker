---
created: 2026-05-25T01:29:45.674Z
title: km init self-heal on provider checksum lock errors
area: cli
files:
  - pkg/terragrunt/runner.go
  - internal/app/cmd/init.go
  - infra/live/root.hcl:34-43
---

## Problem

`km init --dry-run=false` periodically aborts mid-apply with:

```
Error: Required plugins are not installed
  - registry.terraform.io/hashicorp/aws X.Y.Z (in .terraform/providers)
    does not match any of the checksums recorded in the dependency lock file
```

Root cause (operator-workstation-only): operators run a shared
`plugin_cache_dir` (`~/.terraform.d/plugin-cache`, set in `~/.terraformrc`).
A plugin cache forces Terraform to verify providers by `h1:` (unpacked-dir)
hash only — it no longer has the original zip for `zh:` verification. Because
`.terraform.lock.hcl` is gitignored (regenerated per-machine) and the provider
constraint floated, a module's recorded `h1:` set can drift out of sync with
the package the cache extracted. `terragrunt apply -auto-approve` does **not**
re-resolve providers, so it can't reconcile and fails — blocking the whole
`km init`.

The management Lambda is unaffected: it has no plugin cache, so it verifies
against `zh:` hashes (platform-complete) and never hits this.

Manual remediation (done by hand 2026-05-24, took one pass over 19 modules):

```bash
terragrunt run --all --queue-exclude-dir 'sandboxes/**' init -- -upgrade
```

## Solution

In km's init / terragrunt runner (`pkg/terragrunt/runner.go`, driven by
`internal/app/cmd/init.go`), detect this error class per-module and auto-retry
that module's `init` with `-upgrade` before failing the run.

- Prefer a **targeted retry on the specific error** ("Required plugins are not
  installed" / "does not match any of the checksums recorded") over a blanket
  `-upgrade` on every init — blanket `-upgrade` would silently float provider
  versions on routine inits.
- Retry once with `-upgrade`; if it still fails, surface the original error.
- Consider emitting a one-line WARN so the operator knows a lock was healed.

### Related mitigations already applied (2026-05-24)

These reduce frequency but don't remove the failure mode (locks are still
gitignored + plugin cache still in play), so the self-heal is still wanted:

- Pinned `aws = "6.46.0"` / `tls = "4.3.0"` in `infra/live/root.hcl`
  `generate "provider"` block (was `>= 5.0` / `>= 4.0`) — stops floating.
- Deleted 3 stale single-platform committed locks that violated the repo's
  gitignore policy: `ecs-spot-handler`, `s3-replication`, `ses/v1.0.0`.

### Alternatives considered (rejected for now)

- Commit multi-platform locks (`terraform providers lock -platform=...`):
  canonical Terraform fix, but high operational overhead in a terragrunt repo.
- Drop the plugin cache (`plugin_cache_dir`): kills the error class entirely
  but costs disk/bandwidth per module. Operator declined.
