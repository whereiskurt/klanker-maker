---
phase: 84.2-km-init-plan-flag-and-destroy-class-gate
status: planned
date_created: 2026-05-16
depends_on: 84.1
---

# Phase 84.2 — `km init --plan` flag with destroy-class gate

**Status:** Planned. Awaiting Phase 84.1 completion before plan-phase (depends on `ExportTerragruntEnvVars` helper from 84.1-01).

## Design

Full design spec: [`docs/superpowers/specs/2026-05-16-km-init-plan-flag-and-destroy-class-gate-design.md`](../../../docs/superpowers/specs/2026-05-16-km-init-plan-flag-and-destroy-class-gate-design.md)

The spec covers:
- Problem statement (Phase 84 incident → no plan-before-apply visibility)
- 6 design decisions (flag shape, scope, gate, bootstrap parity, output, no config file)
- Architecture (files touched, data flow for `km init --plan` and `km bootstrap --shared-ses --plan`)
- Protected resource types list with per-entry incident rationale
- Gate algorithm + trip output format + override semantics
- Error handling matrix
- Testing strategy (unit + operator UAT)
- Non-goals + open questions + dependencies

## Roadmap entry

See `.planning/ROADMAP.md` § "Phase 84.2: `km init --plan` flag with destroy-class gate (INSERTED)".

## Next step

After Phase 84.1 closes (specifically after 84.1-01 ships the `ExportTerragruntEnvVars` helper), run:

```
/gsd:plan-phase 84.2
```

…to generate the wave-broken PLAN.md files from the spec.

## Origin

Brainstormed 2026-05-16 during Phase 84.1 execution, after the operator's `--dry-run=true` ran during a Phase 84 retrospective and confirmed it prints no actual terragrunt plan output. Inline conversation in `.claude/` session log; design captured in the spec linked above.
