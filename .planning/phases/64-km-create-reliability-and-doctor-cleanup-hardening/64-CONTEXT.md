---
phase: 64
name: km create reliability and doctor cleanup hardening
created: 2026-05-01
status: not-planned
depends_on: 63
---

# Phase 64 Context

## Source

Bundled follow-ups from Phase 63.1 verification + two long-standing todos that share the "make `km create` and `km doctor` more robust under failure" theme.

- CLEAN-1, CLEAN-2, CLEAN-3 surfaced during Phase 63.1 execution (see `.planning/phases/63.1-.../63.1-VERIFICATION.md` and the completion conversation transcript dated 2026-05-01).
- CLEAN-4 from `.planning/todos/pending/doctor-sidecar-health.md` (created 2026-03-26).
- CLEAN-5 from `.planning/todos/pending/spot-multi-az.md` (created 2026-03-26).

## Goal

Address operational hygiene gaps surfaced during Phase 63.1 plus closely-related create-flow reliability todos. Five requirements bundled together because they share the "make km create + km doctor more robust under failure" theme. After this phase, operators should be able to recover from create-time infra failures cleanly (no orphan Slack channels, multi-AZ spot fallback) and use `km doctor` to both detect AND fix the most common drift conditions on the platform.

## Requirements

### CLEAN-1 — Fix dormant test failure `TestUnlockCmd_RequiresStateBucket`

**Where:** `internal/app/cmd/unlock_test.go:73`

**Symptom:** Test asserts the unlock command's error mentions `state bucket` when `StateBucket` is empty, but the command's validation order short-circuits earlier with `sandbox sb-aabbccdd is not locked`. Failure was reproduced against commit `9f00244` (pre-63.1) — predates all Phase 63.1 work; last touched in commit `22366b1` (Phase 30).

**Action:** Reorder unlock's validation so `StateBucket` is checked before the lock-existence check, OR adjust the test to assert the actual current behavior if the existing order is intentional. Pick whichever matches operator expectation.

**Stretch:** Audit `go test ./...` for any other dormant failures while in there.

### CLEAN-2 — `km doctor --auto-fix` archives stale Slack channels and orphan resources

**Where:** `internal/app/cmd/doctor.go` (and any check helpers)

**Symptom:** `km doctor` already detects stale per-sandbox Slack channels (saw "⚠ Stale Slack channels: 1 stale per-sandbox channel(s) with no active sandbox: C0B1EK41FUH — run km destroy to archive" during 63.1 UAT). Operator must manually run `km destroy` or call `conversations.archive` by hand.

**Action:** Add `--auto-fix` (or `--dry-run=false`) flag to `km doctor`. When set, doctor calls `conversations.archive` via the Slack bot token for each stale per-sandbox channel detected. Should also handle other orphan-resource classes the doctor surfaces (audit current checks for candidates).

**Trust model:** Same as Phase 63 archive — operator workstation has SSM read on `/km/slack/bot-token` and the bridge URL; archive can either go through the bridge (signed) or directly via Slack API with the bot token (operator-side only).

### CLEAN-3 — Fix `km create` orphan-channel-on-failure bug

**Where:** `internal/app/cmd/create.go` and `internal/app/cmd/create_slack.go`

**Symptom:** `km create` provisions the Slack channel BEFORE running `terraform apply`. If infra fails (verified failure modes from 63.1 morning testing: the IAM instance-profile race that hit `stp-e9c88f5c` at 11:14PM 2026-04-30, OR any spot capacity error), the Slack channel persists with no sandbox to own it. Result: orphan channels accumulate, retries with the same alias hit `name_taken` errors (we hit this on the second create attempt with alias `63-1-destroy-test`).

**Architectural decision deferred to research/plan:**

- **Option A:** Move Slack channel provisioning to AFTER `terraform apply` succeeds. Pro: simplest, no orphans by construction. Con: changes ordering operators may rely on (channel exists before sandbox boots), may delay first notification.
- **Option B:** Keep current order but rollback the channel on any subsequent failure path. Pro: preserves "channel exists when sandbox boots" semantics. Con: more failure paths to test, rollback itself can fail.

Plan phase should pick one with rationale.

### CLEAN-4 — `km doctor` checks sidecar systemd health on active sandboxes

**Source:** `.planning/todos/pending/doctor-sidecar-health.md` (2026-03-26)

**Symptom:** `km doctor` validates platform infrastructure (Lambdas, DynamoDB, SSM params) but doesn't check whether sidecars (`km-dns-proxy`, `km-http-proxy`, `km-audit-log`, `km-slack` if present) are actually running on active sandbox instances. A sandbox could have crash-looping sidecars without doctor reporting it.

**Action:** When active sandboxes exist, doctor calls `aws ssm send-command` against each instance with a `systemctl is-active <service>` check for each known sidecar. Report any non-active sidecar as a WARN. Should be opt-in or rate-limited to avoid blowing past SSM concurrency limits when many sandboxes are active.

### CLEAN-5 — `km create` retries spot capacity across multiple AZs

**Source:** `.planning/todos/pending/spot-multi-az.md` (2026-03-26)

**Symptom:** Spot capacity in `us-east-1a` is frequently unavailable, causing `km create` to fail. The `ec2spot` module today only tries one AZ (the first subnet from the network module). The very first failure of Phase 63.1 testing (`stp-e9c88f5c`, 11:14PM 2026-04-30) was an IAM instance-profile race during `ec2spot` Terragrunt apply.

**Files:** `infra/modules/ec2spot/v1.0.0/main.tf`, `infra/modules/network/v1.0.0/main.tf`

**Three architectural options for plan phase:**

1. Have `ec2spot` retry across AZs on `InsufficientInstanceCapacity` / `bad-parameters` errors
2. Widen the `network` module to more AZs (currently ~2)
3. Make on-demand the default for testing profiles

Plan phase should pick one (or combine 1+2) with rationale.

## Cross-cutting notes

- **Trust model unchanged:** Operator workstation already has all needed credentials (SSM, DynamoDB, SES, Slack token via SSM, Lambda invoke). No new IAM scopes for sandboxes.
- **Test discipline:** Use the `captureStderr` helper from `internal/app/cmd/testhelpers_test.go` for any new visibility tests. Use `runner UninitRunner`-style dependency injection for any code paths that touch real AWS — never let a `go test ./...` shell out to a live AWS API.
- **Memory `feedback_rebuild_km`:** Always `make build` (NOT bare `go build`) when rebuilding the CLI.
- **Memory `feedback_gsd_not_beads`:** Track follow-up work in GSD plans/todos, never beads issues.

## Suggested wave grouping (for plan phase)

- **Wave 1 (independent):** CLEAN-1 (test fix), CLEAN-4 (doctor sidecar health) — both fully isolated
- **Wave 2 (Slack/doctor cluster):** CLEAN-2 (doctor auto-fix archive)
- **Wave 3 (create reliability cluster):** CLEAN-3 (orphan-channel rollback) + CLEAN-5 (multi-AZ spot) — share `km create` flow and likely share test scaffolding

These are suggestions; planner may regroup based on dependency analysis.
