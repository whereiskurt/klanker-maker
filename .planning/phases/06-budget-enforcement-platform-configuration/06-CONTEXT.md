# Phase 6: Budget Enforcement & Platform Configuration - Context

**Gathered:** 2026-03-22
**Status:** Ready for planning

<domain>
## Phase Boundary

Two concerns: (1) Make the platform fully configurable so forks work out of the box — domain, AWS accounts, SSO, DNS delegation all derived from a single config file. (2) Per-sandbox dollar budgets for compute and AI with real-time tracking, threshold warnings, and hard enforcement via proxy and Lambda.

</domain>

<decisions>
## Implementation Decisions

### km configure flow
- Config file lives at `km-config.yaml` in repo root — not in home directory, not gitignored by default. Teams share the same config
- `km configure` is a guided wizard — detects account topology (2-account vs 3-account), shows what needs to happen for DNS delegation when management ≠ application account, operator confirms each step
- Separate `km bootstrap` command for infrastructure creation (S3 bucket, DynamoDB lock table, KMS key) — `km configure` writes config only, `km bootstrap` provisions
- apiVersion domain in profiles is permissive — validation ignores the domain part, only checks version suffix. Forks can use any domain in apiVersion without breaking validation
- All ~20 hardcoded `klankermaker.ai` references replaced with config-derived values: SES email domain, JSON Schema `$id`, ConfigUI branding, profile templates

### DNS delegation (from prior discussion)
- When management ≠ application account: create hosted zone in app account, add NS delegation in management account. `km configure` detects this and guides through it
- When management = application account: no delegation needed, skip
- Terraform and application accounts are often the same (2-account topology)

### Budget tracking & storage
- Single DynamoDB global table extending defcon.run.34 pattern — PK=SANDBOX#{id}, SK=BUDGET#compute / BUDGET#ai#{model} / BUDGET#limits. One table, one replication config
- Compute spend tracked by Lambda pull — runs every minute, calculates elapsed × spot rate, writes to DynamoDB. No agent-side awareness needed
- AI/token spend tracked per-request — http-proxy increments DynamoDB atomically (UpdateItem ADD) after each Bedrock response. Real-time tracking
- Proxy caches budget limits locally with 10-second TTL — refreshes from DynamoDB every 10s. At worst 10s of overspend before enforcement. Top-up takes effect within 10s

### Enforcement & top-up UX
- At 100% AI budget, proxy returns 403 with JSON error body including spend details, limit, model, and `km budget add` command — agent can parse and report to operator
- At 80% compute/AI budget, operator gets SES warning email. Inside the sandbox, a `KM_BUDGET_REMAINING` env var is updated periodically so agents can check and gracefully wind down
- `km budget add` updates DynamoDB limits and auto-resumes — if EC2 was stopped, calls StartInstances; if proxy was blocking, next cache refresh (10s) unblocks. One command does everything
- ConfigUI dashboard shows real-time budget spend — the Phase 5 placeholder columns become real (compute $/limit, AI $/limit), color-coded green/yellow/red, polled with 10s dashboard refresh

### Bedrock proxy interception
- Anthropic models only for v1 — intercept bedrock-runtime InvokeModel for `anthropic.claude-*` model IDs. Extensible later
- Host pattern match for detection — match requests to `bedrock-runtime.*.amazonaws.com`. URL path contains model ID
- Streaming responses: parse the final metrics event (contains inputTokenCount/outputTokenCount). Pass stream through, read final chunk. No buffering, no latency impact
- Model pricing from AWS Price List API, cached daily at proxy startup. Refresh every 24h. Requires pricing API IAM permission

### Claude's Discretion
- DynamoDB table name and partition key naming conventions
- KM_BUDGET_REMAINING update mechanism (SSM parameter write vs file write vs proxy sidecar API)
- Exact km configure wizard prompts and validation
- CSS color thresholds for budget display (green/yellow/red ranges)
- Terraform module structure for DynamoDB global table and budget Lambda
- How km bootstrap detects already-provisioned resources (idempotent)

</decisions>

<specifics>
## Specific Ideas

- The 403 response body should be parseable by agents: `{"error": "ai_budget_exhausted", "spent": 5.00, "limit": 5.00, "model": "claude-sonnet", "topUp": "km budget add sb-xxx --ai 5"}`
- Budget columns in ConfigUI dashboard should match the existing table design — integrate naturally with the Phase 5 sandbox table
- defcon.run.34 single-table DynamoDB pattern is the source for table design — same PK/SK conventions
- `km configure` should feel like `aws configure` or `gh auth login` — familiar interactive setup

</specifics>

<code_context>
## Existing Code Insights

### Reusable Assets
- `sidecars/http-proxy/httpproxy/`: Existing proxy with host-based allowlisting — Bedrock interception adds a handler for `bedrock-runtime.*.amazonaws.com` hosts
- `pkg/aws/ses.go`: `SendLifecycleNotification` — reuse for 80% budget warning emails
- `pkg/aws/artifacts.go`: `S3PutAPI` narrow interface pattern — same pattern for DynamoDB budget writes
- `cmd/configui/handlers.go`: Dashboard table with placeholder budget columns — wire real data
- `internal/app/cmd/create.go`, `destroy.go`: Hardcoded `sandboxes.klankermaker.ai` — replace with config-derived domain
- `infra/live/site.hcl`: DynamoDB lock table config — extend pattern for budget table
- `pkg/profile/types.go`: `apiVersion` field — make validation domain-agnostic

### Established Patterns
- Narrow AWS interfaces (`S3PutAPI`, `SESV2API`, `SSMAPI`) — follow for DynamoDB (`BudgetAPI`)
- `go:embed` for schemas/templates — config template could use same pattern
- `KM_*` env vars for runtime config — `KM_BUDGET_REMAINING` follows this convention
- Lambda handlers at `cmd/ttl-handler/` — budget enforcement Lambda follows same structure

### Integration Points
- http-proxy: add Bedrock response interceptor alongside existing allowlist logic
- Lambda: new budget-enforcement Lambda at `cmd/budget-enforcer/` or extend TTL handler
- ConfigUI: wire budget data into dashboard template (Phase 5 placeholder columns)
- `km status`: extend output to show budget spend breakdown
- `km configure`: new Cobra command in `internal/app/cmd/`
- `km bootstrap`: new Cobra command for infra provisioning
- `km budget add`: new Cobra command for top-up

</code_context>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 06-budget-enforcement-platform-configuration*
*Context gathered: 2026-03-22*
