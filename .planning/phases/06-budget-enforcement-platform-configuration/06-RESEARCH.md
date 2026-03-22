# Phase 6: Budget Enforcement & Platform Configuration - Research

**Researched:** 2026-03-22
**Domain:** DynamoDB budget tracking, Bedrock proxy interception, platform configurability, IAM enforcement
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**km configure flow:**
- Config file lives at `km-config.yaml` in repo root — not in home directory, not gitignored by default. Teams share the same config
- `km configure` is a guided wizard — detects account topology (2-account vs 3-account), shows what needs to happen for DNS delegation when management ≠ application account, operator confirms each step
- Separate `km bootstrap` command for infrastructure creation (S3 bucket, DynamoDB lock table, KMS key) — `km configure` writes config only, `km bootstrap` provisions
- apiVersion domain in profiles is permissive — validation ignores the domain part, only checks version suffix. Forks can use any domain in apiVersion without breaking validation
- All ~20 hardcoded `klankermaker.ai` references replaced with config-derived values: SES email domain, JSON Schema `$id`, ConfigUI branding, profile templates

**DNS delegation (from prior discussion):**
- When management ≠ application account: create hosted zone in app account, add NS delegation in management account. `km configure` detects this and guides through it
- When management = application account: no delegation needed, skip
- Terraform and application accounts are often the same (2-account topology)

**Budget tracking & storage:**
- Single DynamoDB global table extending defcon.run.34 pattern — PK=SANDBOX#{id}, SK=BUDGET#compute / BUDGET#ai#{model} / BUDGET#limits. One table, one replication config
- Compute spend tracked by Lambda pull — runs every minute, calculates elapsed × spot rate, writes to DynamoDB. No agent-side awareness needed
- AI/token spend tracked per-request — http-proxy increments DynamoDB atomically (UpdateItem ADD) after each Bedrock response. Real-time tracking
- Proxy caches budget limits locally with 10-second TTL — refreshes from DynamoDB every 10s. At worst 10s of overspend before enforcement. Top-up takes effect within 10s

**Enforcement & top-up UX:**
- At 100% AI budget, proxy returns 403 with JSON error body including spend details, limit, model, and `km budget add` command — agent can parse and report to operator
- At 80% compute/AI budget, operator gets SES warning email. Inside the sandbox, a `KM_BUDGET_REMAINING` env var is updated periodically so agents can check and gracefully wind down
- `km budget add` updates DynamoDB limits and auto-resumes — if EC2 was stopped, calls StartInstances; if proxy was blocking, next cache refresh (10s) unblocks. One command does everything
- ConfigUI dashboard shows real-time budget spend — the Phase 5 placeholder columns become real (compute $/limit, AI $/limit), color-coded green/yellow/red, polled with 10s dashboard refresh

**Bedrock proxy interception:**
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

### Deferred Ideas (OUT OF SCOPE)
None — discussion stayed within phase scope
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| CONF-01 | All platform-specific values defined in single config file (`km-config.yaml`) | Config struct design, viper integration with existing config.go |
| CONF-02 | Domain name configurable — SES, JSON Schema $id, apiVersion, ConfigUI branding all derive from configured domain | String replacement locations catalogued, schema.go $id, ses.go domain param already accepts domain arg |
| CONF-03 | AWS account numbers and SSO start URL configurable — referenced by Terragrunt hierarchy, IAM policies, km CLI | site.hcl domain hardcode, config.go extension needed |
| CONF-04 | `km configure` wizard command — walks through domain, accounts, region, SSO setup | Cobra command pattern established in internal/app/cmd/ |
| BUDG-01 | Per-sandbox budget spec in profile YAML (spec.budget.compute.maxSpendUSD, spec.budget.ai.maxSpendUSD) | types.go BudgetSpec addition, schema update |
| BUDG-02 | DynamoDB global table for budget storage, replicated to all agent regions | DynamoDB v2 SDK needed; Terraform resource pattern researched |
| BUDG-03 | Compute spend: instance type spot rate × elapsed minutes, from AWS Price List API at sandbox creation | Pricing API SDK needed; EventBridge 1-minute Lambda pattern matches TTL Lambda |
| BUDG-04 | AI/token spend: http-proxy intercepts Bedrock InvokeModel responses, extracts token usage, increments DynamoDB | MITM proxy pattern required for HTTPS body interception; SSE event parsing |
| BUDG-05 | Model pricing from AWS Price List API cached daily | Pricing API `GetProducts` with AmazonBedrock service filter |
| BUDG-06 | 80% threshold warning email via SES using existing SendLifecycleNotification | Already callable; just add budget event type |
| BUDG-07 | Dual-layer enforcement: proxy 403 at 100% AI + Lambda IAM revocation backstop; EC2 StopInstances at compute limit | EC2 StopInstances in SDK; IAM DetachRolePolicy for Bedrock |
| BUDG-08 | `km budget add` top-up command updates DynamoDB, restores IAM, proxy unblocks on next cache refresh | EC2 StartInstances; IAM AttachRolePolicy; DynamoDB UpdateItem |
| BUDG-09 | `km status` shows current spend vs budget for both pools including per-model AI breakdown | DynamoDB Query on BUDGET# SK prefix; existing status.go extensible |
</phase_requirements>

---

## Summary

Phase 6 splits cleanly into two tracks. The **configuration track** is primarily a refactoring exercise: replace ~20 hardcoded `klankermaker.ai` references with config-derived values, extend the existing `internal/app/config/config.go` Config struct with domain/account fields, implement a `km configure` wizard Cobra command, and wire the config into Terragrunt HCL files. The complexity is breadth (touching many files), not depth. The existing `viper`-based config loader is already the right pattern.

The **budget enforcement track** introduces new infrastructure: a DynamoDB global table for spend storage, AWS SDK modules not yet in `go.mod` (DynamoDB, Bedrock Runtime, Pricing), a Bedrock MITM interception layer in the http-proxy sidecar, a new budget-enforcer Lambda, and three new CLI commands (`km configure`, `km bootstrap`, `km budget add`). The most architecturally challenging piece is Bedrock response interception: reading response bodies through an HTTPS CONNECT tunnel requires goproxy MITM mode (generating TLS certificates on the fly), which means installing a trusted CA in the sandbox. This is a significant departure from the current SNI-only CONNECT proxy.

The key architectural constraint is that Claude streaming responses on Bedrock use server-sent events (SSE). The `input_tokens` field is in the `message_start` event; `output_tokens` (cumulative, final) is in the `message_delta` event. The proxy must parse the SSE stream to find `message_delta` with `usage.output_tokens` while passing all bytes through to the client unchanged.

**Primary recommendation:** Implement budget enforcement in two sub-phases — first the DynamoDB table + compute tracking (simpler, no proxy changes), then the Bedrock MITM interception (requires CA injection and streaming SSE parsing). Run configuration track in parallel since it is independent.

---

## Standard Stack

### Core (new additions needed)
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | latest (v2 module) | DynamoDB CRUD for budget records | Already using aws-sdk-go-v2 for all other AWS services |
| `github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue` | latest | Marshal/unmarshal DynamoDB items | Standard helper for v2 DynamoDB; avoid hand-rolling attribute conversion |
| `github.com/aws/aws-sdk-go-v2/service/bedrockruntime` | latest | Type definitions for Bedrock streaming events | Needed for SSE event type constants and response body parsing |
| `github.com/aws/aws-sdk-go-v2/service/pricing` | latest | AWS Price List API for spot/model rates | Official SDK for GetProducts; Bedrock and EC2 pricing |
| `gopkg.in/yaml.v3` | already indirect | km-config.yaml parsing | Already in go.sum via viper |

### Supporting (already in go.mod, new usage)
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/aws/aws-sdk-go-v2/service/iam` | not yet explicit | Attach/Detach role policies for enforcement backstop | Budget enforcement Lambda only |
| `github.com/aws/aws-sdk-go-v2/service/ec2` | already in go.mod | StopInstances / StartInstances for compute enforcement | km budget add and budget-enforcer Lambda |
| `github.com/spf13/viper` | already in go.mod | km-config.yaml loading | Extend existing config.go |
| `github.com/elazarl/goproxy` | v1.8.2 already in go.mod | HTTPS MITM for Bedrock interception | Must set `HandleConnect` to `goproxy.MitmConnect` for Bedrock hosts |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| DynamoDB for budget state | Redis/ElastiCache | DynamoDB global tables are serverless and replicate; Redis requires always-on cluster |
| goproxy MITM | Custom TCP MITM | goproxy already in the codebase; MITM mode is built in |
| AWS Pricing API | Hardcoded price table | Pricing API is accurate but rate-limited and complex; hybrid (API with fallback static table) recommended |

**Installation:**
```bash
# Add new SDK modules
go get github.com/aws/aws-sdk-go-v2/service/dynamodb
go get github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue
go get github.com/aws/aws-sdk-go-v2/service/bedrockruntime
go get github.com/aws/aws-sdk-go-v2/service/pricing
```

---

## Architecture Patterns

### Recommended Project Structure (new files)
```
pkg/aws/
├── budget.go          # BudgetAPI interface, GetBudget, IncrementSpend, SetLimits
├── budget_test.go     # Unit tests with mockBudgetAPI
├── pricing.go         # PricingAPI interface, GetSpotRate, GetBedrockModelRate
├── pricing_test.go
pkg/profile/
└── types.go           # Add BudgetSpec to Spec struct (BUDG-01)
internal/app/
├── config/
│   └── config.go      # Extend Config: Domain, ManagementAccountID, AppAccountID, SSOStartURL, Region
└── cmd/
    ├── configure.go   # km configure wizard (CONF-04)
    ├── bootstrap.go   # km bootstrap infrastructure provisioner
    └── budget.go      # km budget add (BUDG-08)
cmd/
└── budget-enforcer/   # New Lambda: compute spend check + IAM revocation backstop
    └── main.go
sidecars/http-proxy/
└── httpproxy/
    ├── proxy.go       # Extend: add MITM handler for bedrock-runtime hosts
    └── bedrock.go     # Bedrock SSE parser, token extractor, DynamoDB incrementer
infra/modules/
└── dynamodb-budget/   # Terraform module: global table + replication
infra/live/{region}/
└── dynamodb-budget/   # Terragrunt wiring
km-config.yaml         # New: shared team config file (repo root)
```

### Pattern 1: DynamoDB Single-Table Budget Design
**What:** One DynamoDB table stores all budget state for all sandboxes using composite key design. Global table V2 replication via the `aws_dynamodb_table` replica block (not the legacy `aws_dynamodb_global_table` resource).
**When to use:** For all budget reads and writes.

DynamoDB key design (following defcon.run.34 pattern):
```
PK                    | SK                        | Attributes
SANDBOX#sb-abc123     | BUDGET#limits             | computeLimit, aiLimit, warningThreshold
SANDBOX#sb-abc123     | BUDGET#compute            | spentUSD, lastUpdated, instanceType, spotRate
SANDBOX#sb-abc123     | BUDGET#ai#claude-sonnet-4 | spentUSD, inputTokens, outputTokens, lastUpdated
SANDBOX#sb-abc123     | BUDGET#ai#claude-haiku-3  | spentUSD, inputTokens, outputTokens, lastUpdated
```

```go
// Source: DynamoDB v2 SDK docs + defcon.run.34 pattern
// Atomic increment of AI spend (thread-safe, no read-modify-write needed)
_, err = client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
    TableName: aws.String(tableName),
    Key: map[string]types.AttributeValue{
        "PK": &types.AttributeValueMemberS{Value: "SANDBOX#" + sandboxID},
        "SK": &types.AttributeValueMemberS{Value: "BUDGET#ai#" + modelID},
    },
    UpdateExpression: aws.String("ADD spentUSD :delta, inputTokens :in, outputTokens :out SET lastUpdated = :ts"),
    ExpressionAttributeValues: map[string]types.AttributeValue{
        ":delta": &types.AttributeValueMemberN{Value: strconv.FormatFloat(cost, 'f', 6, 64)},
        ":in":    &types.AttributeValueMemberN{Value: strconv.Itoa(inputTokens)},
        ":out":   &types.AttributeValueMemberN{Value: strconv.Itoa(outputTokens)},
        ":ts":    &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
    },
    ReturnValues: types.ReturnValueUpdatedNew, // Returns new values so proxy can check limit
})
```

### Pattern 2: Bedrock HTTPS MITM Interception
**What:** goproxy MITM mode terminates TLS for Bedrock hosts, reads the SSE response body, extracts token usage from the final `message_delta` event, then passes bytes through unchanged.
**When to use:** Only for `bedrock-runtime.*.amazonaws.com` hosts.

**Critical context:** The current http-proxy uses CONNECT passthrough (OkConnect). Bedrock interception requires switching to `goproxy.MitmConnect` for Bedrock hosts. This requires a CA certificate to be installed in the sandbox (system trust store) so the proxy can generate valid TLS certificates on the fly.

```go
// Source: elazarl/goproxy README + https://github.com/elazarl/goproxy/blob/master/https.go
// In httpproxy/proxy.go NewProxy():

// Bedrock hosts get MITM'd; all others get OkConnect (existing behavior preserved).
proxy.OnRequest(goproxy.ReqHostMatches(regexp.MustCompile(`bedrock-runtime\..*\.amazonaws\.com`))).
    HandleConnect(goproxy.AlwaysMitm)

// Intercept Bedrock responses after MITM establishes.
proxy.OnResponse(goproxy.ReqHostMatches(regexp.MustCompile(`bedrock-runtime\..*\.amazonaws\.com`))).
    DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
        // Extract model ID from URL path: /model/anthropic.claude-*/invoke[-with-response-stream]
        // Parse SSE stream from resp.Body
        // On message_delta: extract output_tokens; on message_start: extract input_tokens
        // Increment DynamoDB atomically
        // Replace resp.Body with io.NopCloser(bytes.NewReader(captured))
        return resp
    })
```

**SSE streaming token event format (verified from Anthropic docs):**
```
event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":25,"output_tokens":1},...}}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}
```
- `message_start.message.usage.input_tokens` = prompt token count
- `message_delta.usage.output_tokens` = final cumulative output token count (use this one)
- Both fields guaranteed to be present in every non-error response

**Latency impact:** The proxy must buffer the full SSE body to parse it, then return a new reader. For non-streaming InvokeModel calls (no SSE), parse JSON response body for `usage.input_tokens`/`usage.output_tokens`. For streaming InvokeModelWithResponseStream, buffer and re-emit. The buffering adds memory proportional to response size but no network latency (happens concurrently with client reading).

### Pattern 3: Proxy Budget Cache (10-second TTL)
**What:** In-process cache in the proxy sidecar avoids a DynamoDB read on every Bedrock request.
**When to use:** Budget limit check before each Bedrock proxied request.

```go
// httpproxy/bedrock.go
type budgetCache struct {
    mu      sync.Mutex
    limits  map[string]*budgetEntry // key: sandboxID
}

type budgetEntry struct {
    computeLimit float64
    aiLimit      float64
    aiSpent      float64
    fetchedAt    time.Time
}

func (c *budgetCache) get(sandboxID string) (*budgetEntry, bool) {
    c.mu.Lock()
    defer c.mu.Unlock()
    e, ok := c.limits[sandboxID]
    if !ok || time.Since(e.fetchedAt) > 10*time.Second {
        return nil, false // cache miss — caller fetches from DynamoDB
    }
    return e, true
}
```

### Pattern 4: Config File (km-config.yaml)
**What:** Single YAML file at repo root holds all platform-specific values. Loaded by `internal/app/config/config.go` via viper. Committed to the repo (shared by team), not gitignored.

```yaml
# km-config.yaml (repo root)
domain: klankermaker.ai          # base domain; all derived addresses use this
accounts:
  management: "123456789012"
  terraform: "234567890123"      # often same as application for 2-account topology
  application: "234567890123"
sso:
  startURL: "https://my-org.awsapps.com/start"
  region: us-east-1
region: us-east-1
```

Config struct extension in `internal/app/config/config.go`:
```go
type Config struct {
    // ... existing fields ...
    Domain              string   // base domain, e.g. "klankermaker.ai"
    ManagementAccountID string
    TerraformAccountID  string
    ApplicationAccountID string
    SSOStartURL         string
    PrimaryRegion       string
    // Budget tracking
    BudgetTableName     string   // DynamoDB table name; default "km-budgets"
}
```

### Pattern 5: Narrow BudgetAPI Interface
**What:** Follow the established `S3PutAPI`, `SESV2API` narrow interface pattern for DynamoDB budget operations.

```go
// pkg/aws/budget.go
type BudgetAPI interface {
    UpdateItem(ctx context.Context, input *dynamodb.UpdateItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
    GetItem(ctx context.Context, input *dynamodb.GetItemInput, optFns ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
    Query(ctx context.Context, input *dynamodb.QueryInput, optFns ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}
// *dynamodb.Client satisfies this directly.
```

### Pattern 6: Terraform DynamoDB Global Table V2
**What:** Use the `replica` block on `aws_dynamodb_table` (not the deprecated `aws_dynamodb_global_table` resource) for DynamoDB Global Tables V2 (2019.11.21).

```hcl
resource "aws_dynamodb_table" "budget" {
  name             = "km-budgets"
  billing_mode     = "PAY_PER_REQUEST"  # required for global tables
  hash_key         = "PK"
  range_key        = "SK"
  stream_enabled   = true               # required for global tables
  stream_view_type = "NEW_AND_OLD_IMAGES"

  attribute {
    name = "PK"
    type = "S"
  }
  attribute {
    name = "SK"
    type = "S"
  }

  replica {
    region_name = "eu-west-1"  # add replica blocks for each agent region
  }

  ttl {
    attribute_name = "expiresAt"
    enabled        = true
  }
}
```

### Anti-Patterns to Avoid
- **Attempting to intercept Bedrock via CONNECT passthrough**: The current OkConnect mode only tunnels TCP bytes — the proxy cannot read the TLS-encrypted response body. MITM mode (AlwaysMitm) is required.
- **Using `aws_dynamodb_global_table` Terraform resource**: This is V1 (2017.11.29). Use `aws_dynamodb_table` with `replica` blocks for V2.
- **Per-request DynamoDB reads for limit checks**: Always use the in-process 10s cache to check limits; only write to DynamoDB after confirmed spend.
- **Buffering streaming responses in memory indefinitely**: Cap at a reasonable size (e.g. 10MB) and fall back to best-effort parsing if exceeded.
- **Mutating the global compiledSchemaOnce for domain**: The schema `$id` is a URI used internally by the JSON Schema compiler for resource registration, not validated against profile content. The `apiVersion` field validation should only check the version suffix (e.g. `v1alpha1`), not the domain prefix.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| DynamoDB atomic increment | Custom read-modify-write with locking | `UpdateItem` with ADD expression | Race-free, single API call, built into DynamoDB |
| DynamoDB item marshaling | Manual `map[string]types.AttributeValue` everywhere | `attributevalue.MarshalMap` / `UnmarshalMap` | Handles type conversion, reduces boilerplate |
| SSE stream parsing | Custom byte scanner | Scan for `\n\n` delimiters, split on `data: ` prefix | SSE format is simple — but use `bufio.Scanner` not manual byte indexing |
| TLS certificate generation for MITM | Custom CA/cert generation | `goproxy.GoproxyCa` (built-in default CA) | goproxy has its own CA; use it or inject custom; never generate certs manually |
| AWS Pricing API response parsing | Custom response traversal | `json.Unmarshal` + typed structs for PriceList JSON | Price List API returns nested JSON — parse once at startup, cache in memory |
| Config wizard terminal UI | Custom readline/prompt | `bufio.Scanner` reading from `os.Stdin` | Matches `aws configure` simplicity; no extra dependency needed for a simple wizard |

**Key insight:** DynamoDB's ADD expression is purpose-built for atomic counters — it's the only correct way to do concurrent spend tracking without distributed locking.

---

## Common Pitfalls

### Pitfall 1: Bedrock MITM Requires Trusted CA in Sandbox
**What goes wrong:** The proxy generates a self-signed certificate for `bedrock-runtime.us-east-1.amazonaws.com`. The AWS SDK inside the sandbox rejects it with a TLS verification error.
**Why it happens:** MITM terminates TLS and re-presents a cert signed by the proxy's CA. That CA is not in the sandbox's system trust store.
**How to avoid:** Inject the proxy CA certificate into the sandbox at boot time (user-data script adds it to `/etc/ssl/certs/` and runs `update-ca-certificates`). Pass the CA cert to the proxy via env var or file mount.
**Warning signs:** Bedrock SDK calls fail with `x509: certificate signed by unknown authority` inside sandbox.

### Pitfall 2: SSE Parsing: message_delta vs message_start for Token Counts
**What goes wrong:** Reading `output_tokens` from `message_start` event (which contains `1` as a placeholder) instead of the final `message_delta` event (which contains the actual total).
**Why it happens:** Anthropic's streaming format emits `usage.output_tokens: 1` in `message_start` and updates it in `message_delta`. The `message_delta.usage.output_tokens` is the cumulative final count.
**How to avoid:** Only record token spend when `type == "message_delta"` is parsed from the SSE stream. Use `message_start.usage.input_tokens` for input tokens (available once, correct).
**Warning signs:** AI spend shows systematically as 1 output token per request.

### Pitfall 3: DynamoDB Global Table PAY_PER_REQUEST Requirement
**What goes wrong:** Provisioned throughput billing mode on global tables requires autoscaling configuration — otherwise table creation fails.
**Why it happens:** DynamoDB Global Tables V2 requires either PAY_PER_REQUEST or autoscaling-enabled provisioned throughput.
**How to avoid:** Use `billing_mode = "PAY_PER_REQUEST"` in the Terraform module. Budget tables will have low, bursty traffic patterns — on-demand billing is also cheaper.

### Pitfall 4: Pricing API Region Constraint
**What goes wrong:** `GetProducts` for `AmazonBedrock` service succeeds but returns empty results, or `GetProducts` for EC2 spot pricing fails with endpoint error.
**Why it happens:** The AWS Pricing API has a single regional endpoint: `pricing.us-east-1.amazonaws.com`. It is only available in `us-east-1` and `ap-south-1`. The pricing client must be configured with one of these regions regardless of the sandbox region.
**How to avoid:** Always create the pricing client with `config.WithRegion("us-east-1")` — do not inherit the sandbox region.
**Warning signs:** `pricing.GetProducts` returns `no such host` or `service endpoint not found`.

### Pitfall 5: Schema $id and apiVersion Validation Decoupling
**What goes wrong:** Changing the JSON Schema `$id` from `https://klankermaker.ai/schemas/...` to `https://{domain}/schemas/...` breaks profile validation because existing profiles have `apiVersion: klankermaker.ai/v1alpha1`.
**Why it happens:** The `$id` is used as the internal schema registration URI — it has no semantic relationship to the `apiVersion` field value in profiles. Validation of `apiVersion` is a separate concern.
**How to avoid:** Keep the `$id` as the internal schema registration key (or make it generic like `km/sandbox-profile/v1alpha1`). Relax `apiVersion` pattern validation to only check the version suffix (e.g. regex `.*\/v\d+alpha\d+`), ignoring the domain prefix.

### Pitfall 6: Existing go:embed compiledSchemaOnce Race with Dynamic Domain
**What goes wrong:** The schema `$id` is set at compile time via `const schemaID`. Making it dynamic (derived from config) breaks `compiledSchemaOnce` (the schema cannot change once compiled) and breaks the `go:embed` pattern.
**Why it happens:** `compiledSchemaOnce` compiles the schema once. The schema's `$id` is just an internal identifier — it does not need to match the domain in use.
**How to avoid:** Keep `schemaID` as a fixed internal constant (not user-configurable). The schema validator does not need to know the operator's domain.

### Pitfall 7: goproxy OnResponse Does Not Fire for Bypassed CONNECT Hosts
**What goes wrong:** `proxy.OnResponse(matcher).DoFunc(...)` is registered but never fires for Bedrock hosts.
**Why it happens:** `OnResponse` only fires for requests that went through MITM (AlwaysMitm). If the CONNECT handler returns OkConnect for a host, that host's traffic is a blind tunnel and OnResponse never fires.
**How to avoid:** Ensure `OnRequest(bedrockMatcher).HandleConnect(goproxy.AlwaysMitm)` is registered BEFORE `OnRequest().HandleConnect(...)` for all other hosts. goproxy uses first-match semantics for CONNECT handlers.

---

## Code Examples

### DynamoDB UpdateItem Atomic Spend Increment
```go
// Source: AWS SDK Go v2 DynamoDB docs + pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/dynamodb
func IncrementAISpend(ctx context.Context, client BudgetAPI, tableName, sandboxID, modelID string, inputTokens, outputTokens int, costUSD float64) error {
    _, err := client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
        TableName: aws.String(tableName),
        Key: map[string]types.AttributeValue{
            "PK": &types.AttributeValueMemberS{Value: "SANDBOX#" + sandboxID},
            "SK": &types.AttributeValueMemberS{Value: "BUDGET#ai#" + modelID},
        },
        UpdateExpression: aws.String("ADD spentUSD :cost, inputTokens :in, outputTokens :out SET lastUpdated = :ts"),
        ExpressionAttributeValues: map[string]types.AttributeValue{
            ":cost": &types.AttributeValueMemberN{Value: fmt.Sprintf("%.6f", costUSD)},
            ":in":   &types.AttributeValueMemberN{Value: strconv.Itoa(inputTokens)},
            ":out":  &types.AttributeValueMemberN{Value: strconv.Itoa(outputTokens)},
            ":ts":   &types.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
        },
        ReturnValues: types.ReturnValueUpdatedNew,
    })
    return err
}
```

### SSE Token Extraction from Bedrock Response Body
```go
// Source: Anthropic streaming docs (platform.claude.com/docs/en/api/messages-streaming)
// Parses SSE stream from Bedrock InvokeModelWithResponseStream response body.
// Returns input tokens (from message_start) and output tokens (from message_delta).
func extractBedrockTokens(body io.Reader) (inputTokens, outputTokens int, err error) {
    scanner := bufio.NewScanner(body)
    var dataLine string
    for scanner.Scan() {
        line := scanner.Text()
        if strings.HasPrefix(line, "data: ") {
            dataLine = strings.TrimPrefix(line, "data: ")
        } else if line == "" && dataLine != "" {
            // End of SSE event — parse data
            var event struct {
                Type    string `json:"type"`
                Message struct {
                    Usage struct {
                        InputTokens int `json:"input_tokens"`
                    } `json:"usage"`
                } `json:"message"`
                Usage struct {
                    OutputTokens int `json:"output_tokens"`
                } `json:"usage"`
            }
            if jsonErr := json.Unmarshal([]byte(dataLine), &event); jsonErr == nil {
                switch event.Type {
                case "message_start":
                    inputTokens = event.Message.Usage.InputTokens
                case "message_delta":
                    outputTokens = event.Usage.OutputTokens // cumulative final count
                }
            }
            dataLine = ""
        }
    }
    return inputTokens, outputTokens, scanner.Err()
}
```

### km-config.yaml Loading Extension
```go
// Source: existing internal/app/config/config.go + viper pattern
// Add to Config struct and Load() function:
cfg := &Config{
    // ... existing fields ...
    Domain:               v.GetString("domain"),
    ManagementAccountID:  v.GetString("accounts.management"),
    TerraformAccountID:   v.GetString("accounts.terraform"),
    ApplicationAccountID: v.GetString("accounts.application"),
    SSOStartURL:          v.GetString("sso.start_url"),
    PrimaryRegion:        v.GetString("region"),
    BudgetTableName:      v.GetString("budget_table_name"),
}
// Add config paths for km-config.yaml in repo root
v.SetConfigName("km-config")  // looks for km-config.yaml
v.AddConfigPath(".")          // repo root when running from repo
v.AddConfigPath(repoRoot)     // repo root when running from subdirectory
```

### goproxy AlwaysMitm for Bedrock Hosts
```go
// Source: github.com/elazarl/goproxy README
var bedrockHostRegex = regexp.MustCompile(`^bedrock-runtime\..+\.amazonaws\.com`)

// In NewProxy(), register Bedrock MITM BEFORE the general AllowConnect handler:
proxy.OnRequest(goproxy.ReqHostMatches(bedrockHostRegex)).
    HandleConnect(goproxy.AlwaysMitm)

// Intercept the actual HTTP response after MITM:
proxy.OnResponse(goproxy.ReqHostMatches(bedrockHostRegex)).
    DoFunc(func(resp *http.Response, ctx *goproxy.ProxyCtx) *http.Response {
        // Read body, parse tokens, increment DynamoDB, return new body reader
        return interceptBedrockResponse(resp, ctx, budgetClient, sandboxID)
    })
```

### 403 Response Body for Agent Parsing
```go
// Source: CONTEXT.md spec
type AIBudgetExhaustedError struct {
    Error  string  `json:"error"`   // "ai_budget_exhausted"
    Spent  float64 `json:"spent"`
    Limit  float64 `json:"limit"`
    Model  string  `json:"model"`
    TopUp  string  `json:"topUp"`   // "km budget add sb-xxx --ai 5"
}

func bedrockBlockedResponse(req *http.Request, sandboxID, modelID string, spent, limit float64) *http.Response {
    body := AIBudgetExhaustedError{
        Error: "ai_budget_exhausted",
        Spent: spent,
        Limit: limit,
        Model: modelID,
        TopUp: fmt.Sprintf("km budget add %s --ai 5", sandboxID),
    }
    // return goproxy.NewResponse(req, "application/json", 403, jsonBody)
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| `aws_dynamodb_global_table` (V1) | `aws_dynamodb_table` with `replica` blocks (V2) | 2019.11.21 API version | Simpler HCL, better conflict resolution, no stream_arn outputs needed |
| Hardcoded config constants in Go | viper-loaded YAML config | Already established in config.go | Platform is now forkable |
| CONNECT-only proxy (no body inspection) | MITM proxy for selected hosts | Phase 6 new capability | Enables Bedrock metering |
| Per-request DynamoDB reads | In-process 10s cache | Phase 6 design decision | Eliminates DynamoDB latency on Bedrock fast path |

**Deprecated/outdated:**
- `aws_dynamodb_global_table` (V1 resource): deprecated in favor of `replica` blocks on `aws_dynamodb_table`. Using V1 resource causes Terraform drift in newer provider versions.
- goproxy `CONNECT` handler returning `OkConnect` for Bedrock: must be replaced with `AlwaysMitm` for body interception.

---

## Open Questions

1. **KM_BUDGET_REMAINING update mechanism**
   - What we know: The context says agents can read this env var to gracefully wind down at 80%
   - What's unclear: Env vars cannot be updated in a running process's parent. Options: (a) write to a well-known file (`/run/km/budget_remaining`), (b) proxy serves a sidecar HTTP API on localhost that agent polls, (c) SSM Parameter Store write (requires SSM access from Lambda/proxy)
   - Recommendation: Write to a file at `/run/km/budget_remaining` (simplest, no extra IAM). Document it. Proxy writes after each DynamoDB cache refresh; agent reads via `cat /run/km/budget_remaining`. This is within Claude's Discretion.

2. **Pricing API for Bedrock model rates**
   - What we know: `service/pricing` SDK exists; must use `us-east-1` endpoint; `AmazonBedrock` is the service name
   - What's unclear: Whether the AWS Price List API actually returns per-token Bedrock pricing in machine-parseable form (it may return prose descriptions, not structured per-token prices)
   - Recommendation: Implement a static fallback table of known Anthropic model prices on Bedrock (as of 2026), refreshed by the pricing API if available. This ensures the proxy works even if the Pricing API doesn't return structured Bedrock rates. Flag as LOW confidence — verify at implementation time.

3. **Budget enforcer Lambda trigger frequency**
   - What we know: Context says "runs every minute" for compute spend
   - What's unclear: EventBridge Scheduler minimum rate is 1 minute — this is fine. But 1-minute granularity means up to $0.16 overspend at $10/hour instance cost before enforcement kicks in.
   - Recommendation: Accept this as the stated design. Document the maximum overspend = spotRate × 1/60 hours.

4. **CA certificate trust injection for MITM**
   - What we know: The proxy must inject its CA cert into the sandbox trust store
   - What's unclear: The exact mechanism — userdata script, cloud-init, or a proxy startup script that installs the cert and restarts the sandbox process
   - Recommendation: Proxy startup script writes CA cert to `/usr/local/share/ca-certificates/km-proxy-ca.crt` and calls `update-ca-certificates`. The proxy CA cert is passed via `KM_PROXY_CA_CERT` env var (base64-encoded PEM). This happens before the sandbox workload starts. This is within Claude's Discretion.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (`go test ./...`) |
| Config file | none — `go test` standard |
| Quick run command | `go test ./pkg/aws/... ./sidecars/http-proxy/... ./internal/app/...` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| CONF-01 | km-config.yaml loaded by config.go | unit | `go test ./internal/app/config/...` | ❌ Wave 0 |
| CONF-02 | Domain propagates through SES/schema/compiler | unit | `go test ./pkg/aws/... ./pkg/profile/... ./pkg/compiler/...` | existing tests |
| CONF-03 | Account IDs in config.go | unit | `go test ./internal/app/config/...` | ❌ Wave 0 |
| CONF-04 | km configure writes km-config.yaml | unit | `go test ./internal/app/cmd/ -run TestConfigure` | ❌ Wave 0 |
| BUDG-01 | BudgetSpec parses from profile YAML | unit | `go test ./pkg/profile/ -run TestBudget` | ❌ Wave 0 |
| BUDG-02 | Budget DynamoDB table Terraform module | smoke | manual terraform plan | ❌ Wave 0 |
| BUDG-03 | Compute spend calculation (spotRate × elapsed) | unit | `go test ./pkg/aws/ -run TestComputeSpend` | ❌ Wave 0 |
| BUDG-04 | Bedrock SSE token extraction | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestBedrockTokenExtract` | ❌ Wave 0 |
| BUDG-05 | Pricing cache returns model rate | unit | `go test ./pkg/aws/ -run TestPricing` | ❌ Wave 0 |
| BUDG-06 | 80% threshold triggers SES notification | unit | `go test ./pkg/aws/ -run TestBudgetWarning` | ❌ Wave 0 |
| BUDG-07 | Proxy returns 403 at 100% AI budget | unit | `go test ./sidecars/http-proxy/httpproxy/ -run TestBudgetEnforce` | ❌ Wave 0 |
| BUDG-08 | km budget add updates DynamoDB and resumes | unit | `go test ./internal/app/cmd/ -run TestBudgetAdd` | ❌ Wave 0 |
| BUDG-09 | km status shows budget breakdown | unit | `go test ./internal/app/cmd/ -run TestStatus.*Budget` | existing test, extend |

### Sampling Rate
- **Per task commit:** `go test ./pkg/aws/... ./pkg/profile/... ./sidecars/http-proxy/...`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/aws/budget_test.go` — covers BUDG-02 DynamoDB interface, BUDG-03 compute spend, BUDG-06 warning threshold
- [ ] `pkg/aws/pricing_test.go` — covers BUDG-05 pricing cache with mock
- [ ] `sidecars/http-proxy/httpproxy/bedrock_test.go` — covers BUDG-04 SSE parsing, BUDG-07 403 enforcement
- [ ] `internal/app/config/config_test.go` — covers CONF-01, CONF-03 config loading
- [ ] `internal/app/cmd/configure_test.go` — covers CONF-04 wizard
- [ ] `internal/app/cmd/budget_test.go` — covers BUDG-08 top-up command
- [ ] `pkg/profile/budget_types_test.go` — covers BUDG-01 BudgetSpec

---

## Hardcoded Domain Reference Catalogue

All locations requiring replacement with config-derived domain (CONF-02):

| File | Line | Current Value | Replacement |
|------|------|---------------|-------------|
| `internal/app/cmd/create.go` | 267 | `const emailDomain = "sandboxes.klankermaker.ai"` | `cfg.Domain` prefixed with `"sandboxes."` |
| `internal/app/cmd/destroy.go` | 192 | `const emailDomain = "sandboxes.klankermaker.ai"` | same |
| `pkg/profile/schema.go` | 15 | `const schemaID = "https://klankermaker.ai/schemas/..."` | Keep as internal const OR make generic (see Pitfall 5) |
| `pkg/compiler/service_hcl.go` | 291, 406 | `"sandboxes.klankermaker.ai"` | compiler receives domain from Config |
| `pkg/compiler/userdata.go` | (grep confirms) | same pattern | same |
| `cmd/ttl-handler/main.go` | 153 | `domain = "sandboxes.klankermaker.ai"` | `KM_EMAIL_DOMAIN` env var (already parameterized here — just set it from config) |
| `infra/live/site.hcl` | 5 | `domain = "klankermaker.ai"` | read from `km-config.yaml` via `get_env("KM_DOMAIN", "klankermaker.ai")` |
| `pkg/profile/validate.go` | apiVersion regex | Check version suffix only | Relax to `.*\/v\d+alpha\d+` pattern |

---

## Sources

### Primary (HIGH confidence)
- `pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/dynamodb` — DynamoDB v2 API, UpdateItem ADD expression
- `pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/bedrockruntime` — Bedrock runtime types for streaming
- `platform.claude.com/docs/en/api/messages-streaming` — Anthropic SSE streaming event format, confirmed `message_start.usage.input_tokens` and `message_delta.usage.output_tokens` field locations
- `github.com/elazarl/goproxy` README and `https.go` — MITM mode via `AlwaysMitm`, `OnResponse` firing after MITM
- `registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/dynamodb_table` — DynamoDB Global Tables V2 via `replica` blocks, `PAY_PER_REQUEST` requirement
- Existing codebase — `internal/app/config/config.go`, `pkg/aws/ses.go`, `sidecars/http-proxy/httpproxy/proxy.go`, `cmd/ttl-handler/main.go`

### Secondary (MEDIUM confidence)
- `docs.aws.amazon.com/amazondynamodb/latest/developerguide/example_dynamodb_Scenario_AtomicCounterOperations_section.html` — atomic counter UpdateItem pattern
- `docs.aws.amazon.com/sdk-for-go/api/service/pricing/` — Pricing API `GetProducts` for `AmazonBedrock`
- `pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/pricing` — Go v2 pricing SDK existence confirmed

### Tertiary (LOW confidence — verify at implementation)
- AWS Pricing API Bedrock structured rates: Community reports (repost.aws) suggest Bedrock pricing may not be in machine-parseable GetProducts format. Recommend static fallback table.
- `KM_BUDGET_REMAINING` file-write mechanism: design recommendation, not verified against specific constraints.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all packages verified in pkg.go.dev; aws-sdk-go-v2 already in go.mod
- Architecture: HIGH — DynamoDB patterns verified; goproxy MITM verified; SSE event format verified from official Anthropic docs
- Pitfalls: HIGH — schema $id pitfall verified by reading schema.go; MITM CA pitfall is a well-known Go TLS issue; DynamoDB V1/V2 distinction verified from Terraform docs
- Pricing API for Bedrock: LOW — may require static fallback table

**Research date:** 2026-03-22
**Valid until:** 2026-04-22 (stable AWS SDK and Terraform provider; Anthropic SSE format is stable)
