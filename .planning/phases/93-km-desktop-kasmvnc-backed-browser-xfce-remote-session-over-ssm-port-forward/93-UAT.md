---
phase: 93
slug: km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward
status: resolved
updated: 2026-06-02
source: live operator UAT (km v0.3.790→v0.3.799, Ubuntu 24.04 EC2)
direction: OS-aware bootstrap (keep Ubuntu) — operator decision 2026-06-02
---

## ✅ RESOLVED — end-to-end verified on a clean boot (km v0.3.799)

A fresh `km create profiles/desktop.yaml` (sandbox `desk-c1cf9494`, Ubuntu 24.04,
no AMI, NO manual patching) reached:
- `cloud-init status: done` (entire userdata runs cleanly on Ubuntu — no AL-ism aborts),
- `kasmvnc` **active**, listening on **`127.0.0.1:8444` (loopback only)**,
- firefox + matchbox + vncserver + kasmvncserver installed, `~/.kasmpasswd` seeded,
- `km desktop status desk-c1cf9494` → "✓ KasmVNC desktop ready".

**Trust model preserved:** loopback-only bind, access exclusively via the SSM
port-forward (`km desktop start`), identical to `km vscode`. **No security-group
change and no public exposure** — apt was switched to HTTPS (443, already allowed)
rather than opening port 80; `network.udp.public_ip` only disables a STUN probe.

### Complete fix list (all committed to pkg/compiler, validated live)
1. OS-detect prelude + `km_pkg_install` (apt/yum/dnf) + `km_ensure_awscli`.
2. AWS CLI v2 extracted with **python3** (Ubuntu lacks `unzip`; apt lock-contended at boot) — stub + full userdata.
3. `amazon-ssm-agent` install/enable guarded to yum hosts (Ubuntu ships it via snap).
4. `git`/`tmux`/`iptables`/`efs` routed through `km_pkg_install`.
5. OS-aware sshd: `ssh.service` + install `openssh-server` on Ubuntu.
6. Ubuntu noble deps: `fonts-dejavu-core` (not `fonts-dejavu`) + `software-properties-common`/`gnupg`/`curl`; KasmVNC deb via `curl` not `wget`.
7. `Acquire::ForceIPv4` (EC2 mirror IPv6 unroutable).
8. **apt over HTTPS** (`km_apt_https`) — SG allows only 443, not port 80.
9. kasmvnc xstartup `XDG_RUNTIME_DIR` fallback (user can't mkdir root-owned `/run/user/<uid>`).
10. kasmvnc.service: `ExecStartPre=+...` (run `/run/user` setup as root) + `HOME`/`XDG_RUNTIME_DIR` env.
11. `vncserver -select-de manual`; snakeoil TLS cert + `sandbox` in `ssl-cert` group; `network.udp.public_ip: 127.0.0.1` (skip STUN hang).

### Remaining operator step (for REMOTE create)
The create-handler Lambda compiles userdata via its bundled `km`. To make the
default **remote** `km create` produce this fixed userdata, redeploy the Lambda:
`make build-lambdas` (clean) then `km init --dry-run=false`. LOCAL create
(`km create --local`) already works with the rebuilt binary. (See
[[project_km_init_skips_existing_lambda_zips]] / [[project_schema_change_requires_km_init]].)

GAP-93-02 (status message) also shipped: `km desktop status` now distinguishes
"still installing" from "not enabled" via a unit-file + cloud-init probe.

### Follow-up: eBPF enforcement on Ubuntu (validated 2026-06-02)
Investigated + fixed + live-verified. The enforcer is portable (embedded bpf2go
bytecode, no clang/CO-RE/libbpf/pkg deps), but its DNS resolver binds
`127.0.0.1:53`, which collides with Ubuntu's `systemd-resolved`. Fix (eBPF
userdata block, `command`/`-L`-guarded, no-op on AL): stop+disable
systemd-resolved and break the `/etc/resolv.conf` symlink before the
`km-ebpf-enforcer` service starts. Live test (`ubuntu-24.04` + `enforcement:
ebpf`, sandbox `desk-2e68131a`): cloud-init `done`, enforcer active, all 4 cgroup
BPF programs attached (connect4/sendmsg4/sockops/egress), DNS allowlist enforced
(s3.amazonaws.com + api.anthropic.com resolve; github.com + example.com blocked
with `"allowed":false` audit). **Both proxy and eBPF modes now work on Ubuntu.**
Commit: `fix(93): free :53 for eBPF resolver on Ubuntu`.

### Removed: unused configui web dashboard (2026-06-02)
`cmd/configui` + `docs/configui-guide.md` deleted; CFUI-01..04 marked removed.
Was never deployed in the operator workflow; its test had drifted at Phase 92.

### Deferred follow-up (not desktop-blocking): SSM tunnel auto-reconnect
`km desktop`/`km vscode` raw port-forwards have no keep-alive; a dropped SSM
socket shows as a frozen desktop (the KasmVNC session itself survives server-side
— only the tunnel dies). Recommended fix: wrap the port-forward in a
reconnect/health-probe loop (KasmVNC is the durable layer, like tmux is for
`km agent`). Custom-doc `idleSessionTimeout` 20→60 is a cheap freebie but does
NOT affect port-forwards (those use the AWS-managed StartPortForwardingSession
doc + account-level idle preference).


## Resolution log (Phase 93.1 — OS-aware bootstrap)

Autonomous work session 2026-06-02 (operator away). The Amazon-Linux-only
bootstrap is being ported to Ubuntu issue-by-issue, each surfaced by a live boot:

| Fix | Commit | Status |
|-----|--------|--------|
| GAP-93-01 example profile uncreateable (notification channel) | (earlier) | ✅ resolved |
| OS-detect prelude + km_pkg_install + km_ensure_awscli; ssm-agent guarded; git/tmux/iptables/efs via helper | `fix(93): make EC2 userdata bootstrap OS-aware` | ✅ shipped |
| AWS CLI extraction via python3 (not unzip — absent on Ubuntu, apt lock-contended at boot) — stub + full userdata | `fix(93): extract AWS CLI with python3, not unzip` | ✅ **validated live** (`aws-cli/2.34.59` installed on real Ubuntu box) |
| GAP-93-02 desktop status distinguishes "installing" vs "not enabled" (unit-file + cloud-init probe) | (same) | ✅ shipped + unit-tested |
| OS-aware sshd unit (`ssh.service` on Ubuntu; install openssh-server) | `fix(93): OS-aware sshd unit` | ✅ shipped, live boot pending |

**Live boot progression (each boot got further):**
- v0.3.790 (remote, desk-af0a8271): died at stub `aws: command not found` (Up 77s).
- v0.3.791 (local, desk-779d7f16): stub aws-install ran but failed at `unzip: command not found` (Up 194s).
- v0.3.792 (local, desk-7d2ae20d): **AWS CLI installed OK** (`/usr/local/bin/aws`), sidecars downloading, then aborted at `systemctl enable --now sshd` (Ubuntu unit is `ssh`).
- v0.3.793 (local, desk-7d2ae20d successor): sshd fix in — boot verification in progress.

**Verified to date:** full Go suite green (only pre-existing `TestUnlockCmd` baseline
fails); rendered desktop userdata passes `bash -n`; AWS CLI python3 install
confirmed on a real Ubuntu 24.04 sandbox via SSM. **Remaining:** confirm a clean
boot reaches `kasmvnc active` + `km desktop start` renders Firefox-in-browser;
then deploy compiler to the create-handler Lambda (`make build-lambdas` clean +
`km init --dry-run=false`) so REMOTE create works; destroy test sandboxes;
re-run the `93-VALIDATION.md` manual UAT table.


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
