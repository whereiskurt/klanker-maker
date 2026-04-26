---
phase: 56
slug: learn-mode-ami-snapshot-and-lifecycle-management
status: planned
nyquist_compliant: true
wave_0_complete: false
created: 2026-04-26
updated: 2026-04-26
---

# Phase 56 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (stdlib) — same as project default |
| **Config file** | none — existing test infrastructure |
| **Quick run command** | `go test ./internal/app/cmd/... ./pkg/aws/... ./pkg/allowlistgen/... -run "AMI\|Bake\|Stale\|RecordAMI\|StaleAMIDays" -count=1` |
| **Full suite command** | `go test ./internal/app/cmd/... ./pkg/aws/... ./pkg/profile/... ./pkg/allowlistgen/... ./internal/app/config/... -count=1` |
| **Estimated runtime** | ~10 seconds (quick) / ~45 seconds (full) |

---

## Sampling Rate

- **After every task commit:** Run quick command above
- **After every plan wave:** Run full suite command above
- **Before `/gsd:verify-work`:** Full suite must be green (`go test ./... -count=1`)
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

| Plan-Task | Wave | Requirement(s) | Test Type | Automated Command | File Exists | Status |
|-----------|------|----------------|-----------|-------------------|-------------|--------|
| 56-01-T1 | 1 | P56-01, P56-02 | unit | `go build ./pkg/aws/... && go vet ./pkg/aws/...` | created in task | ⬜ pending |
| 56-01-T2 | 1 | P56-01, P56-02, P56-07 | unit | `go test ./pkg/aws/... -run "TestKMBakeTags\|TestAMIName\|TestBakeAMI\|TestDeleteAMI\|TestSnapshotIDsFromImage\|TestListBakedAMIs\|TestCopyAMI" -count=1` | created in task | ⬜ pending |
| 56-02-T1 | 1 | P56-09 | unit | `go test ./internal/app/config/... -run "TestConfig_DoctorStaleAMIDays" -count=1` | created in task | ⬜ pending |
| 56-02-T2 | 1 | P56-09 | smoke | `grep -q "doctor_stale_ami_days" km-config.yaml && go build ./...` | edits existing | ⬜ pending |
| 56-03-T1 | 1 | P56-10 | unit | `go test ./internal/app/cmd/... -run "TestBootstrapSCP" -count=1` | created in task | ⬜ pending |
| 56-04-T1 | 2 | P56-03, P56-04, P56-05, P56-06, P56-07 | unit | `go build ./internal/app/cmd/... && go vet ./internal/app/cmd/...` | created in task | ⬜ pending |
| 56-04-T2 | 2 | P56-03, P56-04, P56-05, P56-06, P56-07 | unit | `go test ./internal/app/cmd/... -run "TestAMIList\|TestAMIDelete\|TestAMICopy\|TestBakeFromSandbox" -count=1` | created in task | ⬜ pending |
| 56-04-T3 | 2 | P56-03, P56-04, P56-05, P56-06, P56-07 | smoke | `make build && ./bin/km ami --help \| grep -E "list\|delete\|bake\|copy" \| wc -l \| grep -q "^4$"` | edits existing | ⬜ pending |
| 56-05-T1 | 2 | P56-11 | unit | `go test ./pkg/allowlistgen/... -run "TestRecordAMI\|TestGenerate_WithAMI\|TestGenerateAnnotatedYAML_WithAMI\|TestGenerate_WithoutAMI" -count=1` | created in task | ⬜ pending |
| 56-05-T2 | 2 | P56-01, P56-02, P56-11 | unit | `go test ./internal/app/cmd/... -run "TestRunLearnPostExit\|TestNewShellCmd_AMIFlag\|TestGenerateProfileFromJSON_WithAMI" -count=1` | edits existing | ⬜ pending |
| 56-06-T1 | 2 | P56-08 | unit | `go test ./internal/app/cmd/... -run "TestCheckStaleAMIs" -count=1` | edits existing | ⬜ pending |
| 56-06-T2 | 2 | P56-08 | unit | `go test ./internal/app/cmd/... -run "TestDoctor_AllRegionsFlag\|TestDoctor_DefaultRegionScope" -count=1` | edits existing | ⬜ pending |
| 56-MANUAL | post-merge | P56-12 | manual | See "Manual-Only Verifications" below | N/A | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

Wave 0 of every Plan creates its own test scaffolding before implementation (TDD per task). Specifically:

- [ ] `pkg/aws/ec2_ami_test.go` — mock `EC2AMIAPI` + 12 unit tests for `BakeAMI` / `DeleteAMI` / `CopyAMI` / `KMBakeTags` / `AMIName` / `ListBakedAMIs` / `SnapshotIDsFromImage`. (Plan 01 Task 2.)
- [ ] `internal/app/config/config_test.go` — `TestConfig_DoctorStaleAMIDays_*` (default, env override, file override, zero-clamp). (Plan 02 Task 1.)
- [ ] `internal/app/cmd/bootstrap_test.go` — `TestBootstrapSCP_*` (action additions, describe-ops absence, trusted-base unchanged). (Plan 03 Task 1.)
- [ ] `internal/app/cmd/ami_test.go` — local `mockEC2AMI` + 13 unit tests for list / delete / copy / bake helper. (Plan 04 Task 2.)
- [ ] `pkg/allowlistgen/generator_test.go` additions — `TestRecordAMI_*` + `TestGenerate_WithAMI*` + Phase-55-compat test. (Plan 05 Task 1.)
- [ ] `internal/app/cmd/shell_test.go` additions — `TestRunLearnPostExit_AMIFlag_*` (call order, injection, fallback). (Plan 05 Task 2.)
- [ ] `internal/app/cmd/doctor_test.go` additions — `TestCheckStaleAMIs_*` (9 scenarios) + `TestDoctor_AllRegionsFlag_*` (2 tests). (Plan 06 Tasks 1+2.)

No new test framework needed; existing `testing` (stdlib) + `testify` (v1.11.1, already pinned) patterns are reusable.

**Mock injection contract:** All Wave 2 plans inject AWS clients via `kmaws.EC2AMIAPI` (interface from Plan 01), `SandboxFetcher`, and `SandboxLister`. Tests pass mock implementations; no live AWS required for any unit test.

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Live `km shell --learn --ami` end-to-end | P56-01, P56-02 | Requires live AWS account, EC2 launch, real snapshot | After all plans merged + `make build` + `km init --sidecars`: run `km create profiles/dc34.yaml`, then `km shell --learn --ami <id>`, exit; verify `learned.<id>.<ts>.yaml` contains `ami: ami-xxxxxxxx` in `spec.runtime`; verify AMI exists via `aws ec2 describe-images --image-ids ami-xxxxxxxx --region $KM_REGION` and tags include `km:sandbox-id`, `km:profile`, `km:alias`, `km:baked-at`. |
| Phase 33 slug AMI resolution in ca-central-1 | P56-12 | Requires Terragrunt + live AWS in non-use1 region | After this phase ships: set `KM_REGION=ca-central-1`, attempt a `km create` with a profile that has `spec.runtime.ami: amazon-linux-2023`; verify `data.aws_ami.base_ami` resolves to a non-empty AMI ID in cac1 (run `km create --verbose` to see the terraform plan output, or run `terraform plan` directly against `infra/modules/ec2spot/v1.0.0/` with `region = "ca-central-1"` and `ami_slug = "amazon-linux-2023"`). Closes Phase 33's open human-verification item #2. |
| `km ami copy` cross-region with re-tagging | P56-07 | Requires multi-region AWS + cross-region API call | After source AMI exists in us-east-1, run `km ami copy <ami-id> --to-region ca-central-1`; wait for completion; run `km ami list --region ca-central-1`; verify destination AMI has same tags as source (specifically `km:sandbox-id`, `km:profile`, `km:source-region=us-east-1`). |
| `km ami delete --force` removes orphaned EBS snapshots | P56-06 | Requires real AMI + visible cost impact | Bake AMI; capture its snapshot IDs from `km ami list --wide`; force-delete it (`km ami delete <id> --force --yes`); verify `aws ec2 describe-snapshots --snapshot-ids <ids>` returns empty (snapshots gone, not orphaned). |
| `km doctor` stale-AMI flag in real conditions | P56-08 | Requires real AMIs older than configured threshold | Set `doctor_stale_ami_days: 1` in km-config.yaml; bake an AMI; wait > 24h (or use a fake-old test AMI); run `km doctor`; verify the `Stale AMIs (us-east-1)` check appears with status WARN and lists the AMI. Add a profile referencing the AMI and re-run; verify the check now returns OK (profile-ref skip works). |
| SCP refresh applied to deployed account | P56-10 | Requires Organizations API access | After Plan 03 merges + `km bootstrap` re-run against the application account: from the SSO operator role, run `aws ec2 deregister-image --image-id ami-test --region $KM_REGION` against a known-bad AMI; verify the error is `InvalidAMIID.NotFound` (not `UnauthorizedOperation`). Confirms the SCP exemption is working. |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies (every task in 56-01 through 56-06 has an `<automated>` block in `<verify>`)
- [x] Sampling continuity: no 3 consecutive tasks without automated verify (all 12 plan-tasks have automated commands)
- [x] Wave 0 covers all MISSING references (each plan defines its own test files in TDD-first manner)
- [x] No watch-mode flags (all `go test` commands are one-shot with `-count=1`)
- [x] Feedback latency < 30s (quick command estimated ~10s)
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved by gsd-planner — 2026-04-26
