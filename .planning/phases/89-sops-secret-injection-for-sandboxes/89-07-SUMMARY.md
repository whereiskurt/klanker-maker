---
phase: 89-sops-secret-injection-for-sandboxes
plan: 07
subsystem: testing
tags: [sops, kms, codex, openai, mitm, uat, secret-injection]

requires:
  - phase: 89-01..89-06
    provides: schema, KMS module, bootstrap, compiler/create/destroy plumbing, doctor + docs
provides:
  - Live end-to-end UAT proof of declarative SOPS secret injection
  - Phase 89 × Phase 88 metering composition proven (codex api.openai.com call metered)
  - 8 hot-fix commits closing real gaps surfaced only under live AWS conditions
affects: [codex-parity, phase-70, phase-88, ttl-handler, s3-artifacts-lifecycle]

tech-stack:
  added: []
  patterns:
    - "Remote-create artifact bridge: operator uploads SOPS bundle to remote-create prefix; create-handler rewrites profile to abs /tmp path"
    - "codex behind MITM: custom provider + supports_websockets=false forces HTTP POST /v1/responses"

key-files:
  created:
    - .planning/phases/89-sops-secret-injection-for-sandboxes/89-07-UAT.md
  modified:
    - profiles/codex.yaml
    - profiles/learn.v2.codex.yaml
    - internal/app/cmd/create.go
    - internal/app/cmd/agent.go
    - cmd/create-handler/main.go
    - cmd/ttl-handler/main.go
    - infra/modules/sandbox-secrets-key/v1.0.0/main.tf
    - infra/live/use1/s3-artifacts-lifecycle/terragrunt.hcl

key-decisions:
  - "UAT executed against real AWS (km prefix, account 052251888500, us-east-1) — surfaced 8 bugs no offline test caught"
  - "Scenario 4 PASS: metering proven via a real codex api.openai.com call (BUDGET#ai#gpt-4o-mini = $0.0014), not just raw curl"
  - "codex 0.133 websocket transport is fundamentally incompatible with the MITM proxy; supports_websockets=false on a custom provider is the durable fix"
  - "Open items (km agent run ignores spec.cli.agent; codex model default) are Phase 70 / codex-parity follow-ups, not Phase 89 scope"

patterns-established:
  - "Secret-bearing files consumed by the sandbox user must be 0440 root:sandbox, not 0400 root:root"
  - "S3 objects uploaded outside terraform (PutObject) must be cleaned on BOTH local (destroy.go) and remote (ttl-handler) destroy paths, with the lifecycle rule as backstop"
---

# 89-07: Live UAT — SOPS Secret Injection End-to-End

## Outcome: PASS

Phase 89's deliverable — declarative SOPS secret injection for sandboxes — is
proven end-to-end through the standard remote-create path. The Phase 89 × Phase
88 composition is proven: a real codex `api.openai.com` call, authenticated with
the SOPS-injected `OPENAI_API_KEY`, was metered by the http-proxy MITM into
`BUDGET#ai#gpt-4o-mini-2024-07-18` (spentUSD 0.0014127) with no out-of-band
operator wiring beyond `km create`.

## What the UAT proved

1. `km bootstrap --shared-secrets-key` provisions the per-install KMS key (after the policy fix).
2. Operator encrypts a bundle with `sops --kms <alias>`; `km validate` accepts the profile.
3. `km create` (remote dispatch) uploads the bundle, the Lambda bridges it, userdata decrypts at boot.
4. The decrypted `OPENAI_API_KEY` is exposed to the sandbox user (login shell + `km agent run` env).
5. codex (with the MITM-compatible config) makes a real metered OpenAI call.

## Bugs found + fixed (8 commits)

See `89-07-UAT.md` § "Bugs found + fixed" for the full table. Spans Phase 89
(KMS policy, bundle bridge, abs-path, file perms, agent sourcing, lifecycle
wiring, ttl-handler cleanup) and codex-parity (websocket-vs-MITM config).

## Deploy note

Commits 961fe3d, f3f6a16, 5fc7aca, 68b9188 require `km init` (+ `--sidecars` /
`--lambdas`) to land on the ttl-handler Lambda and apply the lifecycle rule.
The UAT proof used live-patched boxes for the not-yet-deployed fixes.

## Open follow-ups (NOT Phase 89)

1. `km agent run` ignores `spec.cli.agent` (agent.go:215 defaults to claude; needs `--codex`). Phase 70 defect.
2. codex profiles default to `model = "gpt-4o-mini"`; operators may prefer a codex-tuned model.
3. `profiles/codex.yaml` `hibernation: true` + `spot: true` requires `--on-demand` (pre-existing).
