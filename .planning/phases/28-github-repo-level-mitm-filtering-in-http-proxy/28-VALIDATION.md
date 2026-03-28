---
phase: 28
slug: github-repo-level-mitm-filtering-in-http-proxy
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-03-28
---

# Phase 28 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go testing stdlib (`go test`) |
| **Config file** | none — standard `go test ./...` |
| **Quick run command** | `go test ./sidecars/http-proxy/httpproxy/... -run TestGitHub -v` |
| **Full suite command** | `go test ./sidecars/http-proxy/... ./pkg/compiler/...` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run `go test ./sidecars/http-proxy/httpproxy/... -run TestGitHub -v`
- **After every plan wave:** Run `go test ./sidecars/http-proxy/... ./pkg/compiler/...`
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 28-01-01 | 01 | 1 | ExtractRepoFromPath | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestExtractRepoFromPath` | ❌ W0 | ⬜ pending |
| 28-01-02 | 01 | 1 | IsRepoAllowed | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestIsRepoAllowed` | ❌ W0 | ⬜ pending |
| 28-01-03 | 01 | 1 | GitHubBlockedResponse | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestGitHubBlockedResponse` | ❌ W0 | ⬜ pending |
| 28-02-01 | 02 | 2 | MITM handlers | integration | `go test ./sidecars/http-proxy/httpproxy/... -run TestHTTPProxy_GitHub` | ❌ W0 | ⬜ pending |
| 28-02-02 | 02 | 2 | Blocked repo 403 | integration | `go test ./sidecars/http-proxy/httpproxy/... -run TestHTTPProxy_GitHubBlocked` | ❌ W0 | ⬜ pending |
| 28-02-03 | 02 | 2 | Non-repo passthrough | integration | `go test ./sidecars/http-proxy/httpproxy/... -run TestHTTPProxy_GitHubNonRepo` | ❌ W0 | ⬜ pending |
| 28-02-04 | 02 | 2 | No repos = no MITM | integration | `go test ./sidecars/http-proxy/httpproxy/... -run TestHTTPProxy_GitHubNil` | ❌ W0 | ⬜ pending |
| 28-03-01 | 03 | 2 | Compiler EC2 wiring | unit | `go test ./pkg/compiler/... -run TestUserData.*GitHub` | ❌ W0 | ⬜ pending |
| 28-03-02 | 03 | 2 | Compiler ECS wiring | unit | `go test ./pkg/compiler/... -run TestCompileECS.*GitHub` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `sidecars/http-proxy/httpproxy/github_test.go` — unit tests for ExtractRepoFromPath, IsRepoAllowed, GitHubBlockedResponse
- [ ] Integration test functions in `sidecars/http-proxy/httpproxy/http_proxy_test.go` — GitHub allowed/blocked/non-repo/nil scenarios
- [ ] Compiler test fixtures: `pkg/compiler/testdata/ec2-with-github-repos.yaml`, `pkg/compiler/testdata/ecs-with-github-repos.yaml`

*Existing infrastructure covers framework setup — only test files needed.*

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| End-to-end sandbox git clone | Full proxy chain | Requires live sandbox with proxy + GitHub | `km create profiles/claude-dev.yaml`, SSH in, `git clone` allowed and disallowed repos |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 15s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
