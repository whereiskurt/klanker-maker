---
phase: 113
slug: sandbox-self-awareness-on-box-profile-plus-capability-network-privilege-self-census-in-klanker-sandbox
status: approved
nyquist_compliant: true
wave_0_complete: false
created: 2026-06-14
---

# Phase 113 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (stdlib `testing`) |
| **Config file** | none — run via `go test` |
| **Quick run command** | `go test ./pkg/compiler/ -run TestUserdata -count=1 -timeout 600s` |
| **Full suite command** | `go test ./... -count=1 -timeout 600s` |
| **Estimated runtime** | ~120s (pkg/compiler) / several min (full) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/compiler/ -run TestUserdata -count=1 -timeout 600s`
- **After every plan wave:** Run `go test ./... -count=1 -timeout 600s` + `bash scripts/validate-all-profiles.sh`
- **Before `/gsd:verify-work`:** Full suite green AND `validate-all-profiles.sh` green
- **Max feedback latency:** ~120s (compiler package alone)

NOTE: capture the command's OWN exit code (not a piped `tail`) — see `feedback_check_go_test_exit_not_pipe`. Use `-timeout 600s`.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 113-01-01 | 01 | 1 | profile-write block renders | unit | `go test ./pkg/compiler/ -run TestUserdataProfileWriteBlockRendered -count=1` | ❌ W0 | ⬜ pending |
| 113-01-02 | 01 | 1 | profile YAML round-trips | unit | `go test ./pkg/compiler/ -run TestUserdataProfileYAMLRoundTrip -count=1` | ❌ W0 | ⬜ pending |
| 113-01-03 | 01 | 1 | no golden regression | unit | `go test ./pkg/compiler/ -run TestUserdata -count=1` (after golden regen) | ✅ (regen 3 goldens) | ⬜ pending |
| 113-02-01 | 02 | 2 | skill self-census + fixed profile path + graceful fallback | manual UAT | live `km shell` census run | ❌ W0 (live) | ⬜ pending |
| 113-02-02 | 02 | 2 | plugin version bump | smoke | `jq .version .claude-plugin/plugin.json marketplace.json` match | ✅ | ⬜ pending |
| 113-03-01 | 03 | 3 | profiles still valid | smoke | `bash scripts/validate-all-profiles.sh` | ✅ | ⬜ pending |
| 113-03-02 | 03 | 3 | end-to-end census on live box | manual UAT | `km create` + `km shell` + run sections A–F | ❌ W0 (live) | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/compiler/userdata_phase113_test.go` — new test file with `TestUserdataProfileWriteBlockRendered` + `TestUserdataProfileYAMLRoundTrip`
- [ ] Regenerate 3 committed goldens after the profile-write block lands: `testdata/userdata_additional_volume_only.golden.sh`, `testdata/userdata_learn_v2_pre92_baseline.golden.sh`, `testdata/h1_byte_identity_golden.txt`
- [ ] Live UAT setup: a Slack-enabled profile available for `km create` before Plan 113-03 UAT

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Census sections A–F run clean on a live box | phase goal | Requires a provisioned sandbox + SSM; not unit-testable | `km create <slack-profile>` → `km shell <id>` → run the skill's census block; confirm capabilities enumerated, network boundary confirmed (the 1 allowed + 1 blocked curl), sudo state correct |
| Graceful degradation on pre-Phase-113 box | CONTEXT decision | Needs an old sandbox lacking `/opt/km/.km-profile.yaml` | On a sandbox without the file, run census section A — must fall back to env+probes, never error |
| `/opt/km/.km-profile.yaml` present + semantically equivalent | platform deliverable | On-box filesystem check | `km shell <id>` → `cat /opt/km/.km-profile.yaml`; round-trip parse vs `aws s3 cp s3://$BUCKET/artifacts/<id>/.km-profile.yaml -`. NOT byte-identical: the on-box file is `yaml.Marshal` of the parsed struct (formatting/ordering/comments differ from the raw S3 bytes); compare `apiVersion`/`kind`/`metadata.name`/key spec values instead |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 / manual-UAT dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify (live-UAT tasks 113-02-01 / 113-03-02 are inherently manual — flanked by automated unit/smoke tasks)
- [ ] Wave 0 covers all MISSING references (new test file + 3 golden regens)
- [ ] No watch-mode flags
- [ ] Feedback latency < 120s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved 2026-06-14 (plan-checker VERIFICATION PASSED, iteration 2)
