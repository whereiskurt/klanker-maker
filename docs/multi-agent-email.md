# Klanker Maker Multi-Agent Email Guide

## Table of Contents

1. [Overview](#overview)
2. [Email Addressing](#email-addressing)
3. [SES Setup](#ses-setup)
4. [Inbound Email Flow](#inbound-email-flow)
5. [Outbound Email Flow](#outbound-email-flow)
6. [Signed Email (Phase 14)](#signed-email-phase-14)
7. [Optional Encryption (Phase 14)](#optional-encryption-phase-14)
8. [Profile spec.email Controls (Phase 14)](#profile-specemail-controls-phase-14)
9. [Lifecycle Notifications](#lifecycle-notifications)
10. [Cross-Sandbox Orchestration](#cross-sandbox-orchestration)
11. [Structured Email Format](#structured-email-format)
12. [Example: Multi-Agent Pipeline](#example-multi-agent-pipeline)
13. [Security Considerations](#security-considerations)
14. [S3 Replication](#s3-replication)

## Overview

Every sandbox provisioned by Klanker Maker receives a unique email address. SES (Amazon Simple Email Service) is provisioned per sandbox during `km create`, giving each agent the ability to send and receive email without any shared network or IAM boundary between sandboxes.

This design enables cross-sandbox orchestration through email as the sole communication channel. Agents running in isolated sandboxes can coordinate work, pass structured messages, and build multi-stage pipelines -- all without VPC peering, shared databases, or cross-account IAM roles.

Key properties:

- Each sandbox gets exactly one email address, provisioned automatically.
- Agents send email via the SES API using their sandbox IAM role.
- Inbound email is stored in S3, where agents read it as objects.
- No sandbox can send email as another sandbox (IAM condition enforcement).
- Email is the only cross-sandbox communication path. DNS/HTTP proxies do not route inter-sandbox traffic.

## Email Addressing

Sandbox email addresses follow a deterministic format:

```
{sandbox-id}@sandboxes.{domain}
```

For example:

```
sb-a1b2c3d4@sandboxes.klankermaker.ai
```

The domain is configurable via `km configure`. The default is `sandboxes.klankermaker.ai`. Once set, the domain applies to all sandboxes in the account.

The address is exposed inside the sandbox as the `KM_EMAIL_ADDRESS` environment variable, available in both EC2 and ECS substrates. Agents read this variable to discover their own address.

```bash
# Inside a sandbox
echo $KM_EMAIL_ADDRESS
# sb-a1b2c3d4@sandboxes.klankermaker.ai
```

## SES Setup

The SES infrastructure is provisioned by the Terraform module at `infra/modules/ses/v1.0.0/`. During `km init` or `km create`, the following resources are created automatically:

### DNS Records

| Record | Type | Purpose |
|--------|------|---------|
| `{token}._domainkey.sandboxes.{domain}` (x3) | CNAME | DKIM email authentication |
| `_amazonses.sandboxes.{domain}` | TXT | SES domain verification |
| `sandboxes.{domain}` | MX | Routes inbound email to SES (`10 inbound-smtp.{region}.amazonaws.com`) |

### SES Resources

| Resource | Purpose |
|----------|---------|
| `aws_ses_domain_identity` | Domain identity for the sandbox subdomain |
| `aws_ses_domain_dkim` | DKIM signing tokens (3 CNAME records) |
| `aws_ses_receipt_rule_set` | Named rule set (`km-sandbox-email`), activated for the region |
| `aws_ses_receipt_rule` | Inbound rule matching the domain, storing email to S3 |

The receipt rule matches all addresses at the domain and stores inbound messages to the artifacts bucket under the `mail/` prefix.

### Terraform Variables

The module requires four inputs:

- `domain` -- the email subdomain (e.g., `sandboxes.klankermaker.ai`)
- `route53_zone_id` -- hosted zone ID for DNS record creation
- `artifact_bucket_name` -- S3 bucket name for inbound email storage
- `artifact_bucket_arn` -- ARN of the artifact bucket (used in the bucket policy)

## Inbound Email Flow

When an email is sent to a sandbox address, it follows this path:

```
Sender
  |
  v
MX record (Route53)
  |
  v
SES inbound-smtp.{region}.amazonaws.com
  |
  v
Receipt rule: sandbox-inbound
  |
  v
S3 action: s3://{artifact-bucket}/mail/{message-id}
```

The SES receipt rule stores the raw email object in S3 under the `mail/` prefix in the artifacts bucket. The S3 bucket policy grants `ses.amazonaws.com` the `s3:PutObject` permission on the `mail/*` path, scoped to the owning AWS account via an `aws:Referer` condition.

Agents poll their inbox by listing and reading objects from S3:

```bash
# List inbox messages
aws s3api list-objects-v2 \
  --bucket "$KM_ARTIFACTS_BUCKET" \
  --prefix "mail/$SANDBOX_ID/" \
  --query 'Contents[].Key'

# Read a specific message
aws s3api get-object \
  --bucket "$KM_ARTIFACTS_BUCKET" \
  --key "mail/$SANDBOX_ID/inbox/abc123" \
  /tmp/message.eml
```

The IAM role for each sandbox includes `s3:ListObjectsV2` and `s3:GetObject` permissions scoped to `mail/{sandbox-id}/*`, plus `s3:ListBucket` with an `s3:prefix` condition. This means a sandbox can only read its own inbox -- not the inbox of any other sandbox.

## Outbound Email Flow

Agents send email by calling the SES `SendEmail` or `SendRawEmail` API from within the sandbox. The sandbox IAM role grants `ses:SendEmail` and `ses:SendRawEmail` with a condition that the `ses:FromAddress` must match the sandbox's own email address.

```bash
# Send email from within a sandbox
aws sesv2 send-email \
  --from-email-address "$KM_EMAIL_ADDRESS" \
  --destination "ToAddresses=sb-x9y8z7w6@sandboxes.klankermaker.ai" \
  --content "Simple={
    Subject={Data='task-result'},
    Body={Text={Data='{\"status\":\"complete\",\"output\":\"s3://bucket/artifacts/sb-a1b2c3d4/result.tar.gz\"}'}}
  }"
```

The IAM policy enforcing sender identity looks like this in the ECS service template:

```hcl
{
  effect    = "Allow"
  actions   = ["ses:SendEmail", "ses:SendRawEmail"]
  resources = ["*"]
  condition = {
    test     = "StringEquals"
    variable = "ses:FromAddress"
    values   = ["{sandbox-email}"]
  }
}
```

This prevents any sandbox from impersonating another sandbox's email address.

## Signed Email (Phase 14)

Phase 14 introduced an Ed25519-based email signing protocol for inter-sandbox trust. When signing is enabled, every outbound message carries cryptographic headers that allow the receiver to verify the sender's identity without relying solely on IAM sender restrictions.

### How Signing Works

`SignEmailBody` signs the **email body only** (not headers). This design choice makes verification resilient to transit — SES and intermediate MTAs routinely modify headers (DKIM, Received, etc.) but must not alter the body.

The signature is produced using the sender's Ed25519 private key stored in SSM Parameter Store at `/sandbox/{sandbox-id}/signing-key`. The corresponding public key is published to the DynamoDB `km-identities` table during `km create`.

### Custom Headers

Every signed email carries two custom headers:

| Header | Value | Purpose |
|--------|-------|---------|
| `X-KM-Sender-ID` | `sb-a1b2c3d4` | Sender's sandbox ID; receiver uses this to look up the public key in DynamoDB |
| `X-KM-Signature` | base64-encoded Ed25519 signature | Signature over the raw body bytes |

### Why Content.Raw (not Content.Simple)

`SendSignedEmail` sends via SES `Content.Raw` (raw MIME bytes), not `Content.Simple`. SES strips unknown `X-KM-*` headers from Simple messages at delivery time. Raw MIME preserves all headers exactly as written.

### Example Signed Email Headers

```
From: sb-a1b2c3d4@sandboxes.klankermaker.ai
To: sb-x9y8z7w6@sandboxes.klankermaker.ai
Subject: km-agent:task-result:corr-7f3a9b01
X-KM-Sender-ID: sb-a1b2c3d4
X-KM-Signature: 4vD7...base64...==
MIME-Version: 1.0
Content-Type: text/plain; charset="UTF-8"

{"schema_version":"1.0","action":"task-result",...}
```

### Verification Flow

The receiving sandbox verifies an inbound signed email as follows:

1. Read `X-KM-Sender-ID` from the email headers to identify the sender sandbox.
2. Call `FetchPublicKey(ctx, dynClient, "km-identities", senderSandboxID)` to retrieve the sender's Ed25519 public key from DynamoDB.
3. Call `VerifyEmailSignature(pubKeyB64, body, sigB64)` to verify the `X-KM-Signature` against the email body.
4. If verification fails, reject the message (if `verifyInbound: required`) or log and continue (if `optional`).

```go
record, err := aws.FetchPublicKey(ctx, dynClient, "km-identities", senderSandboxID)
if err != nil || record == nil {
    // no identity published — handle per policy
}
if err := aws.VerifyEmailSignature(record.PublicKeyB64, body, sig); err != nil {
    // signature invalid
}
```

---

## Optional Encryption (Phase 14)

In addition to signing, sandboxes can encrypt email content end-to-end using NaCl box encryption. Encryption is independent of signing — a message can be signed only, encrypted only, or both.

### Key Generation and Storage

Each sandbox has two separate key pairs:

| Key Type | Algorithm | SSM Path | DynamoDB Attribute |
|----------|-----------|----------|-------------------|
| Signing key | Ed25519 (64 bytes) | `/sandbox/{id}/signing-key` | `public_key` |
| Encryption key | X25519 (32 bytes) | `/sandbox/{id}/encryption-key` | `encryption_public_key` |

The encryption key pair is a dedicated X25519 key generated by `GenerateEncryptionKey`. It is separate from the Ed25519 signing key (the two algorithms serve different roles and use different key formats).

### Encryption Protocol

Encryption uses `box.SealAnonymous` from `golang.org/x/crypto/nacl/box`. The sender does not embed its identity in the ciphertext — the sender's sandbox ID travels in the `X-KM-Sender-ID` header.

```
Sender (sandbox A)                              Receiver (sandbox B)
-----------------                               --------------------
1. Fetch B's encryption_public_key from DynamoDB (km-identities)
2. box.SealAnonymous(plaintext, recipientPubKey) → ciphertext
3. base64-encode ciphertext → bodyToSign
4. Sign bodyToSign with A's Ed25519 signing key
5. Send raw MIME with X-KM-Encrypted: true, X-KM-Signature, X-KM-Sender-ID

                   ─── email ───>

                                                6. Read X-KM-Sender-ID = "sb-a1b2c3d4"
                                                7. Verify X-KM-Signature (optional per policy)
                                                8. base64-decode ciphertext body
                                                9. box.OpenAnonymous(ciphertext, B.pubKey, B.privKey) → plaintext
```

### The X-KM-Encrypted Header

When encryption is applied, the email carries an additional header:

```
X-KM-Encrypted: true
```

The absence of this header means the body is plaintext (even if signed). Receivers use this header to decide whether to attempt decryption.

### DynamoDB Public Key Discovery

The sender fetches the receiver's X25519 public key at send time:

```go
record, err := aws.FetchPublicKey(ctx, dynClient, "km-identities", recipientSandboxID)
// record.EncryptionPublicKeyB64 contains the base64-encoded X25519 public key
```

If the recipient has no published encryption key and the sender's policy is `encryption: optional`, the message is sent in plaintext. If the policy is `encryption: required`, the send fails with an error.

### Example: Sandbox A Encrypts a Message to Sandbox B

```bash
# Conceptual flow (implemented in SendSignedEmail in pkg/aws/identity.go)

# 1. Look up sandbox B's identity
RECORD=$(km identity fetch sb-x9y8z7w6)
ENC_PUB_KEY=$(echo "$RECORD" | jq -r '.encryption_public_key')

# 2. Encrypt (box.SealAnonymous) and base64-encode
CIPHERTEXT=$(echo '{"action":"task-result"}' | nacl-encrypt "$ENC_PUB_KEY" | base64)

# 3. Sign the ciphertext, send with X-KM-Encrypted: true
```

In Go, this is handled transparently by `SendSignedEmail` when `encryptionPolicy` is set to `"required"` or `"optional"`.

---

## Profile spec.email Controls (Phase 14)

Email signing and encryption behavior is controlled by the `spec.email` block in the sandbox profile. This field is a pointer on `Spec` — when omitted entirely (`nil`), email policy enforcement is disabled and the sandbox uses unrestricted plain email (default behavior for legacy sandboxes).

### Fields

| Field | Type | Values | Description |
|-------|------|--------|-------------|
| `spec.email.signing` | string | `required` \| `optional` \| `off` | Controls Ed25519 signing of outbound email |
| `spec.email.verifyInbound` | string | `required` \| `optional` \| `off` | Controls signature verification of inbound email; `required` rejects unsigned messages |
| `spec.email.encryption` | string | `required` \| `optional` \| `off` | Controls NaCl encrypted email for outbound messages |
| `spec.email.alias` | string | `"team-a.research"` | Human-friendly dot-notation name registered in `km-identities` alias-index GSI (per-sandbox, not per-profile-template) |
| `spec.email.allowedSenders` | list | `["self"]`, `["*"]`, `["sb-abc123"]`, `["build.*"]` | Allow-list of which sandboxes may send to this sandbox; empty means unrestricted |

### Example Profile YAML

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: secure-worker
spec:
  email:
    signing: required
    verifyInbound: required
    encryption: optional
    allowedSenders:
      - "self"
      - "sb-plan0001"
```

### How nil Behaves

`EmailSpec` is stored as `*EmailSpec` on `Spec`. When no `spec.email` block is present:

- The field is `nil` — not the zero value of `EmailSpec`.
- Email policy is not enforced: signing is off, verification is skipped, all senders are accepted.
- This preserves backward compatibility with sandboxes provisioned before Phase 14.

### Built-in Profile Defaults

| Profile | signing | verifyInbound | encryption | allowedSenders |
|---------|---------|---------------|------------|----------------|
| `hardened` | `required` | `required` | `off` | `["self"]` |
| `sealed` | `required` | `required` | `off` | `["self"]` |
| `goose` | `required` | `required` | `off` | `["self"]` |
| `goose-ebpf` | `required` | `required` | `off` | `["self"]` |
| `goose-ebpf-gatekeeper` | `required` | `required` | `off` | `["self"]` |

All built-in profiles enforce full signing and restrict inbound to self-mail only. Custom profiles can relax these settings for inter-sandbox communication.

---

## Lifecycle Notifications

Operators receive email notifications for sandbox lifecycle events. Notifications are sent to the address specified in the `KM_OPERATOR_EMAIL` environment variable. If the variable is unset, notifications are silently skipped.

### Notification Events

| Event | Trigger | Sent From |
|-------|---------|-----------|
| `created` | Sandbox provisioned successfully | `km create` CLI |
| `destroyed` | Sandbox torn down | `km destroy` CLI |
| `ttl-expired` | TTL timer reached | EventBridge scheduler callback |
| `idle-timeout` | No activity for configured duration | Scheduler callback |
| `spot-interruption` | EC2 spot instance reclaimed | EC2 user-data spot poll loop (via AWS CLI) |
| `budget-warning` | Sandbox cost reaches 80% of budget | Budget monitor |
| `budget-exhausted` | Sandbox cost reaches 100% of budget | Budget monitor (triggers teardown) |
| `error` | Non-zero exit or crash detected | Error teardown path |

### Notification Format

```
Subject: km sandbox {event}: {sandbox-id}
Body:    Sandbox {sandbox-id} {event description}. {additional context}
From:    notifications@sandboxes.{domain}
To:      {KM_OPERATOR_EMAIL}
```

For EC2 spot interruptions, the notification is sent directly from the instance using the AWS CLI before the instance is terminated:

```bash
aws sesv2 send-email \
  --from-email-address "notifications@sandboxes.klankermaker.ai" \
  --destination "ToAddresses=$KM_OPERATOR_EMAIL" \
  --content "Simple={
    Subject={Data='km sandbox spot-interruption: $SANDBOX_ID'},
    Body={Text={Data='Sandbox $SANDBOX_ID received spot interruption notice. Artifacts uploaded (best-effort).'}}
  }" \
  --region "$AWS_REGION"
```

## Cross-Sandbox Orchestration

The core pattern for multi-agent orchestration is straightforward:

1. Agent A in sandbox-1 sends a structured email to Agent B in sandbox-2.
2. Agent B polls its S3 inbox, parses the message, and acts on it.
3. Agent B sends a response email back to Agent A.
4. Agent A polls its S3 inbox and reads the response.

This works because:

- Sandboxes share a regional VPC but Security Groups block all cross-sandbox traffic — no network path to each other.
- No shared IAM is required. Each sandbox has its own scoped role.
- No shared state is required. Email is the message bus.
- Every message is persisted in S3. There is a complete audit trail.

### Polling Pattern

Agents implement a simple poll loop to watch for incoming messages:

```python
import boto3, json, time

s3 = boto3.client('s3')
bucket = os.environ['KM_ARTIFACTS_BUCKET']
sandbox_id = os.environ['SANDBOX_ID']
prefix = f"mail/{sandbox_id}/"

def poll_inbox(timeout_seconds=300, interval=5):
    deadline = time.time() + timeout_seconds
    seen = set()
    while time.time() < deadline:
        resp = s3.list_objects_v2(Bucket=bucket, Prefix=prefix)
        for obj in resp.get('Contents', []):
            if obj['Key'] not in seen:
                seen.add(obj['Key'])
                body = s3.get_object(Bucket=bucket, Key=obj['Key'])['Body'].read()
                yield parse_message(body)
        time.sleep(interval)
```

## Structured Email Format

For agent-to-agent communication, messages use a JSON-in-email format that is both human-readable and machine-parseable.

### Message Structure

**Subject line** encodes the action:

```
km-agent:{action}:{correlation-id}
```

**Headers** carry routing metadata:

| Header | Purpose |
|--------|---------|
| `From` | Sender sandbox address |
| `To` | Recipient sandbox address |
| `Subject` | Action and correlation ID |
| `X-KM-Action` | Action type (e.g., `task-assign`, `task-result`, `review-request`) |
| `X-KM-Correlation-ID` | UUID linking related messages across sandboxes |
| `X-KM-Sandbox-ID` | Sender's sandbox ID |
| `X-KM-Timestamp` | ISO 8601 timestamp |
| `X-KM-Schema-Version` | Message schema version (e.g., `1.0`) |

**Body** is a JSON payload:

```json
{
  "schema_version": "1.0",
  "action": "task-assign",
  "correlation_id": "550e8400-e29b-41d4-a716-446655440000",
  "sender": {
    "sandbox_id": "sb-a1b2c3d4",
    "email": "sb-a1b2c3d4@sandboxes.klankermaker.ai",
    "role": "planner"
  },
  "payload": {
    "task": "Implement the user authentication module",
    "repo": "github.com/org/project",
    "branch": "feature/auth",
    "context": {
      "language": "go",
      "test_command": "go test ./pkg/auth/...",
      "acceptance_criteria": [
        "JWT token generation and validation",
        "Password hashing with bcrypt",
        "Session middleware for HTTP handlers"
      ]
    }
  },
  "reply_to": "sb-a1b2c3d4@sandboxes.klankermaker.ai",
  "timeout": "30m"
}
```

### Action Types

| Action | Direction | Purpose |
|--------|-----------|---------|
| `task-assign` | planner -> worker | Assign a subtask to a worker sandbox |
| `task-result` | worker -> planner/reviewer | Report task completion with results |
| `review-request` | worker -> reviewer | Request code review |
| `review-response` | reviewer -> worker/planner | Approve or reject with feedback |
| `status-query` | any -> any | Request current status |
| `status-response` | any -> any | Report current status |
| `abort` | planner -> worker | Cancel an in-progress task |

## Example: Multi-Agent Pipeline

This example walks through a 3-sandbox pipeline where a planner breaks a task into subtasks, a worker implements code, and a reviewer validates the result.

### Sandbox Profiles

**Planner** (`profiles/pipeline-planner.yaml`):

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: pipeline-planner
  labels:
    tier: development
    role: planner

spec:
  lifecycle:
    ttl: "2h"
    idleTimeout: "30m"
    teardownPolicy: destroy

  runtime:
    substrate: ecs
    spot: true
    instanceType: t3.small
    region: us-east-1

  execution:
    shell: /bin/bash
    workingDir: /workspace
    env:
      SANDBOX_MODE: pipeline-planner
      PIPELINE_ROLE: planner

  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"

  identity:
    roleSessionDuration: "1h"
    allowedRegions:
      - us-east-1
    sessionPolicy: minimal

  sidecars:
    dnsProxy:
      enabled: true
      image: km-dns-proxy:latest
    httpProxy:
      enabled: true
      image: km-http-proxy:latest
    auditLog:
      enabled: true
      image: km-audit-log:latest

  policy:
    allowShellEscape: false
    allowedCommands:
      - bash
      - python3
      - aws
    filesystemPolicy:
      writablePaths:
        - /workspace
        - /tmp

  agent:
    maxConcurrentTasks: 1
    taskTimeout: "30m"
    allowedTools:
      - bash
      - read_file
      - write_file
```

**Worker** (`profiles/pipeline-worker.yaml`):

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: pipeline-worker
  labels:
    tier: development
    role: worker

spec:
  lifecycle:
    ttl: "1h"
    idleTimeout: "20m"
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
      SANDBOX_MODE: pipeline-worker
      PIPELINE_ROLE: worker

  sourceAccess:
    mode: allowlist
    github:
      allowedRepos:
        - "github.com/org/project"
      allowedRefs:
        - "main"
        - "feature/*"
      permissions:
        - read
        - write

  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
        - ".github.com"
        - ".githubusercontent.com"
        - ".golang.org"
      allowedHosts:
        - "api.github.com"
        - "github.com"
        - "sum.golang.org"
        - "pkg.go.dev"
      allowedMethods:
        - GET
        - POST
        - PUT
        - PATCH

  identity:
    roleSessionDuration: "1h"
    allowedRegions:
      - us-east-1
    sessionPolicy: minimal

  sidecars:
    dnsProxy:
      enabled: true
      image: km-dns-proxy:latest
    httpProxy:
      enabled: true
      image: km-http-proxy:latest
    auditLog:
      enabled: true
      image: km-audit-log:latest

  artifacts:
    paths:
      - /workspace/output
    maxSizeMB: 100

  policy:
    allowShellEscape: false
    allowedCommands:
      - git
      - go
      - bash
      - python3
      - aws
    filesystemPolicy:
      writablePaths:
        - /workspace
        - /tmp
        - /home

  agent:
    maxConcurrentTasks: 4
    taskTimeout: "30m"
    allowedTools:
      - bash
      - read_file
      - write_file
      - list_files
```

**Reviewer** (`profiles/pipeline-reviewer.yaml`):

```yaml
apiVersion: klankermaker.ai/v1alpha1
kind: SandboxProfile
metadata:
  name: pipeline-reviewer
  labels:
    tier: development
    role: reviewer

spec:
  lifecycle:
    ttl: "1h"
    idleTimeout: "20m"
    teardownPolicy: destroy

  runtime:
    substrate: ecs
    spot: true
    instanceType: t3.small
    region: us-east-1

  execution:
    shell: /bin/bash
    workingDir: /workspace
    env:
      SANDBOX_MODE: pipeline-reviewer
      PIPELINE_ROLE: reviewer

  sourceAccess:
    mode: allowlist
    github:
      allowedRepos:
        - "github.com/org/project"
      allowedRefs:
        - "main"
        - "feature/*"
      permissions:
        - read

  network:
    egress:
      allowedDNSSuffixes:
        - ".amazonaws.com"
        - ".github.com"
        - ".githubusercontent.com"
      allowedHosts:
        - "api.github.com"
        - "github.com"
      allowedMethods:
        - GET

  identity:
    roleSessionDuration: "1h"
    allowedRegions:
      - us-east-1
    sessionPolicy: minimal

  sidecars:
    dnsProxy:
      enabled: true
      image: km-dns-proxy:latest
    httpProxy:
      enabled: true
      image: km-http-proxy:latest
    auditLog:
      enabled: true
      image: km-audit-log:latest

  policy:
    allowShellEscape: false
    allowedCommands:
      - git
      - go
      - bash
      - python3
      - aws
    filesystemPolicy:
      readOnlyPaths:
        - /etc
      writablePaths:
        - /workspace
        - /tmp

  agent:
    maxConcurrentTasks: 2
    taskTimeout: "20m"
    allowedTools:
      - bash
      - read_file
```

### Pipeline Flow

**Step 1: Create the sandboxes.**

```bash
km create profiles/pipeline-planner.yaml    # -> sb-plan0001
km create profiles/pipeline-worker.yaml     # -> sb-work0002
km create profiles/pipeline-reviewer.yaml   # -> sb-revw0003
```

Each sandbox receives its email address:

```
sb-plan0001@sandboxes.klankermaker.ai
sb-work0002@sandboxes.klankermaker.ai
sb-revw0003@sandboxes.klankermaker.ai
```

**Step 2: Planner assigns a task to the worker.**

The planner agent sends:

```
From:    sb-plan0001@sandboxes.klankermaker.ai
To:      sb-work0002@sandboxes.klankermaker.ai
Subject: km-agent:task-assign:corr-7f3a9b01

{
  "schema_version": "1.0",
  "action": "task-assign",
  "correlation_id": "corr-7f3a9b01",
  "sender": {
    "sandbox_id": "sb-plan0001",
    "email": "sb-plan0001@sandboxes.klankermaker.ai",
    "role": "planner"
  },
  "payload": {
    "task": "Implement JWT authentication middleware",
    "repo": "github.com/org/project",
    "branch": "feature/auth",
    "context": {
      "language": "go",
      "test_command": "go test ./pkg/auth/... -v",
      "acceptance_criteria": [
        "JWT token generation with HS256 signing",
        "Token validation middleware for HTTP handlers",
        "Token expiration and refresh logic",
        "Unit tests with >80% coverage"
      ]
    }
  },
  "reply_to": "sb-plan0001@sandboxes.klankermaker.ai",
  "timeout": "30m"
}
```

**Step 3: Worker implements the feature.**

The worker agent in sb-work0002 polls its S3 inbox, receives the task assignment, and:

1. Clones the repository from the allowed GitHub source.
2. Creates the feature branch.
3. Implements the JWT authentication middleware.
4. Runs `go test ./pkg/auth/... -v`.
5. Sends the results back to the planner and to the reviewer.

Worker sends to the reviewer:

```
From:    sb-work0002@sandboxes.klankermaker.ai
To:      sb-revw0003@sandboxes.klankermaker.ai
Subject: km-agent:review-request:corr-7f3a9b01

{
  "schema_version": "1.0",
  "action": "review-request",
  "correlation_id": "corr-7f3a9b01",
  "sender": {
    "sandbox_id": "sb-work0002",
    "email": "sb-work0002@sandboxes.klankermaker.ai",
    "role": "worker"
  },
  "payload": {
    "repo": "github.com/org/project",
    "branch": "feature/auth",
    "commit": "a3c8f2e",
    "files_changed": [
      "pkg/auth/jwt.go",
      "pkg/auth/middleware.go",
      "pkg/auth/jwt_test.go",
      "pkg/auth/middleware_test.go"
    ],
    "test_results": {
      "passed": 12,
      "failed": 0,
      "coverage": "87.3%",
      "output_log": "s3://km-sandbox-artifacts-ea554771/artifacts/sb-work0002/test-output.log"
    }
  },
  "reply_to": "sb-work0002@sandboxes.klankermaker.ai",
  "timeout": "20m"
}
```

Worker also notifies the planner:

```
From:    sb-work0002@sandboxes.klankermaker.ai
To:      sb-plan0001@sandboxes.klankermaker.ai
Subject: km-agent:task-result:corr-7f3a9b01

{
  "schema_version": "1.0",
  "action": "task-result",
  "correlation_id": "corr-7f3a9b01",
  "sender": {
    "sandbox_id": "sb-work0002",
    "email": "sb-work0002@sandboxes.klankermaker.ai",
    "role": "worker"
  },
  "payload": {
    "status": "complete",
    "branch": "feature/auth",
    "commit": "a3c8f2e",
    "test_results": {
      "passed": 12,
      "failed": 0,
      "coverage": "87.3%"
    },
    "review_requested_from": "sb-revw0003@sandboxes.klankermaker.ai"
  },
  "reply_to": "sb-work0002@sandboxes.klankermaker.ai"
}
```

**Step 4: Reviewer evaluates the code.**

The reviewer agent in sb-revw0003 polls its inbox, receives the review request, and:

1. Clones the repository (read-only access).
2. Checks out the feature branch at the specified commit.
3. Reviews the code against the acceptance criteria.
4. Sends a review response.

Reviewer sends approval to the planner:

```
From:    sb-revw0003@sandboxes.klankermaker.ai
To:      sb-plan0001@sandboxes.klankermaker.ai
Subject: km-agent:review-response:corr-7f3a9b01

{
  "schema_version": "1.0",
  "action": "review-response",
  "correlation_id": "corr-7f3a9b01",
  "sender": {
    "sandbox_id": "sb-revw0003",
    "email": "sb-revw0003@sandboxes.klankermaker.ai",
    "role": "reviewer"
  },
  "payload": {
    "verdict": "approved",
    "branch": "feature/auth",
    "commit": "a3c8f2e",
    "comments": [
      {
        "file": "pkg/auth/jwt.go",
        "line": 42,
        "severity": "info",
        "text": "Consider adding a configurable token expiry duration instead of hardcoding 24h."
      }
    ],
    "summary": "Implementation meets all acceptance criteria. Tests pass with 87.3% coverage. One minor suggestion for configurability."
  },
  "reply_to": "sb-revw0003@sandboxes.klankermaker.ai"
}
```

**Step 5: Planner receives approval and completes the pipeline.**

The planner polls its inbox, sees the `task-result` from the worker and the `review-response` from the reviewer. With an approved review, the pipeline is complete. The planner can then trigger cleanup:

```bash
km destroy sb-work0002
km destroy sb-revw0003
km destroy sb-plan0001
```

### Pipeline Sequence Diagram

```
sb-plan0001              sb-work0002              sb-revw0003
(planner)                (worker)                 (reviewer)
    |                        |                        |
    |-- task-assign -------->|                        |
    |                        |                        |
    |                   [clone repo]                  |
    |                   [implement]                   |
    |                   [run tests]                   |
    |                        |                        |
    |<-- task-result --------|                        |
    |                        |-- review-request ----->|
    |                        |                        |
    |                        |                   [clone repo]
    |                        |                   [review code]
    |                        |                        |
    |<-- review-response ----|------------------------|
    |                        |                        |
    |   [pipeline complete]  |                        |
    |                        |                        |
```

## Security Considerations

### Email as the Only Cross-Sandbox Channel

Email is deliberately the only mechanism through which sandboxes can communicate. This is enforced at multiple layers:

- **Network isolation.** Sandboxes run in separate VPCs (or ECS tasks with no shared networking). The DNS proxy and HTTP proxy do not route traffic between sandboxes. There is no inter-sandbox network path.
- **IAM isolation.** Each sandbox has its own IAM role. No cross-account or cross-role trust policies exist between sandboxes. A sandbox cannot assume another sandbox's role.
- **SES sender validation.** The IAM policy for each sandbox restricts `ses:SendEmail` and `ses:SendRawEmail` to the sandbox's own `From` address using a `ses:FromAddress` condition. A sandbox cannot send email pretending to be another sandbox.

### Audit Trail

All inbound email is stored as S3 objects under the `mail/` prefix. This provides:

- A complete record of every message received by every sandbox.
- Object-level versioning (if enabled on the bucket).
- CloudTrail logging of all S3 API calls (ListObjectsV2, GetObject, PutObject).
- SES sending logs via CloudWatch for outbound email.

### Threat Mitigations

| Threat | Mitigation |
|--------|------------|
| Sandbox A impersonates Sandbox B | `ses:FromAddress` IAM condition prevents sending from another address |
| Sandbox reads another sandbox's inbox | S3 IAM scopes `ListObjectsV2`/`GetObject` to `mail/{own-sandbox-id}/*` |
| Email content exfiltration | Network egress policy blocks all traffic except `.amazonaws.com`; email goes through SES only |
| Replay attacks | Correlation IDs and timestamps in structured messages; agents can track seen message IDs |
| Denial of service via email flooding | SES sending quotas apply per-account; operator monitors via lifecycle notifications |

## S3 Replication

The S3 cross-region replication module at `infra/modules/s3-replication/v1.0.0/` replicates sandbox artifacts to a secondary region for durability. The replication is configured with a prefix filter:

- `artifacts/` prefix: **replicated.** Build outputs, test results, and uploaded artifacts are copied to the replica bucket in the destination region.
- `mail/` prefix: **not replicated.** Inbound email is ephemeral inbox data. It does not need cross-region durability.

This filtering is configured in the replication rule:

```hcl
rule {
  id     = "replicate-artifacts"
  status = "Enabled"

  filter {
    prefix = "artifacts/"
  }

  destination {
    bucket        = aws_s3_bucket.replica.arn
    storage_class = "STANDARD"
  }
}
```

The rationale:

- **Artifacts are valuable.** They represent completed work -- build outputs, test results, logs. Losing them to a regional failure would mean re-running the sandbox.
- **Mail is transient.** Inbox messages are consumed and acted upon. Once an agent reads and processes a message, the message has served its purpose. Replicating it adds cost and complexity with no operational benefit.

To enable replication, set `replicationRegion` in the sandbox profile's artifacts configuration. The replication module creates the replica bucket, enables versioning on both source and destination, and configures the IAM role for S3 to perform cross-region replication.
