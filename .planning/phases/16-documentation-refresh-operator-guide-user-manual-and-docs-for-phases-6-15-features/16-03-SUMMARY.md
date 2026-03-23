---
phase: 16-documentation-refresh-operator-guide-user-manual-and-docs-for-phases-6-15-features
plan: "03"
subsystem: docs
tags: [documentation, email, sidecars, phase-14, phase-8]
dependency_graph:
  requires: []
  provides: [docs/multi-agent-email.md, docs/sidecar-reference.md]
  affects: []
tech_stack:
  added: []
  patterns: [doc-with-source-verification]
key_files:
  created: []
  modified:
    - docs/multi-agent-email.md
    - docs/sidecar-reference.md
decisions:
  - "Email guide uses three separate Phase 14 sections (Signed Email, Optional Encryption, Profile Controls) matching the source code layering in identity.go"
  - "Sidecar build section placed as section 5 (after sidecar descriptions) for natural reading flow"
  - "spec.email field documented as *EmailSpec pointer (not bool) matching actual type definition — nil means disabled, not false"
metrics:
  duration: "133s"
  completed_date: "2026-03-23"
  tasks_completed: 2
  files_modified: 2
---

# Phase 16 Plan 03: Multi-Agent Email Guide and Sidecar Reference Update Summary

**One-liner:** Updated email guide with Ed25519 signing/NaCl encryption protocol and sidecar reference with full Makefile build pipeline covering S3 binary delivery and ECR image publishing.

## What Was Built

### Task 1: Update multi-agent email guide with signed/encrypted email

Added three new sections to `docs/multi-agent-email.md` covering the Phase 14 identity layer:

**Signed Email (Phase 14):** Documents the Ed25519 body-signing protocol — `SignEmailBody` signs body only (not headers) for transit resilience. Covers the `X-KM-Sender-ID` and `X-KM-Signature` custom headers, the requirement to use `Content.Raw` (not `Content.Simple`) because SES strips unknown headers from Simple messages, and the full verification flow using `FetchPublicKey` + `VerifyEmailSignature`.

**Optional Encryption (Phase 14):** Documents the dual key pair architecture (Ed25519 for signing, X25519 for encryption), the `box.SealAnonymous`/`box.OpenAnonymous` NaCl protocol, the `X-KM-Encrypted: true` header, DynamoDB public key discovery, and the `required`/`optional` encryption policy gates in `SendSignedEmail`.

**Profile spec.email Controls (Phase 14):** Documents all five `spec.email` fields (`signing`, `verifyInbound`, `encryption`, `alias`, `allowedSenders`), the `*EmailSpec` nil pointer semantics (nil = disabled, not zero-value), and the built-in profile defaults for hardened/sealed (required signing, self-only allow-list) vs open-dev/restricted-dev (optional signing, `*` allow-list).

A Table of Contents was added to the top of the guide linking all sections including new ones.

### Task 2: Update sidecar reference with build pipeline

Added a new **Section 5: Build and Deployment Pipeline** to `docs/sidecar-reference.md` covering:

- `make sidecars`: cross-compiles three Go binaries (dns-proxy, http-proxy, audit-log) for linux/amd64, uploads binaries + tracing config.yaml to S3 at `s3://{KM_ARTIFACTS_BUCKET}/sidecars/`. Requires `KM_ARTIFACTS_BUCKET` env var.
- `make build-sidecars`: local-only compile to `build/` without S3 upload.
- Build context notes: audit-log builds from `./sidecars/audit-log/cmd/` (cmd/ holds package main; root is package auditlog library). Tracing is not a Go binary — it is `otelcol-contrib` with a YAML config. dns-proxy and http-proxy use the repo root as build context for shared package access.
- `make ecr-push`: builds Docker images for all four sidecars and pushes to ECR. ECR URI pattern: `{account-id}.dkr.ecr.{region}.amazonaws.com/km-{name}:{version}`. `KM_SIDECAR_VERSION` defaults to `latest`.
- `PLACEHOLDER_ECR/` prefix: used in compiler-generated HCL when `KM_ACCOUNTS_APPLICATION` is unset.
- S3 binary delivery for EC2: user-data downloads binaries at boot, installs as systemd services. Contrasted with ECS where images are pulled from ECR by the scheduler.

A Table of Contents was added linking all eight sections.

## Deviations from Plan

None — plan executed exactly as written.

## Commits

| Task | Commit | Description |
|------|--------|-------------|
| 1 | c67802e | docs(16-03): update multi-agent email guide with Phase 14 signed/encrypted email |
| 2 | ee62af7 | docs(16-03): update sidecar reference with Phase 8 build and deployment pipeline |

## Self-Check: PASSED

- FOUND: docs/multi-agent-email.md
- FOUND: docs/sidecar-reference.md
- FOUND: commit c67802e (Task 1)
- FOUND: commit ee62af7 (Task 2)
