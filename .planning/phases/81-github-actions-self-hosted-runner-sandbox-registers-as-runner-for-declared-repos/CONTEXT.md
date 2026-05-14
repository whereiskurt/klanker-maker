# Phase 81 — Context for planning

## Design spec (authoritative)

**Read first:** `docs/superpowers/specs/2026-05-13-actions-runner-design.md`

The spec was developed via /superpowers:brainstorming on 2026-05-13. It covers:

- Profile schema (`spec.sourceAccess.github.runner` block with `enabled`, `repos`, `labels`, `runnerGroup`, `maxRepos`)
- Architecture: new token Lambda + sandbox-side sidecar + two systemd template units per repo (`klanker-actions-runner-register@.service` + `klanker-actions-runner@.service`)
- Data flows for boot-time registration, km destroy teardown (belt-and-suspenders DELETE), and `km runner reattach` recovery
- IAM design (Lambda role + per-sandbox `lambda:InvokeFunctionUrl` grant)
- Network allowlist auto-injection in `pkg/compiler/`
- `km runner` CLI surface (status, reattach, detach)
- Four new `km doctor` checks
- Migration story matching Phase 63/67/68/73/79/80 pattern (make build && make sidecars && km init --sidecars && km init)

## Resolved design decisions (don't re-litigate)

- **Scope:** repo-level only. Org/enterprise scope is non-goal for v1.
- **Lifecycle:** long-lived persistent runner. Not JIT, not pooled, not webhook-driven.
- **Multi-repo:** supported. Subset rule: `runner.repos ⊆ allowedRepos` (with wildcard expansion). Default `maxRepos: 5`.
- **Failure model:** per-repo registration is best-effort, NOT transactional. `km create` succeeds even if all registrations fail; sandbox boots with failed systemd units. Operator runs `km runner reattach <sandbox>` to retry.
- **Profile mutability:** immutable. Adding a repo to `runner.repos` requires `km destroy && km create`.
- **`km runner status` polling:** DDB by default, `--refresh` flag for live GitHub API.
- **Default labels** (always applied, not removable): `self-hosted`, `klanker`, `klanker-<sandbox-id>`, `klanker-<alias>` (if set). User labels appended.
- **Substrate:** EC2 only for v1. Docker out of scope (no systemd).
- **GitHub App permission expansion:** operators must re-install App with `administration: write` added. Doctor check enforces.

## Reference: defcon.run.34/infra

`~/working/defcon.run.34/infra` has a working self-hosted runner-adjacent setup. Patterns to draw from when planning:

- `terraform/modules/github-oidc/v1.0.0/main.tf` — Terraform module shape under `modules/<name>/v1.0.0/` (matches klanker's existing convention)
- `terraform/modules/github-oidc/v1.0.0/variables.tf` lines around `ec2_runner_instance_profile` — IAM instance-profile pattern for self-hosted EC2 runners with SSM access + ECR read
- `terraform/live/site/site.hcl` — variable wiring for the runner instance profile

Note: defcon.run.34 uses GitHub OIDC federation (GitHub-hosted runners assume AWS roles), which is a *different* primitive from what Phase 81 builds (self-hosted runners on klanker sandboxes pulling jobs from GitHub). The IAM/instance-profile shape and Terragrunt module layout are transferable; the OIDC trust policy is not relevant for Phase 81.

## Suggested plan-phase decomposition (non-binding)

The spec's scope is large enough that the planner may want to break Phase 81 into 4–5 task groups within the phase. Suggested split:

1. **Profile schema + validation** — `pkg/profile/types.go`, `pkg/profile/validate.go`, unit tests
2. **Token Lambda + Terraform module** — `cmd/km-actions-runner-token/`, `pkg/github/runner.go`, `infra/modules/actions-runner-token/v1.0.0/`, IAM, Lambda Function URL with sigv4 auth
3. **Sandbox-side sidecar + systemd units** — `sidecars/km-actions-runner/`, `klanker-actions-runner-register@.service`, `klanker-actions-runner@.service`, `klanker-actions-runner-shutdown.service`, userdata wiring in `pkg/compiler/`, network allowlist auto-injection
4. **`km runner` CLI + km destroy teardown integration** — `internal/app/cmd/runner.go`, belt-and-suspenders DELETE in destroy path
5. **Doctor checks + docs + integration test** — `actions_runner_app_perms`, `actions_runner_register_failures`, `actions_runner_ghosts`, `actions_runner_drift`; update `docs/github.md`; manual E2E against test repo

This is a planner suggestion only — `/gsd:plan-phase 81` should make its own call.

## Pre-planning checklist

Before /gsd:plan-phase 81:

- [ ] Confirm GitHub App is re-installed with `administration: write` (or note that operator will do this as part of phase rollout)
- [ ] Verify multi-instance support story still holds (resource_prefix scoping in SSM paths + DDB table names)
- [ ] Confirm existing presence daemon (Phase 79) is the right place to add the sixth runner-process signal, or whether that's its own task
