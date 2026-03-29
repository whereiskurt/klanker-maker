# Phase 34: Agent Profiles — agent-orchestrator, goose, and codex - Research

**Researched:** 2026-03-29
**Domain:** SandboxProfile YAML authoring, AI coding agent tool requirements
**Confidence:** HIGH

## Summary

Phase 34 adds three new `profiles/` YAML files to the project: `agent-orchestrator.yaml`, `goose.yaml`, and `codex.yaml`. Each file is a complete SandboxProfile that defines the execution environment, init commands, network egress allowlist, budget, and agent settings for running its respective tool inside a klanker-maker sandbox. No schema changes, no Go code changes, and no Terraform changes are required — this phase is purely profile authoring using the existing, fully-implemented schema.

The main research question is: what do each of these three tools actually need at runtime? The answer determines `initCommands`, `execution.env`, `network.egress`, `policy.allowedCommands`, and `budget`. All three tools are open-source AI coding agents with distinct installation paths, dependency footprints, and API connectivity patterns. The profiles should follow the established pattern of `claude-dev.yaml` as the reference template — it is the most complete example of an AI agent profile in the project.

**Primary recommendation:** Model each new profile on `claude-dev.yaml`. Use `extends` only if a meaningful base profile reduces duplication — in practice the three tools are different enough that standalone profiles are cleaner. Set `metadata.prefix` and `metadata.alias` on each (matching the tool name), keep instance types at `t3.large` for agent-orchestrator (needs tmux sessions + multi-agent coordination), `t3.medium` for goose and codex (single-agent tools), and set separate compute and AI budgets appropriate to each tool's expected session length.

## Tool Requirements

### agent-orchestrator (ComposioHQ)

**Source:** https://github.com/ComposioHQ/agent-orchestrator
**Install:** `npm install -g @composio/ao`
**Runtime:** Node.js 20+, pnpm (for dev builds; global npm install suffices for usage)
**System deps:** tmux, git 2.25+, GitHub CLI (`gh`) — tmux is the default runtime for spawning agent sessions

**Prerequisites:**
- `gh auth login` with `repo` and `read:org` scopes — required for PR creation and issue management
- GitHub CLI must be authenticated before `ao start` can interact with repositories

**Ports:**
- Dashboard: port 3000 (configurable in `agent-orchestrator.yaml`)
- Terminal server: port 14800, direct terminal port: 14801

**Network hosts required (MEDIUM confidence — from SETUP.md and config example):**
- GitHub: `api.github.com`, `github.com`, `*.githubusercontent.com` (implicit via sourceAccess)
- npm registry: `registry.npmjs.org` (for install)
- Optional: Slack webhook (`hooks.slack.com`), Linear API (`api.linear.app`)

**Supported agent types:** `claude-code`, `codex`, `aider`, `goose`, `custom`

**Environment variables:**
- `LINEAR_API_KEY` — optional, for Linear issue tracker integration
- `SLACK_WEBHOOK_URL` — optional, for Slack notifications
- No mandatory env vars beyond `gh` authentication; agent-specific API keys (e.g. ANTHROPIC_API_KEY) are set per-agent

**Config file:** `~/.agent-orchestrator/` (data dir), `~/.worktrees/` (workspace dir)

**Confidence:** MEDIUM — npm package page and SETUP.md consulted; port list from config.yaml.example

### goose (Block)

**Source:** https://github.com/block/goose
**Install:** `curl -fsSL https://github.com/block/goose/releases/download/stable/download_cli.sh | CONFIGURE=false bash`
**Install path:** `~/.local/bin/goose` — PATH must include `~/.local/bin`
**System deps:** `bzip2` (for download extraction on some distros), no other OS-level deps
**Language:** Rust binary — self-contained, no runtime dependencies after install

**Config file:** `~/.config/goose/config.yaml` (YAML format)

**Environment variables:**
- `GOOSE_PROVIDER` — selects LLM provider (e.g. `anthropic`, `openai`, `openrouter`)
- `GOOSE_MODEL` — specific model name
- `GOOSE_MODE` — permission level: `auto`, `approve`, `chat`, `smart_approve`
- `GOOSE_MAX_TOKENS` — optional token limit
- Per-provider API key:
  - Anthropic: `ANTHROPIC_API_KEY` (or `ANTHROPIC_HOST` for custom endpoint like Bedrock)
  - OpenAI: `OPENAI_API_KEY` (and optionally `OPENAI_HOST`)
  - AWS Bedrock: uses IAM role credentials (`AWS_PROFILE` or instance profile)

**Network endpoints (MEDIUM confidence — from providers page):**
- Anthropic: `api.anthropic.com`
- OpenAI: `api.openai.com`
- AWS Bedrock: `bedrock-runtime.{region}.amazonaws.com`
- GitHub releases: `github.com` (for download/updates)
- goosed runs locally on port 3000 (HTTPS + WebSocket) — internal only

**Recommended for klanker-maker:** Use Bedrock as provider (keeps API traffic within AWS, no external API key needed). Set `GOOSE_PROVIDER=amazon_bedrock`, `GOOSE_MODEL=anthropic.claude-sonnet-4-5` (or equivalent Bedrock model ID), and rely on the sandbox IAM role for credentials.

**Confidence:** HIGH — official installation docs and provider docs consulted

### codex (OpenAI)

**Source:** https://github.com/openai/codex
**Install options:**
  1. npm: `npm install -g @openai/codex` (JavaScript version, older)
  2. Binary download: `codex-x86_64-unknown-linux-musl.tar.gz` from GitHub releases (Rust version, current — v0.117.0 as of 2026-03-26)
**Latest version:** rust-v0.117.0

**System requirements:** Ubuntu 20.04+ / Debian 10+, 4 GB RAM minimum (8 GB recommended)

**Runtime:** Rust binary — self-contained after binary download

**Config file:** `~/.codex/config.toml` (TOML format)

**Environment variables:**
- `OPENAI_API_KEY` — required for standard OpenAI API access
- `CODEX_SQLITE_HOME` — optional, overrides SQLite state DB location
- `CODEX_HOME` — fallback for state DB
- `CODEX_CA_CERTIFICATE` — path to PEM file for custom CA (useful for HTTP proxy MITM)
- `RUST_LOG` — debug logging

**Network endpoints:**
- `api.openai.com` — primary API endpoint (MEDIUM confidence — inferred; not explicitly documented)
- GitHub: `github.com` (for binary download)
- npm registry: `registry.npmjs.org` (if using npm install path)

**Sandbox modes (config option):** `read-only` (default), `workspace-write`, `danger-full-access`

**Important:** Codex requires an `OPENAI_API_KEY`. In a klanker-maker sandbox, this must be injected via SSM Parameter Store. The profile's `network.egress.allowedHosts` must include `api.openai.com`. There is no Bedrock-equivalent path for codex — it is OpenAI-only.

**Confidence:** MEDIUM — install docs and config docs consulted; exact API hostname inferred from OpenAI API standard

## Standard Stack

### Core (profile authoring)

| Component | Value | Purpose | Why Standard |
|-----------|-------|---------|--------------|
| Profile schema | klankermaker.ai/v1alpha1 | All three profiles use this apiVersion | Only supported version |
| Base template | `claude-dev.yaml` | Reference for section structure and values | Most complete AI agent profile in project |
| Instance type | `t3.large` (ao), `t3.medium` (goose, codex) | Compute allocation | agent-orchestrator runs multiple tmux sessions; others are single-agent |
| Substrate | `ec2` | All three use EC2 spot | Consistent with all existing agent profiles |
| initCommands pattern | yum install → tool install → workspace setup | Standard init sequence | Matches claude-dev.yaml pattern |

### Supporting

| Component | Version | Purpose | When to Use |
|-----------|---------|---------|-------------|
| SSM Parameter Store | existing infra | Inject OPENAI_API_KEY for codex | Any profile needing external API keys |
| Bedrock provider config | existing infra | goose can use Bedrock instead of Anthropic API | Avoids external API key for goose |

## Architecture Patterns

### Recommended Project Structure

```
profiles/
├── claude-dev.yaml          # existing — reference template
├── open-dev.yaml            # existing
├── hardened.yaml            # existing
├── restricted-dev.yaml      # existing
├── sealed.yaml              # existing
├── agent-orchestrator.yaml  # new — ComposioHQ multi-agent coordinator
├── goose.yaml               # new — Block's AI coding agent
└── codex.yaml               # new — OpenAI's coding agent
```

No changes to `pkg/profile/`, `pkg/compiler/`, or any Go code. No schema changes. No Terraform changes.

### Pattern 1: AI Agent Profile Structure (from claude-dev.yaml)

**What:** Complete SandboxProfile with lifecycle, runtime, execution (env + rsyncPaths + initCommands), sourceAccess, network egress, budget, artifacts, identity, sidecars, observability, policy, email, and agent sections.
**When to use:** Any profile running an AI coding agent that needs API access.

```yaml
# Source: profiles/claude-dev.yaml (project reference)
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: <tool-name>
  labels:
    tier: development
    tool: <tool-name>
    builtin: "true"
  prefix: <short-name>   # e.g. "ao", "goose", "codex"
  alias: <short-name>

spec:
  lifecycle:
    ttl: "4h"
    idleTimeout: "30m"
    teardownPolicy: destroy

  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.large  # or t3.medium
    region: us-east-1

  execution:
    shell: /bin/bash
    workingDir: /workspace
    env:
      SANDBOX_MODE: <tool-name>
      # tool-specific env vars here
    rsyncPaths:
      - ".gitconfig"
      # tool-specific config dirs
    initCommands:
      - "yum install -y git nodejs npm tmux jq"  # adjust per tool
      - "<tool install command>"
      - "mkdir -p /workspace && chown sandbox:sandbox /workspace"
```

### Pattern 2: SSM Secret Injection for External API Keys

**What:** Inject API keys from SSM into sandbox env without hardcoding in profile YAML.
**When to use:** codex (requires OPENAI_API_KEY); any profile needing external API keys.

```yaml
# Source: NETW-06 implementation — SSM Parameter Store injection pattern
# The secret ref is listed in the profile; km create injects it at provision time
execution:
  env:
    OPENAI_API_KEY: ""  # placeholder — injected from SSM at sandbox creation
```

Note: The exact SSM injection mechanism follows the existing pattern from Phase 2 (NETW-06). The profile lists the secret ref in an allowlist; the km compiler injects it via user-data or instance metadata. Verify with `pkg/compiler/` code for exact field name before writing the profile.

### Pattern 3: Bedrock Provider for goose

**What:** Configure goose to use AWS Bedrock instead of direct Anthropic API, so no external API key is needed.
**When to use:** goose profile — IAM role already has Bedrock permissions from instance profile.

```yaml
execution:
  env:
    GOOSE_PROVIDER: "amazon_bedrock"
    GOOSE_MODEL: "anthropic.claude-sonnet-4-5"
    AWS_DEFAULT_REGION: "us-east-1"
    GOOSE_MODE: "auto"
```

Network egress only needs `.amazonaws.com` for Bedrock — no `api.anthropic.com` needed.

### Anti-Patterns to Avoid

- **Hardcoding API keys in profile YAML:** Use SSM refs, not literal key values — profiles are checked into git.
- **Overly broad network allowlists:** Don't use `*` wildcards; each tool has known, enumerable endpoints.
- **Missing `CODEX_CA_CERTIFICATE` for codex:** The HTTP proxy uses MITM TLS. Codex (Rust binary) needs the proxy's CA cert injected so TLS handshakes succeed. Set `CODEX_CA_CERTIFICATE` to the proxy CA path.
- **Forgetting `~/.local/bin` in PATH for goose:** The download script installs to `~/.local/bin/goose`. The sandbox user's PATH must include this directory or the binary won't be found in shell sessions.
- **agent-orchestrator dashboard port conflicts:** The dashboard runs on port 3000. If the sandbox also runs other services on port 3000, configure `ao` to use a different port via `agent-orchestrator.yaml`.
- **Missing gh auth for agent-orchestrator:** `ao start` requires `gh` to be pre-authenticated. Use `gh auth login --with-token` in initCommands with a token injected from SSM, or the tool will fail at runtime prompting interactively.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Multi-agent coordination | Custom tmux script | `@composio/ao` via npm | ao handles session management, reactions, dashboard, and agent lifecycle |
| AI agent with tool use | Custom agent loop | goose binary | goose handles MCP, tool calling, multi-model config, session persistence |
| OpenAI-powered code agent | Custom agent wrapper | codex binary | codex handles sandboxed execution, multi-turn, approval workflow |
| Secret injection | Env file in /workspace | SSM Parameter Store (existing NETW-06 pattern) | SSM refs are auditable and not stored in git |

## Common Pitfalls

### Pitfall 1: MITM Proxy TLS Failures for Rust Binaries

**What goes wrong:** goose and codex are both Rust-based binaries that use the OS trust store or a compiled-in certificate bundle. The klanker-maker HTTP proxy does MITM TLS inspection. Rust's `rustls` or `native-tls` may reject the proxy's self-signed CA.
**Why it happens:** The proxy injects its own CA cert but Rust binaries don't automatically use the system CA store in the same way as curl/node.
**How to avoid:**
- For codex: set `CODEX_CA_CERTIFICATE=/etc/km-proxy/ca.crt` (or wherever the proxy CA is written in user-data)
- For goose: set `SSL_CERT_FILE=/etc/km-proxy/ca.crt` (goose's docs note `SSL_CERT_FILE` as a fallback cert config)
- The proxy CA path must match what the compiler actually writes — verify in `pkg/compiler/` before finalizing
**Warning signs:** `SSL certificate verify failed` or `certificate verify failed (unable to get local issuer certificate)` errors in sandbox logs

### Pitfall 2: agent-orchestrator gh CLI Not Authenticated

**What goes wrong:** `ao start` or any command that creates PRs/issues fails because `gh` is not authenticated in the sandbox.
**Why it happens:** `gh auth login` normally requires interactive browser-based OAuth. In a non-interactive sandbox, it must use `--with-token` and a pre-injected GitHub token.
**How to avoid:** Include in initCommands: `echo "$GITHUB_TOKEN" | gh auth login --with-token` where `GITHUB_TOKEN` is injected from SSM at provision time.
**Warning signs:** `gh: To authenticate, run: gh auth login` in ao logs

### Pitfall 3: goose Interactive Configuration on First Run

**What goes wrong:** Running goose for the first time triggers an interactive configuration wizard asking which provider to use. In a non-interactive sandbox, this hangs.
**Why it happens:** goose looks for `~/.config/goose/config.yaml`; if absent, it prompts.
**How to avoid:** Pre-write `~/.config/goose/config.yaml` in initCommands, or use `CONFIGURE=false` during install. The config.yaml must set `provider` and `model` so the wizard never runs. Alternatively set `GOOSE_PROVIDER` and `GOOSE_MODEL` env vars — goose reads these and skips the wizard.
**Warning signs:** Sandbox init hangs at the goose configuration step

### Pitfall 4: codex First-Run Authentication Prompt

**What goes wrong:** Running `codex` for the first time prompts for ChatGPT account login or API key entry interactively.
**Why it happens:** `~/.codex/config.toml` doesn't exist and `OPENAI_API_KEY` is not set.
**How to avoid:** Write a minimal `~/.codex/config.toml` in initCommands AND ensure `OPENAI_API_KEY` is injected from SSM before the tool is invoked.
**Warning signs:** codex shows authentication UI in terminal; no `~/.codex/config.toml` found

### Pitfall 5: npm Global Install Path Absent from PATH

**What goes wrong:** `ao` or `codex` (npm install path) not found after install.
**Why it happens:** `npm install -g` puts binaries in `/usr/local/bin` or `~/.npm-global/bin`; the sandbox user's PATH may not include these.
**How to avoid:** Always verify PATH in initCommands: `which ao || export PATH="$PATH:/usr/local/bin"`. For AL2023, `/usr/local/bin` is typically in PATH already — confirm by running `npm bin -g` in an initCommand and appending to PATH.

### Pitfall 6: agent-orchestrator Terminal Port Conflicts

**What goes wrong:** `ao start` fails with EADDRINUSE on port 14800 or 14801 if another process occupies these ports.
**Why it happens:** The terminal server ports (14800, 14801) are less well-known than port 3000 and may conflict with sidecar processes.
**How to avoid:** Document in profile comments that ports 3000, 14800, and 14801 are used. Verify that no existing klanker-maker sidecar uses these ports (sidecars currently use no well-known port in the 14xxx range).

## Code Examples

Verified patterns from project profiles and tool documentation:

### agent-orchestrator profile skeleton

```yaml
# Source: profiles/claude-dev.yaml (template), SETUP.md (auth), config.yaml.example (ports)
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: agent-orchestrator
  labels:
    tier: development
    tool: agent-orchestrator
    builtin: "true"
  prefix: ao
  alias: ao

spec:
  lifecycle:
    ttl: "8h"
    idleTimeout: "1h"
    teardownPolicy: destroy

  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.large
    region: us-east-1

  execution:
    shell: /bin/bash
    workingDir: /workspace
    env:
      SANDBOX_MODE: agent-orchestrator
    rsyncPaths:
      - ".gitconfig"
      - ".agent-orchestrator"
    initCommands:
      - "yum install -y git nodejs npm tmux jq"
      - "npm install -g @composio/ao"
      - "echo \"$GITHUB_TOKEN\" | gh auth login --with-token"
      - "mkdir -p /workspace ~/.agent-orchestrator ~/.worktrees"
      - "chown -R sandbox:sandbox /workspace ~/.agent-orchestrator ~/.worktrees"

  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
        - ".npmjs.org"
        - ".npmjs.com"
        - ".nodejs.org"
        - ".github.com"
        - ".githubusercontent.com"
      allowedHosts:
        - "registry.npmjs.org"
        - "api.github.com"
        - "github.com"

  policy:
    allowShellEscape: false
    allowedCommands:
      - git
      - node
      - npm
      - npx
      - ao
      - tmux
      - gh
      - bash
      - sh
      - jq

  agent:
    maxConcurrentTasks: 4
    taskTimeout: "120m"
```

### goose profile skeleton

```yaml
# Source: block.github.io/goose docs, profiles/claude-dev.yaml (template)
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: goose
  labels:
    tier: development
    tool: goose
    builtin: "true"
  prefix: goose
  alias: goose

spec:
  lifecycle:
    ttl: "4h"
    idleTimeout: "30m"
    teardownPolicy: destroy

  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.medium
    region: us-east-1

  execution:
    shell: /bin/bash
    workingDir: /workspace
    env:
      SANDBOX_MODE: goose
      GOOSE_PROVIDER: "amazon_bedrock"
      GOOSE_MODEL: "anthropic.claude-sonnet-4-5"
      AWS_DEFAULT_REGION: "us-east-1"
      GOOSE_MODE: "auto"
    rsyncPaths:
      - ".gitconfig"
      - ".config/goose"
    initCommands:
      - "yum install -y git bzip2 jq"
      - "curl -fsSL https://github.com/block/goose/releases/download/stable/download_cli.sh | CONFIGURE=false bash"
      - "echo 'export PATH=\"$HOME/.local/bin:$PATH\"' >> /home/sandbox/.bashrc"
      - "mkdir -p /home/sandbox/.config/goose"
      - "printf 'provider: amazon_bedrock\nmodel: anthropic.claude-sonnet-4-5\n' > /home/sandbox/.config/goose/config.yaml"
      - "mkdir -p /workspace && chown -R sandbox:sandbox /workspace /home/sandbox/.config /home/sandbox/.local"

  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
        - ".github.com"
        - ".githubusercontent.com"
      allowedHosts:
        - "bedrock-runtime.us-east-1.amazonaws.com"
        - "github.com"

  policy:
    allowShellEscape: false
    allowedCommands:
      - git
      - goose
      - bash
      - sh
      - jq
      - curl

  agent:
    maxConcurrentTasks: 1
    taskTimeout: "60m"
```

### codex profile skeleton

```yaml
# Source: github.com/openai/codex docs, profiles/claude-dev.yaml (template)
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: codex
  labels:
    tier: development
    tool: codex
    builtin: "true"
  prefix: codex
  alias: codex

spec:
  lifecycle:
    ttl: "4h"
    idleTimeout: "30m"
    teardownPolicy: destroy

  runtime:
    substrate: ec2
    spot: true
    instanceType: t3.medium
    region: us-east-1

  execution:
    shell: /bin/bash
    workingDir: /workspace
    env:
      SANDBOX_MODE: codex
      # OPENAI_API_KEY injected from SSM at provision time
      OPENAI_API_KEY: ""
      # Direct proxy CA cert for Rust TLS verification
      CODEX_CA_CERTIFICATE: "/etc/km-proxy/ca.crt"
    rsyncPaths:
      - ".gitconfig"
      - ".codex"
    initCommands:
      - "yum install -y git jq"
      - "curl -fsSL https://github.com/openai/codex/releases/download/rust-v0.117.0/codex-x86_64-unknown-linux-musl.tar.gz -o /tmp/codex.tar.gz"
      - "tar -xzf /tmp/codex.tar.gz -C /tmp && mv /tmp/codex /usr/local/bin/codex && chmod +x /usr/local/bin/codex"
      - "mkdir -p /home/sandbox/.codex"
      - "printf '[model]\\nsandbox_mode = \"workspace-write\"\\n' > /home/sandbox/.codex/config.toml"
      - "mkdir -p /workspace && chown -R sandbox:sandbox /workspace /home/sandbox/.codex"

  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
        - ".openai.com"
        - ".github.com"
        - ".githubusercontent.com"
      allowedHosts:
        - "api.openai.com"
        - "github.com"

  policy:
    allowShellEscape: false
    allowedCommands:
      - git
      - codex
      - bash
      - sh
      - jq

  agent:
    maxConcurrentTasks: 1
    taskTimeout: "60m"
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| claude-code only | Multiple agent types (ao, goose, codex) | Phase 34 | Profiles cover the broader AI coding agent ecosystem |
| Manual agent setup | Declarative initCommands in profile | Established pattern | Reproducible, auditable agent environments |
| Direct API keys in env | SSM injection (NETW-06) | Phase 2 | Keys not stored in YAML or git |

## Open Questions

1. **Exact SSM injection field name for OPENAI_API_KEY in codex profile**
   - What we know: Phase 2 implemented NETW-06 (SSM secret injection via `identity.secretRefs` or similar field). The `claude-dev.yaml` profile does not demonstrate the SSM injection syntax directly.
   - What's unclear: The exact profile YAML field that references SSM parameter names — is it under `identity.secretRefs`, `execution.secretEnv`, or another path?
   - Recommendation: Read `pkg/profile/types.go` in full and check `pkg/compiler/` before finalizing codex profile to use correct SSM field syntax. If the field isn't in the schema, use a comment in the profile indicating the key should be set post-provision via environment.

2. **HTTP proxy CA cert path for codex and goose**
   - What we know: The HTTP proxy does MITM and writes its CA cert somewhere during sandbox init. Codex accepts `CODEX_CA_CERTIFICATE`; goose accepts `SSL_CERT_FILE`.
   - What's unclear: The exact filesystem path where the proxy CA cert is written. Using `/etc/km-proxy/ca.crt` is a reasonable guess based on the proxy sidecar naming convention.
   - Recommendation: Check `cmd/km-http-proxy/` or sidecar build artifacts for the actual CA cert path before writing the final profile.

3. **gh CLI availability in AL2023 sandbox**
   - What we know: `claude-dev.yaml` does not install `gh` CLI — it's not in the initCommands. Phase 13 implemented GitHub App token integration.
   - What's unclear: Whether `gh` is pre-installed in the base AMI or must be installed explicitly.
   - Recommendation: Add explicit `gh` install to agent-orchestrator initCommands. Use the GitHub CLI rpm repo: `yum install -y 'dnf-command(config-manager)' && dnf config-manager --add-repo https://cli.github.com/packages/rpm/gh-cli.repo && dnf install -y gh`

4. **goose Bedrock model ID accuracy**
   - What we know: Bedrock model IDs follow the pattern `anthropic.claude-sonnet-4-5` or the cross-region inference profile pattern `us.anthropic.claude-sonnet-4-6`.
   - What's unclear: Whether goose's `amazon_bedrock` provider expects the bare model ID or the cross-region inference profile ARN format.
   - Recommendation: Match the `claude-dev.yaml` convention: `ANTHROPIC_DEFAULT_SONNET_MODEL: us.anthropic.claude-sonnet-4-6`. Check goose provider docs for whether it accepts the `us.` prefix or requires just the model ID.

5. **codex binary version pinning vs. latest**
   - What we know: v0.117.0 is current as of 2026-03-26. The binary download URL includes the version.
   - What's unclear: Whether the profile should pin to a specific version or use a `latest` redirect.
   - Recommendation: Pin to a specific version in initCommands for reproducibility. Add a comment noting the version should be updated periodically.

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go test (`go test ./...`) |
| Config file | none (standard Go testing) |
| Quick run command | `go test ./pkg/profile/... -run TestBuiltinProfiles -v` |
| Full suite command | `go test ./... -timeout 120s` |

### Phase Requirements to Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| PROF-34-01 | `agent-orchestrator.yaml` passes `km validate` | smoke | `go test ./pkg/profile/... -run TestBuiltinProfiles` | ❌ profile file doesn't exist yet |
| PROF-34-02 | `goose.yaml` passes `km validate` | smoke | `go test ./pkg/profile/... -run TestBuiltinProfiles` | ❌ profile file doesn't exist yet |
| PROF-34-03 | `codex.yaml` passes `km validate` | smoke | `go test ./pkg/profile/... -run TestBuiltinProfiles` | ❌ profile file doesn't exist yet |
| PROF-34-04 | All three profiles load via `km validate <file>` CLI | manual smoke | `./km validate profiles/agent-orchestrator.yaml && ./km validate profiles/goose.yaml && ./km validate profiles/codex.yaml` | ❌ profiles don't exist yet |

### Sampling Rate

- **Per task commit:** `go test ./pkg/profile/... -run TestBuiltinProfiles -v`
- **Per wave merge:** `go test ./... -timeout 120s`
- **Phase gate:** All three profiles pass `km validate` before marking phase complete

### Wave 0 Gaps

- [ ] No test infrastructure gaps — `pkg/profile/` already has `builtins_test.go` from Phase 7. The test should automatically pick up new profile files once they exist in `profiles/`. Verify that `builtins_test.go` iterates all files in the `profiles/` directory (not a hardcoded list) and that each file passes validation.

## Sources

### Primary (HIGH confidence)

- Official goose installation docs: https://block.github.io/goose/docs/getting-started/installation
- goose providers page: https://block.github.io/goose/docs/getting-started/providers
- agent-orchestrator SETUP.md: https://github.com/ComposioHQ/agent-orchestrator/blob/main/SETUP.md
- agent-orchestrator config.yaml.example: https://github.com/ComposioHQ/agent-orchestrator/blob/main/agent-orchestrator.yaml.example
- codex install docs: https://github.com/openai/codex/blob/main/docs/install.md
- DeepWiki goose installation: https://deepwiki.com/block/goose/2-installation-and-setup
- Project profiles/claude-dev.yaml — authoritative template for profile structure

### Secondary (MEDIUM confidence)

- openai/codex README (npm install path): https://github.com/openai/codex
- codex config docs: https://github.com/openai/codex/blob/main/docs/config.md
- openai/codex latest release (v0.117.0): https://github.com/openai/codex/releases/latest
- agent-orchestrator README: https://github.com/ComposioHQ/agent-orchestrator/blob/main/README.md
- OpenAI developer community (OPENAI_API_KEY usage): https://community.openai.com/t/login-with-openai-api-key-environment-variable/1371740

### Tertiary (LOW confidence)

- `api.openai.com` as codex network endpoint — inferred from standard OpenAI API usage, not confirmed in codex-specific docs
- Exact HTTP proxy CA cert path (`/etc/km-proxy/ca.crt`) — naming convention guess; verify in sidecar source

## Metadata

**Confidence breakdown:**

- Profile structure/schema: HIGH — existing profiles provide a clear, complete template; no schema changes needed
- agent-orchestrator requirements: MEDIUM — npm package confirmed, ports from config.yaml.example, gh auth requirement from SETUP.md
- goose requirements: HIGH — official install docs and provider docs consulted; Bedrock path is well-documented
- codex requirements: MEDIUM — binary download confirmed, API key requirement well-known, exact network host inferred
- HTTP proxy MITM CA interaction: MEDIUM — pattern known from project (Phase 3 proxy), specific env var names confirmed from tool docs
- Validation tests: HIGH — builtins_test.go pattern established in Phase 7

**Research date:** 2026-03-29
**Valid until:** 2026-04-29 (tool versions move fast; codex binary version should be re-checked at implementation time)
