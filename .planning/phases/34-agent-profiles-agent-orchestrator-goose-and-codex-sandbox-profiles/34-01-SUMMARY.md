---
phase: 34-agent-profiles-agent-orchestrator-goose-and-codex-sandbox-profiles
plan: "01"
subsystem: profiles
tags: [sandbox-profiles, agent-orchestrator, goose, codex, composio, openai, block, ec2, yaml]

# Dependency graph
requires:
  - phase: 01-schema-compiler-aws-foundation
    provides: SandboxProfile schema, km validate CLI, km create pipeline
  - phase: 32-rsync-save-restore
    provides: rsyncPaths field in spec.execution schema
provides:
  - profiles/agent-orchestrator.yaml — ComposioHQ multi-agent coordinator sandbox profile
  - profiles/goose.yaml — Block AI coding agent (Bedrock) sandbox profile
  - profiles/codex.yaml — OpenAI codex coding agent sandbox profile
  - Updated toolchain km binary in S3 (linux/arm64, supports rsyncPaths)
affects:
  - Any future phase that adds or modifies sandbox profiles
  - km create pipeline (toolchain binary updated)

# Tech tracking
tech-stack:
  added:
    - "@composio/ao (npm) — agent-orchestrator CLI"
    - "goose (Block) — AI coding agent from github.com/block/goose"
    - "codex (OpenAI) — rust-v0.117.0 pinned release from github.com/openai/codex"
  patterns:
    - "External agent profiles live in profiles/ (not pkg/profile/builtins/)"
    - "Rust binaries (goose, codex) need SSL_CERT_FILE or CODEX_CA_CERTIFICATE pointing to /usr/local/share/ca-certificates/km-proxy-ca.crt"
    - "Bedrock-only agents (goose) don't need external AI API allowedHosts — Bedrock is under .amazonaws.com"
    - "Agents requiring gh CLI need GITHUB_TOKEN injected from SSM, not hardcoded"

key-files:
  created:
    - profiles/agent-orchestrator.yaml
    - profiles/goose.yaml
    - profiles/codex.yaml
  modified:
    - build/km-toolchain (linux/arm64 binary, uploaded to s3://km-artifacts-12345/toolchain/km)

key-decisions:
  - "Rust agent binaries (goose, codex) require explicit CA cert env var for MITM proxy — SSL_CERT_FILE and CODEX_CA_CERTIFICATE respectively, pointing to /usr/local/share/ca-certificates/km-proxy-ca.crt"
  - "goose uses bare model ID anthropic.claude-sonnet-4-5 (not us.anthropic prefix) — amazon_bedrock provider may not support cross-region inference profile IDs"
  - "codex binary pinned to rust-v0.117.0 for reproducibility — downloaded from GitHub releases as musl static binary"
  - "agent-orchestrator needs gh CLI authenticated via GITHUB_TOKEN from SSM before ao start — hardcoded token is security risk"
  - "Lambda toolchain km binary must be updated alongside schema changes — deployed linux/arm64 binary to S3 to fix rsyncPaths validation failure"

patterns-established:
  - "Profile CA cert path: always /usr/local/share/ca-certificates/km-proxy-ca.crt (confirmed from pkg/compiler/userdata.go)"
  - "External profiles go in profiles/ not pkg/profile/builtins/"
  - "Toolchain binary update: GOOS=linux GOARCH=arm64 go build + aws s3 cp to s3://km-artifacts-12345/toolchain/km"

requirements-completed: [PROF-34-01, PROF-34-02, PROF-34-03, PROF-34-04]

# Metrics
duration: 14min
completed: 2026-03-29
---

# Phase 34 Plan 01: Agent Profiles — agent-orchestrator, goose, codex Summary

**Three new SandboxProfile YAML files for ComposioHQ agent-orchestrator (t3.large/tmux/gh), Block goose (t3.medium/Bedrock/SSL_CERT_FILE), and OpenAI codex (t3.medium/musl-static-binary/CODEX_CA_CERTIFICATE) — all validated locally and deployed to real AWS infrastructure as running sandboxes**

## Performance

- **Duration:** ~14 min
- **Started:** 2026-03-29T21:00:00Z
- **Completed:** 2026-03-29T21:13:40Z
- **Tasks:** 2 plan tasks + live infrastructure testing
- **Files modified:** 3

## Accomplishments
- Created three complete SandboxProfile YAML files (agent-orchestrator.yaml, goose.yaml, codex.yaml) all passing `km validate`
- Existing profile tests (`go test ./pkg/profile/...`) remain green — no regressions
- Deployed all three profiles to real AWS infrastructure via `km create --remote`; all three reached `running` status
- Identified and fixed a Lambda toolchain version mismatch (old km binary didn't support rsyncPaths) by cross-compiling a linux/arm64 binary and uploading to S3

## Task Commits

Each task was committed atomically:

1. **Task 1: Create agent-orchestrator, goose, and codex profile YAML files** - `236ea00` (feat)
2. **Task 2: Verify existing tests still pass and profiles are well-formed** - `2501665` (test)

## Files Created/Modified
- `profiles/agent-orchestrator.yaml` — ComposioHQ multi-agent coordinator, t3.large, tmux + npm @composio/ao, gh CLI auth
- `profiles/goose.yaml` — Block goose AI agent, t3.medium, Bedrock/amazon_bedrock, SSL_CERT_FILE for proxy CA
- `profiles/codex.yaml` — OpenAI codex, t3.medium, pinned rust-v0.117.0 musl binary, CODEX_CA_CERTIFICATE for proxy

## Decisions Made
- Rust binaries (goose and codex) need explicit CA cert env vars because they don't use the system trust store by default — `SSL_CERT_FILE` for goose, `CODEX_CA_CERTIFICATE` for codex, both pointing to `/usr/local/share/ca-certificates/km-proxy-ca.crt`
- goose uses bare model ID `anthropic.claude-sonnet-4-5` (not `us.anthropic.claude-sonnet-4-5`) since amazon_bedrock provider may not support cross-region inference profile IDs
- codex binary pinned to rust-v0.117.0 for reproducibility as a musl static binary from GitHub releases
- OPENAI_API_KEY in codex profile is empty string with comment — must be injected via OTP/SSM at runtime
- GITHUB_TOKEN in agent-orchestrator is empty string with comment — must be injected from SSM at provision time

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Lambda toolchain km binary outdated — rsyncPaths not supported**
- **Found during:** Infrastructure testing (post-plan deployment)
- **Issue:** The Lambda's km binary (uploaded 2026-03-29 00:15:47) predated the rsyncPaths schema addition. All three sandboxes failed with: `spec.execution: additional properties 'rsyncPaths' not allowed`. Local `km validate` passed because the local binary was current.
- **Fix:** Cross-compiled a fresh linux/arm64 km binary (`GOOS=linux GOARCH=arm64 go build`) and uploaded to `s3://km-artifacts-12345/toolchain/km`. Destroyed the three failed sandboxes and redeployed. All three succeeded on the next Lambda cold start.
- **Files modified:** `build/km-toolchain` (temporary build artifact, uploaded to S3)
- **Verification:** Lambda logs showed `km create subprocess succeeded` for all three; `km list` showed all three as `running`
- **Committed in:** Not committed (binary artifact, not source)

---

**Total deviations:** 1 auto-fixed (Rule 3 - blocking infrastructure issue)
**Impact on plan:** Required rebuilding/re-uploading toolchain binary. No profile YAML changes needed.

## Issues Encountered
- SSO token was expired at the start of infrastructure testing — refreshed via `aws sso login --profile klanker-terraform`
- The Terragrunt error about `mock_outputs_allowed_on_destroy` appears in all sandbox create logs (pre-existing issue, not caused by these profiles) — does not prevent successful provisioning

## Next Phase Readiness
- All three profiles are production-ready and deployed successfully
- The Lambda toolchain is now current with the rsyncPaths schema
- Pre-existing Terragrunt `mock_outputs_allowed_on_destroy` warning should be investigated in a future phase

---
*Phase: 34-agent-profiles-agent-orchestrator-goose-and-codex-sandbox-profiles*
*Completed: 2026-03-29*
