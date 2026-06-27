---
created: 2026-06-25T00:00:00.000Z
title: Lean top-level — reduce the number of top-level repo folders
area: repo-hygiene
files:
  - schemas/ (1 file — fold into pkg/profile/ or docs/)
  - containers/ (3 files)
  - test/ (5 files — e2e; consider folding under a single tests home)
  - scripts/ (7 files)
  - RELEASE-NOTES-*-DRAFT.md (loose root file)
---

## Problem

Operator's larger goal (stated 2026-06-25 alongside Phase 120): make the
repo's top-level landing as lean as possible — reduce the count of top-level
folders. Today there are 16 tracked top-level dirs + ~20 loose root files.

Phase 120 (profiles reset) is a *within-folder* cleanup and does NOT shrink the
top level by itself (both `profiles/` and `testdata/` already exist and stay).
This todo tracks the actual top-level reduction as its own phase.

## Candidate moves (by risk)

**Safe / low-churn (no Go import or terragrunt path impact):**
- `schemas/` (1 file) → fold into `pkg/profile/` (near the JSON schema it pairs
  with) or `docs/`.
- `containers/` (3) → evaluate folding under `sidecars/` or `infra/`.
- `test/` (5, e2e) → consider a single tests home or move under the package
  they exercise.
- `scripts/` (7) → keep, or fold into `tools/`/`hack/` only if it nets a dir.
- loose `RELEASE-NOTES-v0.5.7-DRAFT.md` and other transient root files → move
  into `.planning/` or `docs/` (or delete drafts once released).

**Risky / load-bearing — likely OUT of scope (repo-wide churn):**
- `pkg/` `internal/` `infra/` `cmd/` — moving these rewrites Go import paths
  and terragrunt source paths across the whole repo; high blast radius, low
  reward. Leave unless there's a strong reason.
- `.planning/` (GSD home), `docs/`, `sidecars/`, `skills/` — structural, keep.

## Approach

Treat as a dedicated follow-on phase after Phase 120. Each candidate move needs
a `grep` for hard-coded path references (Go strings, Makefile, .goreleaser,
docs, skills, terragrunt) and a green `go test ./... && make build` gate.
Net goal: fewer top-level dirs, zero functional change.

## Why deferred from Phase 120

Phase 120 is `make build`-only and contract-preserving (byte-identity intact).
Folding top-level dirs is a different risk class (path references across the
codebase) and deserves its own scoped phase rather than ballooning a clean
profiles change.
