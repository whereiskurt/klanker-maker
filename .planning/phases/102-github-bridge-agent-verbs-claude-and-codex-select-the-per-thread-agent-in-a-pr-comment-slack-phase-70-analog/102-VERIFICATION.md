---
phase: 102-github-bridge-agent-verbs-claude-and-codex-select-the-per-thread-agent-in-a-pr-comment-slack-phase-70-analog
verified: 2026-06-08T21:00:00Z
status: passed
score: 6/6 must-haves verified
re_verification: false
---

# Phase 102: GitHub Bridge Agent Verbs Verification Report

**Phase Goal:** Reserved `/claude` and `/codex` verbs in a PR/issue comment select the agent for that thread — the GitHub analog of Slack's Phase 70 prefix routing. The verb writes `agent_type` onto the (repo, number) row; follow-ups with no verb continue with it; precedence verb > thread agent_type > profile default; <=1 agent verb per comment (two = error reply); single agent_session_id reset on cross-agent switch; /codex on a Claude-only profile posts a helpful comment; claude/codex/help reserved (github.commands shadow → km doctor WARN); no verb = byte-identical to today.

**Verified:** 2026-06-08T21:00:00Z
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | `/claude` or `/codex` anywhere in a PR comment parses as an agent verb, strips from `{{args}}`, composes with a command verb | VERIFIED | `ParseCommands` intercepts `name == "claude" \|\| name == "codex"` before the command-map lookup (commands.go:245-252); `ExtractArgsWithAgent` strips the agent token (commands.go:337-345); `TestCommandParse` subtests for composition, dedup, fenced-code suppression all PASS |
| 2 | Two distinct agent verbs in one comment produce exactly one error reply and no dispatch | VERIFIED | `AgentVerbConflict` bool in `ParseResult` (commands.go:119); `postConflictReply()` closure in `Handle()` (webhook_handler.go:368-379) short-circuits before envelope construction; `TestHandle_AgentVerbConflict` PASS; UAT Step d live-PASS |
| 3 | `agent_type` persists in km-github-threads for the (repo, number) row; follow-ups without a verb continue with the stored agent | VERIFIED | `LookupSandbox` returns 4-tuple including `agentType` (interfaces.go:163); projection includes `agent_type` in DDB GetItem (aws_adapters.go:720); poller reads `THREAD_AGENT_TYPE` via `jq -r '.Item.agent_type.S // empty'` (userdata.go:2193); `TestGitHubThreadStore_LookupSandbox_Found` + `_NoAgentType` PASS; UAT Steps a+b live-PASS |
| 4 | Cross-agent switch (verb differs from stored agent_type) clears session so the new agent starts fresh | VERIFIED | Poller block (userdata.go:2263-2271): if `AGENT_OVERRIDE != THREAD_AGENT_TYPE` and `GITHUB_SESSION` is non-empty → `GITHUB_SESSION=""` + `RESUME_ARG=""`; log line `cross-agent switch X->Y; resetting session`; UAT Step c live-PASS with journald evidence |
| 5 | `/codex` on a Claude-only profile posts helpful comment and acks the SQS message instead of stranding the turn | VERIFIED | D6 guard (userdata.go:2276-2281): `if EFFECTIVE_AGENT = codex && ! command -v codex` → `km-github comment` + `aws sqs delete-message` + `continue`; `TestUserdata_GitHubInboundPoller_Phase102AgentVerbs` PASS (grep guard); operator-approved skip of second-sandbox live test (UAT Step e) |
| 6 | `claude`, `codex`, `help` are reserved in `github.commands`; shadowing → `km doctor WARN`; `/help` reply lists agent verbs and current thread agent | VERIFIED | `doctor.go:1503`: loop over `[]string{"help","claude","codex"}`; `TestDoctorGitHubCommandsAgentVerbShadow` subtests (claude→WARN, codex→WARN, clean→OK) all PASS; `buildHelpReply` with `currentAgentType` param (commands.go:426); `TestBuildHelpReplyAgentListing` PASS; UAT bonus step live-PASS |

**Score:** 6/6 truths verified

---

### Required Artifacts

| Artifact | Provides | Status | Details |
|----------|---------|--------|---------|
| `pkg/github/bridge/commands.go` | `ParseResult.AgentVerb` + `AgentVerbConflict`; reserved-token intercept; `ExtractArgsWithAgent` strip; `buildHelpReply` with `currentAgentType` | VERIFIED | All fields present and substantive; wired into `ParseCommands`, `RunCommandPass`, `Handle()` |
| `pkg/github/bridge/payload.go` | `GitHubEnvelope.Agent string` field | VERIFIED | `Agent string \`json:"agent,omitempty"\`` at line 90; set in `Handle()` at line 463 |
| `pkg/github/bridge/webhook_handler.go` | Two-verb conflict reply; `envelope.Agent` population; `threadCurrentAgentType` for help reply | VERIFIED | `postConflictReply` closure + early return (lines 368-405); `Agent: agentVerb` at line 463; `threadCurrentAgentType` threaded to `RunCommandPass` at line 392 |
| `pkg/github/bridge/interfaces.go` | Extended `GitHubThreadStore.LookupSandbox` (4-return) + `UpdateSession` (agentType param) | VERIFIED | LookupSandbox returns `(sandboxID, sessionID, agentType string, err error)` (line 163); UpdateSession takes `sessionID, agentType string` (line 175) |
| `pkg/github/bridge/aws_adapters.go` | DDB projection includes `agent_type`; UpdateItem sets `agent_type = :at` | VERIFIED | Projection at line 720: `"sandbox_id, agent_session_id, agent_type"`; UpdateExpression at line 793: `"SET agent_session_id = :sid, agent_type = :at"` |
| `pkg/compiler/userdata.go` | GitHub poller: `AGENT_OVERRIDE` parse, `THREAD_AGENT_TYPE` read, D4 precedence block, D5 cross-agent reset, D6 codex-missing guard, `agent_type` write-back | VERIFIED | All 4 render tokens confirmed by `TestUserdata_GitHubInboundPoller_Phase102AgentVerbs` PASS; code at lines 2155-2281+2355 |
| `internal/app/cmd/doctor.go` | Reserved-shadow loop covers `help`, `claude`, `codex` | VERIFIED | `for _, reserved := range []string{"help", "claude", "codex"}` at line 1503 |
| `internal/app/cmd/doctor_github_commands_test.go` | `TestDoctorGitHubCommandsAgentVerbShadow` | VERIFIED | Subtests for claude, codex, and clean map — all PASS |
| `pkg/github/bridge/webhook_handler_phase102_test.go` | `TestHandle_AgentVerbConflict` + `TestHandle_EnvelopeCarriesAgent` | VERIFIED | Both tests PASS |
| `pkg/github/bridge/thread_store_test.go` | `agent_type` round-trip tests | VERIFIED | `TestGitHubThreadStore_LookupSandbox_Found`, `_NoAgentType`, `_UpdateSession` — all PASS |
| `pkg/compiler/userdata_github_inbound_test.go` | `TestUserdata_GitHubInboundPoller_Phase102AgentVerbs` render/grep guard | VERIFIED | PASS — confirms `AGENT_OVERRIDE`, `THREAD_AGENT_TYPE`, `command -v codex`, `agent_type = :at` all in rendered poller |
| `docs/github-bridge.md` | Phase 102 operator runbook section | VERIFIED | `## Phase 102 — Agent verbs (/claude, /codex)` section present with deploy surface, precedence table, Codex precondition, reserved tokens, back-compat, and UAT steps |
| `CLAUDE.md` | Phase 102 bullet in phase history + where-to-look row | VERIFIED | Phase 102 bullet at line 21 + where-to-look row at line 90 |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `ParseCommands` reserved-token intercept | `ParseResult.AgentVerb` | `name == "claude" \|\| name == "codex"` at commands.go:245 | WIRED | Intercept fires before command-map lookup; dedup + conflict logic correct |
| `Handle()` command path | `GitHubEnvelope.Agent` | `agentVerb` populated by `parseAgentVerbs` closure; set on envelope at line 463 | WIRED | Both command-configured and dormant paths populate `agentVerb`; conflict short-circuit fires before envelope construction |
| `Handle()` → `LookupSandbox` | `threadCurrentAgentType` | 4-tuple return at line 304; threaded to `RunCommandPass` at line 392 for `/help` reply | WIRED | `threadCurrentAgentType = agentType` carried from lookup to help reply |
| Envelope `.agent` field | Poller `AGENT_OVERRIDE` | `jq -r '.agent // empty'` (userdata.go:2155) | WIRED | Parsed immediately after envelope field extraction |
| `AGENT_OVERRIDE` | `EFFECTIVE_AGENT` | D4 precedence block (userdata.go:2263-2271) | WIRED | verb override > thread agent_type > profile default; cross-agent reset clears both `GITHUB_SESSION` and `RESUME_ARG` |
| `EFFECTIVE_AGENT` | DDB `agent_type` write-back | `SET agent_session_id = :sid, agent_type = :at` (userdata.go:2355) | WIRED | Writes on successful turn; uses `EFFECTIVE_AGENT` value |
| `aws_adapters.go UpdateSession` | DDB UpdateItem | `UpdateExpression: "SET agent_session_id = :sid, agent_type = :at"` (line 793) | WIRED | `agent_type` written via UpdateItem (never PutItem — avoids lossy round-trip footgun) |
| `doctor.go` reserved-shadow loop | WARN emission | `for _, reserved := range []string{"help","claude","codex"}` (line 1503) | WIRED | One WARN per shadowed name; covered by `TestDoctorGitHubCommandsAgentVerbShadow` |

---

### Requirements Coverage

The GH-AGENT-* IDs are phase-local synthetic IDs declared in plan frontmatter (same pattern as Phase 95/96/97/98). REQUIREMENTS.md was last updated at Phase 98; no Phase 102 section exists — this is by design (synthetic IDs are plan-local, not added to the cross-phase requirements table).

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|---------|
| GH-AGENT-VERB | 102-01, 102-04 | Verb parsing, conflict reply, reserved tokens, km doctor WARN | SATISFIED | commands.go:245-273; doctor.go:1503; tests PASS |
| GH-AGENT-PERSIST | 102-02 | `agent_type` schema-on-write in km-github-threads via UpdateItem | SATISFIED | aws_adapters.go:793; interfaces.go:163+175; thread_store tests PASS |
| GH-AGENT-POLLER | 102-03 | `AGENT_OVERRIDE` parse, `THREAD_AGENT_TYPE` read, precedence block | SATISFIED | userdata.go:2155+2193+2263; render test PASS |
| GH-AGENT-SWITCH | 102-03 | Cross-agent session reset (both session + RESUME_ARG cleared) | SATISFIED | userdata.go:2264-2267; UAT Step c live-PASS |
| GH-AGENT-PROFILE | 102-03, 102-04 | `/codex` on Claude-only profile → helpful comment + ack, no strand | SATISFIED | userdata.go:2276-2281; render test PASS; operator-approved live-test skip |
| GH-AGENT-E2E | 102-05 | Deploy-surface audit + live E2E sequence (a-e) | SATISFIED | UAT Part 2 deploy audit complete; UAT Part 6: Steps a+b+c+d+SC#2 live-PASS; Step e code+unit; Step f unit; bonus /help live-PASS |

No orphaned requirements detected (all 6 GH-AGENT-* IDs claimed by plans and verified).

---

### Anti-Patterns Found

| File | Pattern | Severity | Assessment |
|------|---------|----------|------------|
| `pkg/compiler/userdata.go:2278` | Codex-missing comment says `"the 'codex:' verb"` but UAT runbook says `"/codex is unavailable"` | Info | Minor doc/code wording discrepancy — the guard itself is correctly wired; both convey the right operator message. Not a functional gap. |

No blocker anti-patterns. No stub/placeholder implementations found. No TODO/FIXME markers in Phase 102 code paths.

---

### Human Verification Required

All automated checks pass. One item was completed via live E2E UAT with operator-provided credentials rather than programmatic verification:

1. **Step e — `/codex` on Claude-only sandbox live test**
   - Test: Comment `/codex fix the build error` on a repo whose sandbox has no `codex` binary
   - Expected: Bot posts `"This sandbox's profile has no Codex; ..."` comment; no stranded turn
   - Why human: Requires a second configured sandbox with `profiles/github-review.yaml` (Claude-only)
   - Resolution: Operator approved skip; covered by D6 guard code at `userdata.go:2276-2281` and `TestUserdata_GitHubInboundPoller_Phase102AgentVerbs` grep-guard PASS

---

### Test Suite Summary

| Package | Result | Notes |
|---------|--------|-------|
| `pkg/github/bridge` | PASS (12.5s) | All Phase 102 tests PASS; no regressions in Phase 97/98/99/100/101 tests |
| `pkg/compiler` | PASS (5.4s) | `TestUserdata_GitHubInboundPoller_Phase102AgentVerbs` PASS; all existing compiler tests PASS |
| `internal/app/cmd` (targeted) | PASS | `TestDoctorGitHubCommandsAgentVerbShadow` and full `TestDoctorGitHubCommands*` suite PASS |
| `internal/app/cmd` (full) | 1 pre-existing failure | `TestUnlockCmd_RequiresStateBucket` fails due to environment-dependent state bucket check (confirmed pre-Phase-102; not caused by Phase 102 changes) |
| `go build ./...` | PASS | Clean build |

---

### Deploy Surface Audit (from 102-UAT.md Part 2)

- No new Lambda, SQS queue, DDB table, or TF module added
- `internal/app/cmd/init.go` `lambdaBuilds()` and `regionalModules()` unchanged
- `agent_type` is schema-on-write on `km-github-threads` — no TF migration
- No SandboxProfile schema change — `km init --sidecars` NOT required
- Correct deploy: `make build-lambdas` (clean) + `km init --dry-run=false` (NOT `--sidecars`)
- Existing sandboxes need `km destroy && km create` for the new poller

---

### Live E2E Evidence (from 102-UAT.md Part 6)

All live steps executed 2026-06-09 against install prefix `/km/`, bot `klanker-maker`, sandbox `learn-ed0072c3` (profile `profiles/learn.v2.yaml`), repo `whereiskurt/klanker-maker`:

- **Step a** (`/codex /review`): Codex dispatched, both tokens stripped, `agent_type=codex` written to DDB — PASS
- **Step b** (no-verb follow-up): `THREAD_AGENT_TYPE=codex` drove Codex dispatch — PASS
- **Step c** (`/claude` switch): journald `cross-agent switch codex->claude; resetting session`; no `--resume` of codex UUID; `agent_type=claude` in DDB — PASS
- **Step d** (`/claude /codex`): exactly one error reply `"Specify one agent — found /claude and /codex."`, no dispatch — PASS
- **SC#2** (no-verb fresh thread): profile-default `claude` dispatched (byte-identical path) — PASS
- **Bonus** (`/help`): reply listed `/claude` + `/codex` + `Current thread agent: claude` dynamically — PASS

**Known limitation (not a phase failure):** GitHub Codex dispatch path (`userdata.go:2288`) does not pass a resume arg — Codex turns always start fresh sessions. This is pre-existing, orthogonal to agent-selection, and noted as a candidate follow-up in UAT Part 6.

---

### Summary

Phase 102 goal is fully achieved. All 6 observable truths are verified. All 13 required artifacts are substantive and wired. All 8 key links are confirmed. The 6 GH-AGENT-* synthetic requirement IDs are satisfied. The full bridge and compiler test suites are green. A live 5-step GH-AGENT-E2E sequence was completed with operator-provided credentials confirming runtime behavior (precedence, cross-agent switch, dormant/byte-identical path, two-verb error reply). The one unenumerated live test (Step e, Claude-only sandbox) is covered by code and the render/grep guard test.

---

_Verified: 2026-06-08T21:00:00Z_
_Verifier: Claude (gsd-verifier)_
