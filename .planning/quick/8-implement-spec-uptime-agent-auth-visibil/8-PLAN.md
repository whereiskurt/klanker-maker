---
phase: quick-8
plan: 8
type: execute
wave: 1
depends_on: []
files_modified:
  - internal/app/cmd/agent_auth_check.go
  - internal/app/cmd/status.go
  - internal/app/cmd/list.go
  - internal/app/cmd/uptime_test.go
  - internal/app/cmd/status_test.go
  - internal/app/cmd/list_test.go
autonomous: true
requirements: [SPEC-2026-06-07-km-uptime-auth]
must_haves:
  truths:
    - "km list prints a version+timestamp banner at the top (suppressed in --json mode)"
    - "km list shows a UP column with uptime for running rows, '-' otherwise"
    - "km status shows an Uptime: line for running sandboxes, derived from CreatedAt"
    - "km status shows an Auth: section (claude/codex logged-in state) for running sandboxes"
    - "km list --auth shows an AUTH column (cl✓ cx✗) via concurrent SSM fan-out; without --auth zero SSM calls"
    - "formatUptime renders compact 8m / 3h12m / 2d4h forms"
  artifacts:
    - path: "internal/app/cmd/agent_auth_check.go"
      provides: "formatUptime helper + AgentAuthChecker interface + checkAgentAuth single-SSM implementation"
      contains: "func formatUptime"
    - path: "internal/app/cmd/status.go"
      provides: "Uptime: line + Auth: section in printSandboxStatus"
    - path: "internal/app/cmd/list.go"
      provides: "banner call, UP column, --auth flag + concurrent fan-out, AUTH column"
  key_links:
    - from: "internal/app/cmd/list.go runList"
      to: "fprintBanner"
      via: "fprintBanner(cmd.OutOrStdout(), \"km list\", summary)"
      pattern: "fprintBanner.*km list"
    - from: "internal/app/cmd/list.go"
      to: "AgentAuthChecker.CheckAuth"
      via: "concurrent goroutine pool over running rows when --auth set"
      pattern: "AgentAuthChecker"
    - from: "internal/app/cmd/status.go printSandboxStatus"
      to: "AgentAuthChecker.CheckAuth"
      via: "always called for running sandbox, soft-fails on SSM error"
      pattern: "CheckAuth"
---

<objective>
Implement the spec at `docs/superpowers/specs/2026-06-07-km-uptime-auth-design.md` EXACTLY — no more, no less. Surface three things in `km list` / `km status`:

1. A version+timestamp banner at the top of `km list` (reusing existing `fprintBanner`).
2. Per-sandbox **uptime** (age since `CreatedAt`) in both `km status` (Uptime: line) and `km list` (UP column).
3. Per-sandbox **agent auth** (claude/codex logged-in) — always in `km status` for running boxes, behind opt-in `--auth` in `km list`.

Purpose: At-a-glance operator visibility without new AWS surface.
Output: Edits to `list.go`, `status.go`, a new `agent_auth_check.go` (helper + interface), plus tests.

**Strict non-goals (do NOT add):** no sandbox-side changes, no DynamoDB schema change, no EC2 `LaunchTime` (uptime is `CreatedAt`-derived; pause/resume does NOT reset it), no caching, no heartbeat, no new Lambda. No auth check on non-running sandboxes.
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@docs/superpowers/specs/2026-06-07-km-uptime-auth-design.md
@.planning/STATE.md
@CLAUDE.md
@internal/app/cmd/list.go
@internal/app/cmd/status.go
@internal/app/cmd/agent_auth.go
@internal/app/cmd/root.go

<interfaces>
<!-- Key contracts the executor needs — extracted from the codebase. Use these directly. -->

Banner helper (root.go:164) — already used by km status:
```go
func fprintBanner(w io.Writer, cmd, context string)
// renders: "<cmd> — <context> [<version>] <timestamp>\n" + a 46-char rule line.
```

SSM machinery (agent.go + agent_auth.go) to reuse:
```go
type SSMSendAPI interface {
    SendCommand(ctx context.Context, input *ssm.SendCommandInput, optFns ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
    GetCommandInvocation(ctx context.Context, input *ssm.GetCommandInvocationInput, optFns ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
}
func sendSSMAndWait(ctx context.Context, ssmClient SSMSendAPI, instanceID, shellCmd string) (string, error)
```

Instance-ID resolution from a SandboxRecord (shell.go:856):
```go
func extractResourceID(resources []string, pattern string) (string, error)
// usage in agent_auth.go: instanceID, err := extractResourceID(rec.Resources, ":instance/")
```

claude logged-in parse pattern (agent_auth.go verifyClaudeAuthStatus): the box runs
`sudo -u sandbox bash -lc 'claude auth status 2>&1'` and we check for
`"loggedIn": true` OR `"loggedIn":true` (tolerant of spacing).
codex check: `test -f /home/sandbox/.codex/auth.json`.

SandboxRecord fields available (no new fields needed): SandboxID, Alias, Profile,
Substrate, Region, Status ("running" etc.), CreatedAt (time.Time), Resources ([]string ARNs).

DI seams already present (mirror these exactly):
- list.go:  func NewListCmdWithLister(cfg, lister SandboxLister) *cobra.Command
- status.go: func NewStatusCmdWithAllFetchers(cfg, fetcher, budgetFetcher, identityFetcher) *cobra.Command
- list_test.go / status_test.go inject fakes through those constructors; no real AWS.
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: formatUptime helper + AgentAuthChecker interface + checkAgentAuth (new file)</name>
  <files>internal/app/cmd/agent_auth_check.go, internal/app/cmd/uptime_test.go</files>
  <behavior>
    formatUptime(createdAt time.Time) string — compact, computed against time.Now():
    - < 1h  → "8m"        (minutes only)
    - 1h–<1d → "3h12m"    (hours+minutes; omit trailing 0m? spec shows "3h12m" — keep H+M, but a clean hour like 3h0m may render "3h" — pick ONE rule and unit-test it; prefer: always Hh, append Mm only when M>0)
    - >= 1d  → "2d4h"     (days+hours; append Hh only when H>0)
    - zero/sub-minute → "0m"
    Unit-test each band directly with a fixed createdAt = time.Now().Add(-d).
  </behavior>
  <action>
Create `internal/app/cmd/agent_auth_check.go` in package `cmd`. Add exactly three things:

1. `func formatUptime(createdAt time.Time) string` — PURE helper, compact form per the spec
   (`8m`, `3h12m`, `2d4h`). Compute `d := time.Since(createdAt)`. Bands: <1h minutes-only;
   <24h `<H>h<M>m` (drop the `m` segment when M==0 → e.g. "3h"); else `<D>d<H>h` (drop the
   `h` segment when H==0 → e.g. "2d"). Guard negatives/zero → "0m". No color, no AWS.

2. `type AgentAuthChecker interface { CheckAuth(ctx context.Context, rec *kmaws.SandboxRecord) (claudeLoggedIn bool, codexLoggedIn bool, err error) }`
   — the DI seam so tests stub auth with no AWS.

3. The real implementation `ssmAgentAuthChecker` wrapping an `SSMSendAPI`, plus a package-level
   `func checkAgentAuth(ctx, ssm SSMSendAPI, instanceID string) (bool, bool, error)` modeled on
   `agent_auth.go`. ONE SSM round-trip running BOTH checks:
   ```
   sudo -u sandbox bash -lc 'claude auth status 2>/dev/null'
   test -f /home/sandbox/.codex/auth.json && echo KM_CODEX_OK || echo KM_CODEX_MISSING
   ```
   Parse claude via `strings.Contains(out, "\"loggedIn\": true") || strings.Contains(out, "\"loggedIn\":true")`
   (matches `verifyClaudeAuthStatus`); parse codex via `strings.Contains(out, "KM_CODEX_OK")`.
   `CheckAuth` resolves the instance ID from `rec.Resources` via `extractResourceID(rec.Resources, ":instance/")`,
   then delegates to `checkAgentAuth`. Use `sendSSMAndWait` for the round-trip.

Do NOT touch sandbox-side anything; this is read-only SSM. Keep it small — one file.
  </action>
  <verify>
    <automated>go test ./internal/app/cmd/ -run 'TestFormatUptime' -count=1</automated>
  </verify>
  <done>agent_auth_check.go compiles; uptime_test.go covers the three bands + zero case; formatUptime returns "8m"/"3h12m"/"2d4h"-shaped strings; AgentAuthChecker + checkAgentAuth exist with the single-SSM dual check.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: km status — Uptime: line + Auth: section (DI-injected checker)</name>
  <files>internal/app/cmd/status.go, internal/app/cmd/status_test.go</files>
  <behavior>
    - Running sandbox: printSandboxStatus emits "Uptime:     <formatUptime(CreatedAt)>" directly under the "Created At:" line.
    - Non-running sandbox: NO Uptime line, NO Auth section.
    - Running sandbox with a stubbed AgentAuthChecker returning (true,false,nil): prints
        Auth:
          claude:  ✓ logged in
          codex:   ✗ not logged in
    - Stubbed checker returning an error: prints a soft "Auth: <unavailable: ...>" line; command still exits 0.
    - All assertions use the fake checker injected through the new constructor seam — no real AWS.
  </behavior>
  <action>
Thread an optional `AgentAuthChecker` through the status command's DI chain WITHOUT breaking
existing constructors:

1. Add `func NewStatusCmdWithChecker(cfg, fetcher, budgetFetcher, identityFetcher, checker AgentAuthChecker) *cobra.Command`
   as the new widest overload, and have `NewStatusCmdWithAllFetchers` delegate to it with `nil` checker
   (preserves all existing call sites + tests). When `checker == nil` at runtime AND the real AWS path is
   taken, construct an `ssmAgentAuthChecker` from the same `awsCfg` already loaded in `runStatus`.

2. In `printSandboxStatus` (it already takes `ctx`): pass the resolved `checker` down (extend its signature,
   updating the single call site in `runStatus`). After the existing `Created At:` line, when
   `rec.Status == "running"`, print `fmt.Fprintf(out, "Uptime:      %s\n", formatUptime(rec.CreatedAt))`
   (align the label width with the surrounding lines).

3. Still only when `rec.Status == "running"` and `checker != nil`: call `checker.CheckAuth(ctx, rec)`.
   On success print the `Auth:` block with `✓ logged in` / `✗ not logged in` per agent. On error print
   one soft line `fmt.Fprintf(out, "Auth: <unavailable: %v>\n", err)` and continue (never fail the command).
   Skip the whole section for non-running sandboxes.

Keep edits surgical — do not reorder unrelated sections. No schema/struct changes.
  </action>
  <verify>
    <automated>go test ./internal/app/cmd/ -run 'TestStatus' -count=1</automated>
  </verify>
  <done>Running-sandbox status shows Uptime: + Auth: (claude/codex) using the injected fake checker; non-running shows neither; checker-error path prints the soft unavailable line and exits 0; existing status tests still pass.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 3: km list — banner + UP column + --auth flag/fan-out + AUTH column (DI checker)</name>
  <files>internal/app/cmd/list.go, internal/app/cmd/list_test.go</files>
  <behavior>
    - Non-JSON list (incl. empty "No running sandboxes.") prints the banner line "km list — <N sandboxes> [..] <ts>"; --json output does NOT contain the banner (still valid JSON array).
    - printSandboxTable shows a UP column in BOTH narrow and --wide layouts: running rows = formatUptime(CreatedAt), all others = "-".
    - Without --auth: zero SSM calls (fake checker's CheckAuth never invoked); behavior otherwise identical to today plus banner+UP.
    - With --auth: an AUTH column appears; running rows render "cl✓ cx✗" from the injected checker; non-running rows show "-". --wide alone does NOT enable --auth.
    - Tests inject a fake AgentAuthChecker (and count CheckAuth invocations) through a new constructor seam.
  </behavior>
  <action>
1. Banner: at the top of `runList`, after records are fetched, when `!jsonOutput` call
   `fprintBanner(cmd.OutOrStdout(), "km list", summary)` where `summary` is e.g.
   `fmt.Sprintf("%d sandboxes", len(records))` (use singular/plural or just "%d sandboxes" — keep simple).
   Emit it on BOTH the empty-list path (before "No running sandboxes.") and the normal path.
   MUST be suppressed when `jsonOutput` is true so the JSON array is never corrupted.

2. `--auth` flag: add `var auth bool; cmd.Flags().BoolVar(&auth, "auth", false, "Check agent (claude/codex) login state per running sandbox via SSM")`.
   Thread it into `runList`. Do NOT auto-enable on `--wide`.

3. DI seam: add `func NewListCmdWithCheckers(cfg, lister SandboxLister, checker AgentAuthChecker) *cobra.Command`
   as the widest overload; have `NewListCmdWithLister` delegate with `nil` checker (keeps existing tests).
   When `auth` is set and `checker == nil` on the real path, build an `ssmAgentAuthChecker` from the
   `awsCfg` already loaded in `runList`.

4. UP column in `printSandboxTable`: add a compact `UP` column to BOTH the narrow and the two `--wide`
   header/row printf groups. Running rows → `formatUptime(r.CreatedAt)`; everything else → `-`. Keep it
   narrow (a few chars). Maintain alignment (the existing code uses fixed-width printf, not tabwriter).

5. AUTH fan-out (only when `auth==true`): mirror the existing per-row EC2 status loop but bounded-concurrent
   (a small goroutine pool, e.g. a buffered semaphore channel of ~8, with a sync.WaitGroup) calling
   `checker.CheckAuth(ctx, &records[i])` ONLY for `records[i].Status == "running"`. Store results in a
   `map[string]string` keyed by SandboxID (value like `cl✓ cx✗`). Pass that map into `printSandboxTable`
   so it can render an extra `AUTH` column (running rows = the string, others = "-"). When `auth==false`,
   make ZERO checker calls and render no AUTH column.

Surgical edits only — preserve the existing reconcile/sort/JSON paths verbatim.
  </action>
  <verify>
    <automated>go test ./internal/app/cmd/ -run 'TestList' -count=1</automated>
  </verify>
  <done>Banner present on non-JSON + empty paths, absent in --json; UP column in narrow and wide; --auth triggers concurrent CheckAuth and an AUTH column ("cl✓ cx✗"); no --auth = zero CheckAuth calls; existing list tests still pass.</done>
</task>

</tasks>

<verification>
- `make build` succeeds (ldflags-stamped binary — NOT bare `go build`).
- `go test ./internal/app/cmd/...` passes (new + existing).
- Spot-check: `./km list` shows banner + UP; `./km list --json | head` is valid JSON with no banner; `./km list --auth` adds AUTH (running rows). `./km status <running-id>` shows Uptime: + Auth:.
- Manual against a live running sandbox (operator, optional): `./km list --auth`, `./km status <id>`.
</verification>

<success_criteria>
- formatUptime renders `8m` / `3h12m` / `2d4h` compact forms (unit-tested).
- km list: banner (suppressed in --json), UP column (narrow+wide), --auth flag with concurrent fan-out + AUTH column, zero SSM without --auth.
- km status: Uptime: line + Auth: section for running boxes only; soft-fail on SSM error.
- AgentAuthChecker injected through DI seams; all new tests run with no real AWS.
- No sandbox-side / schema / LaunchTime / caching scope added.
- `make build` + `go test ./internal/app/cmd/...` green.
</success_criteria>

<output>
After completion, create `.planning/quick/8-implement-spec-uptime-agent-auth-visibil/8-SUMMARY.md`
</output>
