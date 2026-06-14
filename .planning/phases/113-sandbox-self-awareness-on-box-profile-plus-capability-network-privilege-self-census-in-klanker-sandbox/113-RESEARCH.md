# Phase 113: Sandbox Self-Awareness — Research

**Researched:** 2026-06-14
**Domain:** pkg/compiler/userdata.go template-data threading + skill content authoring
**Confidence:** HIGH

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Write the profile to the box at `/opt/km/.km-profile.yaml` (chosen over S3-read IAM grant and over deferring profile reflection). Works even when egress is fully locked down.
- Source the identical rendered string already uploaded to S3 (`internal/app/cmd/create.go` `remoteProfileYAML`). Thread through the userdata template-data struct as a new `ProfileYAML` field.
- **Redaction: default write verbatim.** Spec-review checkpoint: if review identifies an embeddable-secret field, redact `spec.execution.configFiles` bodies only (keep keys); leave all other fields intact.
- Network probing: passive + exactly two safe-active curls (one allowed host, one known-blocked host).
- Self-contained bash block in the skill — no new binary.
- `sudo -n true` probe for privilege; cross-check against `spec.execution.privileged`.
- Graceful degradation on pre-Phase-113 sandbox (`/opt/km/.km-profile.yaml` absent): fall back to env-var census + live probes only.
- No `klanker:slack` content change (Part 2 cross-links it only).

### Claude's Discretion
- Exact bash idioms for the skill's six-section census.
- Sentinel string chosen for the heredoc (anything that won't appear in a profile YAML).
- Placement of the profile-write block within the userdata template (adjacent to section 2.8/2.9).
- New boolean gate field or direct `{{- if .ProfileYAML }}` in the template.

### Deferred Ideas (OUT OF SCOPE)
- A new sidecar binary (`km-whoami`).
- An IAM grant for sandbox role to read `artifacts/{id}/*` from S3.
- Any change to `klanker:slack` content.
- Full active network mapping (traceroute / port-scan).
</user_constraints>

---

## Summary

Phase 113 has two deliverables that must be coordinated. First, `pkg/compiler/userdata.go` must gain a new `ProfileYAML string` field on `userDataParams` (line ~4732) and a template block that writes the rendered profile to `/opt/km/.km-profile.yaml` at boot. The value is sourced from the **already-computed** profile YAML string that the create flow produces independently of the compiler call. This means `generateUserData()` must accept the profile YAML as an input — the cleanest path is adding it as a new field on `userDataParams` populated during `generateUserData()`. Second, `skills/sandbox/SKILL.md` is rewritten from its current 106-line "detect-and-email" scope into a six-section structured self-census (A identity, B capabilities, C network, D privilege, E Slack readiness, F posture summary) that references the now-real profile file and degrades gracefully when the file is absent.

The key architectural fact: `generateUserData(p, sandboxID, ...)` already has access to `*profile.SandboxProfile p` which can be serialized to YAML inside the function (using `gopkg.in/yaml.v3` or `github.com/goccy/go-yaml` — already in `go.mod`) without any signature change. This is the cleanest path: marshal `p` to YAML inside `generateUserData`, set `params.ProfileYAML = string(yamlBytes)`. This guarantees the on-box YAML comes from the same `*profile.SandboxProfile` used to drive all other template fields — no divergence possible. The `remoteProfileYAML` in `create.go` is an alternative source but is not available inside the compiler package.

**Primary recommendation:** Add `ProfileYAML string` to `userDataParams` struct (line 4732), marshal `p` to YAML inside `generateUserData()` (after all profile mutations complete, around line 5690), set `params.ProfileYAML`, and add a `{{ if .ProfileYAML }}...{{ end }}` template block adjacent to section 2.9 (line ~450). Use sentinel `KM_PROFILE_EOF` — it will never appear in a well-formed YAML profile. Update the three existing golden files and existing byte-identity tests, and add one new unit test asserting the profile round-trips.

---

## Research Question 1: `remoteProfileYAML` Threading and the Two Create Paths

### Local `km create` path (line ~329-783)

1. `raw, _ = os.ReadFile(profilePath)` (line 329) reads raw YAML bytes.
2. `profile.Parse(raw)` → `resolvedProfile` (line 335).
3. `compiler.Compile(resolvedProfile, sandboxID, ...)` (line 737) → `artifacts` (which contains `artifacts.UserData`). Inside `compileEC2`, `generateUserData(p, sandboxID, ...)` is called. The `p` argument is `resolvedProfile`.
4. Separately, `profileYAMLForUpload(resolvedProfile, raw, noBedrock)` (line 965, around `artifacts/{id}/.km-profile.yaml` S3 upload) produces `remoteProfileYAML`. This string is NOT passed to the compiler. It is the **raw file bytes** (with optional Bedrock-stripping).

### Remote pre-dispatch path (`runCreateRemote`, line ~2067-2372)

1. `raw, _ = os.ReadFile(profilePath)` (line 2081).
2. `compiler.Compile(resolvedProfile, sandboxID, ...)` (line 2259) → `artifacts`. Again `generateUserData(p, ...)` has access to `p`.
3. `remoteProfileYAML` is produced (lines 2290-2300) for upload to S3 under `remote-create/{id}/.km-profile.yaml`.
4. The create-handler Lambda downloads this profile from S3, writes it to `/tmp/{id}.yaml`, then calls `km create /tmp/{id}.yaml` as a subprocess — which re-enters the **local path** above.

### Key finding: cleanest threading approach

The `generateUserData` function already receives `*profile.SandboxProfile p` — every field needed is already there. The cleanest solution is to marshal `p` to YAML inside `generateUserData()` itself:

```go
// Inside generateUserData(), after all param population is complete (~line 5690):
profileYAMLBytes, marshalErr := yaml.Marshal(p)
if marshalErr == nil {
    params.ProfileYAML = string(profileYAMLBytes)
} else {
    // Non-fatal: log and leave ProfileYAML empty; the template block gates on non-empty
    // so the file simply won't be written (graceful degradation at compile time)
}
```

This approach:
- Requires NO signature change to `generateUserData()` or `compiler.Compile()`
- Sources the profile from the same struct driving all other template fields — guaranteed alignment
- Works on BOTH the local path and remote path (the subprocess `km create` in the Lambda re-enters the local path with the downloaded profile)
- Avoids any `remoteProfileYAML` threading complexity

Import already present: `github.com/goccy/go-yaml` is used throughout `create.go`. In `pkg/compiler/userdata.go`, standard `gopkg.in/yaml.v3` or `github.com/goccy/go-yaml` can be used — check existing imports at the top of the file.

**File:line:** `userdata.go:5251` (`func generateUserData`), `userdata.go:4732` (start of `userDataParams` struct).

---

## Research Question 2: Userdata Injection Point

### Where to add the profile-write block

The design spec says "adjacent to 2.8 Profile environment variables." Looking at the template:

- **Section "2.8. Sandbox identity"** is at template line ~320 (writes `/etc/profile.d/km-identity.sh`, `KM_SANDBOX_ID`, etc.)
- **Section "2.8. Profile environment variables"** (confusingly also labeled 2.8) is at template lines ~404-427 (writes `/etc/profile.d/km-profile-env.sh` from `.ProfileEnv`).
- **Section "2.9. Claude Code OpenTelemetry"** is at template lines ~432-450.

The cleanest placement is **between section 2.9 and section 3** (after line 450, before the `{{- if .SecretPaths }}` block). This keeps the profile write logically grouped with identity/environment setup, runs before secret injection, and is controlled by a simple `{{- if .ProfileYAML }}` gate.

### Heredoc-within-heredoc concern

The userdata script itself is a bash heredoc (the template is rendered to Go string, then used as EC2 user-data). Adding a `cat > /opt/km/.km-profile.yaml << 'KM_PROFILE_EOF'` inside it is safe because:

1. **Go text/template** renders the entire string BEFORE it becomes user-data. The `{{ .ProfileYAML }}` substitution happens at compile time in Go. The resulting bash script contains the literal YAML text.
2. **EOF sentinel collision:** The profile YAML will never contain the literal string `KM_PROFILE_EOF` on a line by itself. This string does not appear in any valid YAML key or value in a SandboxProfile. The sentinel is safe.
3. **Go template brace collision:** Profile YAML may contain `{{` or `}}` in `spec.execution.configFiles` content (e.g., JSON template files). This IS a real risk. The solution is to use `{{ "{{" }}` escaping — but this is complex. **Simpler solution:** Render the profile YAML to the template as a raw string, not as a template-interpolated value. Use `{{ .ProfileYAML }}` directly — Go's `text/template` will HTML-escape `{{` in template variables when using `html/template` but NOT when using `text/template`. Since `userdata.go` uses `text/template` (`template.New("userdata").Parse(...)`), the rendered string is inserted verbatim. This means `{{ }}` inside the profile YAML value is NOT re-parsed as template syntax — it is already a `string` value, not a template fragment. **Confirmed safe** — the template expansion of `.ProfileYAML` produces the literal string content of the field, not further template parsing.

**File:line:** `userdata.go:5252` (`template.New("userdata").Parse(userDataTemplate)`), `userdata.go:4926` (`parseUserDataTemplate()`).

**Recommended block (to insert after line ~450 in the template string):**

```bash
{{- if .ProfileYAML }}
# ============================================================
# 2.10. Profile on-box: write rendered profile for agent self-census
# ============================================================
mkdir -p /opt/km
cat > /opt/km/.km-profile.yaml << 'KM_PROFILE_EOF'
{{ .ProfileYAML -}}
KM_PROFILE_EOF
chmod 0644 /opt/km/.km-profile.yaml
chown sandbox:sandbox /opt/km/.km-profile.yaml
echo "[km-bootstrap] Profile written to /opt/km/.km-profile.yaml"
{{- end }}
```

Note: the sandbox username is hardcoded as `sandbox` throughout the entire template (see lines 157, 281, 308, 4666 etc.) — there is no `SandboxUser` template field. Match this convention.

---

## Research Question 3: SandboxUser Template Field

**Finding: there is no `SandboxUser` field on `userDataParams`.** The sandbox username is hardcoded as the literal string `"sandbox"` throughout the entire userdata template. Examples:
- Line 157: `chown sandbox:sandbox /workspace`
- Line 281: `chown sandbox:sandbox "{{ .MountPoint }}" 2>/dev/null || true`
- Line 4666: `chown -R sandbox:sandbox "$CFDIR"`
- Lines 1070, 1172, 1741, 1742, etc.

**Conclusion:** Use `chown sandbox:sandbox /opt/km/.km-profile.yaml` — no template field needed. This matches every other ownership line in the template.

---

## Research Question 4: Golden/Unit Test Harness

### Test structure

All `pkg/compiler` tests are in-package (package `compiler`). The `generateUserData()` function is package-private and called directly in tests.

Golden file pattern (from `userdata_h1_byte_identity_test.go` and `userdata_phase92_byte_identity_test.go`):
1. A **capture function** runs once with `CAPTURE_PRE_PHASE_BASELINE=1` env var set — writes actual output to `testdata/*.golden.sh` (or `.txt`). Never runs on normal `go test`.
2. A **dormancy invariant test** (`TestUserdataXXXByteIdentity`) reads the golden, calls `generateUserData()` with the same inputs, asserts byte-identity. Verifies a feature-off profile's output doesn't change.
3. An **active test** asserts the new block renders when the gate is true.

Golden files location: `pkg/compiler/testdata/` (absolute: `/Users/khundeck/working/klankrmkr/pkg/compiler/testdata/`)

**Regeneration command** (when golden files need updating):
```bash
# Delete the golden and re-run — the test will Fatalf with "run tests once after implementing" and auto-generate:
find /Users/khundeck/working/klankrmkr/pkg/compiler/testdata -name "*.golden.sh" -delete
go test ./pkg/compiler/ -count=1 -timeout 600s
```
OR for the capture-style golden:
```bash
CAPTURE_PRE113_BASELINE=1 go test ./pkg/compiler/ -run TestCapturePrePhase113Userdata -count=1
```

**Key existing tests that will need golden file updates:**
- `TestUserdataAdditionalVolumeOnly_GoldenByteIdentical` — reads `testdata/userdata_additional_volume_only.golden.sh`; will change because profile-write block now appears in all EC2 userdata.
- `TestUserdataLearnV2Phase92ByteIdentity` — reads `testdata/userdata_learn_v2_pre92_baseline.golden.sh`; same reason.
- `TestUserdataH1ByteIdentity` — reads `testdata/h1_byte_identity_golden.txt`; same reason.
- Any other golden-comparison test in the package.

**All existing byte-identity goldens must be regenerated** when Phase 113 adds the profile-write block, because the block renders for ALL EC2 profiles (it is gated only on `ProfileYAML` being non-empty, which it always will be after marshal).

**New tests to add (file: `pkg/compiler/userdata_phase113_test.go`):**
1. `TestUserdataProfileWriteBlockRendered` — assert `generateUserData()` output contains `/opt/km/.km-profile.yaml`, `KM_PROFILE_EOF`, `chown sandbox:sandbox`, and that a sample profile field value (e.g., the sandbox name) appears in the embedded YAML.
2. `TestUserdataProfileYAMLRoundTrip` — marshal the input profile `p`, call `generateUserData`, extract the YAML between `KM_PROFILE_EOF` sentinel pair from the output, parse it back to `SandboxProfile`, assert key fields match the original (round-trip).
3. `TestUserdataProfileYAMLAbsentWhenMarshalFails` (optional defensive test) — if marshal returns error, the block must not render and the rest of userdata must be valid.

**Run command:**
```bash
go test ./pkg/compiler/ -run TestUserdata -count=1 -timeout 600s
```

---

## Research Question 5: On-Box Signals for the Self-Census

### 5a. KM_* environment variables available on-box

Source: `/etc/profile.d/km-identity.sh` (always written, lines 336-356 of template):
- `KM_SANDBOX_ID` — sandbox identifier
- `KM_RESOURCE_PREFIX` — e.g. "km" or custom prefix
- `KM_SANDBOX_HOSTNAME` — FQDN (e.g. `{alias or id}.{emailDomain}`)
- `KM_SANDBOX_DOMAIN` — email domain
- `KM_SANDBOX_EMAIL` / `KM_EMAIL_ADDRESS` / `KM_SANDBOX_FROM_EMAIL` — sandbox email address
- `KM_ARTIFACTS_BUCKET` — S3 bucket
- `KM_OPERATOR_EMAIL` — operator email (when set)
- `KM_SANDBOX_ALIAS` — alias (when set)
- `KM_ALIAS_EMAIL` — alias email (when alias set)
- `KM_ALLOWED_SENDERS` — email allowlist (when set)

Source: `/etc/profile.d/km-notify-env.sh` (written when `spec.cli != nil`, lines 1082-1107):
- `KM_NOTIFY_ON_PERMISSION` — "0" or "1"
- `KM_NOTIFY_ON_IDLE` — "0" or "1"
- `KM_NOTIFY_COOLDOWN_SECONDS` — integer (when set)
- `KM_NOTIFY_EMAIL` — address (when set)
- `KM_NOTIFY_EMAIL_ENABLED` — "0" or "1" (when set)
- `KM_NOTIFY_SLACK_ENABLED` — "0" or "1" (when set)
- `KM_SLACK_MENTION_ONLY` — "true"/"false" (when Slack enabled)
- `KM_SLACK_REACT_ALWAYS` — "true"/"false" (when Slack enabled)
- `KM_SLACK_CHANNEL_ID` — channel ID (compile-time pin, when channelOverride set)
- `KM_SLACK_THREADS_TABLE` — DDB table name (when Slack inbound enabled)
- `KM_SLACK_INBOUND_QUEUE_URL` — SQS queue URL (filled at create time)
- `KM_NOTIFY_SLACK_TRANSCRIPT_ENABLED` — "1" (when transcript enabled)
- `KM_SLACK_STREAM_TABLE` — DDB table name (when transcript enabled)
- `KM_AGENT` — "claude" or "codex"
- `KM_GITHUB_INBOUND_QUEUE_URL` — SQS queue URL (filled at create time, when GitHub inbound enabled)
- `KM_H1_INBOUND_QUEUE_URL` — SQS queue URL (filled at create time, when H1 inbound enabled)

Source: `/etc/profile.d/km-slack-runtime.sh` (runtime-fetched when Slack enabled, lines ~359-402):
- `KM_SLACK_CHANNEL_ID` — runtime-resolved channel ID (overrides compile-time)
- `KM_SLACK_BRIDGE_URL` — Lambda Function URL

Source: `/etc/profile.d/km-profile-env.sh` (when `spec.execution.env` is set):
- All profile `spec.execution.env` key-value pairs exported

Source: `/etc/profile.d/km-identity.sh` also includes (for GitHub source access, line ~578):
- `KM_ALLOWED_REFS` — colon-separated ref list (when set)

### 5b. Sidecar helper binary locations

All at `/opt/km/bin/` (also symlinked to `/usr/local/bin/` where noted):

| Binary | Always present | Condition | Lines |
|--------|---------------|-----------|-------|
| `km-send` | Yes (bash script, line 2775) | Always | ~2775 |
| `km-recv` | Yes (bash script, line 3696) | Always | ~3696 |
| `km-slack` | Yes (Go binary, line 1120) | Always (downloaded from S3) | 1120 |
| `km-notify-hook` | Yes (bash script, line 619) | Always | 619 |
| `km-github` | No | Only when `GitHubInboundEnabled` | 1131-1135 |
| `km-h1` | No | Only when `H1InboundEnabled` | 1139-1143 |
| `km-presence` | Yes (Go binary, line 1121) | Always | 1121 |
| `km-dns-proxy` | Yes (Go binary, line 1117) | Always | 1117 |
| `km-http-proxy` | Yes (Go binary, line 1118) | Always | 1118 |
| `km-audit-log` | Yes (Go binary, line 1119) | Always | 1119 |

**Note:** `km-github` and `km-h1` are present only on boxes with the respective inbound-enabled profiles. The presence of these binaries tells the agent which bridges the box serves.

### 5c. Systemd unit names

| Unit | Purpose | Condition |
|------|---------|-----------|
| `km-dns-proxy.service` | DNS filtering sidecar | Always (except eBPF-only) |
| `km-http-proxy.service` | HTTP/S MITM proxy sidecar | Always |
| `km-audit-log.service` | Audit log sidecar | Always |
| `km-tracing.service` | OTEL tracing sidecar | Always |
| `km-presence.service` | Presence/heartbeat daemon | Always |
| `km-mail-poller.service` | Email inbox poll | When `SandboxEmail` non-empty (always for EC2) |
| `km-slack-inbound-poller.service` | Slack inbound SQS poll | When `SlackInboundEnabled` |
| `km-github-inbound-poller.service` | GitHub inbound SQS poll | When `GitHubInboundEnabled` |
| `km-h1-inbound-poller.service` | H1 inbound SQS poll | When `H1InboundEnabled` |
| `km-bootstrap.service` | Per-boot cgroup + /run/km setup | Always |
| `km-ebpf-enforcer.service` | eBPF cgroup BPF enforcer | When `Enforcement == "ebpf" or "both"` |

**File:line:** lines 3027, 3044, 3074, 3096, 3116 (unit definitions); lines 4241-4249 (enable/restart).

### 5d. Network-position passive probes

**Proxy sidecar detection:**
- Process `km-http-proxy` running: `systemctl is-active km-http-proxy`
- `HTTP_PROXY` / `HTTPS_PROXY` env vars set to `http://127.0.0.1:3128` (set for `proxy` and `both` enforcement modes, template lines ~4296-4311)
- Custom CA cert at `/usr/local/share/ca-certificates/km-proxy-ca.crt` (present when proxy is active)

**eBPF cgroup detection:**
- `/sys/fs/cgroup/km.slice/km-{KM_SANDBOX_ID}.scope/` — created by `km-bootstrap.service` on every boot (line 1376). Present when eBPF or both enforcement.
- `systemctl is-active km-ebpf-enforcer` — active only in `ebpf` or `both` mode.
- `/run/km/enforcer.env` — created by `km-ebpf-enforcer.service` ExecStartPre (line 4358).

**iptables DNAT detection (proxy mode):**
- `iptables -t nat -L OUTPUT -n 2>/dev/null | grep -q DNAT` — DNAT rules installed only for `proxy` enforcement.

**No `KM_ENFORCEMENT` env var is exported on-box.** Enforcement mode is only inferable from the above runtime signals. Confirmed by grep — no `export.*KM_ENFORCEMENT` anywhere in `userdata.go`. The skill must infer enforcement from: km-ebpf-enforcer active → ebpf or both; iptables DNAT present → proxy or both; both signals → both.

**DNS resolver:** `/etc/resolv.conf` — on proxy mode, may be unchanged (system resolver passes through km-dns-proxy via iptables DNAT on port 53). On eBPF mode, `km-dns-resolver` daemon handles DNS.

### 5e. Privilege detection

- `sudo -n true 2>/dev/null && echo "privileged" || echo "non-privileged"` — passwordless sudo available iff `spec.execution.privileged: true`.
- Manifest on-box: `/etc/sudoers.d/sandbox` exists (line 144) AND sandbox user is in `wheel` or `sudo` group.
- For non-privileged: `/etc/sudoers.d/sandbox` is removed (line 152) and sandbox is removed from `wheel`/`sudo` groups.
- **Cross-check:** profile's `spec.execution.privileged` (readable from `/opt/km/.km-profile.yaml`).

**File:line:** `userdata.go:137-155` (privileged/non-privileged conditional in template).

---

## Architecture Patterns

### Pattern 1: New `userDataParams` field with template gate

Every Phase 92+ feature follows this pattern:
1. Add a new field to `userDataParams` struct with a doc comment referencing the phase.
2. Populate the field in `generateUserData()` after all other params are built.
3. Gate the template block with `{{- if .FieldName }}...{{- end }}`.
4. Update byte-identity goldens by regenerating them.

Example: `H1InboundEnabled bool` (line 4922), populated at line 5515, gated at template line ~4236 (`{{- if or (eq .Enforcement "ebpf") (eq .Enforcement "both") }}`).

### Pattern 2: configFiles section for chown pattern

Section 7.6 (line 4660-4668) shows the standard write-and-chown idiom:
```
CFDIR="$(dirname '{{ $path }}')"
mkdir -p "$CFDIR"
cat > '{{ $path }}' << 'KM_CONFIG_EOF'
{{ $content }}
KM_CONFIG_EOF
chown -R sandbox:sandbox "$CFDIR"
```
The profile-write block follows this pattern but targets the fixed path `/opt/km/.km-profile.yaml`.

### Pattern 3: Skill graceful degradation

The current `klanker:sandbox` SKILL.md already demonstrates this at line 48:
```bash
cat /opt/km/.km-profile.yaml 2>/dev/null || echo "NO_PROFILE"
```
Phase 113 expands this: when `NO_PROFILE` is returned, fall back to env-var census (existing Step 1 logic) plus live probe-only network/privilege sections.

### Recommended Project Structure for new files

```
pkg/compiler/
├── userdata.go                    — add ProfileYAML field + template block
├── userdata_phase113_test.go      — new test file
│
skills/sandbox/
└── SKILL.md                       — rewrite with sections A-F
│
.claude-plugin/
├── plugin.json                    — version bump 0.4.8 → 0.4.9
└── marketplace.json               — version bump (matching)
```

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Profile YAML serialization | Custom marshaler | `yaml.Marshal(p)` (goccy/go-yaml already in imports) | Covers all fields, handles nil pointers, matches `create.go` convention |
| Template brace escaping | Custom pre-processor | `text/template` (not `html/template`) — `{{ .ProfileYAML }}` renders verbatim, no re-parsing of braces | Already confirmed safe |
| Golden file diffing | Custom differ | Use `diffStrings()` helper already at `userdata_test.go:2054` | Already present in package |

---

## Common Pitfalls

### Pitfall 1: Assuming `remoteProfileYAML` is available in the compiler

**What goes wrong:** Trying to pass `remoteProfileYAML` from `create.go` into `compiler.Compile()` as a parameter requires changing `CompiledArtifacts`, `Compile()`, `compileEC2()`, and `generateUserData()` signatures — a large blast radius.
**How to avoid:** Marshal `p` inside `generateUserData()` directly. The `*profile.SandboxProfile` is already available and reflects the same mutations (noBedrock, ttl/idle overrides) that were applied before `compiler.Compile()` was called.
**Warning signs:** If you see a proposed signature change to `func Compile(p, sandboxID, onDemand, network, ...)` adding a `profileYAML string` parameter — that's the wrong approach.

### Pitfall 2: YAML marshal inside generateUserData captures mutations correctly

**What goes wrong:** Marshaling `p` too early (before noBedrock/ttl/idle mutations are applied) produces a YAML that doesn't match the mutations the create flow applied.
**How to avoid:** In `create.go`, all mutations are applied to `resolvedProfile` BEFORE `compiler.Compile(resolvedProfile, ...)` is called (noBedrock at lines ~2187-2191; ttl/idle at lines ~2179-2185). So `p` inside `generateUserData()` is already the mutated struct. Marshal at the END of `generateUserData()` (after all `params.*` are set but before `tmpl.Execute()`).

### Pitfall 3: Forgetting to regenerate ALL golden files

**What goes wrong:** The profile-write block renders for every EC2 profile (gated only on `ProfileYAML` non-empty, which is always true after marshal). Three committed golden files will fail byte-identity: `userdata_additional_volume_only.golden.sh`, `userdata_learn_v2_pre92_baseline.golden.sh`, `h1_byte_identity_golden.txt`.
**How to avoid:** In Plan 01's test task, explicitly regenerate all three by deleting and re-running, then commit the updated goldens.
**Warning signs:** `TestUserdataAdditionalVolumeOnly_GoldenByteIdentical` fails — golden mismatch.

### Pitfall 4: Go template brace collision in configFiles YAML bodies

**What goes wrong:** A profile's `spec.execution.configFiles` may contain JSON with `{{ }}` braces (e.g. template files). If these are interpolated via `{{ .ProfileYAML }}` in a `text/template`, they would be re-parsed as template directives.
**How to avoid:** `text/template` does NOT recursively expand template variables — `{{ .ProfileYAML }}` replaces itself with the string content of `ProfileYAML`, and the content of that string is NOT further interpreted as template syntax. This is standard `text/template` behavior. No escaping needed.

### Pitfall 5: Plugin cache — clients see stale skill

**What goes wrong:** `klanker:sandbox` SKILL.md is served from the plugin cache. If `plugin.json` and `marketplace.json` versions aren't bumped, clients continue using the old 106-line skill even after the rewrite ships.
**How to avoid:** Bump `plugin.json` version from `"0.4.8"` to `"0.4.9"` (or next available). Apply the same version to `marketplace.json`. This is always required for any skill content change (`project_plugin_version_gates_cache`).
**File:** `/Users/khundeck/working/klankrmkr/.claude-plugin/plugin.json` (current: `"version": "0.4.8"`).

---

## Code Examples

### Adding `ProfileYAML` to `userDataParams` (userdata.go, after line 4923)

```go
// ProfileYAML is the rendered SandboxProfile serialized to YAML, written to
// /opt/km/.km-profile.yaml during boot so the on-box agent can read its own
// declarative configuration without S3 or IAM. Set from yaml.Marshal(p) inside
// generateUserData(). Empty string means the profile-write block is skipped (never
// happens in practice since marshal of a valid *profile.SandboxProfile succeeds).
// Phase 113.
ProfileYAML string
```

### Populating `ProfileYAML` inside `generateUserData()` (after line ~5690, before tmpl.Execute)

```go
// Phase 113: marshal the profile to YAML for the on-box self-census file.
// Marshal here (not from raw file bytes) so any mutations applied before
// compiler.Compile() — noBedrock, ttl/idle overrides — are reflected.
// Use goccy/go-yaml (already imported) for consistency with the rest of the create flow.
profileYAMLBytes, marshalErr := yaml.Marshal(p)
if marshalErr == nil {
    params.ProfileYAML = string(profileYAMLBytes)
} else {
    // Non-fatal: profile-write block will be skipped (ProfileYAML empty)
    // Callers should not see this — a valid *profile.SandboxProfile always marshals.
    _ = marshalErr
}
```

### Template block (insert after section 2.9, around line 450 in `userDataTemplate`)

```bash
{{- if .ProfileYAML }}
# ============================================================
# 2.10. Profile on-box: write rendered profile for agent self-census (Phase 113)
# ============================================================
mkdir -p /opt/km
cat > /opt/km/.km-profile.yaml << 'KM_PROFILE_EOF'
{{ .ProfileYAML -}}
KM_PROFILE_EOF
chmod 0644 /opt/km/.km-profile.yaml
chown sandbox:sandbox /opt/km/.km-profile.yaml
echo "[km-bootstrap] Profile written to /opt/km/.km-profile.yaml"
{{- end }}
```

### Skill: graceful degradation shell pattern

```bash
# Read profile (Phase 113+) or degrade gracefully on older boxes
KM_PROFILE=$(cat /opt/km/.km-profile.yaml 2>/dev/null)
if [ -z "$KM_PROFILE" ]; then
  echo "[INFO] /opt/km/.km-profile.yaml absent — pre-Phase-113 sandbox"
  echo "[INFO] Falling back to env-var census + live probes only"
  PROFILE_AVAILABLE=0
else
  PROFILE_AVAILABLE=1
fi
```

### Skill: enforcement mode inference (no KM_ENFORCEMENT env var on-box)

```bash
# Infer enforcement mode from runtime signals (no KM_ENFORCEMENT env var exported)
EBPF_ACTIVE=$(systemctl is-active km-ebpf-enforcer 2>/dev/null || echo "inactive")
IPTABLES_DNAT=$(iptables -t nat -L OUTPUT -n 2>/dev/null | grep -c DNAT || echo "0")
if [ "$EBPF_ACTIVE" = "active" ] && [ "$IPTABLES_DNAT" -gt "0" ]; then
  ENFORCEMENT="both"
elif [ "$EBPF_ACTIVE" = "active" ]; then
  ENFORCEMENT="ebpf"
elif [ "$IPTABLES_DNAT" -gt "0" ]; then
  ENFORCEMENT="proxy"
else
  ENFORCEMENT="unknown"
fi
# Cross-check from profile (if available)
if [ "$PROFILE_AVAILABLE" -eq 1 ]; then
  PROFILE_ENFORCEMENT=$(echo "$KM_PROFILE" | grep "enforcement:" | awk '{print $2}')
fi
```

### Skill: privilege probe

```bash
# Probe passwordless sudo (matches spec.execution.privileged)
if sudo -n true 2>/dev/null; then
  PRIV_STATUS="privileged (passwordless sudo available)"
else
  PRIV_STATUS="non-privileged (sudo requires password or is disabled)"
fi
# Cross-check from profile
if [ "$PROFILE_AVAILABLE" -eq 1 ]; then
  PROFILE_PRIVILEGED=$(echo "$KM_PROFILE" | grep "privileged:" | awk '{print $2}')
  if [ "$PROFILE_PRIVILEGED" = "true" ] && [[ "$PRIV_STATUS" != privileged* ]]; then
    echo "WARN: profile says privileged:true but sudo -n true failed — possible AMI or policy issue"
  fi
fi
```

### Skill: sidecar binary census

```bash
for bin in km-send km-recv km-slack km-github km-h1; do
  if test -x "/opt/km/bin/$bin"; then
    echo "$bin: present"
  else
    echo "$bin: absent"
  fi
done
```

---

## Validation Architecture

nyquist_validation is enabled (`.planning/config.json` `workflow.nyquist_validation: true`).

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib `testing` package) |
| Config file | none — run via `go test` |
| Quick run command | `go test ./pkg/compiler/ -run TestUserdataProfile -count=1 -timeout 600s` |
| Full suite command | `go test ./... -count=1 -timeout 600s` |

### Phase Requirements → Test Map

Phase 113 has no formal requirement IDs. Tests are derived from the phase goal and CONTEXT decisions.

| Goal | Behavior | Test Type | Automated Command | File Exists? |
|------|----------|-----------|-------------------|-------------|
| Profile-write block renders | `generateUserData()` output contains `/opt/km/.km-profile.yaml` and `KM_PROFILE_EOF` | unit | `go test ./pkg/compiler/ -run TestUserdataProfileWriteBlockRendered -count=1` | ❌ Wave 0 |
| Profile YAML round-trip | YAML embedded in userdata parses back to equivalent `SandboxProfile` | unit | `go test ./pkg/compiler/ -run TestUserdataProfileYAMLRoundTrip -count=1` | ❌ Wave 0 |
| Existing goldens still match | No regression in H1/Phase92/AdditionalVolume byte-identity | unit | `go test ./pkg/compiler/ -run TestUserdata.*ByteIdentical\|TestUserdataH1ByteIdentity\|TestUserdataLearnV2Phase92ByteIdentity -count=1` | ✅ (golden files need update) |
| Profile validation passes | `scripts/validate-all-profiles.sh` green (no schema change) | smoke | `bash scripts/validate-all-profiles.sh` | ✅ |
| Graceful degradation | Skill does not error when profile file absent | manual UAT | `km shell <id>` (pre-Phase-113 box) + run census section A | ❌ Wave 0 (live UAT) |
| Live census runs end-to-end | All six sections A-F complete without error | manual UAT | `km create <slack-profile> && km shell <id>` + run census | ❌ Wave 0 (live UAT) |

### Sampling Rate

- **Per task commit:** `go test ./pkg/compiler/ -run TestUserdata -count=1 -timeout 600s`
- **Per wave merge:** `go test ./... -count=1 -timeout 600s`
- **Phase gate:** Full suite green + `scripts/validate-all-profiles.sh` green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/compiler/userdata_phase113_test.go` — new test file with `TestUserdataProfileWriteBlockRendered`, `TestUserdataProfileYAMLRoundTrip`
- [ ] Golden file updates: `testdata/userdata_additional_volume_only.golden.sh`, `testdata/userdata_learn_v2_pre92_baseline.golden.sh`, `testdata/h1_byte_identity_golden.txt` — all must be regenerated after the profile-write block lands
- [ ] Live UAT setup: `km create profiles/slack-notify.yaml` (or similar Slack-enabled profile) before Plan 113-03 UAT can run

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Skill referenced `/opt/km/.km-profile.yaml` but nothing wrote it | Phase 113 writes it from `generateUserData()` via marshal of `*profile.SandboxProfile` | Phase 113 | Fixes the broken skill reference; no drift between S3 copy and on-box copy |
| `klanker:sandbox` skill: 4 steps, email/tooling focus only | Rewrite to 6 sections A-F with network, privilege, bridges, Slack readiness | Phase 113 | Agent gains full self-knowledge from a single skill invocation |

**Deprecated:**
- The 4-step `klanker:sandbox` SKILL.md content is replaced in its entirety. The existing steps (identity, email policy, tooling, signing key) are preserved as Section A but restructured.

---

## Open Questions

1. **YAML import in `pkg/compiler/userdata.go`**
   - What we know: `go.mod` has `github.com/goccy/go-yaml`. The top of `userdata.go` imports are not shown — need to verify if `gopkg.in/yaml.v3` or `goccy/go-yaml` is already imported in `userdata.go` vs only in `create.go`.
   - What's unclear: Which YAML library is used for marshal inside the compiler package.
   - Recommendation: Check `import` block at top of `userdata.go` (lines 1-30). If neither YAML lib is imported there, add `github.com/goccy/go-yaml` (consistent with `create.go`). `encoding/yaml` does not exist in stdlib — one of these two must be used.

2. **`tmpl.Execute` call location inside `generateUserData()`**
   - What we know: the function signature is at line 5251 and returns `(string, error)`.
   - What's unclear: Exact line where `tmpl.Execute(&buf, params)` is called — need to confirm the marshal call is inserted before that line.
   - Recommendation: Plan 01 implementor should find `tmpl.Execute` in `generateUserData()` and insert the marshal immediately before it.

3. **Spec-review checkpoint for redaction**
   - What we know: Default is verbatim. The only risky field is `spec.execution.configFiles` values (may contain passwords/tokens in some operator setups).
   - What's unclear: Whether any in-repo profiles actually use `configFiles` with secret values.
   - Recommendation: Plan 113-03 (docs/UAT) includes the spec-review checkpoint. If `configFiles` bodies contain sensitive data, the plan can add a `sanitizeProfileForOnBox(p)` helper that redacts configFiles values before marshal. This is a scoped change to `generateUserData()` only.

---

## Sources

### Primary (HIGH confidence)

- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go` — template-data struct (`userDataParams`, lines 4732-4924), `generateUserData()` function (line 5251), template sections (throughout). Direct source inspection.
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/create.go` — `compiler.Compile()` call sites (lines 675, 737, 2259), `remoteProfileYAML` flow (lines 2290-2305), `profileYAMLForUpload()` (line 2710). Direct source inspection.
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata_h1_byte_identity_test.go` — golden test pattern (CAPTURE env var, byte-identity assertions). Direct source inspection.
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata_phase92_byte_identity_test.go` — golden test pattern confirmation.
- `/Users/khundeck/working/klankrmkr/pkg/compiler/testdata/` — golden file locations.
- `/Users/khundeck/working/klankrmkr/.claude-plugin/plugin.json` — current version `"0.4.8"`.
- `/Users/khundeck/working/klankrmkr/skills/sandbox/SKILL.md` — current 106-line skill content being replaced.

### Secondary (MEDIUM confidence)

- CONTEXT.md decisions — locked choices confirmed verbatim.
- Design spec `docs/superpowers/specs/2026-06-14-sandbox-self-awareness-design.md` — approved design, used as authority on section names and scope.

---

## Metadata

**Confidence breakdown:**
- Standard Stack: HIGH — direct source inspection of all relevant files
- Architecture (userdata threading): HIGH — traced both create paths end-to-end with file:line citations
- On-box signals: HIGH — all env vars and binary paths sourced directly from `userdata.go` template
- Pitfalls: HIGH — all confirmed from source (go template text/template behavior, hardcoded "sandbox" username, no KM_ENFORCEMENT env var)
- Skill content structure: MEDIUM — census sections derived from design spec + on-box signal inventory; exact bash idioms left to implementor

**Research date:** 2026-06-14
**Valid until:** 2026-07-14 (30 days — stable codebase, no external dependencies)
