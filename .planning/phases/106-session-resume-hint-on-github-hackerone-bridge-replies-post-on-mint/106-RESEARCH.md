# Phase 106: Session-Resume Hint on GitHub + HackerOne Bridge Replies (Post-on-Mint) - Research

**Researched:** 2026-06-11
**Domain:** `pkg/compiler/userdata.go` — GitHub and HackerOne inbound poller heredocs
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Hint content and format:**
- A GitHub-flavored collapsible `<details>`/`<summary>` fold, posted as a standalone comment:
  ```
  <details>
  <summary>🔧 Resume this agent session</summary>

  On sandbox `sb-1a2b3c4d`, from the `/workspace` folder use `claude --resume 9f8e7d6c-…`
  </details>
  ```
- Agent-correct command branching on `EFFECTIVE_AGENT`: Claude → `claude --resume <id>`; Codex → `codex exec resume <id>`
- `<id>` is the post-run handle: `NEW_GITHUB_SESSION` / `NEW_H1_SESSION`
- Sandbox id is included so the line is self-contained

**Run-from directory = `/workspace` (NOT `/home/sandbox`) — verified**
- Every agent dispatch does `cd /workspace` (GitHub: `userdata.go:2304/2329`; H1: `2616/2627/2639`)
- Session transcript files are at `/home/sandbox/.claude/projects/-workspace/<id>.jsonl` but Claude derives the resumable-session project bucket from the **current working directory**
- `claude --resume` MUST be invoked from `/workspace`; the hint text MUST say `/workspace`

**Injection point = the POLLER, not the agent (locked)**
- Posted by the poller right after session-id extraction and DDB write-back
- Rejected: pre-mint session-id in `km-github`, patching agent's own comment

**Frequency = POST-ON-MINT (locked)**
- Post ONLY when `NEW_*_SESSION != GITHUB_SESSION` (or prior value empty)
- Implemented as a one-line `if` at the write-back site
- Both old and new values are in scope there

**HackerOne safety property (locked)**
- `km-h1` posts INTERNAL by default; hint MUST use internal path (no `--reply-to-researcher`)

**Robustness (locked)**
- Best-effort, non-blocking: `|| true`
- Skip when no session id extracted

**Scope exclusion (locked)**
- Slack poller (`userdata.go` ~1535–2085) MUST stay byte-identical
- No change to km-github or km-h1 Go binaries
- No SandboxProfile schema change, no new TF resource, no new DDB column

### Claude's Discretion
- Exact helper/shell-function factoring inside poller heredocs (shared snippet vs inline per-poller)
- Exact `<summary>` wording and emoji
- Exact env var used to source the sandbox id
- Whether GH+H1 userdata byte-identity golden tests need deliberate golden refresh (yes, see below)

### Deferred Ideas (OUT OF SCOPE)
- Slack resume-hint parity
- Single-comment (appended-to-agent-reply) delivery
- Empirical confirmation of Claude `-p --resume` id stability
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| RESUME-HINT-FORMAT | `<details>`/`<summary>` fold with agent-correct command, sandbox id, `/workspace` directory | Locked design; no code dependency to add |
| RESUME-HINT-MINT | Post only on session-id mint (new or changed); implemented as `if [ "$NEW_*_SESSION" != "$OLD_*_SESSION" ]` after write-back | Both old and new values confirmed in scope at the exact site |
| RESUME-HINT-GITHUB | Injected in `km-github-inbound-poller` heredoc in `userdata.go` after DDB write-back (~line 2391) | Exact location, variables, and call surface confirmed |
| RESUME-HINT-H1 | Injected in `km-h1-inbound-poller` heredoc after DDB write-back (~line 2697); INTERNAL by default | Exact location, variables, and km-h1 call surface confirmed |
| RESUME-HINT-SLACK-EXCLUDED | Slack poller remains byte-identical; existing `TestUserdataH1ByteIdentity` guard pattern applies | Confirmed; H1 golden guards h1-free profile; Slack has separate golden; dormancy must hold |
| RESUME-HINT-TESTS | New `contains`-style tests in `userdata_github_inbound_test.go` and a new H1 `contains` test; H1+GH golden refresh (env-gated capture + commit); Slack golden MUST NOT change | Golden mechanic fully confirmed: env-gated capture, `diffStrings` assertion |
| RESUME-HINT-DOCS | `docs/github-bridge.md` and `docs/h1-bridge.md` updated; deploy = `make build-lambdas` + `km init --dry-run=false` + existing sandbox `km destroy && km create`; operator-guide note in klanker:init SKILL.md fast-path section | Deploy surface confirmed: only create-handler Lambda embeds userdata; bridge Lambdas unaffected |
</phase_requirements>

---

## Summary

Phase 106 is a pure `pkg/compiler/userdata.go` text change: two 8-line blocks (one per poller) inserted at the post-run session-id extraction site, both wrapped in a `|| true` best-effort guard and a one-line `if` that fires only when the session id is freshly minted. No Go struct changes, no Terraform changes, no DDB schema changes, no binary changes.

The two pollers (`km-github-inbound-poller` and `km-h1-inbound-poller`) already hold every required variable at the point of injection: `SANDBOX_ID`, `EFFECTIVE_AGENT`, `NEW_GITHUB_SESSION`/`NEW_H1_SESSION`, `GITHUB_SESSION`/`H1_SESSION` (prior value), `REPO`+`NUMBER` (GitHub), `REPORT_ID` (H1). The injection point is the line immediately following the `echo "[km-...-inbound-poller] Session updated"` log line in each poller.

The primary validation surface is the existing golden-test infrastructure in `pkg/compiler/`. The H1 dormancy golden (`testdata/h1_byte_identity_golden.txt`) will legitimately change after this phase because the H1-enabled path gains new lines; a Wave 0 re-capture under `CAPTURE_PRE_H1_BASELINE=1` must be done against the NEW code and committed. The Slack poller has no golden of its own but the two dormancy golden tests (`TestUserdataH1ByteIdentity` uses `ec2-basic.yaml`, `TestUserdataKmPrefixByteIdentity` uses `learn.v2`) must not change because neither profile enables GitHub-inbound or H1-inbound.

**Primary recommendation:** Insert the hint blocks directly inline (no shared helper function) — the two pollers are already long heredocs, the blocks are 8 lines each, and a shared snippet would require either a Go-level template function (new coupling) or a bash `source` (additional file). Inline keeps the poller self-contained, which is the existing pattern.

---

## Standard Stack

### Core
| Component | Version/Path | Purpose | Notes |
|-----------|-------------|---------|-------|
| `pkg/compiler/userdata.go` | Current HEAD | Template that emits both pollers | Only file that changes |
| `km-github comment` | `cmd/km-github/main.go` | Posts GitHub comment | `--body` accepts inline string (no `@file` prefix needed) |
| `km-h1 comment` | `cmd/km-h1/main.go` | Posts HackerOne comment (INTERNAL by default) | `--body` requires `@file` prefix OR inline string (readBodyArg: bare value = literal) |
| `pkg/compiler/*_test.go` | Current HEAD | Golden + `contains` tests | `extractGitHubInboundPoller`, `extractH1InboundPoller` helpers already exist for GitHub; H1 extractor needs to be added |

### No New Libraries
No new Go imports, no new Terraform modules, no new DDB tables, no new Lambda binaries. The phase is a template-string edit.

---

## Architecture Patterns

### GitHub Inbound Poller — Post-on-Mint Injection Point

**Exact location:** `pkg/compiler/userdata.go:2382–2392` (the `if [ -n "$NEW_GITHUB_SESSION" ]` block).

The variables in scope at that point (confirmed by reading lines 2099–2403):

| Variable | Source | Value at injection point |
|----------|--------|--------------------------|
| `SANDBOX_ID` | `"${KM_SANDBOX_ID:-}"` (line 2100) | The sandbox's ID, e.g. `sb-1a2b3c4d` |
| `EFFECTIVE_AGENT` | computed from `AGENT_OVERRIDE` / `THREAD_AGENT_TYPE` (lines 2271–2280) | `"claude"` or `"codex"` |
| `NEW_GITHUB_SESSION` | `jq -r '.session_id'` / `jq -r '.thread_id'` from output.json (lines 2375–2380) | New session id (non-empty at this point due to outer `if`) |
| `GITHUB_SESSION` | DDB `get-item` `.Item.agent_session_id.S` (line 2200); also cleared on cross-agent reset (line 2274) | OLD session id (empty on first turn; stale id on Gap-E re-mint) |
| `REPO` | `jq -r '.repo'` from SQS envelope (line 2155) | `"owner/repo"` |
| `NUMBER` | `jq -r '.number'` from SQS envelope (line 2156) | PR/issue number |

The injection sits **inside** the `if [ -n "$NEW_GITHUB_SESSION" ] && [ -n "$REPO" ] && [ -n "$NUMBER" ]` block, AFTER the `aws dynamodb update-item` call and the echo log line. The `|| true` on the hint call does not interfere with the outer guard.

**Post-on-mint `if` condition:**

```bash
if [ "$NEW_GITHUB_SESSION" != "$GITHUB_SESSION" ] || [ -z "$GITHUB_SESSION" ]; then
  # post hint
fi
```

Both conditions are in scope. The `[ -z "$GITHUB_SESSION" ]` handles first-turn (no prior session) cleanly. The `[ "$NEW" != "$OLD" ]` handles Gap-E re-mint. Either can be written as a single `if [ "$NEW_GITHUB_SESSION" != "${GITHUB_SESSION:-}" ]` (empty old = empty default → inequality on first turn).

**km-github comment call — body quoting:**

`km-github comment --body` accepts an **inline string** (not `@file`). The `--body` flag is `fs.StringVar(&body, "body", ...)` and `runCommentWith` uses the string directly (`cmd/km-github/main.go:125–134`). The attribution footer (`attributionFooter`) is applied only when `KM_GITHUB_REPLY_AGENT` env var is set; the hint call does NOT set that env var, so the hint will not receive the "via Claude/Codex" footer — correct behavior (the hint is operator-facing meta, not an agent reply).

**Quoting / heredoc safety:** The `<details>` body contains backticks (e.g., `` `sb-…` ``). Inside a bash heredoc with a quoted delimiter (`<< 'GITHUBINBOUND'`), backticks are literal — no shell expansion. The call to `km-github comment --body "..."` must use a temp file or `printf` + `$()` to avoid bash word-splitting the multi-line body. The established pattern in the codebase for multi-line bodies is `printf '%s' "$VAR" > "$TMPFILE"` then `--body "$(cat $TMPFILE)"`, or using a printf-to-variable approach. Since the hint body is a short known template (constructed from shell variables), the safe approach inside the quoted heredoc is:

```bash
HINT_BODY=$(printf '<details>\n<summary>🔧 Resume this agent session</summary>\n\nOn sandbox `%s`, from the `/workspace` folder use `%s`\n</details>' \
  "$SANDBOX_ID" "$RESUME_CMD")
/opt/km/bin/km-github comment --repo "$REPO" --number "$NUMBER" --body "$HINT_BODY" || true
```

This avoids a temp file and is safe because single-quoted heredoc delimiters prevent expansion of backtick content in the template itself. The `$RESUME_CMD` variable is constructed before the call from `EFFECTIVE_AGENT` and `NEW_GITHUB_SESSION`.

### HackerOne Inbound Poller — Post-on-Mint Injection Point

**Exact location:** `pkg/compiler/userdata.go:2688–2698` (the `if [ -n "$NEW_H1_SESSION" ]` block).

The variables in scope:

| Variable | Source | Value at injection point |
|----------|--------|--------------------------|
| `SANDBOX_ID` | `"${KM_SANDBOX_ID:-}"` (line 2425) | Sandbox ID |
| `EFFECTIVE_AGENT` | computed (lines 2587–2596) | `"claude"` or `"codex"` |
| `NEW_H1_SESSION` | jq from output.json (lines 2681–2686) | New session id (non-empty) |
| `H1_SESSION` | DDB `get-item` (line 2527) | OLD session id |
| `REPORT_ID` | `jq -r '.report_id'` from SQS envelope (line 2484) | HackerOne report ID |

The outer guard at H1 write-back site is `if [ -n "$NEW_H1_SESSION" ]` (line 2688). The hint is injected after the `aws dynamodb update-item` and echo log, still inside that block.

**km-h1 comment — body via `@file`:** `km-h1 comment --body` uses `readBodyArg` (`cmd/km-h1/main.go:395–405`): a leading `@` reads a file path; a bare value is treated as a literal. So the hint can be passed either way. However, the H1 body may grow large (future-proofing) and the `@file` pattern is the established convention for km-h1. The short hint is safe as a literal — `--body "$(printf ...)"` — no `@file` required, matching the km-github pattern for consistency.

**INTERNAL by default:** `km-h1 comment` defaults `internal: true` at the JSON-marshalling layer (`cmd/km-h1/main.go:186`). The hint MUST NOT pass `--reply-to-researcher`. This is the safety lock. The hint is operator-facing (it contains sandbox IDs and AWS-account-adjacent handles); it must never go external.

**`REPLY_TO_RESEARCHER` variable:** Present in the H1 poller scope but is only passed to `km-h1` by the agent-dispatch preamble guidance, never by the poller's own post-run code. The hint post follows the same pattern as the "codex-missing guard" at line 2601 which also calls `km-h1 comment` without `--reply-to-researcher`.

### Sandbox ID Source

Both pollers source `SANDBOX_ID` from `KM_SANDBOX_ID` which is baked into the systemd unit's `EnvironmentFile=/etc/km/notify.env`. It is written at compile time via `{{ .SandboxID }}` in the Go template (line 337: `export KM_SANDBOX_ID="{{ .SandboxID }}"`). The pollers each set `SANDBOX_ID="${KM_SANDBOX_ID:-}"` at startup (lines 2100, 2425). No additional plumbing is required — `SANDBOX_ID` is already in scope.

The H1 poller also has `TARGET="${KM_SANDBOX_ALIAS:-$SANDBOX_ID}"` (line 2435) which is the thread-continuity key. The hint uses `SANDBOX_ID` (the actual EC2 sandbox ID), not `TARGET`, because the operator needs the box id for `km shell`.

### Agent-Correct Resume Command Construction

```bash
RESUME_CMD=""
if [ "$EFFECTIVE_AGENT" = "codex" ]; then
  RESUME_CMD="codex exec resume $NEW_GITHUB_SESSION"
else
  RESUME_CMD="claude --resume $NEW_GITHUB_SESSION"
fi
```

This pattern is consistent with how `RESUME_ARG` is already constructed in both pollers (lines 2264–2265 GitHub; 2580–2581 H1).

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Checking "is session new?" | custom DDB read back | compare `NEW_*_SESSION` vs `GITHUB_SESSION`/`H1_SESSION` already in scope | Both old and new values are in scope at write-back site |
| Multi-line body escaping | custom bash quoting | `printf` to variable, pass with `"$HINT_BODY"` | Established pattern; backticks are safe inside `<< 'HEREDOC'` |
| New binary or helper | new Go binary | use existing `km-github comment` / `km-h1 comment` | No binary changes — context says explicitly "no change to km-github/km-h1 Go binaries" |

---

## Common Pitfalls

### Pitfall 1: `km-h1 comment` attribution footer on the hint
**What goes wrong:** The hint body passes through the same `km-h1` path as agent replies. If `KM_H1_REPLY_AGENT` is set in the poller scope (it is — set inline in the `sudo -u sandbox` string), it does NOT survive the `sudo` barrier into the `km-h1` call made by root-level code.
**Why it happens:** `KM_H1_REPLY_AGENT` is exported inside `sudo -u sandbox bash -lc "..."` strings, not in the poller's outer shell.
**How to avoid:** The hint call is made by the poller's outer root shell (after the sudo block returns), so `KM_H1_REPLY_AGENT` is NOT set there. There is no attribution footer concern — `km-h1` has no attribution footer at all (that's only `km-github`).
**Verdict:** Non-issue.

### Pitfall 2: `km-github comment` attribution footer appended to the hint
**What goes wrong:** `km-github comment` calls `attributionFooter(body, os.Getenv("KM_GITHUB_REPLY_AGENT"))`. If `KM_GITHUB_REPLY_AGENT` is set in the outer shell, the hint would get "via Claude/Codex" appended.
**Why it happens:** Unlike the H1 poller, the GitHub poller's outer shell does NOT set `KM_GITHUB_REPLY_AGENT` — it is only set inside the `sudo -u sandbox bash -lc "..."` strings (lines 2303, 2315, 2328, 2358). The hint post is after the sudo block.
**How to avoid:** Nothing to do — `KM_GITHUB_REPLY_AGENT` is not in scope at the hint-post site.
**Verdict:** Non-issue.

### Pitfall 3: Hint fires on Gap-E retry (cross-box stale session)
**What goes wrong:** Gap-E path (lines 2336–2365) clears `GITHUB_SESSION=""` and `RESUME_ARG=""` before retrying as a fresh session. After retry, `NEW_GITHUB_SESSION` will be freshly minted but `GITHUB_SESSION` is already `""`. The post-on-mint `if` fires, which is correct — a re-mint after a stale cross-box session IS a new handle the operator needs.
**Verdict:** Correct behavior; no special handling needed.

### Pitfall 4: `km-h1 comment` for hint does not set `--reply-to-researcher`
**What goes wrong:** Accidentally omitting the guard and passing `--reply-to-researcher`.
**How to avoid:** The hint call MUST NOT include `--reply-to-researcher`. The default `internal: true` behavior is exactly right. Document explicitly in plan.

### Pitfall 5: Body quoting — backtick in `<details>` fold
**What goes wrong:** A backtick in the `<details>` body causes subshell expansion in double-quoted `"..."` bash strings (though NOT in `<< 'GITHUBINBOUND'` heredoc text). The `printf` into a variable with `$()` is safe if the backticks are inside single-quoted format strings.
**How to avoid:** Use `printf '<details>...\`%s\`...' "$SANDBOX_ID"` (single-quote the format string so the backtick around `%s` is literal). Alternatively use `$(printf ...)` assigned to `HINT_BODY` then `--body "$HINT_BODY"`.

### Pitfall 6: Golden test drift — H1 dormancy golden
**What goes wrong:** Phase 106 adds new lines to the H1 poller `{{ if .H1InboundEnabled }}` block. The existing `TestUserdataH1ByteIdentity` uses the `ec2-basic.yaml` profile which has NO H1 inbound enabled — so the golden must still match. Since the H1 poller block is gated on `H1InboundEnabled`, the dormancy golden does NOT change.
**How to avoid:** The H1 dormancy golden (`h1_byte_identity_golden.txt`) covers an H1-FREE profile. Adding lines inside `{{- if .H1InboundEnabled }}` does NOT affect the dormancy golden.
**Verdict:** `TestUserdataH1ByteIdentity` MUST still pass unchanged after Phase 106. No golden refresh needed for the dormancy test.

### Pitfall 7: km-github poller's byte-identity test
**What goes wrong:** `TestUserdataKmPrefixByteIdentity` uses `profiles/learn.v2.yaml` (an H1-free AND GitHub-inbound-free profile). Changes to the GitHub poller heredoc do NOT affect it.
**How to avoid:** Changes are inside `{{- if .GitHubInboundEnabled }}`. The golden for `learn.v2` is safe.
**Verdict:** `TestUserdataKmPrefixByteIdentity` MUST still pass unchanged.

### Pitfall 8: H1 `km-h1 comment` for the hint must write body to a tempfile if the body contains newlines and is passed as `"$VAR"`
**What goes wrong:** Multi-line `$HINT_BODY` passed as `--body "$HINT_BODY"` can fail if the body contains newlines and the shell tokenizes it. However, `flag.StringVar` reads the entire `--body` value including whitespace as a single token when passed as a shell-quoted `"$HINT_BODY"` variable. This is safe.
**Verdict:** `printf` into `$HINT_BODY` then `--body "$HINT_BODY"` is safe in bash.

---

## Code Examples

### GitHub Poller — Post-on-Mint Block (injection at ~line 2391)

The existing write-back block ends at line 2391 with the echo log. The hint block inserts after it, still inside the `if [ -n "$NEW_GITHUB_SESSION" ] && ...` guard:

```bash
# (existing) DDB write-back
aws dynamodb update-item \
  --table-name "$GITHUB_THREADS_TABLE" \
  --key "{\"repo\":{\"S\":\"$REPO\"},\"number\":{\"N\":\"$NUMBER\"}}" \
  --update-expression "SET agent_session_id = :sid, agent_type = :at" \
  --expression-attribute-values "{\":sid\":{\"S\":\"$NEW_GITHUB_SESSION\"},\":at\":{\"S\":\"$EFFECTIVE_AGENT\"}}" \
  --region "$REGION" 2>/dev/null || true
echo "[km-github-inbound-poller] Session updated — repo=$REPO PR=#$NUMBER session=${NEW_GITHUB_SESSION:0:8}... agent=$EFFECTIVE_AGENT"

# Phase 106: post resume-hint fold on session mint (first turn or re-mint after
# Gap-E cross-box stale session). Post-on-mint: fires only when the session id
# is new or changed. Best-effort (|| true) — a failed hint post MUST NOT block
# the SQS ack or turn completion.
if [ -n "$NEW_GITHUB_SESSION" ] && [ "$NEW_GITHUB_SESSION" != "${GITHUB_SESSION:-}" ]; then
  if [ "$EFFECTIVE_AGENT" = "codex" ]; then
    RESUME_CMD="codex exec resume $NEW_GITHUB_SESSION"
  else
    RESUME_CMD="claude --resume $NEW_GITHUB_SESSION"
  fi
  HINT_BODY=$(printf '<details>\n<summary>🔧 Resume this agent session</summary>\n\nOn sandbox `%s`, from the `/workspace` folder use `%s`\n</details>' \
    "$SANDBOX_ID" "$RESUME_CMD")
  /opt/km/bin/km-github comment --repo "$REPO" --number "$NUMBER" --body "$HINT_BODY" || true
fi
```

### H1 Poller — Post-on-Mint Block (injection at ~line 2697)

```bash
# (existing) DDB write-back
aws dynamodb update-item \
  --table-name "$H1_THREADS_TABLE" \
  --key "{\"report_id\":{\"S\":\"$REPORT_ID\"},\"target\":{\"S\":\"$TARGET\"}}" \
  --update-expression "SET agent_session_id = :sid, agent_type = :at" \
  --expression-attribute-values "{\":sid\":{\"S\":\"$NEW_H1_SESSION\"},\":at\":{\"S\":\"$EFFECTIVE_AGENT\"}}" \
  --region "$REGION" 2>/dev/null || true
echo "[km-h1-inbound-poller] Session updated — report=$REPORT_ID target=$TARGET session=${NEW_H1_SESSION:0:8}... agent=$EFFECTIVE_AGENT"

# Phase 106: post resume-hint fold on session mint (internal by default — safety
# layer 4 preserved; the hint never goes external). Best-effort (|| true).
if [ -n "$NEW_H1_SESSION" ] && [ "$NEW_H1_SESSION" != "${H1_SESSION:-}" ]; then
  if [ "$EFFECTIVE_AGENT" = "codex" ]; then
    RESUME_CMD="codex exec resume $NEW_H1_SESSION"
  else
    RESUME_CMD="claude --resume $NEW_H1_SESSION"
  fi
  HINT_BODY=$(printf '<details>\n<summary>🔧 Resume this agent session</summary>\n\nOn sandbox `%s`, from the `/workspace` folder use `%s`\n</details>' \
    "$SANDBOX_ID" "$RESUME_CMD")
  /opt/km/bin/km-h1 comment --report "$REPORT_ID" --body "$HINT_BODY" || true
fi
```

Note: `km-h1 comment` defaults `internal: true` — no `--reply-to-researcher` flag needed or wanted.

---

## Validation Architecture

Nyquist validation is enabled (`workflow.nyquist_validation: true`).

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (`go test`) |
| Config file | none — standard Go test runner |
| Quick run command | `go test ./pkg/compiler/ -run TestUserdata_GitHubInboundPoller -count=1` |
| Full suite command | `go test ./pkg/compiler/ -count=1 -timeout 120s` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| RESUME-HINT-GITHUB | Hint block appears in GitHub poller when GH-inbound enabled | unit/contains | `go test ./pkg/compiler/ -run TestUserdata_GitHubInboundPoller_ResumeHint -count=1` | ❌ Wave 0 |
| RESUME-HINT-MINT | Hint block conditional: `NEW_GITHUB_SESSION != GITHUB_SESSION` present | unit/contains | `go test ./pkg/compiler/ -run TestUserdata_GitHubInboundPoller_ResumeHintMintCondition -count=1` | ❌ Wave 0 |
| RESUME-HINT-H1 | Hint block appears in H1 poller when H1-inbound enabled; no `--reply-to-researcher` | unit/contains | `go test ./pkg/compiler/ -run TestUserdataH1EnabledRendersPoller -count=1` (extend with new assertions) | ✅ extend |
| RESUME-HINT-SLACK-EXCLUDED | Slack poller unchanged (dormancy golden) | unit/byte-identity | `go test ./pkg/compiler/ -run TestUserdataH1ByteIdentity -count=1` | ✅ existing (must PASS) |
| RESUME-HINT-SLACK-EXCLUDED | km-prefix golden unchanged | unit/byte-identity | `go test ./pkg/compiler/ -run TestUserdataKmPrefixByteIdentity -count=1` | ✅ existing (must PASS) |
| RESUME-HINT-TESTS | Full compiler suite green | unit | `go test ./pkg/compiler/ -count=1 -timeout 120s` | ✅ existing |
| RESUME-HINT-DOCS | docs/github-bridge.md + docs/h1-bridge.md have Phase 106 entry | manual | — | ❌ Wave 0 (docs only) |

### Sampling Rate
- **Per task commit:** `go test ./pkg/compiler/ -run TestUserdata_GitHubInbound -count=1 && go test ./pkg/compiler/ -run TestUserdataH1 -count=1`
- **Per wave merge:** `go test ./pkg/compiler/ -count=1 -timeout 120s`
- **Phase gate:** Full compiler suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/compiler/userdata_github_inbound_test.go` — add `TestUserdata_GitHubInboundPoller_ResumeHint` containing assertions for:
  - `<details>` fold present in GitHub poller when enabled
  - `claude --resume` / `codex exec resume` branches
  - `SANDBOX_ID` variable referenced
  - `/workspace` string present in hint construction
  - `|| true` guard present (non-blocking)
  - `!= ${GITHUB_SESSION:-}` mint condition present
- [ ] `pkg/compiler/userdata_h1_byte_identity_test.go` — extend `TestUserdataH1EnabledRendersPoller` wantSubstrings to include:
  - `claude --resume` / `codex exec resume`
  - `<details>` or `🔧 Resume`
  - `--report "$REPORT_ID" --body` (hint km-h1 call — no `--reply-to-researcher`)
- [ ] `docs/github-bridge.md` — add Phase 106 section (`## Phase 106`)
- [ ] `docs/h1-bridge.md` — add Phase 106 section (`## Phase 106`)

**H1 dormancy golden `h1_byte_identity_golden.txt`:** The existing golden covers `ec2-basic.yaml` (no H1-inbound). Adding lines inside `{{- if .H1InboundEnabled }}` does NOT change what the `ec2-basic.yaml` profile renders. `TestUserdataH1ByteIdentity` MUST remain green without a new golden capture. This is the key dormancy invariant that must be verified in Wave 0 (run the test after edits; it must still PASS).

---

## Deploy / Operator Surface

Phase 106 only changes `pkg/compiler/userdata.go`. The userdata is embedded in the `create-handler` Lambda at build time.

**Deploy sequence:**
```bash
make build-lambdas   # rebuilds create-handler.zip which embeds the new userdata
km init --dry-run=false   # NOT --sidecars (env block unchanged; but create-handler zip must be re-uploaded via terragrunt apply)
```

Wait — `km init --sidecars` only builds and uploads the sidecar binaries, it does NOT update the create-handler zip. The create-handler is deployed by terragrunt apply. However, `make build-lambdas` DOES build the create-handler zip. And `km init --dry-run=false` runs the full terragrunt apply which uploads and deploys the new create-handler zip.

Alternatively: `make build-lambdas` + `km init --sidecars` only fast-deploys the sidecar binaries + cold-starts Lambdas but does NOT upload the new create-handler zip via terragrunt. So a **full `km init --dry-run=false`** is required to upload the new create-handler which contains the new userdata.

Per CONTEXT.md `<specifics>`: "poller is compiled into userdata by the create-handler Lambda ⇒ `make build-lambdas` (clean) so the create-handler embeds the new userdata".

Confirmed deploy:
- `make build-lambdas` — rebuilds create-handler.zip with new userdata template
- `km init --dry-run=false` — full terragrunt apply, uploads new create-handler.zip
- **NOT `--sidecars`** alone (insufficient — won't upload create-handler code)
- Existing sandboxes need `km destroy && km create` to pick up the new poller heredoc
- Bridge Lambdas (km-github-bridge, km-h1-bridge) are UNAFFECTED — no Lambda code changes
- No new TF resource, no new DDB column, no SandboxProfile schema change

**`km init --github` / `km init --h1` are INSUFFICIENT for this phase** — those fast-path scoped applies refresh env+IAM only; they do not update the create-handler zip.

---

## Sources

### Primary (HIGH confidence)
- Direct code inspection: `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go:2095–2407` (GitHub poller) and `:2408–2714` (H1 poller)
- `cmd/km-github/main.go` — `--body` as inline string confirmed
- `cmd/km-h1/main.go:391–405` — `readBodyArg` accepts both bare literal and `@file`
- `pkg/compiler/userdata_github_inbound_test.go` — confirmed existing `extractGitHubInboundPoller` + test structure
- `pkg/compiler/userdata_h1_byte_identity_test.go` — confirmed golden mechanic, `CAPTURE_PRE_H1_BASELINE=1` env-gate, `diffStrings` assertion
- `internal/app/cmd/init.go:2985–3002` — `sidecarBuilds()` confirms km-github + km-h1 uploaded by `km init --sidecars`
- `go test ./pkg/compiler/ -count=1` — full suite PASS confirmed at HEAD before any changes

### Secondary (MEDIUM confidence)
- `docs/h1-bridge.md:317–354` — deploy sequence confirms `make build` + `make build-lambdas` + `km init --dry-run=false` pattern
- `docs/github-bridge.md:347–374` — Phase 97 deploy sequence confirms same pattern
- `Makefile:220–248` — `build-lambdas` target confirmed to include `cmd/create-handler`

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — direct code reading of exact injection sites, variable scope, and CLI surfaces
- Architecture: HIGH — injection points, variable availability, quoting patterns confirmed by code
- Pitfalls: HIGH — attribution footer non-issue confirmed by code paths; quoting confirmed by bash semantics inside quoted heredoc; golden impact analyzed by reading test code
- Deploy: HIGH — Makefile + docs + init.go confirm `make build-lambdas` + `km init --dry-run=false` required

**Research date:** 2026-06-11
**Valid until:** Stable — `pkg/compiler/userdata.go` heredoc structure changes slowly; valid for 60 days or until next GitHub/H1 poller phase
