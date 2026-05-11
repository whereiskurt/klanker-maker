---
phase: 79-km-presence-daemon
plan: "03"
subsystem: build-pipeline
tags: [makefile, sidecar, build, km-presence]
dependency_graph:
  requires: ["79-00"]
  provides: ["km-presence S3 upload pipeline", "build/km-presence binary"]
  affects: ["sidecars target", "build-sidecars target"]
tech_stack:
  added: []
  patterns: ["mirror km-slack stripped-binary build pattern"]
key_files:
  created: []
  modified:
    - Makefile
decisions:
  - "Used -trimpath -ldflags '-s -w' flags (stripped binary, no version embed) to match km-slack pattern — km-presence is a sandbox-internal daemon, not the km CLI"
  - "Added km-presence after km-slack in both targets for logical grouping of sandbox-internal binaries"
metrics:
  duration: "112s"
  completed: "2026-05-11"
  tasks_completed: 1
  files_modified: 1
---

# Phase 79 Plan 03: Makefile km-presence Build Pipeline Summary

**One-liner:** Extended Makefile sidecar build pipeline with km-presence binary (build + S3 upload + echo manifest), mirroring the km-slack stripped-binary pattern.

## What Was Done

Added km-presence to two Makefile targets:

1. `sidecars` target — build + S3 upload + echo manifest (3 lines)
2. `build-sidecars` target — build only, no S3 upload (1 line)

## Lines Added to Makefile (after edit, exact content)

| Line | Target | Content |
|------|--------|---------|
| 91 | `sidecars` | `\tGOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -ldflags '-s -w' -o build/km-presence ./cmd/km-presence/` |
| 97 | `sidecars` | `\taws s3 cp build/km-presence s3://$(KM_ARTIFACTS_BUCKET)/sidecars/km-presence` |
| 107 | `sidecars` | `\t@echo "  s3://$(KM_ARTIFACTS_BUCKET)/sidecars/km-presence"` |
| 131 | `build-sidecars` | `\tGOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -ldflags '-s -w' -o build/km-presence ./cmd/km-presence/` |

All 4 lines use TAB indentation (verified via `grep -P "^\t.*km-presence"`).

## Build Artifact

```
/Users/khundeck/working/klankrmkr/build/km-presence:
  ELF 64-bit LSB executable, x86-64, version 1 (SYSV), statically linked, stripped
  Size: 1.2M
```

Cross-compiled via GOOS=linux GOARCH=amd64 CGO_ENABLED=0 as expected for sandbox deployment.

## make build Unaffected

The `build` target (km CLI) was not touched. It still invokes `bump-version` and builds `./cmd/km/` with full `$(LDFLAGS)` version embedding. Confirmed by reviewing the target at lines 60-63 — no changes made.

## Operator Rollout Sequence

After this phase merges, the complete operator rollout sequence is:

```bash
make build            # 1. Build km CLI (bump version)
make sidecars         # 2. Build + upload all sidecars including km-presence
km init --sidecars    # 3. Refresh management Lambda + sidecar S3 objects
```

This sequence ensures that `km create` (remote path via management Lambda) picks up the new km-presence binary from S3, which Plan 79-02's userdata script fetches at sandbox boot.

## Deviations from Plan

None — plan executed exactly as written.

## Self-Check: PASSED

- [x] `build/km-presence` exists: `ls -lh build/km-presence` → 1.2M ELF 64-bit
- [x] Commit b7f40e5 exists: `git log --oneline | grep b7f40e5`
- [x] `grep -n "km-presence" Makefile` → 4 hits (lines 91, 97, 107, 131)
- [x] All 4 lines TAB-indented: `grep -Pn "^\t.*km-presence" Makefile` → 4 hits
- [x] `make build-sidecars` succeeds without errors
