# Klanker Marketplace Plugin Design

**Date:** 2026-04-15
**Status:** Approved

## Overview

A Claude Code marketplace plugin (`klanker`) distributed via `whereiskurt/klanker-maker` that provides sandbox email orchestration, operator CLI guidance, and platform communication skills.

Users install with: `claude plugin add whereiskurt/klanker-maker`

## Plugin Structure

```
.claude/plugins/klanker/
├── plugin.json            # Manifest: name, version, 4 skills
└── skills/
    ├── sandbox.md         # klanker:sandbox — environment detection
    ├── email.md           # klanker:email — send/receive/orchestrate
    ├── operator.md        # klanker:operator — platform requests via email
    └── user.md            # klanker:user — operator CLI workflow
```

## Skills

### klanker:sandbox (in-sandbox)

Detects whether Claude is running inside a KM sandbox. Reads `KM_*` environment variables, discovers email policy from the deployed profile (`spec.email` block), and verifies tooling (`km-send`/`km-recv` at `/opt/km/bin/`, signing key in SSM). Foundation skill — invoked by `email` and `operator` before they operate.

### klanker:email (in-sandbox)

Orchestrates email between sandboxes via `km-send` and `km-recv`. Covers:

- **Sending:** Always write body to file (not stdin), check exit codes, use meaningful subjects
- **Receiving:** Always use `--json`, check signature field per policy, mark-read after processing
- **Poll-and-wait:** Send then poll with backoff (10s → 60s), match by subject, configurable timeout
- **Delegate-and-wait:** Structured `km-agent:{action}:{correlation-id}` subject format for task assignment/results
- **Fan-out:** Send to N sandboxes, collect M results, report timeouts
- **Error handling:** Signing failures, verification failures, poll timeouts, poller down

Adapts to the sandbox's email policy (signing required/optional, encryption required/optional, verify inbound required/optional) as discovered by `klanker:sandbox`.

### klanker:operator (in-sandbox)

Communicates with the platform operator via email. The operator inbox has a Haiku AI interpreter that understands natural language and maps to `km at` commands. Covers:

- Schedule agent runs, reminders, deferred operations
- Extend TTL, add budget, create/pause/resume/destroy sandboxes
- Status queries
- Conversation threading (Haiku maintains state via SES Message-ID)
- KM-AUTH safe phrase is auto-appended by `km-send`

### klanker:user (operator workstation)

Guides the `km` CLI on the operator's machine. Covers:

- **Getting started:** `km-config.yaml`, `km doctor`, `km info`
- **Bootstrap:** `km init`, `--sidecars`, `--lambdas`
- **Creating sandboxes:** Default to `profiles/learn.yaml`, validation, common flags
- **Agent execution:** `km agent run` (fire-and-forget, `--wait`, `--interactive`, `--auto-start`), attach, results, list
- **Learn mode:** Create with learn profile → `km shell --learn` → observe → generate profile → validate
- **Lifecycle:** pause/resume/stop/destroy, lock/unlock, `km at` scheduling
- **Monitoring:** `km otel`, `km shell`, `km email send/read`

## Design Decisions

1. **Approach B (skill family)** chosen over monolithic skill or skill+docs. Each skill is focused and composable. New capabilities (e.g., artifacts, budget monitoring) can be added as new skills without bloating existing ones.

2. **Rigid patterns for email** — the skill prescribes exact command usage (always `--body <file>`, always `--json` for receive, always check signatures). This prevents agents from reinventing error-prone patterns.

3. **Policy-adaptive** — the sandbox skill detects email policy from the deployed profile, and the email skill adapts (reject unsigned messages when `verifyInbound: required`, etc.) rather than hardcoding assumptions.

4. **Operator via email, not direct API** — sandboxes request platform actions by emailing the operator inbox, leveraging the existing Haiku AI interpreter. No new APIs or IAM needed.

5. **Default to learn.yaml** — the user skill prescribes `profiles/learn.yaml` as the starting point for new users, with wildcard allowlists and full tooling.
