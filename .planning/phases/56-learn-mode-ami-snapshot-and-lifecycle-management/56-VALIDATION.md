---
phase: 56
slug: learn-mode-ami-snapshot-and-lifecycle-management
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-04-26
---

# Phase 56 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (stdlib) — same as project default |
| **Config file** | none — existing test infrastructure |
| **Quick run command** | `go test ./internal/app/cmd/... ./pkg/aws/... -run "AMI\|Bake\|Stale" -count=1` |
| **Full suite command** | `go test ./internal/app/cmd/... ./pkg/aws/... ./pkg/profile/... ./pkg/config/... -count=1` |
| **Estimated runtime** | ~10 seconds (quick) / ~45 seconds (full) |

---

## Sampling Rate

- **After every task commit:** Run quick command above
- **After every plan wave:** Run full suite command above
- **Before `/gsd:verify-work`:** Full suite must be green (`go test ./... -count=1`)
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| TBD     | TBD  | TBD  | TBD         | TBD       | TBD               | TBD         | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

*Filled in by gsd-planner during plan generation.*

---

## Wave 0 Requirements

- [ ] AWS SDK EC2 mocks pattern — extend `pkg/aws/` (or new `pkg/aws/ec2_image.go`) following `doctor_test.go` mocking convention.
- [ ] `pkg/aws/ec2_image_test.go` — stubs for CreateImage / DescribeImages / DeregisterImage / CopyImage helpers.
- [ ] `internal/app/cmd/ami_test.go` — stubs for the new `km ami list/delete/bake/copy` subcommand tree.
- [ ] No new test framework needed; existing `testify`/stdlib patterns are reusable.

*Filled in / refined by gsd-planner during plan generation.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Live `km shell --learn --ami` end-to-end | TBD | Requires live AWS account, EC2 launch, real snapshot | `km create profiles/dc34.yaml`, `km shell --learn --ami <id>`, exit; verify `learned.*.yaml` has `ami: ami-xxxxxxxx`; verify AMI exists via `aws ec2 describe-images` and tags include sandbox-id, profile, alias, date. |
| Phase 33 slug AMI resolution in ca-central-1 | TBD | Requires Terragrunt + live AWS in non-use1 region | After this phase ships, set `KM_REGION=ca-central-1`, attempt a `km create` (or `terraform plan` directly against the module) with `ami: amazon-linux-2023`; verify `data.aws_ami.base_ami` resolves to a Canonical/Amazon AMI in cac1. Closes Phase 33's open human-verification item #2. |
| `km ami copy` cross-region with re-tagging | TBD | Requires multi-region AWS + cross-region API call | After source AMI exists, run `km ami copy <ami-id> --to-region ca-central-1`; verify destination AMI has same tags as source (re-tagged after copy completes). |
| `km ami delete --force` removes orphaned EBS snapshots | TBD | Requires real AMI + visible cost impact | Bake AMI, force-delete it; verify `aws ec2 describe-snapshots` shows the underlying snapshots are gone (not orphaned). |
| `km doctor` stale-AMI flag in real conditions | TBD | Requires real AMIs older than configured threshold | Set `doctor.staleAMIDays: 1`; create a test AMI; wait > 1 day OR fake creation date; run `km doctor`; verify the stale check fires and lists the AMI. |

*Refined by gsd-planner during plan generation.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending (filled in by planner)
