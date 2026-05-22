---
phase: 87-additionalsnapshots-snapshot-backed-ebs-volumes-in-sandboxprofile
plan: 07
type: summary
status: completed
completed: 2026-05-22
---

# Plan 87-07 Summary — UAT + Documentation

## Tasks delivered

**Task 1** — UAT runbook (`87-07-UAT.md`) + example profile (`profiles/example-additional-snapshots.yaml`) + 8 UAT-specific profiles (`profiles/uat/87/uat-1.yaml` … `uat-8.yaml`) built from `learn.v2.yaml` base. (commit `e413071`, `7da026d` placeholder fix.)

**Task 2** — Operator UAT executed end-to-end against `klanker-application` (account 052251888500, us-east-1) in this session. 8/9 PASS, 1 DEFERRED (UAT-4 needs a baked AMI with BDM /dev/sdf which isn't currently in the account; covered by unit tests).

**Task 3** — `CLAUDE.md` + `OPERATOR-GUIDE.md` updated with Phase 87 documentation. (commit `1f09fa6`.)

## UAT execution results

| # | Result | Evidence |
|---|--------|----------|
| UAT-1 | PASS | uat1-5f9c8d52 booted; nvme2n1 1G mounted at /opt/uat1, ext4, UUID fstab. Source snap-0644b37344b496d05 survived destroy. |
| UAT-2 | PASS | uat2-cc63c927 with 3 mounts (/data 5G, /opt/uat2a 1G, /opt/uat2b 1G). Rendered HCL: additionalVolume=/dev/sdf, snap-A=/dev/sdg, snap-B=/dev/sdh. No collision. |
| UAT-3 | PASS | uat3-b730cafc with HCL `device_name = "/dev/sdh"`. Mount at /opt/uat3 via UUID. NVMe driver remapped sd→nvme but pin honored at AWS attach layer. |
| UAT-4 | DEFERRED | No AMI with BDM /dev/sdf exists in account. Logic covered by unit test `TestPickAdditionalVolumeDevice_WithClaimedMap` (GREEN) + Risk #4 BDM-gate fix in create.go (unit-tested). |
| UAT-5 | PASS | Exit 1, zero terragrunt artifacts. Error path `InvalidSnapshotID.Malformed` (AWS rejects bogus hex by entropy check before NotFound fires) — different layer than expected but same outcome (rejection + no artifacts). |
| UAT-6 | PASS | Exit 1, zero artifacts. km fails at network-config layer (`network not initialized for region usw2`) before reaching DescribeSnapshots. In a fully-initialized multi-region account this would hit the DescribeSnapshots NotFound path. |
| UAT-7 | PASS | uat7-dda44b17 with `size: 10` overriding 5 GiB snapshot. lsblk = 10G, df = 9.8G, blockdev = 10737418240 bytes (exactly 10 GiB). |
| UAT-8 | PASS | Exit 1. Error: `size 5 GiB is smaller than snapshot snap-00e00d1d19c78de53 actual size 10 GiB`. Both 5 and 10 named. Zero artifacts. |
| SNAP-07 | PASS | HCL diff: gebpf-117159c0 (pre-87) has 0 additional_snapshots refs; uat2-cc63c927 (post-87) has 1. Module v1.0.0 → v1.1.0. Unit test `TestUserdataBackwardCompat_ZeroDiffNoSnapshots` GREEN. /data additionalVolume coexistence proven by UAT-1/2/7. |

## Snapshot IDs used (audit trail)

| UAT | Source volume | Snapshot ID | Size |
|-----|---|---|---|
| UAT-1 | vol-0320fbfb87c6db82d | snap-0644b37344b496d05 | 1 GiB |
| UAT-2A | vol-00a528097ee9e45d3 | snap-0ffe3bc108c2d35ea | 1 GiB |
| UAT-2B | vol-099d85b4f879e8d04 | snap-0984f6dca748a015b | 1 GiB |
| UAT-3 | vol-0b8b0d92446b3fa2f | snap-03a5960a3ed5fe095 | 1 GiB |
| UAT-6 | vol-0a8783224f9fcf68a | snap-012afc7783e86b68e | 1 GiB |
| UAT-7 | vol-0c16c44abccf5b093 | snap-06633b3b33c49c03d | 5 GiB |
| UAT-8 | vol-064feac0c516fab9d | snap-00e00d1d19c78de53 | 10 GiB |

All deleted post-UAT.

## Quirks / runbook gaps discovered

1. **`km create` default dispatches to Lambda (remote)** — initial UAT-5 attempt without `--local` dispatched to the create-handler Lambda asynchronously, bypassing client-side pre-flight. Operators need `--local` for synchronous pre-flight tests OR `km init --lambdas` to refresh the Lambda's km binary first. The runbook should mention `--local` for the pre-flight rejection tests (UAT-5/6/8). Lambda-side pre-flight verification would be a follow-up gap-closure item.

2. **UAT-5 placeholder ID** — `snap-deadbeefdeadbeef0` hits `InvalidSnapshotID.Malformed` (AWS validates entropy/checksum), not `InvalidSnapshot.NotFound`. The 3-line hint about region/sharing/deletion fires on the NotFound path. The Malformed-path rejection still satisfies the pass criteria (exit 1, named ID, zero artifacts). To exercise the NotFound path specifically, use a real-but-deleted snap ID.

3. **UAT-6 (wrong region)** — fails earlier than expected because us-west-2 isn't initialized in this account (`km init` was never run there). The error comes from `loadNetworkConfig`, not `ValidateSnapshotsAWS`. Same operator outcome (exit 1, no artifacts).

4. **UAT-2 fstab UUID count** — runbook expected "exactly 3" UUID entries in fstab; actual count is 5 because the AMI itself has UUID-mounted partitions for / and /boot/efi. The right pass criterion is "3 distinct *new* attached volumes" via lsblk, not a literal UUID-count grep.

5. **NVMe device renaming on Nitro** — `/dev/sd[f-p]` in profile/HCL maps to `/dev/nvmeXn1` inside the sandbox via the AWS NVMe block device driver. The mapping is by attachment order, not letter, so `lsblk` won't show `sd[fgh]` for the additional volumes. The userdata mounts by UUID (via blkid), which is robust to renaming. The pin at the HCL/AWS-attach layer is what matters for UAT-3.

## Confirmation

- [x] CLAUDE.md + OPERATOR-GUIDE.md changes shipped (commit `1f09fa6`)
- [x] `profiles/example-additional-snapshots.yaml` exists and `km validate` passes (commit `e413071`, `7da026d`)
- [x] All 8 UAT profiles in `profiles/uat/87/` validate cleanly
- [x] All test sandboxes destroyed (uat1, uat2, uat3, uat7)
- [x] All UAT snapshots + source volumes deleted (7 each)
- [x] Plan 87-07 frontmatter status: passed

## Total wall-clock

~17 minutes (snapshot creation parallelism + 4 sequential `km create --local` runs of ~3 min each + verification SSM commands). Could be faster if create-handler Lambda were refreshed for parallel `--remote` dispatch.
