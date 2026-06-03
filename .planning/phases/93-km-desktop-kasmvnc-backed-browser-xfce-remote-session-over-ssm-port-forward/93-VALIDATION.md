---
phase: 93
slug: km-desktop-kasmvnc-backed-browser-xfce-remote-session-over-ssm-port-forward
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-02
---

# Phase 93 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Derived from `93-RESEARCH.md` § Validation Architecture.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` standard library (no external framework) |
| **Config file** | none |
| **Quick run command** | `go test ./pkg/profile/... ./pkg/compiler/... ./internal/app/cmd/... -run Desktop -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | ~30–60 seconds (quick); full suite is the repo norm |

Inventory gate (in addition to `go test`): `bash scripts/validate-all-profiles.sh`.

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/profile/... ./pkg/compiler/... ./internal/app/cmd/... -run Desktop -count=1`
- **After every plan wave:** Run `go test ./... -count=1`
- **Before `/gsd:verify-work`:** Full suite green **AND** `bash scripts/validate-all-profiles.sh` exits 0
- **Max feedback latency:** ~60 seconds

---

## Per-Task Verification Map

Task IDs are assigned by the planner; the Requirement → Test mapping is fixed here.

| Requirement | Test Type | Automated Command | File (to create) | Status |
|-------------|-----------|-------------------|------------------|--------|
| DSK-01-SCHEMA | unit (compile) | `go build ./pkg/profile/...` | `pkg/profile/types.go` | ⬜ pending |
| DSK-02-HELPER | unit | `go test ./pkg/profile/... -run TestIsDesktopEnabled` | `pkg/profile/types_test.go` | ⬜ pending |
| DSK-03-VALIDATE | unit | `go test ./pkg/profile/... -run TestDesktopValidate` | `pkg/profile/validate_test.go` | ⬜ pending |
| DSK-04-SCHEMA-EXPORT | unit + inventory | `go test ./pkg/profile/... -run TestSchema` + `./km validate profiles/desktop.yaml` | `pkg/profile/schemas/*`, `schema_export.go` | ⬜ pending |
| DSK-05-COMPILER-THREAD | unit | `go test ./pkg/compiler/... -run TestUserDataDesktop` | `pkg/compiler/service_hcl.go` | ⬜ pending |
| DSK-06-USERDATA-INSTALL | unit (golden) | `go test ./pkg/compiler/... -run TestUserDataDesktopEnabled` | `pkg/compiler/userdata_test.go` | ⬜ pending |
| DSK-07-USERDATA-SESSION | unit (golden) | `go test ./pkg/compiler/... -run TestUserDataDesktopKiosk` (+`...Full`, `...ChromeBinary`) | `pkg/compiler/userdata_test.go` | ⬜ pending |
| DSK-08-CREDENTIAL | unit | `go test ./internal/app/cmd/... -run TestDesktopCredential` | `internal/app/cmd/create*_test.go` | ⬜ pending |
| DSK-09-CLI-START | unit | `go test ./internal/app/cmd/... -run TestDesktopStart` | `internal/app/cmd/desktop_test.go` | ⬜ pending |
| DSK-10-CLI-STATUS | unit | `go test ./internal/app/cmd/... -run TestDesktopStatus` | `internal/app/cmd/desktop_test.go` | ⬜ pending |
| DSK-11-SECURITY | unit (golden; part of DSK-07) | covered by `TestUserDataDesktopKiosk` (asserts `interface: 127.0.0.1` + `require_ssl: false`) | `pkg/compiler/userdata_test.go` | ⬜ pending |
| DSK-12-PROFILE-EXAMPLE | inventory gate | `bash scripts/validate-all-profiles.sh` | `profiles/desktop.yaml`, `scripts/validate-all-profiles.sh` | ⬜ pending |
| DSK-13-SKILL | manual check | `ls skills/desktop/SKILL.md && grep version .claude-plugin/plugin.json` | `skills/desktop/SKILL.md`, `plugin.json`, `marketplace.json` | ⬜ pending |
| DSK-14-DOCS | manual check | `ls docs/desktop.md` + grep CLAUDE.md row | `docs/desktop.md`, `CLAUDE.md`, `OPERATOR-GUIDE.md` | ⬜ pending |
| DSK-15-TESTS | full suite | `go test ./... -count=1` | (all above) | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/profile/types_test.go` — `TestIsDesktopEnabled` stub (RED/skip)
- [ ] `pkg/profile/validate_test.go` — `TestDesktopValidate*` stubs (RED/skip)
- [ ] `pkg/compiler/userdata_test.go` — `TestUserDataDesktopEnabled`, `TestUserDataDesktopDisabled`, `TestUserDataDesktopKiosk`, `TestUserDataDesktopFull`, `TestUserDataDesktopCredentialSeed`, `TestUserDataDesktopChromeBinary` stubs
- [ ] `internal/app/cmd/desktop_test.go` — new file mirroring `vscode_test.go` (mock fetcher, mock SSM), stubs behind skip
- No framework install needed — Go stdlib testing already in use.

---

## Manual-Only Verifications

These require a live Ubuntu 24.04 EC2 sandbox and a human operator — they cannot
be exercised in CI. They form the operator UAT checkpoint plan (final wave).

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| KasmVNC starts on real sandbox | DSK-06/07 | Needs real EC2 + package install + systemd | `km create profiles/desktop.yaml --wait`; `km desktop status <id>` → ready |
| Browser-in-browser renders | DSK-09 | Needs real GPU-less X session + browser | `km desktop start <id>`; open `https://localhost:8444/`; Firefox visible |
| Bidirectional clipboard | DSK-07/11 | Browser clipboard API + KasmVNC, human eyes | Copy text local→remote Firefox and remote→local; both directions work |
| `km resume` restores session | DSK-06 | Needs pause/resume lifecycle on live infra | `km pause <id>`; `km resume <id>`; `km desktop start` reconnects |
| AMI bake → fast create | DSK-06 | Needs `km ami bake` + relaunch | Bake desktop AMI; create from it; packages present, credential seeded fresh |
| Full XFCE mode renders | DSK-07 | Real desktop session, human eyes | Profile `mode: full`; create; desktop + taskbar visible |
| Locked-network first boot | DSK-11 | Needs real egress enforcement | Locked `spec.network` + no AMI → fails gracefully with clear error |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 60s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
