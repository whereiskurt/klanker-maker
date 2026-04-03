---
phase: 42-ebpf-gatekeeper-mode-connect4-dnat-rewrite-for-selective-l7-proxy
plan: "03"
subsystem: compiler/userdata, ebpf
tags: [e2e, gatekeeper, both-mode, deploy, verification, bugfix]

# Dependency graph
requires:
  - plan: 42-01
    provides: Dual-PID exemption, --proxy-pid flag, buildL7ProxyHosts
  - plan: 42-02
    provides: Both-mode gatekeeper template (connect4 replaces iptables DNAT)

provides:
  - E2E verification of gatekeeper mode with goose-ebpf-gatekeeper profile
  - Fix for /run/km ownership (km-sidecar can write PID file)
  - Fix for --proxy-pid systemd argument (ExecStartPre + EnvironmentFile pattern)
  - Toolchain deployment documentation (ARM64 vs AMD64 binary paths)
---

## What was done

### Task 1: Build, verify profile, upload km binary + sidecars
- `profiles/goose-ebpf-gatekeeper.yaml` confirmed with `enforcement: both`
- `make build` succeeded
- `go test ./pkg/ebpf/... ./pkg/compiler/...` — all pass
- `make sidecars` uploaded all binaries to S3
- Pre-existing test failure `TestUnlockCmd_RequiresStateBucket` deferred

### Task 2: E2E gatekeeper mode verification
Two bugs found and fixed during E2E testing:

**Bug 1: /run/km ownership** (commit ce37963)
- `/run/km/` was created by bootstrap as root:root 755
- `km-http-proxy` runs as `km-sidecar`, ExecStartPost writes PID to `/run/km/http-proxy.pid`
- Permission denied → systemd marks service as failed → `set -e` kills bootstrap
- Fix: `chown km-sidecar:km-sidecar /run/km` after `mkdir -p`

**Bug 2: --proxy-pid systemd shell expansion** (commit f2e8642)
- `$(cat /run/km/http-proxy.pid)` inside `<< 'UNIT'` heredoc — no shell expansion
- Systemd ExecStart also doesn't expand `$(...)`
- Literal string passed as --proxy-pid value, next flag `--tls` parsed as PID → parse error
- Fix: ExecStartPre writes PID to env file, EnvironmentFile loads it, ExecStart uses `${KM_HTTP_PROXY_PID}`

**E2E Results — 2 full create/verify/destroy cycles passed:**
- cloud-init: done ✓
- km-ebpf-enforcer: active, firewall_mode=block, dns_port=53 ✓
- km-http-proxy: active, PID captured correctly ✓
- DNS: resolves via enforcer's DNS resolver on 127.0.0.1:53 ✓
- evil.com: blocked (no response) ✓
- No iptables DNAT rules ✓
- Allowed git clone (whereiskurt/meshtk): success ✓
- Blocked git clone (torvalds/linux): failed (blocked by proxy) ✓

## Self-Check: PASSED

## Key files

### key-files.created
- docs/worktree-setup.md

### key-files.modified
- pkg/compiler/userdata.go (chown /run/km + ExecStartPre proxy-pid pattern)

## Deviations
- Two bugs discovered during E2E required in-place fixes to userdata template
- Toolchain binary path (s3://toolchain/km) is ARM64 for Lambda, separate from sidecars (AMD64 for EC2)
- Documentation added for worktree setup (gitignored files needed for km commands)
