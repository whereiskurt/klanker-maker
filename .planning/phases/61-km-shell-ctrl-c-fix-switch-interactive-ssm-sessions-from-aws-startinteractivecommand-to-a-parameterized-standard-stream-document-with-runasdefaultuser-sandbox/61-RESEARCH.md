# Phase 61: km shell Ctrl+C Fix — Research

**Researched:** 2026-04-23
**Domain:** AWS SSM Session Manager custom documents, Terraform aws_ssm_document, Go CLI parameter construction
**Confidence:** HIGH (core schema, IAM, Terraform patterns verified against official AWS docs and codebase); MEDIUM (empty-command shell behavior, SCP interaction)

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions
- Create `KM-Sandbox-Session` per-region SSM document with `sessionType: Standard_Stream`, `runAsEnabled: true`, `runAsDefaultUser: sandbox`, parameterized `shellProfile.linux: "{{ command }}"` (default empty).
- Use the new doc for all four affected callsites; pass inner command via `command` parameter.
- Drop `sudo -u sandbox -i` wrapper from each callsite.
- Place doc in `infra/modules/ssm-session-doc/v1.0.0/` using `aws_ssm_document`. Plug into `regionalModules()` in `internal/app/cmd/init.go`.
- No imperative `ssmClient.CreateDocument` from `km init`.
- IAM update: add `arn:aws:ssm:<region>:<acct>:document/KM-Sandbox-Session` to operator role's `ssm:StartSession` resources.
- Do NOT touch `shell.go:179` (root path — already works).
- No `KM-Root-Session` doc.
- Backwards compatibility: fail fast (option b) when `KM-Sandbox-Session` doc is not provisioned in the sandbox's region.
- No version-flagged migration. Single swap at CLI.
- Update `shell_test.go` and `agent_test.go` assertions. New test for `regionalModules()` ordering.

### Claude's Discretion
- Exact text of `shellProfile.linux` template (must handle empty = interactive shell AND non-empty = command-on-exit).
- Whether Terraform module exposes doc name as output or CLI hardcodes `KM-Sandbox-Session`.
- Quoting/escaping strategy for the `command` parameter values.

### Deferred Ideas (OUT OF SCOPE)
- eBPF cgroup placement for SSM-initiated sandbox sessions.
- Symmetric `KM-Root-Session` doc.
- Per-environment SSM session preferences (idle timeout, log group routing).
</user_constraints>

---

## Summary

Phase 61 fixes a Ctrl+C teardown bug in four SSM callsites by replacing `AWS-StartInteractiveCommand` (sessionType `InteractiveCommands`, which terminates on Ctrl+C) with a custom `KM-Sandbox-Session` document (sessionType `Standard_Stream`, which forwards Ctrl+C as PTY bytes).

The fix has three mechanical parts: (1) a new Terraform module `infra/modules/ssm-session-doc/v1.0.0/` that manages the `aws_ssm_document` resource, wired into `regionalModules()` as a no-dependency module; (2) IAM extension in the SCP / operator policy to permit `ssm:StartSession` on the new document ARN; (3) four CLI callsite edits dropping `sudo -u sandbox -i` and switching `--document-name` and `--parameters`.

The single highest-risk detail is the **empty-command shell behavior**: when `shellProfile.linux` resolves to empty string the SSM agent opens `sh` (not bash, not a login shell) and does NOT source `/etc/profile.d/`. This breaks the `km shell` non-root interactive path. The template must be a conditional one-liner: `[ -z "{{ command }}" ] && exec bash -l || bash -lc "{{ command }}"`.

**Primary recommendation:** Use the conditional bash-l template, construct `--parameters` JSON via `encoding/json`, and add `lifecycle { create_before_destroy = true }` to the Terraform resource to survive content updates with schema v1.0 documents.

---

## Standard Stack

### Core (this phase)
| Component | Version / Name | Purpose | Why Standard |
|-----------|---------------|---------|--------------|
| `aws_ssm_document` | hashicorp/aws provider | Manages the custom SSM Session document | Standard Terraform resource for SSM docs |
| SSM Session `Standard_Stream` | schemaVersion "1.0" | PTY session type that forwards signals correctly | Only session type that behaves like SSH for Ctrl+C |
| `runAsEnabled` + `runAsDefaultUser` | SSM session schema | User switch without sudo | Replaces the `sudo -u sandbox -i` wrapper |
| `encoding/json` | Go stdlib | Safe parameter JSON construction | Avoids manual quote-escaping bugs |

### Supporting
| Component | Purpose | When to Use |
|-----------|---------|-------------|
| `aws ssm start-session --parameters` | Passes `command` value to the doc template | All four non-root callsites |
| `bash -lc` | Runs a command in a bash login shell | Non-empty command parameter paths |
| `exec bash -l` | Spawns interactive login bash | Empty command (shell.go:214) path |

---

## Architecture Patterns

### Recommended Project Structure (new files only)
```
infra/modules/ssm-session-doc/v1.0.0/
├── main.tf          # aws_ssm_document resource
├── variables.tf     # var.document_name (default "KM-Sandbox-Session"), var.tags
└── outputs.tf       # output "document_name" (the actual name used)

infra/live/use1/ssm-session-doc/
└── terragrunt.hcl   # wires to module, uses region_label for state key
```

### Pattern 1: Terraform aws_ssm_document for Session type

**What:** Create an SSM Session document with `document_type = "Session"`, content as JSON string.

**When to use:** Any regional SSM session preferences or custom session doc.

```hcl
# Source: https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/ssm_document
resource "aws_ssm_document" "km_sandbox_session" {
  name          = var.document_name
  document_type = "Session"
  document_format = "JSON"

  content = jsonencode({
    schemaVersion = "1.0"
    description   = "KM sandbox session: Standard_Stream PTY as sandbox user"
    sessionType   = "Standard_Stream"
    parameters = {
      command = {
        type    = "String"
        default = ""
      }
    }
    inputs = {
      runAsEnabled      = true
      runAsDefaultUser  = "sandbox"
      idleSessionTimeout = "20"
      shellProfile = {
        linux = "[ -z \"{{ command }}\" ] && exec bash -l || bash -lc \"{{ command }}\""
      }
    }
  })

  tags = var.tags

  lifecycle {
    create_before_destroy = true
  }
}
```

**Critical:** `document_type = "Session"` and `document_format = "JSON"` are both required. The `schemaVersion` here is SSM Session schema ("1.0"), not SSM document schema (2.0). These are different namespaces.

**Versioning gotcha:** SSM Session documents with `schemaVersion "1.0"` (the only valid version for Session docs) do NOT support AWS-side in-place content updates via the SSM UpdateDocument API. Terraform's `aws_ssm_document` handles this by destroying and recreating the resource. `create_before_destroy = true` ensures zero-downtime replacement (new doc created with the same name is allowed). Active sessions at the moment of recreation continue until they end naturally — updating the doc does not terminate live sessions.

### Pattern 2: Terragrunt live module for ssm-session-doc

Follows the exact same structure as `infra/live/use1/dynamodb-schedules/terragrunt.hcl`. State key must use the region label:

```hcl
# infra/live/use1/ssm-session-doc/terragrunt.hcl
locals {
  repo_root     = dirname(find_in_parent_folders("CLAUDE.md"))
  site_vars     = read_terragrunt_config("${local.repo_root}/infra/live/site.hcl")
  region_config = read_terragrunt_config("${get_terragrunt_dir()}/../region.hcl")
  region_label  = local.region_config.locals.region_label
  region_full   = local.region_config.locals.region_full
}

include "root" {
  path = find_in_parent_folders("root.hcl")
}

remote_state {
  backend = "s3"
  generate = {
    path      = "backend.tf"
    if_exists = "overwrite_terragrunt"
  }
  config = {
    bucket         = local.site_vars.locals.backend.bucket
    key            = "${local.site_vars.locals.site.tf_state_prefix}/${local.region_label}/ssm-session-doc/terraform.tfstate"
    region         = local.site_vars.locals.backend.region
    encrypt        = local.site_vars.locals.backend.encrypt
    dynamodb_table = local.site_vars.locals.backend.dynamodb_table
  }
}

terraform {
  source = "${local.repo_root}/infra/modules/ssm-session-doc/v1.0.0"
}

inputs = {
  document_name = "KM-Sandbox-Session"
  tags = {
    "km:component" = "ssm-session"
    "km:managed"   = "true"
  }
}
```

### Pattern 3: CLI parameter JSON construction via encoding/json

**What:** Build `--parameters` JSON safely without manual quote-escaping.

**Current (bad) pattern** at `agent.go:376` and `agent.go:535`:
```go
fmt.Sprintf(`{"command":["%s"]}`, strings.ReplaceAll(tmuxCmd, `"`, `\"`))
```

**Correct pattern** — use `encoding/json`:
```go
import "encoding/json"

params := map[string][]string{
    "command": {innerCmd},
}
paramsJSON, err := json.Marshal(params)
if err != nil {
    return fmt.Errorf("marshal parameters: %w", err)
}
c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
    "--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
    "--document-name", "KM-Sandbox-Session",
    "--parameters", string(paramsJSON))
```

`encoding/json` handles all Unicode, backslash, and quote escaping correctly. No `strings.ReplaceAll` hacks.

**For the empty-parameter case** (`km shell` non-root, `shell.go:214`), do NOT pass `--parameters` at all, or pass `--parameters '{"command":[""]}'`. Either triggers the `[ -z "{{ command }}" ]` branch in the shellProfile.

### Pattern 4: regionalModules() insertion

Add `ssm-session-doc` between `s3-replication` and `create-handler` in `internal/app/cmd/init.go:83-147`. It has no dependencies on other modules and no modules depend on it. This minimizes diff churn while keeping a logical grouping (infrastructure foundation modules before Lambda handlers):

```go
{
    name:    "ssm-session-doc",
    dir:     filepath.Join(regionDir, "ssm-session-doc"),
    envReqs: nil,  // no env var requirements
},
```

Position: after `dynamodb-schedules` (line 114), before `s3-replication` (line 119). Rationale: both are standalone no-dependency modules. This groups SSM infrastructure with the DynamoDB tables.

### Anti-Patterns to Avoid

- **`sudo -u sandbox -i` wrapper in the new callsites:** Redundant with `runAsDefaultUser: sandbox`. The SSM agent already starts the shell as sandbox. Layering sudo on top causes a root→sandbox hop that is unnecessary and breaks the profile sourcing order.
- **`fmt.Sprintf` for `--parameters` JSON:** Any tmux command or Claude invocation command that contains quotes (they all do) will produce malformed JSON. Use `encoding/json.Marshal`.
- **Assuming empty shellProfile = bash login shell:** Empty shellProfile on Standard_Stream spawns `sh` without sourcing `/etc/profile.d/`. The shell.go path requires bash + profile sourcing for km env vars. Must use the conditional one-liner.
- **Touching `shell.go:179` (root path):** Root path uses default doc which is Standard_Stream. It already works. No change needed.
- **Adding `--parameters` for the root path:** Root path has no `--document-name` override, so no `--parameters` either. Leave it alone.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| JSON escaping for `--parameters` | `strings.ReplaceAll` quote escaping | `encoding/json.Marshal` | Handles all edge cases: Unicode, nested quotes, backslashes |
| User switching in SSM session | `sudo -u sandbox -i` wrapper | `runAsDefaultUser: sandbox` in doc | SSM agent handles the switch; sudo adds an extra shell layer |
| SSM document lifecycle | Imperative `CreateDocument` in Go | `aws_ssm_document` Terraform resource | Consistent with all other regional infra; idempotent |

---

## Common Pitfalls

### Pitfall 1: Empty shellProfile spawns sh, not bash
**What goes wrong:** `shellProfile.linux: "{{ command }}"` with `command=""` causes the SSM agent to execute an empty string as a shell command, which spawns a plain `sh` session without sourcing `/etc/profile.d/` scripts. The `km-profile-env.sh` and `km-identity.sh` profile scripts never run. `km shell` lands in a `sh` prompt missing all km environment variables.
**Why it happens:** SSM agent runs `sh -c ""` when shellProfile evaluates to empty, not a login shell.
**How to avoid:** Use `[ -z "{{ command }}" ] && exec bash -l || bash -lc "{{ command }}"` as the shellProfile.linux value. The `exec bash -l` replaces the sh process with a bash login shell that sources all profile scripts.
**Warning signs:** `echo $KM_SANDBOX_ID` is empty inside the session; prompt is `$` instead of bash.

### Pitfall 2: Terraform schema version confusion
**What goes wrong:** Thinking SSM Session `schemaVersion: "1.0"` is the same as the SSM document schema version that blocks in-place updates. They are different namespaces. Session-type documents always use `"1.0"` in their content JSON — this is not the Terraform schema version field.
**Why it happens:** The Terraform docs note about schema version 2.0+ refers to SSM Command/Automation documents. Session documents have their own schema version namespace.
**How to avoid:** Always use `schemaVersion: "1.0"` in Session document content. Add `lifecycle { create_before_destroy = true }` to handle any content changes via safe recreation.

### Pitfall 3: SCP DenySSMPivot blocks new document
**What goes wrong:** The `DenySSMPivot` SCP in `bootstrap.go` denies `ssm:StartSession` on `*` for principals not in `trustedSSM`. The operator's SSO role IS in `trustedSSM` (`arn:aws:iam::*:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*`), so the SCP allows it. However, if `KM-Sandbox-Session` were used by a principal NOT in `trustedSSM`, the SCP would block it. This is correct behavior — the new document doesn't change the SCP logic.
**Why it happens:** Confusion between the SCP's deny scope and the operator role's permissions. The operator SSO role is explicitly carved out.
**How to avoid:** No SCP change needed. The operator role is in `trustedSSM`. Verify that the bootstrap SCP template correctly includes `AWSReservedSSO_*` in the trusted list (it does, at `bootstrap.go:342`).

### Pitfall 4: Missing `--parameters` flag causes document parameter validation failure
**What goes wrong:** Calling `aws ssm start-session --document-name KM-Sandbox-Session` without `--parameters` when the document defines a `command` parameter with a default of `""` — AWS accepts this because the default covers the missing parameter. However, if the default is absent or the parameter is marked required, the call fails.
**Why it happens:** AWS SSM validates parameters at session start.
**How to avoid:** Keep `"default": ""` on the `command` parameter. For `km shell` non-root, pass no `--parameters` argument — the default kicks in and `[ -z "" ]` evaluates true, opening bash -l.

### Pitfall 5: Test count hardcoding in TestRunInitWithRunnerAllModules
**What goes wrong:** `init_test.go:92` asserts `len(mock.applied) != 6`. Adding `ssm-session-doc` to `regionalModules()` increments the expected count to 7 (or higher depending on which modules are created in the test). The test also hardcodes `moduleNames` at line 74.
**Why it happens:** Tests bake the exact count of modules.
**How to avoid:** Update the test to include `ssm-session-doc` in `moduleNames` and change the expected count from 6 to 7. Also update `TestRunInitSkipsSESWithoutZoneID` which expects 5.

---

## Code Examples

### Shell.go:214 — km shell non-root (before → after)

Before:
```go
// Source: internal/app/cmd/shell.go:214
c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
    "--target", instanceID, "--region", region, "--profile", "klanker-terraform",
    "--document-name", "AWS-StartInteractiveCommand",
    "--parameters", `{"command":["sudo -u sandbox -i"]}`)
```

After (no --parameters needed; default="" triggers bash -l):
```go
c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
    "--target", instanceID, "--region", region, "--profile", "klanker-terraform",
    "--document-name", "KM-Sandbox-Session")
```

### Agent.go:300 — km agent --claude (before → after)

Before:
```go
// Source: internal/app/cmd/agent.go:300-305
c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
    "--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
    "--document-name", "AWS-StartInteractiveCommand",
    "--parameters", fmt.Sprintf(
        `{"command":["sudo -u sandbox -i bash -c 'source /etc/profile.d/km-profile-env.sh 2>/dev/null; source /etc/profile.d/km-identity.sh 2>/dev/null; cd /workspace; %sexec %s'"]}`,
        noBedrockPrefix, claudeCmd))
```

After:
```go
innerCmd := fmt.Sprintf(
    "source /etc/profile.d/km-profile-env.sh 2>/dev/null; source /etc/profile.d/km-identity.sh 2>/dev/null; cd /workspace; %sexec %s",
    noBedrockPrefix, claudeCmd)
paramsJSON, _ := json.Marshal(map[string][]string{"command": {innerCmd}})
c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
    "--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
    "--document-name", "KM-Sandbox-Session",
    "--parameters", string(paramsJSON))
```

Note: `source /etc/profile.d/...` is now redundant because `bash -lc` in shellProfile already sources those files via login shell. The planner should decide whether to retain explicit sourcing for belt-and-suspenders or trim it. Retaining is safer.

### Agent.go:373 — km agent attach (before → after)

Before:
```go
// Source: internal/app/cmd/agent.go:372-376
tmuxCmd := `sudo -u sandbox -i bash -c "tmux attach-session -t $(tmux list-sessions -F '#{session_name}' 2>/dev/null | grep km-agent | tail -1) 2>/dev/null || echo No agent tmux sessions found"`
c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
    "--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
    "--document-name", "AWS-StartInteractiveCommand",
    "--parameters", fmt.Sprintf(`{"command":["%s"]}`, strings.ReplaceAll(tmuxCmd, `"`, `\"`)))
```

After:
```go
innerCmd := `tmux attach-session -t $(tmux list-sessions -F '#{session_name}' 2>/dev/null | grep km-agent | tail -1) 2>/dev/null || echo No agent tmux sessions found`
paramsJSON, _ := json.Marshal(map[string][]string{"command": {innerCmd}})
c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
    "--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
    "--document-name", "KM-Sandbox-Session",
    "--parameters", string(paramsJSON))
```

Note: The tmux command no longer needs `sudo -u sandbox -i bash -c "..."` wrapper. The SSM agent already runs as sandbox via runAsDefaultUser. The inner command runs directly in bash -lc (because command is non-empty). Single-quotes around tmux expression are preserved in JSON safely.

### Agent.go:532 — km agent run --interactive (before → after)

Before:
```go
// Source: internal/app/cmd/agent.go:531-535
tmuxCmd := fmt.Sprintf("sudo -u sandbox -i tmux new-session -s '%s' '/tmp/km-agent-run.sh; exec bash'", sessionName)
c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
    "--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
    "--document-name", "AWS-StartInteractiveCommand",
    "--parameters", fmt.Sprintf(`{"command":["%s"]}`, strings.ReplaceAll(tmuxCmd, `"`, `\"`)))
```

After:
```go
innerCmd := fmt.Sprintf("tmux new-session -s '%s' '/tmp/km-agent-run.sh; exec bash'", sessionName)
paramsJSON, _ := json.Marshal(map[string][]string{"command": {innerCmd}})
c := exec.CommandContext(ctx, "aws", "ssm", "start-session",
    "--target", instanceID, "--region", rec.Region, "--profile", "klanker-terraform",
    "--document-name", "KM-Sandbox-Session",
    "--parameters", string(paramsJSON))
```

---

## IAM Scope: What Changes

### Current trustedSSM (bootstrap.go:338-343)
```go
trustedSSM := []string{
    fmt.Sprintf("arn:aws:iam::%s:role/km-ec2spot-ssm-*", appAccount),
    fmt.Sprintf("arn:aws:iam::%s:role/km-github-token-refresher-*", appAccount),
    fmt.Sprintf("arn:aws:iam::%s:role/km-ttl-handler", appAccount),
    "arn:aws:iam::*:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_*",
}
```

The `DenySSMPivot` SCP denies `ssm:StartSession` on `*` unless the caller is in trustedSSM. The operator SSO role (`AWSReservedSSO_*`) IS in trustedSSM, so the SCP allows the operator to call `ssm:StartSession` on the new document. **No SCP change needed.**

### What DOES need to change: Operator allow-list policy

The SCP is a deny boundary; the operator ALSO needs an explicit ALLOW for `ssm:StartSession` on the new document ARN. This is in the Terraform module for the operator role (not in `bootstrap.go` which generates SCP output for copy-paste). The planner should find the operator role's ssm:StartSession allow policy and extend the resource list:

```hcl
# In the operator IAM policy (infra/modules, wherever ssm:StartSession Allow lives):
resource = [
  "arn:aws:ec2:${var.region}:${var.account_id}:instance/*",
  "arn:aws:ssm:${var.region}::document/AWS-StartInteractiveCommand",   # keep for backwards compat during rollout
  "arn:aws:ssm:${var.region}:${var.account_id}:document/KM-Sandbox-Session",  # new
]
```

**Note:** `AWS-StartInteractiveCommand` is an AWS-managed document (account 0 in ARN). `KM-Sandbox-Session` is an account-owned document (account ID required in ARN).

The planner needs to find the exact file — search for `ssm:StartSession` in `infra/modules/` to locate the operator role policy.

---

## State of the Art

| Old Approach | Current Approach | Impact |
|--------------|-----------------|--------|
| `InteractiveCommands` sessionType (`AWS-StartInteractiveCommand`) | `Standard_Stream` sessionType (custom doc) | Ctrl+C no longer tears down the SSM session |
| `sudo -u sandbox -i` user switch in command | `runAsDefaultUser: sandbox` in doc inputs | User switch happens at SSM agent level, before shell spawn |
| `fmt.Sprintf + strings.ReplaceAll` for JSON | `encoding/json.Marshal` | Handles all escaping edge cases correctly |

---

## Open Questions

1. **Exact location of operator allow policy for ssm:StartSession**
   - What we know: The SCP deny is in `bootstrap.go`. The allow must live in an operator IAM role Terraform module.
   - What's unclear: Which file in `infra/modules/` contains the operator role's ssm:StartSession ALLOW statement.
   - Recommendation: Planner should `grep -r "ssm:StartSession" infra/modules/` to locate it. The task must extend the Resource list there.

2. **`km doctor` check for KM-Sandbox-Session**
   - What we know: `doctor.go` has `checkLambdaFunction`, `checkRegionVPC`, `checkDynamoTable` patterns — all inject a client that calls a describe/list API.
   - What's unclear: Whether the phase should add a `checkSSMDocument` function calling `ssm:DescribeDocument` on `KM-Sandbox-Session` per region.
   - Recommendation: Add the check. The pattern is straightforward: `SSMDescribeAPI` interface with `DescribeDocument`, `DoctorDeps.SSMDescribeClient`, and a `checkSSMDocument(ctx, client, region)` function. This aligns with the existing pattern and gives operators clear remediation ("run km init <region>").

3. **Conditional bash -l template: single-quotes vs double-quotes inside shellProfile**
   - What we know: The shellProfile.linux value is a JSON string field. The `{{ command }}` substitution happens at SSM agent level before the string is passed to sh.
   - What's unclear: Whether backslash-escaped double-quotes inside the template survive the SSM agent substitution.
   - Recommendation: Use the conditional with double-quote escaping as shown in Pitfall 1 (`bash -lc \"{{ command }}\"`). The `json.Marshal` on the content will handle outer JSON escaping; the inner escaped quotes survive as literal characters that the shell interprets.

4. **Whether `source /etc/profile.d/...` in agent.go commands is still needed**
   - What we know: `bash -lc` (non-empty command path) sources login files including `/etc/profile.d/`.
   - What's unclear: Whether the sandbox AMI's `/etc/profile.d/` scripts run before `.bashrc` or after, and whether the timing satisfies `km-profile-env.sh` needs.
   - Recommendation: Retain the explicit `source /etc/profile.d/km-profile-env.sh 2>/dev/null` as belt-and-suspenders. The overhead is negligible and the safety margin is high.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` package (stdlib) |
| Config file | none (standard go test) |
| Quick run command | `go test ./internal/app/cmd/... -run TestShell -v` |
| Full suite command | `go test ./internal/app/cmd/... -v` |

### Phase Requirements → Test Map

No REQUIREMENTS.md entries explicitly cover Phase 61. This phase addresses a functional correctness bug (CONF-05 implicitly — "km shell opens an interactive shell"). Testing covers behavioral correctness of the four callsites.

| Behavior | Test Type | Automated Command | File |
|----------|-----------|-------------------|------|
| `km shell` non-root uses `KM-Sandbox-Session`, no `sudo -u sandbox -i` | unit | `go test ./internal/app/cmd/... -run TestShellCmd_EC2` | `shell_test.go` |
| `km agent --claude` uses `KM-Sandbox-Session`, no `sudo` | unit | `go test ./internal/app/cmd/... -run TestAgentCmd_BackwardCompat` | `agent_test.go` |
| `km agent attach` uses `KM-Sandbox-Session`, no `sudo` | unit | `go test ./internal/app/cmd/... -run TestAgentAttach` | `agent_test.go` |
| `km agent run --interactive` uses `KM-Sandbox-Session`, no `sudo` | unit | `go test ./internal/app/cmd/... -run TestAgentInteractive` | `agent_test.go` |
| `km shell --root` still uses no `--document-name` | unit | `go test ./internal/app/cmd/... -run TestShellCmd_EC2` (root variant) | `shell_test.go` |
| `regionalModules()` includes `ssm-session-doc` | unit | `go test ./internal/app/cmd/... -run TestRunInitWithRunnerAllModules` | `init_test.go` |
| Ctrl+C forwards signal (does not terminate session) | integration/manual | Manual UAT (see UAT steps in CONTEXT.md) | N/A |
| Backwards compat: missing doc region fails fast with actionable error | unit | `go test ./internal/app/cmd/... -run TestShellCmd_MissingSSMDoc` (new) | `shell_test.go` |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/... -run "TestShell|TestAgent|TestRunInit" -v`
- **Per wave merge:** `go test ./internal/app/cmd/... -v`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

The test infrastructure exists (Go stdlib, no framework install needed). The following specific test additions are required:

- [ ] `shell_test.go` — update `TestShellCmd_EC2` to assert `KM-Sandbox-Session` in `--document-name` and absence of `sudo -u sandbox -i`
- [ ] `shell_test.go` — add `TestShellCmd_EC2_Root` to assert no `--document-name` on root path (regression guard)
- [ ] `agent_test.go` — update `TestAgentCmd_BackwardCompat` to assert `KM-Sandbox-Session`
- [ ] `agent_test.go` — update `TestAgentAttach` to assert `KM-Sandbox-Session` and absence of `sudo`
- [ ] `agent_test.go` — update `TestAgentInteractive` to assert `KM-Sandbox-Session` and absence of `sudo`
- [ ] `init_test.go` — update `TestRunInitWithRunnerAllModules` to expect 7 modules (add `ssm-session-doc`); update `moduleNames` slice
- [ ] `init_test.go` — update `TestRunInitSkipsSESWithoutZoneID` expected count from 5 to 6
- [ ] `init_test.go` — add `TestRegionalModulesIncludesSSMDoc` that calls `cmd.RegionalModules()` and asserts `ssm-session-doc` is present

---

## Sources

### Primary (HIGH confidence)
- [AWS Session Manager document schema](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-schema.html) — Standard_Stream sessionType, runAsEnabled, runAsDefaultUser, shellProfile fields and template syntax
- [Allow configurable shell profiles](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-preferences-shell-config.html) — empty shellProfile behavior, exec bash -l pattern
- [Sample IAM policies for Session Manager](https://docs.aws.amazon.com/systems-manager/latest/userguide/getting-started-restrict-access-quickstart.html) — ssm:StartSession IAM resource ARN format for custom documents
- [Bring your own CLI to Session Manager with configurable shell profiles](https://aws.amazon.com/blogs/mt/bring-your-own-cli-session-manager-configurable-shell-profiles/) — parameterized Standard_Stream doc JSON with `{{ linuxcmd }}` pattern
- Codebase direct reads: `shell.go`, `agent.go`, `init.go`, `bootstrap.go`, `doctor.go`, `shell_test.go`, `agent_test.go`, `init_test.go`, `infra/modules/dynamodb-budget/v1.0.0/`, `infra/live/use1/dynamodb-budget/terragrunt.hcl`

### Secondary (MEDIUM confidence)
- [aws_ssm_document Terraform resource](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/ssm_document) — document_type = "Session", document_format = "JSON" required fields
- [Terraform Provider AWS issue #31131](https://github.com/hashicorp/terraform-provider-aws/issues/31131) — versioning behavior for SSM documents; Session docs with schema 1.0 require destroy/recreate

### Tertiary (LOW confidence — needs validation)
- WebSearch results on empty shellProfile behavior (confirmed directionally: empty = sh not bash, no profile sourcing; exact agent behavior should be validated in UAT step 2 of CONTEXT.md)

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — Schema, Terraform resource, IAM format verified against official AWS docs
- Architecture: HIGH — Module pattern verified by direct codebase read of dynamodb-budget equivalent
- Pitfalls: HIGH (schema confusion, bash vs sh) / MEDIUM (SCP interaction, empty parameter edge case)
- Test changes: HIGH — test file read directly; exact count updates identified

**Research date:** 2026-04-23
**Valid until:** 2026-07-23 (stable AWS SSM schema; check if aws provider major version changes)
