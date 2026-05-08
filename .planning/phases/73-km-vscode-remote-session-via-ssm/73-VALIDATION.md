---
phase: 73
slug: km-vscode-remote-session-via-ssm
status: complete
nyquist_compliant: true
wave_0_complete: true
created: 2026-05-06
completed: 2026-05-08
---

# Phase 73 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.
> Phase 73 ships km's first VS Code Remote-SSH integration. Sandbox-side
> changes are bash-only (sshd enable + authorized_keys); operator-side
> changes are Go (keypair generation + ssh-config block management +
> two-subcommand cobra wiring + create/destroy hook-in).

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test (Go 1.21+) |
| **Config file** | go.mod / go.sum |
| **Quick run command** | `go test ./internal/app/cmd/ -run "TestVscode\|TestSSHConfig" -count=1 && go test ./pkg/sshkey/... -count=1` |
| **Full suite command** | `go test ./... -count=1` |
| **Estimated runtime** | quick ~6s, full ~90s |

---

## Sampling Rate

- **After every task commit:** Run quick command (vscode + sshconfig + sshkey scoped tests)
- **After every plan wave:** Run full suite
- **Before `/gsd:verify-work`:** Full suite green + manual smoke test (Section "Manual-Only Verifications")
- **Max feedback latency:** 90 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Concern | Test Type | Automated Command | File Exists | Status |
|---------|------|------|---------|-----------|-------------------|-------------|--------|
| 73-00-01 | 00 | 0 | pkg/sshkey stub compiles, tests skip | unit | `go test ./pkg/sshkey/... -count=1` | ✅ | ✅ |
| 73-00-02 | 00 | 0 | sshconfig+vscode stubs compile, tests skip | unit | `go test ./internal/app/cmd/ -run "TestSSHConfig\|TestVSCodeStart\|TestVSCodeStatus" -count=1` | ✅ | ✅ |
| 73-00-03 | 00 | 0 | profile+compiler stubs compile, tests skip | unit | `go test ./pkg/profile/... ./pkg/compiler/... -count=1` | ✅ | ✅ |
| 73-01-01 | 01 | 1 | GenerateAndWrite implementation compiles | unit | `go build ./pkg/sshkey/... && go vet ./pkg/sshkey/...` | ✅ | ✅ |
| 73-01-02 | 01 | 1 | All 7 keygen tests pass (mode, parse, comment, newline, parentdir, idempotent) | unit | `go test ./pkg/sshkey/... -count=1 -v` | ✅ | ✅ |
| 73-02-01 | 02 | 1 | VSCodeEnabled field + IsVSCodeEnabled helper added | unit | `go build ./pkg/profile/... && go vet ./pkg/profile/...` | ✅ | ✅ |
| 73-02-02 | 02 | 1 | TestVSCodeEnabled_DefaultTrue + TestVSCodeEnabled_False pass; JSON schema valid | unit | `go test ./pkg/profile/... -count=1 -run "TestVSCodeEnabled" -v && python3 -c "import json; json.load(open('pkg/profile/schemas/sandbox_profile.schema.json'))"` | ✅ | ✅ |
| 73-03-01 | 03 | 1 | UpsertHost production impl builds (atomicWrite, managedSections, parseHostBlocks) | unit | `go build ./internal/app/cmd/... && go vet ./internal/app/cmd/...` | ✅ | ✅ |
| 73-03-02 | 03 | 1 | RemoveHost production impl builds; idempotent paths handled | unit | `go build ./internal/app/cmd/... && go vet ./internal/app/cmd/...` | ✅ | ✅ |
| 73-03-03 | 03 | 1 | All 9 TestSSHConfig_* tests pass (upsert/remove/preserve/idempotent cases) | unit | `go test ./internal/app/cmd/ -run TestSSHConfig -count=1 -v` | ✅ | ✅ |
| 73-04-01 | 04 | 2 | NetworkConfig.VSCodeSSHPubKey field added to service_hcl.go | unit | `go build ./pkg/compiler/... && grep -n "VSCodeSSHPubKey" pkg/compiler/service_hcl.go` | ✅ | ✅ |
| 73-04-02 | 04 | 2 | userDataParams gains VSCodeEnabled+VSCodeSSHPubKey; generateUserData validates missing pubkey | unit | `go build ./pkg/compiler/... && grep -n "VSCodeEnabled\|VSCodeSSHPubKey" pkg/compiler/userdata.go | head -10` | ✅ | ✅ |
| 73-04-03 | 04 | 2 | Template conditional block emits sshd/authorized_keys/restorecon when VSCodeEnabled=true | unit | `go build ./pkg/compiler/... && grep -n "VSCodeEnabled\|VSCodeSSHPubKey\|restorecon" pkg/compiler/userdata.go | head -10` | ✅ | ✅ |
| 73-04-04 | 04 | 2 | All 4 TestUserDataVSCode* tests pass including column-0 pubkey + missing-key error | unit | `go test ./pkg/compiler/... -run TestUserDataVSCode -count=1 -v` | ✅ | ✅ |
| 73-05-01 | 05 | 3 | km create generates keypair + threads pubkey to NetworkConfig; build passes | unit | `make build && ./km validate profiles/learn.yaml` | ✅ | ✅ |
| 73-06-01 | 06 | 4 | runVSCodeStart implemented; binary builds; vet clean | unit | `make build && ./km --help && go vet ./internal/app/cmd/...` | ✅ | ✅ |
| 73-06-02 | 06 | 4 | runVSCodeStatus + root.go registration; km vscode subcommand discoverable | unit | `make build && ./km vscode --help && ./km vscode start --help && ./km vscode status --help` | ✅ | ✅ |
| 73-06-03 | 06 | 4 | All 6 TestVSCode* tests pass (missing-key, port-forward args, output, status cases) | unit | `go test ./internal/app/cmd/ -run "TestVSCodeStart\|TestVSCodeStatus" -count=1 -v` | ✅ | ✅ |
| 73-07-01 | 07 | 2 | km destroy cleanup block added; RemoveHost + key file deletion; build passes | unit | `make build && ./km destroy --help` | ✅ | ✅ |
| 73-08-01 | 08 | 5 | docs/vscode.md exists with ≥ 80 lines | doc | `test -f docs/vscode.md && wc -l docs/vscode.md | awk '{ if ($1 < 80) exit 1 }'` | ✅ | ✅ |
| 73-08-02 | 08 | 5 | CLAUDE.md gains km vscode CLI entries + VS Code Remote-SSH section | doc | `grep -c "km vscode start\|VS Code Remote-SSH (Phase 73)" CLAUDE.md | awk '{ if ($1 < 2) exit 1 }'` | ✅ | ✅ |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [x] `pkg/sshkey/keygen_test.go` — failing stubs for `GenerateAndWrite` (file modes, idempotency, pubkey parses back via `ssh.ParseAuthorizedKey`, comment field embedded correctly, parent dir created with 0700 if missing)
- [x] `internal/app/cmd/sshconfig_test.go` — failing stubs for the managed-block parser/writer (no file / empty file / no markers / markers + entries / multiple entries / preserve content outside markers / Host alias collision / line-ending handling LF vs CRLF)
- [x] `internal/app/cmd/vscode_test.go` — failing stubs for `start` and `status` (flag parsing, error messages for missing private key locally, SSM command construction for the status checks, port-forward command construction)
- [x] `pkg/profile/types_test.go` (or `validate_test.go`) — failing stubs for `vscodeEnabled` default-true semantics + nil-CLI handling
- [x] `pkg/compiler/userdata_test.go` — failing stubs for the conditional userdata block (emits when `VSCodeEnabled: true`, omits when `false`, embedded pubkey content is correct, `restorecon` line is present)

*Wave 0 is mandatory because the phase introduces multiple new code surfaces (`pkg/sshkey/`, `internal/app/cmd/sshconfig.go`, `internal/app/cmd/vscode.go`, schema field, userdata template branch). Stubs lock contracts before implementation.*

---

## Manual-Only Verifications

| Behavior | Why Manual | Test Instructions |
|----------|------------|-------------------|
| End-to-end: operator's local desktop VS Code → sandbox via Remote-SSH | Requires real EC2 + SSM + local VS Code + Remote-SSH extension + browser-driven UX | 1. `make build && km init --sidecars` 2. `km create profiles/learn.yaml --alias vscode-smoke` 3. `km vscode start <sb>` (terminal blocks; copy the printed Host alias) 4. In VS Code: F1 → "Remote-SSH: Connect to Host..." → pick `km-<sb-id>` 5. Confirm vscode-server installs (~50MB on first connect) 6. File → Open Folder → `/workspace` 7. Edit a file, save 8. Ctrl-C the `km vscode start` terminal 9. `km shell <sb> -- cat /workspace/<edited-file>` confirms persistence |
| Per-sandbox keypair lifecycle: create generates, destroy cleans up | Requires real `km create` + `km destroy` against an actual sandbox | 1. `ls ~/.km/keys/ | grep sb-<id>` before create — should be absent 2. `km create profiles/learn.yaml --alias kp-test` 3. `ls ~/.km/keys/sb-<id>*` — should show private (mode 0600) and public (mode 0644) 4. `km destroy <sb> --remote --yes` 5. `ls ~/.km/keys/sb-<id>*` — should error "no such file" |
| `~/.ssh/config` managed-block lifecycle | Requires inspection of an actual `~/.ssh/config` file | 1. Backup current `~/.ssh/config` 2. `km vscode start <sb>` (Ctrl-C immediately after the host block prints) 3. Inspect `~/.ssh/config` — block between markers exists with correct Host alias 4. `km destroy <sb>` 5. Inspect `~/.ssh/config` — Host entry removed; markers removed if no entries left; surrounding content unchanged |
| `vscodeEnabled: false` produces clean error | Requires sandbox provisioned with flag false | 1. Create profile YAML with `spec.cli.vscodeEnabled: false` 2. `km create` it 3. `km vscode start <sb>` should fail with "VS Code not enabled in this sandbox's profile (set `spec.cli.vscodeEnabled: true` and recreate)" — not a raw SSM error |
| Local port collision behavior | Requires another process on operator laptop bound to 2222 | 1. `nc -l 2222 &` on operator laptop 2. `km vscode start <sb>` → expect bind error from `aws ssm start-session` 3. `km vscode start <sb> --local-port 9222` → succeeds 4. Verify `~/.ssh/config` Host entry's `Port` line reads `9222` 5. `kill %1` cleanup |
| Cross-machine key portability gap is informative, not silent | Documented limitation requires actual two-machine test | 1. `km create` on machine A (key files written to A's `~/.km/keys/`) 2. Sync code to machine B (or just clone the repo) 3. `km vscode start <sb>` from machine B → expect helpful error pointing at the missing key file location and suggesting copy |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references (sshkey package, sshconfig parser, vscode subcommand, profile schema, userdata template)
- [x] No watch-mode flags
- [x] Feedback latency < 90s
- [x] `nyquist_compliant: true` set in frontmatter (after planner populates Per-Task Verification Map)

**Approval:** approved 2026-05-08
