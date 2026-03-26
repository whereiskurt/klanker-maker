# Phase 22: Remote Sandbox Creation — `km create --remote` via Lambda + Email-to-Create — Research

**Researched:** 2026-03-26
**Domain:** AWS Lambda container images, EventBridge rules, SES receipt rule pipeline, SSM safe-phrase auth, Go Lambda runtime, `km create` dispatch flow
**Confidence:** HIGH

---

<phase_requirements>
## Phase Requirements

Phase 22 requirements (REMOTE-01 through REMOTE-06) are not yet in REQUIREMENTS.md at research time — they will be added in the plan. Based on the phase description, the requirements are:

| ID | Description | Research Support |
|----|-------------|-----------------|
| REMOTE-01 | Create a `km-create-handler` Lambda (container image) bundling `km` binary + terragrunt + terraform + Terraform modules | Container image Lambda pattern; existing `km-ttl-handler` zip-based Lambda is the template for IAM/VPC shape; size of terraform+terragrunt requires container image |
| REMOTE-02 | `km create --remote` compiles profile locally, uploads artifacts to S3, publishes `SandboxCreate` EventBridge event, Lambda runs `terragrunt apply` | `km create` local flow (Steps 1–9) already exists; remote flag splits compile (local) from apply (Lambda); EventBridge rule + S3 handoff are the integration points |
| REMOTE-03 | Email-to-create: SES receipt rule for `create@sandboxes.{domain}` parses YAML attachment, verifies safe phrase, triggers create Lambda | SES S3 receipt + S3 notification or SNS rule already established; `kmAuthPattern` already in `pkg/aws/mailbox.go`; Lambda reads email from S3 `mail/` prefix |
| REMOTE-04 | Safe-phrase auth: `KM-AUTH: <phrase>` in email body must match SSM parameter `/sandbox/{id}/safe-phrase` for email-triggered creation | `kmAuthPattern` and SSM safe-phrase already wired in `create.go` Step 12d; email handler reads SSM, validates, proceeds or rejects |
| REMOTE-05 | EventBridge integration: new `km-sandbox-create` rule routes `SandboxCreate` events (`source = ["km.sandbox"]`, `detail-type = ["SandboxCreate"]`) to the create Lambda | EventBridge rule pattern mirrors existing `km-sandbox-idle` rule in `ttl-handler` module |
| REMOTE-06 | Status notification: Lambda emails operator with sandbox ID and connection details on success, error details on failure | `SendLifecycleNotification` pattern already in `pkg/aws/ses.go`; Lambda can send "created" or "create-failed" event |
</phase_requirements>

---

## Summary

Phase 22 adds two related paths for triggering sandbox creation without requiring the operator to have terraform/terragrunt installed locally:

1. **`km create --remote`** — the local `km` binary compiles the profile and uploads artifacts to S3, then publishes a `SandboxCreate` EventBridge event. A new `km-create-handler` Lambda picks up the event, downloads the compiled Terragrunt artifacts from S3, and runs `terragrunt apply` inside the Lambda container.

2. **Email-to-create** — the operator emails a profile YAML to `create@sandboxes.{domain}`. A SES receipt rule delivers the email to S3. An S3 event notification (or a second SES rule action) triggers the `km-create-handler` Lambda, which parses the YAML attachment, validates the `KM-AUTH: <phrase>` body token against the operator's SSM safe phrase, then runs the full `km create` flow.

The create Lambda **must be a container image** (not a zip file): terraform alone is ~70MB binary, terragrunt is ~40MB, and bundling the `km` binary with Terraform modules pushes the payload above the 250MB zip limit. Container images support up to 10GB. The existing `km-ttl-handler` Lambda (zip-based, 1536MB RAM, 900s timeout) is the IAM/runtime template for the new Lambda.

Key design principle: the Lambda runs `terragrunt apply` via subprocess (same as the `terraformDestroy` function in `cmd/ttl-handler/main.go`). This is already proven. The create Lambda needs more IAM permissions (create, not just destroy) and must have the full `infra/modules/` tree bundled in the container image.

**Primary recommendation:** Use a container image Lambda (ECR, `provided.al2023`, arm64) that bundles the `km` binary + terraform + terragrunt + `infra/modules/`. For the `--remote` flag: compile locally, upload `service.hcl` + `user-data.sh` artifacts to S3 under `remote-create/{sandbox-id}/`, publish `SandboxCreate` to EventBridge. For email-to-create: use a second lightweight Lambda (or extend the create handler) to parse SES mail, validate safe phrase, then re-invoke the main create Lambda via `lambda:InvokeFunction` or publish a `SandboxCreate` event.

---

## Standard Stack

### Core (zero new external dependencies beyond what is already in go.mod)

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-lambda-go` | v1.47.0 (already in go.mod) | Lambda handler registration (`lambda.Start`) | Already used by all existing Lambdas |
| `github.com/aws/aws-sdk-go-v2/service/eventbridge` | v1.x (add to go.mod) | Publish `SandboxCreate` events from `km create --remote` | EventBridge `PutEvents` API; existing code uses `scheduler`, not `events`; new dependency needed |
| `github.com/aws/aws-sdk-go-v2/service/s3` | v1.97.1 (in go.mod) | Upload/download compiled artifacts, read email from S3 | Already used throughout |
| `github.com/aws/aws-sdk-go-v2/service/sesv2` | v1.x (in go.mod) | Send creation success/failure notifications | Already used in `ses.go` |
| `github.com/aws/aws-sdk-go-v2/service/ssm` | v1.x (in go.mod) | Read safe phrase for email auth, write new safe phrase | Already used in `create.go` |
| `os/exec` | stdlib | Run `terragrunt apply` subprocess inside the container | Same pattern as `terraformDestroy` in `cmd/ttl-handler/main.go` |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `mime/multipart` + `net/mail` | stdlib | Parse email MIME body + attachment (YAML profile) | Email-to-create path; same as `mailbox.go` pattern |
| `encoding/base64` | stdlib | Decode SES S3-stored email object (SES stores raw MIME) | SES delivers raw RFC 5322 messages to S3 |
| `github.com/aws/aws-sdk-go-v2/service/lambda` | v1.x (in go.mod) | Email handler Lambda invokes create Lambda directly | Alternative to EventBridge re-publish in email path |

### Container Image Build Stack

| Tool | Version | Purpose | Source |
|------|---------|---------|--------|
| Docker (buildx) | current | Build ECR container image | Already used for sidecar images in Makefile |
| terraform | ~1.9.x (latest stable) | Run `terraform init/apply` inside Lambda | Download in Dockerfile from releases.hashicorp.com |
| terragrunt | ~0.67.x (latest stable) | Orchestrate Terraform modules inside Lambda | Download in Dockerfile from releases.gruntwork.io |
| `provided.al2023` base image | current | AWS Lambda custom runtime base | `public.ecr.aws/lambda/provided:al2023-arm64` |

**Confidence (container image sizes):** MEDIUM — verified terraform is ~70MB amd64 and ~65MB arm64; terragrunt is ~30–40MB arm64; km binary is ~20MB; infra/modules/ is small (HCL, not binaries). Total container image well under 500MB, comfortably under the 10GB limit.

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Container image Lambda | Zip Lambda with terraform layer | Lambda layers also have 250MB limit per layer (50MB per layer deployed); stacking terraform + terragrunt hits the limit; container image is the correct approach for large binaries |
| EventBridge `PutEvents` from `km create --remote` | SQS FIFO queue | EventBridge is already used for idle/TTL events; consistent with existing event bus; SQS adds another service with no benefit here |
| Second email-parsing Lambda | Extend create Lambda to handle both event types | Separate concerns: email parsing is lightweight (go+mime), create is heavyweight (terraform); two Lambdas is cleaner for memory sizing and IAM scope |

**Installation:**
```bash
# One new direct dependency — EventBridge SDK client
go get github.com/aws/aws-sdk-go-v2/service/eventbridge
```

---

## Architecture Patterns

### Recommended Project Structure

```
cmd/
├── km/                        # existing CLI
├── ttl-handler/               # existing Lambda
├── budget-enforcer/           # existing Lambda
├── github-token-refresher/    # existing Lambda
├── create-handler/            # NEW — main Lambda handler (runs terragrunt apply)
│   └── main.go
└── email-create-handler/      # NEW — lightweight email parser Lambda
    └── main.go

infra/modules/
├── ttl-handler/v1.0.0/        # existing
├── create-handler/            # NEW
│   └── v1.0.0/
│       ├── main.tf            # IAM role, ECR-based Lambda, EventBridge rule
│       ├── variables.tf
│       └── outputs.tf

infra/live/use1/
├── ses/                       # existing — update receipt rule to add create@ recipient
├── create-handler/            # NEW
│   └── terragrunt.hcl

pkg/aws/
├── ses.go                     # existing — no change needed
├── mailbox.go                 # existing — ParseSignedMessage, kmAuthPattern already here
└── eventbridge.go             # NEW — PutSandboxCreateEvent, narrow EventBridgeAPI interface

internal/app/cmd/
├── create.go                  # extend: add --remote flag and runCreateRemote() function
└── create_remote_test.go      # NEW — unit tests for remote dispatch flow

Makefile
├── build-lambdas target       # extend: add create-handler and email-create-handler
└── add container build targets for ECR
```

### Pattern 1: `km create --remote` Dispatch Flow

**What:** Local `km` compiles profile (Steps 1–9 of `runCreate`), uploads Terragrunt artifacts to S3 under `remote-create/{sandbox-id}/`, then calls `PutSandboxCreateEvent`. Returns immediately after event publish — does not wait for Lambda completion.

**When to use:** Operator does not have terraform/terragrunt installed. Useful for CI/CD workflows.

```go
// Source: mirrors runCreate Step 1-9 pattern in internal/app/cmd/create.go
func runCreateRemote(cfg *config.Config, profilePath string, onDemand bool, awsProfile string) error {
    ctx := context.Background()

    // Steps 1-7: identical to local create (parse, validate, generate ID, compile)
    // ...

    // Upload artifacts to S3: service.hcl, user-data.sh, .km-profile.yaml
    artifactPrefix := "remote-create/" + sandboxID + "/"
    s3Client.PutObject(ctx, &s3.PutObjectInput{
        Bucket: aws.String(cfg.ArtifactsBucket),
        Key:    aws.String(artifactPrefix + "service.hcl"),
        Body:   strings.NewReader(artifacts.ServiceHCL),
    })
    // ... user-data.sh, budget-enforcer.hcl, github-token.hcl

    // Publish SandboxCreate event — Lambda picks up from here
    return awspkg.PutSandboxCreateEvent(ctx, ebClient, sandboxID, cfg.ArtifactsBucket, artifactPrefix, operatorEmail)
}
```

**S3 artifact layout for remote create:**
```
s3://{artifacts-bucket}/
  remote-create/{sandbox-id}/
    service.hcl
    user-data.sh               (empty string for ECS)
    budget-enforcer.hcl        (optional)
    github-token.hcl           (optional)
    .km-profile.yaml
```

### Pattern 2: Create Handler Lambda — Terragrunt Apply via Subprocess

**What:** Lambda receives `SandboxCreate` EventBridge event, downloads compiled artifacts from S3, reconstructs the sandbox directory in `/tmp`, runs `terragrunt apply`, then runs post-create steps (TTL schedule, budget init, email identity, notification).

**When to use:** All remote create invocations, both `--remote` flag and email-to-create.

```go
// Source: mirrors terraformDestroy() in cmd/ttl-handler/main.go
// Event payload shape
type CreateEvent struct {
    SandboxID      string `json:"sandbox_id"`
    ArtifactBucket string `json:"artifact_bucket"`
    ArtifactPrefix string `json:"artifact_prefix"` // remote-create/{sandbox-id}/
    OperatorEmail  string `json:"operator_email"`
    OnDemand       bool   `json:"on_demand"`
}

func (h *CreateHandler) HandleCreateEvent(ctx context.Context, event CreateEvent) error {
    // 1. Download service.hcl, user-data.sh from S3
    // 2. Reconstruct sandbox dir in /tmp (same layout as infra/live/{region}/sandboxes/{id}/)
    // 3. Run terragrunt apply via exec.CommandContext("/var/task/terragrunt", "apply", "-auto-approve", ...)
    // 4. Post-create steps: TTL schedule, budget init, SES email, identity, safe phrase
    // 5. Send "created" or "create-failed" notification via SES
}
```

**Critical: Terragrunt needs repo root context.** The local create path uses `findRepoRoot()` which walks up from the binary to find `CLAUDE.md`. Inside Lambda, `/var/task/` IS the root. The container must have `infra/live/`, `infra/modules/`, `site.hcl`, `region.hcl` bundled under `/var/task/`. Terragrunt reads `find_in_parent_folders("site.hcl")` from the sandbox directory — the full Terragrunt hierarchy must be present.

**Alternative simpler approach:** Instead of running `terragrunt apply`, the Lambda calls `km create` itself (bundled `km` binary inside the container). The Lambda does `exec.Command("/var/task/km", "create", "/tmp/{id}/.km-profile.yaml", "--aws-profile", "")`. This reuses all the existing `runCreate` logic without rewriting the post-create steps. The `km` binary runs inside the container with the Lambda execution role as credentials (no `--aws-profile` needed).

**Recommendation:** Use `km create` subprocess inside the container. This avoids duplicating the full create flow in the Lambda and means all future changes to `create.go` automatically apply to remote create as well. The profile YAML is downloaded from S3 first, then `km create` runs against it. AWS credentials come from the Lambda execution role (no `--aws-profile`).

### Pattern 3: Email-to-Create Lambda

**What:** Lightweight Lambda receives S3 event notification (SES stores email at `mail/` prefix), reads raw MIME from S3, parses headers + YAML attachment, validates `KM-AUTH:` phrase against SSM, then invokes the create Lambda or publishes `SandboxCreate` event.

**When to use:** Operator emails `create@sandboxes.{domain}` with profile YAML attached.

```go
// Source: pkg/aws/mailbox.go kmAuthPattern + net/mail parsing
type EmailCreateEvent struct {
    Records []struct {
        S3 struct {
            Bucket struct{ Name string }
            Object struct{ Key string }
        }
    }
}

func HandleEmailCreate(ctx context.Context, event EmailCreateEvent) error {
    // 1. GetObject S3 key from event (SES delivered to mail/{message-id})
    // 2. net/mail.ReadMessage() — parse MIME headers
    // 3. Extract YAML attachment from multipart body (or entire body if text/yaml)
    // 4. Extract KM-AUTH: phrase from body using kmAuthPattern (already in mailbox.go)
    // 5. Read expected phrase from SSM /km/config/remote-create/safe-phrase
    //    (a GLOBAL safe phrase, not per-sandbox — this is for create authorization)
    // 6. Compare phrase — reject with notification email if mismatch
    // 7. Parse YAML profile bytes via profile.Parse()
    // 8. Write profile to /tmp/{uuid}.yaml
    // 9. Invoke create Lambda (or publish SandboxCreate event with profile bytes encoded)
    // 10. Send acknowledgment email to sender
}
```

**Safe phrase design:** There are two safe phrases in the system:
- **Per-sandbox safe phrase** (existing, `/sandbox/{sandbox-id}/safe-phrase`): already implemented in Phase 21/create.go Step 12d — used to authorize actions on an existing sandbox via email (e.g., extend TTL, stop).
- **Global create safe phrase** (new, `/km/config/remote-create/safe-phrase`): used to authorize new sandbox creation via email. The operator sets this once during `km configure` or `km init`.

The phase description says "operator-configured safe phrase in SSM" without specifying a single per-request path. A global create key is the correct model: the operator configures one phrase that authorizes all email-triggered creates.

### Pattern 4: SES Receipt Rule Extension for `create@` Address

**What:** Add a second recipient to the existing SES receipt rule set to route emails addressed to `create@sandboxes.{domain}` to a different S3 prefix (or same `mail/` prefix) and trigger the email-create Lambda.

**Current state:** The existing receipt rule (`infra/modules/ses/v1.0.0/main.tf`) uses `recipients = [var.domain]` which matches ALL addresses at the domain. Adding a specific `create@` rule requires adding a new receipt rule with higher priority (lower position number = higher priority in SES).

```terraform
# New receipt rule — position 1, evaluated before the catch-all at position 2
resource "aws_ses_receipt_rule" "create_inbound" {
  name          = "create-inbound"
  rule_set_name = aws_ses_receipt_rule_set.km_sandbox.rule_set_name
  recipients    = ["create@${var.domain}"]
  enabled       = true
  scan_enabled  = false

  s3_action {
    bucket_name       = var.artifact_bucket_name
    object_key_prefix = "mail/create/"   # separate prefix for create requests
    position          = 1
  }

  lambda_action {
    function_arn    = var.email_create_handler_arn
    invocation_type = "Event"            # async — don't block SES delivery
    position        = 2
  }

  depends_on = [aws_ses_active_receipt_rule_set.km_sandbox]
}
```

**SES Lambda action constraint:** The Lambda must grant `ses.amazonaws.com` permission to invoke it (`aws_lambda_permission`). The Lambda action can be `Event` (async) or `RequestResponse` (sync, 30s max). Use `Event` to avoid blocking SES.

**Alternative:** S3 action only + S3 event notification triggers Lambda. Simpler — no Lambda action in receipt rule. The S3 bucket already has a policy; add an S3 event notification resource in the ses module.

**Recommendation:** S3 action only (already proven pattern) + S3 event notification on `mail/create/` prefix to trigger the email-create Lambda. Avoids adding Lambda action to receipt rule.

### Pattern 5: IAM for Create Handler Lambda

The create Lambda needs significantly broader IAM permissions than the ttl-handler because it runs `km create` which provisions all sandbox resources. Key permissions needed:

```
EC2: RunInstances, DescribeInstances, CreateSecurityGroup, AuthorizeSecurityGroupEgress, ...
IAM: CreateRole, PutRolePolicy, CreateInstanceProfile, AddRoleToInstanceProfile, PassRole, ...
ECS: CreateCluster, RegisterTaskDefinition, CreateService, ...
S3: GetObject, PutObject, ListBucket (state + artifacts)
DynamoDB: GetItem, PutItem (state lock + budget)
EventBridge Scheduler: CreateSchedule (TTL schedule)
SSM: GetParameter, PutParameter (safe phrase, GitHub token)
SES: SendEmail (notifications)
Lambda: CreateFunction, DeleteFunction (budget-enforcer per-sandbox Lambda)
KMS: GenerateDataKey, Decrypt (SOPS, SSM SecureString)
SecretsManager: (if needed)
```

This is a broad permission set. Scope to `km:*` resources where possible using tag conditions (`aws:RequestedRegion` + `km:managed = true` conditions).

**Critical IAM risk:** The create Lambda role must be able to `iam:PassRole` to the sandbox EC2 instance profile role. PassRole must be scoped to `arn:aws:iam::*:role/km-ec2spot-*` to avoid privilege escalation. The SCP from Phase 10 allows trusted roles to pass roles — the create Lambda role must be in the SCP carve-out list.

### Pattern 6: EventBridge Event Shape

```json
{
  "source": ["km.sandbox"],
  "detail-type": ["SandboxCreate"],
  "detail": {
    "sandbox_id": "sb-a1b2c3d4",
    "artifact_bucket": "km-artifacts-xxx",
    "artifact_prefix": "remote-create/sb-a1b2c3d4/",
    "operator_email": "operator@example.com",
    "on_demand": false
  }
}
```

EventBridge `PutEvents` in `pkg/aws/eventbridge.go`:
```go
// EventBridgeAPI is the narrow interface for publishing events.
type EventBridgeAPI interface {
    PutEvents(ctx context.Context, input *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error)
}

func PutSandboxCreateEvent(ctx context.Context, client EventBridgeAPI, sandboxID, bucket, prefix, operatorEmail string, onDemand bool) error {
    detail, _ := json.Marshal(map[string]any{
        "sandbox_id":      sandboxID,
        "artifact_bucket": bucket,
        "artifact_prefix": prefix,
        "operator_email":  operatorEmail,
        "on_demand":       onDemand,
    })
    _, err := client.PutEvents(ctx, &eventbridge.PutEventsInput{
        Entries: []eventbridgetypes.PutEventsRequestEntry{{
            Source:       aws.String("km.sandbox"),
            DetailType:   aws.String("SandboxCreate"),
            Detail:       aws.String(string(detail)),
        }},
    })
    return err
}
```

### Anti-Patterns to Avoid

- **Don't use a zip Lambda for the create handler** — terraform + terragrunt + km binary exceeds the 250MB unzipped zip limit. Always use a container image.
- **Don't run `terraform apply` directly in the Lambda** — use `km create` subprocess instead, so all post-create steps (budget, TTL, identity) are included. Direct terraform apply skips Steps 11–15.
- **Don't use synchronous Lambda action in SES receipt rule** — SES has a 30s timeout for `RequestResponse`; terraform apply takes 2–5 minutes. Always use `invocation_type = "Event"` (async).
- **Don't share the create safe phrase with per-sandbox safe phrases** — they serve different authorization purposes. Create phrase = "who can trigger creates"; per-sandbox phrase = "who can control this sandbox".
- **Don't put the IAM carve-out for the create Lambda in the wrong SCP statement** — the create Lambda role must be in `trusted_arns_iam` (the IAM escalation carve-out), not `trusted_arns_instance`. See Phase 10 SCP pattern.
- **Don't write Terragrunt artifacts to `/tmp` without isolation** — use `/tmp/{sandbox-id}/` as the working directory, same as `terraformDestroy` uses `/tmp/tf-{sandbox-id}`.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| MIME email parsing for YAML attachment | Custom byte scanner | `net/mail.ReadMessage()` + `mime/multipart` | RFC 5322 edge cases, folded headers; same pattern as `mailbox.go` |
| Safe phrase extraction from email body | Custom regex | `kmAuthPattern` in `pkg/aws/mailbox.go` (already implemented) | `regexp.MustCompile("(?m)KM-AUTH:\\s*(\\S+)")` is already there |
| EventBridge event publishing | Direct HTTP to event bus | `eventbridge.PutEvents` SDK call | SDK handles retries, signatures, endpoint resolution |
| Lambda container image from scratch | Handwritten Dockerfile | Use `public.ecr.aws/lambda/provided:al2023-arm64` as base | AWS provides the correct Lambda bootstrap environment for `provided.al2023` |
| Terraform state lock management | Custom locking | Existing DynamoDB lock table pattern from `terraformDestroy` | Lock table is already provisioned; same `-lock=false` caution applies for Lambda context |

**Key insight:** The entire `runCreate` logic already exists in `internal/app/cmd/create.go`. The create Lambda does not need to re-implement it — it bundles the `km` binary and runs `exec.Command("/var/task/km", "create", profilePath)` with the Lambda execution role credentials. This is the same pattern as TTL handler bundling terraform binary.

---

## Common Pitfalls

### Pitfall 1: Container Image Size and Build Time
**What goes wrong:** Dockerfile downloads terraform and terragrunt during `docker build` — slow CI, large image, version drift.
**Why it happens:** Binaries are large and version-pinned.
**How to avoid:** Pin specific terraform and terragrunt versions in the Dockerfile ARG. Cache the binary download layer. Use multi-stage build: download stage → final stage with just binaries + km + infra/modules.
**Warning signs:** `docker build` taking >5 minutes; image >300MB.

### Pitfall 2: Terragrunt `find_in_parent_folders` Fails in /tmp
**What goes wrong:** `terragrunt apply` in `/tmp/{sandbox-id}/` cannot find `site.hcl` — errors with "Could not find a terragrunt.hcl file".
**Why it happens:** Terragrunt walks up the directory tree from the sandbox directory looking for `site.hcl`. In the Lambda, `/tmp/` has no parent `site.hcl`.
**How to avoid:** Bundle the full `infra/live/` hierarchy into the container at `/var/task/infra/live/`. Then reconstruct the sandbox directory inside `/var/task/infra/live/use1/sandboxes/{sandbox-id}/` (writable inside container at /tmp — use a symlink or copy). Alternatively, use `TERRAGRUNT_CONFIG` env var to point to an explicit site.hcl path.
**Warning signs:** Terragrunt error: "Could not find a terragrunt.hcl file in the current directory or any of its parents".

### Pitfall 3: Lambda Execution Role Lacks PassRole for Sandbox IAM
**What goes wrong:** `terragrunt apply` fails with `iam:PassRole` error when trying to create the EC2 instance profile.
**Why it happens:** The Lambda role needs `iam:PassRole` on the sandbox role ARN pattern.
**How to avoid:** Add `iam:PassRole` to the create handler IAM role, scoped to `arn:aws:iam::*:role/km-ec2spot-*` and `arn:aws:iam::*:role/km-ecs-*`. Also add the Lambda role ARN to the Phase 10 SCP trusted carve-outs.
**Warning signs:** Terraform apply error: "is not authorized to perform: iam:PassRole".

### Pitfall 4: SES Receipt Rule Ordering / Position Conflicts
**What goes wrong:** The new `create-inbound` receipt rule fires for all emails (not just `create@`) or the existing `sandbox-inbound` rule intercepts create emails first.
**Why it happens:** SES evaluates receipt rules by position number in ascending order. Both rules match the same domain. The `create@` rule must have a lower position number (higher priority) than the catch-all rule.
**How to avoid:** Set `position = 1` on the new `create-inbound` rule. Set the existing `sandbox-inbound` rule to `position = 2`. Note: the `recipients` field on the `create-inbound` rule must be `["create@{domain}"]` (exact address), not just `["{domain}"]`.
**Warning signs:** Create emails routed to `mail/` instead of `mail/create/`; or all sandbox emails mistakenly routed to the create handler.

### Pitfall 5: Lambda Timeout for `km create`
**What goes wrong:** Lambda times out before `terragrunt apply` completes.
**Why it happens:** EC2 provisioning + IAM + security group creation can take 3–5 minutes. ECS cluster + task definition takes 2–4 minutes.
**How to avoid:** Set Lambda timeout to 900 seconds (15 minutes) — same as the ttl-handler. Use 1536MB RAM minimum (terraform is memory-hungry). Set 2048MB ephemeral storage (terraform downloads AWS provider ~500MB).
**Warning signs:** Lambda log shows "Task timed out after X seconds".

### Pitfall 6: EventBridge Retry on Create Lambda Failure
**What goes wrong:** If `terragrunt apply` fails midway and the Lambda returns an error, EventBridge retries — running a second create attempt on a partially-provisioned sandbox.
**Why it happens:** EventBridge default retry policy for Lambda targets is 2 retries with exponential backoff.
**How to avoid:** Set `maximum_retry_attempts = 0` on the EventBridge rule target (same as `budget-enforcer` module which explicitly sets this). The create event is not idempotent — partial apply + retry = duplicate resources or state conflict.
**Warning signs:** Duplicate `km-ec2spot-{sandbox-id}` security groups; "resource already exists" terraform errors.

### Pitfall 7: SCP Blocking Create Lambda IAM Actions
**What goes wrong:** The create Lambda's terraform apply fails because the SCP from Phase 10 blocks `iam:CreateRole` or `ec2:RunInstances`.
**Why it happens:** The Phase 10 SCP denies IAM escalation for all roles except a trusted carve-out list. The create Lambda role must be in that list.
**How to avoid:** Add `arn:aws:iam::${account}:role/km-create-handler` to `trusted_arns_iam` in the SCP. Update the SCP Terraform module (v1.0.0 → v1.1.0 pattern) or update the trusted ARNs variable in `infra/live/use1/scp/terragrunt.hcl`.
**Warning signs:** CloudTrail shows explicit deny from SCP on `iam:CreateRole`; org-level SCP error in terraform apply output.

### Pitfall 8: `km create` in Lambda Needs AWS Profile = Empty
**What goes wrong:** The Lambda's subprocess call to `km create` uses `--aws-profile klanker-terraform` (the local create default), which fails because that profile does not exist in the Lambda runtime.
**Why it happens:** `NewCreateCmd` defaults `awsProfile` to `"klanker-terraform"`. Inside Lambda, there are no named profiles — credentials come from the execution role via IMDS.
**How to avoid:** Pass `--aws-profile ""` when invoking `km create` as a Lambda subprocess. The `LoadAWSConfig` function calls `config.LoadDefaultConfig` which falls back to execution role when profile is empty string. Verify `pkg/aws/client.go` `LoadAWSConfig` handles empty profile correctly (it does — `config.WithSharedConfigProfile("")` is a no-op).

---

## Code Examples

### EventBridge Publisher (pkg/aws/eventbridge.go — new file)
```go
// Source: mirrors pkg/aws/scheduler.go narrow-interface pattern
package aws

import (
    "context"
    "encoding/json"
    "fmt"

    awssdk "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/eventbridge"
    eventbridgetypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
)

// EventBridgeAPI is the narrow interface for publishing events.
// Implemented by *eventbridge.Client.
type EventBridgeAPI interface {
    PutEvents(ctx context.Context, input *eventbridge.PutEventsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutEventsOutput, error)
}

// SandboxCreateDetail is the EventBridge detail payload for SandboxCreate events.
type SandboxCreateDetail struct {
    SandboxID      string `json:"sandbox_id"`
    ArtifactBucket string `json:"artifact_bucket"`
    ArtifactPrefix string `json:"artifact_prefix"`
    OperatorEmail  string `json:"operator_email"`
    OnDemand       bool   `json:"on_demand"`
}

// PutSandboxCreateEvent publishes a SandboxCreate event to the default EventBridge bus.
func PutSandboxCreateEvent(ctx context.Context, client EventBridgeAPI, detail SandboxCreateDetail) error {
    detailJSON, err := json.Marshal(detail)
    if err != nil {
        return fmt.Errorf("marshal SandboxCreate detail: %w", err)
    }
    out, err := client.PutEvents(ctx, &eventbridge.PutEventsInput{
        Entries: []eventbridgetypes.PutEventsRequestEntry{{
            Source:     awssdk.String("km.sandbox"),
            DetailType: awssdk.String("SandboxCreate"),
            Detail:     awssdk.String(string(detailJSON)),
        }},
    })
    if err != nil {
        return fmt.Errorf("put SandboxCreate event: %w", err)
    }
    if out.FailedEntryCount > 0 {
        return fmt.Errorf("SandboxCreate event failed: %v", out.Entries[0].ErrorMessage)
    }
    return nil
}
```

### Create Handler Lambda Main (cmd/create-handler/main.go — skeleton)
```go
// Source: mirrors cmd/ttl-handler/main.go structure
package main

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"

    lambdaruntime "github.com/aws/aws-lambda-go/lambda"
    awssdk "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/aws/aws-sdk-go-v2/service/sesv2"
    awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

type CreateEvent struct {
    SandboxID      string `json:"sandbox_id"`
    ArtifactBucket string `json:"artifact_bucket"`
    ArtifactPrefix string `json:"artifact_prefix"`
    OperatorEmail  string `json:"operator_email"`
    OnDemand       bool   `json:"on_demand"`
}

type CreateHandler struct {
    S3Client      *s3.Client
    SESClient     awspkg.SESV2API
    Bucket        string
    Domain        string
    OperatorEmail string
}

func (h *CreateHandler) Handle(ctx context.Context, event CreateEvent) error {
    sandboxID := event.SandboxID

    // 1. Download profile YAML from S3
    profileKey := event.ArtifactPrefix + ".km-profile.yaml"
    resp, err := h.S3Client.GetObject(ctx, &s3.GetObjectInput{
        Bucket: awssdk.String(event.ArtifactBucket),
        Key:    awssdk.String(profileKey),
    })
    if err != nil {
        return fmt.Errorf("download profile: %w", err)
    }
    defer resp.Body.Close()

    // 2. Write profile to /tmp/{sandbox-id}.yaml
    profilePath := filepath.Join("/tmp", sandboxID+".yaml")
    // ... write profile bytes to profilePath

    // 3. Run km create as subprocess (km binary at /var/task/km)
    args := []string{"create", profilePath, "--aws-profile", ""}
    if event.OnDemand {
        args = append(args, "--on-demand")
    }
    cmd := exec.CommandContext(ctx, "/var/task/km", args...)
    cmd.Env = append(os.Environ(),
        "KM_ARTIFACTS_BUCKET="+event.ArtifactBucket,
        "KM_OPERATOR_EMAIL="+event.OperatorEmail,
    )
    out, err := cmd.CombinedOutput()
    if err != nil {
        // Send failure notification
        awspkg.SendLifecycleNotification(ctx, h.SESClient, event.OperatorEmail, sandboxID, "create-failed", h.Domain)
        return fmt.Errorf("km create failed: %s: %w", string(out), err)
    }

    // 4. Send success notification (km create already sends "created" notification)
    // km create Step 14 sends the notification — no duplicate needed here
    return nil
}
```

### Email Create Handler Lambda (cmd/email-create-handler/main.go — skeleton)
```go
// Source: mirrors pkg/aws/mailbox.go ParseSignedMessage + kmAuthPattern
package main

import (
    "context"
    "io"
    "mime"
    "mime/multipart"
    "net/mail"
    "strings"

    lambdaruntime "github.com/aws/aws-lambda-go/lambda"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    awspkg "github.com/whereiskurt/klankrmkr/pkg/aws"
)

// S3EventRecord is the S3 notification event structure.
type S3EventRecord struct {
    Records []struct {
        S3 struct {
            Bucket struct{ Name string `json:"name"` } `json:"bucket"`
            Object struct{ Key  string `json:"key"`  } `json:"object"`
        } `json:"s3"`
    } `json:"Records"`
}

func handleEmailCreate(ctx context.Context, event S3EventRecord) error {
    // 1. GetObject for the SES-delivered email
    // 2. net/mail.ReadMessage() to parse MIME
    // 3. Extract KM-AUTH phrase: awspkg.kmAuthPattern.FindStringSubmatch(body)
    //    Note: kmAuthPattern is unexported — copy the regex or export it
    // 4. GetParameter SSM /km/config/remote-create/safe-phrase
    // 5. Compare phrases — reject if mismatch, send rejection email via SES
    // 6. Extract YAML from multipart attachment (Content-Type: text/yaml or application/x-yaml)
    //    or plain text body if single-part
    // 7. profile.Parse(yamlBytes) to validate
    // 8. Upload profile to s3://artifacts/remote-create/{uuid}/.km-profile.yaml
    // 9. PutSandboxCreateEvent(ctx, ebClient, detail)
    // 10. Send acknowledgment: "Your sandbox creation request has been received: {sandbox-id}"
    return nil
}
```

**Note on `kmAuthPattern`:** It is currently unexported (`var kmAuthPattern = regexp.MustCompile(...)`). Either export it (`KMAuthPattern`) or move it to a shared location. The email-create handler is in a separate `cmd/` package and cannot access unexported symbols from `pkg/aws`.

### Dockerfile for Create Handler (container image Lambda)
```dockerfile
# Multi-stage build for km-create-handler Lambda
# Stage 1: Download terraform + terragrunt
FROM public.ecr.aws/amazonlinux/amazonlinux:2023 AS downloader
ARG TERRAFORM_VERSION=1.9.8
ARG TERRAGRUNT_VERSION=0.67.16
RUN yum install -y curl unzip && \
    curl -Lo /tmp/terraform.zip \
      https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_arm64.zip && \
    unzip /tmp/terraform.zip -d /usr/local/bin/ && \
    curl -Lo /usr/local/bin/terragrunt \
      https://github.com/gruntwork-io/terragrunt/releases/download/v${TERRAGRUNT_VERSION}/terragrunt_linux_arm64 && \
    chmod +x /usr/local/bin/terragrunt

# Stage 2: Lambda runtime
FROM public.ecr.aws/lambda/provided:al2023-arm64
COPY --from=downloader /usr/local/bin/terraform /var/task/terraform
COPY --from=downloader /usr/local/bin/terragrunt /var/task/terragrunt
# km binary (pre-built for linux/arm64)
COPY build/km-create-handler /var/task/bootstrap
# km CLI binary for subprocess invocation
COPY build/km /var/task/km
# Terraform modules and Terragrunt hierarchy
COPY infra/ /var/task/infra/
CMD ["bootstrap"]
```

### Terraform Module for Create Handler (infra/modules/create-handler/v1.0.0/main.tf — key excerpts)
```hcl
resource "aws_lambda_function" "create_handler" {
  function_name = "km-create-handler"
  role          = aws_iam_role.create_handler.arn
  package_type  = "Image"
  image_uri     = var.ecr_image_uri
  architectures = ["arm64"]
  timeout       = 900    # 15 minutes
  memory_size   = 1536
  ephemeral_storage { size = 2048 }
  environment {
    variables = {
      KM_ARTIFACTS_BUCKET = var.artifact_bucket_name
      KM_EMAIL_DOMAIN     = var.email_domain
      KM_OPERATOR_EMAIL   = var.operator_email
      KM_STATE_BUCKET     = var.state_bucket
      KM_STATE_PREFIX     = var.state_prefix
      KM_REGION_LABEL     = var.region_label
    }
  }
}

# EventBridge rule: route SandboxCreate events to create Lambda
resource "aws_cloudwatch_event_rule" "sandbox_create" {
  name        = "km-sandbox-create"
  event_pattern = jsonencode({
    source      = ["km.sandbox"]
    detail-type = ["SandboxCreate"]
  })
}

resource "aws_cloudwatch_event_target" "create_to_lambda" {
  rule      = aws_cloudwatch_event_rule.sandbox_create.name
  target_id = "km-create-handler"
  arn       = aws_lambda_function.create_handler.arn

  retry_policy {
    maximum_retry_attempts = 0  # create is not idempotent
  }
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Zip-based Lambda for TTL/budget | Still zip for small Go binaries | Phases 4–6 | Create Lambda needs container image due to terraform binary size |
| Lambda with terraform subprocess | Proven in ttl-handler (terraformDestroy) | Phase 4 | Create Lambda reuses same pattern for apply |
| No remote create path | `--remote` flag + EventBridge dispatch | Phase 22 | Enables create without local terraform |
| Per-sandbox safe phrase only | Per-sandbox + global create safe phrase | Phase 22 | Two distinct auth contexts |

**Lambda container image deployment (current approach):**
AWS Lambda container images must be stored in ECR (not any registry). The ECR repository must be in the same region as the Lambda. The container image URI format is: `{account}.dkr.ecr.{region}.amazonaws.com/{repo}:{tag}`. Container Lambda cold start is ~1–2 seconds extra vs zip Lambda — acceptable for sandbox creation.

**EventBridge PutEvents vs EventBridge Scheduler:**
Phase 22 uses EventBridge Events (rules), not EventBridge Scheduler. The existing code uses Scheduler for TTL (time-based); here we use Events (pattern-based routing). The SDK clients are different: `eventbridge` vs `scheduler`.

---

## Open Questions

1. **km create subprocess vs reimplemented create flow in Lambda**
   - What we know: Calling `km create` as a subprocess is simpler and reuses all existing logic including Steps 11-15. But it requires the `km` binary in the container and means Lambda must run `km create` with empty `--aws-profile` (using execution role).
   - What's unclear: Whether `km create` subprocess correctly falls back to execution role credentials when `--aws-profile ""`. The `LoadAWSConfig(ctx, "")` call with empty profile should use the ambient credential chain (execution role in Lambda).
   - Recommendation: Verify in `pkg/aws/client.go` that empty profile string uses default credential chain. If it does (expected), use subprocess approach. If not, add explicit handling.

2. **Email YAML attachment vs email body YAML**
   - What we know: Operators may send YAML as an attachment (Content-Type: text/yaml, application/x-yaml) or as the plain text body.
   - What's unclear: Which convention to enforce in the email-create handler.
   - Recommendation: Support both. If message is single-part with Content-Type text/plain or text/yaml, treat the body as the YAML. If multipart, look for a part with Content-Type text/yaml or application/x-yaml or filename ending in `.yaml`.

3. **Global create safe phrase vs per-request phrase**
   - What we know: The phase description says "must match operator-configured safe phrase in SSM". A global phrase (set once) is simple but means anyone who gets the phrase can trigger creates indefinitely.
   - What's unclear: Whether the phrase should be rotated per-request.
   - Recommendation: Global phrase (simpler, matches existing per-sandbox safe phrase pattern). Operator can rotate it manually via SSM Console or `km configure`.

4. **kmAuthPattern export**
   - What we know: `kmAuthPattern` in `pkg/aws/mailbox.go` is unexported.
   - What's unclear: Whether to export it or duplicate it in `cmd/email-create-handler/main.go`.
   - Recommendation: Export as `KMAuthPattern` in `pkg/aws/mailbox.go` — it will be used by at least two consumers (email-create handler + potentially future email-based actions).

5. **SES receipt rule: S3 prefix for create emails**
   - What we know: Current receipt rule uses flat `mail/` prefix. We need to distinguish `create@` emails from sandbox inbox emails.
   - Recommendation: Use `mail/create/` prefix for emails to `create@`. S3 event notification on `mail/create/` prefix triggers email-create Lambda. This avoids modifying the existing `sandbox-inbound` rule and keeps the create path clean.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (stdlib) — `go test ./...` |
| Config file | none |
| Quick run command | `go test ./cmd/create-handler/... ./cmd/email-create-handler/... ./pkg/aws/... -run TestCreate -v` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| REMOTE-01 | Container image Lambda builds and starts | smoke | `docker run km-create-handler /var/task/km version` | No — Wave 0 |
| REMOTE-01 | CreateHandler.Handle returns no error for valid CreateEvent | unit | `go test ./cmd/create-handler/... -run TestHandle_ValidEvent -v` | No — Wave 0 |
| REMOTE-02 | PutSandboxCreateEvent publishes correct event shape | unit | `go test ./pkg/aws/... -run TestPutSandboxCreateEvent -v` | No — Wave 0 |
| REMOTE-02 | `km create --remote` calls PutSandboxCreateEvent (source verify) | unit | `go test ./internal/app/cmd/... -run TestCreateRemote_PublishesEvent -v` | No — Wave 0 |
| REMOTE-03 | handleEmailCreate parses YAML from multipart attachment | unit | `go test ./cmd/email-create-handler/... -run TestHandleEmail_MultipartYAML -v` | No — Wave 0 |
| REMOTE-03 | handleEmailCreate parses YAML from plain text body | unit | `go test ./cmd/email-create-handler/... -run TestHandleEmail_PlainYAML -v` | No — Wave 0 |
| REMOTE-04 | handleEmailCreate rejects email with wrong KM-AUTH phrase | unit | `go test ./cmd/email-create-handler/... -run TestHandleEmail_WrongPhrase -v` | No — Wave 0 |
| REMOTE-04 | handleEmailCreate accepts email with correct KM-AUTH phrase | unit | `go test ./cmd/email-create-handler/... -run TestHandleEmail_CorrectPhrase -v` | No — Wave 0 |
| REMOTE-05 | EventBridge rule routes SandboxCreate to create Lambda (config test) | integration | manual — check EventBridge console or CloudTrail | No — manual only |
| REMOTE-06 | CreateHandler sends "created" notification on success | unit | `go test ./cmd/create-handler/... -run TestHandle_SendsNotification -v` | No — Wave 0 |
| REMOTE-06 | CreateHandler sends "create-failed" notification on error | unit | `go test ./cmd/create-handler/... -run TestHandle_SendsFailureNotification -v` | No — Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./pkg/aws/... ./cmd/create-handler/... ./cmd/email-create-handler/... -v`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `cmd/create-handler/main_test.go` — mock S3, SES, exec.Cmd DI; covers Handle, subprocess invocation
- [ ] `cmd/email-create-handler/main_test.go` — mock S3, SSM, EventBridge; covers MIME parsing, safe phrase check
- [ ] `pkg/aws/eventbridge.go` — PutSandboxCreateEvent function + EventBridgeAPI interface
- [ ] `pkg/aws/eventbridge_test.go` — mock EventBridgeAPI; covers PutSandboxCreateEvent happy path + failed entry handling
- [ ] `internal/app/cmd/create_remote_test.go` — verifies `--remote` flag wires up PutSandboxCreateEvent call
- [ ] Makefile additions: `build-create-handler` target (container build), `push-create-handler` target (ECR push)

*(All existing test files remain valid; only additions needed)*

---

## Sources

### Primary (HIGH confidence)
- Codebase: `cmd/ttl-handler/main.go` — Lambda handler structure, subprocess terraform pattern, IAM shape, env vars
- Codebase: `infra/modules/ttl-handler/v1.0.0/main.tf` — EventBridge event rule pattern, Lambda permissions, scheduler IAM
- Codebase: `infra/modules/budget-enforcer/v1.0.0/main.tf` — `maximum_retry_attempts = 0` pattern, per-sandbox Lambda shape
- Codebase: `infra/modules/ses/v1.0.0/main.tf` — SES receipt rule structure, S3 action, S3 bucket policy ownership
- Codebase: `internal/app/cmd/create.go` — full create workflow (Steps 1–15), safe phrase (Step 12d), existing `--remote` flag not yet present
- Codebase: `pkg/aws/mailbox.go` — `kmAuthPattern` regex, `MailboxS3API` interface, `ParseSignedMessage` structure
- Codebase: `pkg/aws/ses.go` — `SendLifecycleNotification` signature and pattern
- Codebase: `pkg/compiler/compiler.go` — `CompiledArtifacts` struct, `Compile()` signature
- Codebase: `Makefile` — `build-lambdas` targets, container build via `buildx`, ECR push pattern
- AWS Lambda container image docs (verified from codebase usage and existing ECR patterns in Makefile)

### Secondary (MEDIUM confidence)
- AWS Lambda zip limit (250MB) — well-known constraint, consistent with phase description's "container image for size" rationale
- EventBridge `PutEvents` API — standard AWS pattern; SDK is analogous to existing `scheduler` usage
- terraform arm64 binary size ~65MB — from public Hashicorp releases page; consistent with Dockerfile sizing recommendation

### Tertiary (LOW confidence — verify during planning/implementation)
- Specific terraform/terragrunt version numbers (1.9.8 / 0.67.16) — current as of research date; verify latest stable before Dockerfile pinning
- Lambda container image cold start penalty (~1-2s) — from AWS documentation; acceptable for sandbox creation use case
- ECR `provided.al2023-arm64` base image tag availability — verify with `aws ecr-public describe-images` or Docker Hub

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all dependencies already in go.mod except `eventbridge`; container image approach confirmed by size analysis
- Architecture patterns: HIGH — all patterns directly mirror existing code in the repo
- Pitfalls: HIGH — Terragrunt hierarchy issue and SCP carve-out are confirmed from code inspection; EventBridge retry risk confirmed from existing `budget-enforcer` module pattern (which explicitly sets retries to 0)
- Open questions: MEDIUM — subprocess vs reimplemented flow is a design choice; both are safe

**Research date:** 2026-03-26
**Valid until:** 2026-04-26 (stable domain — AWS SDK v2, Lambda, EventBridge are stable; terraform/terragrunt versions should be re-verified at implementation time)
