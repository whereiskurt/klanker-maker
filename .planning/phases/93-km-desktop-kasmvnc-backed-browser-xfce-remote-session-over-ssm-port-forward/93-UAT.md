---
phase: 93
slug: km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward
status: failed
updated: 2026-06-02
source: live operator UAT (km v0.3.790, sandbox desk-af0a8271, Ubuntu 24.04 EC2)
direction: OS-aware bootstrap (keep Ubuntu) — operator decision 2026-06-02
---

# Phase 93 — UAT Findings (live)

Live UAT of the desktop feature on a real remote sandbox. **All 47 autonomous
Desktop tests pass, but the feature does not work end-to-end on a real Ubuntu
sandbox.** The autonomous tests only assert that the correct strings appear in
the rendered userdata; they never execute the script on Ubuntu. The live boot
exposed a blocking platform-integration gap plus two smaller correctness bugs.

Test sandbox `desk-af0a8271` (profile `profiles/desktop.yaml`, `ubuntu-24.04`,
`mode: kiosk`, `browsers: [firefox]`) reached EC2 `running` but cloud-init
**errored at 77s** — KasmVNC never installed. Sandbox destroyed after diagnosis.

## Gaps

### GAP-93-01 — `km validate` (WARN) vs `km create` (ERROR) severity mismatch
- **status:** resolved (inline, 2026-06-02)
- **detail:** `profiles/desktop.yaml` with both notification channels disabled
  passed `km validate` (and `scripts/validate-all-profiles.sh`) with only a
  WARNING, but `km create` rejected it as a hard ERROR ("no notification channel
  will deliver"). The shipped example was not createable.
- **fix applied:** set `notification.email.enabled: true` in
  `profiles/desktop.yaml` (re-validates clean; inventory still 21/21 green).
- **residual follow-up:** the validate-vs-create severity divergence is latent
  for ANY profile, not just desktop. Decide a single source of truth — either
  `km validate` should also ERROR on "no delivery channel" (so the inventory
  gate catches it), or `km create` should downgrade to a warning. Recommend the
  former. (Small; can fold into gap-closure or a separate hygiene fix.)

### GAP-93-02 — `km desktop status` misreports during install/boot
- **status:** failed (needs fix)
- **detail:** `parseDesktopStatus` (mirrors `parseVSCodeStatus`) returns
  *"desktop not enabled in this sandbox's profile — set
  spec.runtime.desktop.enabled: true and recreate the sandbox"* whenever the
  KasmVNC unit is inactive AND `~/.kasmpasswd` is absent. During a normal fresh
  boot (install still running) BOTH are legitimately absent, so the operator is
  told to recreate a perfectly good (still-installing) sandbox.
- **fix direction:** distinguish "enabled-but-not-ready-yet" from
  "not-enabled-in-profile" — e.g. probe a desktop marker that the userdata
  writes early (before the heavy install), or check cloud-init status, or soften
  the message to "KasmVNC not ready yet (still installing?) — check
  `km desktop status` again shortly / inspect cloud-init". Add a status test
  covering the still-installing state.

### GAP-93-03 — Platform userdata bootstrap is Amazon-Linux-only; desktop targets Ubuntu (BLOCKER)
- **status:** failed (blocker — root cause of UAT failure)
- **direction chosen:** make the bootstrap OS-aware and KEEP Ubuntu (operator
  decision 2026-06-02). Reject: pivot to AL2023.
- **detail (two independent failures, one root cause):**
  1. **Remote-create bootstrap stub needs the AWS CLI, absent on Ubuntu.**
     Desktop userdata is ~95KB (> the 12KB EC2 user-data limit), so
     `runCreateRemote` ships a small stub that `aws s3 cp`s the full script from
     `s3://.../artifacts/<id>/km-userdata.sh`. Ubuntu 24.04 base AMIs have **no
     `aws` CLI** (Amazon Linux 2023 does — which is why this never bit before).
     Cloud-init died: `part-001: line 18: aws: command not found`, cloud-init
     `status: error`, Up 77s. The full userdata never ran.
  2. **The full userdata is Amazon-Linux-only.** It opens with
     `set -euo pipefail` then `yum install -y amazon-ssm-agent` (line 11), makes
     ~20 bare `aws ...` calls with no AWS-CLI install step, and uses `dnf`/`yum`
     for tmux/iptables. On Ubuntu the first `yum` aborts the whole script. The
     (correct, apt-based) Phase 93 KasmVNC block at ~line 1495 never executes.
- **why autonomous tests missed it:** golden tests assert the KasmVNC strings
  exist in the rendered template; nothing executes the script on Ubuntu, and the
  Phase 93 plan/research mirrored the KasmVNC install correctly but assumed the
  surrounding pipeline already supported Ubuntu. It does not.
- **fix scope (OS-aware bootstrap):**
  - **remote-create stub** (`pkg/compiler` userdata stub generation +
    `internal/app/cmd/create*.go`): install/bootstrap the AWS CLI when absent
    before `aws s3 cp` (e.g. AWS CLI v2 bundled installer from
    `awscli.amazonaws.com` — already covered by the `.amazonaws.com` allowlist —
    needs `unzip`; or a curl + presigned-URL download that needs no CLI). Make
    the credential-wait loop tolerate the CLI being installed first.
  - **full userdata** (`pkg/compiler/userdata.go`): OS-detect (Ubuntu vs
    AL2023) and branch package manager (`apt-get` vs `yum`/`dnf`), SSM-agent
    handling (Ubuntu ships amazon-ssm-agent via snap; the explicit
    `yum install amazon-ssm-agent` must be skipped/guarded on Ubuntu), AWS CLI
    presence (install on Ubuntu), tmux/iptables install, and CA-bundle path
    (already partly handled at line ~2211).
  - **network allowlist (likely GAP-93-03b, unconfirmed — install failed before
    reaching it):** the example `desktop.yaml` `enforcement: proxy` allowlist
    lists `.mozilla.*`/`.firefox.com` but NOT the Ubuntu apt mirrors
    (`archive.ubuntu.com`, `security.ubuntu.com`, `*.ec2.archive.ubuntu.com`),
    Launchpad PPA hosts (`ppa.launchpadcontent.net`/`launchpad.net`), or the
    KasmVNC GitHub release host (`github.com`/`objects.githubusercontent.com`).
    Even once the bootstrap is OS-aware, a fresh non-AMI first boot under proxy
    enforcement will fail to fetch packages unless these are allowlisted (or the
    AMI is pre-baked). Add them to the example, or make the example AMI-based,
    and document clearly. Re-verify on the next live boot.
  - **dual-OS regression:** add coverage/verification that the rendered userdata
    is executable on BOTH Amazon Linux 2023 and Ubuntu 24.04 (golden tests can't
    catch runtime; needs at least a lint/shellcheck-per-OS-branch and a live
    boot of each in UAT).

## Autonomous status (for reference — all GREEN)
- 47 Desktop tests pass, 0 skips. `go test ./...` clean except the known
  pre-existing `TestUnlockCmd_RequiresStateBucket` (AWS SSO expiry, unrelated).
- `scripts/validate-all-profiles.sh`: 21/21 valid.
- `plugin.json` + `marketplace.json` bumped 0.3.0 → 0.4.0 in lockstep.

## Recommended next step
`/gsd:plan-phase 93 --gaps` (gap-closure cycle reads this file) — research the
OS-aware bootstrap (AWS CLI bootstrap method on Ubuntu, apt/yum branching,
ssm-agent, allowlist, dual-OS verification), then plan + execute, then a fresh
live UAT boot.
