---
phase: 36
slug: km-sandbox-base-container-image
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-30
---

# Phase 36 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (existing) + docker build verification |
| **Config file** | none — existing Go test infrastructure |
| **Quick run command** | `go test ./pkg/compiler/... -run ECS` |
| **Full suite command** | `go test ./pkg/compiler/... && go build -o km ./cmd/km/` |
| **Estimated runtime** | ~5 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/compiler/... -run ECS`
- **After every plan wave:** Run `go test ./pkg/compiler/... && go build -o km ./cmd/km/`
- **Before `/gsd:verify-work`:** Full suite must be green + `docker build` of sandbox image
- **Max feedback latency:** 5 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 36-01-01 | 01 | 1 | Dockerfile | build | `docker build containers/sandbox/` | ❌ W0 | ⬜ pending |
| 36-01-02 | 01 | 1 | entrypoint.sh | manual | Shell script lint + review | ❌ W0 | ⬜ pending |
| 36-02-01 | 02 | 2 | Compiler env vars | unit | `go test ./pkg/compiler/... -run ECSEnv` | ❌ W0 | ⬜ pending |
| 36-02-02 | 02 | 2 | Image URI replacement | unit | `go test ./pkg/compiler/... -run MainImage` | ❌ W0 | ⬜ pending |
| 36-03-01 | 03 | 2 | Makefile target | build | `make sandbox-image` | ❌ W0 | ⬜ pending |
| 36-03-02 | 03 | 2 | ECR push | integration | `make ecr-push` (includes sandbox) | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `containers/sandbox/Dockerfile` — builds successfully
- [ ] `containers/sandbox/entrypoint.sh` — shell script passes shellcheck (if available)
- [ ] Existing Go tests pass after compiler changes

*Existing test infrastructure covers compiler changes.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Entrypoint CA trust setup | CA trust env vars | Requires running container with S3 access | `docker run km-sandbox:latest env \| grep SSL_CERT_FILE` |
| Entrypoint secret injection | SSM secrets | Requires live SSM Parameter Store | Create test param, run container, verify env var |
| SIGTERM artifact upload | Graceful shutdown | Requires running container + S3 | `docker kill --signal SIGTERM`, check S3 for artifacts |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 5s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
