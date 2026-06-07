---
phase: 98-github-bridge-expansion
type: uat
status: gaps_found
date: 2026-06-07
scenario: GH-X-RESUME auto-resume → review → km-github post-back (live AWS + GitHub)
outcome: full chain proven end-to-end (with manual unblocks); 5 defects found, 2 fixed+deployed, 3 folded into 98-06 Tasks 3-5
---

# Phase 98 — Live UAT record (2026-06-07)

Real AWS (`klanker-application`, us-east-1) + real GitHub App (`klanker-maker`), repo
`whereiskurt/klanker-maker` PR #11. Goal: validate the "configure-once, pause, GitHub @-mention
wakes it, agent reviews and posts back" workflow (GH-X-RESUME + the post-back chain).

## Outcome

**The full chain was proven end-to-end** — but only after manual intervention. Final evidence:

```
whereiskurt        18:56:19Z  @klanker-maker can I get a quick review here?
klanker-maker[bot] 19:11:34Z  ## Quick review — Phase 94 doctor cleanup + Slack ratelimit fix ...
```

Chain validated: bridge mention/auth/resolve → auto-resume (`StartInstances`, no 403) →
DDB status→running → poller drain (queue URL from SSM) → worktree-per-PR (`/workspace/pr-11`) →
agent review of the real diff → `km-github` POST to the PR.

## What validated cleanly

| Capability | Status | Evidence |
|---|---|---|
| Bridge mention scan / allowlist / repo resolve | ✅ | 👀 ACK on each comment |
| Auto-resume IAM (Gap A fix) | ✅ | `auto-resume … status=paused` with NO `UnauthorizedOperation` 403 after redeploy |
| DDB status write-back (Gap B fix) | ✅ | bridge log `status write-back running`; `km list`/DDB flipped paused→running |
| Poller drain (env empty → SSM fallback) | ✅ | poller reads `/km/sandbox/<id>/github-inbound-queue-url` from SSM, binds queue |
| Worktree-per-PR isolation | ✅ | `/workspace/pr-11` created |
| Agent review quality | ✅ | substantive, accurate review of the actual diff |
| `km-github` post-back | ✅ | `klanker-maker[bot]` comment on PR #11 |

## Defects found

| # | Gap | Severity | Status |
|---|-----|----------|--------|
| A | `ec2:StartInstances` IAM conditioned on `km:managed=true` (a tag no sandbox carries) → every resume 403 | blocker | **FIXED + deployed + validated** (`50e6c9b7`); regression test `e57ff4ba` |
| B | Resume path never wrote DDB `status=running` → `km list` stale, repeat mentions re-fire StartInstances | high | **FIXED + deployed + validated** (`1eda6f0e`, wired in main.go `94722eb9`) |
| C | Resumer filters `instance-state-name=stopped` only → a quick pause→mention (box still `stopping`) no-ops with "no stopped EC2 instances found"; prompt enqueued but box never starts | high | folded → **98-06 Task 3** |
| D | Token mint robustness: (1) create hardcodes `push`→`contents:write`; GitHub 422s the WHOLE mint when the install grants only `contents:read`; (2) wildcard-only `allowedRepos` + ambiguous install bakes empty `installation_id`+`permissions` into the refresher schedule, so it can never mint (SSM pin ignored) | high | folded → **98-06 Task 4** (operator also adding `Contents: write` to the App) |
| E | Stale/cross-box `agent_session_id` in `km-github-threads` (from a destroyed box) → `claude --resume` `No conversation found` → exit 1 → FIFO head-of-line block + poller wedge (spun 1m36s CPU) | high | folded → **98-06 Task 5** |

## Manual interventions used to reach green (each is what a Task 3–5 fix automates)

1. **Gap A:** rebuilt `km` (`make build` — had been omitted), redeployed; condition fix applied (`km:managed`→`km:resource-prefix`). [Also fixed the docs deploy sequence: `567b5a93`.]
2. **Gap C:** re-posted the @-mention once the box was fully `stopped` → clean resume.
3. **Gap D:** invoked `km-github-token-refresher-<id>` directly with a corrected payload
   (`installation_id=118557537`, `permissions={issues:write,pull_requests:write,checks:write}` —
   dropping the `contents:write` that 422'd). Token landed in SSM. (Expires ~1h; not durable.)
4. **Gap E:** deleted the stale `km-github-threads` row for `(whereiskurt/klanker-maker, 11)`
   so the next dispatch ran a fresh session.
5. **Poller wedge:** `systemctl restart km-github-inbound-poller` to clear the stuck receive loop.

## Environment notes (not defects)

- Per-sandbox token's `KM_GITHUB_INBOUND_QUEUE_URL` env is empty by design — poller falls back
  to SSM at runtime (confirmed working).
- Installation `118557537` = `whereiskurt`, grants `issues:write, pull_requests:write,
  checks:write, contents:read, metadata:read`.
- Pre-existing, out-of-scope: `cmd/km-slack` `TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0`
  fails identically at the pre-Phase-98 base; multiple bot comments on PR #11 were the queued
  duplicate messages reprocessing (agent handled gracefully).

## Next

Execute `98-06` Tasks 3–5, redeploy, then re-run this scenario unattended (no manual mint /
row delete / poller restart) and approve the Task 6 checkpoint.
