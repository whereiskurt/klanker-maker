# Phase 32: Profile-scoped rsync paths with external file lists and shell wildcards - Research

**Researched:** 2026-03-29
**Domain:** Go profile schema, config, compiler userdata, CLI rsync command
**Confidence:** HIGH

## Summary

Currently, `km rsync save` uses a global list of paths from `km-config.yaml`'s `rsync_paths` key (defaulting to `.claude`, `.bashrc`, `.bash_profile`, `.gitconfig`). This is a platform-level concern rather than a profile concern: every sandbox, regardless of its workload profile, uses the same set of paths. Phase 32 moves this into the per-profile YAML (`spec.execution.rsyncPaths`) and additionally supports two powerful extensions: (1) an external YAML file reference (`spec.execution.rsyncFileList`) that is resolved at `km rsync save` time and (2) shell wildcards in path entries (e.g., `projects/*/config`) that are expanded on the sandbox instance during the tar command.

The migration path is clear. The current `rsync_paths` global config can remain as a backward-compatibility fallback for profiles that don't define `rsyncPaths`. The existing `rsync.go` command and `config.go` defaults need small targeted changes. The JSON schema and `types.go` get new fields on `ExecutionSpec`. The shell command that builds the tar archive is the only runtime change (wildcard expansion in shell is free if the paths are unquoted in the for-loop).

The external file list feature is the most design-interesting part. The referenced YAML file must be loadable from the operator's local machine at `km rsync save` time (not at sandbox boot). This means the operator specifies a local path to a YAML file containing additional paths. The resolver must handle paths relative to the profile file's directory, consistent with how `initScripts` paths are resolved today (see `AddSearchPath` in `km validate` and the `initScripts` upload logic in `create.go`).

**Primary recommendation:** Add `rsyncPaths []string` and `rsyncFileList string` to `ExecutionSpec` and the JSON schema; resolve merged paths in `rsync.go` at save-time using a helper that loads and de-duplicates entries; support shell wildcards by dropping the single-quote escaping around individual paths in the `tar` command, relying on the sandbox shell's glob expansion.

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| Go stdlib `encoding/yaml` (via `gopkg.in/yaml.v3`) | (project-wide) | Parse external YAML file lists | Already used everywhere in the project for YAML parsing |
| Go stdlib `path/filepath` | stdlib | Resolve relative file-list paths | Consistent with how `initScripts` are resolved |
| Go stdlib `os` | stdlib | Read file-list files at save-time | No new dependencies |

### Supporting

None needed — this is purely a schema, config, and CLI change with no new AWS SDK calls.

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| External YAML file resolved at `km rsync save` time | Inline list in profile | External file is the explicit request from the phase description; keeps profile YAML clean for long lists |
| Shell wildcard expansion on the sandbox (unquoted tar args) | Go-side glob expansion via SSM list-directory commands | Shell expansion is zero-friction, works on whatever sandbox OS is installed; Go-side requires extra SSM round trips |
| Fallback to global `rsync_paths` when profile field absent | Hard-require profile field | Backward compat for existing sandboxes created before Phase 32 |

**Installation:** No new packages needed.

## Architecture Patterns

### Recommended Project Structure

Files touched by this phase:

```
pkg/profile/
├── types.go                 # Add rsyncPaths/rsyncFileList to ExecutionSpec
├── schemas/
│   └── sandbox_profile.schema.json  # Add rsyncPaths array + rsyncFileList string fields
internal/app/cmd/
├── rsync.go                 # loadRsyncPaths() helper; use profile paths when available
internal/app/config/
├── config.go                # Keep rsync_paths default; document as fallback
```

### Pattern 1: Profile-level rsync path resolution order

**What:** When `km rsync save` is invoked, paths are resolved in priority order: (1) `spec.execution.rsyncPaths` from the profile YAML, (2) entries from `spec.execution.rsyncFileList` merged in, (3) fallback to `cfg.RsyncPaths` (global config) if neither profile field is set.

**When to use:** Any time `km rsync save` is called. The profile is found via the same S3 metadata lookup that already exists (`.km-profile.yaml` stored at `artifacts/{sandbox-id}/.km-profile.yaml` during `km create`).

**Example:**

```go
// resolveRsyncPaths returns the merged, de-duplicated list of paths to archive.
// Priority: profile rsyncPaths + rsyncFileList entries > global cfg.RsyncPaths fallback.
func resolveRsyncPaths(profile *profile.SandboxProfile, profileDir string, globalFallback []string) ([]string, error) {
    if profile == nil {
        return globalFallback, nil
    }
    exec := profile.Spec.Execution
    if len(exec.RsyncPaths) == 0 && exec.RsyncFileList == "" {
        return globalFallback, nil
    }
    seen := make(map[string]bool)
    var merged []string
    for _, p := range exec.RsyncPaths {
        if !seen[p] { seen[p] = true; merged = append(merged, p) }
    }
    if exec.RsyncFileList != "" {
        extra, err := loadFileList(filepath.Join(profileDir, exec.RsyncFileList))
        if err != nil {
            return nil, fmt.Errorf("rsyncFileList %q: %w", exec.RsyncFileList, err)
        }
        for _, p := range extra {
            if !seen[p] { seen[p] = true; merged = append(merged, p) }
        }
    }
    return merged, nil
}
```

### Pattern 2: Shell wildcard expansion in tar command

**What:** The current implementation quotes every path in single-quotes before passing to the `for` loop on the sandbox. This prevents wildcard expansion. Removing single-quote wrapping and relying on the bash glob expansion enables patterns like `projects/*/config`.

**When to use:** When any path contains `*`, `?`, or `[...]` shell glob characters. A simple `strings.ContainsAny(p, "*?[")` check is enough to decide — or just remove all quoting and let bash handle everything safely.

**Example:**

Current shell command in `rsync.go`:
```go
// OLD — single-quotes prevent wildcard expansion
quotedPaths = append(quotedPaths, fmt.Sprintf("'%s'", p))
```

New approach — no quoting, rely on bash glob-expansion:
```go
// NEW — unquoted; bash expands wildcards in the for loop
// Paths are already validated against a safe pattern at resolve time
// to prevent shell injection (no spaces, no $, no backticks)
```

The tar command shell template becomes:
```bash
for p in .claude .bashrc projects/*/config; do
  [ -e "$p" ] && PATHS="$PATHS $p"
done
```

**Security note:** Since paths come from the profile YAML (operator-controlled, validated) not from user input, the injection risk is the same as other shell commands already injected via `initCommands`. Validate against a safe glob pattern at resolve time.

### Pattern 3: External file list YAML format

**What:** The `rsyncFileList` field points to a YAML file on the operator's local machine. The file contains a top-level `paths` array.

**Example file (`cc-files.yaml`):**
```yaml
paths:
  - .claude
  - .claude.json
  - .config/gh
  - projects/*/config
  - .ssh/known_hosts
```

```go
type rsyncFileListYAML struct {
    Paths []string `yaml:"paths"`
}

func loadFileList(path string) ([]string, error) {
    data, err := os.ReadFile(path)
    if err != nil { return nil, err }
    var fl rsyncFileListYAML
    if err := yaml.Unmarshal(data, &fl); err != nil {
        return nil, fmt.Errorf("parse: %w", err)
    }
    return fl.Paths, nil
}
```

### Pattern 4: Profile retrieval at rsync save time

**What:** `km rsync save` needs access to the profile to read `rsyncPaths`/`rsyncFileList`. The profile is already stored in S3 at `artifacts/{sandbox-id}/.km-profile.yaml` since Phase 4. The same `FetchSandbox` + S3 GetObject path used in other commands can retrieve it.

**When to use:** Every `km rsync save` call. This is a read-only S3 fetch, non-fatal if missing (falls back to global config).

**Example:**
```go
// Fetch stored profile from S3 (best-effort; fall back to global config on any error)
func fetchStoredProfile(ctx context.Context, s3c *s3.Client, bucket, sandboxID string) (*profile.SandboxProfile, string, error) {
    key := fmt.Sprintf("artifacts/%s/.km-profile.yaml", sandboxID)
    out, err := s3c.GetObject(ctx, &s3.GetObjectInput{
        Bucket: &bucket, Key: &key,
    })
    if err != nil { return nil, "", err }
    defer out.Body.Close()
    data, err := io.ReadAll(out.Body)
    if err != nil { return nil, "", err }
    p, err := profile.Parse(data)
    return p, "", err
}
```

The `profileDir` for file-list resolution will be the local working directory (`"."`), since the profile was stored in S3 without its original directory. The operator must ensure `rsyncFileList` path is resolvable from cwd when running `km rsync save`, or use an absolute path.

### Anti-Patterns to Avoid

- **Requiring profile field:** Don't break existing sandboxes that don't have `rsyncPaths` in their profile — fall back to global config.
- **Resolving file list at `km create` time and embedding in S3:** The file list may change between create and save; resolve at save-time for freshness.
- **Shell-quoting wildcard paths:** Single-quoting prevents glob expansion. Validate paths instead of quoting.
- **Changing the tar format or S3 key structure:** The existing `.tar.gz` format and `rsync/{name}.tar.gz` key are fine — no format migration needed.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| YAML parsing of file-list | Custom parser | `gopkg.in/yaml.v3` (already in go.mod) | Correct handling of edge cases; already a project dependency |
| Shell glob expansion | Go filepath.Glob + extra SSM calls | Shell `for p in ...; do` — bash expands naturally | Zero extra round trips; works with all path patterns |
| Path de-duplication | Complex merge logic | Simple `map[string]bool` seen-set | Paths are short strings, O(n) is fine |
| Profile retrieval from S3 | New S3 client setup | Reuse existing `s3.NewFromConfig(awsCfg)` pattern from `rsync.go` | Already fully wired in the save command |

**Key insight:** All the hard parts (S3, SSM, profile parsing, config loading) are already built. This phase is schema + config + a 30-line helper function + un-quoting path strings.

## Common Pitfalls

### Pitfall 1: Wildcard paths cause shell injection if paths come from untrusted sources

**What goes wrong:** If wildcard paths were ever sourced from untrusted input (not operator-controlled YAML), an entry like `$(rm -rf ~)` would execute.

**Why it happens:** Removing single-quote escaping of path strings means they are interpreted by bash.

**How to avoid:** Validate each path entry against `^[a-zA-Z0-9_./*?-]+$` at resolve time. Reject paths containing spaces, `$`, backticks, `;`, `|`, `&`, `>`, `<`. The profile YAML is already operator-controlled (same trust level as `initCommands`), so this is defense-in-depth.

**Warning signs:** Any path containing whitespace or shell metacharacters other than glob chars.

### Pitfall 2: File-list path resolution breaks when `km rsync save` is run from a different directory than `km create`

**What goes wrong:** `rsyncFileList: cc-files.yaml` works from the project root but fails when the operator runs `km rsync save` from a different working directory.

**Why it happens:** The profile stored in S3 doesn't carry its original directory context.

**How to avoid:** Document that `rsyncFileList` is resolved from the operator's current working directory at save time. Alternatively, support absolute paths. Consider logging the resolved path so operators can diagnose.

**Warning signs:** "rsyncFileList not found" errors when running from a different directory.

### Pitfall 3: Profile not in S3 for older sandboxes (pre-Phase 4)

**What goes wrong:** Sandboxes created before Phase 4 don't have `.km-profile.yaml` in S3. Attempting to fetch and parse the profile fails.

**Why it happens:** Phase 4 introduced storing the profile in S3. Older sandboxes don't have it.

**How to avoid:** Wrap S3 profile fetch in a best-effort block. If fetch fails for any reason (not found, parse error), fall back silently to `cfg.RsyncPaths`. Log a debug message.

**Warning signs:** Unexpected fallback to global rsync paths for new sandboxes.

### Pitfall 4: JSON schema `additionalProperties: false` on `execution` blocks new fields

**What goes wrong:** Adding `rsyncPaths` and `rsyncFileList` to `types.go` without adding them to `sandbox_profile.schema.json` causes `km validate` to reject profiles that use them.

**Why it happens:** The `execution` object in the schema has `"additionalProperties": false`.

**How to avoid:** Add both fields to the `execution` properties block in the schema before writing any profile that uses them. Run `go test ./pkg/profile/...` to verify.

**Warning signs:** `km validate` returning "Additional property rsyncPaths is not allowed."

### Pitfall 5: Profile inheritance merges rsyncPaths additively vs. override semantics

**What goes wrong:** If a child profile overrides `rsyncPaths`, the existing `inherit.go` merge logic may or may not merge arrays additively (the project decision for inheritances is "child overrides parent, no additive merge on allowlists").

**Why it happens:** Array fields in the profile have "override" semantics — a child's list fully replaces the parent's, not appends to it.

**How to avoid:** Treat `rsyncPaths` the same as `allowedDNSSuffixes` — it is a list allowlist, so child overrides parent. No special inheritance logic needed; the existing yaml.v3 unmarshal + merge already handles this correctly for lists.

**Warning signs:** Unexpected path inclusion from a parent profile when a child defines `rsyncPaths`.

## Code Examples

Verified patterns from existing source:

### Adding a field to ExecutionSpec (types.go pattern)

```go
// Source: pkg/profile/types.go — existing ExecutionSpec fields as template
type ExecutionSpec struct {
    Shell        string            `yaml:"shell"`
    WorkingDir   string            `yaml:"workingDir"`
    Env          map[string]string `yaml:"env,omitempty"`
    InitCommands []string          `yaml:"initCommands,omitempty"`
    InitScripts  []string          `yaml:"initScripts,omitempty"`
    Rsync        string            `yaml:"rsync,omitempty"`
    // NEW in Phase 32:
    RsyncPaths   []string          `yaml:"rsyncPaths,omitempty"`
    RsyncFileList string           `yaml:"rsyncFileList,omitempty"`
}
```

### Adding array field to JSON schema (sandbox_profile.schema.json pattern)

```json
// Source: existing initScripts definition in sandbox_profile.schema.json (line 228-232)
"rsyncPaths": {
  "type": "array",
  "items": { "type": "string" },
  "description": "Paths relative to sandbox user $HOME to include in rsync snapshots. Shell wildcards supported (e.g. projects/*/config)."
},
"rsyncFileList": {
  "type": "string",
  "description": "Path to a local YAML file containing additional rsync paths (resolved from operator cwd at km rsync save time)."
}
```

### Config fallback pattern (config.go existing)

```go
// Source: internal/app/config/config.go line 149
v.SetDefault("rsync_paths", []string{".claude", ".bashrc", ".bash_profile", ".gitconfig"})
// rsync_paths remains as global fallback; profiles with rsyncPaths/rsyncFileList take precedence
```

### Existing path-quoting in rsync.go (the thing to change)

```go
// Source: internal/app/cmd/rsync.go lines 85-88 — current quoting (to be removed for wildcard support)
var quotedPaths []string
for _, p := range paths {
    quotedPaths = append(quotedPaths, fmt.Sprintf("'%s'", p))  // REMOVE single-quote wrapping
}
```

New version — no quoting, validate instead:

```go
// Validate paths are safe for unquoted shell expansion
func validateRsyncPath(p string) error {
    if matched, _ := regexp.MatchString(`^[a-zA-Z0-9_./*?-]+$`, p); !matched {
        return fmt.Errorf("unsafe rsync path %q: only alphanumeric, dot, slash, wildcard chars allowed", p)
    }
    return nil
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Global `rsync_paths` in `km-config.yaml` | Profile-scoped `rsyncPaths` + `rsyncFileList` in `spec.execution` | Phase 32 | Per-workload path customization without config change |
| Fixed literal paths only | Shell wildcard support (`projects/*/config`) | Phase 32 | Captures dynamic directory structures |
| Quoted path strings (no wildcards) | Unquoted paths with validation | Phase 32 | Enables glob expansion on the sandbox |

**Deprecated/outdated:**
- Global `rsync_paths` in `km-config.yaml`: Remains as fallback but no longer the primary mechanism. New profiles should use `spec.execution.rsyncPaths`. Documentation and `km-config.yaml.example` should note it as a legacy default.

## Open Questions

1. **Where is `rsyncFileList` resolved when running `km rsync save`?**
   - What we know: The profile is fetched from S3; the original directory is not stored in S3 metadata.
   - What's unclear: Should the path be relative to cwd, relative to some configured `profile_search_paths`, or absolute-only?
   - Recommendation: Resolve from operator's current working directory. Document this clearly. This is consistent with how `km validate` uses the file's directory (passed explicitly) and how operators already run commands from the repo root.

2. **Should `rsyncFileList` also be uploaded to S3 at `km create` time (alongside `.km-profile.yaml`)?**
   - What we know: Currently nothing about the external file list is stored in the sandbox's S3 artifact path.
   - What's unclear: If the file list changes after sandbox creation, which version should save use?
   - Recommendation: Resolve the file list at `km rsync save` time from the local filesystem. This is simpler, more composable, and avoids the question of S3 staleness. The file list is a local operator convenience tool, not a sandbox artifact.

3. **Should wildcard expansion work for `km rsync load` / restore at boot time?**
   - What we know: The restore path (userdata.go template) uses `tar xzf` — extraction automatically restores whatever paths are in the archive; no wildcards needed on load.
   - What's unclear: Nothing — this is not an issue. Only the save step needs wildcard support.
   - Recommendation: No changes to the restore path.

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) + testify |
| Config file | none (standard `go test`) |
| Quick run command | `go test ./pkg/profile/... ./internal/app/cmd/... -run TestRsync -v` |
| Full suite command | `go test ./...` |

### Phase Requirements to Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| RSYNC-01 | `rsyncPaths` in profile YAML is parsed correctly into `ExecutionSpec` | unit | `go test ./pkg/profile/... -run TestRsyncPaths -v` | Wave 0 |
| RSYNC-02 | `rsyncFileList` external YAML is loaded and merged with `rsyncPaths` | unit | `go test ./internal/app/cmd/... -run TestLoadFileList -v` | Wave 0 |
| RSYNC-03 | Wildcard paths pass validation; shell-injecting paths rejected | unit | `go test ./internal/app/cmd/... -run TestValidateRsyncPath -v` | Wave 0 |
| RSYNC-04 | Profile without `rsyncPaths` falls back to global `cfg.RsyncPaths` | unit | `go test ./internal/app/cmd/... -run TestRsyncPathFallback -v` | Wave 0 |
| RSYNC-05 | JSON schema validates `rsyncPaths` array and `rsyncFileList` string | unit | `go test ./pkg/profile/... -run TestRsyncSchema -v` | Wave 0 |
| RSYNC-06 | `km rsync save` generates tar command without path quoting when wildcards present | unit | `go test ./internal/app/cmd/... -run TestRsyncSaveCmd -v` | Wave 0 |

### Sampling Rate

- **Per task commit:** `go test ./pkg/profile/... ./internal/app/cmd/...`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `internal/app/cmd/rsync_test.go` — covers RSYNC-02, RSYNC-03, RSYNC-04, RSYNC-06 (no existing rsync_test.go)
- [ ] `pkg/profile/types_test.go` — add `TestRsyncPaths` to existing file (covers RSYNC-01, RSYNC-05)

*(The `rsync.go` command has no existing test file — this is a Wave 0 gap. The profile type tests exist but need new test cases.)*

## Sources

### Primary (HIGH confidence)

- Direct source read: `internal/app/cmd/rsync.go` — complete implementation of `km rsync save/load/list/view/delete`
- Direct source read: `pkg/profile/types.go` — `ExecutionSpec` struct definition
- Direct source read: `pkg/profile/schemas/sandbox_profile.schema.json` — `execution` object schema (lines 201-238)
- Direct source read: `internal/app/config/config.go` — `RsyncPaths` field, `rsync_paths` default, viper wiring
- Direct source read: `pkg/compiler/userdata.go` — rsync restore section in userdata template (lines 615-627), `userDataParams` struct

### Secondary (MEDIUM confidence)

- Inferred from STATE.md roadmap note: "move rsync path lists from global config into per-profile YAML with external file list references (e.g. `rsyncFileDetails: 'cc-files.yaml'`) and shell wildcard support; remove global rsync_paths" — defines the feature scope

### Tertiary (LOW confidence)

- Shell wildcard expansion behavior in bash `for p in ...; do` loop: standard POSIX behavior, HIGH confidence from general knowledge. Not verified against specific sandbox AMI bash version but universally consistent.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — no new dependencies; pure refactor of existing code
- Architecture: HIGH — patterns are directly from existing code (initScripts, profile parsing, S3 profile fetch)
- Pitfalls: HIGH — derived from existing code review (schema `additionalProperties: false`, inheritance semantics, shell injection)

**Research date:** 2026-03-29
**Valid until:** 2026-06-29 (stable codebase; no external dependencies)
