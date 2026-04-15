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
14. [Service Control Policies](#14-service-control-policies)
15. [GitHub App Token Security](#15-github-app-token-security)
16. [Sandbox Identity and Email Signing](#16-sandbox-identity-and-email-signing)
17. [Threat Model](#17-threat-model)

---

## 1. Philosophy

Klanker Maker's security model is built on four principles:

**Explicit allowlists everywhere.** Every external interaction -- DNS resolution, HTTP egress, repository access, IAM permissions, secret paths, agent tools -- requires an explicit entry in the SandboxProfile YAML. Nothing is permitted by default. The `sealed` profile demonstrates the extreme case: empty allowlists for DNS suffixes and HTTP hosts, producing a sandbox with zero network egress.

**Deny by default.** Security groups start with no egress rules. The IAM role has no inline permissions beyond SSM agent requirements and platform-required policies (see Section 7). The DNS proxy returns NXDOMAIN for any domain not matching an allowed suffix. The HTTP proxy returns 403 for any host not in the allowed list. If a profile section is omitted, the default is the most restrictive option.

**Defense in depth.** No single layer is trusted to hold. Network egress is enforced at up to four independent layers: Security Groups (L3/L4), eBPF cgroup programs (kernel-level connect/egress filtering), DNS proxy or resolver (application-layer domain filtering), and HTTP proxy (application-layer host filtering). In proxy mode, iptables DNAT ensures traffic cannot bypass the proxy sidecars. In eBPF mode, cgroup-attached BPF programs enforce allowlists in the kernel — even root users cannot bypass them. IMDSv2 prevents SSRF-based credential theft even if application code is compromised. Secret redaction in audit logs prevents credential leakage even if log storage is breached.

**Assume agent compromise.** The platform assumes the AI agent running inside the sandbox will attempt to escape its constraints. The agent has shell access, can execute arbitrary code within its allowed tool set (`agent.allowedTools`), and may try to exfiltrate data, escalate privileges, or pivot to other infrastructure. Every design decision starts from this assumption.

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

## 3. VPC and Network Isolation

Klanker Maker uses a **shared regional VPC** provisioned once per region by `km init`. All sandboxes in a region share this VPC and its subnets. Isolation between sandboxes is enforced at the **Security Group layer**, not the VPC layer.

**Why a shared VPC instead of per-sandbox VPCs?** AWS imposes a default limit of 5 VPCs per region (increasable, but not infinitely). A shared VPC avoids hitting VPC limits, reduces provisioning time (no VPC/IGW/subnet creation per sandbox), and simplifies networking. The tradeoff is that sandboxes share a network — but Security Groups, iptables DNAT through sidecars, and IAM scoping provide the actual enforcement boundaries.

**VPC configuration** (from `infra/modules/network/v1.0.0/main.tf`, provisioned by `km init`):

- CIDR: configurable (default `10.0.0.0/16`)
- Subnets: public and private subnets across multiple availability zones
- DNS support: enabled (`enable_dns_support = true`, `enable_dns_hostnames = true`)
- Internet Gateway: one per VPC, for outbound connectivity through proxy sidecars
- NAT Gateway: optional for private subnet egress

**Per-Sandbox Security Groups:**

Each sandbox gets its own Security Group (`km-ec2spot-{sandbox_id}`), compiled from its SandboxProfile by the profile compiler. The security group enforces L3/L4 network policy:

- **Zero ingress rules.** No SSH. No RDP. No inbound ports of any kind.
- **Egress rules** are compiled from the SandboxProfile by the profile compiler. The baseline emits TCP/443 (HTTPS for SSM agent and API traffic) and UDP/53 (DNS resolution). No default "allow all" egress.
- **Self-referencing rule** (`self = true`): all protocols, all ports, but only from members of the same security group. This allows the DNS proxy, HTTP proxy (port 3128/3129), and audit log sidecar to communicate with the main workload on the same host.
- SSM Session Manager is the only path into the instance, gated by IAM policy.
- No cross-sandbox communication — each sandbox's security group only allows traffic from its own members.

**Residual risk:** Sandboxes share a VPC, so a compromised instance could theoretically attempt ARP spoofing or subnet-level attacks against other sandbox instances. This is mitigated by: (1) Security Groups blocking all cross-sandbox traffic, (2) the short-lived nature of sandboxes (TTL-enforced), (3) per-sandbox IAM roles with no cross-sandbox permissions, and (4) full audit logging of all network activity. For deployments requiring stronger isolation, the ec2spot module supports an optional per-sandbox VPC mode (pass `vpc_id = ""`) at the cost of VPC quota consumption.

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

Network egress is enforced at up to four independent layers, each operating at a different level of the stack. An agent must bypass all active layers to exfiltrate data.

### Layer 1: Security Groups (L3/L4)

The `ec2spot` security group starts with zero egress rules. The profile compiler (`pkg/compiler/security.go`) emits rules based on the SandboxProfile:

- **TCP 443** (`0.0.0.0/0`) -- HTTPS egress for SSM agent and outbound API traffic
- **UDP 53** (`0.0.0.0/0`) -- DNS resolution

These are the only ports open at the infrastructure level. An agent cannot open a raw TCP connection on port 80, 8080, or any non-standard port -- the security group drops it before it reaches the network.

### Layer 1b: eBPF Cgroup Enforcement (Kernel-Level Filtering)

When `spec.network.enforcement` is `"ebpf"` or `"both"`, four BPF programs are attached to the sandbox's cgroup (`/sys/fs/cgroup/km.slice/km-{id}.scope`):

- **cgroup/connect4** — intercepts every TCP `connect()` from processes in the cgroup. Performs an LPM trie lookup on the destination IP. If the IP is not in the `allowed_cidrs` map, returns 0 (EPERM — connection refused). If the IP is in the `http_proxy_ips` map (indicating it needs L7 inspection), rewrites the destination to 127.0.0.1:3129 (transparent redirect to the MITM proxy on `const_https_proxy_port`). Emits a structured event to the ring buffer for every deny and redirect.

- **cgroup/sendmsg4** — intercepts every UDP `sendmsg()`. Redirects all DNS queries (port 53) to the local resolver at 127.0.0.1:5353 (`const_dns_proxy_port`), which enforces `allowedDNSSuffixes` and populates the BPF `allowed_cidrs` map with resolved IPs. Non-DNS UDP is passed through.

- **sockops** — tracks TCP connection establishment. Maps local source ports to socket cookies so the MITM proxy can recover the original destination IP/port after BPF rewrites it.

- **cgroup_skb/egress** — packet-level backstop. Parses the IPv4 header of every outbound packet and checks the destination against the `allowed_cidrs` LPM trie. Drops packets to non-allowlisted IPs. This catches traffic that might bypass the connect4 hook (e.g., raw sockets).

**Key security property:** BPF programs attached to a cgroup v2 scope apply to ALL processes in that scope, regardless of user privilege. Root users, setuid binaries, and processes that modify iptables are all subject to the same BPF enforcement. This fixes the root-bypass vulnerability present in proxy-only mode (where root can install packages via `yum` by bypassing DNAT rules).

**Dynamic allowlist:** The `allowed_cidrs` LPM trie starts with pre-seeded entries (VPC CIDR, IMDS, link-local) and grows dynamically as the DNS resolver resolves allowed domains. Entries expire based on DNS TTL — the resolver's TTL cache sweep goroutine removes expired IPs from the BPF map.

### Layer 2: DNS Proxy Sidecar (Application-Layer Domain Filtering)

The DNS proxy sidecar (`sidecars/dns-proxy/dnsproxy/proxy.go`) intercepts all DNS queries:

- Listens on port 53 by default (`DNS_PORT` env var, default `"53"`). In proxy-only mode, iptables DNAT rules redirect all UDP/TCP port 53 traffic to 5353 (except traffic from the `km-sidecar` user, preventing redirect loops). In eBPF mode, the BPF `sendmsg4` program redirects DNS to port 5353 (`const_dns_proxy_port`) instead of iptables.
- For each query, checks the domain against `allowedDNSSuffixes` from the profile. Matching is case-insensitive, supports exact match and suffix match (e.g., `.amazonaws.com` allows `sts.us-east-1.amazonaws.com`).
- **Allowed:** forwards to upstream DNS at `169.254.169.253` (the VPC DNS resolver) and returns the response.
- **Denied:** returns `NXDOMAIN` immediately. The agent sees a non-existent domain.
- Every query (allowed and denied) is logged as a structured JSON event with `sandbox_id`, `domain`, and `allowed` fields.

### Layer 3: HTTP Proxy Sidecar (Application-Layer Host Filtering)

The HTTP proxy sidecar (`sidecars/http-proxy/httpproxy/proxy.go`) intercepts all HTTP/HTTPS traffic:

- Listens on port 3128 locally (HTTP). In eBPF gatekeeper mode, port 443 traffic is redirected to port 3129 (`const_https_proxy_port`) instead of 3128.
- In proxy-only mode, `iptables -t nat` DNAT rules redirect TCP ports 80 and 443 to 3128 (except traffic from `km-sidecar` user). In eBPF gatekeeper mode, the BPF `connect4` program handles HTTPS redirect to 3129.
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

**Managed policy:** `AmazonSSMManagedInstanceCore` is attached for SSM agent operation. In addition, the ec2spot module attaches several inline policies scoped to platform requirements:

| Inline Policy | Permissions | Purpose |
|---------------|-------------|---------|
| `km-{id}-eventbridge` | `events:PutEvents` | Audit-log sidecar publishes SandboxIdle events to EventBridge |
| `km-{id}-bedrock` | `bedrock:InvokeModel`, `bedrock:InvokeModelWithResponseStream`, `bedrock:ListInferenceProfiles`, `bedrock:ListFoundationModels`, `aws-marketplace:ViewSubscriptions`, `aws-marketplace:Subscribe` | AI model invocation via Bedrock (conditional on `enable_bedrock`) |
| `km-{id}-budget-dynamo` | `dynamodb:GetItem`, `dynamodb:UpdateItem`, `dynamodb:Query` on `km-budgets` table; `dynamodb:GetItem` on `km-sandboxes` table | HTTP proxy AI spend metering and budget tracking |
| `km-{id}-github-token` | `kms:Decrypt` (conditioned on `kms:ViaService = ssm`); `ssm:GetParameter` on `/sandbox/{id}/*` | Read GitHub token and other per-sandbox secrets from SSM |

The sandbox has no permissions to create EC2 instances, modify IAM roles, or access S3 buckets outside its artifact bucket.

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
```

**Mode:** Always `allowlist`. There is no `denylist` or `open` mode.

**GitHub access controls:**

- `allowedRepos`: glob patterns specifying which repositories can be cloned or fetched. `github.com/*` allows any repo on GitHub; `github.com/whereiskurt/*` restricts to a specific organization.
- `allowedRefs`: branch/tag patterns restricting which refs the agent can push to. Supports bash glob wildcards (`feature/*`, `fix/*`). See "AllowedRefs Enforcement" below for implementation details.
The `hardened` profile has `agent.allowedTools: [read_file]` and no GitHub configuration at all -- zero repository access.

**Deny-by-default contract:** Both a nil `sourceAccess.github` AND an explicitly empty `allowedRepos: []` result in zero GitHub token infrastructure being provisioned. The compiler gates all token infrastructure (GitHub token Lambda/EventBridge, per-sandbox SSM parameter, `github_token_inputs` HCL block, and the `GIT_ASKPASS` credential helper in EC2 user-data) behind the condition `GitHub != nil && len(AllowedRepos) > 0`. No token means no access -- this is the primary access control layer.

**Network-level enforcement (Phase 28):** In addition to token scoping, the HTTP proxy enforces repo-level access at the network layer via MITM interception. When `sourceAccess.github.allowedRepos` is configured, the proxy:
1. Implicitly allows GitHub hosts (github.com, api.github.com, raw.githubusercontent.com, codeload.githubusercontent.com) — profiles do **not** need these in `network.egress.allowedHosts`.
2. Intercepts HTTPS connections to GitHub hosts via MITM (using the platform CA already trusted by the sandbox).
3. Extracts `owner/repo` from the URL path and checks it against the `allowedRepos` list.
4. Blocks requests to repos not in the allowlist with a 403 JSON response.
5. Passes through non-repo GitHub URLs (e.g. `/rate_limit`, `/login`) unconditionally.

This provides defense-in-depth: even if a sandbox has valid credentials for a repo, the proxy blocks network access unless that repo is in the allowlist. It also prevents access to public repos outside the allowlist, which token scoping alone cannot prevent.

**No default access:** If `sourceAccess.github` is nil (as in the `hardened` and `sealed` profiles), no GitHub token is injected, no GitHub MITM filter is enabled, and GitHub hosts are not implicitly allowed. The agent has no repository access. The GitHub App installation token is stored in SSM Parameter Store at a per-sandbox path (`/sandbox/{sandbox_id}/github-token`) and is fetched lazily at git-operation time via the `GIT_ASKPASS` credential helper, not injected at boot time.

### AllowedRefs Enforcement

`allowedRefs` is enforced on EC2 sandboxes via a git `pre-push` hook installed during the EC2 user-data bootstrap (section 4b of the boot sequence). The compiler:

1. Sets `export KM_ALLOWED_REFS="main:feature/*"` (colon-separated pattern list)
2. Writes the hook script to `/opt/km/hooks/pre-push`
3. Runs `git config --system core.hooksPath /opt/km/hooks` so all git operations on the instance use the hook directory

The hook reads `KM_ALLOWED_REFS` and applies bash glob matching (`[[ "$branch" == $pattern ]]`) against the target ref of each push. Wildcards like `feature/*` match any branch beginning with `feature/`. A denied push receives an error message:

```
[km] Push to 'unauthorized-branch' denied -- not in allowedRefs: main:feature/*
```

**AllowedRefs limitations:**

- **EC2 only.** ECS sandboxes do not receive user-data bootstrap and therefore do not get the pre-push hook. `allowedRefs` has no enforcement effect in ECS sandboxes in this release.
- **Bypassable with `--no-verify`.** The hook can be bypassed by running `git push --no-verify` if the sandbox policy allows unrestricted shell access. AllowedRefs is defense-in-depth, not the primary control.
- **Push enforcement only.** The hook runs on `git push` only. It does not prevent checking out or working with local branches that do not match the allowlist.
- **Primary enforcement layer remains token scoping.** The GitHub App installation token is scoped by repository (`allowedRepos`). Ref restrictions are a secondary control.

### ECS GitHub Credential Gap (v1 Limitation)

ECS sandboxes deploy the GitHub token infrastructure (Lambda function and EventBridge scheduled rule for token refresh, SSM parameter for token storage) but do NOT inject a credential helper into the running container. The `GIT_ASKPASS` mechanism used by EC2 (which reads from SSM at git-operation time) has no equivalent in ECS.

Concretely: `git clone https://github.com/...` inside an ECS sandbox task will fail with "Authentication failed" unless the operator manually injects the token via a container environment variable or init container. The `github_token_inputs` HCL block is emitted in the ECS `service.hcl` for the Lambda/EventBridge infrastructure, but the ECS task definition itself has no `GIT_ASKPASS` or `GITHUB_TOKEN` equivalent.

This is a known v1 limitation. Resolving it requires either injecting the token into ECS container environment at task-launch time (via ECS secrets from SSM) or running a credential helper as a sidecar container.

---

## 10. Filesystem Policy

Filesystem enforcement is implemented in the EC2 user-data bootstrap script, not in the SandboxProfile schema. There is no `filesystemPolicy` field in the profile type definitions.

**Enforcement mechanism:** The user-data bootstrap script applies read-only bind mounts before sidecar startup (section 2.5 of the boot sequence):

```bash
mount --bind "/etc" "/etc"
mount -o remount,bind,ro "/etc"
```

This creates a bind mount of the path onto itself, then remounts it as read-only. The agent can read `/etc/passwd` but cannot modify `/etc/resolv.conf` or plant a crontab.

**Boot order matters:** Read-only mounts are applied in section 2.5 of the bootstrap, before sidecars start in section 5. This prevents a race condition where a sidecar or the agent could modify protected paths before enforcement takes effect.

**Privileged mode:** When `spec.execution.privileged` is true, the sandbox user is granted wheel group membership and passwordless sudo access. This is opt-in and not enabled in the `hardened` or `sealed` profiles.

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

**eBPF audit events (enforcement mode):** When eBPF enforcement is enabled, the ring buffer consumer emits structured events for every deny and redirect:
- Source/destination IP and port
- Process name and PID
- Action (deny/allow/redirect) and layer (connect4/sendmsg4/egress/sockops)
- Sandbox ID for correlation

**eBPF TLS observability (uprobe sidecar):** The SSL uprobe observer captures TLS plaintext without MITM certificates. For every TLS connection, it logs:
- HTTP method, URL path, and Host header (HTTP/1.1 and HTTP/2 headers)
- Source process name and PID
- TLS library (OpenSSL, Go crypto/tls, BoringSSL)
- This provides an independent audit trail alongside proxy logs — useful for compliance and forensic analysis

**Log destinations** are configurable per profile:

| Destination | Use Case |
|------------|----------|
| `cloudwatch` | Real-time monitoring, CloudWatch Insights queries, alerting |
| `s3` | Long-term archival |
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

**Lifecycle as budget enforcement:** The `lifecycle.ttl` field (e.g., `"4h"` for `goose`, `"1h"` for `sealed`) ensures sandboxes are automatically destroyed after their maximum lifetime. The `idleTimeout` catches abandoned sandboxes. The `teardownPolicy: destroy` ensures resources are fully cleaned up, not just stopped.

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

## 14. Service Control Policies

Service Control Policies (SCPs) are AWS Organizations policies that apply at the account level, independent of IAM role policies. SCPs provide a backstop that prevents sandbox role EC2/network/IAM breakout even if a sandbox's IAM role policy is misconfigured or bypassed.

**Why SCPs matter:** IAM role policies are enforced by the IAM service. If an agent somehow obtains a more permissive role (e.g., via cross-role assumption or a confused deputy attack), IAM alone cannot stop it. SCPs are evaluated before IAM -- they are organization-level denies that no role within the target account can override. Even the account root cannot override an SCP.

**Deployment:** The `infra/modules/scp/v1.0.0` module deploys a single SCP named `km-sandbox-containment` and attaches it to the Application account via `aws_organizations_policy_attachment`. The Management account is exempt from SCPs by AWS design.

### Deny Statements

The SCP has six deny statements across five `statement` blocks (DenySSMPivot and DenyOrgDiscovery are separate statements but the first four concerns are consolidated into three blocks). Most use `ArnNotLike` on `aws:PrincipalARN` to carve out trusted platform roles (note: `NotPrincipal` is not supported in SCPs -- `ArnNotLike` on the condition variable achieves the same effect).

#### 1. DenyInfraAndStorage (consolidated)

A single consolidated statement denying Security Group mutation (create, delete, authorize/revoke ingress and egress rules, modify rules), network escape (creation of VPCs, subnets, route tables, internet gateways, NAT gateways, VPC peering connections, Transit Gateway attachments), and storage exfiltration (EBS snapshot creation, snapshot copying, AMI creation, AMI copying, image export) for all roles not in `trusted_arns_base`. This consolidation from three separate statements into one fits within the 5,120-byte SCP limit.

#### 2. DenyInstanceMutation

Denies `ec2:RunInstances`, `ec2:ModifyInstanceAttribute`, and `ec2:ModifyInstanceMetadataOptions`. Trusted roles: `trusted_arns_instance` = base roles + `km-ecs-spot-handler` (which launches Spot instances as part of normal platform operation).

**Important:** `km-ecs-task-*` is intentionally NOT carved out. The ECS task role IS the sandbox workload. If the task role could run new instances, it would escape its compute budget and IAM scope. The SCP enforces this at the organization level regardless of the task role's IAM policy.

#### 3. DenyIAMEscalation

Denies `iam:CreateRole`, `iam:AttachRolePolicy`, `iam:DetachRolePolicy`, `iam:PassRole`, and `iam:AssumeRole`. Trusted roles: `trusted_arns_iam` = base roles + `km-budget-enforcer-*`.

The budget-enforcer Lambda carve-out is precise: it needs `AttachRolePolicy`/`DetachRolePolicy` to revoke Bedrock IAM permissions on budget breach, but it does NOT need `CreateRole` or `PassRole`. The SCP allows the enforcer to do exactly what it needs without granting broader IAM escalation.

#### 4. DenySSMPivot

Denies `ssm:SendCommand` and `ssm:StartSession`. Trusted roles: `trusted_arns_ssm` = only `km-ec2spot-ssm-*` instance roles, `km-github-token-refresher-*`, and operator SSO roles.

This prevents a compromised sandbox from using SSM to pivot to other instances in the account. The GitHub token refresher is listed here (not in base/instance/iam carve-outs) because it only needs SSM `GetParameter`/`PutParameter`, not instance session access.

#### 5. DenyOrgDiscovery

Denies `organizations:List*` and `organizations:Describe*` (wildcard actions covering all list and describe operations). This statement has **no condition** -- it applies to all roles in the Application account without exception. Application account roles have no legitimate reason to enumerate org structure. The management account is exempt by AWS design.

#### 6. DenyOutsideAllowedRegions

Denies all actions outside the configured `allowed_regions` using `StringNotEquals` on `aws:RequestedRegion`. Uses `not_actions` (NotAction) so global AWS services (IAM, STS, Organizations, Route53, billing, CloudFront, health, pricing, Bedrock) work regardless of region. Trusted roles (`trusted_role_arns`) are exempt via an `ArnNotLike` condition so operators can perform cross-region operations (e.g., S3 replication to a secondary region).

### Carve-Out Summary

| Statement | Trusted Roles (carve-out) |
|-----------|--------------------------|
| DenyInfraAndStorage | `trusted_arns_base` (operator SSO + provisioner roles) |
| DenyInstanceMutation | `trusted_arns_instance` = base + `km-ecs-spot-handler` |
| DenyIAMEscalation | `trusted_arns_iam` = base + `km-budget-enforcer-*` |
| DenySSMPivot | `trusted_arns_ssm` = `km-ec2spot-ssm-*` + `km-github-token-refresher-*` + SSO roles |
| DenyOrgDiscovery | None (applies to all) |
| DenyOutsideAllowedRegions | `trusted_role_arns` (operators can operate cross-region) |

---

## 15. GitHub App Token Security

GitHub repository access uses short-lived GitHub App installation tokens rather than personal access tokens (PATs) or SSH keys. This eliminates long-lived credentials in the sandbox environment.

### Token Lifecycle

1. **App installation**: The operator installs the GitHub App on their organization and registers the App's private key and installation ID via `km configure github`. The App's RSA private key is stored in SSM Parameter Store at `/km/config/github/private-key` as a KMS-encrypted SecureString.

2. **Token generation**: At sandbox creation, and then every 45 minutes, the `km-github-token-refresher-{sandbox-id}` Lambda generates a short-lived installation token:
   - Reads the App's RSA private key from SSM.
   - Mints a 10-minute GitHub App JWT (RS256, signed with the RSA key).
   - Exchanges the JWT for an installation token via `POST /app/installations/{id}/access_tokens`, scoped to the repositories listed in `sourceAccess.github.allowedRepos`.
   - Writes the token to SSM at `/sandbox/{sandbox-id}/github-token` as a KMS-encrypted SecureString.

3. **Token refresh**: The Lambda is triggered by EventBridge Scheduler every 45 minutes. GitHub installation tokens expire after 1 hour; the 45-minute refresh interval ensures the token never expires during normal sandbox operation.

4. **Token consumption**: Inside the sandbox, the `GIT_ASKPASS` helper script reads the token from SSM at git operation time (not at boot). The token is never placed in environment variables or written to disk.

### Token Scope

The installation token is scoped at request time to exactly the repositories listed in the profile's `sourceAccess.github.allowedRepos`. The token has no access to other repositories in the organization.

### KMS Encryption

Each GitHub token sandbox gets a dedicated KMS key (`alias/km-github-token-{sandbox-id}`) with a three-principal policy:

| Principal | Permissions |
|-----------|------------|
| Account root | `kms:*` (administration) |
| Lambda role | `kms:Encrypt`, `kms:Decrypt`, `kms:GenerateDataKey` |
| Sandbox IAM role | `kms:Decrypt` (read token from SSM) |

The sandbox role can decrypt the token (to use it via GIT_ASKPASS) but cannot encrypt or manage the KMS key.

### Security Properties

| Property | SSH Keys or PATs | GitHub App Tokens |
|----------|-----------------|-------------------|
| Credential lifetime | Permanent until revoked | 1 hour maximum |
| Scope | All repos accessible to key | Only repos in profile allowedRepos |
| Storage | Often in `~/.ssh` or env vars | KMS-encrypted SSM, never in env |
| Compromise impact | Full org access permanently | Single sandbox, expires in ≤1 hour |
| Rotation | Manual | Automatic every 45 minutes |

---

## 16. Sandbox Identity and Email Signing

Each sandbox has a cryptographic identity -- an Ed25519 key pair generated at creation time. This identity supports three capabilities: signing outbound emails, verifying inbound emails, and optional end-to-end encryption between sandboxes.

### Key Generation

At `km create`, two key pairs are generated:

1. **Ed25519 signing key** (`GenerateSandboxIdentity`): A 64-byte Ed25519 private key (seed + public key concatenated). Used for signing email bodies.
2. **X25519 encryption key** (`GenerateEncryptionKey`): A 32-byte NaCl box key pair. Used for end-to-end encryption. This is separate from the Ed25519 key by design -- the signing key proves identity, the encryption key provides confidentiality.

Both private keys are stored in SSM Parameter Store as KMS-encrypted SecureStrings:

- `/sandbox/{sandbox-id}/signing-key` — Ed25519 private key (base64)
- `/sandbox/{sandbox-id}/encryption-key` — X25519 private key (base64)

### DynamoDB Identity Record

The public keys are published to the `km-identities` DynamoDB table. Each sandbox has one row keyed by `sandbox_id` (the sole hash key -- no sort key). The row contains:

| Attribute | Description |
|-----------|-------------|
| `sandbox_id` | Hash key, string |
| `public_key` | Base64-encoded Ed25519 public key (32 bytes) |
| `email_address` | `{sandbox-id}@{domain}` |
| `encryption_public_key` | Base64-encoded X25519 public key (32 bytes) |
| `signing_policy` | `required`, `optional`, or `off` |
| `verify_inbound_policy` | `required`, `optional`, or `off` |
| `encryption_policy` | `required`, `optional`, or `off` |
| `alias` | Human-friendly dot-notation name (from Phase 17, optional) |
| `allowed_senders` | DynamoDB StringSet of allow-list patterns (from Phase 17, optional) |

Empty string means "not specified" -- the attribute is omitted from the row to preserve backward compatibility with sandboxes created before identity was added.

### Email Signing

When a sandbox sends email (`SendSignedEmail`), the flow is:

1. Read the Ed25519 private key from SSM (KMS-decrypted).
2. Apply the encryption policy gate (see below).
3. Sign the email body (not headers) with Ed25519: `ed25519.Sign(priv, []byte(body))`.
4. Build a raw MIME message with custom headers:
   - `X-KM-Sender-ID: {sandbox-id}` — identifies the sender
   - `X-KM-Signature: {base64-ed25519-signature}` — Ed25519 signature over the body
   - `X-KM-Encrypted: true` — present only if body is encrypted
5. Send via SES `Content.Raw` (not `Content.Simple` -- the Simple message type strips custom headers).

Headers are not signed. Only the body is signed. This simplifies verification: the body is the stable content, while headers may be modified in transit by SES or email relays.

### Email Verification

When a sandbox receives an email with an `X-KM-Signature` header:

1. Extract `X-KM-Sender-ID` to identify the sender sandbox.
2. Fetch the sender's public key from DynamoDB (`FetchPublicKey` on `km-identities`).
3. Call `VerifyEmailSignature(pubKeyB64, body, sigB64)`: decodes the public key and signature from base64, calls `ed25519.Verify`.
4. If verification fails, `SignatureOK = false` is set on the message record (not treated as a hard error -- the message is still delivered but flagged).

### Optional Encryption

When `spec.email.encryption` is `required` or `optional`:

1. The sender fetches the recipient's `encryption_public_key` from DynamoDB.
2. Encrypts the body using `box.SealAnonymous(nil, plaintext, recipientPubKey, rand.Reader)` (NaCl box anonymous seal). The sender's identity is NOT embedded in the ciphertext -- it is carried in the `X-KM-Sender-ID` header instead.
3. The base64-encoded ciphertext replaces the plaintext body. The `X-KM-Encrypted: true` header signals the recipient.
4. The recipient decrypts using `box.OpenAnonymous(nil, ciphertext, pubKey, privKey)` with their X25519 private key from SSM.

For `encryption: required`, if the recipient has no published encryption key, the send fails with an error. For `encryption: optional`, the message is sent in plaintext if the recipient has no key.

### Profile Controls

```yaml
spec:
  email:
    signing:      "required"    # Sign all outbound email
    verifyInbound: "required"   # Reject unsigned inbound email
    encryption:   "optional"    # Encrypt if recipient has a key
```

| Field | Values | Effect |
|-------|--------|--------|
| `spec.email.signing` | `required`, `optional`, `off` | Controls whether outbound email is signed |
| `spec.email.verifyInbound` | `required`, `optional`, `off` | Controls inbound signature verification policy |
| `spec.email.encryption` | `required`, `optional`, `off` | Controls end-to-end encryption for outbound email |

---

## 17. Threat Model

### Scenario: Agent Sandbox Escape

**Threat:** An AI agent exploits a vulnerability in the runtime environment, kernel, or sidecar to gain arbitrary code execution outside the sandbox constraints.

**Mitigations (layered):**
1. The agent is already running as an unprivileged user with constrained `agent.allowedTools`.
2. Filesystem bind mounts prevent modification of system binaries and configuration.
3. Even with arbitrary code execution, Security Groups block all egress except DNS (UDP 53) and HTTPS (TCP 443).
4. Even with arbitrary network access on allowed ports, the DNS proxy returns NXDOMAIN for non-allowed domains and the HTTP proxy returns 403 for non-allowed hosts.
5. Even if the agent bypasses iptables DNAT (requires root), the Security Group still blocks non-443/53 traffic at the VPC level.
6. Even if the agent compromises the entire EC2 instance, it is isolated by Security Groups within a shared VPC with no cross-sandbox traffic allowed.
7. Even if the agent escalates to the instance IAM role, it is region-locked and has permissions scoped to SSM, KMS decrypt, EventBridge PutEvents, DynamoDB budget tables, and optionally Bedrock model invocation.
8. Even if the agent reaches the AWS API, it is in the Application account -- Terraform state, DNS configuration, and billing are in separate accounts.

**Residual risk:** Kernel exploits or hypervisor escapes could theoretically breach the EC2 instance boundary. This is AWS's responsibility under the shared responsibility model.

### Scenario: Data Exfiltration via Allowed Hosts

**Threat:** An agent uses an allowed host (e.g., `api.github.com`) to exfiltrate sensitive data by encoding it in API request bodies, commit messages, or issue comments.

**Mitigations:**
1. GitHub App installation tokens are scoped to the repositories in `sourceAccess.github.allowedRepos`. Profiles without GitHub configuration have no token and no repository access.
2. `allowedRefs` restricts which branches can be pushed to (enforced via pre-push hook on EC2).
3. All HTTP requests through the proxy are logged with host, sandbox ID, and event type.
4. Audit logs capture shell commands, enabling post-incident forensic analysis.

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
3. Even if credentials are stolen, they are scoped to the sandbox role (SSM, KMS decrypt, EventBridge PutEvents, DynamoDB budget tables, and optionally Bedrock) with region lock.
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
2. The sandbox IAM role does not have `ec2:RunInstances` permission at all -- permissions are scoped to SSM, KMS decrypt, EventBridge, DynamoDB budget tables, and optionally Bedrock.
3. Lifecycle TTL ensures sandboxes are destroyed after their maximum lifetime, preventing indefinite resource consumption.
4. CloudTrail logs all API calls, including unauthorized attempts, enabling rapid detection.

**Residual risk:** Within the allowed region and existing instance, the agent can consume CPU and memory up to the instance type's limits. This is bounded by the instance type selection in the profile and the lifecycle TTL.

### Remaining Attack Surface Summary

| Vector | Status | Mitigation |
|--------|--------|------------|
| Kernel exploit / hypervisor escape | AWS shared responsibility | Account isolation limits blast radius |
| DNS exfiltration through allowed suffixes | Known limitation | Low bandwidth; logged; anomaly detection recommended |
| Data exfiltration through allowed HTTP hosts | Known limitation | Audit logging; scope `allowedRepos` and `allowedRefs` narrowly |
| iptables bypass (requires root) | Mitigated by unprivileged user + bind mounts | Security Groups still enforce at VPC level |
| Sidecar binary tampering | Mitigated by S3 source + boot-time download | Read-only `/opt/km/bin` recommended |
| Time-of-check/time-of-use on allowlists | Not applicable -- allowlists are compiled at boot | Sidecars load allowlist once at startup |
| CloudWatch log tampering | Mitigated by IAM scoping | Sandbox role has `logs:PutLogEvents` only, not `logs:DeleteLogGroup` |

---

## Built-in Profile Security Tiers

The four built-in profiles demonstrate the security model at different trust levels:

| Profile | DNS Suffixes | HTTP Hosts | Git Access | Agent Tools | Teardown | TTL |
|---------|-------------|------------|------------|-------------|----------|-----|
| `sealed` | Empty (zero egress) | Empty | None | None | `destroy` | 1h |
| `hardened` | `.amazonaws.com` only | `sts.us-east-1.amazonaws.com`, `ssm.us-east-1.amazonaws.com` | None | `read_file` only | `destroy` | 4h |
| `goose` | AWS + Anthropic + GitHub + npm + PyPI + OpenAI + Google | ~30 hosts | Read/write, allowlisted repos | `bash`, `read_file`, `write_file`, `list_files` | `stop` | 1h |

The `sealed` profile produces a sandbox with zero network egress, zero repository access, no agent tools, and a 1-hour TTL. It is the most restrictive possible configuration and serves as a baseline for verifying that the deny-by-default model works correctly.
