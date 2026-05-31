---
phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
plan: 0
type: execute
wave: 0
depends_on: []
files_modified:
  - .planning/research/codex-config-toml.md
  - pkg/compiler/userdata_phase92_byte_identity_test.go
  - pkg/compiler/security_phase92_byte_identity_test.go
  - pkg/compiler/agent_claude_golden_test.go
  - pkg/compiler/agent_codex_golden_test.go
  - pkg/profile/inherit_notification_test.go
  - pkg/profile/validate_mixed_settings_test.go
  - pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh
  - pkg/compiler/testdata/security_iam_pre92_baseline.golden.hcl
autonomous: true
requirements: []
verifies: [VC-3, VC-4, VC-5, VC-6, VC-7]

must_haves:
  truths:
    - "Codex 0.133 + Claude Code 2.1.132 schema decisions are recorded in writing before Wave 5 plans land."
    - "Six RED test stubs exist and fail compile/run cleanly against pre-Phase-92 main."
    - "Golden baselines for learn.v2 userdata and IAM HCL are captured from pre-Phase-92 main BEFORE Wave 1 IAM rename touches security.go/service_hcl.go."
    - "Every Wave 1–5 plan can wire `verifies: VC-N` to one of the stubs created here."
  artifacts:
    - path: ".planning/research/codex-config-toml.md"
      provides: "Codex 0.133 + Claude Code 2.1.132 schema research output (already present, verify untouched)."
      contains: "permissions.deny"
    - path: "pkg/compiler/userdata_phase92_byte_identity_test.go"
      provides: "VC-3 RED stub — golden test that profiles/learn.v2.yaml userdata is byte-identical pre vs post phase."
      min_lines: 40
    - path: "pkg/compiler/security_phase92_byte_identity_test.go"
      provides: "VC-4 RED stub — golden test for aws_iam_role.max_session_duration + region_lock HCL output."
      min_lines: 40
    - path: "pkg/compiler/agent_claude_golden_test.go"
      provides: "VC-5 RED stubs — golden tests for synthesizeClaudeSettings() per learn.v2/dc34/locked/codex fixtures."
      min_lines: 60
    - path: "pkg/compiler/agent_codex_golden_test.go"
      provides: "VC-5 RED stub — golden test for synthesizeCodexConfig() (inert config + args echo)."
      min_lines: 40
    - path: "pkg/profile/inherit_notification_test.go"
      provides: "VC-7 RED stub — child-only transcript flag must inherit parent perSandbox."
      min_lines: 40
    - path: "pkg/profile/validate_mixed_settings_test.go"
      provides: "VC-6 RED stub — autoApprove + inlined configFiles → ValidationError."
      min_lines: 40
    - path: "pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh"
      provides: "Pre-Phase-92 userdata output captured BEFORE any Wave 1 changes land."
      min_lines: 1
    - path: "pkg/compiler/testdata/security_iam_pre92_baseline.golden.hcl"
      provides: "Pre-Phase-92 IAM HCL output captured BEFORE any Wave 1 changes land."
      min_lines: 1
  key_links:
    - from: "pkg/compiler/userdata_phase92_byte_identity_test.go"
      to: "pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh"
      via: "string comparison via diffStrings()"
      pattern: "diffStrings"
    - from: "pkg/compiler/security_phase92_byte_identity_test.go"
      to: "pkg/compiler/testdata/security_iam_pre92_baseline.golden.hcl"
      via: "string comparison via diffStrings()"
      pattern: "diffStrings"
    - from: "pkg/compiler/agent_claude_golden_test.go"
      to: "pkg/compiler/agent_claude.go (to be created Wave 5)"
      via: "MISSING — Wave 5 must create synthesizeClaudeSettings"
      pattern: "synthesizeClaudeSettings"
    - from: "pkg/compiler/agent_codex_golden_test.go"
      to: "pkg/compiler/agent_codex.go (to be created Wave 5)"
      via: "MISSING — Wave 5 must create synthesizeCodexConfig"
      pattern: "synthesizeCodexConfig"
---

<objective>
Capture the Phase 92 baseline contracts before any restructure code lands. Two outputs:

1. **Goldens captured from pre-Phase-92 main.** `profiles/learn.v2.yaml` userdata + IAM HCL output get serialized to `.golden.sh` / `.golden.hcl` files. These prove the post-phase pipeline is semantically transparent (PRD §Verification Criteria item 3 and 4).
2. **Six RED test stubs** that subsequent waves turn GREEN. Each stub references the post-phase API (`Spec.IAM`, `Spec.Notification`, `Spec.Agent`, `synthesizeClaudeSettings`, `synthesizeCodexConfig`) so the stubs WILL NOT COMPILE against pre-Phase-92 main — that is the desired RED state.

**Ordering note (CRITICAL):** Tasks 1 and 2 (baseline capture) MUST run against current main BEFORE Wave 1's IAM rename modifies `security.go` and `service_hcl.go`. The orchestrator must hold Wave 1 until Task 2 commits the baseline goldens.

Purpose: Establishes Nyquist verification — every downstream task points at one of these VC# stubs.
Output: 6 RED test files + 2 golden baseline files + (already-present) research document.
</objective>

<execution_context>
@/Users/khundeck/.claude/get-shit-done/workflows/execute-plan.md
@/Users/khundeck/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@.planning/ROADMAP.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-CONTEXT.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-RESEARCH.md
@.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-VALIDATION.md
@.planning/research/codex-config-toml.md
@profiles/learn.v2.yaml
@pkg/compiler/security.go
@pkg/compiler/userdata_test.go
@pkg/profile/inherit_test.go

<interfaces>
<!-- Key contracts that Wave 0 RED stubs reference. These types don't yet exist; the stubs encode the post-phase shape. -->

POST-PHASE SHAPE (used by RED stubs — Wave 1-5 implement):

From pkg/profile/types.go (post-Wave-1):
```go
// IdentitySpec → IAMSpec (rename), SessionPolicy field DELETED, AllowedSecretPaths PRESERVED
type IAMSpec struct {
    RoleSessionDuration string   `json:"roleSessionDuration" yaml:"roleSessionDuration"`
    AllowedRegions      []string `json:"allowedRegions"      yaml:"allowedRegions"`
    AllowedSecretPaths  []string `json:"allowedSecretPaths,omitempty" yaml:"allowedSecretPaths,omitempty"`
}
// Spec.Identity IdentitySpec → Spec.IAM IAMSpec
// Spec.Agent AgentSpec (dead block) → deleted from Spec
```

From pkg/profile/types.go (post-Wave-2):
```go
type NotificationSpec struct {
    Events *NotificationEventsSpec `json:"events,omitempty" yaml:"events,omitempty"`
    Email  *NotificationEmailSpec  `json:"email,omitempty"  yaml:"email,omitempty"`
    Slack  *NotificationSlackSpec  `json:"slack,omitempty"  yaml:"slack,omitempty"`
}
type NotificationSlackSpec struct {
    Enabled          *bool                              `json:"enabled,omitempty" yaml:"enabled,omitempty"`
    PerSandbox       *bool                              `json:"perSandbox,omitempty" yaml:"perSandbox,omitempty"`
    ChannelOverride  string                             `json:"channelOverride,omitempty" yaml:"channelOverride,omitempty"`
    ArchiveOnDestroy *bool                              `json:"archiveOnDestroy,omitempty" yaml:"archiveOnDestroy,omitempty"`
    Inbound          *NotificationSlackInboundSpec      `json:"inbound,omitempty" yaml:"inbound,omitempty"`
    Transcript       *NotificationSlackTranscriptSpec   `json:"transcript,omitempty" yaml:"transcript,omitempty"`
    Invites          *NotificationSlackInvitesSpec      `json:"invites,omitempty" yaml:"invites,omitempty"`
}
// Spec.Notification *NotificationSpec — added to Spec
```

From pkg/profile/types.go (post-Wave-4):
```go
type AgentSpec struct {  // REPLACES the dead AgentSpec from Wave 1
    Default string            `json:"default,omitempty" yaml:"default,omitempty"`
    Claude  *AgentClaudeSpec  `json:"claude,omitempty"  yaml:"claude,omitempty"`
    Codex   *AgentCodexSpec   `json:"codex,omitempty"   yaml:"codex,omitempty"`
}
type AgentClaudeSpec struct {
    TrustedDirectories []string         `json:"trustedDirectories,omitempty" yaml:"trustedDirectories,omitempty"`
    Tools              AgentToolsSpec   `json:"tools,omitempty" yaml:"tools,omitempty"`
    Permissions        map[string]any   `json:"permissions,omitempty" yaml:"permissions,omitempty"`
    Args               []string         `json:"args,omitempty" yaml:"args,omitempty"`
}
type AgentToolsSpec struct {
    AutoApprove []string `json:"autoApprove,omitempty" yaml:"autoApprove,omitempty"`
    Deny        []string `json:"deny,omitempty" yaml:"deny,omitempty"`
}
```

From pkg/compiler/agent_claude.go (post-Wave-5):
```go
// Creates settings.json from typed AgentClaudeSpec.
// MUST emit "permissions.allow" + "permissions.deny" (NOT legacy "autoApprove"; NOT "disallowedTools").
func synthesizeClaudeSettings(agent *profile.AgentSpec) (map[string]any, error)
```

From pkg/compiler/agent_codex.go (post-Wave-5):
```go
// Codex 0.133 has NO native tool gating — emit inert hooks + args echo + log note when tools.* populated.
func synthesizeCodexConfig(agent *profile.AgentSpec) (string, error)
```
</interfaces>
</context>

<tasks>

<task type="auto">
  <name>Task 1: Capture pre-Phase-92 userdata baseline for learn.v2.yaml</name>
  <files>
    pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh,
    pkg/compiler/userdata_phase92_byte_identity_test.go
  </files>
  <action>
**CRITICAL ORDERING: Run this BEFORE any Wave 1 file is touched. If Wave 1 has begun, abort and rebase to pre-Phase-92 main.**

Step 1 — Capture the golden file by running the existing compiler against `profiles/learn.v2.yaml` on current (pre-Phase-92) main:

  Create a small Go program (or use existing test scaffolding) to call:
    ```go
    p, err := profile.Load("profiles/learn.v2.yaml")
    cfg := compiler.DefaultConfig(...)  // use the same defaults as existing userdata_test.go baseProfile()
    got, err := compiler.GenerateUserData(p, cfg)
    os.WriteFile("pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh", got, 0644)
    ```

  Acceptable shortcut: write a `TestCapturePre92Userdata` test guarded by `if os.Getenv("CAPTURE_PRE92_BASELINE") != "1" { t.Skip() }` that writes the golden when env var is set. Run once with `CAPTURE_PRE92_BASELINE=1 go test ./pkg/compiler/ -run TestCapturePre92Userdata`, then commit the .golden.sh file.

Step 2 — Write the RED byte-identity test stub:

  Create `pkg/compiler/userdata_phase92_byte_identity_test.go`:
    ```go
    package compiler

    import (
      "os"
      "testing"

      "github.com/klankermaker/km/pkg/profile"
    )

    // TestUserdataLearnV2Phase92ByteIdentity verifies that the userdata generated
    // for profiles/learn.v2.yaml is byte-identical to the pre-Phase-92 baseline.
    // This guards the contract that Phase 92's restructure is semantically
    // transparent — same effective userdata, different YAML surface.
    //
    // VC-3
    func TestUserdataLearnV2Phase92ByteIdentity(t *testing.T) {
      golden := "testdata/userdata_learn_v2_pre92_baseline.golden.sh"
      want, err := os.ReadFile(golden)
      if err != nil {
        t.Fatalf("read golden %s: %v (Wave 0 baseline capture was not committed)", golden, err)
      }

      p, err := profile.Load("../../profiles/learn.v2.yaml")
      if err != nil {
        t.Fatalf("load profile: %v", err)
      }
      cfg := DefaultConfigForTest()  // mirrors current baseProfile() setup; reuse existing test helper or write a thin wrapper
      got, err := GenerateUserData(p, cfg)
      if err != nil {
        t.Fatalf("generate userdata: %v", err)
      }
      if string(got) != string(want) {
        t.Errorf("userdata for profiles/learn.v2.yaml drifted from pre-Phase-92 baseline:\n%s",
          diffStrings(string(want), string(got)))
      }
    }
    ```

  Notes:
  - `DefaultConfigForTest()` may not exist — if not, inline the default config inside the test or extract from existing `userdata_test.go`.
  - On pre-Phase-92 main this test will PASS (golden matches generated output). After Waves 1–5 land, the test re-runs against the new pipeline and must still pass (byte-identity contract).
  - Commit message: `test(92): add Wave 0 userdata baseline + byte-identity stub for learn.v2 (VC-3)`.
  </action>
  <verify>
    <automated>go test ./pkg/compiler/ -run TestUserdataLearnV2Phase92ByteIdentity</automated>
    Expected on pre-Phase-92 main: PASS (golden matches). After Wave 1–5: still PASS (byte-identity contract holds).
    VC-3.
  </verify>
  <done>
    .golden.sh file committed; RED stub compiles and passes on pre-Phase-92 main; one commit covers both files.
  </done>
</task>

<task type="auto">
  <name>Task 2: Capture pre-Phase-92 IAM HCL baseline + RED stub</name>
  <files>
    pkg/compiler/testdata/security_iam_pre92_baseline.golden.hcl,
    pkg/compiler/security_phase92_byte_identity_test.go
  </files>
  <action>
**CRITICAL ORDERING: Run this BEFORE Wave 1's IAM rename modifies `pkg/compiler/security.go` or `pkg/compiler/service_hcl.go`. The whole point of this golden is to prove the rename produces byte-identical Terraform output.**

Step 1 — Capture the IAM HCL baseline from pre-Phase-92 main:

  Pick a representative profile (e.g. `pkg/profile/builtins/restricted-dev.yaml` which exercises `allowedSecretPaths`). Drive `pkg/compiler/security.go` to emit the IAM role + region-lock policy HCL string and write to the golden file:
    ```go
    p, _ := profile.Load("pkg/profile/builtins/restricted-dev.yaml")
    hcl := compiler.EmitIAMSection(p)  // or whatever the public/test-package entry point is
    os.WriteFile("pkg/compiler/testdata/security_iam_pre92_baseline.golden.hcl", []byte(hcl), 0644)
    ```

  Acceptable shortcut: same skip-guarded test pattern as Task 1, env var `CAPTURE_PRE92_IAM_BASELINE=1`.

  Per RESEARCH.md §2c, the relevant security.go reads are:
    - Line 50: `p.Spec.Identity.RoleSessionDuration` → `max_session_duration` on the IAM role
    - Line 56: `p.Spec.Identity.AllowedRegions` → region-lock policy `aws_iam_role_policy.ec2spot_region_lock`
    - Line 74: `p.Spec.Identity.AllowedSecretPaths` → SSM parameter ARN allow-list

  AND service_hcl.go (lines 1032-1033) which also serializes `AllowedSecretPaths`. The captured HCL must include output from BOTH files combined, or the test must drive both functions and concat results.

Step 2 — Write the RED byte-identity test stub:

  Create `pkg/compiler/security_phase92_byte_identity_test.go`:
    ```go
    package compiler

    import (
      "os"
      "testing"

      "github.com/klankermaker/km/pkg/profile"
    )

    // TestIAMHCLPhase92ByteIdentity verifies that after Wave 1's IdentitySpec → IAMSpec
    // rename, the emitted IAM role HCL + region-lock policy HCL + service_hcl
    // SSM-allowlist HCL remains byte-identical. This is the contract that the rename
    // is purely lexical at the Go-source layer and does NOT change Terraform output.
    //
    // VC-4
    func TestIAMHCLPhase92ByteIdentity(t *testing.T) {
      golden := "testdata/security_iam_pre92_baseline.golden.hcl"
      want, err := os.ReadFile(golden)
      if err != nil {
        t.Fatalf("read golden %s: %v (Wave 0 baseline capture was not committed)", golden, err)
      }

      p, err := profile.Load("../profile/builtins/restricted-dev.yaml")
      if err != nil {
        t.Fatalf("load profile: %v", err)
      }
      got := emitCombinedIAMHCLForTest(p)  // thin wrapper that concats security.go + service_hcl.go IAM sections
      if got != string(want) {
        t.Errorf("IAM HCL output drifted from pre-Phase-92 baseline:\n%s",
          diffStrings(string(want), got))
      }
    }
    ```

  Pre-Phase-92 main: PASS. After Wave 1's rename: still PASS (Wave 1 must not change emitted text).
  Commit message: `test(92): add Wave 0 IAM HCL baseline + byte-identity stub (VC-4)`.
  </action>
  <verify>
    <automated>go test ./pkg/compiler/ -run TestIAMHCLPhase92ByteIdentity</automated>
    Expected on pre-Phase-92 main: PASS. Wave 1 must keep this GREEN.
    VC-4.
  </verify>
  <done>
    .golden.hcl committed; RED stub compiles + passes on pre-Phase-92 main; Wave 1 IAM rename CANNOT begin until this task is committed.
  </done>
</task>

<task type="auto">
  <name>Task 3: Wave 0 RED stubs for synthesizers + inheritance + mixed-mode validation</name>
  <files>
    pkg/compiler/agent_claude_golden_test.go,
    pkg/compiler/agent_codex_golden_test.go,
    pkg/profile/inherit_notification_test.go,
    pkg/profile/validate_mixed_settings_test.go
  </files>
  <action>
Create four RED test stubs that reference post-phase API (which does NOT exist yet — that is the RED state). These tests will fail to compile on pre-Phase-92 main; they MUST compile and pass on the wave that completes their dependency.

**File 1 — `pkg/compiler/agent_claude_golden_test.go` (VC-5, GREEN at Wave 5):**

  ```go
  package compiler

  import (
    "encoding/json"
    "os"
    "path/filepath"
    "testing"

    "github.com/klankermaker/km/pkg/profile"
  )

  // TestSynthesizeClaudeSettingsGolden verifies synthesizeClaudeSettings() output
  // is byte-identical to per-fixture golden files for 4 representative profiles.
  //
  // CONTRACT (per Wave 0 research / Claude Code 2.1.132 docs):
  //   - Emit "permissions.allow" (NOT legacy "autoApprove")
  //   - Emit "permissions.deny" (NOT "disallowedTools")
  //   - "trustedDirectories" is a top-level key (NOT inside permissions)
  //   - "permissions" passthrough merges agent.claude.permissions[k] into output
  //   - mergeNotifyHookIntoSettings runs AFTER synthesizer (verified in Wave 5
  //     integration tests, not here)
  //
  // VC-5
  func TestSynthesizeClaudeSettingsGolden(t *testing.T) {
    fixtures := []struct {
      name        string
      profilePath string
      goldenPath  string
    }{
      {"learn.v2",      "../../profiles/learn.v2.yaml",                  "testdata/claude_settings_learn_v2.golden.json"},
      {"dc34",          "../../profiles/dc34.yaml",                      "testdata/claude_settings_dc34.golden.json"},
      {"locked",        "../../profiles/locked.yaml",                    "testdata/claude_settings_locked.golden.json"},
      {"codex",         "../../profiles/codex.yaml",                     "testdata/claude_settings_codex.golden.json"},
    }
    for _, f := range fixtures {
      t.Run(f.name, func(t *testing.T) {
        p, err := profile.Load(f.profilePath)
        if err != nil {
          t.Fatalf("load: %v", err)
        }
        got, err := synthesizeClaudeSettings(p.Spec.Agent)
        if err != nil {
          t.Fatalf("synthesize: %v", err)
        }
        gotJSON, _ := json.MarshalIndent(got, "", "  ")
        want, err := os.ReadFile(filepath.Clean(f.goldenPath))
        if err != nil {
          t.Fatalf("read golden %s: %v (Wave 5 must produce + commit goldens)", f.goldenPath, err)
        }
        if string(gotJSON) != string(want) {
          t.Errorf("synthesizeClaudeSettings(%s) drift:\n%s",
            f.name, diffStrings(string(want), string(gotJSON)))
        }
      })
    }
  }
  ```

  Note: this WILL NOT COMPILE on pre-Phase-92 main because `synthesizeClaudeSettings` and `p.Spec.Agent` (new shape) don't exist yet. Use a build tag to skip compile until Wave 5:
  ```go
  //go:build phase92_wave5
  // +build phase92_wave5
  ```
  Wave 5's CI gate removes the build tag after creating `pkg/compiler/agent_claude.go`. Wave 5's "task done" criterion includes: `go test ./pkg/compiler/ -run TestSynthesizeClaudeSettingsGolden` is GREEN.

**File 2 — `pkg/compiler/agent_codex_golden_test.go` (VC-5, GREEN at Wave 5):**

  Same pattern as File 1 but for `synthesizeCodexConfig(agent *profile.AgentSpec) (string, error)`. One fixture (`profiles/codex.yaml`). Golden file `testdata/codex_config_codex.golden.toml`. Same build tag `phase92_wave5`. Comment block notes:
    - Codex 0.133 has NO native tool allow/deny in config.toml (per Wave 0 research).
    - Synthesizer must emit existing inert hook blocks + args echo + a documented note.
    - Test verifies the EMITTED toml is byte-identical to the golden, NOT that Codex actually honors the keys.

**File 3 — `pkg/profile/inherit_notification_test.go` (VC-7, GREEN at Wave 2):**

  ```go
  //go:build phase92_wave2
  // +build phase92_wave2

  package profile

  import (
    "testing"

    "github.com/klankermaker/km/internal/util/ptr"
  )

  // TestInheritNotificationSpec_ChildOnlyTranscriptInheritsParentPerSandbox
  // verifies the pointer-merge bug fix: a child profile that sets only one
  // notification field (e.g. notification.slack.transcript.enabled: true) must
  // inherit the parent's other notification settings (e.g. notification.slack.perSandbox: true).
  //
  // Pre-Phase-92 behavior: child's pointer-typed Spec.CLI fully replaced parent's,
  // losing all parent notify settings. Phase 92 fixes this via typed mergeNotificationSpec.
  //
  // VC-7
  func TestInheritNotificationSpec_ChildOnlyTranscriptInheritsParentPerSandbox(t *testing.T) {
    parent := &Profile{
      Spec: Spec{
        Notification: &NotificationSpec{
          Slack: &NotificationSlackSpec{
            Enabled:    ptr.Bool(true),
            PerSandbox: ptr.Bool(true),
          },
        },
      },
    }
    child := &Profile{
      Spec: Spec{
        Notification: &NotificationSpec{
          Slack: &NotificationSlackSpec{
            Transcript: &NotificationSlackTranscriptSpec{Enabled: ptr.Bool(true)},
          },
        },
      },
    }
    merged, err := ResolveInheritance(parent, child)
    if err != nil {
      t.Fatalf("resolve: %v", err)
    }
    if merged.Spec.Notification == nil || merged.Spec.Notification.Slack == nil {
      t.Fatalf("merged slack is nil — pointer merge dropped parent's slack settings")
    }
    if merged.Spec.Notification.Slack.PerSandbox == nil || !*merged.Spec.Notification.Slack.PerSandbox {
      t.Errorf("expected merged perSandbox=true (from parent), got %v",
        merged.Spec.Notification.Slack.PerSandbox)
    }
    if merged.Spec.Notification.Slack.Transcript == nil || merged.Spec.Notification.Slack.Transcript.Enabled == nil ||
        !*merged.Spec.Notification.Slack.Transcript.Enabled {
      t.Errorf("expected merged transcript.enabled=true (from child), got %v",
        merged.Spec.Notification.Slack.Transcript)
    }
  }
  ```

  Notes:
  - `internal/util/ptr` may not exist — use a local helper or `func boolPtr(b bool) *bool { return &b }`.
  - Build tag `phase92_wave2` keeps this from blocking pre-Wave-2 builds. Wave 2 removes the tag after `mergeNotificationSpec` lands.

**File 4 — `pkg/profile/validate_mixed_settings_test.go` (VC-6, GREEN at Wave 4):**

  ```go
  //go:build phase92_wave4
  // +build phase92_wave4

  package profile

  import (
    "strings"
    "testing"
  )

  // TestValidate_MixedAgentClaudeAndInlinedConfigFiles_Errors verifies the locked
  // decision that populating agent.claude.tools.autoApprove AND inlining
  // execution.configFiles[".claude/settings.json"] simultaneously is a hard
  // validation error. No merge fallback.
  //
  // VC-6
  func TestValidate_MixedAgentClaudeAndInlinedConfigFiles_Errors(t *testing.T) {
    p := &Profile{
      APIVersion: "klankermaker.example.com/v1",
      Kind:       "SandboxProfile",
      Metadata:   Metadata{Name: "mixed-mode"},
      Spec: Spec{
        Agent: &AgentSpec{
          Claude: &AgentClaudeSpec{
            Tools: AgentToolsSpec{AutoApprove: []string{"Bash", "Read"}},
          },
        },
        Execution: &ExecutionSpec{
          ConfigFiles: map[string]string{
            "/home/sandbox/.claude/settings.json": `{"autoApprove":["Bash"]}`,
          },
        },
      },
    }
    err := p.ValidateSemantic()
    if err == nil {
      t.Fatalf("expected validation error for mixed mode, got nil")
    }
    if !strings.Contains(err.Error(), "agent.claude.tools.autoApprove") ||
        !strings.Contains(err.Error(), "configFiles") {
      t.Errorf("error must reference both fields by name, got: %v", err)
    }
  }
  ```

  Build tag `phase92_wave4` — Wave 4 removes it after the validator lands.

Commit message: `test(92): add Wave 0 RED stubs for synthesizers + inherit + mixed-mode (VC-5, VC-6, VC-7)`.
  </action>
  <verify>
    <automated>go test -tags phase92_red_stubs_compile_check ./pkg/profile/... ./pkg/compiler/... 2>&1 | head -40</automated>
    Expected on pre-Phase-92 main: build tags skip the files entirely (no compile failure). Files committed but not active.
    After their respective waves: each test runs and is GREEN.
    VC-5, VC-6, VC-7.
  </verify>
  <done>
    Four RED stub files committed with build tags; `go test ./...` still passes (stubs are skipped); each downstream wave (2, 4, 5) has a known target to turn GREEN.
  </done>
</task>

</tasks>

<verification>
- `go test ./pkg/compiler/ -run TestUserdataLearnV2Phase92ByteIdentity` — PASS on pre-Phase-92 main (VC-3 baseline locked in).
- `go test ./pkg/compiler/ -run TestIAMHCLPhase92ByteIdentity` — PASS on pre-Phase-92 main (VC-4 baseline locked in).
- `go test ./...` still GREEN (no regression from new RED stubs because they're behind build tags).
- Both `.golden.sh` and `.golden.hcl` baseline files exist on disk and are committed.
- `.planning/research/codex-config-toml.md` exists (already created prior to planning; this plan verifies it is untouched).
- Wave 1 orchestrator has a clear handoff: do not begin until Task 2 commits the IAM HCL baseline.
</verification>

<success_criteria>
- All 6 RED test stub files exist in the repo.
- 2 golden baseline files captured from pre-Phase-92 main and committed.
- All stubs use build tags (`phase92_wave2`, `phase92_wave4`, `phase92_wave5`) so they do not block the pre-phase build.
- Tasks 1 and 2 produce passing tests on current main (golden + byte-identity contract is intact pre-change).
- Wave 1 may now safely begin its IAM rename.
</success_criteria>

<output>
After completion, create `.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-00-SUMMARY.md` capturing:
- Exact paths of all 6 stub files + 2 golden files (one entry per file).
- Confirmation that Task 1 + Task 2 were committed BEFORE any Wave 1 file was touched (timestamp evidence in commit log).
- Note for downstream waves: which build tag each wave must remove (Wave 2 → `phase92_wave2`; Wave 4 → `phase92_wave4`; Wave 5 → `phase92_wave5`).
</output>
