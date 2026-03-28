# Phase 29: Configurable Sandbox ID Prefix - Research

**Researched:** 2026-03-28
**Domain:** Go profile schema extension, ID generation, regex validation, backwards compatibility
**Confidence:** HIGH

## Summary

Phase 29 is a surgical refactor — the sandbox ID prefix is currently hardcoded to `"sb"` in exactly two production code locations (generation and strict validation) plus one Lambda "best-effort" repair location. Everything else in the system already treats the sandbox ID as an opaque string parameter. The work decomposes cleanly into four logical layers:

1. **Schema layer** — add `metadata.prefix` field to `Metadata` struct and JSON Schema with pattern validation `^[a-z][a-z0-9]{0,11}$`.
2. **Generation layer** — change `GenerateSandboxID()` signature to accept a prefix string, default to `"sb"` when empty.
3. **Validation layer** — replace the `^sb-[a-f0-9]{8}$` strict regex in `destroy.go` and the `strings.HasPrefix(ref, "sb-")` check in `sandbox_ref.go` with a generalized pattern `^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$`.
4. **Email handler repair** — remove the hard-coded `"sb-"` prefix prepend in `cmd/email-create-handler/main.go:246-247` since the loose `extractSandboxID()` already returns whatever prefix was in the subject.

No Terraform modules, no AWS resource naming patterns, no SSM/S3/CloudWatch paths, and no compiler HCL templates require changes — they all receive the sandbox ID as a pre-computed string and embed it verbatim.

**Primary recommendation:** Extend `GenerateSandboxID` to accept a prefix, wire the prefix from `resolvedProfile.Metadata.Prefix` in `runCreate`, and generalize the two rigid validation patterns. Total production code delta is under 50 lines across five files.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| PREFIX-01 | Profile schema supports optional `metadata.prefix` field with validation `^[a-z][a-z0-9]{0,11}$` | Add `prefix` to `Metadata` struct and JSON Schema `metadata.properties` block; use `pattern` keyword with the specified regex |
| PREFIX-02 | `GenerateSandboxID()` accepts a prefix parameter — generates `{prefix}-{8 hex}` IDs | Change signature to `GenerateSandboxID(prefix string) string`; default to `"sb"` when prefix is `""` |
| PREFIX-03 | All sandbox ID validation/matching patterns accept any valid prefix, not just `sb-` | Replace `^sb-[a-f0-9]{8}$` in `destroy.go` and `strings.HasPrefix(ref, "sb-")` in `sandbox_ref.go` with generalized pattern; update email-handler prefix-repair logic |
| PREFIX-04 | Compiler, CLI, and Lambda code use sandbox ID as-is — no component hardcodes the `sb-` prefix | Confirm (already true for most paths); remove the one Lambda repair-prepend in email-create-handler |
| PREFIX-05 | Backwards compatible — profiles without `metadata.prefix` default to `sb` | In `GenerateSandboxID`, treat `prefix == ""` as `"sb"`; schema field is `omitempty` so existing profiles parse unchanged |
</phase_requirements>

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `encoding/json` (stdlib) | Go 1.25.5 | JSON Schema updates | Already used in the schema pipeline |
| `regexp` (stdlib) | Go 1.25.5 | Generalized ID pattern | Already used in destroy.go and email handler |
| `github.com/goccy/go-yaml` | already in go.mod | Profile YAML parsing | Already the project's YAML library |
| `github.com/santhosh-tekuri/jsonschema/v6` | already in go.mod | JSON Schema validation | Already used in `pkg/profile/validate.go` |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `strings` (stdlib) | Go 1.25.5 | Prefix detection and manipulation | Already used in sandbox_ref.go |

No new dependencies are required.

**Installation:**
```bash
# No new packages needed
```

## Architecture Patterns

### Recommended Project Structure

No new files or directories required. All changes are within existing files:

```
pkg/
├── compiler/
│   └── sandbox_id.go          # GenerateSandboxID signature change
├── profile/
│   ├── types.go               # Metadata.Prefix field
│   └── schemas/
│       └── sandbox_profile.schema.json  # metadata.properties.prefix
internal/app/cmd/
├── destroy.go                  # sandboxIDPattern generalization
└── sandbox_ref.go              # HasPrefix("sb-") → generalized check
cmd/
└── email-create-handler/
    └── main.go                 # Remove sb- repair prepend, update sandboxIDPattern
```

### Pattern 1: Parameterized ID Generation with Default

**What:** `GenerateSandboxID` accepts the prefix from the profile's `metadata.prefix`; defaults to `"sb"` when empty.
**When to use:** Anytime a new sandbox is created via `km create` or the create-handler Lambda.
**Example:**
```go
// pkg/compiler/sandbox_id.go
// GenerateSandboxID returns a unique sandbox identifier in the form {prefix}-XXXXXXXX.
// If prefix is empty, "sb" is used for backwards compatibility.
// Format matches: ^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$
func GenerateSandboxID(prefix string) string {
    if prefix == "" {
        prefix = "sb"
    }
    id := uuid.New().String()
    hex := strings.ReplaceAll(id, "-", "")
    return prefix + "-" + hex[:8]
}
```

Call site in `internal/app/cmd/create.go` becomes:
```go
// Step 4: Generate sandbox ID (or use override from create-handler Lambda)
sandboxID := sandboxIDOverride
if sandboxID == "" {
    sandboxID = compiler.GenerateSandboxID(resolvedProfile.Metadata.Prefix)
}
```

### Pattern 2: Generalized Validation Regex

**What:** Replace the two hardcoded `^sb-[a-f0-9]{8}$` / `HasPrefix("sb-")` checks with a pattern that accepts any valid prefix.
**When to use:** In `destroy.go` (strict format gating) and `sandbox_ref.go` (prefix detection for numeric-vs-ID disambiguation).

```go
// internal/app/cmd/destroy.go
// sandboxIDPattern matches valid sandbox IDs: {prefix}-[a-f0-9]{8}
// where prefix is ^[a-z][a-z0-9]{0,11}$
var sandboxIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$`)
```

```go
// internal/app/cmd/sandbox_ref.go
// A sandbox ID has format {prefix}-{8hex}. Detect by checking for a dash
// followed by exactly 8 hex chars at the end of the string.
var sandboxIDLike = regexp.MustCompile(`^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$`)

func ResolveSandboxID(...) (string, error) {
    if sandboxIDLike.MatchString(ref) {
        return ref, nil
    }
    // ... numeric fallback unchanged
}
```

### Pattern 3: Schema Field Addition

**What:** Add `prefix` to the `Metadata` object in both `types.go` and `sandbox_profile.schema.json`.
**When to use:** Profile parsing and JSON Schema validation.

```go
// pkg/profile/types.go
type Metadata struct {
    Name   string            `yaml:"name"`
    Labels map[string]string `yaml:"labels,omitempty"`
    Prefix string            `yaml:"prefix,omitempty"`
}
```

```json
// pkg/profile/schemas/sandbox_profile.schema.json — inside metadata.properties
"prefix": {
  "type": "string",
  "pattern": "^[a-z][a-z0-9]{0,11}$",
  "description": "Optional sandbox ID prefix (e.g. 'claude', 'build', 'research'). Defaults to 'sb' when omitted."
}
```

The `metadata` object already has `"additionalProperties": false`, so no other schema changes are needed — the field just needs to be declared.

### Pattern 4: Email Handler — Remove Prefix Repair

**What:** The email-create-handler reconstructs a sandbox ID from an email subject. It currently extracts the hex portion with a loose pattern and then re-prepends `"sb-"`. With arbitrary prefixes, this repair is wrong.
**When to use:** This fix applies to `cmd/email-create-handler/main.go`.

Current (broken for custom prefixes):
```go
// extractSandboxID returns only the hex portion (m[1] from the capture group)
var sandboxIDPattern = regexp.MustCompile(`(?i)\b(?:sb-)?([0-9a-f]{8,16})\b`)

// Then at line 246:
if !strings.HasPrefix(sandboxID, "sb-") {
    sandboxID = "sb-" + sandboxID  // WRONG for claude-abc123de
}
```

Fixed approach — update the extraction pattern to capture the full ID including prefix:
```go
// Matches {prefix}-{8hex} where prefix is 1-12 lowercase alphanumeric chars.
// Also matches bare hex strings for legacy subjects.
var sandboxIDPattern = regexp.MustCompile(`(?i)\b([a-z][a-z0-9]{0,11}-[0-9a-f]{8})\b`)

// extractSandboxID now returns the full ID including prefix.
// The prefix-repair block (HasPrefix + prepend) is removed entirely.
```

### Anti-Patterns to Avoid

- **Trying to parse the prefix back out of an existing ID string:** The prefix is stored once in the profile and embedded in the ID at generation time. The system never needs to re-derive the prefix from a live ID — the full ID is the key everywhere.
- **Validating prefix in `ValidateSemantic`:** The prefix constraint is already expressible as a JSON Schema `pattern`, which is where it belongs. Duplicating it in `ValidateSemantic` creates two sources of truth.
- **Changing the `metadata.name` validation or any other Metadata field:** The `prefix` field is purely additive.
- **Updating Terraform modules or HCL templates:** They receive the sandbox ID as a pre-formed string and embed it verbatim. No changes needed there.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Prefix format validation | Custom Go validator | JSON Schema `pattern` keyword | Already the project's validation approach; keeps rules in one place |
| Uniqueness of generated IDs | Collision tracking | UUID v4 randomness (128 bits, 8 hex = 32 bits used) | Collision probability is negligible for typical sandbox counts; matches existing approach |

**Key insight:** The system already treats the sandbox ID as an opaque string in 95% of code paths. The only places that know the internal structure are the generation and validation sites — exactly where this change needs to be made.

## Common Pitfalls

### Pitfall 1: `sandboxIDPattern` in `destroy.go` only guards after `ResolveSandboxID`
**What goes wrong:** The validation in `destroy.go` at line 120 only runs for the local-destroy path (`runDestroy`). The `ResolveSandboxID` call at line 70 runs first and uses `strings.HasPrefix(ref, "sb-")` in `sandbox_ref.go`. If only one of the two checks is updated, commands like `km destroy claude-abc123de` will be rejected by the still-hardcoded check.
**How to avoid:** Update both files in the same commit. The planner should treat these as one atomic task, not two separate tasks.
**Warning signs:** Test `km destroy claude-abc123de --yes` fails with "invalid sandbox reference" rather than the destroy flow starting.

### Pitfall 2: The JSON Schema `metadata` block has `additionalProperties: false`
**What goes wrong:** Adding `prefix` to `types.go` without adding it to the JSON Schema will cause all profiles with `metadata.prefix` to fail schema validation with "additional property not allowed".
**How to avoid:** The `prefix` field must be added to both `types.go` (Go struct) and `sandbox_profile.schema.json` (JSON Schema properties block) as a single paired change.
**Warning signs:** `km validate myprofile.yaml` returns a schema error about `metadata.prefix`.

### Pitfall 3: `schema.go` caches the compiled schema via `sync.Once`
**What goes wrong:** The embedded schema is compiled once and cached in `compiledSchema`. After changing `sandbox_profile.schema.json`, tests that share process state (e.g. multiple sub-tests) all use the cached schema. In practice this is fine for a cold build, but is worth knowing.
**How to avoid:** No action needed — the schema is embedded at build time via `//go:embed`. Changes to the JSON file are picked up on the next `go build` / `go test`.

### Pitfall 4: `extractSandboxID` in email-create-handler captures only the hex portion
**What goes wrong:** The current regex capture group `([0-9a-f]{8,16})` returns only the hex digits, then re-prepends `"sb-"`. With a `claude-` prefix in the subject, extractSandboxID returns `abc123de` and the repair prepends `sb-` to produce `sb-abc123de` — the wrong ID.
**How to avoid:** Change the regex to capture the full `{prefix}-{8hex}` form. The existing test in `cmd/email-create-handler/main_test.go` must be updated to confirm the full ID is returned.
**Warning signs:** Email status command for a `claude-*` sandbox returns "Sandbox not found" because it's looking up the wrong ID.

### Pitfall 5: 38 test files use hardcoded `sb-*` IDs — but most don't need to change
**What goes wrong:** A naive "update all tests" sweep is unnecessary and risky. Most test fixtures use `"sb-abc12345"` as a stable test value; they remain valid because `sb` is a valid prefix under the new scheme.
**How to avoid:** Only update tests that directly exercise the prefix behaviour:
  - `compiler_test.go:TestGenerateSandboxID` — must be updated to call `GenerateSandboxID("")` and `GenerateSandboxID("claude")` and verify both forms.
  - `destroy_test.go` — must add a test asserting `claude-abc123de` passes the new pattern.
  - `sandbox_ref_test.go` — must add a test asserting `claude-abc123de` is recognized as an ID, not a number.
  - Email-handler tests for `extractSandboxID` — must verify full ID is returned.

## Code Examples

Verified patterns from the actual codebase:

### Current GenerateSandboxID (to be changed)
```go
// pkg/compiler/sandbox_id.go:15-21 — current form
func GenerateSandboxID() string {
    id := uuid.New().String()
    hex := strings.ReplaceAll(id, "-", "")
    return "sb-" + hex[:8]
}
```

### Current strict validation (to be generalized)
```go
// internal/app/cmd/destroy.go:35
var sandboxIDPattern = regexp.MustCompile(`^sb-[a-f0-9]{8}$`)
```

### Current prefix detection (to be generalized)
```go
// internal/app/cmd/sandbox_ref.go:20
if strings.HasPrefix(ref, "sb-") {
    return ref, nil
}
```

### Current email handler repair (to be removed)
```go
// cmd/email-create-handler/main.go:246-247
if !strings.HasPrefix(sandboxID, "sb-") {
    sandboxID = "sb-" + sandboxID
}
```

### Metadata struct (to be extended)
```go
// pkg/profile/types.go:21-24 — current form
type Metadata struct {
    Name   string            `yaml:"name"`
    Labels map[string]string `yaml:"labels,omitempty"`
}
```

### JSON Schema metadata block (partial, to be extended)
```json
// pkg/profile/schemas/sandbox_profile.schema.json:20-36 — current metadata block
"metadata": {
  "type": "object",
  "required": ["name"],
  "additionalProperties": false,
  "properties": {
    "name": { "type": "string", "minLength": 1 },
    "labels": { "type": "object", "additionalProperties": { "type": "string" } }
  }
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Hardcoded `"sb"` prefix | Configurable prefix per profile | Phase 29 | Operators can namespace sandbox IDs by workload type |
| `^sb-[a-f0-9]{8}$` validation | `^[a-z][a-z0-9]{0,11}-[a-f0-9]{8}$` validation | Phase 29 | All CLI commands accept any valid-prefix ID |
| Zero-arg `GenerateSandboxID()` | `GenerateSandboxID(prefix string)` | Phase 29 | Caller provides prefix, empty string defaults to `"sb"` |

**No deprecated patterns from the wider Go ecosystem are involved** — this is entirely an internal API change.

## Open Questions

1. **`--sandbox-id` override flag in `km create`**
   - What we know: `create.go` accepts `--sandbox-id` to override the generated ID (used by the create-handler Lambda). This flag bypasses `GenerateSandboxID` entirely.
   - What's unclear: Should the override value be validated against the new generalized pattern before use?
   - Recommendation: Yes — add a `IsValidSandboxID(id string) bool` helper in `pkg/compiler/sandbox_id.go` that validates the generalized pattern, and use it to gate the override. This is a one-liner check and prevents garbage IDs from reaching Terraform.

2. **Email-create-handler subject format change**
   - What we know: The email status command instructs users to write `Subject: status sb-<id>`. With arbitrary prefixes, this instruction becomes `status <sandbox-id>`.
   - What's unclear: Whether to update the handler's help text and SES reply template.
   - Recommendation: Update the help string at line 313 from `"status sb-abc123de"` to `"status <sandbox-id>"`. Low-risk cosmetic change.

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing stdlib (`testing` package) |
| Config file | none — `go test ./...` from repo root |
| Quick run command | `go test ./pkg/compiler/... ./pkg/profile/... ./internal/app/cmd/... ./cmd/email-create-handler/... -run TestPrefix -v` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| PREFIX-01 | `metadata.prefix` with valid value passes schema validation | unit | `go test ./pkg/profile/... -run TestValidateSchema -v` | Wave 0 — extend existing `validate_test.go` |
| PREFIX-01 | `metadata.prefix` with invalid value (e.g. `"0bad"`, `"toolongprefix0"`) fails schema validation | unit | `go test ./pkg/profile/... -run TestValidateSchema -v` | Wave 0 |
| PREFIX-01 | Profile without `metadata.prefix` passes schema validation unchanged | unit | `go test ./pkg/profile/... -run TestValidateSchema -v` | Existing tests already cover this |
| PREFIX-02 | `GenerateSandboxID("claude")` returns `"claude-[a-f0-9]{8}"` | unit | `go test ./pkg/compiler/... -run TestGenerateSandboxID -v` | Wave 0 — update existing `compiler_test.go` |
| PREFIX-02 | `GenerateSandboxID("")` returns `"sb-[a-f0-9]{8}"` (backwards compat) | unit | `go test ./pkg/compiler/... -run TestGenerateSandboxID -v` | Wave 0 |
| PREFIX-03 | `destroy` accepts `claude-abc123de` as valid sandbox ID | unit | `go test ./internal/app/cmd/... -run TestDestroy -v` | Wave 0 — extend `destroy_test.go` |
| PREFIX-03 | `ResolveSandboxID` recognizes `claude-abc123de` as ID (not numeric ref) | unit | `go test ./internal/app/cmd/... -run TestResolveSandboxID -v` | Wave 0 — extend `sandbox_ref_test.go` or create |
| PREFIX-03 | Email handler `extractSandboxID("status claude-abc123de")` returns `"claude-abc123de"` | unit | `go test ./cmd/email-create-handler/... -run TestExtractSandboxID -v` | Wave 0 — extend `main_test.go` |
| PREFIX-04 | `km create` with profile containing `metadata.prefix: claude` generates `claude-*` ID | integration | manual / `go test ./internal/app/cmd/... -run TestCreate -v` | Existing create tests; Wave 0 for prefix case |
| PREFIX-05 | Profile without `metadata.prefix` field creates `sb-*` sandbox ID | unit | `go test ./pkg/compiler/... -run TestGenerateSandboxID -v` | Covered by PREFIX-02 empty-string test |

### Sampling Rate
- **Per task commit:** `go test ./pkg/compiler/... ./pkg/profile/... ./internal/app/cmd/... ./cmd/email-create-handler/...`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/compiler/compiler_test.go` — update `TestGenerateSandboxID` to cover both `""` (default `sb`) and custom prefix cases
- [ ] `pkg/profile/validate_test.go` — add test cases for `metadata.prefix` valid/invalid values
- [ ] `internal/app/cmd/destroy_test.go` — add test asserting generalized pattern accepts `claude-abc123de`
- [ ] `internal/app/cmd/sandbox_ref_test.go` (or existing) — add test for `ResolveSandboxID` with custom-prefix ID
- [ ] `cmd/email-create-handler/main_test.go` — update/add tests for `extractSandboxID` returning full ID

## Sources

### Primary (HIGH confidence)
- Direct source reading: `pkg/compiler/sandbox_id.go` — generation logic
- Direct source reading: `internal/app/cmd/destroy.go:35` — strict validation regex
- Direct source reading: `internal/app/cmd/sandbox_ref.go:20` — prefix detection
- Direct source reading: `cmd/email-create-handler/main.go:112,246-247` — loose pattern and repair
- Direct source reading: `pkg/profile/types.go` — `Metadata` struct
- Direct source reading: `pkg/profile/schemas/sandbox_profile.schema.json` — schema `metadata` block
- Direct source reading: `internal/app/cmd/create.go:165-167` — `GenerateSandboxID()` call site
- Direct source reading: `pkg/profile/validate.go` — validation pipeline (schema + semantic layers)
- Direct source reading: `pkg/compiler/compiler_test.go:40-56` — existing `TestGenerateSandboxID`

### Secondary (MEDIUM confidence)
- Grep survey of all 52 files containing `sb-`: confirms no other production files hardcode the prefix string beyond the 5 identified touch points

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — no new dependencies; all changes use stdlib or already-imported packages
- Architecture: HIGH — touch points identified by direct source reading, not inference
- Pitfalls: HIGH — each pitfall derived from reading the exact code pattern that would break

**Research date:** 2026-03-28
**Valid until:** Until any of the five identified files change (stable; no external dependencies)
