# Self-healing provider locks in `km init`

**Date:** 2026-05-27
**Status:** Approved (design)

## Problem

Upgrading an old install (observed on km 0.2.x) is painful. After the binary is
upgraded, the regional Terraform modules still carry stale `.terraform.lock.hcl`
files whose provider versions/checksums predate the current pins in
`infra/live/root.hcl` (`aws 6.46.0`, `tls 4.3.0`). `km init`'s implicit
auto-init (during `Apply`) respects the existing lock and refuses to proceed
with a version-constraint / checksum mismatch. The operator must manually run
`terragrunt init -reconfigure -upgrade` (per-module, or a `run --all` sweep) to
move the lock forward before `km init` will work.

This was confirmed against the live code and the operator's report ("I used the
upgrade and reconfigure options. It need to move a lock version on").

### What the original proposal got wrong

The proposal was to add `-upgrade` to the shared
`pkg/terragrunt/runner.go` `Reconfigure` method, on the belief that
`Reconfigure` "already runs per-module before every plan/apply" at
`init.go:996`. The code disagrees on two points:

- `init.go:994` gates that `Reconfigure` call to `if mod.name == "ses"` only — a
  Phase-84 special case for the ses v1.0.0→v2.0.0 source change. The other
  regional modules get only terragrunt's implicit auto-init (no `-upgrade`). So
  adding `-upgrade` to the shared method would **not** heal the other modules
  during `km init` — it wouldn't fix the reported pain.
- `Reconfigure` has four callers; two (`uninit.go:434`, `cluster.go:580`) run it
  immediately before **destroy**. Adding `-upgrade` to the shared method would
  bleed an upgrade into both teardown paths.

The pins are exact (`6.46.0`, `4.3.0`), so the original "won't float to latest"
reasoning is correct — `-upgrade` resolves to the pinned versions. Only the
*targeting* was off.

## Goal

`km init` (both the apply loop and the `--plan` preview) self-heals stale
provider locks by running `terragrunt init -reconfigure -upgrade` per module,
moving each module's lock forward to root.hcl's pins automatically. No manual
sweep required to upgrade an old install.

## Approach (selected)

**Always upgrade, per-module.** Add a dedicated runner method that runs
`init -reconfigure -upgrade`, and call it before every regional module's apply
and plan in `km init`. Destroy paths are untouched.

Approaches considered and rejected:

- **Heal-on-failure retry** (plain init; on a lock-mismatch failure, auto
  `-upgrade` and retry once): zero cost when healthy, but requires matching
  terraform's error text, which is brittle across tf versions. More code.
- **Opt-in `km init --upgrade` flag**: preserves drift visibility and per-run
  cost, but the operator has to *know* to run it — defeats "self-healing."
- **Original "add `-upgrade` to shared `Reconfigure`"**: wrong targeting (see
  above).

## Changes

### 1. `pkg/terragrunt/runner.go` — new `ReconfigureUpgrade` method

Sibling to the existing `Reconfigure`:

```go
// ReconfigureUpgrade runs `terragrunt init -reconfigure -upgrade` inside
// sandboxDir. Like Reconfigure it refreshes backend metadata; additionally
// -upgrade moves .terraform.lock.hcl forward to the provider versions pinned in
// root.hcl, healing stale locks left by an older km install (e.g. upgrading a
// 0.2.x box). root.hcl pins exact versions (aws 6.46.0, tls 4.3.0), so -upgrade
// resolves to those exact versions and never floats to the registry's latest.
//
// Safe to call when no upgrade is needed — it re-resolves to the same pinned
// versions and rewrites an already-correct lock as a no-op.
func (r *Runner) ReconfigureUpgrade(ctx context.Context, sandboxDir string) error {
    cmd := r.buildCommand(ctx, sandboxDir, "init", "-reconfigure", "-upgrade")
    return r.runCommand(ctx, cmd)
}
```

### 2. `internal/app/cmd/init.go` — interface + apply loop

- Add `ReconfigureUpgrade(ctx context.Context, dir string) error` to the
  `InitRunner` interface (line 49–56).
- In the apply loop (954–1004): **replace** the `if mod.name == "ses"`
  `Reconfigure` block with a **universal** `ReconfigureUpgrade` call run before
  *every* module's `Apply`, bounded by the existing `reconfigureTimeout`
  (2 min). The universal `-reconfigure -upgrade` subsumes the ses-only case
  (it covers both the ses v1→v2 backend-cache re-init *and* lock drift). The
  ses preflight (`InitSESPreflight`, line 979) stays. The error message is
  broadened to name both lock-upgrade and the existing state-digest recovery
  pointer.

### 3. `internal/app/cmd/init.go` — `--plan` path

In `planModule` (line 1311), run `ReconfigureUpgrade` before
`PlanWithOutput`, bounded by the per-module timeout already in scope. This makes
`km init --plan` work on a stale-lock box (otherwise the operator hits the lock
block at plan time and can't even preview). `-upgrade` only writes the local
`.terraform.lock.hcl`, not infrastructure, so it is safe inside a read-only
preview.

## Out of scope

Per the "init only" decision:

- `internal/app/cmd/bootstrap.go` foundation apply (`defaultApplyTerragrunt`,
  Reconfigure at 1312) — stays on plain `Reconfigure`. Foundation modules
  (SCP/KMS/artifacts) rarely change; revisit if a foundation upgrade hits the
  same block.
- `internal/app/cmd/uninit.go` (434) and `internal/app/cmd/cluster.go` (580)
  destroy paths — stay on plain `Reconfigure`. `-upgrade` before destroy is
  semantically odd and was explicitly excluded.
- No new `km doctor` lock-drift check.

## Testing (TDD)

Write failing tests first.

- Extend `mockRunner` (init_test.go:25) with a recording `ReconfigureUpgrade`
  (capture dirs in order, mirroring `applied`).
- Apply path: assert `ReconfigureUpgrade` fires once per applied module and
  *before* that module's `Apply` (ordering), for all modules — not just ses.
- Plan path: assert `ReconfigureUpgrade` fires before `PlanWithOutput` for each
  module (mockPlanRunner, init_plan_test.go).
- Update/replace the existing ses-only reconfigure assertion to reflect the
  universal behavior.
- Confirm `*terragrunt.Runner` satisfies the extended `InitRunner` (compile-time).

## Accepted tradeoffs

- A registry round-trip + a few seconds per module on **every** `km init` /
  `km init --plan`, even on healthy installs (warm with `plugin_cache_dir`).
- Lock drift becomes invisible — a root.hcl pin bump won't be noticed until the
  lock changes on the next init.
