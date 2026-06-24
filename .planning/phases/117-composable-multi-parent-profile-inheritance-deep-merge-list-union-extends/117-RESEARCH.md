# Phase 117: Composable Multi-Parent Profile Inheritance — Research

**Researched:** 2026-06-24
**Domain:** Go YAML parsing, profile inheritance engine, deep-merge, DAG resolution
**Confidence:** HIGH

---

## Summary

Phase 117 replaces `pkg/profile/inherit.go`'s typed-per-section reflection merge with a single generic `map[string]any` deep-merge. The implementation is straightforward because the codebase already uses `github.com/goccy/go-yaml` v1.19.2 throughout, which decodes YAML maps to `map[string]interface{}` (not `map[interface{}]interface{}`), making the round-trip `YAML → map[string]any → deepMerge → yaml.Marshal → yaml.Unmarshal(*SandboxProfile)` clean and faithful.

The existing merge engine has two documented classes of defects: (1) eight sections merge at whole-struct granularity via `reflect.IsZero` so a child touching any field replaces the entire section; (2) six pointer sections (`Artifacts`, `Budget`, `Email`, `OTP`, `CLI`, `Secrets`) are not merged at all — the child's `nil` silently drops the parent's value. The generic deep-merge eliminates both defect classes in one function.

No profile in `profiles/` or `pkg/profile/builtins/` currently uses `extends:`, so there is zero production behavior change from the engine replacement. The byte-identity golden for `learn.v2.yaml` (`TestUserdataLearnV2Phase92ByteIdentity`) is the regression gate for Plan 04's YAML refactor.

**Primary recommendation:** Implement the generic `map[string]any` deep-merge engine (Plan 02), wire it into the existing `Resolve` + `load` paths (Plan 03), then author `base/` fragments and refactor the five `learn.v2.*` variants plus `dc34.yaml` (Plan 04). Keep the DAG resolution simple: topological sort on resolved bases, memoize by path, raise `maxInheritanceDepth` from 3 to 10. Fragment marker: `metadata.abstract: true` (add to JSON Schema `metadata.properties` as optional boolean; `km validate` and `validate-all-profiles.sh` skip any profile where the decoded fragment has `metadata.abstract: true`).

---

## Standard Stack

### Core (already in go.mod)
| Library | Version | Purpose | Role in Phase 117 |
|---------|---------|---------|-------------------|
| `github.com/goccy/go-yaml` | v1.19.2 | YAML parse/marshal | `yaml.Unmarshal(raw, &map[string]any)` for deep-merge input; `yaml.Marshal(acc)` to re-serialize merged result |
| `encoding/json` | stdlib | JSON marshal | Used in `ValidateSchema` (YAML→JSON→jschema); unchanged |
| `github.com/santhosh-tekuri/jsonschema/v6` | existing | JSON Schema validator | `extends` field type change from `string` to `oneOf[string, array]` requires schema update |

### No new dependencies required
The entire deep-merge engine is pure Go. `goccy/go-yaml` already handles `map[string]any` faithfully.

---

## Architecture Patterns

### Recommended Directory Structure
```
pkg/profile/
├── inherit.go          # Replace entirely — new deepMerge engine + DAG resolve
├── inherit_test.go     # Add table-driven deep-merge tests alongside existing
├── types.go            # Add Extends []string union type + UnmarshalYAML
├── validate.go         # Add fragment-skip path (metadata.abstract check)
├── schema.go           # Unchanged (schema update is in the .json file)
├── schemas/
│   └── sandbox_profile.schema.json   # extends: string|array; metadata.abstract: bool
profiles/
├── base/               # New — partial fragment YAML files
│   ├── safenetwork.yaml
│   ├── ir-tools.yaml
│   ├── agent-claude.yaml
│   ├── observability-standard.yaml
│   └── ...
├── learn.v2.yaml       # Refactored to extends: [base/...]
├── learn.v2.chatty.yaml
├── learn.v2.polite.yaml
├── learn.v2.codex.yaml
├── learn.v2.desktop.yaml
└── dc34.yaml
```

### Pattern 1: Union `extends` Field with Custom UnmarshalYAML (goccy/go-yaml)

The `Extends` field on `SandboxProfile` is currently `string`. It must become a type that accepts both a bare string and a sequence.

```go
// In pkg/profile/types.go

// ExtendsField is a string | []string union for the YAML `extends:` key.
// Single-string extends: "base/foo" is back-compat; list extends:
// [base/foo, base/bar] triggers multi-parent resolution.
type ExtendsField []string

// UnmarshalYAML implements goccy/go-yaml custom decoding for the string|[]string union.
// A scalar "foo" becomes []string{"foo"}.
// A sequence ["foo","bar"] becomes []string{"foo","bar"}.
func (e *ExtendsField) UnmarshalYAML(ctx context.Context, unmarshal func(interface{}) error) error {
    // Try scalar first
    var s string
    if err := unmarshal(&s); err == nil {
        *e = ExtendsField{s}
        return nil
    }
    // Try sequence
    var ss []string
    if err := unmarshal(&ss); err != nil {
        return err
    }
    *e = ExtendsField(ss)
    return nil
}

// In SandboxProfile struct:
Extends ExtendsField `yaml:"extends,omitempty"`
```

**goccy/go-yaml custom unmarshaler note:** goccy/go-yaml v1.x uses `func (e *T) UnmarshalYAML(ctx context.Context, unmarshal func(interface{}) error) error` — the context-aware two-argument form (not the gopkg.in/yaml.v3 one-argument form). Verify against goccy/go-yaml docs; the function signature differs from standard yaml.v3.

**Confidence:** HIGH — goccy/go-yaml's custom unmarshal is documented and used extensively in Go YAML tooling.

### Pattern 2: Generic map[string]any Deep-Merge Engine

```go
// deepMerge merges src into dst (both map[string]any) recursively.
// Scalars: src wins (last-write).
// Maps: recursive key-union.
// Slices: concat then dedup (order-preserving, first-occurrence kept).
func deepMerge(dst, src map[string]any) map[string]any {
    if dst == nil {
        dst = make(map[string]any)
    }
    for k, sv := range src {
        dv, exists := dst[k]
        if !exists {
            dst[k] = sv
            continue
        }
        // Both have the key — check types
        dMap, dIsMap := dv.(map[string]any)
        sMap, sIsMap := sv.(map[string]any)
        if dIsMap && sIsMap {
            dst[k] = deepMerge(dMap, sMap)
            continue
        }
        dSlice, dIsSlice := toSlice(dv)
        sSlice, sIsSlice := toSlice(sv)
        if dIsSlice && sIsSlice {
            dst[k] = concatDedup(dSlice, sSlice)
            continue
        }
        // Scalar: src wins
        dst[k] = sv
    }
    return dst
}
```

**Round-trip faithfulness:** `goccy/go-yaml` v1.19.2 decodes YAML to `map[string]interface{}` (same as `map[string]any`) — confirmed by codebase usage in `ValidateSchema` at `pkg/profile/validate.go:78` which does `yaml.Unmarshal(raw, &doc)` into `any` and then `json.Marshal(doc)` successfully. The marshal→unmarshal round-trip to `*SandboxProfile` is faithful with one caveat: **non-pointer, non-omitempty bool fields will serialize as `false` when absent from a fragment** (see Pitfall 3 below).

### Pattern 3: DAG Resolution with Memoization

Replace the current single-parent chain:

```go
// Current: linear chain
func resolve(name string, searchPaths []string, visited map[string]bool, depth int) (*SandboxProfile, error)

// Phase 117: DAG — breadth-first, left→right, child last
func resolve(name string, searchPaths []string, visited map[string]bool, depth int, memo map[string]map[string]any) (*SandboxProfile, error) {
    // ... load raw bytes → map[string]any
    // for each base in profile.Extends (left to right):
    //   if already in memo: use cached map
    //   else: recurse, memoize result
    // acc = deepMerge(base1, base2, ..., child_map)
    // yaml.Marshal(acc) → yaml.Unmarshal → *SandboxProfile
    // return result
}
```

**Depth/cycle handling for DAG:**
- `visited map[string]bool` must be path-scoped (per resolution chain), not shared across siblings, or diamond bases are falsely flagged as cycles. Use a per-chain `visited` copy, or track the DAG via topological sort.
- Memoization key: absolute resolved path (or builtin name). Memoizing by-path prevents redundant resolution of shared bases.
- Raise `maxInheritanceDepth` from 3 to 10 (or per-base-count: depth = longest path from leaf to root).

### Anti-Patterns to Avoid

- **Diamond cycle false-positive:** Passing a single `visited` map across all branches of a multi-parent resolves incorrectly — a base used by two siblings will be flagged as a cycle. Each branch needs its own path-ancestry set; the memo cache handles the shared-base optimization separately.
- **Re-using gopkg.in/yaml.v3 in the new engine:** The codebase is uniformly `goccy/go-yaml`. Do not introduce `gopkg.in/yaml.v3` Marshal in the merge engine — use `github.com/goccy/go-yaml` throughout.
- **marshal → unmarshal → validate chain on fragments:** Fragments must NOT pass through `Validate()` (they fail required-field checks). Only the final merged leaf is validated.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| String|slice YAML union | Custom tokenizer | `UnmarshalYAML` on `ExtendsField` | goccy/go-yaml calls the interface; 4 lines of code |
| Deep map merge | Anything using reflection | Recursive `map[string]any` function | Already have `map[string]any` from YAML decode |
| Topological sort for DAG | Custom graph lib | Depth-first with per-branch visited set | DAG is shallow (realistic depth ≤ 5) |
| Schema validation of fragments | Custom validator | Skip by `metadata.abstract: true` check before calling `ValidateSchema` | Single boolean check, zero schema changes to the required-field constraints |

---

## Common Pitfalls

### Pitfall 1: Diamond Inheritance with Shared `visited` Map

**What goes wrong:** Current `resolve()` passes a single `visited map[string]bool` through the call chain. If two bases in a multi-parent list both extend the same grandparent, the second resolution of grandparent triggers the cycle guard.

**Root cause:** `visited` is mutation-shared; it accumulates all ancestors from sibling branches.

**How to avoid:** For DAG resolution, `visited` must track the ancestry of the *current path* only. Pass a copy per branch, or separate "cycle detection" (path-based) from "memoization" (result caching). The simplest correct approach: for each base in `extends`, pass a *new copy* of the current chain's visited set when recursing into that base. The shared `memo` map handles deduplication of work.

### Pitfall 2: Non-Pointer Bool Fields and the Zero-Value Trap

**What goes wrong:** `RuntimeSpec.Spot bool` (no omitempty), `SidecarConfig.Enabled bool` (no omitempty), `TlsCaptureSpec.Enabled bool` (no omitempty) — when absent from a fragment, `goccy/go-yaml` encodes them as `false` in the `map[string]any`. A base fragment that defines only `spec.network` will have `spec.runtime.spot: false` in its decoded map, which then deep-merges as `false` into a child that wants `spot: true`.

**Root cause:** The round-trip only faithfully preserves what was in the source YAML. A fragment that omits `spot:` decodes `spot` as the Go zero value `false` — but `false` is a valid explicit value, not "absent". The `map[string]any` has no way to distinguish omitted-from-YAML vs explicitly-`false`.

**How to avoid:** Base fragments MUST only declare the fields they intend to set. They must not declare full `spec.runtime:` blocks unless they intend to set ALL runtime fields. The deep-merge engine must handle the case where a *scalar false* in a base is overridden by a *scalar true* in the child (scalar: last/child wins). So the risk is only: base fragment sets `spot: false` → child ALSO needs to set `spot: true` to override it. Document this clearly in `OPERATOR-GUIDE.md`.

**Warning signs:** Unexpected `spot: false` or `hibernation: false` after merge when child profile expected `true`.

### Pitfall 3: The `extends` Field in JSON Schema Currently Declares `type: string`

**What goes wrong:** After adding `ExtendsField` custom unmarshaling to Go, the JSON Schema at `pkg/profile/schemas/sandbox_profile.schema.json` line 44 still says `"type": "string"`. Schema validation (called from `ValidateSchema`) runs BEFORE `Parse` and will reject any profile that uses `extends: [...]` list syntax.

**Root cause:** `ValidateSchema` converts raw YAML → JSON → runs jschema. A list `extends` produces a JSON array, which fails the current `"type": "string"` constraint.

**How to avoid:** Update the schema's `extends` definition to `oneOf: [{type: string}, {type: array, items: {type: string}}]` in Plan 01. **This must land before Plan 03 wire-in**, or `km validate` will reject the new syntax.

### Pitfall 4: `initCommandsAppend` — Explicit Field vs List-Concat Ambiguity

**What goes wrong:** The CONTEXT.md open question D proposes an explicit `execution.initCommandsAppend` field. If the phase uses pure list-concat without it, a standalone profile (no `extends:`) that sets `initCommands: [...]` will have its list treated as "append to base" after refactoring — confusing for profiles that inherit nothing.

**Root cause:** List concat is only unambiguous when there's a base to concat with. Without `initCommandsAppend`, the semantic of `initCommands: [...]` changes depending on whether the profile uses `extends:`.

**How to avoid:** Introduce `execution.initCommandsAppend: []string` as an explicit separate field (Plan 01/02). A profile's `initCommands` always means "the complete list for standalone use"; `initCommandsAppend` is concatenated after the inherited/merged `initCommands`. This preserves backward compatibility (all existing profiles use only `initCommands`; none gain new behavior unless they use `extends:`).

### Pitfall 5: validate.go Currently Checks `parsed.Extends != ""` (string comparison)

**What goes wrong:** After changing `Extends` from `string` to `ExtendsField` (`[]string`), the checks in `internal/app/cmd/validate.go` line 76 (`parsed.Extends != ""`) and `internal/app/cmd/create.go` lines 342, 2094 (`parsed.Extends != ""`) will no longer compile.

**Root cause:** Type change from `string` to `ExtendsField` breaks string-equality comparisons.

**How to avoid:** Add a helper method `func (e ExtendsField) IsSet() bool { return len(e) > 0 }` and update all three call sites. Track the three locations: `validate.go:76`, `create.go:342`, `create.go:2094`.

### Pitfall 6: `load()` Resolves Extends as Builtin or `dir/name.yaml` — Must Handle `base/foo` Relative Paths

**What goes wrong:** Current `load()` at `inherit.go:49` checks `IsBuiltin(name)` first, then searches `searchPaths` for `<dir>/<name>.yaml`. A fragment reference `base/safenetwork` would search for `base/safenetwork.yaml` in each search-path directory — correct for sibling `profiles/base/` but needs the relative resolution to work from the *declaring file's directory*.

**Root cause:** `searchPaths` in `validate.go:84` and `create.go:345` includes `filepath.Dir(filePath)` as the first entry, so `profiles/dc34.yaml` referencing `base/safenetwork` would look for `profiles/base/safenetwork.yaml` — which is the desired behavior. BUT if a base fragment itself references another base, the relative path must be resolved from the fragment's location, not the original leaf's location.

**How to avoid:** When loading a fragment that in turn has `extends:`, the search path for the fragment's bases must include the fragment's directory. This means `load()` must be path-aware (know the file's own directory) rather than just accepting a name + searchPaths. Consider changing `load()` to accept a `baseDir string` parameter from the resolved path, and prepend it to searchPaths for recursive calls.

### Pitfall 7: The Frozen byte-identity Golden (`learn.v2.yaml`)

**What goes wrong:** `TestUserdataLearnV2Phase92ByteIdentity` in `pkg/compiler/userdata_phase92_byte_identity_test.go` calls `profile.Parse(raw)` on `profiles/learn.v2.yaml` directly — NOT `profile.Resolve()`. As long as `learn.v2.yaml` has no `extends:` field, it bypasses the new merge engine entirely and the golden remains pristine.

**Root cause:** Plan 04 refactors `learn.v2.yaml` onto base fragments (adding `extends:`). At that point, `generateLearnV2Userdata()` at line 41 will call `profile.Parse()` — which only reads the extends field but does NOT resolve it. The test will compile the leaf's partial spec, not the merged spec, producing wrong output.

**How to avoid:** When Plan 04 adds `extends:` to `learn.v2.yaml`, the `generateLearnV2Userdata()` helper MUST be updated to use `profile.Resolve()` (with the profiles/ directory as searchPath) instead of `profile.Parse()`. The golden file must be re-captured AFTER the refactor, with a byte-diff proving the compiled output is semantically equivalent to pre-refactor. See memory entry `project_frozen_byte_identity_golden_capture_trap` for the capture trap (do NOT use `CAPTURE_PRE92_BASELINE=1` — that re-creates the wrong baseline).

---

## Code Examples

### Current merge() Entry Point (to be replaced — inherit.go:75)

```go
// CURRENT — at pkg/profile/inherit.go:75-125
func merge(parent, child *SandboxProfile) *SandboxProfile {
    result := &SandboxProfile{
        APIVersion: child.APIVersion,
        Kind:       child.Kind,
        Metadata:   child.Metadata,
        Spec:       child.Spec,  // child.Spec bulk-copied — all pointer sections use child's value
    }
    // ... mergeSpecSection (reflect.IsZero per section: all-or-nothing)
    // ... mergeNotificationSpec (typed, field-level)
    // ... mergeAgentSpec (typed, field-level)
    // NOT merged: Artifacts, Budget, Email, OTP, CLI, Secrets
}
```

### Current resolve() (single-parent chain — inherit.go:21)

```go
// CURRENT — at pkg/profile/inherit.go:21-47
func resolve(name string, searchPaths []string, visited map[string]bool, depth int) (*SandboxProfile, error) {
    if depth > maxInheritanceDepth { ... }  // maxInheritanceDepth = 3
    if visited[name] { return nil, "circular" }
    visited[name] = true
    profile, _ := load(name, searchPaths)
    if profile.Extends == "" { return profile, nil }
    parent, _ := resolve(profile.Extends, searchPaths, visited, depth+1)
    merged := merge(parent, profile)
    return merged, nil
}
```

### Current load() (builtin-first, then searchPaths — inherit.go:49)

```go
// CURRENT — at pkg/profile/inherit.go:49-69
func load(name string, searchPaths []string) (*SandboxProfile, error) {
    if IsBuiltin(name) { return LoadBuiltin(name) }
    for _, dir := range searchPaths {
        path := filepath.Join(dir, name+".yaml")   // NOTE: appends ".yaml" automatically
        data, _ := os.ReadFile(path)
        p, _ := Parse(data)
        return p, nil
    }
    return nil, "not found"
}
```

### Current mergeSpecSection (all-or-nothing reflect — inherit.go:384)

```go
// CURRENT — at pkg/profile/inherit.go:384-396
func mergeSpecSection(result, parent, child interface{}) {
    childVal := reflect.ValueOf(child).Elem()
    parentVal := reflect.ValueOf(parent).Elem()
    resultVal := reflect.ValueOf(result).Elem()
    if childVal.IsZero() {
        resultVal.Set(parentVal)  // use parent only if child is ALL zeros
    } else {
        resultVal.Set(childVal)   // use child if ANY child field is set
    }
}
```

### Proposed: ExtendsField Union Type

```go
// pkg/profile/types.go — replace `Extends string` with:
type ExtendsField []string

func (e ExtendsField) IsSet() bool { return len(e) > 0 }

// goccy/go-yaml custom unmarshaler (context-aware two-argument form):
func (e *ExtendsField) UnmarshalYAML(ctx context.Context, unmarshal func(interface{}) error) error {
    var s string
    if err := unmarshal(&s); err == nil && s != "" {
        *e = ExtendsField{s}
        return nil
    }
    var ss []string
    if err := unmarshal(&ss); err != nil {
        return err
    }
    *e = ExtendsField(ss)
    return nil
}

type SandboxProfile struct {
    APIVersion string       `yaml:"apiVersion"`
    Kind       string       `yaml:"kind"`
    Metadata   Metadata     `yaml:"metadata"`
    Extends    ExtendsField `yaml:"extends,omitempty"`
    Spec       Spec         `yaml:"spec"`
}
```

### Proposed: Fragment Marker in Schema and Validate

```json
// pkg/profile/schemas/sandbox_profile.schema.json — add to metadata.properties:
"abstract": {
  "type": "boolean",
  "description": "When true, this is a partial base fragment and must not be validated standalone."
}
```

```go
// pkg/profile/validate.go — before running ValidateSchema, check for abstract marker:
func IsAbstractFragment(raw []byte) bool {
    var doc map[string]any
    if err := yaml.Unmarshal(raw, &doc); err != nil {
        return false
    }
    meta, ok := doc["metadata"].(map[string]any)
    if !ok { return false }
    v, _ := meta["abstract"].(bool)
    return v
}
```

### Proposed: validate-all-profiles.sh update for base/ skip

```bash
# scripts/validate-all-profiles.sh — add near the PROFILES array:
# Skip profiles/base/ — partial fragments are validated only when merged into a leaf.
for p in profiles/base/*.yaml; do
  printf '  skip  %s (base fragment)\n' "$p"
done
```

---

## Exact Overlap in profiles/ — What Plan 04 Extracts

### learn.v2.yaml vs learn.v2.{chatty,polite,codex} — Byte-Identical Sections

Comparing `profiles/learn.v2.yaml` and `profiles/learn.v2.chatty.yaml` (representative):

**Byte-identical between all four learn.v2 variants:**
- `spec.lifecycle` (entire block: ttl, idleTimeout, teardownPolicy)
- `spec.runtime` (entire block: ami, substrate, instanceType, region, spot, hibernation, rootVolumeSize, mountEFS, efsMountPoint, additionalVolume)
- `spec.execution.shell`, `.workingDir`, `.useBedrock`, `.privileged`, `.env` (5 keys), `.rsyncPaths` (5 entries)
- `spec.execution.configFiles["/home/sandbox/.claude/plugins/known_marketplaces.json"]` (the marketplace JSON) — present in learn.v2.yaml but NOT in chatty/polite which add their own settings.json key
- `spec.execution.initCommands` lines 1–14 (yum install through chown -R); lines 15–17 differ (version pins: claude-code `@2.1.132` vs dc34's `@2.1.114`; codex `rust-v0.133.0` vs dc34's `rust-v0.121.0`)
- `spec.sourceAccess` (entire block)
- `spec.network` (entire block)
- `spec.budget` (entire block)
- `spec.artifacts` (entire block)
- `spec.iam` (entire block)
- `spec.sidecars` (entire block — all four sidecars identical)
- `spec.observability` (entire block — learnMode: true + cloudwatch + tlsCapture)
- `spec.email` (entire block — except allowedSenders differs: dc34 has `self` and email addresses; learn.v2 has `*`)
- `spec.cli` (entire block: noBedrock: true)
- `spec.agent` (entire block: claude.trustedDirectories, tools.autoApprove 9 tools, args --dangerously-skip-permissions)

**Per-variant deltas (what stays in the leaf):**
- `learn.v2.yaml`: metadata.name/prefix, notification block (github.inbound.enabled:true, invites, archiveOnDestroy:false)
- `learn.v2.chatty.yaml`: metadata labels (phase91), `inbound.mentionOnly:false`, `invites.useConnect:false`, NO github.inbound
- `learn.v2.polite.yaml`: metadata labels (phase91), `inbound.mentionOnly:true`, `inbound.reactAlways:false`, `invites.useConnect:false`
- `learn.v2.codex.yaml`: `agent.default:codex`, different notification (no github.inbound)
- `learn.v2.desktop.yaml`: VERY different — `ubuntu-24.04` AMI, `runtime.desktop` block, different initCommands

**learn.v2.yaml vs dc34.yaml — Differences:**
- `dc34` adds: `useBedrock:true` (vs false), `env.SANDBOX_MODE:goose-ebpf-gatekeeper` (vs `learn-v2-direct-api`), adds GOOSE_PROVIDER/GOOSE_MODEL/GOOSE_MODE env keys, `env.OPENAI_API_KEY:""` (same), adds a CLAUDE.md configFile, different `initCommands` tail (adds goose config, adds meshtk/klanker-maker git clones, adds nvm, adds npx get-shit-done), `rsyncPaths` adds `.aws`, `instanceType:t3.large` (vs t3.2xlarge), `rootVolumeSize:15` (vs 80), `spot:false`, `hibernation:true` (vs false), `mountEFS:true`, `email.allowedSenders` (specific emails vs `*`), `notification` has no github.inbound, `archiveOnDestroy:true` (vs false)

**Viable base fragments for Plan 04:**
- `base/safenetwork.yaml` — `spec.network.enforcement:both + egress.*:"*"` (shared by dc34 + all learn variants)
- `base/sidecars-all.yaml` — complete `spec.sidecars` block (byte-identical across all)
- `base/observability-learn.yaml` — `spec.observability` block (byte-identical across learn variants)
- `base/budget-standard.yaml` — standard budget block (shared across learn variants; dc34 has same values)
- `base/artifacts-workspace.yaml` — `spec.artifacts` (paths: [/workspace], maxSizeMB: 500)
- `base/iam-us-east-1.yaml` — `spec.iam.roleSessionDuration:1h + allowedRegions:[us-east-1]`
- `base/agent-claude-all-tools.yaml` — `spec.agent.claude.trustedDirectories + tools.autoApprove (9 tools) + args`
- `base/email-strict.yaml` — `spec.email.signing:required + verifyInbound:required + encryption:required`

**IMPORTANT — cannot share as base without initCommandsAppend:**
`spec.execution.initCommands` differs between dc34 and learn.v2 only in version pins (claude-code `@2.1.114` vs `@2.1.132`, codex `rust-v0.121.0` vs `rust-v0.133.0`) and trailing clones. If `initCommandsAppend` is introduced (CONTEXT.md open question D), the 14-command common head can go in a base and version-specific installs move to `initCommandsAppend` in each leaf. Without it, `initCommands` stays in each leaf.

---

## State of the Art

| Old Approach | Current in Codebase | Phase 117 Change |
|--------------|---------------------|-----------------|
| `extends: string` (single parent) | In `SandboxProfile.Extends string` | `Extends ExtendsField` (string|[]string union) |
| Per-section `mergeSpecSection` (reflect.IsZero all-or-nothing) | 8 sections via reflect | Generic `map[string]any` deep-merge |
| Typed merger zoo (mergeNotificationSpec, mergeAgentSpec, ...) | 12+ functions | Collapsed into single deepMerge |
| Budget/Artifacts/Email/OTP/CLI/Secrets NOT merged | Confirmed at inherit.go:80 (`Spec: child.Spec`) | Fixed by map-level merge |
| Single-parent chain (maxDepth=3) | inherit.go:10 | Multi-parent DAG with memoization; raise depth to 10 |
| All profiles standalone-valid | scripts/validate-all-profiles.sh hardcodes full inventory | Add `metadata.abstract: true` skip |
| List fields replace (never union) | mergeAgentToolsSpec: child non-empty replaces parent | Lists concat+dedup |

**Deprecated (to delete in Plan 02):**
- `mergeSpecSection` (reflect-based, all-or-nothing)
- `mergeNotificationSpec` / `mergeNotificationEventsSpec` / `mergeNotificationEmailSpec` / `mergeNotificationSlackSpec` / `mergeNotificationSlackInboundSpec` / `mergeNotificationSlackTranscriptSpec` / `mergeNotificationSlackInvitesSpec` / `mergeNotificationGitHubSpec` / `mergeNotificationGitHubInboundSpec`
- `mergeAgentSpec` / `mergeAgentClaudeSpec` / `mergeAgentCodexSpec` / `mergeAgentToolsSpec`
- `mergePermissionsPassthrough`
- `pickBoolPtr` / `pickIntPtr` / `pickString`

---

## Open Questions

1. **`initCommandsAppend` — introduce now or defer?**
   - What we know: without it, `initCommands` cannot be partially inherited + extended; every leaf must repeat or omit the full list; CONTEXT.md labels this a "default lean: introduce" in Plan 01/02.
   - What's unclear: whether `initCommandsAppend` needs a schema type (`[]string`) and a compiler-level handling, or whether it's purely a merge-time concept.
   - Recommendation: introduce `execution.initCommandsAppend: []string` in Plan 01 (schema + types.go addition); the merge engine appends it to the inherited `initCommands` during resolve. At compile time, `initCommands` is the final merged list. This is additive to the schema (`omitempty`) and requires no compiler changes.

2. **goccy/go-yaml UnmarshalYAML exact signature for v1.19.2**
   - What we know: goccy/go-yaml v1.x uses a context-aware form, not the gopkg.in/yaml.v3 form.
   - What's unclear: exact import path for `context.Context` and whether the unmarshaler is `func(interface{}) error` or `func(any) error`.
   - Recommendation: Write a 10-line unit test for `ExtendsField.UnmarshalYAML` in Plan 01 before wiring in the rest, to confirm the signature compiles cleanly.

3. **Fragment base path resolution for nested fragments**
   - What we know: Current `load()` prepends the declaring-leaf's directory to searchPaths. A base fragment in `profiles/base/foo.yaml` that itself uses `extends: base/grandparent` would search `profiles/base/base/grandparent.yaml` — wrong.
   - Recommendation: `load()` must return both the parsed profile and the file's resolved directory, so recursive base resolution starts from the fragment's own directory. Change signature to `loadWithDir(name, baseDir string, searchPaths []string) (*SandboxProfile, resolvedDir string, error)`.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing stdlib |
| Config file | none (standard `go test`) |
| Quick run command | `go test ./pkg/profile/... -count=1 -run TestDeepMerge -timeout 60s` |
| Full suite command | `go test ./pkg/profile/... ./pkg/compiler/... -count=1 -timeout 600s` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command | File |
|----------|-----------|-------------------|------|
| string|[]string `extends` unmarshal | unit | `go test ./pkg/profile/... -run TestExtendsUnmarshal` | pkg/profile/inherit_test.go (add) |
| Scalar last-wins deep merge | unit | `go test ./pkg/profile/... -run TestDeepMerge_ScalarWins` | pkg/profile/inherit_test.go (add) |
| Nested map key-union deep merge | unit | `go test ./pkg/profile/... -run TestDeepMerge_MapUnion` | pkg/profile/inherit_test.go (add) |
| List concat+dedup (string lists) | unit | `go test ./pkg/profile/... -run TestDeepMerge_ListDedup` | pkg/profile/inherit_test.go (add) |
| List concat+dedup (object lists AdditionalSnapshots) | unit | `go test ./pkg/profile/... -run TestDeepMerge_ObjectListDedup` | pkg/profile/inherit_test.go (add) |
| Diamond inheritance idempotence | unit | `go test ./pkg/profile/... -run TestResolve_Diamond` | pkg/profile/inherit_test.go (add) |
| Diamond: base resolved once (memoization) | unit | `go test ./pkg/profile/... -run TestResolve_DiamondMemoized` | pkg/profile/inherit_test.go (add) |
| Multi-parent ordering (left→right→child precedence) | unit | `go test ./pkg/profile/... -run TestResolve_MultiParentOrder` | pkg/profile/inherit_test.go (add) |
| Cycle detection for DAG (still catches true cycles) | unit | `go test ./pkg/profile/... -run TestResolveCircularDetection` | pkg/profile/inherit_test.go (existing) |
| Depth guard for DAG (raised to 10) | unit | `go test ./pkg/profile/... -run TestResolveDepthExceeded` | pkg/profile/inherit_test.go (update limit) |
| Fragment skip in validate (metadata.abstract:true) | unit | `go test ./pkg/profile/... -run TestIsAbstractFragment` | pkg/profile/validate_test.go (add) |
| Notification + Agent blocks still merge field-level | unit | `go test ./pkg/profile/... -run TestInherit` | inherit_notification_test.go, inherit_agent_test.go (existing — must stay green) |
| km validate accepts list extends syntax | integration | `./km validate profiles/learn.v2.yaml` | manual smoke / validate-all-profiles.sh |
| learn.v2.yaml byte-identity golden (Plan 04 gate) | regression | `go test ./pkg/compiler/... -run TestUserdataLearnV2Phase92ByteIdentity` | pkg/compiler/userdata_phase92_byte_identity_test.go (existing; helper update needed for Plan 04) |
| validate-all-profiles.sh green with base/ fragments | integration | `bash scripts/validate-all-profiles.sh` | scripts/validate-all-profiles.sh (update for base/ skip) |
| dc34.yaml resolves multi-parent and validates | smoke | `./km validate profiles/dc34.yaml` | manual |
| Compiled output equivalence after refactor | regression | userdata byte-diff before/after Plan 04 | manual + byte-diff script |

### Sampling Rate
- **Per task commit:** `go test ./pkg/profile/... -count=1 -timeout 60s`
- **Per wave merge:** `go test ./pkg/profile/... ./pkg/compiler/... -count=1 -timeout 600s`
- **Phase gate:** `go test ./pkg/profile/... ./pkg/compiler/... -count=1 -timeout 600s` + `bash scripts/validate-all-profiles.sh` + `./km validate profiles/dc34.yaml` green before `/gsd:verify-work`

### Wave 0 Gaps (new tests needed before implementation)
- [ ] `pkg/profile/inherit_test.go` — add `TestExtendsUnmarshal`, `TestDeepMerge_*`, `TestResolve_Diamond`, `TestResolve_DiamondMemoized`, `TestResolve_MultiParentOrder`, `TestIsAbstractFragment` (Plan 01/02)
- [ ] `testdata/profiles/diamond-base.yaml`, `diamond-a.yaml`, `diamond-b.yaml`, `diamond-child.yaml` — test fixtures for diamond cases (Plan 02)
- [ ] `testdata/profiles/multi-parent-child.yaml` — extends: [base-a, base-b] test fixture (Plan 02)

*(If no gaps for existing tests: the full `pkg/profile` test suite at `go test ./pkg/profile/... -count=1` is already green and must remain green throughout all plans.)*

---

## Sources

### Primary (HIGH confidence)
- `pkg/profile/inherit.go` — complete source read; all merge functions documented above
- `pkg/profile/types.go` — complete source read; all list fields, pointer fields, non-omitempty bools documented
- `pkg/profile/validate.go` — complete source read; ValidateSchema + ValidateSemantic flow
- `pkg/profile/builtins.go` — IsBuiltin + LoadBuiltin
- `internal/app/cmd/validate.go` — Resolve() + Validate() invocation; all three Extends string-check locations
- `internal/app/cmd/create.go` — two additional Extends check locations (lines 342, 2094)
- `scripts/validate-all-profiles.sh` — hardcoded 22-profile inventory; no base/ awareness
- `profiles/dc34.yaml`, `profiles/learn.v2.yaml`, `profiles/learn.v2.chatty.yaml`, `profiles/learn.v2.polite.yaml` — read in full; overlap quantified
- `pkg/compiler/userdata_phase92_byte_identity_test.go` — golden test mechanics; `generateLearnV2Userdata` calls `Parse()` not `Resolve()`
- `pkg/profile/schemas/sandbox_profile.schema.json` — `extends` is `type: string`; required top-level fields; metadata.properties
- `go.mod` — `github.com/goccy/go-yaml v1.19.2`

### Secondary (MEDIUM confidence)
- goccy/go-yaml v1.x documented behavior: decodes YAML maps to `map[string]interface{}` (inferred from ValidateSchema `yaml.Unmarshal(raw, &doc)` + `json.Marshal(doc)` pattern working in codebase)
- CONTEXT.md — design decisions, open questions, plan breakdown

---

## Metadata

**Confidence breakdown:**
- Current inherit.go behavior: HIGH — full source read, every function documented
- YAML library round-trip faithfulness: HIGH — confirmed `goccy/go-yaml` + existing `map[string]any` usage in ValidateSchema
- Profile overlap quantification: HIGH — profiles read in full and compared
- goccy/go-yaml custom UnmarshalYAML exact signature: MEDIUM — usage is standard but exact two-argument context form requires a compile check in Plan 01
- Fragment nested path resolution: MEDIUM — current `load()` path logic inferred; edge case for fragment-of-fragment not verified

**Research date:** 2026-06-24
**Valid until:** 2026-07-24 (stable domain — profile schema and inherit.go change only in-phase)
