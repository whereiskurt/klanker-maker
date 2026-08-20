---
phase: 125
slug: per-profile-private-subnet-sandboxes-with-per-az-nat-gateway
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-08-19
---

# Phase 125 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` (stdlib), `go test` |
| **Config file** | none — standard `go.mod` (`go 1.25.5`) |
| **Quick run command** | `go test ./pkg/compiler/... ./pkg/capacity/... ./internal/app/config/... -count=1` |
| **Full suite command** | `go test ./... -count=1 -timeout 600s` |
| **Estimated runtime** | ~35s quick · ~10min full |

**KNOWN FOOTGUN (repo-specific):** never judge green from a pipe. `go test ... | tail`
returns tail's exit code and masks a real FAIL. Capture the command's own exit status
(redirect + `$?`, `PIPESTATUS`, or `set -o pipefail`) and read the `ok`/`FAIL` summary.
This caused a false "green" and a premature push on Phase 105.

The whole-repo `./...` suite has been green since 2026-06-13 — a FAIL means a real
regression, not pre-existing debt.

---

## Sampling Rate

- **After every task commit:** targeted `go test ./<pkg>/ -run <NewTest> -count=1`
- **After every plan wave:** `go test ./internal/app/cmd/... ./internal/app/config/... ./pkg/compiler/... -count=1`
- **Before `/gsd-verify-work`:** `go test ./... -count=1 -timeout 600s` green
  **AND** `scripts/validate-all-profiles.sh` exit 0
- **Max feedback latency:** ~35 seconds (quick), 600s cap on full

---

## Per-Task Verification Map

| Task ID | Wave | Requirement | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|-------------|-----------------|-----------|-------------------|-------------|--------|
| 125-W0-* | 0 | all | N/A | unit | see Wave 0 Requirements below | ❌ W0 | ⬜ pending |
| 125-SUBPIN | 1 | REQ-125-EC2SPOT | N/A | unit | `go test ./pkg/terragrunt/ -count=1` | ❌ W0 | ⬜ pending |
| 125-NETMOD | 1 | REQ-125-NETMOD | no public IPv4 on private ENI | manual/live | `terragrunt plan` in `infra/live/use1/network/` | N/A live | ⬜ pending |
| 125-EC2SPOT | 1 | REQ-125-EC2SPOT | `associate_public_ip=false` honored | unit | `go test ./pkg/compiler/ -run TestCompileEC2 -count=1` | ✅ `pkg/compiler/compiler_test.go` | ⬜ pending |
| 125-TOGGLE-CFG | 1 | REQ-125-TOGGLE | boolean-only, no injection surface | unit | `go test ./internal/app/config/ -run TestLoad -count=1` | ✅ `internal/app/config/config_test.go` | ⬜ pending |
| 125-TOGGLE-SCHEMA | 1 | REQ-125-TOGGLE | JSON Schema `type: boolean` | unit | `go test ./pkg/profile/ -count=1` + `scripts/validate-all-profiles.sh` | ✅ | ⬜ pending |
| 125-PLUMB | 2 | REQ-125-PLUMB | sweep unforked | unit | `go test ./internal/app/cmd/ -run TestCreate -count=1` | ✅ `create_test.go` | ⬜ pending |
| 125-RANKAZ | 2 | REQ-125-PLUMB | NAT-less AZ dropped | unit | `go test ./pkg/capacity/ -count=1` | ✅ `pkg/capacity/rankaz_test.go` | ⬜ pending |
| 125-REMOTE | 2 | REQ-125-REMOTE | outputs reach Lambda | unit | `go test ./internal/app/cmd/ -run TestLoadNetworkOutputs -count=1` | ✅ `init_test.go` | ⬜ pending |
| 125-GUARD-CREATE | 3 | REQ-125-GUARD | fail-fast, no half-created box | unit | `go test ./internal/app/cmd/ -run TestCreate.*Private -count=1` | ❌ W0 | ⬜ pending |
| 125-GUARD-INIT | 3 | REQ-125-GUARD | refuse NAT-disable w/ live private boxes | unit | `go test ./internal/app/cmd/ -run TestInit.*NAT -count=1` | ❌ W0 | ⬜ pending |
| 125-GUARD-META | 3 | REQ-125-GUARD | `network_placement` survives pause/resume | unit | `go test ./pkg/aws/ -count=1` | ❌ W0 | ⬜ pending |
| 125-DOCTOR | 3 | REQ-125-DOCTOR | N/A | unit | `go test ./internal/app/cmd/ -run TestDoctorNetwork -count=1` | ❌ W0 | ⬜ pending |
| 125-UAT | 4 | REQ-125-UAT | full egress-path proof | manual/live | operator-run, 7 steps | N/A live | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `internal/app/config/config_test.go` — add `TestLoadNetworkNATGateway_{Set,Unset}`,
      mirroring `TestLoadGithubDefaultRouter_Set`. **This is the merge-list regression
      guard** — without the v2→v merge-list entry the yaml value is silently dropped.
- [ ] `internal/app/cmd/create_test.go` — placement-resolution table test
      (public/private → correct `SandboxSubnets`, and that the AZ sweep reorders the
      resolved list, not `PublicSubnets` specifically).
- [ ] `internal/app/cmd/init_test.go` — extend the existing `public_subnets`
      output-extraction test with `private_subnets` + `nat_gateway_ids`.
- [ ] `internal/app/cmd/doctor_network_test.go` — NEW file. NAT-idle WARN and
      private-without-NAT WARN cases. Mirror `doctor_slack_peer_bridges_test.go`.
- [ ] `pkg/capacity/rankaz_test.go` — extend (file exists) for the NAT-AZ-filter param.
- [ ] `pkg/aws/` — round-trip test for `network_placement` through `SandboxMetadata`
      marshal/unmarshal, mirroring whatever covers `SlackReactAlways`. Confirm the exact
      test file during Wave 0 rather than assuming `sandbox_dynamo_test.go`.
- [ ] `pkg/terragrunt/` — test that the substrate version pin resolves per-substrate
      (ec2spot→v1.3.0, ecs→v1.0.0) and that neither points at a nonexistent directory.
      **This test would have caught the pre-existing ECS break.**

*Existing infrastructure covers everything else.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Additive-only plan on NAT enable | REQ-125-NETMOD | `terraform plan` output is not a `go test` assertion | Flip `network.nat_gateway: true`, run `km init --plan`; expect 4 EIP + 4 NAT + 4 routes + route-table moves, zero destroy-class trips |
| No public IPv4 on private ENI | REQ-125-UAT | requires a real EC2 launch | `km create` private profile, confirm instance has no public IP and a `10.0.10x.0/24` address |
| Egress source IP == that AZ's NAT EIP | REQ-125-UAT | requires real internet egress | from the box, hit an echo service; compare to `nat_eip_public_ips[az_index]` |
| Allowlist still enforced from private subnet | REQ-125-UAT | requires live eBPF/proxy path | `evil.example.com` must still be blocked (proves NAT changed the path, not the filtering) |
| ICE rotation picks a different AZ *and* its NAT | REQ-125-UAT | requires real capacity failure | force ICE, confirm the sweep lands in another AZ using that AZ's NAT |
| Clean NAT teardown | REQ-125-UAT | requires real apply | destroy private box, flip toggle off, `km init`; private subnets survive as routeless islands, public sandboxes unaffected |

---

## Security Domain

Private-subnet placement is **security-positive** — it removes public IPv4 exposure
entirely for opted-in sandboxes. No new attack surface.

| ASVS Category | Applies | Control |
|---|---|---|
| V4 Access Control | Yes | Reduction of exposure, not an addition |
| V5 Input Validation | Marginal | Two plain booleans, JSON Schema `type: boolean` |
| V6 Cryptography | No | No new secrets or crypto material |

**Egress filtering is NOT weakened by NAT.** NAT changes the *path* egress takes (via a
NAT EIP instead of a direct public IP), not *whether* it is filtered. The
`spec.network.egress` allowlist runs at the instance/cgroup level (Security Groups +
eBPF/proxy sidecars), upstream of NAT, and is untouched. UAT step 4 explicitly re-proves
the `evil.example.com` block from a private sandbox.

**Accepted property (not a regression, worth one line in the operator doc):** all
sandboxes in the same AZ share that AZ's NAT EIP as their observed source IP. Inherent to
NAT; not something this phase introduces or mitigates.

---

## Validation Sign-Off

- [ ] All tasks have automated verify or a Wave 0 dependency
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all ❌ references
- [ ] No watch-mode flags
- [ ] Feedback latency < 35s (quick) / 600s (full)
- [ ] Full-suite exit code read directly, never through a pipe
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
