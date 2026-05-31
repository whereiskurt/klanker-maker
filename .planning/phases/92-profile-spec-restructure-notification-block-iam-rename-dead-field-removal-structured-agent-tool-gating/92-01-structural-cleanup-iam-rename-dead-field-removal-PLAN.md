---
phase: 92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating
plan: 1
type: execute
wave: 1
depends_on: []
files_modified:
  - pkg/profile/types.go
  - pkg/profile/schemas/sandbox_profile.schema.json
  - pkg/profile/inherit.go
  - pkg/profile/validate.go
  - pkg/profile/aws_validate.go
  - pkg/profile/types_test.go
  - pkg/compiler/security.go
  - pkg/compiler/service_hcl.go
  - pkg/allowlistgen/generator.go
  - pkg/compiler/testdata/ec2-basic.yaml
  - pkg/compiler/testdata/ec2-empty-repos.yaml
  - pkg/compiler/testdata/ec2-with-allowed-refs.yaml
  - pkg/compiler/testdata/ec2-with-secrets.yaml
  - pkg/compiler/testdata/ec2-with-budget.yaml
  - pkg/compiler/testdata/ecs-basic.yaml
  - pkg/compiler/testdata/ecs-empty-repos.yaml
  - pkg/compiler/testdata/ecs-with-github.yaml
  - pkg/compiler/testdata/docker-basic.yaml
  - pkg/compiler/testdata/docker-with-budget.yaml
  - profiles/ao.yaml
  - profiles/codex.yaml
  - profiles/dc34.yaml
  - profiles/dc34.ami.yaml
  - profiles/example-additional-snapshots.yaml
  - profiles/goose.yaml
  - profiles/learn.v2.yaml
  - profiles/learn.v2.chatty.yaml
  - profiles/learn.v2.codex.yaml
  - profiles/learn.v2.polite.yaml
  - profiles/locked.yaml
  - profiles/locked.ami.yaml
  - pkg/profile/builtins/ao.yaml
  - pkg/profile/builtins/codex.yaml
  - pkg/profile/builtins/goose.yaml
  - pkg/profile/builtins/hardened.yaml
  - pkg/profile/builtins/learn.yaml
  - pkg/profile/builtins/open-dev.yaml
  - pkg/profile/builtins/restricted-dev.yaml
  - pkg/profile/builtins/sealed.yaml
  - scripts/validate-all-profiles.sh
  - docs/sandbox-secrets.md
  - CLAUDE.md
  - OPERATOR-GUIDE.md
autonomous: true
requirements: []
verifies: [VC-1, VC-2, VC-4, VC-11]

must_haves:
  truths:
    - "`spec.identity:` is renamed to `spec.iam:` in types, schema, validators, compiler, and ALL 20 profile YAMLs + 10 testdata YAMLs."
    - "`identity.sessionPolicy` is removed from types, schema (`properties` + `required`), validators, and every YAML that used it."
    - "`identity.allowedSecretPaths` (Phase 89 SOPS drift) is now declared in the JSON schema under `iam`."
    - "The dead `spec.agent:` block (`MaxConcurrentTasks`, `TaskTimeout`, `AllowedTools`) is removed from types, schema (incl. `spec.required`), inherit.go, all 20 profile YAMLs + 10 testdata YAMLs + `pkg/allowlistgen/generator.go`."
    - "`scripts/validate-all-profiles.sh` exists, iterates the 20-file Profile Inventory, exits non-zero on any failure."
    - "The Wave 0 IAM byte-identity golden test stays GREEN — Wave 1 changes are purely lexical and emit identical Terraform HCL."
    - "`go test ./...` compiles and passes — no dangling references to `Spec.Identity` or dead `AgentSpec` fields."
  artifacts:
    - path: "pkg/profile/types.go"
      provides: "IdentitySpec renamed to IAMSpec; SessionPolicy field deleted; dead AgentSpec + Spec.Agent field deleted."
      contains: "type IAMSpec struct"
    - path: "pkg/profile/schemas/sandbox_profile.schema.json"
      provides: "`iam` block (with `allowedSecretPaths`); `sessionPolicy` removed from properties + required; `agent` block + `agent` in spec.required removed."
      contains: "\"iam\":"
    - path: "pkg/profile/inherit.go"
      provides: "Line 101 references &result.Spec.IAM (renamed); line 104 agent merge call deleted."
      contains: "result.Spec.IAM"
    - path: "pkg/compiler/security.go"
      provides: "3 sites updated from p.Spec.Identity → p.Spec.IAM."
      contains: "p.Spec.IAM"
    - path: "pkg/compiler/service_hcl.go"
      provides: "2 sites updated from p.Spec.Identity → p.Spec.IAM."
      contains: "p.Spec.IAM"
    - path: "pkg/allowlistgen/generator.go"
      provides: "Dead AgentSpec{MaxConcurrentTasks, TaskTimeout} construction removed."
      pattern: "^(?!.*MaxConcurrentTasks).*$"
    - path: "scripts/validate-all-profiles.sh"
      provides: "Bash script iterating 20-file Profile Inventory; exits non-zero on any km validate failure."
      min_lines: 25
  key_links:
    - from: "pkg/profile/types.go"
      to: "pkg/compiler/security.go"
      via: "Spec.IAM field reference (renamed from Spec.Identity)"
      pattern: "Spec\\.IAM"
    - from: "pkg/profile/types.go"
      to: "pkg/profile/schemas/sandbox_profile.schema.json"
      via: "JSON schema iam block matches IAMSpec struct"
      pattern: "\"iam\""
    - from: "pkg/profile/inherit.go"
      to: "pkg/profile/types.go"
      via: "mergeSpecSection takes &result.Spec.IAM (rename only — no new typed merger here)"
      pattern: "Spec\\.IAM"
    - from: "scripts/validate-all-profiles.sh"
      to: "km binary (built locally)"
      via: "calls `./km validate <profile>` per file in inventory loop"
      pattern: "km validate"
---

<objective>
Land the "structural cleanup" wave: IAM rename, dead-field deletion, schema drift fix, and the 20-profile validation script. All purely lexical / structural — no behavior changes.

**Wave 1 IS DEPENDENT ON Wave 0 Task 2 being committed.** The IAM byte-identity golden (`pkg/compiler/testdata/security_iam_pre92_baseline.golden.hcl`) MUST exist on disk before this wave begins so this wave's verification can prove the rename didn't change Terraform output.

This wave does NOT touch:
- `spec.cli.notify*` (Wave 2/3 own the notification block)
- `spec.cli.{agent,claudeArgs,codexArgs,vscodeEnabled}` (Waves 2/4 own these)
- `pkg/compiler/userdata.go` (Wave 3 owns notification emission)
- The new `agent:` block (Wave 4 introduces it)

Wave 1 IS the wave that re-deletes the dead `agent:` block — Wave 4 later re-introduces an `agent:` block with brand-new structured semantics. Wave 1's deletion is "drop the old, leave the slot empty"; Wave 4's addition is "fill the slot with new shape."

Purpose: Move IAM concerns out of the `identity:` namespace into the correct `iam:` namespace; close the Phase 89 schema drift; remove fields the codebase already ignores.
Output: 5 Go source changes + 1 schema change + 10 testdata YAMLs + 20 profile YAMLs + 1 new script + doc updates.
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
@pkg/profile/types.go
@pkg/profile/inherit.go
@pkg/profile/schemas/sandbox_profile.schema.json
@pkg/compiler/security.go
@pkg/compiler/service_hcl.go
@pkg/allowlistgen/generator.go
@pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh
@pkg/compiler/testdata/security_iam_pre92_baseline.golden.hcl

<interfaces>
<!-- Wave 1 PRE-STATE (current main, confirmed per RESEARCH.md §2a, §2c): -->

```go
// pkg/profile/types.go (CURRENT, pre-Wave-1)
type IdentitySpec struct {   // line 277
    RoleSessionDuration string   `yaml:"roleSessionDuration"`
    AllowedRegions      []string `yaml:"allowedRegions"`
    SessionPolicy       string   `yaml:"sessionPolicy"`        // DEAD — never read by anything
    AllowedSecretPaths  []string `yaml:"allowedSecretPaths,omitempty"`  // Phase 89 — read by compiler, missing from schema
}
type AgentSpec struct {       // line 372 — entire block dead
    MaxConcurrentTasks int    `yaml:"maxConcurrentTasks"`
    TaskTimeout        string `yaml:"taskTimeout"`
    AllowedTools       []string `yaml:"allowedTools"`
}
type Spec struct {
    ...
    Identity IdentitySpec  // line 35
    Agent    AgentSpec     // line 38
    ...
}
```

```go
// pkg/profile/inherit.go (CURRENT)
// line 101: mergeSpecSection(&result.Spec.Identity, &parent.Spec.Identity, &child.Spec.Identity)
// line 104: mergeSpecSection(&result.Spec.Agent,    &parent.Spec.Agent,    &child.Spec.Agent)
```

```go
// pkg/compiler/security.go (CURRENT) — RESEARCH.md §2c
// line 50: p.Spec.Identity.RoleSessionDuration
// line 56: p.Spec.Identity.AllowedRegions
// line 74: p.Spec.Identity.AllowedSecretPaths
```

```go
// pkg/compiler/service_hcl.go (CURRENT) — RESEARCH.md §2c
// line 1032: len(p.Spec.Identity.AllowedSecretPaths) > 0
// line 1033: strings.Join(p.Spec.Identity.AllowedSecretPaths, ",")
```

```go
// pkg/allowlistgen/generator.go (CURRENT) — RESEARCH.md §2j
// line 96-99: AgentSpec{MaxConcurrentTasks: 1, TaskTimeout: "30m"}
```

```json
// pkg/profile/schemas/sandbox_profile.schema.json (CURRENT)
// line 399: "identity" block
// line 401: required: ["roleSessionDuration", "allowedRegions", "sessionPolicy"]
// line 488: "agent" block
// line 490: required: ["maxConcurrentTasks", "taskTimeout"]
// line 56: spec.required has "identity"
// line 59: spec.required has "agent"
// allowedSecretPaths: NOT IN SCHEMA (drift)
```

<!-- Wave 1 POST-STATE (target): -->

```go
// pkg/profile/types.go (TARGET)
type IAMSpec struct {
    RoleSessionDuration string   `yaml:"roleSessionDuration"`
    AllowedRegions      []string `yaml:"allowedRegions"`
    AllowedSecretPaths  []string `yaml:"allowedSecretPaths,omitempty"`
    // SessionPolicy: DELETED
}
// AgentSpec: DELETED (Wave 4 reintroduces with new shape)
type Spec struct {
    ...
    IAM IAMSpec  // renamed
    // Agent: deleted; Wave 4 reintroduces as *AgentSpec pointer
    ...
}
```

```json
// pkg/profile/schemas/sandbox_profile.schema.json (TARGET)
// "iam" block replaces "identity"; properties: roleSessionDuration, allowedRegions, allowedSecretPaths
//   required: ["roleSessionDuration", "allowedRegions"]
// "agent" block: REMOVED
// spec.required: drop "identity" (add "iam"); drop "agent" (no add — agent is optional in Wave 4)
```
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Rename IdentitySpec → IAMSpec, delete dead AgentSpec, fix schema drift</name>
  <files>
    pkg/profile/types.go,
    pkg/profile/schemas/sandbox_profile.schema.json,
    pkg/profile/inherit.go,
    pkg/profile/validate.go,
    pkg/profile/aws_validate.go,
    pkg/profile/types_test.go,
    pkg/compiler/security.go,
    pkg/compiler/service_hcl.go,
    pkg/allowlistgen/generator.go
  </files>
  <behavior>
    - After this task: `go build ./...` succeeds (no dangling `Spec.Identity` or `Spec.Agent.MaxConcurrentTasks` references).
    - After this task: `pkg/profile/inherit_test.go` existing tests pass with the rename.
    - After this task: Wave 0's `TestIAMHCLPhase92ByteIdentity` (VC-4) is GREEN — Terraform HCL output is byte-identical to the pre-Phase-92 baseline.
    - After this task: a profile YAML with `identity:` (the OLD key) fails `km validate` schema check with `additionalProperties: false` rejection on `spec.identity`.
    - After this task: a profile YAML with `iam: { roleSessionDuration: 1h, allowedRegions: [us-east-1] }` validates cleanly.
    - After this task: a profile YAML with `iam.allowedSecretPaths: [/foo]` is honored by the schema (drift closed).
    - After this task: a profile with `agent: { maxConcurrentTasks: 1 }` fails schema validation (`agent` is not in properties at all — Wave 4 adds it back with new shape).
    - After this task: `pkg/allowlistgen/generator.go` no longer constructs dead `AgentSpec{}`.
  </behavior>
  <action>
**`pkg/profile/types.go`:**
  1. Rename type `IdentitySpec` → `IAMSpec` (line 277).
  2. Delete field `SessionPolicy string` from the renamed struct.
  3. Keep `AllowedSecretPaths []string `yaml:"allowedSecretPaths,omitempty"`` (Phase 89 field).
  4. In `type Spec struct`: rename field `Identity IdentitySpec` → `IAM IAMSpec` (yaml tag `iam`).
  5. Delete the entire `type AgentSpec struct` block (line 372).
  6. Delete the `Agent AgentSpec` field from `type Spec struct` (line 38).
  7. Update any constructor helpers / defaults that reference the old names.
  8. Keep JSON/YAML tags consistent: `yaml:"iam"`.

**`pkg/profile/schemas/sandbox_profile.schema.json`:**
  1. Rename top-level key `"identity"` → `"iam"` in `properties.spec.properties` (line ~399).
  2. Inside the renamed `iam` block:
     - Remove `"sessionPolicy"` from `properties`.
     - Remove `"sessionPolicy"` from `required` (line ~401). New required: `["roleSessionDuration", "allowedRegions"]`.
     - Add `"allowedSecretPaths"`: `{ "type": "array", "items": { "type": "string" } }` to `properties` (drift fix).
  3. In `properties.spec.required`: replace `"identity"` with `"iam"`; remove `"agent"` (the dead `agent:` block is gone; Wave 4 will re-add as optional, not required).
  4. Delete the entire `"agent"` definition block (line ~488).
  5. Verify `additionalProperties: false` is still present on `spec` (it must be — that's how legacy `identity:` will be rejected).
  6. Decide `allowedRegions: minItems` per Claude's Discretion in CONTEXT.md §Claude's Discretion item 6: KEEP minItems: 1 unless a builtin profile breaks (open-dev with `[]` would need relaxation). Document the decision inline in commit message.

**`pkg/profile/inherit.go`:**
  1. Line 101: change `&result.Spec.Identity, &parent.Spec.Identity, &child.Spec.Identity` → `&result.Spec.IAM, &parent.Spec.IAM, &child.Spec.IAM`.
  2. Line 104: DELETE the `mergeSpecSection(&result.Spec.Agent, ...)` call entirely. (Wave 4 adds the new typed `mergeAgentSpec` for the new pointer-typed Spec.Agent.)

**`pkg/profile/validate.go`, `pkg/profile/aws_validate.go`:**
  1. Grep for `Spec.Identity` and rename each call site to `Spec.IAM`.
  2. Grep for `SessionPolicy` and delete each reference (it should be 0 remaining after rename in current code, but verify).
  3. Grep for `Spec.Agent` and decide per occurrence:
     - If reading dead fields (`MaxConcurrentTasks`, `TaskTimeout`, `AllowedTools`): delete the block.
     - If under a defensive nil-check that's no longer reachable: delete or replace with a placeholder TODO for Wave 4.

**`pkg/profile/types_test.go`:**
  1. Line 167 (per RESEARCH.md §3b): delete or rewrite the assertion `p.Spec.Agent.MaxConcurrentTasks`. If the test was only verifying that field, delete the test function. Don't leave a placeholder — clean removal.

**`pkg/compiler/security.go`:**
  1. Lines 50, 56, 74 (per RESEARCH.md §2c): replace `p.Spec.Identity.` with `p.Spec.IAM.` (3 sites).

**`pkg/compiler/service_hcl.go`:**
  1. Lines 1032, 1033 (per RESEARCH.md §2c): replace `p.Spec.Identity.AllowedSecretPaths` → `p.Spec.IAM.AllowedSecretPaths` (2 sites).
  2. If a YAML serialization tag is present in HCL emission (e.g., emits `identity = {}`), update to `iam = {}` to match new struct.

**`pkg/allowlistgen/generator.go` (RESEARCH.md §2j, §5c):**
  1. Lines 96-99: delete the `AgentSpec{MaxConcurrentTasks: 1, TaskTimeout: "30m"}` construction. If `MaxConcurrentTasks`/`TaskTimeout` values were genuinely needed elsewhere in the program, move them to a constant or function parameter. Per CONTEXT.md confirmation that these are "confirmed-dead", the values can be deleted entirely.

**Verification step within this task:**
  - Run `go build ./...` after Go-source changes; fix any dangling references.
  - Run `go test ./pkg/profile/... ./pkg/compiler/...` — Wave 0's `TestIAMHCLPhase92ByteIdentity` MUST be GREEN.
  - If byte-identity is broken, the rename inadvertently changed serialization — fix before committing.

Commit message: `feat(92-01): rename IdentitySpec → IAMSpec, delete dead AgentSpec, add allowedSecretPaths to schema`.
  </action>
  <verify>
    <automated>go build ./... &amp;&amp; go test ./pkg/profile/... ./pkg/compiler/... -run "TestIAMHCLPhase92ByteIdentity|TestUserdataLearnV2Phase92ByteIdentity" -count=1</automated>
    Expected: build succeeds; both byte-identity tests GREEN.
    VC-1, VC-4.
  </verify>
  <done>
    `go build ./...` succeeds; both Wave 0 byte-identity tests pass; schema has `iam:` and lacks `agent:`; no remaining `Spec.Identity` or dead `Spec.Agent.MaxConcurrentTasks` references in the codebase.
  </done>
</task>

<task type="auto">
  <name>Task 2: Rewrite all 20 profile YAMLs + 10 testdata YAMLs (identity → iam, drop sessionPolicy, drop dead agent block); add scripts/validate-all-profiles.sh</name>
  <files>
    profiles/ao.yaml,
    profiles/codex.yaml,
    profiles/dc34.yaml,
    profiles/dc34.ami.yaml,
    profiles/example-additional-snapshots.yaml,
    profiles/goose.yaml,
    profiles/learn.v2.yaml,
    profiles/learn.v2.chatty.yaml,
    profiles/learn.v2.codex.yaml,
    profiles/learn.v2.polite.yaml,
    profiles/locked.yaml,
    profiles/locked.ami.yaml,
    pkg/profile/builtins/ao.yaml,
    pkg/profile/builtins/codex.yaml,
    pkg/profile/builtins/goose.yaml,
    pkg/profile/builtins/hardened.yaml,
    pkg/profile/builtins/learn.yaml,
    pkg/profile/builtins/open-dev.yaml,
    pkg/profile/builtins/restricted-dev.yaml,
    pkg/profile/builtins/sealed.yaml,
    pkg/compiler/testdata/ec2-basic.yaml,
    pkg/compiler/testdata/ec2-empty-repos.yaml,
    pkg/compiler/testdata/ec2-with-allowed-refs.yaml,
    pkg/compiler/testdata/ec2-with-secrets.yaml,
    pkg/compiler/testdata/ec2-with-budget.yaml,
    pkg/compiler/testdata/ecs-basic.yaml,
    pkg/compiler/testdata/ecs-empty-repos.yaml,
    pkg/compiler/testdata/ecs-with-github.yaml,
    pkg/compiler/testdata/docker-basic.yaml,
    pkg/compiler/testdata/docker-with-budget.yaml,
    scripts/validate-all-profiles.sh
  </files>
  <action>
**Single atomic rewrite of all 30 YAMLs.** Group them into one task because they share the exact same mechanical transformation and must move together — partial state breaks the build (compiler testdata tests fail) and partial state breaks `km validate`.

**Transformation per file (identical for all 30):**
  1. Rename top-level YAML key `identity:` → `iam:`.
  2. Inside the renamed `iam:` block:
     - DELETE the `sessionPolicy:` key + value.
     - Keep `roleSessionDuration:`, `allowedRegions:`, `allowedSecretPaths:` (if present).
  3. DELETE the entire top-level `agent:` block (the dead one with `maxConcurrentTasks`/`taskTimeout`/`allowedTools`).
  4. Do NOT touch:
     - `spec.cli:` (Wave 2/3 own it)
     - `spec.execution:` (Wave 5 owns the inlined-JSON deletion)
     - `spec.runtime:` (Wave 2 adds the vscode sub-block)

**Special-case handling per CONTEXT.md:**
  - `profiles/example-additional-snapshots.yaml`: snapshot ID placeholders. Confirm `km validate` accepts them. If pattern check rejects e.g. `snap-PLACEHOLDER`, replace with synthetic `snap-aaaaaaaaaaaaaaaaa` (17 a's) per Claude's Discretion in CONTEXT.md.
  - `profiles/learn.v2.yaml`: this is the byte-identity baseline. After rewrite, `TestUserdataLearnV2Phase92ByteIdentity` (VC-3) must STILL pass — meaning compiler output is unchanged. If it fails, the YAML rewrite changed something the compiler reads (e.g., re-ordered keys that the compiler iterates). Investigate before committing.

**`scripts/validate-all-profiles.sh` (NEW FILE, per CONTEXT.md §Profile Inventory):**

  ```bash
  #!/usr/bin/env bash
  # scripts/validate-all-profiles.sh — Phase 92 hard gate.
  # Iterates the 20-file Profile Inventory and runs `km validate` against each.
  # Exits non-zero on any failure. Single source of truth for the inventory.
  #
  # Usage: bash scripts/validate-all-profiles.sh
  # Requires: km binary built (./km) — call `make build` first if needed.

  set -euo pipefail

  KM_BIN="${KM_BIN:-./km}"
  if [[ ! -x "$KM_BIN" ]]; then
    echo "ERROR: km binary not found at $KM_BIN. Run 'make build' first." >&2
    exit 2
  fi

  PROFILES=(
    profiles/ao.yaml
    profiles/codex.yaml
    profiles/dc34.yaml
    profiles/dc34.ami.yaml
    profiles/example-additional-snapshots.yaml
    profiles/goose.yaml
    profiles/learn.v2.yaml
    profiles/learn.v2.chatty.yaml
    profiles/learn.v2.codex.yaml
    profiles/learn.v2.polite.yaml
    profiles/locked.yaml
    profiles/locked.ami.yaml
    pkg/profile/builtins/ao.yaml
    pkg/profile/builtins/codex.yaml
    pkg/profile/builtins/goose.yaml
    pkg/profile/builtins/hardened.yaml
    pkg/profile/builtins/learn.yaml
    pkg/profile/builtins/open-dev.yaml
    pkg/profile/builtins/restricted-dev.yaml
    pkg/profile/builtins/sealed.yaml
  )

  fail=0
  for p in "${PROFILES[@]}"; do
    if "$KM_BIN" validate "$p" >/tmp/km-validate-$$.out 2>&1; then
      printf '  ok    %s\n' "$p"
    else
      printf '  FAIL  %s\n' "$p" >&2
      cat /tmp/km-validate-$$.out >&2
      fail=1
    fi
  done
  rm -f /tmp/km-validate-$$.out

  if [[ $fail -ne 0 ]]; then
    echo "" >&2
    echo "validate-all-profiles: at least one profile failed km validate" >&2
    exit 1
  fi
  echo "validate-all-profiles: all ${#PROFILES[@]} profiles valid"
  ```

  Per RESEARCH.md §3d, NO CI workflow files exist (`.github/workflows/` is absent). Skip the "wire into CI" sub-task from CONTEXT.md §Profile Inventory — this is local-verification only. Note this in the SUMMARY.

  Make the script executable: `chmod +x scripts/validate-all-profiles.sh`.

**Verification step within this task:**
  - Build km: `make build`.
  - Run: `bash scripts/validate-all-profiles.sh`. ALL 20 must pass.
  - Also run: `go test ./pkg/compiler/...` (testdata YAMLs are compiled in tests — they must validate too).

Commit message: `feat(92-01): rewrite 20 profile YAMLs + 10 testdata YAMLs (identity → iam, drop sessionPolicy + dead agent block); add scripts/validate-all-profiles.sh`.
  </action>
  <verify>
    <automated>make build &amp;&amp; bash scripts/validate-all-profiles.sh &amp;&amp; go test ./pkg/profile/... ./pkg/compiler/... -count=1</automated>
    Expected: all 20 profiles pass `km validate`; testdata YAMLs compile cleanly; byte-identity tests stay GREEN.
    VC-2, VC-3, VC-11.
  </verify>
  <done>
    All 30 YAMLs rewritten; `scripts/validate-all-profiles.sh` exists, is executable, and exits 0 against all 20 inventory files; `go test ./...` passes.
  </done>
</task>

<task type="auto">
  <name>Task 3: Doc sweep — Phase 89 sandbox-secrets + CLAUDE.md + OPERATOR-GUIDE.md</name>
  <files>
    docs/sandbox-secrets.md,
    CLAUDE.md,
    OPERATOR-GUIDE.md
  </files>
  <action>
**`docs/sandbox-secrets.md`:**
  - Grep for `identity.allowedSecretPaths` and replace with `iam.allowedSecretPaths` everywhere.
  - Grep for `spec.identity:` examples and update to `spec.iam:`.
  - Add a NOTE at the top: "Phase 92 (2026-05-31): `spec.identity:` renamed to `spec.iam:`. `allowedSecretPaths` now declared in JSON schema (drift fix). `sessionPolicy` removed without replacement."
  - Verify no remaining `sessionPolicy` references.

**`CLAUDE.md`:**
  - Grep for `identity.allowedSecretPaths` / `spec.identity` / `identity.sessionPolicy` and update each occurrence.
  - Update the `## Architecture` and any field-mapping sections to reflect `iam:`.
  - Do NOT touch sections describing notification fields, agent block, vscode — those move in later waves.
  - Add to "## Where to look" if a row references identity:
    | SOPS / SSM allowlist via `iam.allowedSecretPaths` | `docs/sandbox-secrets.md` (Phase 89, renamed Phase 92) |

**`OPERATOR-GUIDE.md`:**
  - Grep for `identity:` in profile YAML examples and update to `iam:`.
  - Grep for `sessionPolicy:` and delete from examples.
  - Add a Phase 92 entry under "Recent phase changes" or equivalent section:
    > Phase 92 (2026-05-31): `spec.identity:` → `spec.iam:`. `sessionPolicy:` removed. Dead `spec.agent:` block removed. Schema gained `iam.allowedSecretPaths` (Phase 89 drift fix).

**Smoke check after edits:**
  - `grep -rn 'spec\.identity\|sessionPolicy\|maxConcurrentTasks' docs/ CLAUDE.md OPERATOR-GUIDE.md` — must return zero lines (or only deliberately quoted-as-example legacy mentions).

Commit message: `docs(92-01): sweep identity → iam, drop sessionPolicy references (Phase 89 drift fix)`.
  </action>
  <verify>
    <automated>! grep -rn '\bspec\.identity\b\|\bsessionPolicy\b\|\bmaxConcurrentTasks\b' docs/sandbox-secrets.md CLAUDE.md OPERATOR-GUIDE.md</automated>
    Expected: zero matches. Exit code 0 from the negated grep means the sweep is clean.
    VC-1.
  </verify>
  <done>
    All three docs reflect the Wave 1 renames; the grep returns no stale references.
  </done>
</task>

</tasks>

<verification>
- `go test ./...` passes (VC-1).
- `bash scripts/validate-all-profiles.sh` exits 0 against all 20 inventory profiles (VC-11).
- `TestIAMHCLPhase92ByteIdentity` (Wave 0 stub) is GREEN — proves IAM Terraform output is unchanged (VC-4).
- `TestUserdataLearnV2Phase92ByteIdentity` (Wave 0 stub) is GREEN — proves learn.v2 userdata is unchanged at the source level (Wave 3 carries this forward to notification renames; this wave must not break it).
- `km validate` rejects a profile with `identity:` key (legacy) — verify with a one-shot ad-hoc fixture (`echo "..." | km validate -`) (VC-2).
- `km validate` rejects a profile with `spec.agent: {maxConcurrentTasks: 1}` — verify ad-hoc (VC-2).
- Grep audit of docs returns no `identity`/`sessionPolicy`/`maxConcurrentTasks` references.
</verification>

<success_criteria>
- IAM rename done in 5 sites (3 in security.go + 2 in service_hcl.go) — RESEARCH.md §2c confirmed.
- `pkg/allowlistgen/generator.go` no longer references dead `AgentSpec{MaxConcurrentTasks,TaskTimeout}` — RESEARCH.md §2j discrepancy honored.
- `pkg/profile/configfiles/` directory is NOT touched in this wave (it doesn't exist per RESEARCH.md §2f).
- No CI workflow integration attempted (`.github/workflows/` absent per RESEARCH.md §3d).
- All 20 profile YAMLs + 10 testdata YAMLs migrated to `iam:` in one atomic commit per file group.
- The dead `agent:` block deletion is recorded in CONTEXT.md style — Wave 4 will re-introduce a new `agent:` shape.
</success_criteria>

<output>
After completion, create `.planning/phases/92-profile-spec-restructure-notification-block-iam-rename-dead-field-removal-structured-agent-tool-gating/92-01-SUMMARY.md` capturing:
- The 5 IAM rename sites with file:line evidence (3 in security.go, 2 in service_hcl.go).
- The `pkg/allowlistgen/generator.go` dead-field fix.
- The `allowedSecretPaths` schema-drift-fix note.
- The full path list of 30 YAMLs touched.
- Confirmation that Wave 0's byte-identity tests stayed GREEN through this wave.
- A note that Wave 2/3 own the notification block and Wave 4 will re-introduce `agent:` with new shape — Wave 1 cleanup intentionally leaves the slot empty.
</output>
