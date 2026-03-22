# Klanker Maker Security Model

This document describes the security architecture of Klanker Maker, a policy-driven sandbox platform for running AI agent workloads in AWS. It is intended for operators evaluating the platform's trust boundaries, security engineers reviewing the design, and contributors extending the enforcement layers.

---

## Table of Contents

1. [Philosophy](#1-philosophy)
2. [Account Isolation](#2-account-isolation)
3. [VPC Isolation](#3-vpc-isolation)
4. [SSM Session Manager](#4-ssm-session-manager)
5. [Network Enforcement](#5-network-enforcement)
6. [IMDSv2](#6-imdsv2)
7. [IAM Scoping](#7-iam-scoping)
8. [Secret Management](#8-secret-management)
9. [Source Access Control](#9-source-access-control)
10. [Filesystem Policy](#10-filesystem-policy)
11. [Audit Trail](#11-audit-trail)
12. [Budget as Security](#12-budget-as-security)
13. [Spot Interruption Security](#13-spot-interruption-security)
14. [Threat Model](#14-threat-model)

---

## 1. Philosophy

Klanker Maker's security model is built on four principles:

**Explicit allowlists everywhere.** Every external interaction -- DNS resolution, HTTP egress, repository access, IAM permissions, secret paths, shell commands, filesystem writes -- requires an explicit entry in the SandboxProfile YAML. Nothing is permitted by default. The `sealed` profile demonstrates the extreme case: empty allowlists for DNS suffixes, HTTP hosts, and methods, producing a sandbox with zero network egress.

**Deny by default.** Security groups start with no egress rules. The IAM role has no inline permissions beyond SSM agent requirements. The DNS proxy returns NXDOMAIN for any domain not matching an allowed suffix. The HTTP proxy returns 403 for any host not in the allowed list. The filesystem is read-only except for explicitly declared writable paths. If a profile section is omitted, the default is the most restrictive option.

**Defense in depth.** No single layer is trusted to hold. Network egress is enforced at three independent layers: Security Groups (L3/L4), DNS proxy (application-layer domain filtering), and HTTP proxy (application-layer host filtering). iptables DNAT ensures traffic cannot bypass the proxy sidecars. IMDSv2 prevents SSRF-based credential theft even if application code is compromised. Secret redaction in audit logs prevents credential leakage even if log storage is breached.

**Assume agent compromise.** The platform assumes the AI agent running inside the sandbox will attempt to escape its constraints. The agent has shell access, can execute arbitrary code within its allowed command set, and may try to exfiltrate data, escalate privileges, or pivot to other infrastructure. Every design decision starts from this assumption.

---

## 2. Account Isolation

Klanker Maker uses a 3-account model to contain blast radius:

| Account | Purpose | Contains |
|---------|---------|----------|
| **Management** | AWS Organizations root, billing, identity federation | IAM Identity Center, SCPs, billing alerts |
| **Terraform** | Infrastructure provisioning and state | Terragrunt runners, S3 state buckets, DynamoDB lock tables, KMS keys for state encryption |
| **Application** | Sandbox execution | EC2 instances, VPCs, Security Groups, SSM parameters, CloudWatch Logs, S3 artifact buckets |

**Why three accounts?** The critical insight is blast radius containment. If an agent escapes its sandbox -- breaks out of the proxy sidecars, escalates IAM privileges, or exploits a kernel vulnerability -- it lands in the Application account. From there it cannot:

- **Reach Terraform state.** State files containing infrastructure secrets, resource ARNs, and configuration live in a separate account with no cross-account role trust from Application.
- **Modify DNS or networking infrastructure.** Route53 zones, Organization-level SCPs, and account-level settings are in Management.
- **Provision new infrastructure.** The Terraform account's IAM roles are not assumable from Application account principals.

The Application account is treated as a hostile environment. Its IAM boundaries, VPC isolation, and network controls exist to slow and detect compromise -- not to make it impossible. The account boundary is the hard stop.

---

## 3. VPC Isolation

Each sandbox gets its own VPC. There is no VPC peering, no shared subnets, no Transit Gateway attachments between sandboxes.

**VPC configuration** (from `infra/modules/ec2spot/v1.0.0/main.tf`):

- CIDR: `10.0.0.0/16` per sandbox
- Subnets: one per availability zone, created with `cidrsubnet("10.0.0.0/16", 8, count.index)`
- DNS support: enabled (`enable_dns_support = true`, `enable_dns_hostnames = true`)
- Internet Gateway: one per VPC, for outbound connectivity through proxy sidecars

**Security Groups:**

Two security groups enforce L3/L4 network policy:

1. **`sandbox_mgmt`** -- Management plane access.
   - Zero ingress rules. No SSH. No RDP. No inbound ports of any kind.
   - Egress rules are compiled from the SandboxProfile by the profile compiler. The baseline emits TCP/443 (HTTPS for SSM agent and API traffic) and UDP/53 (DNS resolution). No default "allow all" egress.
   - SSM Session Manager is the only path into the instance, gated by IAM policy.

2. **`sandbox_internal`** -- Intra-VPC sidecar communication.
   - Single ingress rule: `self = true` (all protocols, all ports, but only from members of the same security group).
   - This allows the DNS proxy (port 5353), HTTP proxy (port 3128), and audit log sidecar to communicate with the main workload on the same host.
   - No cross-VPC or cross-sandbox communication.

**Why per-sandbox VPCs?** A shared VPC with security group isolation leaves a lateral movement path: an agent that compromises one sandbox's instance could ARP-spoof, exploit VPC-level vulnerabilities, or reach other instances on the same subnet. Per-sandbox VPCs eliminate this class of attack entirely. The cost is one VPC per sandbox (AWS allows 5 VPCs per region by default, increasable to hundreds via service quota request).

---

## 4. SSM Session Manager

Klanker Maker uses AWS Systems Manager Session Manager as the sole access path into sandbox instances. SSH is never configured.

**Why no SSH:**

| Concern | SSH | SSM Session Manager |
|---------|-----|---------------------|
| Inbound ports | Requires port 22 open | Zero inbound ports required |
| Key management | SSH key pairs must be generated, distributed, rotated | No keys -- IAM-gated |
| Audit trail | Requires separate audit daemon | Full CloudTrail audit of every session start/stop |
| Bastion host | Needed for private subnets | Not needed -- SSM agent communicates outbound |
| Private subnet access | Requires bastion or VPN | Works via VPC endpoints (SSM, SSM Messages, EC2 Messages) |
| Credential exposure | Keys can be stolen from `~/.ssh` | No persistent credentials on the instance |

**How SSM agent communication works:**

1. The SSM agent is installed and started during EC2 user-data bootstrap (step 1 of the boot script).
2. The agent initiates an outbound HTTPS connection to the regional SSM service endpoint (`ssm.<region>.amazonaws.com`).
3. This connection is kept alive as a long-poll channel. The agent periodically checks for pending commands or session requests.
4. When an operator runs `aws ssm start-session --target <instance-id>`, the SSM service signals the agent over the existing outbound channel.
5. The agent opens a WebSocket connection to the SSM Messages endpoint (`ssmmessages.<region>.amazonaws.com`) for the interactive session.
6. All session I/O is relayed through this outbound WebSocket -- no inbound connection is ever made to the instance.

The EC2 instance's IAM role includes the `AmazonSSMManagedInstanceCore` managed policy, which grants the minimum permissions for the SSM agent to function: `ssm:UpdateInstanceInformation`, `ssmmessages:*`, `ec2messages:*`.

Security Group egress on TCP/443 to `0.0.0.0/0` is required for the SSM agent to reach the service endpoint. This is the baseline egress rule compiled by the profile compiler.

---

## 5. Network Enforcement

Network egress is enforced at three independent layers, each operating at a different level of the stack. An agent must bypass all three to exfiltrate data.

### Layer 1: Security Groups (L3/L4)

The `ec2spot` security group starts with zero egress rules. The profile compiler (`pkg/compiler/security.go`) emits rules based on the SandboxProfile:

- **TCP 443** (`0.0.0.0/0`) -- HTTPS egress for SSM agent and outbound API traffic
- **UDP 53** (`0.0.0.0/0`) -- DNS resolution

These are the only ports open at the infrastructure level. An agent cannot open a raw TCP connection on port 80, 8080, or any non-standard port -- the security group drops it before it reaches the network.

### Layer 2: DNS Proxy Sidecar (Application-Layer Domain Filtering)

The DNS proxy sidecar (`sidecars/dns-proxy/dnsproxy/proxy.go`) intercepts all DNS queries:

- Listens on port 5353 locally.
- `iptables -t nat` DNAT rules redirect all UDP/TCP port 53 traffic to 5353 (except traffic from the `km-sidecar` user, preventing redirect loops).
- For each query, checks the domain against `allowedDNSSuffixes` from the profile. Matching is case-insensitive, supports exact match and suffix match (e.g., `.amazonaws.com` allows `sts.us-east-1.amazonaws.com`).
- **Allowed:** forwards to upstream DNS at `169.254.169.253` (the VPC DNS resolver) and returns the response.
- **Denied:** returns `NXDOMAIN` immediately. The agent sees a non-existent domain.
- Every query (allowed and denied) is logged as a structured JSON event with `sandbox_id`, `domain`, and `allowed` fields.

### Layer 3: HTTP Proxy Sidecar (Application-Layer Host Filtering)

The HTTP proxy sidecar (`sidecars/http-proxy/httpproxy/proxy.go`) intercepts all HTTP/HTTPS traffic:

- Listens on port 3128 locally.
- `iptables -t nat` DNAT rules redirect TCP ports 80 and 443 to 3128 (except traffic from `km-sidecar` user).
- For HTTPS (CONNECT tunnels): checks the target host against `allowedHosts` from the profile. Allowed connections get `OkConnect`; blocked connections get `RejectConnect` (client sees connection refused).
- For plain HTTP: checks `req.Host` against the allowed list. Blocked requests receive an HTTP 403 with body "Blocked by km sandbox policy".
- W3C `traceparent` headers are injected on allowed CONNECT requests for distributed tracing.
- Every blocked request is logged with `sandbox_id`, `host`, and `event_type: http_blocked`.

### iptables DNAT Configuration

The user-data bootstrap script (`pkg/compiler/userdata.go`) configures iptables rules that make proxy bypass impossible from userspace:

```
# IMDS exemption (must be first -- prevents breaking IMDSv2 token requests)
iptables -t nat -I OUTPUT -d 169.254.169.254 -j RETURN

# DNS redirect (UDP and TCP port 53 -> 5353)
iptables -t nat -A OUTPUT -p udp --dport 53 ! -m owner --uid-owner km-sidecar -j REDIRECT --to-ports 5353
iptables -t nat -A OUTPUT -p tcp --dport 53 ! -m owner --uid-owner km-sidecar -j REDIRECT --to-ports 5353

# HTTP/HTTPS redirect (ports 80, 443 -> 3128)
iptables -t nat -A OUTPUT -p tcp --dport 80  ! -m owner --uid-owner km-sidecar -j REDIRECT --to-ports 3128
iptables -t nat -A OUTPUT -p tcp --dport 443 ! -m owner --uid-owner km-sidecar -j REDIRECT --to-ports 3128
```

The `km-sidecar` system user runs all sidecar processes. The `! -m owner --uid-owner km-sidecar` exemption prevents redirect loops: the proxy's own upstream connections to real DNS servers and HTTPS endpoints are not redirected back to itself.

The IMDS exemption (`-I OUTPUT -d 169.254.169.254 -j RETURN`) is inserted first (`-I` inserts at the top of the chain) to ensure IMDSv2 token requests on port 80 to the link-local metadata address are not caught by the HTTP redirect rule.

---

## 6. IMDSv2

All EC2 instances enforce IMDSv2 (Instance Metadata Service version 2) with `http_tokens = "required"`:

```hcl
metadata_options {
  http_tokens                 = "required"
  http_put_response_hop_limit = 1
  http_endpoint               = "enabled"
}
```

**Why this matters:** The Instance Metadata Service (IMDS) at `169.254.169.254` exposes the instance's IAM role credentials. IMDSv1 uses simple GET requests with no authentication -- any process or SSRF vulnerability that can reach the link-local address can steal credentials. This is one of the most common cloud exploitation techniques.

**How IMDSv2 works:**

1. A client must first send a `PUT` request to `http://169.254.169.254/latest/api/token` with an `X-aws-ec2-metadata-token-ttl-seconds` header specifying the token lifetime (up to 21600 seconds / 6 hours).
2. The IMDS responds with a session token.
3. All subsequent metadata `GET` requests must include the token in the `X-aws-ec2-metadata-token` header.
4. Requests without a valid token receive HTTP 401.

**Why `http_put_response_hop_limit = 1`:** This limits the token response to a single network hop, preventing containers or processes behind a NAT from using a stolen token PUT response. The token response packet's TTL is set to 1, so it cannot traverse a network boundary.

**How the bootstrap script uses IMDSv2:** The user-data script immediately fetches a token and verifies it works before proceeding. If the token fetch fails, the bootstrap aborts (`exit 1`). The token is then used for all subsequent metadata queries (region discovery, spot termination polling).

---

## 7. IAM Scoping

Each sandbox gets its own IAM role and instance profile. The role is scoped with multiple constraints:

**Per-sandbox role:** The IAM role name includes the sandbox ID and region label (`km-ec2spot-ssm-{sandbox_id}-{region_label}`), preventing cross-sandbox role confusion.

**Session duration:** Configurable via `identity.roleSessionDuration` in the profile. The `compileIAMPolicy` function in `pkg/compiler/security.go` parses the Go duration string and sets `max_session_duration` on the IAM role. Default is 3600 seconds (1 hour). Short sessions limit the window of exposure if credentials are compromised.

**Region lock:** When `identity.allowedRegions` is non-empty, an inline policy is attached that restricts all API calls to the specified regions using an `aws:RequestedRegion` condition:

```json
{
  "Effect": "Allow",
  "Action": "*",
  "Resource": "*",
  "Condition": {
    "StringEquals": {
      "aws:RequestedRegion": ["us-east-1"]
    }
  }
}
```

This prevents a compromised sandbox from spinning up resources in unmonitored regions (a common crypto-mining technique: launch GPU instances in `ap-southeast-1` from a compromised `us-east-1` role).

**Minimal permissions:** The only managed policy attached is `AmazonSSMManagedInstanceCore`. The sandbox has no permissions to create EC2 instances, modify IAM roles, access S3 buckets outside its artifact bucket, or call any AWS service beyond what SSM, KMS decrypt, and CloudWatch Logs require.

---

## 8. Secret Management

Secrets are stored in AWS SSM Parameter Store as `SecureString` parameters, encrypted with a per-sandbox KMS key.

**KMS key configuration** (from `infra/modules/secrets/v1.0.0/main.tf`):

- **Auto-rotation enabled:** `enable_key_rotation = true` -- AWS automatically rotates the backing key material annually.
- **30-day deletion window:** `deletion_window_in_days = 30` -- prevents accidental permanent key loss; allows recovery during the window.
- **Key policy with three principals:**
  1. Account root (`arn:aws:iam::{account_id}:root`) -- full `kms:*` for key administration.
  2. SSM service (`ssm.amazonaws.com`) -- `Encrypt`, `Decrypt`, `GenerateDataKey*`, `DescribeKey` for parameter encryption/decryption.
  3. ECS tasks service (`ecs-tasks.amazonaws.com`) -- `Decrypt` and `DescribeKey` only, with `aws:SourceAccount` condition to prevent confused deputy attacks.

**Secret injection at boot:** The user-data bootstrap script fetches each allowed secret path from SSM Parameter Store using `aws ssm get-parameter --with-decryption`, converts the path's basename to an uppercase environment variable name, and exports it into the shell environment. Only paths listed in `identity.allowedSecretPaths` in the profile are fetched. The profile compiler (`pkg/compiler/security.go`, `compileSecrets` function) builds this list at compile time.

**Secret redaction in audit logs:** The `RedactingDestination` in `sidecars/audit-log/auditlog.go` wraps the audit log destination and applies four redaction patterns before any event is persisted:

1. AWS access key IDs (`AKIA[A-Z0-9]{16}`)
2. Bearer tokens (`Bearer [A-Za-z0-9\-._~+/]+=*`)
3. Hex strings of 40+ characters (SSH keys, API tokens)
4. Literal secret values provided at construction (the actual SSM parameter values)

Redaction is applied recursively to all string values in the event `Detail` map. Structural fields (`SandboxID`, `EventType`, `Timestamp`, `Source`) are never modified.

---

## 9. Source Access Control

Repository access is controlled through the `sourceAccess` section of the SandboxProfile:

```yaml
sourceAccess:
  mode: allowlist
  github:
    allowedRepos:
      - "github.com/whereiskurt/*"
    allowedRefs:
      - "main"
      - "develop"
    permissions:
      - read
```

**Mode:** Always `allowlist`. There is no `denylist` or `open` mode.

**GitHub access controls:**

- `allowedRepos`: glob patterns specifying which repositories can be cloned or fetched. `github.com/*` allows any repo on GitHub; `github.com/whereiskurt/*` restricts to a specific organization.
- `allowedRefs`: branch/tag patterns the agent can check out. Supports wildcards (`feature/*`, `fix/*`).
- `permissions`: `read` (clone/fetch only), `write` (push allowed). The `hardened` profile allows only `git` in its command allowlist and has no GitHub configuration at all -- zero repository access.

**No default access:** If `sourceAccess.github` is nil (as in the `hardened` and `sealed` profiles), no GitHub token is injected and the agent has no repository access. The GitHub App token is stored in SSM Parameter Store at `/km/github/app-token` and is only fetched when the profile explicitly configures GitHub access.

**Enforcement:** The execution environment enforces these constraints. The GitHub token (if injected) is scoped to the allowed repositories. Ref restrictions are enforced at the Git operation level within the sandbox.

---

## 10. Filesystem Policy

The `filesystemPolicy` section of the SandboxProfile controls what paths are writable and which are protected:

```yaml
policy:
  filesystemPolicy:
    readOnlyPaths:
      - /etc
    writablePaths:
      - /workspace
      - /tmp
```

**Enforcement mechanism:** The user-data bootstrap script applies read-only bind mounts before sidecar startup (section 2.5 of the boot sequence):

```bash
mount --bind "/etc" "/etc"
mount -o remount,bind,ro "/etc"
```

This creates a bind mount of the path onto itself, then remounts it as read-only. The agent can read `/etc/passwd` but cannot modify `/etc/resolv.conf` or plant a crontab.

**Writable paths** are explicitly declared. In the `open-dev` profile: `/workspace`, `/tmp`, `/home`. In `restricted-dev`: `/workspace` and `/tmp` only. The `hardened` and `sealed` profiles omit `filesystemPolicy` entirely, meaning no explicit writable paths are declared beyond the defaults.

**Boot order matters:** Read-only mounts are applied in section 2.5 of the bootstrap, before sidecars start in section 5. This prevents a race condition where a sidecar or the agent could modify protected paths before enforcement takes effect.

---

## 11. Audit Trail

Every sandbox action is captured through multiple complementary logging systems:

**Command logging:** The audit log sidecar (`sidecars/audit-log/auditlog.go`) processes JSON-line events from stdin. Each event follows a canonical schema:

```json
{
  "timestamp":  "2026-03-21T12:00:00Z",
  "sandbox_id": "sb-a1b2c3d4",
  "event_type": "shell_command",
  "source":     "audit-log",
  "detail":     { ... }
}
```

Event types: `shell_command`, `dns_query`, `http_request`.

**Network logging:** The DNS proxy and HTTP proxy sidecars emit structured JSON log lines for every query and request, including:
- Domain name and allow/deny decision (DNS proxy)
- Host name and allow/deny decision (HTTP proxy)
- Sandbox ID for correlation

**Log destinations** are configurable per profile:

| Destination | Use Case |
|------------|----------|
| `cloudwatch` | Real-time monitoring, CloudWatch Insights queries, alerting |
| `s3` | Long-term archival (stub in current implementation, falls back to stdout) |
| `stdout` | Development and debugging |

The CloudWatch destination creates a per-sandbox log group (`/km/sandboxes/{sandbox_id}/`) with a dedicated `audit` log stream. Events are buffered (threshold: 25 events) and flushed in batches.

**Secret redaction before storage:** All audit events pass through the `RedactingDestination` before reaching any backend. Secrets are replaced with `[REDACTED]` in the stored logs. This means even if CloudWatch Logs or S3 storage is breached, credential exposure is mitigated.

**CloudTrail:** All AWS API calls made by sandbox IAM roles are logged to CloudTrail. This captures SSM session starts/stops, parameter store reads, S3 uploads, and any unauthorized API attempts. CloudTrail logging is account-level and cannot be disabled from within a sandbox.

---

## 12. Budget as Security

Budget limits serve a dual security purpose: they prevent resource exhaustion attacks and bound the financial impact of a compromised sandbox.

**Compute budget prevents crypto-mining:** A compromised agent with unrestricted EC2 access could launch GPU instances for cryptocurrency mining. Budget controls are the last line of defense after IAM region-locking and permission scoping. If an agent somehow creates resources, budget alerts trigger and IAM revocation follows.

**AI budget prevents API abuse:** Agents with access to AI API keys (e.g., through injected secrets) could make unlimited API calls. Budget limits on allowed hosts, combined with proxy-level request logging, provide visibility and enforcement.

**Dual-layer enforcement:**

1. **Proxy 403:** The HTTP proxy blocks requests to non-allowed hosts immediately. No external API call can be made to a host not in the allowlist. This is the fast path -- the request never leaves the sandbox.
2. **IAM revocation:** If budget alerts fire, the sandbox's IAM role can be revoked at the account level, cutting off all AWS API access regardless of what the agent is doing inside the instance.

**Lifecycle as budget enforcement:** The `lifecycle.ttl` field (e.g., `"24h"` for `open-dev`, `"1h"` for `sealed`) ensures sandboxes are automatically destroyed after their maximum lifetime. The `idleTimeout` catches abandoned sandboxes. The `teardownPolicy: destroy` ensures resources are fully cleaned up, not just stopped.

---

## 13. Spot Interruption Security

Spot instances can be reclaimed by AWS with a 2-minute warning. Klanker Maker handles this without data loss through two mechanisms:

### EC2 Substrate: IMDS Polling

For EC2-based sandboxes (the current v1 substrate), the user-data bootstrap script starts a background polling loop:

```bash
while true; do
  SPOT_ACTION=$(curl -sf \
    -H "X-aws-ec2-metadata-token: $IMDS_TOKEN" \
    "http://169.254.169.254/latest/meta-data/spot/termination-time" 2>/dev/null || echo "")
  if [ -n "$SPOT_ACTION" ]; then
    /opt/km/bin/km-upload-artifacts || true
    # Send SES notification to operator (best-effort)
    break
  fi
  sleep 5
done
```

When the termination notice appears in instance metadata, the handler:
1. Uploads configured artifact paths to S3 (respecting `maxSizeMB` limits)
2. Sends an SES email notification to the operator (best-effort)
3. Exits cleanly

The polling loop uses the IMDSv2 token obtained at boot, runs every 5 seconds, and completes well within the 2-minute warning window.

### ECS Substrate: EventBridge Rule

For ECS-based sandboxes, the `ecs-spot-handler` module (`infra/modules/ecs-spot-handler/v1.0.0/main.tf`) deploys:

- An EventBridge rule watching for `ECS Task State Change` events with `stopCode: SpotInterruption`
- A Lambda function that triggers artifact upload via `ecs:ExecuteCommand` inside the stopping container
- The Lambda has a 25-second timeout (Fargate gives ~30 seconds; 5-second margin)

**Security of the handler itself:** The spot handler Lambda's IAM role has minimal permissions: CloudWatch Logs (for its own execution logs), `ecs:ExecuteCommand` and `ecs:DescribeTasks` scoped to the specific ECS cluster ARN, and `ssmmessages:*` for the ECS Exec session channel.

---

## 14. Threat Model

### Scenario: Agent Sandbox Escape

**Threat:** An AI agent exploits a vulnerability in the runtime environment, kernel, or sidecar to gain arbitrary code execution outside the sandbox constraints.

**Mitigations (layered):**
1. The agent is already running as an unprivileged user with a constrained command allowlist.
2. Filesystem bind mounts prevent modification of system binaries and configuration.
3. Even with arbitrary code execution, Security Groups block all egress except DNS (UDP 53) and HTTPS (TCP 443).
4. Even with arbitrary network access on allowed ports, the DNS proxy returns NXDOMAIN for non-allowed domains and the HTTP proxy returns 403 for non-allowed hosts.
5. Even if the agent bypasses iptables DNAT (requires root), the Security Group still blocks non-443/53 traffic at the VPC level.
6. Even if the agent compromises the entire EC2 instance, it is in an isolated VPC with no peering to other sandboxes.
7. Even if the agent escalates to the instance IAM role, it is region-locked and has minimal permissions (SSM, KMS decrypt, CloudWatch Logs only).
8. Even if the agent reaches the AWS API, it is in the Application account -- Terraform state, DNS configuration, and billing are in separate accounts.

**Residual risk:** Kernel exploits or hypervisor escapes could theoretically breach the EC2 instance boundary. This is AWS's responsibility under the shared responsibility model.

### Scenario: Data Exfiltration via Allowed Hosts

**Threat:** An agent uses an allowed host (e.g., `api.github.com`) to exfiltrate sensitive data by encoding it in API request bodies, commit messages, or issue comments.

**Mitigations:**
1. `sourceAccess.permissions` controls whether push access is granted. Read-only profiles prevent write-based exfiltration through Git.
2. All HTTP requests through the proxy are logged with host, sandbox ID, and event type.
3. Audit logs capture shell commands, enabling post-incident forensic analysis.
4. The `allowedMethods` field can restrict to GET-only, preventing POST/PUT-based exfiltration for some hosts.

**Residual risk:** Any allowed host with write capability is a potential exfiltration channel. Operators must consider this when configuring allowlists. The `hardened` profile mitigates this by allowing only `.amazonaws.com` DNS suffixes and only `sts`/`ssm` hosts.

### Scenario: DNS Exfiltration Through Allowed Suffixes

**Threat:** An agent encodes data in DNS query labels for an allowed suffix (e.g., `<encoded-data>.s3.amazonaws.com`) and reads the responses, or uses a controlled subdomain under an allowed suffix to exfiltrate via DNS.

**Mitigations:**
1. DNS queries are suffix-matched, not prefix-matched. The query `exfil-data.amazonaws.com` resolves only if `.amazonaws.com` is in the allowlist -- which it typically is.
2. All DNS queries (allowed and denied) are logged by the DNS proxy sidecar, creating a forensic trail.
3. Anomaly detection on DNS query patterns (high volume, unusual label lengths, high entropy) can be implemented on the CloudWatch log data.

**Residual risk:** DNS exfiltration through allowed suffixes is a known limitation. The data rate is low (DNS labels are limited to 63 bytes per label, 253 bytes total), but it is non-zero. Operators running `sealed` profiles (empty DNS allowlist) eliminate this channel entirely. For other profiles, monitoring DNS query volume and label entropy is the recommended detection strategy.

### Scenario: SSRF-Based Credential Theft

**Threat:** An agent exploits an SSRF vulnerability in an application running inside the sandbox to reach the Instance Metadata Service and steal IAM credentials.

**Mitigations:**
1. IMDSv2 with `http_tokens = "required"` blocks all GET-only SSRF attempts. The attacker must be able to send a PUT request with a custom header (significantly harder for most SSRF vectors).
2. `http_put_response_hop_limit = 1` prevents token theft from behind a NAT or container boundary.
3. Even if credentials are stolen, they are scoped to the minimal sandbox role (SSM + KMS decrypt + CloudWatch Logs) with region lock.
4. Session duration limits the window of credential validity.

**Residual risk:** A full-control SSRF (where the attacker controls method, headers, and body) could still obtain IMDSv2 tokens. The IAM scoping ensures stolen credentials have minimal value.

### Scenario: Sidecar Process Compromise

**Threat:** An agent exploits a vulnerability in the DNS proxy or HTTP proxy sidecar to bypass filtering.

**Mitigations:**
1. Sidecars run as the `km-sidecar` system user (`useradd -r -s /usr/sbin/nologin km-sidecar`), a non-login account with no shell.
2. Sidecar binaries are downloaded from a controlled S3 bucket at boot time, not from user-writable paths.
3. Even if a sidecar is compromised, the Security Group still enforces L3/L4 filtering independently.
4. The iptables DNAT rules use UID-based exemptions (`--uid-owner km-sidecar`), so only the `km-sidecar` user's traffic bypasses redirection. An agent running as a different user cannot exploit this exemption.

**Residual risk:** If the agent gains root access, it can modify iptables rules directly. Root access is mitigated by running the agent as an unprivileged user and protecting system paths with read-only bind mounts.

### Scenario: Resource Exhaustion / Crypto-Mining

**Threat:** A compromised agent launches expensive compute resources (GPU instances) in unmonitored regions.

**Mitigations:**
1. IAM region lock restricts API calls to `allowedRegions` only. A sandbox locked to `us-east-1` cannot call `ec2:RunInstances` in `ap-southeast-1`.
2. The sandbox IAM role does not have `ec2:RunInstances` permission at all -- only SSM, KMS, and CloudWatch Logs.
3. Lifecycle TTL ensures sandboxes are destroyed after their maximum lifetime, preventing indefinite resource consumption.
4. CloudTrail logs all API calls, including unauthorized attempts, enabling rapid detection.

**Residual risk:** Within the allowed region and existing instance, the agent can consume CPU and memory up to the instance type's limits. This is bounded by the instance type selection in the profile and the lifecycle TTL.

### Remaining Attack Surface Summary

| Vector | Status | Mitigation |
|--------|--------|------------|
| Kernel exploit / hypervisor escape | AWS shared responsibility | Account isolation limits blast radius |
| DNS exfiltration through allowed suffixes | Known limitation | Low bandwidth; logged; anomaly detection recommended |
| Data exfiltration through allowed HTTP hosts | Known limitation | Audit logging; restrict `permissions` to read-only |
| iptables bypass (requires root) | Mitigated by unprivileged user + bind mounts | Security Groups still enforce at VPC level |
| Sidecar binary tampering | Mitigated by S3 source + boot-time download | Read-only `/opt/km/bin` recommended |
| Time-of-check/time-of-use on allowlists | Not applicable -- allowlists are compiled at boot | Sidecars load allowlist once at startup |
| CloudWatch log tampering | Mitigated by IAM scoping | Sandbox role has `logs:PutLogEvents` only, not `logs:DeleteLogGroup` |

---

## Built-in Profile Security Tiers

The four built-in profiles demonstrate the security model at different trust levels:

| Profile | DNS Suffixes | HTTP Hosts | Git Access | Commands | Filesystem | TTL |
|---------|-------------|------------|------------|----------|------------|-----|
| `sealed` | None | None | None | None | Default | 1h |
| `hardened` | `.amazonaws.com` only | `sts`, `ssm` only | None | `git` only | Default | 4h |
| `restricted-dev` | AWS + GitHub + package registries | 5 hosts | Read-only, one org | 6 commands | `/etc` read-only | 8h |
| `open-dev` | AWS + GitHub + npm + PyPI + Go + Docker | 6 hosts | Read/write, all repos | 8 commands | `/workspace`, `/tmp`, `/home` writable | 24h |

The `sealed` profile produces a sandbox with zero network egress, zero repository access, zero allowed commands, and a 1-hour TTL. It is the most restrictive possible configuration and serves as a baseline for verifying that the deny-by-default model works correctly.
