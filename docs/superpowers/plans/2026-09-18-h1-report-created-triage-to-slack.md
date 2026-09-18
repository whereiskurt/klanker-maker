# HackerOne `report_created` → silent triage → Slack — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an operator declare `events.report_created.reply: none` so the H1 bridge dispatches the operator's own triage prompt with **zero writes to the HackerOne report**, and add `h1.debug_capture` so every raw delivery is preserved in S3 before any gate.

**Architecture:** Two additive config keys flow from `km-config.yaml` → `KM_H1_PROGRAMS` / `KM_H1_DEBUG_CAPTURE` → the `km-h1-bridge` Lambda. The bridge stamps `reply_mode` onto the `H1Envelope`; the sandbox-side `km-h1-inbound-poller` reads it and (a) swaps the "post with km-h1 (REQUIRED)" preamble block for a "do NOT post" block, (b) skips the two best-effort `km-h1 comment` sites it owns. The bridge skips its own synchronous "On it" ack. A `RawCapturer` runs as step 0 of `Handle()`, before HMAC verification. Nothing new ships on the box.

**Tech Stack:** Go 1.25 (`internal/app/config`, `internal/app/cmd`, `pkg/h1/bridge`, `cmd/km-h1-bridge`, `pkg/compiler`), AWS SDK v2 (`s3`), Terraform module `infra/modules/lambda-h1-bridge/v1.0.0` + terragrunt live unit, bash (the poller heredoc inside `pkg/compiler/userdata.go`).

**Spec:** `docs/superpowers/specs/2026-09-18-h1-report-created-triage-to-slack-design.md`

## Global Constraints

- **Dormant ⇒ byte-identical.** With `reply` absent/`internal` and `debug_capture` false, every rendered artifact (envelope JSON, poller script, Terraform env block) must be identical to today. `TestUserdataH1ByteIdentity` and every existing `pkg/h1/bridge` test must stay green untouched.
- **`reply` is per-event and only affects the auto-triage path.** Comment-keyword dispatches (`report_comment_created`) never carry `reply_mode` and still ack "On it".
- **Zero values are the safe values.** `ReplyMode == ""` ≡ `internal` (today). A nil `Capture` is dormant. A capture error never changes the HTTP response.
- **Capture is step 0, before signature verification**, so a 401 is still captured.
- **Bridge always returns 200 on internal error** (existing contract — never 5xx, so HackerOne does not redeliver with a fresh GUID).
- **All Terraform edits are in place at `v1.0.0`** (additive var with a default; count-gated resource). Never a new module version dir for this.
- **Commit by pathspec** (`git add <files>` / `git commit -- <files>`) — the working tree carries unrelated uncommitted edits (`VERSION`, `profiles/base/os/redhat.yaml`, `profiles/dc35-*.yaml`); never `git add -A` or `git add .`.
- **`go test` exit codes must be read directly** — never `go test ... | tail`; tail's exit hides a FAIL.
- **Never `git stash`.**
- Commit messages end with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.

---

## File map

| File | Responsibility in this plan |
|---|---|
| `internal/app/config/config.go` | `H1EventEntry.Reply`, `H1Config.DebugCapture` (Task 1) |
| `internal/app/config/config_h1_test.go` | YAML round-trip through the real loader (Task 1) |
| `internal/app/cmd/init.go` | export `KM_H1_DEBUG_CAPTURE`; `reply` rides in `KM_H1_PROGRAMS` unchanged (Task 2) |
| `internal/app/cmd/init_h1_event_prompts_test.go` | `Reply` survives `ResolveH1EventPrompts` (Task 2) |
| `internal/app/cmd/init_test.go` | `KM_H1_DEBUG_CAPTURE` export tests (Task 2) |
| `pkg/h1/bridge/resolve.go` | `EventEntry.Reply` (Task 3) |
| `pkg/h1/bridge/payload.go` | `H1Envelope.ReplyMode`; `TopLevelKeys` helper (Tasks 3, 5) |
| `pkg/h1/bridge/webhook_handler.go` | ack skip; step-0 capture; drop-log keys (Tasks 3, 4, 5) |
| `pkg/h1/bridge/webhook_handler_test.go` | ack-skip + envelope tests (Task 3) |
| `pkg/h1/bridge/interfaces.go` | `RawCapturer` interface (Task 4) |
| `pkg/h1/bridge/webhook_handler_capture_test.go` (new) | capture ordering/fail-soft tests (Task 4) |
| `pkg/h1/bridge/capture.go` (new) | `S3RawCapturer` adapter (Task 6) |
| `pkg/h1/bridge/capture_test.go` (new) | adapter tests with a fake PutObject (Task 6) |
| `cmd/km-h1-bridge/main.go` | wire `S3RawCapturer` when `KM_H1_DEBUG_CAPTURE=true` (Task 6) |
| `infra/modules/lambda-h1-bridge/v1.0.0/{variables,main}.tf` | `debug_capture` var → env + count-gated `s3:PutObject` policy (Task 7) |
| `infra/live/use1/lambda-h1-bridge/terragrunt.hcl` | `get_env("KM_H1_DEBUG_CAPTURE", "false")` (Task 7) |
| `pkg/compiler/userdata.go` | poller: `REPLY_MODE`, `POSTING_SECTION`, gate the two `km-h1 comment` sites (Task 8) |
| `pkg/compiler/userdata_h1_reply_mode_test.go` (new) | render assertions both sides of the gate (Task 8) |
| `docs/h1-bridge.md`, `CLAUDE.md`, spec | operator docs (Task 9) |

---

### Task 1: Config keys — `events.<name>.reply` and `h1.debug_capture`

**Files:**
- Modify: `internal/app/config/config.go:407-412` (`H1EventEntry`), `:475-490` (`H1Config`)
- Test: `internal/app/config/config_h1_test.go`

**Interfaces:**
- Produces: `config.H1EventEntry.Reply string` (mapstructure/yaml/json `reply`, omitempty); `config.H1Config.DebugCapture bool` (mapstructure/yaml/json `debug_capture`, omitempty). Consumed by Tasks 2 and 3 (the json tag `reply` is what the bridge's `bridge.EventEntry` must match).

- [ ] **Step 1: Write the failing test** — append to `internal/app/config/config_h1_test.go`:

```go
// TestLoadH1_ReplyAndDebugCapture verifies the two additive keys from the
// 2026-09-18 report_created→Slack design decode through the REAL loader (not a
// struct literal — project_struct_level_tests_bypass_schema): events.<name>.reply
// and h1.debug_capture. Absent ⇒ zero values ("" / false), which are the
// pre-existing behaviour.
func TestLoadH1_ReplyAndDebugCapture(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigH1(t, dir, `
domain: example.com
region: us-east-1
h1:
  bot_handle: "@km"
  debug_capture: true
  programs:
    - handle: acme-corp
      targets:
        - {alias: h1-acme, profile: h1}
      events:
        report_created: {prompt: "run my skill for {{report_id}}", reply: none}
        report_reopened: {prompt: "re-look"}
`)
	chdirH1(t, dir)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !cfg.H1.DebugCapture {
		t.Errorf("H1.DebugCapture: got false, want true")
	}
	p := cfg.H1.Programs[0]
	if got := p.Events["report_created"].Reply; got != "none" {
		t.Errorf("Events[report_created].Reply: got %q, want %q", got, "none")
	}
	if got := p.Events["report_reopened"].Reply; got != "" {
		t.Errorf("Events[report_reopened].Reply: got %q, want \"\" (absent ⇒ internal)", got)
	}
}

// TestLoadH1_DebugCaptureAbsentIsFalse pins the dormant default.
func TestLoadH1_DebugCaptureAbsentIsFalse(t *testing.T) {
	dir := t.TempDir()
	writeKMConfigH1(t, dir, `
domain: example.com
region: us-east-1
h1:
  programs:
    - handle: acme-corp
      targets: [{alias: h1-acme}]
`)
	chdirH1(t, dir)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.H1.DebugCapture {
		t.Errorf("H1.DebugCapture: got true, want false when absent")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/app/config/ -run 'TestLoadH1_ReplyAndDebugCapture|TestLoadH1_DebugCaptureAbsentIsFalse' -v`
Expected: compile error `cfg.H1.DebugCapture undefined` / `.Reply undefined`.

- [ ] **Step 3: Add the two fields** in `internal/app/config/config.go`

Replace the `H1EventEntry` struct body:

```go
type H1EventEntry struct {
	// Prompt is the template injected as the initial agent turn when this event
	// fires. May reference report fields / {{args}} like GithubCommandEntry.Prompt.
	Prompt string `mapstructure:"prompt" yaml:"prompt" json:"prompt"`

	// Reply controls whether the bridge and the sandbox may write to the HackerOne
	// report on this auto-triage event. "" or "internal" (default) is today's
	// behaviour: the bridge posts its INTERNAL "On it" ack and the poller tells the
	// agent to post an INTERNAL reply. "none" suppresses both — nothing touches the
	// report; the operator's prompt says where output goes. Per-event on purpose:
	// comment-keyword triggers are a human asking and are never silenced.
	Reply string `mapstructure:"reply" yaml:"reply,omitempty" json:"reply,omitempty"`
}
```

Add to `H1Config` after `DefaultProfile`:

```go
	// DebugCapture, when true, makes the bridge write every raw delivery
	// ({received_at, headers, body}) to s3://<artifacts>/h1-captures/<delivery-guid>.json
	// as step 0 — before signature verification — so the real payload shape can be
	// pinned. Off by default. Exported as KM_H1_DEBUG_CAPTURE by km init.
	DebugCapture bool `mapstructure:"debug_capture" yaml:"debug_capture,omitempty" json:"debug_capture,omitempty"`
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/app/config/ -run 'TestLoadH1' -v`
Expected: all `TestLoadH1_*` PASS (the existing four plus the two new).

- [ ] **Step 5: Commit**

```bash
git add internal/app/config/config.go internal/app/config/config_h1_test.go
git commit -m "feat(config): h1 events.<name>.reply and h1.debug_capture keys

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>" -- internal/app/config/config.go internal/app/config/config_h1_test.go
```

---

### Task 2: `km init` export — `KM_H1_DEBUG_CAPTURE`; `reply` rides in `KM_H1_PROGRAMS`

**Files:**
- Modify: `internal/app/cmd/init.go` (inside `ExportTerragruntEnvVars`, directly after the `KM_H1_BOT_HANDLE` block ending near line 2215)
- Test: `internal/app/cmd/init_test.go`, `internal/app/cmd/init_h1_event_prompts_test.go`

**Interfaces:**
- Consumes: `config.H1Config.DebugCapture`, `config.H1EventEntry.Reply` (Task 1).
- Produces: env var `KM_H1_DEBUG_CAPTURE="true"` (only when the yaml is true; never set otherwise). `KM_H1_PROGRAMS` JSON now carries `"reply":"none"` inside each event entry that sets it (automatic — the json tag from Task 1 does it; the test pins it).

- [ ] **Step 1: Write the failing tests**

Append to `internal/app/cmd/init_test.go` (the file already imports `os`, `testing`, `cmd`, `config`):

```go
// ---- 2026-09-18: KM_H1_DEBUG_CAPTURE export tests ----

// TestInitExportsH1DebugCapture_True: h1.debug_capture: true ⇒ KM_H1_DEBUG_CAPTURE=true.
func TestInitExportsH1DebugCapture_True(t *testing.T) {
	t.Setenv("KM_H1_DEBUG_CAPTURE", "")
	os.Unsetenv("KM_H1_DEBUG_CAPTURE")

	cfg := &config.Config{}
	cfg.H1.DebugCapture = true

	cmd.ExportTerragruntEnvVars(cfg)

	if got := os.Getenv("KM_H1_DEBUG_CAPTURE"); got != "true" {
		t.Errorf("KM_H1_DEBUG_CAPTURE = %q, want %q", got, "true")
	}
}

// TestInitExportsH1DebugCapture_FalseIsUnset: false/absent ⇒ env var NOT set, so the
// terragrunt default "false" applies (dormant byte-identity).
func TestInitExportsH1DebugCapture_FalseIsUnset(t *testing.T) {
	t.Setenv("KM_H1_DEBUG_CAPTURE", "")
	os.Unsetenv("KM_H1_DEBUG_CAPTURE")

	cfg := &config.Config{} // DebugCapture false

	cmd.ExportTerragruntEnvVars(cfg)

	if _, ok := os.LookupEnv("KM_H1_DEBUG_CAPTURE"); ok {
		t.Errorf("KM_H1_DEBUG_CAPTURE should not be set when h1.debug_capture is false; got %q", os.Getenv("KM_H1_DEBUG_CAPTURE"))
	}
}

// TestInitExportsH1DebugCapture_DriftWarn: env already set to a different value ⇒
// env wins, not overwritten (same env-wins rule as every other KM_* export).
func TestInitExportsH1DebugCapture_DriftWarn(t *testing.T) {
	t.Setenv("KM_H1_DEBUG_CAPTURE", "false")

	cfg := &config.Config{}
	cfg.H1.DebugCapture = true

	cmd.ExportTerragruntEnvVars(cfg)

	if got := os.Getenv("KM_H1_DEBUG_CAPTURE"); got != "false" {
		t.Errorf("env-wins: KM_H1_DEBUG_CAPTURE = %q, want %q (pre-set env must not be overwritten)", got, "false")
	}
}
```

Append to `internal/app/cmd/init_h1_event_prompts_test.go` (imports already cover `os`, `filepath`, `strings`, `testing`, `cmd`, `config`; add `"encoding/json"` to the import block):

```go
// TestResolveH1EventPrompts_PreservesReply: the reply field must survive @file
// inlining AND appear in the JSON that becomes KM_H1_PROGRAMS — a dropped field
// here would make `reply: none` silently a no-op at the bridge.
func TestResolveH1EventPrompts_PreservesReply(t *testing.T) {
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "p.txt"), []byte("run my skill"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	programs := []config.H1ProgramEntry{{
		Handle: "acme",
		Events: map[string]config.H1EventEntry{
			"report_created": {Prompt: "@p.txt", Reply: "none"},
			"report_triaged": {Prompt: "inline"},
		},
	}}
	got, err := cmd.ResolveH1EventPrompts(programs, configDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0].Events["report_created"].Reply != "none" {
		t.Errorf("reply dropped by ResolveH1EventPrompts: %+v", got[0].Events["report_created"])
	}
	if got[0].Events["report_created"].Prompt != "run my skill" {
		t.Errorf("prompt not inlined: %q", got[0].Events["report_created"].Prompt)
	}

	// The JSON form (what KM_H1_PROGRAMS carries) must contain "reply":"none" for
	// report_created and NO reply key for report_triaged (omitempty ⇒ byte-identical
	// for the dormant entry).
	b, _ := json.Marshal(got)
	s := string(b)
	if !strings.Contains(s, `"reply":"none"`) {
		t.Errorf("KM_H1_PROGRAMS JSON missing reply:none: %s", s)
	}
	if strings.Count(s, `"reply"`) != 1 {
		t.Errorf("reply key must be omitted when empty (want exactly 1 occurrence): %s", s)
	}
}
```

- [ ] **Step 2: Run the tests to verify the failing ones fail**

Run: `go test ./internal/app/cmd/ -run 'TestInitExportsH1DebugCapture|TestResolveH1EventPrompts_PreservesReply' -v`
Expected: `TestResolveH1EventPrompts_PreservesReply` PASS already (Task 1's tags do the work — this test is a guard); `TestInitExportsH1DebugCapture_True` FAIL (`KM_H1_DEBUG_CAPTURE = "", want "true"`); the other two PASS trivially.

- [ ] **Step 3: Add the export block** in `internal/app/cmd/init.go`, immediately after the `if cfg.H1.BotHandle != "" { ... }` block (the last H1 block in `ExportTerragruntEnvVars`):

```go
	// 2026-09-18: KM_H1_DEBUG_CAPTURE — raw-delivery capture toggle for the H1
	// bridge. Consumed by infra/live/use1/lambda-h1-bridge/terragrunt.hcl
	// get_env("KM_H1_DEBUG_CAPTURE", "false"). Only exported when true so an absent
	// or false key leaves the env var unset (terragrunt default "false" ⇒ the bridge's
	// Capture stays nil ⇒ byte-identical to before). env-wins drift WARN as elsewhere.
	if cfg.H1.DebugCapture {
		if envVal := os.Getenv("KM_H1_DEBUG_CAPTURE"); envVal != "" && envVal != "true" {
			fmt.Fprintf(os.Stderr, "WARN: KM_H1_DEBUG_CAPTURE=%s (env) overrides km-config.yaml h1.debug_capture=true\n", envVal)
		} else if envVal == "" {
			os.Setenv("KM_H1_DEBUG_CAPTURE", "true") //nolint:errcheck
		}
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/app/cmd/ -run 'TestInitExportsH1DebugCapture|TestResolveH1EventPrompts' -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/cmd/init.go internal/app/cmd/init_test.go internal/app/cmd/init_h1_event_prompts_test.go
git commit -m "feat(init): export KM_H1_DEBUG_CAPTURE; pin reply through KM_H1_PROGRAMS

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>" -- internal/app/cmd/init.go internal/app/cmd/init_test.go internal/app/cmd/init_h1_event_prompts_test.go
```

---

### Task 3: Bridge — `reply: none` skips the ack and stamps `reply_mode` on the envelope

**Files:**
- Modify: `pkg/h1/bridge/resolve.go:22-27` (`EventEntry`), `pkg/h1/bridge/payload.go:233-262` (`H1Envelope`), `pkg/h1/bridge/webhook_handler.go` (`Handle()`: the auto-triage branch at `:244-256`, the envelope literal at `:344-354`, the ack at `:372-380`)
- Test: `pkg/h1/bridge/webhook_handler_test.go`

**Interfaces:**
- Consumes: JSON tag `reply` (must equal Task 1's tag so `KM_H1_PROGRAMS` decodes into it).
- Produces: `bridge.EventEntry.Reply string` (json `reply,omitempty`); `bridge.H1Envelope.ReplyMode string` (json `reply_mode,omitempty`); constant `bridge.ReplyModeNone = "none"`. Task 8's poller reads `.reply_mode`.

- [ ] **Step 1: Write the failing test** — append to `pkg/h1/bridge/webhook_handler_test.go`:

```go
// ============================================================
// TestHandle_AutoTriage_ReplyNone — 2026-09-18 report_created→Slack design
// ============================================================

// reply: none on an auto-triage event ⇒ NO internal "On it" ack, and the envelope
// carries reply_mode:"none" so the poller can silence the agent-side post.
func TestHandle_AutoTriage_ReplyNone_SkipsAckAndStampsEnvelope(t *testing.T) {
	events := map[string]bridge.EventEntry{
		"report_created": {Prompt: "run my skill for {{report_id}}", Reply: bridge.ReplyModeNone},
	}
	fakes := newFakes()
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, events), fakes)
	body := h1Body("km-sandbox", "300", "external-reporter", "", false)
	r := h.Handle(context.Background(), newRequest(body, "report_created", "g-none"))
	if r.StatusCode != 200 {
		t.Fatalf("status=%d; want 200", r.StatusCode)
	}
	if len(fakes.sqs.sends) != 1 {
		t.Fatalf("must still dispatch; sends=%d", len(fakes.sqs.sends))
	}
	if len(fakes.commenter.posts) != 0 {
		t.Errorf("reply:none must NOT post the internal ack; posts=%+v", fakes.commenter.posts)
	}
	var env bridge.H1Envelope
	if err := json.Unmarshal([]byte(fakes.sqs.sends[0].body), &env); err != nil {
		t.Fatalf("envelope decode: %v", err)
	}
	if env.ReplyMode != bridge.ReplyModeNone {
		t.Errorf("envelope reply_mode=%q; want %q", env.ReplyMode, bridge.ReplyModeNone)
	}
	if !strings.Contains(fakes.sqs.sends[0].body, `"reply_mode":"none"`) {
		t.Errorf("raw envelope must carry reply_mode:none: %s", fakes.sqs.sends[0].body)
	}
}

// reply absent / "internal" ⇒ byte-identical to before: ack posted, no reply_mode key.
func TestHandle_AutoTriage_ReplyDefault_AcksAndOmitsReplyMode(t *testing.T) {
	for _, reply := range []string{"", "internal"} {
		events := map[string]bridge.EventEntry{
			"report_created": {Prompt: "Triage {{report_id}}", Reply: reply},
		}
		fakes := newFakes()
		h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, events), fakes)
		body := h1Body("km-sandbox", "301", "external-reporter", "", false)
		h.Handle(context.Background(), newRequest(body, "report_created", "g-"+reply))
		if len(fakes.commenter.posts) != 1 || !fakes.commenter.posts[0].internal {
			t.Errorf("reply=%q: want exactly one INTERNAL ack; posts=%+v", reply, fakes.commenter.posts)
		}
		if strings.Contains(fakes.sqs.sends[0].body, "reply_mode") {
			t.Errorf("reply=%q: envelope must omit reply_mode (dormant byte-identity): %s", reply, fakes.sqs.sends[0].body)
		}
	}
}

// The comment-keyword path never carries reply_mode and always acks, even when the
// program's events map sets reply:none — a human typed the handle and expects a
// response.
func TestHandle_Comment_IgnoresEventReplyNone(t *testing.T) {
	events := map[string]bridge.EventEntry{
		"report_created": {Prompt: "x", Reply: bridge.ReplyModeNone},
	}
	fakes := newFakes()
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, events), fakes)
	body := h1Body("km-sandbox", "302", "alice", "@km please look", false)
	h.Handle(context.Background(), newRequest(body, "report_comment_created", "g-c"))
	if len(fakes.sqs.sends) != 1 {
		t.Fatalf("comment must dispatch; sends=%d", len(fakes.sqs.sends))
	}
	if len(fakes.commenter.posts) != 1 {
		t.Errorf("comment trigger must still ack; posts=%+v", fakes.commenter.posts)
	}
	if strings.Contains(fakes.sqs.sends[0].body, "reply_mode") {
		t.Errorf("comment envelope must not carry reply_mode: %s", fakes.sqs.sends[0].body)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/h1/bridge/ -run 'TestHandle_AutoTriage_Reply|TestHandle_Comment_IgnoresEventReplyNone' -v`
Expected: compile error `undefined: bridge.ReplyModeNone` / unknown field `Reply`.

- [ ] **Step 3: Add the fields and constant**

`pkg/h1/bridge/resolve.go` — replace the `EventEntry` struct:

```go
// EventEntry maps a HackerOne lifecycle event to the auto-triage prompt.
// An absent/empty Events map leaves a program comment-keyword-only (auto-triage
// dormant by default).
type EventEntry struct {
	Prompt string `json:"prompt"`
	// Reply is "" | "internal" | ReplyModeNone. "none" suppresses the bridge's
	// synchronous internal ack AND is carried to the poller as reply_mode so the
	// sandbox never posts to the report on this event. JSON tag matches
	// config.H1EventEntry so KM_H1_PROGRAMS decodes directly.
	Reply string `json:"reply,omitempty"`
}

// ReplyModeNone is the EventEntry.Reply / H1Envelope.ReplyMode value meaning
// "nothing — bridge or sandbox — writes to the HackerOne report for this trigger".
// Any other value (including "") is the pre-existing internal-reply behaviour.
const ReplyModeNone = "none"
```

`pkg/h1/bridge/payload.go` — add to `H1Envelope` after `ReplyToResearcher`:

```go
	// ReplyMode is set ONLY on the auto-triage path, from the matched event's
	// Reply. ReplyModeNone tells the poller to (a) tell the agent not to post to
	// HackerOne and (b) skip its own best-effort km-h1 comment sites (resume hint,
	// codex-missing notice). omitempty: absent ≡ internal (pre-existing envelopes
	// and every comment-keyword envelope decode as "").
	ReplyMode string `json:"reply_mode,omitempty"`
```

- [ ] **Step 4: Thread it through `Handle()`** in `pkg/h1/bridge/webhook_handler.go`

Where the trigger-gate variables are declared (`var promptBody string` / `var agentVerb string` / `var replyToResearcherIntent bool`), add:

```go
	var replyMode string // ReplyModeNone only on the auto-triage path
```

In the auto-triage branch, right after `entry := events[eventType]`, add:

```go
		replyMode = entry.Reply
```

In the fanout loop's `H1Envelope{...}` literal, add after `ReplyToResearcher: researcherReply && i == 0,`:

```go
			ReplyMode:         replyMode,
```

Replace the step-10 ack block with:

```go
	// ── Step 10: synchronous INTERNAL ack ────────────────────────────────────
	// Post exactly one internal "on it" comment (never researcher-visible). The ack is
	// always internal regardless of the reply gate — the gate governs the AGENT's reply
	// from the sandbox, not this synchronous acknowledgement.
	//
	// reply: none (auto-triage only) suppresses it: the operator has declared that
	// nothing on this event writes to the report until a human does.
	if dispatched && h.Commenter != nil && replyMode != ReplyModeNone {
		if cErr := h.Commenter.PostComment(ctx, payload.ReportID(), "On it — dispatched to a sandbox agent.", true); cErr != nil {
			h.log().Warn("h1-bridge: internal ack failed (non-fatal)", "err", cErr)
		}
	}
```

- [ ] **Step 5: Run the package tests**

Run: `go test ./pkg/h1/bridge/ -v 2>&1 | grep -E '^(=== RUN|--- (PASS|FAIL)|PASS|FAIL|ok)' ; go test ./pkg/h1/bridge/ >/dev/null; echo "exit=$?"`
Expected: the three new tests PASS, every pre-existing test PASS, `exit=0`.

- [ ] **Step 6: Commit**

```bash
git add pkg/h1/bridge/resolve.go pkg/h1/bridge/payload.go pkg/h1/bridge/webhook_handler.go pkg/h1/bridge/webhook_handler_test.go
git commit -m "feat(h1-bridge): reply: none skips the internal ack and stamps reply_mode on the envelope

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>" -- pkg/h1/bridge/resolve.go pkg/h1/bridge/payload.go pkg/h1/bridge/webhook_handler.go pkg/h1/bridge/webhook_handler_test.go
```

---

### Task 4: Bridge — `RawCapturer` as step 0 of `Handle()`

**Files:**
- Modify: `pkg/h1/bridge/interfaces.go` (append), `pkg/h1/bridge/webhook_handler.go` (`WebhookHandler` struct + top of `Handle()`)
- Create: `pkg/h1/bridge/webhook_handler_capture_test.go`

**Interfaces:**
- Produces: `bridge.RawCapturer` interface `Capture(ctx context.Context, deliveryGUID string, headers map[string]string, rawBody []byte) error`; field `WebhookHandler.Capture RawCapturer` (nil ⇒ dormant). Task 6 implements it for S3.

- [ ] **Step 1: Write the failing tests** — create `pkg/h1/bridge/webhook_handler_capture_test.go`:

```go
package bridge_test

// Tests for the step-0 raw capture (2026-09-18 report_created→Slack design).
// Capture must run BEFORE signature verification (so a 401 is still captured),
// must receive the verbatim headers + body, and must never change the response.

import (
	"context"
	"sync"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

type captureCall struct {
	guid    string
	headers map[string]string
	body    []byte
}

type fakeCapturer struct {
	mu    sync.Mutex
	calls []captureCall
	err   error
}

func (c *fakeCapturer) Capture(_ context.Context, guid string, headers map[string]string, body []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, captureCall{guid, headers, body})
	return c.err
}

func TestHandle_Capture_RunsBeforeVerifyAndOn401(t *testing.T) {
	fakes := newFakes()
	cap := &fakeCapturer{}
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, nil), fakes)
	h.Capture = cap

	body := h1Body("km-sandbox", "400", "alice", "@km hi", false)
	req := newRequest(body, "report_comment_created", "g-401")
	req.Headers["x-h1-signature"] = "sha256=deadbeef" // wrong ⇒ 401

	r := h.Handle(context.Background(), req)
	if r.StatusCode != 401 {
		t.Fatalf("status=%d; want 401 (bad signature)", r.StatusCode)
	}
	if len(cap.calls) != 1 {
		t.Fatalf("capture must run even when the signature is rejected; calls=%d", len(cap.calls))
	}
	got := cap.calls[0]
	if got.guid != "g-401" {
		t.Errorf("guid=%q; want g-401", got.guid)
	}
	if string(got.body) != string(body) {
		t.Errorf("captured body must be the verbatim raw body")
	}
	if got.headers["x-h1-event"] != "report_comment_created" {
		t.Errorf("captured headers must be the request headers; got %+v", got.headers)
	}
	if len(fakes.sqs.sends) != 0 {
		t.Errorf("a 401 must not dispatch")
	}
}

func TestHandle_Capture_ErrorIsSoft(t *testing.T) {
	fakes := newFakes()
	cap := &fakeCapturer{err: errString("s3 down")}
	events := map[string]bridge.EventEntry{"report_created": {Prompt: "x"}}
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, events), fakes)
	h.Capture = cap

	body := h1Body("km-sandbox", "401", "reporter", "", false)
	r := h.Handle(context.Background(), newRequest(body, "report_created", "g-soft"))
	if r.StatusCode != 200 {
		t.Fatalf("status=%d; want 200 (capture failure is non-fatal)", r.StatusCode)
	}
	if len(fakes.sqs.sends) != 1 {
		t.Errorf("capture failure must not block dispatch; sends=%d", len(fakes.sqs.sends))
	}
}

func TestHandle_Capture_NilIsDormant(t *testing.T) {
	fakes := newFakes()
	events := map[string]bridge.EventEntry{"report_created": {Prompt: "x"}}
	h := baseHandler(singleProgram("km-sandbox", []string{"alice"}, events), fakes) // Capture nil
	body := h1Body("km-sandbox", "402", "reporter", "", false)
	r := h.Handle(context.Background(), newRequest(body, "report_created", "g-nil"))
	if r.StatusCode != 200 || len(fakes.sqs.sends) != 1 {
		t.Errorf("nil Capture must be a no-op: status=%d sends=%d", r.StatusCode, len(fakes.sqs.sends))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/h1/bridge/ -run 'TestHandle_Capture' -v`
Expected: compile error `h.Capture undefined`.

- [ ] **Step 3: Add the interface** — append to `pkg/h1/bridge/interfaces.go`:

```go
// RawCapturer persists one verbatim webhook delivery for payload-shape
// diagnosis (h1.debug_capture). It is invoked as step 0 of Handle(), BEFORE
// signature verification, so a rejected or unparseable delivery is captured too.
// Implementations must be fail-soft in spirit: the handler logs a returned error
// and continues; a capture failure never changes the HTTP response.
//
// deliveryGUID is the X-H1-Delivery header ("" when absent). headers are the
// lowercase-keyed request headers as received. rawBody is the already
// base64-DECODED body. Implemented by S3RawCapturer (capture.go).
type RawCapturer interface {
	Capture(ctx context.Context, deliveryGUID string, headers map[string]string, rawBody []byte) error
}
```

- [ ] **Step 4: Add the field and the step-0 call** in `pkg/h1/bridge/webhook_handler.go`

In the `WebhookHandler` struct, after the `Logger *slog.Logger` field:

```go
	// Capture, when non-nil, persists every raw delivery before any gate
	// (h1.debug_capture). nil ⇒ dormant.
	Capture RawCapturer
```

At the very top of `Handle()`, before `// ── Step 1: verify signature`:

```go
	// ── Step 0: raw capture (debug) ──────────────────────────────────────────
	// Runs before verification on purpose: the point is to see what HackerOne
	// actually sent, including deliveries we then reject. Fail-soft.
	if h.Capture != nil {
		if cErr := h.Capture.Capture(ctx, req.Headers["x-h1-delivery"], req.Headers, req.RawBody); cErr != nil {
			h.log().Warn("h1-bridge: raw capture failed (non-fatal)", "err", cErr)
		}
	}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./pkg/h1/bridge/ -run 'TestHandle_Capture' -v; go test ./pkg/h1/bridge/ >/dev/null; echo "exit=$?"`
Expected: three PASS, `exit=0`.

- [ ] **Step 6: Commit**

```bash
git add pkg/h1/bridge/interfaces.go pkg/h1/bridge/webhook_handler.go pkg/h1/bridge/webhook_handler_capture_test.go
git commit -m "feat(h1-bridge): RawCapturer runs as step 0, before signature verification

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>" -- pkg/h1/bridge/interfaces.go pkg/h1/bridge/webhook_handler.go pkg/h1/bridge/webhook_handler_capture_test.go
```

---

### Task 5: Bridge — drop-path logs name the payload's top-level keys

**Files:**
- Modify: `pkg/h1/bridge/payload.go` (append helper), `pkg/h1/bridge/webhook_handler.go` (two log lines: `no program config, silent drop` at `:200` and `event not a trigger, dropping` at `:209`)
- Test: `pkg/h1/bridge/payload_test.go`

**Interfaces:**
- Produces: `bridge.TopLevelKeys(raw []byte) []string` — sorted keys of the root object, then `data.<key>` for each key under `data`; `nil` for non-object JSON.

- [ ] **Step 1: Write the failing test** — append to `pkg/h1/bridge/payload_test.go`:

```go
// TopLevelKeys is what the drop-path log lines carry so a wrong wrapper (the
// program handle path was never live-confirmed against a webhook) is diagnosable
// from CloudWatch alone.
func TestTopLevelKeys(t *testing.T) {
	raw := []byte(`{"data":{"report":{},"activity":{}},"meta":1}`)
	got := bridge.TopLevelKeys(raw)
	want := []string{"data", "meta", "data.activity", "data.report"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("TopLevelKeys = %v; want %v", got, want)
	}
	if bridge.TopLevelKeys([]byte(`[1,2]`)) != nil {
		t.Errorf("non-object JSON must yield nil")
	}
	if bridge.TopLevelKeys([]byte(`{"x":1}`)) == nil {
		t.Errorf("object without data must still list root keys")
	}
}
```

(`payload_test.go` is `package bridge_test` and already imports `strings` and `bridge`; if `strings` is missing, add it.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/h1/bridge/ -run TestTopLevelKeys -v`
Expected: `undefined: bridge.TopLevelKeys`.

- [ ] **Step 3: Implement** — append to `pkg/h1/bridge/payload.go` (add `"sort"` to its imports):

```go
// TopLevelKeys returns the sorted root-object keys of raw, followed by
// "data.<key>" for each key under a root "data" object. Used only on the
// drop-path log lines so an unexpected wrapper shape is visible in CloudWatch
// without debug_capture. nil when raw is not a JSON object.
func TopLevelKeys(raw []byte) []string {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil
	}
	keys := make([]string, 0, len(root))
	for k := range root {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if d, ok := root["data"]; ok {
		var data map[string]json.RawMessage
		if err := json.Unmarshal(d, &data); err == nil {
			sub := make([]string, 0, len(data))
			for k := range data {
				sub = append(sub, "data."+k)
			}
			sort.Strings(sub)
			keys = append(keys, sub...)
		}
	}
	return keys
}
```

- [ ] **Step 4: Use it on both drop lines** in `webhook_handler.go`:

```go
		h.log().Info("h1-bridge: no program config, silent drop", "program", payload.ProgramHandle(),
			"event", eventType, "top_level_keys", TopLevelKeys(req.RawBody))
```

```go
		h.log().Info("h1-bridge: event not a trigger, dropping", "event", eventType, "program", payload.ProgramHandle(),
			"top_level_keys", TopLevelKeys(req.RawBody))
```

- [ ] **Step 5: Run the tests**

Run: `go test ./pkg/h1/bridge/ >/dev/null; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 6: Commit**

```bash
git add pkg/h1/bridge/payload.go pkg/h1/bridge/payload_test.go pkg/h1/bridge/webhook_handler.go
git commit -m "feat(h1-bridge): drop-path logs carry the payload's top-level keys

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>" -- pkg/h1/bridge/payload.go pkg/h1/bridge/payload_test.go pkg/h1/bridge/webhook_handler.go
```

---

### Task 6: `S3RawCapturer` adapter + Lambda wiring

**Files:**
- Create: `pkg/h1/bridge/capture.go`, `pkg/h1/bridge/capture_test.go`
- Modify: `cmd/km-h1-bridge/main.go` (imports; env read near the other `os.Getenv` reads ~`:106`; handler construction ~`:225`; cold-start log)

**Interfaces:**
- Consumes: `bridge.RawCapturer` (Task 4).
- Produces: `bridge.S3RawCapturer{Client S3PutObjectAPI; Bucket string; Now func() time.Time}`; key `h1-captures/<guid>.json` (or `h1-captures/<unix-nanos>.json` when guid is empty); body JSON `{"received_at": RFC3339, "headers": {...}, "body": <raw JSON if valid, else string>}`. Env: `KM_H1_DEBUG_CAPTURE` (`"true"` enables), bucket from `KM_ARTIFACTS_BUCKET`.

- [ ] **Step 1: Write the failing test** — create `pkg/h1/bridge/capture_test.go`:

```go
package bridge_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/whereiskurt/klanker-maker/pkg/h1/bridge"
)

type fakeS3Put struct {
	inputs []*s3.PutObjectInput
	err    error
}

func (f *fakeS3Put) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.inputs = append(f.inputs, in)
	return &s3.PutObjectOutput{}, f.err
}

func TestS3RawCapturer_WritesGUIDKeyedRecord(t *testing.T) {
	fake := &fakeS3Put{}
	c := &bridge.S3RawCapturer{
		Client: fake,
		Bucket: "my-artifacts",
		Now:    func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) },
	}
	headers := map[string]string{"x-h1-event": "report_created", "x-h1-delivery": "abc-123"}
	body := []byte(`{"data":{"report":{"id":"1"}}}`)

	if err := c.Capture(context.Background(), "abc-123", headers, body); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(fake.inputs) != 1 {
		t.Fatalf("PutObject calls=%d; want 1", len(fake.inputs))
	}
	in := fake.inputs[0]
	if *in.Bucket != "my-artifacts" || *in.Key != "h1-captures/abc-123.json" {
		t.Errorf("bucket/key = %s/%s; want my-artifacts/h1-captures/abc-123.json", *in.Bucket, *in.Key)
	}
	if in.ContentType == nil || *in.ContentType != "application/json" {
		t.Errorf("ContentType = %v; want application/json", in.ContentType)
	}
	raw, _ := io.ReadAll(in.Body)
	var rec struct {
		ReceivedAt string            `json:"received_at"`
		Headers    map[string]string `json:"headers"`
		Body       json.RawMessage   `json:"body"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("record is not JSON: %v\n%s", err, raw)
	}
	if rec.ReceivedAt != "2026-09-18T12:00:00Z" {
		t.Errorf("received_at = %q", rec.ReceivedAt)
	}
	if rec.Headers["x-h1-event"] != "report_created" {
		t.Errorf("headers not preserved: %+v", rec.Headers)
	}
	if string(rec.Body) != string(body) {
		t.Errorf("valid JSON body must be embedded verbatim, got %s", rec.Body)
	}
}

func TestS3RawCapturer_NonJSONBodyIsStringAndMissingGUIDUsesTimestamp(t *testing.T) {
	fake := &fakeS3Put{}
	c := &bridge.S3RawCapturer{
		Client: fake,
		Bucket: "b",
		Now:    func() time.Time { return time.Unix(0, 1700000000000000000) },
	}
	if err := c.Capture(context.Background(), "", map[string]string{}, []byte("not json")); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if got := *fake.inputs[0].Key; got != "h1-captures/1700000000000000000.json" {
		t.Errorf("key = %q; want timestamp fallback", got)
	}
	raw, _ := io.ReadAll(fake.inputs[0].Body)
	if !strings.Contains(string(raw), `"body":"not json"`) {
		t.Errorf("non-JSON body must be embedded as a string: %s", raw)
	}
}

func TestS3RawCapturer_PutErrorIsReturned(t *testing.T) {
	fake := &fakeS3Put{err: errString("boom")}
	c := &bridge.S3RawCapturer{Client: fake, Bucket: "b"}
	if err := c.Capture(context.Background(), "g", nil, []byte(`{}`)); err == nil {
		t.Errorf("PutObject error must be returned (the handler logs it and continues)")
	}
}

func TestS3RawCapturer_EmptyBucketIsError(t *testing.T) {
	c := &bridge.S3RawCapturer{Client: &fakeS3Put{}, Bucket: ""}
	if err := c.Capture(context.Background(), "g", nil, []byte(`{}`)); err == nil {
		t.Errorf("empty bucket must error rather than PutObject to \"\"")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/h1/bridge/ -run TestS3RawCapturer -v`
Expected: `undefined: bridge.S3RawCapturer`.

- [ ] **Step 3: Implement** — create `pkg/h1/bridge/capture.go`:

```go
package bridge

// capture.go — S3RawCapturer, the h1.debug_capture implementation of RawCapturer.
//
// One object per delivery at s3://<artifacts>/h1-captures/<X-H1-Delivery>.json.
// The GUID is the key so a HackerOne redelivery overwrites rather than
// accumulates; a delivery with no GUID header falls back to a nanosecond
// timestamp. The record embeds the body as raw JSON when it parses and as a
// string otherwise, so a non-JSON delivery is still captured verbatim.
//
// Captures contain the full report body (vulnerability details): the prefix is as
// sensitive as captures/. Off by default; the operator turns it off once the real
// payload shape is pinned.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3PutObjectAPI is the narrow S3 surface S3RawCapturer needs. Satisfied by *s3.Client.
type S3PutObjectAPI interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// S3RawCapturer writes raw deliveries under h1-captures/ in Bucket.
type S3RawCapturer struct {
	Client S3PutObjectAPI
	Bucket string
	// Now is a test seam; nil ⇒ time.Now.
	Now func() time.Time
}

// captureRecord is the persisted shape.
type captureRecord struct {
	ReceivedAt string            `json:"received_at"`
	Headers    map[string]string `json:"headers"`
	Body       any               `json:"body"`
}

// Capture implements RawCapturer.
func (c *S3RawCapturer) Capture(ctx context.Context, deliveryGUID string, headers map[string]string, rawBody []byte) error {
	if c.Bucket == "" {
		return errors.New("h1-bridge: S3RawCapturer: empty bucket")
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	t := now().UTC()

	name := deliveryGUID
	if name == "" {
		name = strconv.FormatInt(t.UnixNano(), 10)
	}
	key := "h1-captures/" + name + ".json"

	rec := captureRecord{ReceivedAt: t.Format(time.RFC3339), Headers: headers}
	if json.Valid(rawBody) {
		rec.Body = json.RawMessage(rawBody)
	} else {
		rec.Body = string(rawBody)
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("h1-bridge: marshal capture record: %w", err)
	}

	_, err = c.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      awssdk.String(c.Bucket),
		Key:         awssdk.String(key),
		Body:        bytes.NewReader(payload), // re-readable for SDK retries
		ContentType: awssdk.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("h1-bridge: put capture %s: %w", key, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the adapter tests**

Run: `go test ./pkg/h1/bridge/ -run TestS3RawCapturer -v`
Expected: four PASS.

- [ ] **Step 5: Wire it in `cmd/km-h1-bridge/main.go`**

Add `"github.com/aws/aws-sdk-go-v2/service/s3"` to the imports (alongside the other `service/*` imports).

Add to the environment-variables doc comment at the top of the file, after the `KM_ARTIFACTS_PREFIX` line:

```go
//	KM_H1_DEBUG_CAPTURE      — "true" ⇒ every raw delivery is written to s3://$KM_ARTIFACTS_BUCKET/h1-captures/<guid>.json before verification (optional; default off)
```

After `artifactsPrefix := os.Getenv("KM_ARTIFACTS_PREFIX")`:

```go
	// ── Raw-delivery capture (h1.debug_capture) ──────────────────────────────
	debugCapture := strings.EqualFold(os.Getenv("KM_H1_DEBUG_CAPTURE"), "true")
```

In the `webhookHandler = &bridge.WebhookHandler{...}` literal, no change. Immediately after that literal (before `WireActionQuota(...)`):

```go
	// Step-0 raw capture — dormant unless KM_H1_DEBUG_CAPTURE=true. Needs the
	// artifacts bucket; without it we log once and stay dormant rather than fail
	// every delivery's capture.
	if debugCapture {
		if artifactsBucket == "" {
			slog.Warn("km-h1-bridge: KM_H1_DEBUG_CAPTURE=true but KM_ARTIFACTS_BUCKET is empty; capture disabled")
		} else {
			webhookHandler.Capture = &bridge.S3RawCapturer{
				Client: s3.NewFromConfig(cfg),
				Bucket: artifactsBucket,
			}
		}
	}
```

Add `"debug_capture", webhookHandler.Capture != nil,` to the `slog.Info("km-h1-bridge: cold start", ...)` attribute list.

- [ ] **Step 6: Build and test**

Run: `go build ./cmd/km-h1-bridge/ && go vet ./cmd/km-h1-bridge/ ./pkg/h1/bridge/ && go test ./cmd/km-h1-bridge/ ./pkg/h1/bridge/ >/dev/null; echo "exit=$?"`
Expected: builds, vets clean, `exit=0`.

- [ ] **Step 7: Commit**

```bash
git add pkg/h1/bridge/capture.go pkg/h1/bridge/capture_test.go cmd/km-h1-bridge/main.go
git commit -m "feat(h1-bridge): S3RawCapturer writes h1-captures/<guid>.json when KM_H1_DEBUG_CAPTURE=true

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>" -- pkg/h1/bridge/capture.go pkg/h1/bridge/capture_test.go cmd/km-h1-bridge/main.go
```

---

### Task 7: Terraform — `debug_capture` env + count-gated `s3:PutObject` grant

**Files:**
- Modify: `infra/modules/lambda-h1-bridge/v1.0.0/variables.tf` (append), `infra/modules/lambda-h1-bridge/v1.0.0/main.tf` (new policy after `dynamodb_action_quota`; env block ~`:352-370`), `infra/live/use1/lambda-h1-bridge/terragrunt.hcl` (`inputs`)

**Interfaces:**
- Consumes: env var `KM_H1_DEBUG_CAPTURE` from Task 2; Lambda env `KM_H1_DEBUG_CAPTURE` read by Task 6.
- Produces: module var `debug_capture` (bool, default `false`).

- [ ] **Step 1: Add the variable** — append to `variables.tf`:

```hcl
# 2026-09-18: raw-delivery capture for payload-shape diagnosis. When true the
# Lambda gets KM_H1_DEBUG_CAPTURE=true and s3:PutObject on <artifacts>/h1-captures/*.
# false (default) = env var "false" and NO IAM grant (dormant byte-identity).
variable "debug_capture" {
  description = "Write every raw HackerOne delivery to s3://<artifacts_bucket>/h1-captures/<guid>.json before signature verification. Requires artifacts_bucket."
  type        = bool
  default     = false
}
```

- [ ] **Step 2: Add the count-gated policy** — in `main.tf`, directly after the `aws_iam_role_policy.dynamodb_action_quota` resource:

```hcl
# 2026-09-18: raw-delivery capture. Gated on var.debug_capture so a dormant install
# grants nothing. Scoped to the h1-captures/ prefix only — the bridge never needs
# to read the bucket and never writes anywhere else in it.
resource "aws_iam_role_policy" "s3_debug_capture" {
  count = var.debug_capture && var.artifacts_bucket != "" ? 1 : 0
  name  = "${local.function_name}-s3-debug-capture"
  role  = aws_iam_role.h1_bridge.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "H1RawCaptureWrite"
        Effect   = "Allow"
        Action   = ["s3:PutObject"]
        Resource = "arn:aws:s3:::${var.artifacts_bucket}/h1-captures/*"
      }
    ]
  })
}
```

- [ ] **Step 3: Add the env var** — in the `environment.variables` map of `aws_lambda_function.h1_bridge`, after `KM_ARTIFACTS_PREFIX = var.artifacts_prefix`:

```hcl
      # 2026-09-18 — raw-delivery capture toggle (see aws_iam_role_policy.s3_debug_capture)
      KM_H1_DEBUG_CAPTURE = var.debug_capture ? "true" : "false"
```

- [ ] **Step 4: Wire the live unit** — in `infra/live/use1/lambda-h1-bridge/terragrunt.hcl` `inputs`, after `h1_api_base_url = get_env("KM_H1_API_BASE_URL", "")`:

```hcl
  # 2026-09-18: raw-delivery capture. km init exports KM_H1_DEBUG_CAPTURE=true only when
  # km-config.yaml h1.debug_capture is true; absent ⇒ "false" ⇒ dormant.
  debug_capture = get_env("KM_H1_DEBUG_CAPTURE", "false") == "true"
```

- [ ] **Step 5: Validate the module offline**

Run:
```bash
cd infra/modules/lambda-h1-bridge/v1.0.0 && terraform fmt -check -diff && terraform init -backend=false -input=false >/dev/null && terraform validate; echo "exit=$?"; cd -
```
Expected: `fmt` prints nothing, `validate` reports `Success! The configuration is valid.`, `exit=0`. (If `terraform init` needs providers it cannot fetch offline, run `terraform validate` after a `terraform providers lock`-free init; the module declares no `required_providers` — providers come from `root.hcl` — so `-backend=false` init is sufficient. If a stray `.terraform.lock.hcl` appears in the module dir, delete it: `find infra/modules/lambda-h1-bridge/v1.0.0 -name .terraform.lock.hcl -delete` — see memory `project_module_source_lock_drift`.)

Also confirm the env↔IAM pairing test suite is unaffected:

Run: `go test ./pkg/terragrunt/ ./pkg/hygiene/ >/dev/null; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 6: Commit**

```bash
git add infra/modules/lambda-h1-bridge/v1.0.0/variables.tf infra/modules/lambda-h1-bridge/v1.0.0/main.tf infra/live/use1/lambda-h1-bridge/terragrunt.hcl
git commit -m "infra(h1-bridge): debug_capture var → KM_H1_DEBUG_CAPTURE + gated s3:PutObject on h1-captures/

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>" -- infra/modules/lambda-h1-bridge/v1.0.0/variables.tf infra/modules/lambda-h1-bridge/v1.0.0/main.tf infra/live/use1/lambda-h1-bridge/terragrunt.hcl
```

---

### Task 8: Poller — honour `reply_mode: none`

**Files:**
- Modify: `pkg/compiler/userdata.go` — inside the `km-h1-inbound-poller` heredoc (`cat > /opt/km/bin/km-h1-inbound-poller << 'H1INBOUND'`, ~`:3059-3405`): envelope parse (~`:3178`), preamble (~`:3220-3256`), codex-missing guard (~`:3286`), resume hint (~`:3397`)
- Create: `pkg/compiler/userdata_h1_reply_mode_test.go`

**Interfaces:**
- Consumes: envelope field `reply_mode` (Task 3), value `none`.
- Produces: the rendered bash. The template is a Go raw string containing a **single-quoted heredoc** (`'H1INBOUND'`) — `$VAR` inside is bash at runtime, never expanded at render time. Do not introduce `{{`/`}}` inside the heredoc (they are Go-template delimiters in this file).

- [ ] **Step 1: Write the failing test** — create `pkg/compiler/userdata_h1_reply_mode_test.go`:

```go
package compiler

// reply_mode: none — the poller must (a) parse the field, (b) swap the "post with
// km-h1 (REQUIRED)" preamble block for the do-NOT-post block, and (c) gate its own
// two best-effort km-h1 comment sites (codex-missing notice, Phase 106 resume hint).
// The DEFAULT rendering must keep every pre-existing line so a reply_mode-less
// envelope behaves exactly as before.
//
// These assert on executable bash, not comments (memory:
// project_userdata_template_invisible_to_go_build).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

func renderH1EnabledPoller(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller unavailable")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "testdata", h1BaselineProfile))
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	p, err := profile.Parse(raw)
	if err != nil {
		t.Fatalf("parse profile: %v", err)
	}
	enabled := true
	if p.Spec.Notification == nil {
		p.Spec.Notification = &profile.NotificationSpec{}
	}
	p.Spec.Notification.H1 = &profile.NotificationH1Spec{
		Inbound: &profile.NotificationH1InboundSpec{Enabled: &enabled},
	}
	got, err := generateUserData(p, "sb-h1-replymode", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("generateUserData: %v", err)
	}
	start := strings.Index(got, "cat > /opt/km/bin/km-h1-inbound-poller << 'H1INBOUND'")
	end := strings.Index(got, "\nH1INBOUND\n")
	if start < 0 || end < 0 || end < start {
		t.Fatalf("could not isolate the km-h1-inbound-poller heredoc")
	}
	return got[start:end]
}

func TestH1Poller_ReplyModeNone_IsHonoured(t *testing.T) {
	poller := renderH1EnabledPoller(t)

	mustContain := []string{
		// (a) parsed from the envelope
		`REPLY_MODE=$(echo "$BODY" | jq -r '.reply_mode // empty' 2>/dev/null || true)`,
		// (b) the swap is a real bash branch on the value
		`if [ "$REPLY_MODE" = "none" ]; then`,
		`Do NOT post anything to HackerOne for this trigger (no km-h1 comment).`,
		`A human will decide whether it reaches HackerOne.`,
		// the default branch keeps the original REQUIRED text verbatim
		`--- Posting your response (REQUIRED) ---`,
		`Do NOT only print your answer — it is discarded unless you post it with km-h1.`,
		// (c) both poller-owned comment sites are gated on the same value
		`[ "$REPLY_MODE" != "none" ]`,
	}
	for _, s := range mustContain {
		if !strings.Contains(poller, s) {
			t.Errorf("poller missing %q", s)
		}
	}

	// Every km-h1 comment invocation the poller itself makes must sit inside a
	// reply_mode gate. Count the sites and the gates.
	sites := strings.Count(poller, `/opt/km/bin/km-h1 comment --report "$REPORT_ID"`)
	gates := strings.Count(poller, `[ "$REPLY_MODE" != "none" ]`)
	if sites != 2 {
		t.Fatalf("expected exactly 2 poller-owned km-h1 comment sites (codex-missing, resume hint); found %d — add a gate for the new one and update this test", sites)
	}
	if gates < sites {
		t.Errorf("poller-owned km-h1 comment sites=%d but reply_mode gates=%d; every site must be gated", sites, gates)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/compiler/ -run TestH1Poller_ReplyModeNone_IsHonoured -v`
Expected: FAIL — `poller missing "REPLY_MODE=..."` and the others; the site count assertion reports `found 2` (both sites exist today, ungated).

- [ ] **Step 3: Parse the field** — in `pkg/compiler/userdata.go`, directly after the line

```bash
  REPLY_TO_RESEARCHER=$(echo "$BODY" | jq -r '.reply_to_researcher // false' 2>/dev/null || echo false)
```

add:

```bash
  # reply_mode: "none" ⇒ nothing (poller or agent) writes to the HackerOne report
  # for this trigger. Set by the bridge only on auto-triage events whose config
  # says reply: none. Empty/absent ⇒ pre-existing internal-reply behaviour.
  REPLY_MODE=$(echo "$BODY" | jq -r '.reply_mode // empty' 2>/dev/null || true)
```

- [ ] **Step 4: Swap the posting section** — replace the block from `PREAMBLE="[HackerOne Comment Trigger]` through the closing `Do NOT only print your answer — it is discarded unless you post it with km-h1."` (inclusive of the closing quote) with:

```bash
  # The posting section depends on reply_mode. The DEFAULT text below is verbatim
  # what shipped before reply_mode existed — do not reflow it.
  if [ "$REPLY_MODE" = "none" ]; then
    POSTING_SECTION="--- Posting your response ---
Do NOT post anything to HackerOne for this trigger (no km-h1 comment). Deliver your
output where the task below says. A human will decide whether it reaches HackerOne."
  else
    POSTING_SECTION="--- Posting your response (REQUIRED) ---
Your reply reaches HackerOne ONLY if you post it with km-h1. Replies are INTERNAL
(team-only) by DEFAULT — this is the safety default; never message an external
researcher unless explicitly authorized for THIS trigger.

$REPLY_GUIDANCE

Do NOT only print your answer — it is discarded unless you post it with km-h1."
  fi

  PREAMBLE="[HackerOne Comment Trigger]
Program: $PROGRAM
Report: $REPORT_ID
Event: $KIND
Actor: $ACTOR
URL: $REPORT_URL

--- Reading the report ---
Fetch the full report JSON (title, state, severity, the researcher's writeup):

  km-h1 read --report $REPORT_ID

--- Trigger context ---
$COMMENT_BODY

$POSTING_SECTION"
```

- [ ] **Step 5: Gate the codex-missing notice** — replace

```bash
  if [ "$EFFECTIVE_AGENT" = "codex" ] && ! command -v codex >/dev/null 2>&1; then
    /opt/km/bin/km-h1 comment --report "$REPORT_ID" \
      --body "This sandbox's profile has no Codex; /codex is unavailable here."
```

with

```bash
  if [ "$EFFECTIVE_AGENT" = "codex" ] && ! command -v codex >/dev/null 2>&1; then
    echo "[km-h1-inbound-poller] WARN: /codex requested but codex is not installed — report=$REPORT_ID"
    if [ "$REPLY_MODE" != "none" ]; then
      /opt/km/bin/km-h1 comment --report "$REPORT_ID" \
        --body "This sandbox's profile has no Codex; /codex is unavailable here."
    fi
```

- [ ] **Step 6: Gate the Phase 106 resume hint** — replace

```bash
      if [ -n "$NEW_H1_SESSION" ] && [ "$NEW_H1_SESSION" != "${H1_SESSION:-}" ]; then
```

with

```bash
      # reply_mode:none ⇒ no hint either — it is a comment on the report.
      if [ -n "$NEW_H1_SESSION" ] && [ "$NEW_H1_SESSION" != "${H1_SESSION:-}" ] && [ "$REPLY_MODE" != "none" ]; then
```

- [ ] **Step 7: Run the compiler tests — new test, the byte-identity golden, and the enabled-render test**

Run: `go test ./pkg/compiler/ -run 'TestH1Poller_ReplyModeNone_IsHonoured|TestUserdataH1ByteIdentity|TestUserdataH1EnabledRendersPoller|TestUserData_H1PollerDispatchJoinsCgroupViaRunuser' -v 2>&1 | grep -E '^(--- |ok|FAIL)'; go test ./pkg/compiler/ >/dev/null; echo "exit=$?"`
Expected: all four PASS (`TestUserdataH1ByteIdentity` is the H1-FREE golden and must be untouched — if it fails, the edit leaked outside the `{{- if .H1InboundEnabled }}` block); full package `exit=0`.

- [ ] **Step 8: Syntax-check the heredoc body** (the template is a Go raw string — a bash syntax error compiles fine; memory `project_userdata_template_invisible_to_go_build`):

```bash
SCRATCH=/private/tmp/claude-501/-Users-khundeck-working-klankrmkr/c2872bd1-f340-4ab7-a624-bb7f81bb1d3c/scratchpad
awk '/cat > \/opt\/km\/bin\/km-h1-inbound-poller << .H1INBOUND./{f=1;next} /^H1INBOUND$/{f=0} f' pkg/compiler/userdata.go \
  | sed 's/{{[^}]*}}/X/g' > "$SCRATCH/km-h1-inbound-poller.sh"
bash -n "$SCRATCH/km-h1-inbound-poller.sh"; echo "bash -n exit=$?"
```
Expected: `bash -n exit=0`. (The `sed` neutralises the Go-template tokens like `{{ .SsmPrefix }}` that live inside the heredoc.)

- [ ] **Step 9: Commit**

```bash
git add pkg/compiler/userdata.go pkg/compiler/userdata_h1_reply_mode_test.go
git commit -m "feat(userdata): km-h1-inbound-poller honours reply_mode none — no km-h1 comment, do-not-post preamble

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>" -- pkg/compiler/userdata.go pkg/compiler/userdata_h1_reply_mode_test.go
```

---

### Task 9: Docs — operator runbook, spec correction, CLAUDE.md pointer

**Files:**
- Modify: `docs/h1-bridge.md` (§ "Config surface — `h1.programs`" ~`:125-190`, § "Trigger models" ~`:192-204`, § "Deploy sequence" ~`:312`, § "Troubleshooting" ~`:407`), `docs/superpowers/specs/2026-09-18-h1-report-created-triage-to-slack-design.md` (§3 item 1), `CLAUDE.md` ("Where to look" table)

- [ ] **Step 1: Correct the spec** — in §3 item 1 of the spec, replace "Two sites push onto the H1 report" with "Three sites push onto the H1 report" and add a third bullet:

```markdown
   - The poller's Phase 106 resume hint (`userdata.go:~3397`) posts a `<details>🔧 Resume…</details>`
     internal comment on the first turn of every report; the codex-missing guard (`:~3286`)
     posts too. Both are poller-owned and must gate on `reply_mode`.
```

- [ ] **Step 2: Update `docs/h1-bridge.md`**

In the config example under "Config surface — `h1.programs`", change the `events:` entry to:

```yaml
          events:
            report_created:
                prompt: '@profiles/prompts/h1.report_created.prompt.txt'
                # reply: none | internal (default). none ⇒ NOTHING writes to the report on
                # this event: no bridge "On it" ack, no agent km-h1 comment, no resume hint.
                # Use it when a human blesses the triage later via "@km /triage".
                reply: none
```

Add to the field table:

```markdown
| `programs[].events.<event>.reply` | `internal` (default) or `none`. Per-event. `none` silences every write to the report on that auto-triage event; comment-keyword triggers are never silenced |
| `debug_capture` | install-wide bool, default `false`. `true` ⇒ every raw delivery `{received_at, headers, body}` is written to `s3://<artifacts>/h1-captures/<X-H1-Delivery>.json` **before** signature verification. Turn on for the first live deliveries to pin the real payload shape; contains the full report body — treat as sensitive |
```

Add a new subsection after "Trigger models":

```markdown
### Silent auto-triage (`reply: none`)

Auto-triage events default to the Phase 103 behaviour: the bridge posts an INTERNAL
"On it" ack, the poller tells the agent to post an INTERNAL reply with `km-h1 comment`,
and the first turn adds a resume-hint comment. Set `reply: none` on an event to make
that path **write-free** toward HackerOne — useful when the agent's output goes to
Slack (or anywhere else your prompt says) and an analyst decides whether it ever
reaches the report by typing `@km /triage` as an internal comment (the ordinary
comment-keyword flow, which still acks and still posts).

What changes under `reply: none`:

| Site | default | `reply: none` |
|---|---|---|
| bridge "On it — dispatched…" internal ack | posted | skipped |
| poller preamble | "Posting your response (REQUIRED)… post it with km-h1" | "Do NOT post anything to HackerOne for this trigger… A human will decide" |
| poller resume hint (`<details>🔧 Resume…`) | posted on session mint | skipped |
| poller codex-missing notice | posted | logged only |

Your prompt file must therefore say **where** the output goes — the preamble no longer
does. `{{report_id}} {{title}} {{state}} {{program}}` are still expanded, and
`km-h1 read --report N` still works (it is a read).

The envelope carries `reply_mode:"none"`; an older sandbox (pre-recreate) ignores the
field and behaves as before — `reply: none` on the bridge alone still removes the ack.

### Capturing the real payload (`debug_capture`)

The Phase 103 parser was pinned against a synthetic payload (`103-CAPTURE/field-paths.md`).
With `h1.debug_capture: true`, `km init --h1` gives the bridge `s3:PutObject` on
`h1-captures/*` and it writes every delivery there as step 0 — before HMAC, so a
mis-pasted secret still yields a capture. Inspect with
`aws s3 ls s3://<artifacts>/h1-captures/` then `aws s3 cp … -`. The drop-path log lines
(`no program config, silent drop`, `event not a trigger, dropping`) also now carry
`top_level_keys`, so a wrong wrapper is visible in CloudWatch without capture. Turn it off
again once the shape is confirmed.
```

In "Deploy sequence", add a line:

```markdown
- `reply:` / `debug_capture:` edits: `make build` + `km init --h1 --dry-run=false` refreshes the
  bridge env + IAM. The poller half of `reply: none` (preamble, resume hint) rides in the
  create-handler userdata: `make build-lambdas` + `km init --dry-run=false`, then
  `km destroy && km create` the `h1-<handle>` sandbox.
```

In "Troubleshooting", add rows:

```markdown
| `reply: none` set but an "On it" comment still appears | bridge env not refreshed — `km init --h1 --dry-run=false`; confirm with `aws lambda get-function-configuration --function-name <prefix>-h1-bridge --query 'Environment.Variables.KM_H1_PROGRAMS'` contains `"reply":"none"` |
| `reply: none` set, no ack, but the agent still posted / a resume hint appeared | sandbox predates the poller change — `km destroy && km create` |
| Every event drops with `program=""` | routing key path not present in the real payload — enable `debug_capture`, read one object under `h1-captures/`, compare against `pkg/h1/bridge/payload.go` struct tags |
```

- [ ] **Step 3: CLAUDE.md pointer** — in the "Where to look" table, after the "HackerOne comment-trigger bridge" row, add:

```markdown
| Silent auto-triage — `events.<event>.reply: none` (no ack, no agent post, no resume hint), `h1.debug_capture` raw-delivery capture to `h1-captures/`, why the analyst blesses via `@km /triage` | `docs/h1-bridge.md` § Silent auto-triage + § Capturing the real payload; spec `docs/superpowers/specs/2026-09-18-h1-report-created-triage-to-slack-design.md`; diagram `docs/diagrams/h1-triage/` |
```

- [ ] **Step 4: Full-suite check**

Run: `go build ./... && go test ./internal/app/config/ ./internal/app/cmd/ ./pkg/h1/... ./cmd/km-h1-bridge/ ./pkg/compiler/ ./pkg/hygiene/ >/dev/null; echo "exit=$?"`
Expected: `exit=0`. (`internal/app/cmd` has five known deterministic pre-existing failures in `TestBootstrap*`/`TestCluster*` — see memory `project_cmd_suite_pre_existing_failures`; a non-zero exit there is acceptable ONLY if the failing tests are exactly those five. Confirm with `go test ./internal/app/cmd/ 2>&1 | grep -- '--- FAIL'`.)

- [ ] **Step 5: Commit**

```bash
git add docs/h1-bridge.md docs/superpowers/specs/2026-09-18-h1-report-created-triage-to-slack-design.md CLAUDE.md
git commit -m "docs(h1): reply: none, debug_capture, and the three write sites

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>" -- docs/h1-bridge.md docs/superpowers/specs/2026-09-18-h1-report-created-triage-to-slack-design.md CLAUDE.md
```

---

## Deploy (operator, after merge)

1. `make build` → `make build-lambdas` → `km init --dry-run=false` (**not** `--sidecars`: bridge env + IAM, and the poller rides in the create-handler zip).
2. `km destroy <h1-sandbox> --remote --yes && km create profiles/h1.yaml h1-<handle>`.
3. In `km-config.yaml` on the target install: `h1.debug_capture: true`, `events.report_created: {prompt: '@<your file>', reply: none}`.
4. First live `report_created`: `aws s3 ls s3://<artifacts>/h1-captures/`; confirm the object's `body.data.report.relationships.program.data.attributes.handle` exists. If it does not, that is the Phase 103 wrapper risk realised — fix `payload.go` before anything else.
5. Confirm on HackerOne: no "On it", no resume hint. Confirm in Slack: your skill's output. Then `@km /triage` as an internal comment and confirm the internal post.
6. Set `debug_capture: false` and `km init --h1 --dry-run=false`.
