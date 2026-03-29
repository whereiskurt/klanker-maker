---
phase: 34-agent-profiles-agent-orchestrator-goose-and-codex-sandbox-profiles
verified: 2026-03-29T22:00:00Z
status: passed
score: 5/5 must-haves verified
re_verification: false
---

# Phase 34: Agent Profiles Verification Report

**Phase Goal:** Three new SandboxProfile YAML files (agent-orchestrator, goose, codex) are added to profiles/ and pass km validate, giving operators ready-to-use sandbox environments for the broader AI coding agent ecosystem
**Verified:** 2026-03-29T22:00:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| #   | Truth                                                                                 | Status     | Evidence                                                                                         |
| --- | ------------------------------------------------------------------------------------- | ---------- | ------------------------------------------------------------------------------------------------ |
| 1   | agent-orchestrator.yaml is a valid SandboxProfile that passes km validate             | VERIFIED   | `km validate profiles/agent-orchestrator.yaml` exits 0; output: "profiles/agent-orchestrator.yaml: valid" |
| 2   | goose.yaml is a valid SandboxProfile that passes km validate                          | VERIFIED   | `km validate profiles/goose.yaml` exits 0; output: "profiles/goose.yaml: valid"                  |
| 3   | codex.yaml is a valid SandboxProfile that passes km validate                          | VERIFIED   | `km validate profiles/codex.yaml` exits 0; output: "profiles/codex.yaml: valid"                  |
| 4   | Each profile has correct tool-specific initCommands, env vars, and network egress     | VERIFIED   | See detailed artifact checks below                                                               |
| 5   | Existing builtin profiles and tests remain unaffected                                 | VERIFIED   | `go test ./pkg/profile/... -count=1` passes (ok, 0.293s); no failures                           |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact                            | Expected                                   | Status     | Details                                                             |
| ----------------------------------- | ------------------------------------------ | ---------- | ------------------------------------------------------------------- |
| `profiles/agent-orchestrator.yaml`  | ComposioHQ agent-orchestrator sandbox profile | VERIFIED  | Exists, substantive (158 lines), contains `name: agent-orchestrator` |
| `profiles/goose.yaml`               | Block goose AI coding agent sandbox profile   | VERIFIED  | Exists, substantive (156 lines), contains `name: goose`              |
| `profiles/codex.yaml`               | OpenAI codex coding agent sandbox profile     | VERIFIED  | Exists, substantive (153 lines), contains `name: codex`              |

**Artifact Level 2 (substantive) checks — all profiles contain all 13 required sections:**

All three profiles contain every required and optional spec section: lifecycle, runtime, execution, sourceAccess, network, identity, sidecars, observability, policy, agent, budget, artifacts, email.

**Artifact Level 3 (wiring) — structural correctness:**

- `apiVersion: klankermaker.ai/v1alpha1` present in all three
- `kind: SandboxProfile` present in all three
- All four sidecars (dnsProxy, httpProxy, auditLog, tracing) enabled with km-* images in all three

### Key Link Verification

| From                                | To                        | Via           | Status   | Details                                               |
| ----------------------------------- | ------------------------- | ------------- | -------- | ----------------------------------------------------- |
| `profiles/agent-orchestrator.yaml`  | `pkg/profile/validate.go` | km validate   | WIRED    | `km validate profiles/agent-orchestrator.yaml` exits 0 |
| `profiles/goose.yaml`               | `pkg/profile/validate.go` | km validate   | WIRED    | `km validate profiles/goose.yaml` exits 0              |
| `profiles/codex.yaml`               | `pkg/profile/validate.go` | km validate   | WIRED    | `km validate profiles/codex.yaml` exits 0              |

### Tool-Specific Configuration Verification

**agent-orchestrator.yaml (ComposioHQ):**
- instanceType: t3.large (correct — multi-agent needs more memory)
- ttl: 8h, idleTimeout: 1h (correct — longer for multi-agent sessions)
- GITHUB_TOKEN: "" (empty placeholder with SSM injection comment)
- `gh auth login --with-token` in initCommands (line 39)
- `npm install -g @composio/ao` in initCommands
- Network egress includes .npmjs.org, .npmjs.com, .nodejs.org for npm install
- budget: compute=4.00, ai=10.00 (higher for multi-agent)
- agent.maxConcurrentTasks: 4, taskTimeout: 120m
- No CA cert env var (not a Rust binary — Node.js uses system trust store)

**goose.yaml (Block):**
- instanceType: t3.medium (correct)
- GOOSE_PROVIDER: amazon_bedrock (Bedrock, no external AI API)
- GOOSE_MODEL: anthropic.claude-sonnet-4-5 (bare model ID, with comment explaining no us. prefix)
- SSL_CERT_FILE: /usr/local/share/ca-certificates/km-proxy-ca.crt (correct CA cert path for Rust binary)
- CONFIGURE=false in download_cli.sh invocation (skips interactive wizard)
- Network egress: .amazonaws.com only for AI (no api.openai.com, no api.anthropic.com)
- agent.maxConcurrentTasks: 1, taskTimeout: 60m

**codex.yaml (OpenAI):**
- instanceType: t3.medium (correct)
- CODEX_CA_CERTIFICATE: /usr/local/share/ca-certificates/km-proxy-ca.crt (correct CA cert path for Rust binary)
- OPENAI_API_KEY: "" (empty placeholder with OTP/SSM injection comment)
- Pinned to rust-v0.117.0 musl static binary from GitHub releases
- Network egress includes .openai.com and api.openai.com
- agent.maxConcurrentTasks: 1, taskTimeout: 60m

### Requirements Coverage

| Requirement   | Source Plan    | Description                                   | Status         | Evidence                                                      |
| ------------- | -------------- | --------------------------------------------- | -------------- | ------------------------------------------------------------- |
| PROF-34-01    | 34-01-PLAN.md  | agent-orchestrator SandboxProfile YAML        | SATISFIED      | profiles/agent-orchestrator.yaml exists and passes km validate |
| PROF-34-02    | 34-01-PLAN.md  | goose SandboxProfile YAML                     | SATISFIED      | profiles/goose.yaml exists and passes km validate              |
| PROF-34-03    | 34-01-PLAN.md  | codex SandboxProfile YAML                     | SATISFIED      | profiles/codex.yaml exists and passes km validate              |
| PROF-34-04    | 34-01-PLAN.md  | Existing tests unaffected                     | SATISFIED      | go test ./pkg/profile/... passes with no regressions           |

**Note on PROF-34-xx IDs:** These requirement IDs are referenced in the ROADMAP.md Phase 34 entry and in the PLAN frontmatter but do not appear in the traceability table in REQUIREMENTS.md. The requirements themselves are implicit from the phase goal and success criteria. This is a documentation gap in REQUIREMENTS.md (the traceability table was not updated for phase 34 requirements), but does not affect the actual code-level verification — all four stated requirements are satisfied by the artifacts in the codebase.

### Anti-Patterns Found

| File                        | Line | Pattern              | Severity | Impact                                                          |
| --------------------------- | ---- | -------------------- | -------- | --------------------------------------------------------------- |
| `profiles/agent-orchestrator.yaml` | 39 | `echo "$GITHUB_TOKEN"` in initCommands | Info | GITHUB_TOKEN is injected from SSM (env var line 31 is empty string with comment) — this is intentional and correct; the initCommand reference is safe because the real value is injected at provision time, not stored in YAML |

No blocking or warning-level anti-patterns found. All sensitive keys (OPENAI_API_KEY, GITHUB_TOKEN) are empty strings with explanatory comments in the YAML.

**CA cert path verification:** Neither `/etc/km-proxy/ca.crt` (the incorrect RESEARCH path) nor any other wrong path appears in any of the three profiles. All Rust-binary profiles (goose, codex) correctly use `/usr/local/share/ca-certificates/km-proxy-ca.crt`.

### Human Verification Required

None. All required checks are verifiable programmatically for YAML profiles:
- km validate confirms schema compliance
- go test confirms no regressions
- grep confirms structural completeness, correct values, and absence of hardcoded secrets

---

## Summary

Phase 34 goal is fully achieved. Three new SandboxProfile YAML files exist in `profiles/`, each passes `km validate` (schema valid, exit 0), and each contains tool-appropriate configuration:

- **agent-orchestrator.yaml** — ComposioHQ multi-agent coordinator with t3.large, 8h TTL, gh CLI auth, npm-based `@composio/ao` install, higher budget (compute=4.00, ai=10.00), 4 concurrent tasks
- **goose.yaml** — Block AI coding agent with t3.medium, Bedrock backend (no external AI hosts), SSL_CERT_FILE for Rust proxy TLS, CONFIGURE=false to skip interactive wizard
- **codex.yaml** — OpenAI codex with t3.medium, pinned rust-v0.117.0 musl binary, CODEX_CA_CERTIFICATE for proxy TLS, OPENAI_API_KEY as empty placeholder with SSM comment

The existing builtin profile test suite (`go test ./pkg/profile/...`) passes with no regressions. All four PROF-34-xx requirements are satisfied.

The only documentation gap found is that the REQUIREMENTS.md traceability table does not list the PROF-34-xx IDs — they are defined in the ROADMAP but not added to the table. This is a documentation issue only and does not affect correctness of the profiles.

---

_Verified: 2026-03-29T22:00:00Z_
_Verifier: Claude (gsd-verifier)_
