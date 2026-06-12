---
phase: 106
slug: session-resume-hint-on-github-hackerone-bridge-replies-post-on-mint
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-06-11
---

# Phase 106 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing (`go test`) |
| **Config file** | none — standard Go test runner |
| **Quick run command** | `go test ./pkg/compiler/ -run 'TestUserdata_GitHubInbound|TestUserdataH1' -count=1` |
| **Full suite command** | `go test ./pkg/compiler/ -count=1 -timeout 120s` |
| **Estimated runtime** | ~30 seconds (full pkg/compiler suite) |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/compiler/ -run 'TestUserdata_GitHubInbound|TestUserdataH1' -count=1`
- **After every plan wave:** Run `go test ./pkg/compiler/ -count=1 -timeout 120s`
- **Before `/gsd:verify-work`:** Full pkg/compiler suite must be green
- **Max feedback latency:** ~30 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 106-01-01 | 01 | 0 | RESUME-HINT-TESTS | unit (stubs) | `go test ./pkg/compiler/ -run TestUserdata_GitHubInboundPoller_ResumeHint -count=1` | ❌ W0 | ⬜ pending |
| 106-01-02 | 01 | 0 | RESUME-HINT-TESTS | unit (stubs) | `go test ./pkg/compiler/ -run TestUserdataH1EnabledRendersPoller -count=1` | ✅ extend | ⬜ pending |
| 106-02-01 | 02 | 1 | RESUME-HINT-FORMAT, RESUME-HINT-MINT, RESUME-HINT-GITHUB | unit/contains | `go test ./pkg/compiler/ -run TestUserdata_GitHubInboundPoller_ResumeHint -count=1` | ✅ (W0) | ⬜ pending |
| 106-02-02 | 02 | 1 | RESUME-HINT-SLACK-EXCLUDED | unit/byte-identity | `go test ./pkg/compiler/ -run 'TestUserdataH1ByteIdentity|TestUserdataKmPrefixByteIdentity' -count=1` | ✅ existing | ⬜ pending |
| 106-03-01 | 03 | 1 | RESUME-HINT-FORMAT, RESUME-HINT-MINT, RESUME-HINT-H1 | unit/contains | `go test ./pkg/compiler/ -run TestUserdataH1EnabledRendersPoller -count=1` | ✅ (W0) | ⬜ pending |
| 106-03-02 | 03 | 1 | RESUME-HINT-SLACK-EXCLUDED | unit/byte-identity | `go test ./pkg/compiler/ -run TestUserdataH1ByteIdentity -count=1` | ✅ existing | ⬜ pending |
| 106-04-01 | 04 | 2 | RESUME-HINT-DOCS | manual (doc review) | — (grep `## Phase 106` in docs) | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `pkg/compiler/userdata_github_inbound_test.go` — add `TestUserdata_GitHubInboundPoller_ResumeHint` (+ `…_MintCondition`) asserting:
  - `<details>` / `🔧 Resume` fold present in GitHub poller when GH-inbound enabled
  - both `claude --resume` and `codex exec resume` branches rendered
  - `SANDBOX_ID` referenced and `/workspace` string present in the hint body
  - `|| true` non-blocking guard present
  - mint condition present (`!= "${GITHUB_SESSION:-}"` style guard) so the hint posts on first-mint / re-mint only
- [ ] `pkg/compiler/userdata_h1_byte_identity_test.go` — extend `TestUserdataH1EnabledRendersPoller` `wantSubstrings` with the hint markers (`claude --resume` / `codex exec resume`, `<details>`/`🔧 Resume`, the `km-h1` internal-comment call with no `--reply-to-researcher`)
- [ ] No new framework install — Go test runner already present

*The byte-identity guards (`TestUserdataH1ByteIdentity`, `TestUserdataKmPrefixByteIdentity`) already exist and MUST stay green — they are the Slack-exclusion / dormancy non-regression invariant. Run them after edits; no golden recapture expected.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Docs carry a correct, consistent Phase 106 entry | RESUME-HINT-DOCS | Prose accuracy / operator-facing consistency not unit-assertable | `grep -n "## Phase 106" docs/github-bridge.md docs/h1-bridge.md` returns a hit in each; reviewer confirms run-from `/workspace`, post-on-mint semantics, H1-internal note, and the `make build-lambdas` + `km init --dry-run=false` + recreate deploy line |
| Live resume comment renders + the pasted command actually re-attaches | RESUME-HINT-GITHUB, RESUME-HINT-H1 | Requires a live github-bot / h1 sandbox, a real PR/report, and SSM access | (Optional live UAT) Trigger a bridge turn; confirm exactly ONE collapsed resume fold on first turn, none on a same-session follow-up; copy the command, `cd /workspace`, run it, confirm the session re-attaches. On H1, confirm the hint is on the INTERNAL comment only |

*Live UAT is optional confirmation; the golden + contains tests are the gating automated coverage.*

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
