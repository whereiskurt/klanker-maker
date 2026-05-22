---
phase: 87
slug: additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-21
---

# Phase 87 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution. Derived from `87-RESEARCH.md` § Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` (stdlib) — no separate test runner |
| **Config file** | none (plain `go test ./...`) |
| **Quick run command** | `go test ./pkg/profile/... ./pkg/compiler/... -count=1 -run 'TestAdditional|TestValidate|TestAWSValidate|TestUserdata|TestBackwardCompat'` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~60s quick, ~5min full |

---

## Sampling Rate

- **After every task commit:** Run quick command above (~60s)
- **After every plan wave:** Run full suite (`go test ./... -count=1`)
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 60 seconds

---

## Per-Task Verification Map

| Task ID (TBD by planner) | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---|---|---|---|---|---|---|---|
| 87-01-* | 01 | 0 | SNAP-01 | unit | `go test ./pkg/profile/... -run TestAdditionalSnapshot` | ❌ W0 | ⬜ pending |
| 87-02-* | 02 | 1 | SNAP-02 | unit | `go test ./pkg/profile/... -run TestValidate` | ✅ | ⬜ pending |
| 87-03-* | 03 | 2 | SNAP-03 | unit (mocked SDK) | `go test ./pkg/profile/... -run TestAWSValidate` | ❌ W0 | ⬜ pending |
| 87-04-* | 04 | 3 | SNAP-04 | unit (HCL render) | `go test ./pkg/compiler/... -run 'TestAdditionalSnapshot|TestPickAdditionalVolumeDevice'` | ✅ (extend) | ⬜ pending |
| 87-05-* | 05 | 3 | SNAP-05 | unit (golden) | `go test ./pkg/compiler/... -run TestUserdata` | ✅ (extend) | ⬜ pending |
| 87-06-* | 06 | 4 | SNAP-06 | manual | `terragrunt validate` on dry-run profile | n/a | ⬜ pending |
| 87-07-* | 07 | 3 | SNAP-07 | unit (golden, zero-diff) | `go test ./pkg/compiler/... -run TestBackwardCompat` | ❌ W0 | ⬜ pending |
| 87-08-* | 08 | 5 | SNAP-08 | UAT (real AWS) | operator-driven `km create` × 8 scenarios | n/a | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

*Note: exact task IDs (`87-NN-MM`) get assigned by the planner when it breaks each SNAP-NN into atomic tasks.*

---

## Wave 0 Requirements

- [ ] `pkg/profile/types_test.go` — extend with SNAP-01 YAML parse cases (0/1/3 snapshot entries)
- [ ] `pkg/profile/aws_validate_test.go` (NEW) — mock `EC2SnapshotAPI` interface + table-driven SNAP-03 cases
- [ ] `pkg/profile/validate_test.go` — extend with SNAP-02 Layer 1 table cases
- [ ] `pkg/compiler/service_hcl_test.go` — extend with SNAP-04 HCL render assertions for `additional_snapshots` block
- [ ] `pkg/compiler/userdata_test.go` — extend with SNAP-05 golden file (legacy byte-identity modulo `${FSTYPE}`) + multi-entry loop assertions
- [ ] `pkg/compiler/ec2_storage_test.go` — extend `TestPickAdditionalVolumeDevice` for new `claimed map[string]bool` parameter
- [ ] No new test framework needed — Go stdlib `testing` is already wired

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| `terragrunt validate` on new `ec2spot/v1.1.0/` module | SNAP-06 | Module-level HCL validation runs outside Go test process; needs terragrunt binary | `cd .planning/scratch/ && terragrunt --terragrunt-config <dry-run-profile-terragrunt.hcl> validate` |
| Real AWS `DescribeSnapshots` against a freshly-created snapshot | SNAP-08 / UAT-1 | Requires AWS credentials + real snapshot ID; mocks can't catch wire-level wiring bugs | `aws ec2 create-snapshot --volume-id <vol-id> --description "uat-1"` → wait `completed` → author profile referencing returned `snap-*` → `km create profiles/uat-1.yaml` → SSM in → `mount \| grep <mountpoint>` → `cmp` against source contents → `km destroy --remote --yes` → `aws ec2 describe-volumes` confirms only the materialised volume gone |
| 8 UAT scenarios (single, multi, explicit-device, AMI-BDM-collision, missing, wrong-region, size-override, shrink-rejected) | SNAP-08 | Each tests an AWS-side or boot-time behaviour that mocks can't reproduce | See `BRIEF.md § SNAP-08` table |
| Zero-diff regression for an `additionalVolume`-only profile under `v1.1.0` | SNAP-07 (cross-check) | Golden tests catch Go template output; this confirms the runtime userdata still mounts ext4 identically | Pick an existing `additionalVolume`-only profile from `profiles/`; `km create`; SSM in; confirm fstab line + mount point match pre-Phase-87 baseline |

---

## Aliasing Risks (Nyquist Dimension 8)

Risks where a passing test does NOT prove the behaviour works in production:

### SNAP-07 — golden-file byte-identity (HIGHEST RISK)

The byte-identical userdata assertion is most aliasing-prone. If the refactored `range .AdditionalVolumeMounts` loop produces the same bash for the single-entry case, the test passes — but it may silently omit the `blkid`-based `FSTYPE` substitution in a way that works identically on ext4 volumes at runtime while failing on xfs/btrfs.

**Mitigation:** Golden file MUST specifically assert the fstab line contains `${FSTYPE}` (not hard-coded `ext4`). Add an explicit `strings.Contains(out, "${FSTYPE}")` assertion alongside the golden-diff check.

### SNAP-03 — mocked DescribeSnapshots IAM-missing path

The mock for "IAM-missing" must use the correct EC2 error code (`UnauthorizedOperation`, not `AccessDenied`) or the WARN-and-skip path won't be exercised. A test that mocks generic `err != nil` without checking the specific code path will not catch the wrong error type being tested.

**Mitigation:** Test must construct a real `smithy.GenericAPIError` with `Code: "UnauthorizedOperation"` and assert the WARN log + skip-and-continue behaviour, NOT just "any error → graceful." Add a second case for `"AccessDenied"` that asserts the call DOES fail (different code path).

### SNAP-04 — device allocation cross-entry deduplication

Tests must cover the exact case where explicit `device` on entry 0 + auto-picked entry 1 yields the correct next-available slot skipping both AMI BDM and the pinned device. A simple "auto-pick from empty `claimed`" test does not catch the cross-entry deduplication.

**Mitigation:** Add a table case with: AMI BDM = `[/dev/sdf]`, `additionalVolume` auto-picked (would land on `/dev/sdg`), explicit entry 0 pinned to `/dev/sdh`, auto entry 1. Assert entry 1 lands on `/dev/sdi`, not `/dev/sdh` (collision) or `/dev/sdg` (additionalVolume's slot).

### SNAP-05 — userdata FS-detection branching

The `mkfs.ext4 -F` branch only fires when `blkid` returns nothing. A test golden file covering only the "blank volume" path will not catch a bug where `blkid`-returns-`xfs` accidentally falls through to `mkfs.ext4` (which would corrupt the snapshot data).

**Mitigation:** Add a SHELL-side test (or detailed runtime UAT documentation) confirming that for a snapshot-restored xfs volume, no `mkfs.ext4` is invoked. Could be a `bash -n` static check or a manual UAT-9.

### SNAP-08 / UAT-5 — pre-flight artifact cleanliness

UAT-5 (missing snapshot) asserts "no terragrunt artifact is left on disk." This requires the pre-flight to abort BEFORE the compiler runs. Easy to alias: a test that checks "create errors" without checking the working directory's filesystem state will not catch a pre-flight that runs AFTER compile.

**Mitigation:** UAT-5 runbook MUST include `find $(km path scratch) -name 'terragrunt.hcl' -newer <known-timestamp>` to confirm zero new artifacts; the integration test in `create_test.go` MUST assert the scratch directory is empty after the mocked pre-flight rejection.

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify (UAT phase is the only run of manual tasks; bounded by SNAP-08)
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 60s for quick command
- [ ] `nyquist_compliant: true` set in frontmatter after planner finalises plans

**Approval:** pending
