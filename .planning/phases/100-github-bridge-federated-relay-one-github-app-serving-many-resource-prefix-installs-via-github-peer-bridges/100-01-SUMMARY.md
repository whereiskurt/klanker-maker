---
phase: 100-github-bridge-federated-relay-one-github-app-serving-many-resource-prefix-installs-via-github-peer-bridges
plan: 01
subsystem: config + km init + terraform
tags: [github-bridge, federated-relay, config-plumbing, terragrunt, env-export]
requires: []
provides:
  - "cfg.Github.PeerBridges ([]string) — parsed github.peer_bridges config key"
  - "KM_GITHUB_PEER_BRIDGES env export (km init) with env-wins drift WARN"
  - "github_peer_bridges TF var + Lambda env entry (lambda-github-bridge v1.1.0)"
affects:
  - "Plan 02 (relayer) — consumes cfg.Github.PeerBridges / KM_GITHUB_PEER_BRIDGES"
  - "Plan 03 (reorder) — builds on the parsed peer list"
tech-stack:
  added: []
  patterns:
    - "viper UnmarshalKey single-struct decode (no per-field merge entry)"
    - "env-wins drift WARN on KM_* env/yaml mismatch (Phase 95 mirror)"
    - "additive in-place TF module edit (default empty preserves dormancy)"
key-files:
  created:
    - internal/app/cmd/github_peer_bridges_init_test.go
  modified:
    - internal/app/config/config.go
    - internal/app/config/config_test.go
    - internal/app/cmd/init.go
    - infra/modules/lambda-github-bridge/v1.1.0/variables.tf
    - infra/modules/lambda-github-bridge/v1.1.0/main.tf
    - infra/live/use1/lambda-github-bridge/terragrunt.hcl
decisions:
  - "NO new \"github.peer_bridges\" merge-list entry — the existing \"github\" entry + UnmarshalKey(\"github\",…) decode the whole block atomically; proven by TestLoadGithubPeerBridges_Set (RESEARCH Pitfall 2)"
  - "Edited lambda-github-bridge v1.1.0 in place (additive, default=\"\") rather than bumping to v1.2.0 — keeps the live source line untouched (RESEARCH Open Question 1)"
metrics:
  duration: 3m13s
  completed: 2026-06-08
  tasks: 4
  files: 7
---

# Phase 100 Plan 01: GitHub federated-relay config plumbing Summary

Plumbed the opt-in `github.peer_bridges: []string` config key end-to-end — struct field → `km init` env export (`KM_GITHUB_PEER_BRIDGES`, comma-joined, env-wins drift WARN) → terragrunt `get_env` → TF variable → Lambda env block — mirroring the shipped Slack Phase 95 pipeline, with ONE proven deviation: no redundant merge-list entry is added.

## What was built

- **`GithubConfig.PeerBridges []string`** (config.go) — decoded by the EXISTING `UnmarshalKey("github", &cfg.Github)` and the EXISTING `"github"` merge-list entry. No new merge entry, no new population block.
- **`KM_GITHUB_PEER_BRIDGES` export** (init.go `ExportTerragruntEnvVars`) — gated on `len(cfg.Github.PeerBridges) > 0`; `strings.Join(...,",")`; env-wins drift WARN on env/yaml mismatch; empty list leaves env unset (byte-identical to Phase 97/98). Co-located after the `KM_GITHUB_REPOS` block.
- **TF + terragrunt wiring** — `github_peer_bridges` TF var (default `""`) in `variables.tf`; `KM_GITHUB_PEER_BRIDGES = var.github_peer_bridges` in the `main.tf` environment block; `github_peer_bridges = get_env("KM_GITHUB_PEER_BRIDGES", "")` in the live terragrunt unit's inputs. Module v1.1.0 edited in place; `source = .../v1.1.0` unchanged.

## Tasks

| Task | Name | Commit | Files |
| ---- | ---- | ------ | ----- |
| 1 | RED config round-trip test | f1cf371b | config_test.go |
| 2 | GREEN GithubConfig.PeerBridges field | 9a169acb | config.go |
| 3 | init.go KM_GITHUB_PEER_BRIDGES export + drift WARN (RED→GREEN) | 17c72841 | github_peer_bridges_init_test.go, init.go |
| 4 | terragrunt + TF module wiring | b3ca48b5 | variables.tf, main.tf, terragrunt.hcl |

## Verification

- `go test ./internal/app/config/... -run GithubPeerBridges -count=1` — GREEN (Set + Absent)
- `go test ./internal/app/cmd/... -run GithubPeerBridges -count=1` — GREEN (Set / Absent / DriftWarn / NoOverwrite)
- Merge-list block (config.go:525-560) contains 0 `"github.peer_bridges"` entries — RESEARCH Pitfall 2 honored
- `terraform fmt -check` clean; `github_peer_bridges` + `KM_GITHUB_PEER_BRIDGES` present in module + unit; `source = .../v1.1.0` unchanged
- `make build` succeeds (km v0.4.880)

## Deviations from Plan

None — plan executed exactly as written. The one intentional design deviation (no `"github.peer_bridges"` merge-list entry) was specified by the plan/RESEARCH and is proven by `TestLoadGithubPeerBridges_Set` passing with only a struct field added.

Note: a linter auto-reformatted pre-existing whitespace in `infra/modules/lambda-github-bridge/v1.1.0/main.tf` (IAM policy block indentation, unrelated to the env-var change) during Task 4; `terraform fmt -check` is now clean as a side effect. This was not a code change I authored and required no action.

## Dormancy invariant

Absent `github.peer_bridges` ⇒ `cfg.Github.PeerBridges` nil ⇒ `KM_GITHUB_PEER_BRIDGES` not exported ⇒ TF default `""` ⇒ byte-identical init.go env surface + Lambda behavior to Phase 97/98.

## Self-Check: PASSED

All 8 key files exist; all 4 task commits present in history.
