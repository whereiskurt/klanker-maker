# Slack reaction quiet period + email allowlists Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Thread replies get 👀 only after a quiet period; `allow_emails` accepts `*@domain`; a klanker's profile can carry its own `allowEmails` that replaces the install list everywhere, including the mention that cold-creates it.

**Architecture:** One shared `EmailAllowlist` matcher (exact + domain) used by the route, by per-sandbox lists carried on the `km-sandboxes` row, and by `km-slack dm`. `km init` resolves the route's effective list from the route profile and exports it, so the bridge's existing route gate covers the first turn. The quiet period reads the thread row's `last_dispatch_ts` in the react step. All knobs follow the existing tri-state install/profile/row/`SandboxRoutingInfo` pattern.

**Tech Stack:** Go (bridge Lambda, km CLI, profile schema), Terraform var + terragrunt `get_env`, DynamoDB string attributes.

**Spec:** `docs/superpowers/specs/2026-10-17-slack-react-quiet-and-email-allowlists-design.md`

## Global Constraints

- Wildcard form is exactly `*@<domain>` (regex `^\*@[a-z0-9.-]+\.[a-z]{2,}$` after lowercasing); every other `*` is rejected by `km init`/`km validate`. `a@sub.example.com` does NOT match `*@example.com`.
- Per-klanker `allowEmails`: absent ⇒ install list; present ⇒ **replaces** the install list for that sandbox; present-but-empty ⇒ validation error. Never "empty means everyone".
- Quiet period default `180` when the install key is absent; `0` disables. Top-level messages always react. `react_always: false` still silences thread replies regardless.
- Row read errors ⇒ react (quiet period) / deny (email gate). A deny is never cached.
- New config keys go in the v2→v merge-list (`internal/app/config/config.go` ~line 1188) or they are silently ignored; new row attributes round-trip through `pkg/aws/sandbox_dynamo.go` marshal AND unmarshal; new profile fields go in `pkg/profile/schemas/sandbox_profile.schema.json` (`additionalProperties:false`) and are tested through YAML → `ValidateSchema`.
- Build/vet/test before every commit; stage by pathspec; trailers `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` and `Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt`. Branch `feat/slack-react-quiet-email-allowlists`; never push.

---

### Task 1: `EmailAllowlist` with `*@domain`; route, config validation, doctor

**Files:**
- Modify: `pkg/slack/bridge/default_route.go` (`DefaultRoute`, `NewDefaultRoute`, `Allows`)
- Modify: `internal/app/config/config.go` (`SlackDefaultRoute.NormalizedEmails`/`Validate`, ~line 170–200)
- Modify: `internal/app/cmd/doctor_slack.go` (`checkSlackDefaultRoute` allowlist case, ~line 686–708)
- Test: `pkg/slack/bridge/default_route_test.go` (append), `internal/app/config/config_slack_default_route_test.go` (find the existing route tests; append), `internal/app/cmd/doctor_slack_default_route_test.go` (append)

**Interfaces:**
- Produces: `bridge.EmailAllowlist` (`CompileEmailAllowlist(entries []string) (*EmailAllowlist, error)`, `(*EmailAllowlist).Allows(email string) bool`, `(*EmailAllowlist).Len() int`); `bridge.ValidateEmailEntry(s string) (normalized string, isDomain bool, err error)`; `DefaultRoute.Allows` delegates to it; `config.SlackDefaultRoute.Validate` uses `ValidateEmailEntry`.

- [ ] **Step 1: Failing tests**

`pkg/slack/bridge/default_route_test.go`:
```go
func TestEmailAllowlist_ExactAndDomain(t *testing.T) {
	al, err := CompileEmailAllowlist([]string{"Kurt@Example.com", "*@Corp.example"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ok := range []string{"kurt@example.com", "KURT@example.com", "bob@corp.example", "Alice@CORP.EXAMPLE"} {
		if !al.Allows(ok) {
			t.Errorf("%q must be allowed", ok)
		}
	}
	for _, no := range []string{"bob@example.com", "a@sub.corp.example", "corp.example", "", "*@corp.example"} {
		if al.Allows(no) {
			t.Errorf("%q must be denied", no)
		}
	}
	if al.Len() != 2 {
		t.Fatalf("Len = %d", al.Len())
	}
}

func TestValidateEmailEntry_RejectsOtherGlobs(t *testing.T) {
	for _, bad := range []string{"*", "*@", "*@*", "k*@example.com", "*@example", "kurt@", "@example.com", "*@exa mple.com"} {
		if _, _, err := ValidateEmailEntry(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	n, isDomain, err := ValidateEmailEntry(" *@Example.COM ")
	if err != nil || !isDomain || n != "*@example.com" {
		t.Fatalf("got %q %v %v", n, isDomain, err)
	}
	n, isDomain, err = ValidateEmailEntry("Kurt@Example.com")
	if err != nil || isDomain || n != "kurt@example.com" {
		t.Fatalf("got %q %v %v", n, isDomain, err)
	}
}

func TestDefaultRoute_AllowsDomainEntry(t *testing.T) {
	r := NewDefaultRoute("ir-bot", "profiles/ir-bot.yaml", []string{"*@example.com"})
	if r == nil || !r.Allows("anyone@example.com") || r.Allows("x@other.example") {
		t.Fatal("route must honour a domain entry")
	}
}
```

Config test (in the file holding the existing `SlackDefaultRoute` validation tests): `allow_emails: ["*@example.com", "kurt@example.com"]` validates; `["k*@example.com"]` fails with a message naming the entry; `NormalizedEmails()` keeps `*@example.com` lowercased.

Doctor test: scopes present, `allow_emails: ["*@example.com", "kurt@example.com"]`, lookup resolves `kurt@example.com` only ⇒ `Slack default route allowlist` is `CheckOK` and its message contains `1 domain wildcard` and `1 email(s) resolve`; a lookup failure for the exact entry still WARNs naming it, not the wildcard.

- [ ] **Step 2: Run, expect FAIL** — `go test ./pkg/slack/bridge/ -run 'EmailAllowlist|ValidateEmailEntry|AllowsDomainEntry'` and the two cmd/config tests.

- [ ] **Step 3: Implement**

`pkg/slack/bridge/default_route.go`:
```go
var reDomainEntry = regexp.MustCompile(`^\*@[a-z0-9.-]+\.[a-z]{2,}$`)
var reExactEntry = regexp.MustCompile(`^[^@\s*]+@[a-z0-9.-]+\.[a-z]{2,}$`)

// ValidateEmailEntry normalises one allowlist entry: lowercased, trimmed; either
// an exact address or exactly `*@<domain>`. Any other use of `*` is an error so
// a typo cannot widen an allowlist.
func ValidateEmailEntry(s string) (string, bool, error) {
	n := strings.ToLower(strings.TrimSpace(s))
	switch {
	case reDomainEntry.MatchString(n):
		return n, true, nil
	case reExactEntry.MatchString(n):
		return n, false, nil
	default:
		return "", false, fmt.Errorf("allowlist entry %q: must be an email address or *@<domain>", s)
	}
}

// EmailAllowlist is the one matcher for every email gate: the default route's
// list, a sandbox's own allowEmails, and km-slack dm targets.
type EmailAllowlist struct {
	exact   map[string]struct{}
	domains map[string]struct{}
}

func CompileEmailAllowlist(entries []string) (*EmailAllowlist, error) {
	al := &EmailAllowlist{exact: map[string]struct{}{}, domains: map[string]struct{}{}}
	for _, e := range entries {
		if strings.TrimSpace(e) == "" {
			continue
		}
		n, isDomain, err := ValidateEmailEntry(e)
		if err != nil {
			return nil, err
		}
		if isDomain {
			al.domains[strings.TrimPrefix(n, "*@")] = struct{}{}
		} else {
			al.exact[n] = struct{}{}
		}
	}
	return al, nil
}

func (al *EmailAllowlist) Len() int { return len(al.exact) + len(al.domains) }

// Allows: exact first, then the domain after the LAST '@'. Subdomains do not
// match (`a@sub.example.com` is not `*@example.com`). Empty ⇒ false.
func (al *EmailAllowlist) Allows(email string) bool {
	if al == nil {
		return false
	}
	e := strings.ToLower(strings.TrimSpace(email))
	if e == "" || strings.HasPrefix(e, "*@") {
		return false
	}
	if _, ok := al.exact[e]; ok {
		return true
	}
	at := strings.LastIndex(e, "@")
	if at < 1 {
		return false
	}
	_, ok := al.domains[e[at+1:]]
	return ok
}
```
`DefaultRoute` keeps its fields but holds `list *EmailAllowlist`; `NewDefaultRoute` compiles (on error: log and treat the bad entries as absent — `km init` already refuses them, so this is belt-and-braces) and `Allows` delegates. Keep `NewDefaultRoute` returning nil when alias/profile/entries are missing exactly as today.

`config.go` `SlackDefaultRoute.Validate`: run `bridge.ValidateEmailEntry` over `AllowEmails` (importing `pkg/slack/bridge` from config is a layering question — if the import graph forbids it, duplicate the two regexes in config as `validateRouteEmailEntry` with a comment naming the bridge copy and a test asserting both accept/reject the same table). `NormalizedEmails` lowercases and trims as today.

`doctor_slack.go`: split entries with `strings.HasPrefix(e, "*@")`; resolve only exact ones; OK message `"%d email(s) resolve in the workspace, %d domain wildcard(s)"`; WARN lists only unresolved exact entries.

- [ ] **Step 4: Run, expect PASS** — `go test ./pkg/slack/bridge/ ./internal/app/config/ && cd internal/app/cmd && go test -run 'DefaultRoute|Doctor' . && cd ../../.. && go build ./... && go vet ./pkg/slack/bridge/ ./internal/app/...`

- [ ] **Step 5: Commit** — `feat(slack): EmailAllowlist with *@domain entries; route, config validation and doctor use it`

---

### Task 2: Profile fields, schema, row attributes, routing info

**Files:**
- Modify: `pkg/profile/types.go` (inbound spec ~line 305–340), `pkg/profile/schemas/sandbox_profile.schema.json` (the inbound object), `pkg/profile/validate.go` (next to rule S-threadbypass ~line 405)
- Modify: `internal/app/cmd/create_slack_inbound.go` (after the `slack_allow` block ~line 200), `pkg/aws/sandbox_dynamo.go` (next to `slack_allow` at ~330 and ~579; the `SandboxMetadata` struct), `pkg/slack/bridge/aws_adapters.go` (`routingInfoFromItem`, after `slack_allow` ~line 1257), `pkg/slack/bridge/events_interfaces.go` (`SandboxRoutingInfo`)
- Test: `pkg/profile/validate_slack_allow_emails_test.go` (new, YAML → `ValidateSchema`), `pkg/aws/sandbox_dynamo_test.go` (append round-trip), `internal/app/cmd/create_slack_inbound_test.go` (append), `pkg/slack/bridge/aws_adapters_test.go` (append)

**Interfaces:**
- Produces: `SlackInboundSpec.AllowEmails []string` (`json:"allowEmails,omitempty"`), `SlackInboundSpec.ReactQuietSeconds *int` (`json:"reactQuietSeconds,omitempty"`); row attrs `slack_allow_emails` (S, comma-joined, normalised) and `slack_react_quiet_seconds` (N as string via `UpdateSandboxAttr`); `SandboxMetadata.SlackAllowEmails []string`, `SandboxMetadata.SlackReactQuietSeconds *int`; `SandboxRoutingInfo.AllowEmails []string`, `SandboxRoutingInfo.ReactQuietSeconds *int`.

- [ ] **Step 1: Failing tests**
  - Profile (through YAML, asserting the error `Path`): `allowEmails: [kurt@example.com, "*@example.com"]` valid; `allowEmails: []` ⇒ error at `spec.notification.slack.inbound.allowEmails` with message containing "omit the field"; `allowEmails: ["k*@x.com"]` ⇒ error; `reactQuietSeconds: 180` valid; `-1` ⇒ error; either set with `inbound.enabled` absent ⇒ WARN (rule like S-threadbypass).
  - `sandbox_dynamo`: a `SandboxMetadata` with both fields marshals to the two attrs and unmarshals back equal; absent attrs leave both nil (the lossy-round-trip guard).
  - `create_slack_inbound`: with the profile fields set, the fake `UpdateSandboxAttr` records `slack_allow_emails = "kurt@example.com,*@example.com"` and `slack_react_quiet_seconds = "180"`; unset ⇒ neither call.
  - `routingInfoFromItem`: item with both attrs ⇒ `AllowEmails` split and `ReactQuietSeconds` = 180; absent ⇒ nil; a non-numeric quiet value ⇒ nil with no panic.

- [ ] **Step 2: Run, expect FAIL.**

- [ ] **Step 3: Implement** following the `slack_allow` / `slack_thread_bypass` code at each site (same log shapes, same non-fatal handling). Schema: add both properties to the inbound object (`allowEmails`: array of string, minItems 1; `reactQuietSeconds`: integer, minimum 0). `validate.go`: entries validated with the same regex pair as Task 1 (duplicate in `pkg/profile` with a cross-reference comment if importing the bridge is not allowed); the WARN rule mirrors S-threadbypass.

- [ ] **Step 4: Run, expect PASS** — `go test ./pkg/profile/ ./pkg/aws/ ./pkg/slack/bridge/ && cd internal/app/cmd && go test -run 'SlackInbound|CreateSlack' . && cd ../../.. && go build ./... && scripts/validate-all-profiles.sh`

- [ ] **Step 5: Commit** — `feat(profile): notification.slack.inbound.allowEmails and reactQuietSeconds, carried on the sandbox row`

---

### Task 3: `km init` — route list from the profile; `slack.react_quiet_seconds`; bridge env + Terraform var

**Files:**
- Modify: `internal/app/cmd/init.go` (`ValidateSlackDefaultRouteProfile` ~1163, `exportSlackDefaultRouteEnv` ~1276, the react-always export block ~2104)
- Modify: `internal/app/config/config.go` (`SlackConfig.ReactQuietSeconds *int` with `mapstructure:"react_quiet_seconds"`; merge-list entry; `IsSet` unmarshal block beside `react_always`; validation `>= 0`)
- Modify: `infra/modules/lambda-slack-bridge/v1.0.0/variables.tf` (+ `slack_react_quiet_seconds`, default `"180"`), `main.tf` (env `KM_SLACK_REACT_QUIET_SECONDS = var.slack_react_quiet_seconds`), `infra/live/use1/lambda-slack-bridge/terragrunt.hcl` (`slack_react_quiet_seconds = get_env("KM_SLACK_REACT_QUIET_SECONDS", "180")`)
- Modify: `cmd/km-slack-bridge/main.go` (parse the env into `eventsHandler.ReactQuietSeconds`, default 180 on empty, log the value)
- Test: `internal/app/cmd/init_slack_default_route_test.go` (append), `internal/app/config/config_test.go` (merge-list regression guard like the one for `slack.allow`), `cmd/km-slack-bridge/main_test.go` (env parse), `pkg/terragrunt` guard that the module's env block and the live unit both name the var (mirror `TestTTLHandlerModule_EveryEnvTableHasAnIAMGrant`'s shape: read both files, assert the strings)

**Interfaces:**
- Produces: `ValidateSlackDefaultRouteProfile(route, searchPaths) (allowEmails []string, err error)` — returns the resolved profile's `AllowEmails` (nil when absent); `exportSlackDefaultRouteEnv(cfg, profileAllow []string)`: effective = `profileAllow` when non-empty, else `cfg` list; neither ⇒ returns an error the caller turns into a hard `km init` failure naming both places; prints `[info] slack.default_route allowlist: from profile <path> (N entries)` or `… from km-config.yaml (N entries)`. `EventsHandler.ReactQuietSeconds int`.

- [ ] **Step 1: Failing tests**
  - init: route profile with `allowEmails` ⇒ exported `KM_SLACK_DEFAULT_ROUTE_ALLOW` equals the profile list (normalised) even when `km-config.yaml` lists different emails; profile without ⇒ config list; neither ⇒ error mentioning `allowEmails` and `allow_emails`.
  - config: `slack.react_quiet_seconds: 0` round-trips as `*int(0)` (not nil) — the merge-list guard; absent ⇒ nil; `-5` ⇒ validation error.
  - init export: nil ⇒ `KM_SLACK_REACT_QUIET_SECONDS` left unset (terragrunt default 180 applies); `0` ⇒ exported `"0"`; env-wins WARN as for react_always.
  - bridge main: `""` ⇒ 180; `"0"` ⇒ 0; `"abc"` ⇒ 180 with a warning.
  - terragrunt guard: both files contain `slack_react_quiet_seconds` and the module env names `KM_SLACK_REACT_QUIET_SECONDS`.

- [ ] **Step 2: Run, expect FAIL.**

- [ ] **Step 3: Implement.** In `ValidateSlackDefaultRouteProfile`, after the inbound check, normalise `n.Slack.Inbound.AllowEmails` through `ValidateEmailEntry` (error on a bad entry naming the profile path) and return it. In the `km init` flow, pass it to `exportSlackDefaultRouteEnv`. The react export copies the `KM_SLACK_REACT_ALWAYS` block with `strconv.Itoa`.

- [ ] **Step 4: Run, expect PASS** — the packages above plus `go build ./...`; then `./km init --plan` is NOT required here (the controller runs the apply).

- [ ] **Step 5: Commit** — `feat(init): the route's allowlist comes from the route profile when it has one; slack.react_quiet_seconds → KM_SLACK_REACT_QUIET_SECONDS`

---

### Task 4: Bridge — per-sandbox email gate everywhere

**Files:**
- Modify: `pkg/slack/bridge/events_handler.go` (owned path after the Phase 118 id check ~line 407; continuity + route enqueue/resume in `resolveDefaultRoute` ~840–890; the strict trust predicate for routed threads where `allowed` is built, and for mapped channels where `effectiveAllow` is used ~line 527), `pkg/slack/bridge/handler_dm.go` (target check ~line 26), `cmd/km-slack-bridge/main.go` (wire `SenderEmails` for the events handler AND the Handler unconditionally; the route stays gated), `cmd/km-slack-bridge/default_route_wiring_test.go`
- Test: `pkg/slack/bridge/events_handler_allow_emails_test.go` (new), `pkg/slack/bridge/handler_dm_test.go` (append)

**Interfaces:**
- Produces: `func (h *EventsHandler) senderAllowedForSandbox(ctx, info SandboxRoutingInfo, userID string, onRoutePath bool) bool` — `info.AllowEmails` set ⇒ compile (memoise on the info via a small cache keyed by the joined string) and decide by email; else if `onRoutePath` ⇒ `h.DefaultRoute.Allows(email)`; else ⇒ true. Email via `h.SenderEmails`; nil fetcher or lookup error ⇒ false when a list applies. Log `events: sender not in sandbox allowEmails (silent drop)` at Info with sandbox and user.

- [ ] **Step 1: Failing tests** (fixtures: `routedHandler`, `newHandler`, `scriptedEmailsByUser` already exist)
  - Owned `#sb-` channel, row `AllowEmails: [kurt@example.com]`: kurt dispatched; `other@example.com` dropped silently (no 👀, no send) with the Info log; row without the list ⇒ both dispatched (today).
  - Route path: install list `*@example.com`, alias box row `AllowEmails: [kurt@…]`: kurt ⇒ enqueue; bob@example.com ⇒ dropped even though the install list admits him (override). Continuity branch and resume branch: same two cases. A row list may admit someone the install list does not (`AllowEmails: [z@other.example]` ⇒ dispatched).
  - Strict context on a routed thread with a row list: only listed authors are trusted.
  - Lookup error with a list present ⇒ drop; AND with Phase 118 ids: `Allow: [U0KURT]` + `AllowEmails: [kurt@…]`, a user `U0X` with kurt's email ⇒ dropped by the id gate.
  - `dm`: posting sandbox row `AllowEmails: [kurt@…]` ⇒ `--to <bob>` is `dm_not_allowed` even though the route admits the domain; no row list ⇒ route rule as today. Needs `Handler.SandboxByID` wired (reuse the events handler's instance in main).
  - Wiring test: with no route env, `eventsHandler.SenderEmails` and `handler.SenderEmails` are both non-nil.

- [ ] **Step 2: Run, expect FAIL.**

- [ ] **Step 3: Implement.** Keep the route's cold-create gate as `h.DefaultRoute.Allows` (the env now carries the profile list, Task 3). Insert `senderAllowedForSandbox` at the four sites; in `handler_dm.go` add `SandboxByID SandboxByIDFetcher` to `Handler`, and after the route check: `if info, err := h.SandboxByID.FetchBySandboxID(ctx, env.SenderID); err == nil && len(info.AllowEmails) > 0 { if !compiled(info.AllowEmails).Allows(email) { 403 dm_not_allowed } }` — a fetch error falls back to the route decision (log it).

- [ ] **Step 4: Run, expect PASS** — `go test ./pkg/slack/bridge/ ./cmd/km-slack-bridge/ && go build ./... && go vet ./pkg/slack/bridge/ ./cmd/km-slack-bridge/`

- [ ] **Step 5: Commit** — `feat(slack-bridge): a sandbox's allowEmails replaces the install list on every path, dm included`

---

### Task 5: Bridge — reaction quiet period

**Files:**
- Modify: `pkg/slack/bridge/events_handler.go` (`EventsHandler.ReactQuietSeconds int`; the react step ~line 689–712)
- Test: `pkg/slack/bridge/events_handler_react_quiet_test.go` (new)

- [ ] **Step 1: Failing tests** (fixtures: `fakeThreads` has `DispatchBoundary`; add a settable `boundary map[string]string` and `boundaryErr`; `h.Now` is injectable)
  - install 180, thread reply, boundary 60 s ago ⇒ no `reactions.add`, Info log `reaction skipped (quiet period)`; boundary 300 s ago ⇒ reaction; no row ⇒ reaction; boundary read error ⇒ reaction.
  - top-level message ⇒ reaction regardless of boundary.
  - `ReactAlways=false` (install or row) ⇒ no reaction on a thread reply regardless of boundary (existing rule wins).
  - row `ReactQuietSeconds=0` with install 180 ⇒ reaction on a 60 s-old thread; row `600` with install `0` ⇒ no reaction at 300 s.
  - Routed thread (continuity fixture): same window rule, using the row already fetched (assert `DispatchBoundary` called at most once per event).

- [ ] **Step 2: Run, expect FAIL.**

- [ ] **Step 3: Implement.** In the react step, after the `effectiveReactAlways` check:
```go
quiet := h.ReactQuietSeconds
if info.ReactQuietSeconds != nil {
	quiet = *info.ReactQuietSeconds
}
if quiet > 0 && msg.ThreadTS != "" && h.Threads != nil {
	if last, err := h.Threads.DispatchBoundary(ctx, msg.Channel, msg.ThreadTS); err == nil && last != "" {
		if t, perr := time.Parse(time.RFC3339, last); perr == nil && h.now().Sub(t) <= time.Duration(quiet)*time.Second {
			h.log().Info("events: reaction skipped (quiet period)", "channel", msg.Channel, "thread_ts", msg.ThreadTS, "sandbox", info.SandboxID, "since_last_dispatch_s", int(h.now().Sub(t).Seconds()), "quiet_s", quiet)
			skipReaction = true
		}
	}
}
```
(`DispatchBoundary` returns the ISO timestamp `MarkDispatched` wrote; confirm its format in `aws_adapters.go` and parse accordingly. `h.now()` is whatever helper wraps `h.Now` with a `time.Now` default.)

- [ ] **Step 4: Run, expect PASS** — `go test ./pkg/slack/bridge/ && go build ./...`

- [ ] **Step 5: Commit** — `feat(slack-bridge): reaction quiet period — thread replies react only after N seconds of silence`

---

### Task 6: Docs, CLAUDE.md, example config

**Files:** `docs/slack-notifications.md` (§ Phase 91 "Quiet period" subsection + knob table row; § Default route allowlist forms, override rule, the `km init` resolution and its recreate consequence; § Phase 118 "AND with user ids"), `docs/slack-default-route.md` (§4 decisions rows, §11), `docs/profile-reference.md` (two rows), `docs/slack-app-permissions.md` (no change — state that in the commit body), `CLAUDE.md` (one bullet under the Slack default route block + "Where to look" row), `km-config.yaml` example in `OPERATOR-GUIDE.md` or wherever `slack.react_always` is shown.

- [ ] Write; verify `go build ./...`; commit `docs(slack): reaction quiet period, *@domain allowlists, per-klanker allowEmails`.

---

## Execution order
1 → 2 → 3 → 4 → 5 → 6. Tasks 4 and 5 both edit `events_handler.go`; never in parallel.

## After the plan (controller)
`make build` → `make build-lambdas` → `km init --dry-run=false` (bridge env + zip, create-handler schema); set `ir-bot.yaml` `allowEmails`, re-run `km init` (the route env), recreate `ir-bot`; UAT per spec §4 (the "third domain member cannot start ir-bot" step first).
