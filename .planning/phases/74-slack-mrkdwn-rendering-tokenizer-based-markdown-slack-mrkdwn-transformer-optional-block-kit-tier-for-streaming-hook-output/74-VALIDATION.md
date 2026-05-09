---
phase: 74
slug: slack-mrkdwn-rendering-tokenizer-based-markdown-slack-mrkdwn-transformer-optional-block-kit-tier-for-streaming-hook-output
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-05-09
---

# Phase 74 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go `testing` package (stdlib), Go 1.25.5 — `testing.F` for fuzz, `testing/quick` for property tests |
| **Config file** | none — `go test ./...` discovers all `*_test.go` files |
| **Quick run command** | `go test ./pkg/slack/... ./cmd/km-slack/...` |
| **Full suite command** | `go test ./...` |
| **Fuzz run command** | `go test -fuzz=FuzzMrkdwnify -fuzztime=30s ./pkg/slack/...` |
| **Estimated runtime** | ~30 seconds quick, ~3 minutes full |

---

## Sampling Rate

- **After every task commit:** Run `go test ./pkg/slack/... ./cmd/km-slack/...`
- **After every plan wave:** Run `go test ./...`
- **Before `/gsd:verify-work`:** Full suite must be green; fuzz target must run for ≥30s without finding new corpus entries
- **Max feedback latency:** 30 seconds

---

## Per-Task Verification Map

> All requirement IDs are placeholders defined in this phase (not yet in REQUIREMENTS.md). Mapping is from RESEARCH.md § Validation Architecture.

### PR1 — Tokenizer + Tier 1 mrkdwn (plan `01-tokenizer-tier1.md`)

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 74-01-W0 | 01 | 0 | scaffolding | manual | `test -d pkg/slack/testdata && grep -q MaxRenderedBytes pkg/slack/payload.go` | ❌ W0 | ⬜ pending |
| 74-01-01 | 01 | 1 | REND-01 bold collapse | unit | `go test ./pkg/slack/... -run TestMrkdwnify_Bold` | ❌ W0 | ⬜ pending |
| 74-01-02 | 01 | 1 | REND-02 heading map | unit | `go test ./pkg/slack/... -run TestMrkdwnify_Heading` | ❌ W0 | ⬜ pending |
| 74-01-03 | 01 | 1 | REND-03 link conversion | unit | `go test ./pkg/slack/... -run TestMrkdwnify_Link` | ❌ W0 | ⬜ pending |
| 74-01-04 | 01 | 1 | REND-04 strikethrough | unit | `go test ./pkg/slack/... -run TestMrkdwnify_Strike` | ❌ W0 | ⬜ pending |
| 74-01-05 | 01 | 1 | REND-05 HTML-escape | unit | `go test ./pkg/slack/... -run TestMrkdwnify_HTMLEscape` | ❌ W0 | ⬜ pending |
| 74-01-06 | 01 | 1 | REND-06 hr drop | unit | `go test ./pkg/slack/... -run TestMrkdwnify_HRule` | ❌ W0 | ⬜ pending |
| 74-01-07 | 01 | 1 | REND-07 pipe-table fence | unit | `go test ./pkg/slack/... -run TestMrkdwnify_PipeTable` | ❌ W0 | ⬜ pending |
| 74-01-08 | 01 | 2 | REND-08 code-fence preservation | property | `go test ./pkg/slack/... -run TestCodeFencePreservation` | ❌ W0 | ⬜ pending |
| 74-01-09 | 01 | 2 | REND-09 code-span preservation | property | `go test ./pkg/slack/... -run TestCodeSpanPreservation` | ❌ W0 | ⬜ pending |
| 74-01-10 | 01 | 2 | REND-10 idempotence | property | `go test ./pkg/slack/... -run TestMrkdwnifyIdempotent` | ❌ W0 | ⬜ pending |
| 74-01-11 | 01 | 2 | REND-11 fuzz no-panic + idempotent | fuzz | `go test -fuzz=FuzzMrkdwnify -fuzztime=30s ./pkg/slack/...` | ❌ W0 | ⬜ pending |
| 74-01-12 | 01 | 2 | REND-12 corpus fixtures | corpus | `go test ./pkg/slack/... -run TestMrkdwnifyCorpus` | ❌ W0 | ⬜ pending |
| 74-01-13 | 01 | 2 | REND-13 fail-soft (recover) | unit | `go test ./pkg/slack/... -run TestMrkdwnify_FailSoft` | ❌ W0 | ⬜ pending |
| 74-01-14 | 01 | 3 | REND-14 body overflow truncation | unit | `go test ./cmd/km-slack/... -run TestRunWith_Overflow` | ❌ W0 | ⬜ pending |
| 74-01-15 | 01 | 3 | REND-15 `--render=plain` no-op default | unit | `go test ./cmd/km-slack/... -run TestRunWith_Plain` | ❌ W0 | ⬜ pending |
| 74-01-16 | 01 | 3 | REND-16 `KM_SLACK_RENDER` env override | unit | `go test ./cmd/km-slack/... -run TestRunWith_EnvOverride` | ❌ W0 | ⬜ pending |

### PR2 — Tier 2 Block Kit (plan `02-tier2-blocks.md`)

| Task ID | Plan | Wave | Requirement | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|-----------|-------------------|-------------|--------|
| 74-02-W0 | 02 | 0 | scaffolding (envelope field, BlockPoster interface, blocks.go skeleton) | manual | `grep -q '"blocks"' pkg/slack/payload.go && grep -q BlockPoster pkg/slack/bridge/interfaces.go` | ❌ W0 | ⬜ pending |
| 74-02-01 | 02 | 1 | BLK-01 `# h` → header block | unit | `go test ./pkg/slack/... -run TestBlocks_H1Header` | ❌ W0 | ⬜ pending |
| 74-02-02 | 02 | 1 | BLK-02 `## h`/`### h` → bold section | unit | `go test ./pkg/slack/... -run TestBlocks_H2H3Section` | ❌ W0 | ⬜ pending |
| 74-02-03 | 02 | 1 | BLK-03 tool-line → context block | unit | `go test ./pkg/slack/... -run TestBlocks_ToolLine` | ❌ W0 | ⬜ pending |
| 74-02-04 | 02 | 1 | BLK-04 `---` → divider, no auto-dividers | unit | `go test ./pkg/slack/... -run TestBlocks_Divider` | ❌ W0 | ⬜ pending |
| 74-02-05 | 02 | 1 | BLK-05 section text 3000-char split | unit | `go test ./pkg/slack/... -run TestBlocks_SectionOverflow` | ❌ W0 | ⬜ pending |
| 74-02-06 | 02 | 1 | BLK-06 50-block cap → Tier 1 fallback | unit | `go test ./pkg/slack/... -run TestBlocks_50BlockFallback` | ❌ W0 | ⬜ pending |
| 74-02-07 | 02 | 1 | BLK-07 `text:` plain-text fallback | unit | `go test ./pkg/slack/... -run TestBlocks_PlainTextFallback` | ❌ W0 | ⬜ pending |
| 74-02-08 | 02 | 2 | BLK-08 Block Kit structural validity | property | `go test ./pkg/slack/... -run TestBlocks_StructuralValidity` | ❌ W0 | ⬜ pending |
| 74-02-09 | 02 | 1 | BLK-09 header strip backtick/asterisk/underscore | unit | `go test ./pkg/slack/... -run TestBlocks_HeaderStrip` | ❌ W0 | ⬜ pending |
| 74-02-10 | 02 | 1 | BLK-10 header >150 chars truncated | unit | `go test ./pkg/slack/... -run TestBlocks_HeaderTruncate` | ❌ W0 | ⬜ pending |
| 74-02-11 | 02 | 2 | BRDG-01 `Blocks=""` → text-only post (backward compat) | bridge unit | `go test ./pkg/slack/bridge/... -run TestHandler_Post_NoBlocks` | ✅ existing | ⬜ pending |
| 74-02-12 | 02 | 2 | BRDG-02 `Blocks!=""` → PostMessageBlocks dispatch | bridge unit | `go test ./pkg/slack/bridge/... -run TestHandler_Post_WithBlocks` | ❌ W0 | ⬜ pending |
| 74-02-13 | 02 | 2 | BRDG-03 canonical JSON `"blocks"` between `"action"`/`"body"` | unit | `go test ./pkg/slack/... -run TestCanonicalJSON_BlocksOrdering` | ❌ W0 | ⬜ pending |
| 74-02-14 | 02 | 3 | HOOK-01 `_km_stream_drain` argv includes `--render` | unit | `go test ./pkg/compiler/... -run TestStreamDrain_RenderFlag` | ❌ W0 | ⬜ pending |
| 74-02-15 | 02 | 3 | bridge integration: real post returns 200 | manual | `km slack test` against a sandbox channel after `make build && km init --lambdas` | n/a | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

PR1 (`01-tokenizer-tier1.md`):
- [ ] `pkg/slack/payload.go` — add `MaxRenderedBytes = 35000` constant
- [ ] `pkg/slack/mrkdwn.go` — file scaffold with `Mrkdwnify(string) string` exported entry point (returns input unchanged for now)
- [ ] `pkg/slack/mrkdwn_test.go` — file scaffold with stub tests for REND-01..REND-16 (each `t.Skip("Wave N implementation")`)
- [ ] `pkg/slack/testdata/` — directory created with at least one placeholder fixture (`bold-collapse.md` + `.expected-mrkdwn.txt`)
- [ ] `cmd/km-slack/main.go` — `--render` flag added to `runPost` (parses but does nothing in W0)
- [ ] `cmd/km-slack/main_test.go` — stubs for REND-14/15/16

PR2 (`02-tier2-blocks.md`):
- [ ] `pkg/slack/payload.go` — `Blocks string \`json:"blocks"\`` field on `SlackEnvelope` in alphabetical position (between `Action` and `Body`)
- [ ] `pkg/slack/blocks.go` — file scaffold with `RenderBlocks(string) (blocksJSON string, fallbackText string, ok bool)` exported entry point
- [ ] `pkg/slack/blocks_test.go` — stubs for BLK-01..BLK-10
- [ ] `pkg/slack/bridge/interfaces.go` — optional `BlockPoster` interface declaration
- [ ] `pkg/slack/bridge/aws_adapters.go` — `PostMessageBlocks` method stub on `SlackPosterAdapter`
- [ ] `pkg/slack/bridge/handler.go` — dispatch branch stub for `env.Blocks != ""`
- [ ] `pkg/slack/bridge/handler_test.go` — stubs for BRDG-02 / BRDG-03
- [ ] `pkg/compiler/userdata.go` — keep `_km_stream_drain` argv unchanged in W0; flip happens in Wave 3

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Bridge integration: real `chat.postMessage` succeeds with `blocks` payload | BRDG-02 (live path) | Requires deployed bridge Lambda + real Slack workspace; not reproducible in unit tests | After `make build && km init --lambdas`, run `km slack test`. Verify the test message renders with header/section/context blocks (not literal `*bold*` text). Inspect Slack channel `#km-test-bridge`. |
| Streaming hook end-to-end: long Claude turn renders cleanly with headings, code blocks, and tool lines visually correct in Slack | REND-* / BLK-* combined | Visual fidelity check; impossible to assert from unit tests | Create a sandbox with `notifySlackTranscriptEnabled: true`, run `km shell`, ask Claude a multi-step question that uses Edit/Read/Bash tools and includes `# headings` + ` ```code blocks``` `. Confirm Slack thread shows: clean bold (no `**`), heading blocks, code in monospace, tool lines as gray context. |
| 50-block cap fallback in production traffic | BLK-06 | Hard to reproduce without a turn that legitimately produces >50 structural elements | Set `KM_SLACK_RENDER=blocks` and run a sandbox session that performs >25 file edits. Verify the post arrives as Tier 1 mrkdwn (one big text block) rather than failing with `too_many_blocks`. |
| Operator safety valve: downgrade via `KM_SLACK_RENDER=plain` without redeploy | REND-16 (operational behavior) | Requires changing a running Lambda env var or sandbox env, not a code path | On a sandbox, `sudo systemctl edit km-slack-stream` to override `Environment=KM_SLACK_RENDER=plain`, restart, post a new turn, confirm raw markdown appears in Slack. |

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 30s (quick suite)
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** pending
