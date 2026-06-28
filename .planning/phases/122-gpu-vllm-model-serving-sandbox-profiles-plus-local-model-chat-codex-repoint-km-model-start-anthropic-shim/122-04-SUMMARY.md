---
phase: 122-gpu-vllm-model-serving-sandbox-profiles-plus-local-model-chat-codex-repoint-km-model-start-anthropic-shim
plan: "04"
subsystem: operator-cli
tags: [km-model, bifrost, port-forward, anthropic-shim, gpu-serving, phase-122]
dependency_graph:
  requires: [122-01]
  provides: [km-model-start, km-model-status, httpTunnelProbe]
  affects: [internal/app/cmd/root.go, internal/app/cmd/shell.go]
tech_stack:
  added: []
  patterns: [DI-injectable-cobra-cmd, runReconnectingPortForward, tunnelProbe]
key_files:
  created:
    - internal/app/cmd/model.go
  modified:
    - internal/app/cmd/shell.go
    - internal/app/cmd/root.go
    - internal/app/cmd/model_test.go
decisions:
  - "Bifrost :8001 is the single forwarded port for both Codex (OpenAI/Responses) and Claude Code (Anthropic /anthropic path) — --anthropic is semantic alias not a different port"
  - "httpTunnelProbe uses plain http.Client (no TLS) mirroring httpsTunnelProbe pattern"
  - "runModelStart has no SSM pre-flight unlike vscode/desktop — Bifrost has no per-sandbox credential file to verify"
  - "parseModelStatus checks both vllm AND bifrost as independent systemd units — allows targeted error messages"
metrics:
  duration: "~10 minutes"
  completed: "2026-06-27"
  tasks_completed: 3
  files_modified: 4
---

# Phase 122 Plan 04: km model start / status — Bifrost gateway port-forward

**One-liner:** `km model start` SSM port-forward to Bifrost :8001 with auto-reconnect, plain-HTTP liveness probe, and Claude Code + Codex connection hints.

## What Was Built

### Task 1: httpTunnelProbe (plain-HTTP) in shell.go

Added `httpTunnelProbe(localPort int) tunnelProbe` next to `httpsTunnelProbe` in `internal/app/cmd/shell.go`. Probes `http://127.0.0.1:{port}/` with a 6s timeout using a plain `http.Client` (no TLS). Any non-error HTTP response (incl. 4xx) means the tunnel is live. Mirrors the exact doc-comment and return-value semantics of `httpsTunnelProbe`.

**Commit:** `829d1897`

### Task 2: internal/app/cmd/model.go

New `km model` command tree, fully mirroring `vscode.go` and `desktop.go`:

- `NewModelCmd(cfg)` / `newModelCmdInternal(cfg, fetcher, execFn, ssmClient)` — DI-injectable constructor
- `newModelStartCmd`: `km model start <sb> [--local-port 8001] [--anthropic]`
- `newModelStatusCmd`: `km model status <sb>`
- `resolveModelDeps`: initialises real AWS clients when test-injected deps are nil
- `runModelStart`: local port probe → `FetchSandbox` → print connection hints → `buildPortForwardCmd(…, "8001")` → `runReconnectingPortForward(…, httpTunnelProbe, true, …)`
- `runModelStatus`: `sendSSMAndWait(modelStatusScript)` → `parseModelStatus`
- `parseModelStatus`: structured errors for each failure mode (neither active / vllm only / bifrost only)
- `modelStatusScript`: single-round-trip SSM script checking `systemctl is-active vllm`, `systemctl is-active bifrost`, and `curl localhost:8001/`

**`--anthropic` semantics:** Bifrost serves the Anthropic Messages API on its `/anthropic` path. `ANTHROPIC_BASE_URL` must be set to `http://localhost:{port}/anthropic` (the path prefix, not just the host:port). This is explicitly noted in the connection hints and documented in CONTEXT.md.

**Commit:** `ead47438`

### Task 3: root.go registration + model_test.go

- `root.AddCommand(NewModelCmd(cfg))` added next to vscode/desktop registrations (~line 92).
- `model_test.go` fully implemented (t.Skip removed):
  - `TestModelStart_PortForwardWiring`: mock fetcher + mock execFn, asserts `AWS-StartPortForwardingSession`, `portNumber`, `8001`, `localPortNumber` all appear in cmd args.
  - `TestModelStart_AnthropicFlag`: `--anthropic=true` still targets remote :8001.
  - `TestModelStart_CustomLocalPort`: non-default local port (18001) appears in args.
  - `TestModelCmd_Registered`: start + status subcommands present.
  - `TestModelCmd_InRootTree`: model found in root cmd tree.
  - `TestModelStatus_Healthy/VLLMInactive/BifrostInactive/NeitherActive`: parseModelStatus coverage.

**Commit:** `0def7d41`

## Verification

```
go build ./... && go vet ./internal/app/cmd/  — clean
go test ./internal/app/cmd/ -run TestModel -count=1 -timeout 600s  — ok (0.927s)
./km model --help  — shows start + status subcommands
make build  — km v0.5.35 clean
```

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- internal/app/cmd/model.go: FOUND
- internal/app/cmd/model_test.go: FOUND
- 122-04-SUMMARY.md: FOUND
- commit 829d1897 (httpTunnelProbe): FOUND
- commit ead47438 (model.go): FOUND
- commit 0def7d41 (root + tests): FOUND
