# Requirements: Klanker Maker

**Defined:** 2026-03-21
**Core Value:** A sandbox is a declarative policy object that compiles into a controlled, auditable execution environment

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Schema & Validation

- [x] **SCHM-01**: Operator can define a SandboxProfile in YAML with apiVersion, kind, metadata, spec sections
- [x] **SCHM-02**: Schema supports lifecycle, runtime, execution, sourceAccess, network, identity, sidecars, observability, policy, and agent sections
- [x] **SCHM-03**: Operator can run `km validate <profile.yaml>` and get clear error messages for invalid profiles
- [x] **SCHM-04**: Profile can extend a base profile via `extends` field, inheriting and overriding specific sections (code exists, needs verification — Phase 7) (verified Phase 7 — inherit_test.go passes)
- [x] **SCHM-05**: Four built-in profiles ship with Klanker Maker: open-dev, restricted-dev, hardened, sealed (code exists, needs verification — Phase 7) (verified Phase 7 — builtins_test.go passes)

### Provisioning & Lifecycle

- [x] **PROV-01**: Operator can run `km create <profile>` to compile profile into Terragrunt inputs and provision EC2 + VPC + IAM
- [x] **PROV-02**: Operator can run `km destroy <sandbox-id>` to cleanly tear down all sandbox resources
- [x] **PROV-03**: Operator can run `km list` to see all running sandboxes with status
- [x] **PROV-04**: Operator can run `km status <sandbox-id>` to see detailed sandbox state
- [x] **PROV-05**: Sandbox auto-destroys after TTL expires
- [x] **PROV-06**: Sandbox auto-destroys after idle timeout with no activity
- [x] **PROV-07**: Sandbox teardown policy is configurable (destroy/stop/retain)
- [x] **PROV-08**: Every sandbox resource is tagged with `km:sandbox-id` for tracking and cost attribution
- [x] **PROV-09**: Operator can specify substrate (`ec2` or `ecs`) in the profile's `runtime.substrate` field and `km create` provisions the corresponding infrastructure
- [x] **PROV-10**: ECS substrate provisions an AWS Fargate task with sidecar containers for enforcement (DNS proxy, HTTP proxy, audit log) defined in the task definition
- [x] **PROV-11**: EC2 sandboxes use spot instances by default; on-demand fallback is configurable per profile
- [x] **PROV-12**: ECS sandboxes use Fargate Spot capacity provider by default; on-demand fallback is configurable per profile
- [x] **PROV-13**: Sandbox handles spot interruption gracefully — uploads artifacts to S3 before termination when possible

### Network & Security

- [x] **NETW-01**: Security Groups enforce egress restrictions as the primary enforcement layer
- [x] **NETW-02**: DNS proxy sidecar filters outbound DNS by allowlisted suffixes (works on both EC2 and ECS substrates)
- [x] **NETW-03**: HTTP proxy sidecar filters outbound HTTP/S by allowlisted hosts and methods (works on both EC2 and ECS substrates)
- [x] **NETW-04**: IAM role is session-scoped with configurable duration and region lock
- [x] **NETW-05**: IMDSv2 is enforced (http-tokens=required) on all sandbox EC2 instances
- [x] **NETW-06**: Secrets are injected via SSM Parameter Store with allowlist of permitted secret refs
- [x] **NETW-07**: SOPS encrypts secrets at rest with KMS keys provisioned as part of Klanker Maker infrastructure
- [x] **NETW-08**: GitHub source access controls allowlist repos, refs, and permissions (clone/fetch/push)

### Observability & Artifacts

- [x] **OBSV-01**: Audit log sidecar captures command execution logs (works on both EC2 and ECS substrates)
- [x] **OBSV-02**: Audit log sidecar captures network traffic logs (works on both EC2 and ECS substrates)
- [x] **OBSV-03**: Log destination is configurable (CloudWatch/S3/stdout)
- [x] **OBSV-04**: Filesystem policy enforces writable and read-only paths
- [x] **OBSV-05**: Artifacts upload to S3 on sandbox exit with configurable size limits
- [x] **OBSV-06**: S3 artifact storage supports multi-region replication
- [x] **OBSV-07**: Secret patterns are redacted from audit logs before storage
- [x] **OBSV-08**: Tracing sidecar collects OpenTelemetry traces and spans from sandbox workloads and exports to a configurable OTel collector endpoint
- [x] **OBSV-09**: Each sandbox session is logged as an MLflow run with sandbox metadata (profile, sandbox-id, duration, exit status) as run parameters
- [x] **OBSV-10**: OTel trace context is propagated through proxy sidecars so outbound HTTP requests carry trace headers

### Email & Communication

- [x] **MAIL-01**: SES is configured globally with Route53 domain verification
- [x] **MAIL-02**: Each sandbox agent gets its own email address (agent-id@domain)
- [x] **MAIL-03**: Agents inside sandboxes can send email via SES
- [x] **MAIL-04**: Operator receives email notifications for sandbox lifecycle events (expiry, errors, limits)
- [x] **MAIL-05**: Cross-account agent orchestration is possible via email

### Infrastructure Foundation

- [x] **INFR-01**: AWS multi-account setup: management account, terraform account, application account (defcon.run.34 pattern)
- [x] **INFR-02**: AWS SSO configured for operator access across accounts
- [x] **INFR-03**: Route53 hosted zone configured in management account, delegated to application account
- [x] **INFR-04**: KMS keys provisioned for SOPS encryption
- [x] **INFR-05**: S3 buckets for artifacts with lifecycle policies and cross-region replication
- [x] **INFR-06**: Terragrunt per-sandbox directory isolation (no workspace sharing)
- [x] **INFR-07**: Domain registered in management account and connected to application account
- [x] **INFR-08**: All infrastructure modules and application code from defcon.run.34 (Terraform modules: network, ec2spot, ecs-cluster, ecs-task, ecs-service, secrets; Terragrunt patterns: site.hcl, service.hcl; Go application: apps/local/configui/) are copied into the Klanker Maker repo, renamed, and adapted — no runtime or build-time dependency on defcon.run.34 exists

### ConfigUI

- [x] **CFUI-01**: Web-based profile editor for creating/editing SandboxProfile YAML
- [x] **CFUI-02**: Live sandbox status dashboard showing running sandboxes
- [x] **CFUI-03**: AWS resource discovery showing what each sandbox provisioned
- [x] **CFUI-04**: SOPS secrets management UI for encrypt/decrypt operations

### Platform Configuration

- [x] **CONF-01**: All platform-specific values (domain name, AWS account IDs, SSO start URL, region preferences) are defined in a single configuration file (e.g. `km-config.yaml` or `.klankermaker.yaml`) — operators checking out the repo set their own values before first use, AWS SSO-style configure flow
- [x] **CONF-02**: Domain name is configurable — SES email addresses (`{sandbox-id}@sandboxes.{domain}`), JSON Schema `$id` URL, `apiVersion` in profiles, and ConfigUI branding all derive from the configured domain, not hardcoded `klankermaker.ai`
- [x] **CONF-03**: AWS account numbers (management, terraform, application) and SSO start URL are configurable — referenced by Terragrunt hierarchy, IAM policies, and `km` CLI commands without hardcoding
- [x] **CONF-04**: `km init` or `km configure` command walks the operator through initial setup: domain, accounts, region, SSO — writes the config file and validates AWS access
- [x] **CONF-05**: `km shell <sandbox-id>` opens an interactive shell into a running sandbox — abstracts the substrate (EC2: SSM Session Manager, ECS: ECS Exec, future k8s: kubectl exec). Operator never needs to know the underlying AWS CLI incantation

### Budget Enforcement

- [x] **BUDG-01**: Per-sandbox budget with separate compute and AI spend pools defined in profile YAML (`spec.budget.compute.maxSpendUSD`, `spec.budget.ai.maxSpendUSD`)
- [x] **BUDG-02**: DynamoDB global table (single-table design, extending defcon.run.34 auth pattern) stores budget limits and running spend per sandbox, replicated to all regions where agents run for low-latency local reads
- [x] **BUDG-03**: Compute spend tracked as instance type spot rate × elapsed minutes (per-minute billing); rate sourced from AWS Price List API at sandbox creation
- [x] **BUDG-04**: AI/token spend tracked per Bedrock Anthropic model (Haiku, Sonnet, Opus); http-proxy sidecar intercepts `InvokeModel` responses, extracts `usage.input_tokens`/`usage.output_tokens`, multiplies by model rate, increments DynamoDB budget record
- [x] **BUDG-05**: Model pricing sourced from AWS Price List API (cached, refreshed daily) — supports all Anthropic models available on Bedrock
- [x] **BUDG-06**: At 80% budget threshold (configurable via `spec.budget.warningThreshold`), operator receives warning email via SES using existing `SendLifecycleNotification` pattern
- [x] **BUDG-07**: Dual-layer enforcement — at 100% AI budget, http-proxy returns 403 for Bedrock calls (immediate, real-time); the same EventBridge-triggered Lambda that checks compute spend also reads DynamoDB AI spend records and revokes the instance profile's Bedrock IAM permissions as a backstop (catches SDK/CLI calls that bypass the proxy); at 100% compute budget, Lambda suspends the sandbox: EC2 instances are stopped (`StopInstances` — preserves EBS, no compute charges, resumable on top-up); ECS Fargate tasks trigger artifact upload then stop (tasks are ephemeral — top-up re-provisions from stored profile in S3)
- [x] **BUDG-08**: Operator can top up a sandbox budget via `km budget add <sandbox-id> --compute <amount> --ai <amount>` which updates DynamoDB limits and resumes enforcement: for AI, restores Bedrock IAM and proxy unblocks; for compute, EC2 instances are started (`StartInstances` — resumes from stopped state), ECS Fargate tasks are re-provisioned from the stored profile in S3
- [x] **BUDG-09**: `km status <sandbox-id>` shows current spend vs budget for both compute and AI pools, including per-model AI breakdown
- [x] **BUDG-10**: AI/token spend tracked for Anthropic API (Claude Code) calls via `api.anthropic.com`; http-proxy sidecar intercepts `POST /v1/messages` responses (both non-streaming and SSE streaming), extracts `usage.input_tokens`/`usage.output_tokens`, prices against Anthropic's published model rates, and increments DynamoDB budget record using the same `IncrementAISpend` path as Bedrock metering

### Operator Experience

- [x] **OPER-01**: All terragrunt-calling CLI commands (`km create`, `km destroy`, `km init`, `km uninit`) suppress raw terragrunt/terraform output by default — show step-level summaries instead; `--verbose` flag restores full output streaming; errors and warnings always shown regardless of mode

### Operator Notification Hooks

- [x] **HOOK-01**: Compiler unconditionally writes `/opt/km/bin/km-notify-hook` bash script during user-data execution; script exists on every sandbox regardless of profile settings, and is gated at run-time by env vars
- [x] **HOOK-02**: Compiler merges `Notification` and `Stop` hook entries into `~/.claude/settings.json`, preserving any user-supplied entries from `spec.execution.configFiles` (parses existing JSON, appends km hook command, writes merged result; fails fast if user JSON is invalid)
- [x] **HOOK-03**: Compiler writes `/etc/profile.d/km-notify-env.sh` with `KM_NOTIFY_ON_PERMISSION` / `KM_NOTIFY_ON_IDLE` / `KM_NOTIFY_COOLDOWN_SECONDS` / `KM_NOTIFY_EMAIL` only when the corresponding `spec.cli.notify*` profile field is set; unset profile fields produce no env var
- [x] **HOOK-04**: `km shell` and `km agent run` honor `--notify-on-permission` / `--no-notify-on-permission` / `--notify-on-idle` / `--no-notify-on-idle` CLI flags, overriding profile defaults via env vars injected at SSM-launch time (interactive shell uses pre-session SendCommand to write `/etc/profile.d/zz-km-notify.sh`; agent run prepends `export KM_NOTIFY_ON_*=...` lines to the generated bash script)
- [x] **HOOK-05**: `/opt/km/bin/km-notify-hook` honors gate env vars, cooldown (`/tmp/km-notify.last`), builds correct subjects (`[<sandbox-id>] needs permission` / `[<sandbox-id>] idle`) and bodies (Notification: `.message` from stdin payload; Stop: last assistant text from `transcript_path` JSONL), calls `km-send --body <file>` (not stdin, per CLAUDE.md OpenSSL 3.5+ requirement), and never blocks Claude on send failure (always exits 0)

### Slack Notifications

- [x] **SLCK-01**: Profile schema gains five `spec.cli` fields — `notifyEmailEnabled` (*bool), `notifySlackEnabled` (*bool), `notifySlackPerSandbox` (bool), `notifySlackChannelOverride` (string, pattern `^C[A-Z0-9]+$`), `slackArchiveOnDestroy` (*bool); `ValidationError` gains `IsWarning bool` field; five semantic validation rules (mutual-exclusion error, two no-op warnings, channel-ID regex error, neither-channel warning); `km validate` prints warnings without failing
- **SLCK-02**: Compiler extends the inlined `km-notify-hook` heredoc in `pkg/compiler/userdata.go` for parallel email + Slack dispatch (sent_any pattern), adds `KM_NOTIFY_EMAIL_ENABLED`, `KM_NOTIFY_SLACK_ENABLED`, `KM_SLACK_CHANNEL_ID`, `KM_SLACK_BRIDGE_URL` to the `/etc/profile.d/km-notify-env.sh` template emitted via `NotifyEnv`; cooldown updates iff at least one channel succeeded; Phase 62 backward compat preserved (unset `notifyEmailEnabled` → no env var → hook default of `1` keeps email on)
- **SLCK-03**: `km-slack` Go binary at `/opt/km/bin/km-slack` (built via `cmd/km-slack/main.go`, deployed via the sidecar Makefile target + S3 upload, downloaded in user-data); signs canonical JSON envelope with sandbox Ed25519 key from `/sandbox/{id}/signing-key`, POSTs to `$KM_SLACK_BRIDGE_URL`, retries 3 attempts on 5xx/network with 1s/2s/4s backoff, refuses bodies >40 KB; `--body <file>` only (no stdin, OpenSSL 3.5+ constraint per CLAUDE.md)
- **SLCK-04**: `km-slack-bridge` Go Lambda with Function URL (auth=NONE, first publicly-addressable Lambda in this codebase); verifies Ed25519 signature using public key from DynamoDB `km-identities` table (NOT SSM — RESEARCH.md correction #1); enforces ±5-min timestamp window + nonce table `km-slack-bridge-nonces` (10-min TTL, conditional write); channel-mismatch authorization (sandbox `post` rejected if channel ≠ `slack_channel_id` in `km-sandboxes` DynamoDB); action authorization (`archive`/`test` only from operator); dispatches to Slack `chat.postMessage` / `conversations.archive`; returns 503 + Retry-After on Slack 429
- **SLCK-05**: `km slack init` operator command — interactive bootstrap that validates bot token via `auth.test`, writes SSM params `/km/slack/{bot-token,workspace,invite-email,shared-channel-id,bridge-url}`, creates `#km-notifications` shared channel, sends Slack Connect invite to invite-email, deploys bridge Lambda via Terraform apply; companion commands `km slack test` and `km slack status`
- **SLCK-06**: `km create` provisions Slack channel before user-data finalizes — shared mode reads `/km/slack/shared-channel-id`, per-sandbox mode calls `conversations.create` for `#sb-{id}` (with sanitized channel name) + `conversations.inviteShared`, override mode validates the channel exists; channel ID stored in DynamoDB `km-sandboxes.slack_channel_id` and injected into `/etc/profile.d/km-notify-env.sh` as `KM_SLACK_CHANNEL_ID`; failure during channel creation aborts `km create` and tears down partially-created infra
- **SLCK-07**: `km destroy` archive flow — for sandboxes provisioned with `notifySlackPerSandbox: true` and `slackArchiveOnDestroy != false`, posts a final "destroyed at <timestamp>" message and calls `conversations.archive` via the bridge Lambda using operator signing key; archive failure logs warning, does NOT block destroy; missing `/km/slack/bridge-url` skips the Slack archive entirely with a clear log line
- **SLCK-08**: `km doctor` adds two checks — `checkSlackTokenValidity` calls `auth.test` via the bridge Lambda using operator signing, returns WARN on invalid/expired token; `checkStaleSlackChannels` scans `km-sandboxes` for records with `slack_channel_id` whose sandbox no longer exists, returns WARN listing stale channels
- **SLCK-09**: End-to-end live verification — `test/e2e/slack/` harness gated by `RUN_SLACK_E2E=1`; covers shared-mode notification delivery, per-sandbox lifecycle + archive, Phase 62 email backward compat, Slack rate-limit propagation; bot token rotation and Slack Connect invite acceptance covered as documented UAT in `63-10-UAT.md`
- **SLCK-10**: Documentation — `docs/slack-notifications.md` operator guide (workspace prerequisites, `km slack init` walkthrough, profile field reference, troubleshooting matrix, security model, rotation procedures); `CLAUDE.md` updated with new commands (`km slack init/test/status`), env var conventions (`KM_NOTIFY_SLACK_ENABLED`, `KM_SLACK_CHANNEL_ID`, `KM_SLACK_BRIDGE_URL`, `KM_NOTIFY_EMAIL_ENABLED`), and SSM parameter convention (`/km/slack/*`)
- **SLCK-11**: `km create` step 11d runtime injection visibility — Lambda subprocess (`internal/app/cmd/create.go:790-825`) currently silences zerolog (`destroy.go:138`-style `log.Logger = zerolog.New(io.Discard)` in `create.go:189-193`), so all step 11d failure branches (bridge URL missing, terragrunt outputs read failure, instance ID missing, SSM SendCommand failure) discard their warnings. Each branch must emit a visible `fmt.Fprintf(os.Stderr, ...)` line — explicit success (`✓ Slack: channel C... wired into sandbox env`) AND each failure variant — so operators can diagnose why `KM_SLACK_CHANNEL_ID`/`KM_SLACK_BRIDGE_URL` aren't appearing in `/etc/profile.d/km-notify-env.sh` after `km create --remote`. Root cause of the silent failure must also be diagnosed and fixed (likely SSM SendCommand timing — agent may not be reachable when `runner.Output` returns).
- **SLCK-12**: `km destroy` Slack archive auto-trigger — `destroySlackChannel` (`internal/app/cmd/destroy_slack.go`) is invoked at `destroy.go:474` but the archive bridge call evidently doesn't reach Slack (verified during UAT 4b: direct `conversations.archive` returned `ok:true` after destroy completed, proving channel was NOT archived by destroy). Visible logging shipped in `377b588` — diagnose root cause from next-attempt stderr output and fix. Likely cause: final-post bridge call returns an error (Case H at `destroy_slack.go:106`) which skips the archive entirely; instrument WHY the final-post fails (operator key load? SSM access? Bridge URL mismatch?). End state: a `km destroy` of a per-sandbox sandbox with `slackArchiveOnDestroy != false` must archive the channel and emit `✓ Slack: archived channel C...` on stderr.
- **SLCK-13**: Bot-token rotation full E2E — UAT Scen 7 verified the idempotent path (`--force` reuses existing channel after `1ad765c`); the FULL rotation cycle remains unverified: revoke token in Slack App admin → wait for the bridge Lambda's `SSMBotTokenFetcher` 15-min cache TTL to elapse → reissue new token → `km slack init --force --bot-token <new>` → `km slack test` succeeds with the new token. Plan must include a documented operator runbook step + automated test where feasible (cache invalidation via Lambda cold-start trigger as a fallback to the 15-min wait).


### eBPF Network Enforcement

- **EBPF-NET-01**: `pkg/ebpf/` package scaffold with bpf2go pipeline — `go generate` compiles BPF C programs, bpf2go generates Go loader code, `make build` embeds compiled bytecode in km binary
- **EBPF-NET-02**: BPF cgroup/connect4 program intercepts all `connect()` syscalls from sandbox cgroup; looks up destination IP in `BPF_MAP_TYPE_LPM_TRIE` allowlist; returns 0 (EPERM) for disallowed IPs, returns 1 (allow) for allowed IPs
- **EBPF-NET-03**: BPF cgroup/connect4 program rewrites destination IP/port for connections needing L7 inspection (GitHub, Bedrock endpoints) — redirects to `127.0.0.1:{proxy_port}`, stores original dest in hash map keyed by socket cookie (DNAT replacement without iptables)
- **EBPF-NET-04**: BPF cgroup/sendmsg4 program intercepts UDP port 53 DNS queries; redirects to km-dns-resolver daemon listening on localhost
- **EBPF-NET-05**: Userspace km-dns-resolver daemon receives redirected DNS queries, resolves domains, checks against profile allowlist (supports wildcards via suffix matching), returns NXDOMAIN for denied domains, pushes allowed resolved IPs into BPF LPM_TRIE map
- **EBPF-NET-06**: BPF cgroup_skb/egress program provides packet-level defense-in-depth — blocks packets to IPs not in the LPM_TRIE allowlist, catches raw socket traffic and hardcoded IPs that bypass connect()
- **EBPF-NET-07**: BPF ring buffer emits structured deny events to userspace — `{timestamp, pid, src_ip, dst_ip, dst_port, action, layer}` for audit logging
- **EBPF-NET-08**: All BPF programs and maps pinned to `/sys/fs/bpf/km/{sandbox-id}/` — enforcement persists after `km create` exits; `km destroy` unpins and detaches; reattach on restart via `LoadPinnedLink()`/`LoadPinnedMap()`
- **EBPF-NET-09**: Profile schema gains `spec.network.enforcement` field — `proxy` (current iptables DNAT), `ebpf` (pure eBPF), `both` (eBPF primary + proxy for L7); default is `proxy` for backwards compatibility
- **EBPF-NET-10**: TC egress classifier (best-effort) parses TLS ClientHello SNI from first TCP segment of port-443 connections; validates hostname against BPF hash map; passes traffic where SNI is not in first segment (no TCP reassembly — Chrome large ClientHellos may be segmented)
- **EBPF-NET-11**: Compiler emits eBPF enforcement setup in EC2 user-data when profile has `enforcement: ebpf | both` — starts km-dns-resolver daemon, attaches BPF programs to sandbox cgroup, populates initial allowlist from profile
- **EBPF-NET-12**: Root-in-sandbox bypass prevention verified — process with `CAP_NET_ADMIN` inside sandbox can flush iptables (irrelevant) but cannot connect to blocked IP (EPERM from cgroup/connect4); process cannot detach BPF programs (no `CAP_BPF` in host namespace)

### eBPF TLS Uprobe Observability

- **EBPF-TLS-01**: `pkg/ebpf/tls/` package with per-library probe modules — each module discovers library path, resolves symbol offsets, attaches uprobes/uretprobes via `link.OpenExecutable()`, reads plaintext via ring buffer
- **EBPF-TLS-02**: OpenSSL module hooks `SSL_write`/`SSL_write_ex` entry + `SSL_read`/`SSL_read_ex` entry+return on `libssl.so.3`; auto-detects OpenSSL version from `.rodata` for struct offset selection; handles OpenSSL 1.1.x and 3.x
- **EBPF-TLS-03**: GnuTLS module hooks `gnutls_record_send` entry + `gnutls_record_recv` entry+return on `libgnutls.so`
- **EBPF-TLS-04**: NSS module hooks `PR_Write`/`PR_Send` entry + `PR_Read`/`PR_Recv` entry+return on `libnspr4.so`
- **EBPF-TLS-05**: Go crypto/tls module hooks `crypto/tls.(*Conn).Write` and `(*Conn).Read` in target Go binaries — disassembles function to find all `RET` offsets via `golang.org/x/arch`, attaches uprobe at each RET instead of uretprobe; detects Go ABI version (stack vs register)
- **EBPF-TLS-06**: rustls module hooks `Writer::write` entry + `Reader::read` entry+return in Rust binaries — discovers symbols via ELF scan for `rustc` marker + `rustls` pattern matching on mangled v0 names; handles inverted read path
- **EBPF-TLS-07**: Connection correlation via kprobe on `connect()`/`accept()` populates `(pid, fd) → {remote_ip, remote_port}` BPF hash map; SSL hooks extract fd from library struct or connection map; ring buffer events include remote endpoint
- **EBPF-TLS-08**: Ring buffer events carry `{timestamp_ns, pid, tid, fd, remote_ip, remote_port, direction, library_type, payload_len, payload[≤16384 bytes]}` — 16KB aligned with TLS max fragment length
- **EBPF-TLS-09**: Userspace consumer reads ring buffer, reassembles HTTP request/response pairs, routes to registered handlers — budget metering handler extracts token counts using existing `ExtractBedrockTokens()`/`ExtractAnthropicTokens()`
- **EBPF-TLS-10**: Budget metering via uprobes replaces MITM proxy metering when tlsCapture is enabled — Bedrock and Anthropic response bodies parsed for token usage, routed through existing `IncrementAISpend()` DynamoDB path
- **EBPF-TLS-11**: Profile schema gains `spec.observability.tlsCapture` — `enabled` (bool), `libraries` (array of openssl/gnutls/nss/go/rustls/all), `capturePayloads` (bool, default false for metadata-only metering)
- **EBPF-TLS-12**: Library discovery at sandbox startup scans `/proc/<pid>/maps` for shared libraries and ELF headers of binaries; attaches probes to each discovered library/binary; logs which libraries instrumented
- **EBPF-TLS-13**: Per-library toggle via BPF map `(cgroup_id, library_type) → enabled`; userspace can enable/disable specific libraries without detaching probes; `km status` shows capture status
- **EBPF-TLS-14**: GitHub repo path extraction from captured HTTPS plaintext — HTTP request paths parsed to extract `owner/repo`; compared against profile allowedRepos; violations logged to audit trail

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Advanced Profiles

- **PROF-01**: Profile composition (policy bundles) beyond simple extends
- **PROF-02**: Profile versioning with migration support

### Cost & Operations

- **COST-02**: Warm pool / pre-provisioned sandboxes for faster startup
- **COST-03**: `km gc` for orphan detection and cleanup

### Platform Expansion

- **PLAT-01**: Kubernetes substrate option (k8s/EKS) — natural v2 extension after EC2 and ECS are working
- **PLAT-02**: Docker/local substrate for development
- **PLAT-03**: Sandbox REST API server (persistent control plane)
- **PLAT-04**: Multi-cloud support (GCP, Azure)

### Sandbox Identity Customization

- **PREFIX-01**: Profile schema supports optional `metadata.prefix` field with validation (`^[a-z][a-z0-9]{0,11}$`)
- **PREFIX-02**: `GenerateSandboxID()` accepts a prefix parameter — generates `{prefix}-{8 hex}` IDs
- **PREFIX-03**: All sandbox ID validation/matching patterns accept any valid prefix, not just `sb-`
- **PREFIX-04**: Compiler, CLI, and Lambda code use sandbox ID as-is — no component hardcodes the `sb-` prefix
- **PREFIX-05**: Backwards compatible — profiles without `metadata.prefix` default to `sb`
- **ALIAS-01**: `km create --alias <name>` stores alias in S3 metadata.json; all commands resolve alias to sandbox ID via S3 scan
- **ALIAS-02**: Profile-level `metadata.alias` template auto-generates `{alias}-1`, `{alias}-2` etc. by scanning active sandboxes
- **ALIAS-03**: `--alias` flag overrides profile-level template; alias freed on destroy for reuse
- **ALIAS-04**: `km list` displays alias column; `ResolveSandboxRef` resolves aliases (future: DynamoDB index for O(1) lookup)

### Advanced Policy

- **PLCY-01**: OPA / policy engine integration for enterprise compliance
- **PLCY-02**: Multi-tenancy with RBAC and session ownership

## Out of Scope

Explicitly excluded. Documented to prevent scope creep.

| Feature | Reason |
|---------|--------|
| Docker/local substrate | Development convenience only; adds complexity without isolation guarantees; v2 candidate |
| Kubernetes substrate (k8s/EKS) | EC2 and ECS cover v1 use cases; k8s is a near-future v2 extension (PLAT-01) |
| Multi-cloud implementation | Schema is cloud-neutral but v1 implements AWS only |
| Full OPA policy engine | YAML allowlists cover 90% of use cases; OPA adds operator complexity |
| Real-time collaboration / multi-user editing | Single-operator model for v1; multi-tenancy is a major scope increase |
| Interactive terminal / SSH into sandboxes | Creates "pet server" behavior; conflicts with ephemeral destroy-on-TTL model |
| Deny-list network policy | Allowlists are more auditable and secure; deny-lists are incomplete by definition |
| Mobile app | Web ConfigUI is sufficient |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| SCHM-01 | Phase 1 | Complete |
| SCHM-02 | Phase 1 | Complete |
| SCHM-03 | Phase 1 | Complete |
| SCHM-04 | Phase 7 | Complete |
| SCHM-05 | Phase 7 | Complete |
| INFR-01 | Phase 1 | Complete |
| INFR-02 | Phase 1 | Complete |
| INFR-03 | Phase 1 | Complete |
| INFR-04 | Phase 1 | Complete |
| INFR-05 | Phase 1 | Complete |
| INFR-06 | Phase 1 | Complete |
| INFR-07 | Phase 1 | Complete |
| INFR-08 | Phase 1 | Complete |
| PROV-01 | Phase 2 | Complete |
| PROV-02 | Phase 2 | Complete |
| PROV-08 | Phase 2 | Complete |
| PROV-09 | Phase 2 | Complete |
| PROV-10 | Phase 2 | Complete |
| PROV-11 | Phase 2 | Complete |
| PROV-12 | Phase 2 | Complete |
| NETW-01 | Phase 2 | Complete |
| NETW-04 | Phase 2 | Complete |
| NETW-05 | Phase 2 | Complete |
| NETW-06 | Phase 2 | Complete |
| NETW-07 | Phase 2 | Complete |
| NETW-08 | Phase 2 | Complete |
| PROV-03 | Phase 11 | Complete |
| PROV-04 | Phase 11 | Complete |
| PROV-05 | Phase 11 | Complete |
| PROV-06 | Phase 11 | Complete |
| PROV-07 | Phase 3 | Complete |
| NETW-02 | Phase 3 | Complete |
| NETW-03 | Phase 3 | Complete |
| OBSV-01 | Phase 3 | Complete |
| OBSV-02 | Phase 3 | Complete |
| OBSV-03 | Phase 3 | Complete |
| OBSV-04 | Phase 4 | Complete |
| OBSV-05 | Phase 4 | Complete |
| OBSV-06 | Phase 12 | Complete |
| OBSV-07 | Phase 4 | Complete |
| OBSV-08 | Phase 3 | Complete |
| OBSV-09 | Phase 3 | Complete |
| OBSV-10 | Phase 3 | Complete |
| PROV-13 | Phase 4 | Complete |
| MAIL-01 | Phase 4 | Complete |
| MAIL-02 | Phase 4 | Complete |
| MAIL-03 | Phase 4 | Complete |
| MAIL-04 | Phase 4 | Complete |
| MAIL-05 | Phase 4 | Complete |
| CFUI-01 | Phase 5 | Complete |
| CFUI-02 | Phase 5 | Complete |
| CFUI-03 | Phase 5 | Complete |
| CFUI-04 | Phase 5 | Complete |
| CONF-01 | Phase 6 | Complete |
| CONF-02 | Phase 6 | Complete |
| CONF-03 | Phase 6 | Complete |
| CONF-04 | Phase 6 | Complete |
| CONF-05 | Phase 6 | Complete |
| BUDG-01 | Phase 6 | Complete |
| BUDG-02 | Phase 6 | Complete |
| BUDG-03 | Phase 6 | Complete |
| BUDG-04 | Phase 6 | Complete |
| BUDG-05 | Phase 6 | Complete |
| BUDG-06 | Phase 6 | Complete |
| BUDG-07 | Phase 19 | Complete |
| BUDG-08 | Phase 19 | Complete |
| BUDG-10 | Phase 20 | Complete |
| OPER-01 | Phase 20 | Complete |
| BUDG-09 | Phase 6 | Complete |
| PROV-06 | Phase 7 | Complete |
| OBSV-07 | Phase 7 | Complete |
| OBSV-09 | Phase 7 | Complete |
| CONF-03 | Phase 7 | Complete |

| EBPF-NET-01 | Phase 40 | Planned |
| EBPF-NET-02 | Phase 40 | Planned |
| EBPF-NET-03 | Phase 40 | Planned |
| EBPF-NET-04 | Phase 40 | Planned |
| EBPF-NET-05 | Phase 40 | Planned |
| EBPF-NET-06 | Phase 40 | Planned |
| EBPF-NET-07 | Phase 40 | Planned |
| EBPF-NET-08 | Phase 40 | Planned |
| EBPF-NET-09 | Phase 40 | Planned |
| EBPF-NET-10 | Phase 40 | Planned |
| EBPF-NET-11 | Phase 40 | Planned |
| EBPF-NET-12 | Phase 40 | Planned |
| EBPF-TLS-01 | Phase 41 | Planned |
| EBPF-TLS-02 | Phase 41 | Planned |
| EBPF-TLS-03 | Phase 41 | Planned |
| EBPF-TLS-04 | Phase 41 | Planned |
| EBPF-TLS-05 | Phase 41 | Planned |
| EBPF-TLS-06 | Phase 41 | Planned |
| EBPF-TLS-07 | Phase 41 | Planned |
| EBPF-TLS-08 | Phase 41 | Planned |
| EBPF-TLS-09 | Phase 41 | Planned |
| EBPF-TLS-10 | Phase 41 | Planned |
| EBPF-TLS-11 | Phase 41 | Planned |
| EBPF-TLS-12 | Phase 41 | Planned |
| EBPF-TLS-13 | Phase 41 | Planned |
| EBPF-TLS-14 | Phase 41 | Planned |
| HOOK-01 | Phase 62 | Complete |
| HOOK-02 | Phase 62 | Complete |
| HOOK-03 | Phase 62 | Complete |
| HOOK-04 | Phase 62 | Complete |
| HOOK-05 | Phase 62 | Complete |
| SLCK-01 | Phase 63 | Complete |
| SLCK-02 | Phase 63 | Planned |
| SLCK-03 | Phase 63 | Planned |
| SLCK-04 | Phase 63 | Planned |
| SLCK-05 | Phase 63 | Planned |
| SLCK-06 | Phase 63 | Planned |
| SLCK-07 | Phase 63 | Planned |
| SLCK-08 | Phase 63 | Planned |
| SLCK-09 | Phase 63 | Planned |
| SLCK-10 | Phase 63 | Planned |
| SLCK-11 | Phase 63.1 | Complete |
| SLCK-12 | Phase 63.1 | Complete |
| SLCK-13 | Phase 63.1 | Complete |

**Coverage:**
- v1 requirements: 81 total
- Mapped to phases: 71
- Unmapped: 0
- eBPF requirements (Phase 40-41): 26 total

---
*Requirements defined: 2026-03-21*
*Last updated: 2026-03-21 — PROV-09, PROV-10 added; ECS moved from Out of Scope to v1; k8s added to v2; Docker/local remains out of scope*
*Last updated: 2026-03-21 — INFR-08 added: no cross-repo dependency on defcon.run.34; all modules and app code must be copied and adapted into Klanker Maker repo*
*Last updated: 2026-03-21 — PROV-11, PROV-12, PROV-13 added: spot instances by default for EC2 and ECS, graceful interruption handling with artifact upload*
*Last updated: 2026-03-21 — OBSV-08, OBSV-09, OBSV-10 added: OTel tracing sidecar, MLflow experiment tracking per sandbox session, trace context propagation through proxy sidecars*
*Last updated: 2026-03-22 — COST-01 promoted from v2, expanded into BUDG-01 through BUDG-09: per-sandbox budget enforcement with DynamoDB global table, http-proxy Bedrock metering, threshold warnings, hard enforcement, operator top-up*
*Last updated: 2026-04-26 — HOOK-01..HOOK-05 added: operator-notify hook for Claude Code Notification (permission) and Stop (idle) events; profile-driven via spec.cli.notifyOn{Permission,Idle}/notifyCooldownSeconds/notificationEmailAddress with --notify-on-{permission,idle} CLI overrides on km shell and km agent run (Phase 62)*
*Last updated: 2026-04-29 — SLCK-01..SLCK-10 added: Slack-notify hook for Claude Code permission and idle events extending Phase 62 with parallel Slack delivery via klankermaker.ai-owned Pro workspace; profile-driven via spec.cli.notifyEmailEnabled/notifySlackEnabled/notifySlackPerSandbox/notifySlackChannelOverride/slackArchiveOnDestroy; bridge Lambda + km-slack binary + km slack init/test/status commands; ValidationError gains IsWarning field for non-blocking validation messages (Phase 63)*
