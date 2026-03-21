# Pitfalls Research

**Domain:** Policy-driven sandbox platform (YAML profiles -> EC2 + IAM + proxy sidecars)
**Researched:** 2026-03-21
**Confidence:** HIGH (critical pitfalls), MEDIUM (integration gotchas), HIGH (security mistakes)

---

## Critical Pitfalls

### Pitfall 1: Sidecar Proxies Are Not a Security Boundary — They Are Observability Aids

**What goes wrong:**
The DNS proxy and HTTP proxy sidecars run in the same OS trust domain as the workload (same EC2 instance, same Linux user namespace, typically running as root or with `NET_ADMIN`). Any process inside the sandbox can reconfigure iptables to bypass the proxy, kill the proxy process, or bind to the redirect port before the proxy. This is not theoretical — Istio security research documents seven distinct bypass techniques all applicable to a single-EC2 model. The sandbox "feels" enforced because well-behaved workloads comply, but a compromised or malicious agent can trivially escape.

**Why it happens:**
Teams design the enforcement model around the happy path: the agent runs, traffic flows through the proxy, logs accumulate. The adversarial model — where the agent itself is the attacker — is addressed late, after the enforcement story is already committed to stakeholders.

**How to avoid:**
Design the security model in two layers from day one:
1. **OS-level hard enforcement** — Use AWS Security Groups and VPC NACLs as the actual enforcement point. The security group on the sandbox EC2 instance should only allow egress to the HTTP proxy's port; no direct internet egress. Traffic can only leave via the proxy. If the proxy dies, traffic stops — fail-closed.
2. **Proxy as policy + audit layer** — The HTTP/DNS proxy then enforces the allowlist and writes audit records. It is not the last line of defense; it is the policy interpreter on top of the hard boundary.
3. **IMDSv2 with hop limit 1** — Enforce IMDSv2 (`http-tokens = required`, `http-put-response-hop-limit = 1`) at instance launch so workloads inside containers or child processes cannot trivially harvest IAM credentials via IMDS. Do this in the Terraform module, not as an afterthought.

**Warning signs:**
- Sidecar processes running as root without explicit justification
- Security group allowing `0.0.0.0/0` egress on port 443 (direct internet bypass)
- No test coverage verifying that killing the proxy stops outbound traffic
- Design documents describing the sidecar as "enforcement" without mentioning the OS-level layer

**Phase to address:** Phase — Sidecar + Network Enforcement. Must be established before any real workloads run.

---

### Pitfall 2: Terraform Destroy Leaves Orphaned Resources That Accumulate Silently

**What goes wrong:**
`terraform destroy` (or `terragrunt destroy`) fails on AWS resource dependency violations — most commonly: security groups that AWS won't delete while an ENI (elastic network interface) is still attached, VPCs that won't delete while subnets exist, or IAM instance profiles that won't delete while an EC2 instance is still terminating. The destroy command exits with an error, the sandbox is marked as "destroyed" in Klanker Maker's state, but real AWS resources persist. These orphans accrue cost and may hold residual IAM credentials that are still valid.

For Klanker Maker specifically, the inherited ec2spot module uses `aws_spot_instance_request` which has a known Terraform behavior: destroying the spot request does not automatically terminate the fulfilled EC2 instance. The actual instance requires a separate `aws_instance` resource or explicit termination logic.

**Why it happens:**
Happy-path testing: `apply` succeeds, `destroy` is run immediately after with no running workloads. Production failures happen when a workload has been running for 30 minutes — ENIs get attached by the OS, security groups get referenced by other resources created at runtime, or AWS takes longer than Terraform's timeout to terminate instances.

**How to avoid:**
- Implement a destroy pre-flight that calls AWS API directly (not Terraform state) to enumerate all resources tagged with the sandbox ID. Tag every resource at creation with `km:sandbox-id = <id>`.
- Add a sandbox-level garbage collector: a scheduled process (or a `km gc` command) that lists all AWS resources with the `km:` tag prefix and verifies they have corresponding live Terraform state. Any tagged resource without state is an orphan — alert and optionally destroy.
- For the ec2spot module specifically: ensure the `spot_type = "one-time"` and add explicit `aws_ec2_instance_state` or use `aws_instance` with a spot_instance_request_id rather than relying on `aws_spot_instance_request` cleanup behavior.
- Test destroy with a running workload (active connections, open files) — not just an idle instance.

**Warning signs:**
- Destroy command in CI only tested on freshly-created instances
- No resource tagging strategy defined upfront
- No orphan detection in `km list` or `km status`
- Cost anomalies in AWS Cost Explorer after sandbox sessions

**Phase to address:** Phase — Core Provisioning (tagging and teardown correctness from day one); Phase — Lifecycle Management (gc and orphan detection).

---

### Pitfall 3: IAM Role Compilation Produces Overly-Broad Policies via Inheritance Flattening

**What goes wrong:**
When a child SandboxProfile `extends` a parent (e.g., `restricted-dev` extends `open-dev`), and both define `identity.aws.allowedActions`, the merge behavior matters enormously. If compilation does a union (additive merge) of the two action lists, the child can never be *more* restrictive than the parent. A profile that says "I extend restricted-dev but only need S3 read" still gets all of restricted-dev's actions plus its own. The resulting IAM policy is broader than the author intended, and the compilation appears to succeed.

A second failure mode: YAML's own merge key (`<<:`) is deprecated in YAML 1.2 and has undefined behavior for arrays — it only merges mappings, not sequences. If the Go compiler uses Go's standard YAML library and array fields are merged naively, array fields from the parent silently overwrite child fields rather than being combined.

**Why it happens:**
Inheritance semantics are not explicitly defined before implementation begins. "Extends" is assumed to mean one thing (overlay/override) but implemented as another (additive). There is no test that proves a child profile produces a strictly scoped IAM policy even when its parent is permissive.

**How to avoid:**
- Define inheritance semantics explicitly in the schema spec before writing any compiler code: for allow-lists (actions, resources, repos, secrets), child values OVERRIDE parent values — not extend them. A child that specifies `allowedActions` gets exactly those actions.
- For fields that should be additive (e.g., `metadata.labels`), explicitly document that they merge.
- Write a compiler test that takes `open-dev` as parent and a minimal child profile, and asserts the resulting IAM policy JSON contains only the child's actions.
- Use IAM Access Analyzer to validate generated policies against a reference policy — integrate this into the `km validate` command.

**Warning signs:**
- Inheritance semantics not documented in the schema spec
- No test asserting that a restricted child of a permissive parent produces a restricted policy
- `km validate` only checks YAML schema validity, not compiled IAM policy scope
- IAM policies with `*` in Action or Resource fields in the compiled output

**Phase to address:** Phase — YAML Schema + Compiler. Define semantics in the spec before writing the compiler.

---

### Pitfall 4: Spot Instance Interruption During Active Workloads Creates Ungraceful Termination

**What goes wrong:**
AWS Spot instances receive a 2-minute interruption notice before termination. If Klanker Maker uses spot instances (the inherited ec2spot module does), a running agent workload gets killed mid-execution with no artifact upload, no audit log flush, and no cleanup. The sandbox state machine never receives a termination signal, leaving the sandbox stuck in "running" state. Artifacts and logs for that session are lost.

**Why it happens:**
Spot is chosen for cost savings and works fine in testing (spot interruptions are rare at low volume). The interruption handling path is not tested because it requires actually triggering a spot interruption or mocking AWS metadata. It gets deferred until a real interruption happens in production.

**How to avoid:**
- Poll the EC2 instance metadata endpoint for the spot interruption notice (`/latest/meta-data/spot/termination-time`) inside the audit log sidecar. On detection: flush all pending logs, trigger artifact upload if configured, and emit a `SANDBOX_INTERRUPTED` lifecycle event.
- Set the sandbox state to `interrupted` via a pre-termination hook (user data script calling back to Klanker Maker or writing to a well-known S3 prefix).
- Consider using `on-demand` instances for workloads with strict completion requirements, and use spot only for low-priority or resumable workloads. Expose this as a profile option (`runtime.instance_market: spot|on-demand`).
- Test the interruption path explicitly with `aws ec2 terminate-instances` on the underlying spot instance during a test run.

**Warning signs:**
- No handling of `/latest/meta-data/spot/termination-time` in sidecar startup code
- Sandbox list shows sandboxes stuck in "running" state after cost anomalies
- Audit logs missing the final minutes of a session
- No `instance_market` field in the SandboxProfile spec

**Phase to address:** Phase — Sidecar Implementation; Phase — Lifecycle Management.

---

### Pitfall 5: Profile Inheritance Chains Enable Circular Dependencies and Silent Infinite Loops

**What goes wrong:**
Profile A extends B, B extends C, C extends A. The YAML loader processes the first profile, tries to resolve its parent, encounters a cycle, and either hangs (infinite recursion) or panics (stack overflow). If validation does not detect cycles before compilation, a malformed profile file crashes the CLI rather than producing a user-friendly error.

YAML itself has no native cycle detection for includes or extends. The Go YAML library does not detect semantic cycles across multiple files — it only parses individual documents.

**Why it happens:**
The extends mechanism is implemented as a recursive "load parent, merge" operation. Cycle detection is assumed to be handled by the schema or by convention ("users won't do this") until someone does it by accident.

**How to avoid:**
- Implement cycle detection in the profile loader as a depth-first search over the `extends` graph before any compilation begins. Return a structured error with the cycle path: `A -> B -> C -> A`.
- Enforce a max inheritance depth (e.g., 5 levels) as a hard limit.
- Add `km validate` test cases with circular profiles and profiles with depth > 5.

**Warning signs:**
- Profile loader implemented as simple recursive function with no visited-set tracking
- `km validate` does not test for cycles
- No max-depth limit documented in the schema spec

**Phase to address:** Phase — YAML Schema + Compiler.

---

### Pitfall 6: Terragrunt Concurrent Init Corrupts Lock Files When Multiple Sandboxes Provision Simultaneously

**What goes wrong:**
When two `km create` commands run concurrently (two users, or a test suite), both invoke `terragrunt apply` which begins with `terraform init`. Concurrent inits sharing a provider cache directory (e.g., `TF_PLUGIN_CACHE_DIR`) corrupt the `.terraform.lock.hcl` file, causing one or both applies to fail with "inconsistent dependency lock file" errors. This is a known Terragrunt bug with multiple open GitHub issues (issues #2020, #2646, #4534).

**Why it happens:**
The default Terraform behavior is to share a provider cache across invocations. Terragrunt's `run-all` parallel execution or concurrent independent invocations hit this race condition. It's not caught in testing because single-user development always provisions one sandbox at a time.

**How to avoid:**
- Use Terragrunt's Provider Cache Server (`terragrunt provider-cache`) instead of `TF_PLUGIN_CACHE_DIR`. The cache server handles concurrent requests safely.
- Or: give each sandbox its own working directory with its own `.terraform/` directory, never sharing plugin caches between concurrent operations.
- Pin provider versions explicitly in all modules. Consistent provider versions eliminate checksum mismatches.
- Add a concurrency test: spin up two `km create` commands simultaneously and verify both succeed.

**Warning signs:**
- `TF_PLUGIN_CACHE_DIR` set globally in shell environment without concurrency controls
- No concurrent provisioning test in the test suite
- Provider versions not pinned (using `>= 5.0` instead of `= 5.82.0`)
- "inconsistent lock file" errors in CI logs

**Phase to address:** Phase — Core Provisioning.

---

## Technical Debt Patterns

Shortcuts that seem reasonable but create long-term problems.

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Allow all outbound in security group, rely on proxy for enforcement | Avoids complex SG rule management | Proxy bypass = no enforcement; direct internet access from sandbox | Never — SG egress restriction is the actual boundary |
| Skip sandbox-id tagging on some resources (e.g., key pairs) | Saves boilerplate | Orphan detector misses those resources; manual cleanup required | Never for v1 |
| Use `aws_spot_instance_request` without explicit terminate logic | Matches existing ec2spot module | Destroy leaves running instances; orphan accumulation | Acceptable only with explicit gc tooling from day one |
| Hardcode TTL enforcement in profile compile time | Simple to implement | Cannot change TTL of a running sandbox; requires destroy/recreate | Acceptable for v1 if documented |
| Store Terragrunt state per-sandbox in a shared S3 prefix | Simple path scheme | State path collisions if sandbox IDs are not globally unique | Never — enforce UUID-based IDs |
| Allow `extends` chains of arbitrary depth | Maximum flexibility | Cycles, hard-to-debug compiled policies, slow compilation | Cap at 5 levels |
| IMDSv1 enabled (no `http-tokens = required`) | No action needed | SSRF in workload = IAM credential theft from IMDS | Never — require IMDSv2 from the first module |

---

## Integration Gotchas

Common mistakes when connecting to external services.

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| AWS IMDS + sidecars | Setting hop limit to 1 then wondering why the sidecar process can't reach IMDS for its own credentials | Set hop limit to 2 when sidecars need IMDS access; or use explicit IAM creds for sidecar processes, not IMDS |
| SSM Parameter Store (secrets injection) | Compiling secret ARNs into the Terraform module directly | Compile secret paths into userdata/environment variables at provision time; the IAM role grants `ssm:GetParameter` scoped to those paths only |
| CloudWatch Logs (audit destination) | Assuming the CW agent starts before the workload | Use systemd `After=cloudwatch-agent.service` in workload unit; add a startup readiness check in the audit sidecar |
| GitHub source access via deploy key | Storing deploy key in userdata in plaintext | Store deploy key in SSM SecureString; pull at instance init via `aws ssm get-parameter --with-decryption`; delete from memory after use |
| Terragrunt remote state (S3 + DynamoDB lock) | Reusing the same DynamoDB table partition key format as defcon.run.34 | Use a sandbox-scoped key: `km/<sandbox-id>/terraform.tfstate.lock` to prevent lock collisions across sandboxes |
| EC2 spot price data source | `data.aws_ec2_spot_price.price` call fails in regions with no recent price history | Add a fallback: if spot price lookup fails, use on-demand price as a ceiling |

---

## Performance Traps

Patterns that work at small scale but fail as usage grows.

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|----------------|
| Terragrunt dependency graph recalculation on every apply | `km create` takes 60+ seconds before any AWS call is made | Pin Terragrunt to a version before 0.50.15; or restructure so sandbox modules have minimal cross-dependencies | ~5 concurrent sandboxes |
| All sandbox state in one S3 bucket with no partitioning | Terraform state list operations slow to enumerate; DynamoDB lock table hot partition | Use `km/<env>/<region>/<sandbox-id>/` key prefix; consider separate DynamoDB table per environment | ~50 total sandboxes |
| Audit log sidecar writing to local disk then syncing | Disk fills during long workload runs; data lost on spot interruption | Stream directly to CloudWatch Logs or S3 via the CW agent; local disk only as a buffer | Workloads > 1 hour |
| `km list` polling EC2 describe-instances for every call | Rate limiting from AWS DescribeInstances API at high frequency | Cache state in a local file (`~/.km/state.json`) updated on create/destroy; validate against AWS on explicit `--refresh` flag | >20 concurrent sandboxes |
| Single VPC shared across all sandboxes | Security group limits (AWS default: 5 SGs per ENI, 500 rules per SG) hit; blast radius if VPC misconfigured | Per-sandbox VPC or at minimum per-sandbox subnet and security group; VPC resource limits are generous for ephemeral environments | >100 active sandboxes |

---

## Security Mistakes

Domain-specific security issues beyond general web security.

| Mistake | Risk | Prevention |
|---------|------|------------|
| EC2 instance profile with `sts:AssumeRole` for any role in the account | Sandbox workload escalates to admin by assuming a high-privilege role | Scope the instance profile's `sts:AssumeRole` permission to a specific set of sandbox-scoped roles with a permission boundary |
| IMDSv1 enabled (no session token required) | SSRF in any HTTP library inside the workload exfiltrates IAM credentials via `169.254.169.254` | Set `http-tokens = required` and `http-put-response-hop-limit = 1` (or 2 if sidecars need IMDS) in every EC2 Terraform resource |
| Audit log sidecar can be killed by workload process | Workload kills audit process, then proceeds unlogged | Run sidecar as a separate systemd service with `Restart=always`; workload user does not have permission to `systemctl stop` the audit service |
| HTTP proxy allowlist bypassed via raw TCP/UDP | Workload uses non-HTTP protocol (DNS over UDP, raw TCP) to exfiltrate; proxy never sees it | Supplement HTTP proxy with VPC security group rules blocking UDP egress except DNS port 53 to the DNS proxy only |
| DNS proxy bypass via `/etc/resolv.conf` edit | Workload writes a new resolv.conf pointing to 8.8.8.8; DNS proxy is bypassed | Make `/etc/resolv.conf` immutable (`chattr +i`) after bootstrap; or use network namespace to prevent workload from changing DNS config |
| Sandbox-scoped IAM role has `iam:PassRole` | Workload launches services (Lambda, EC2) with elevated roles it passes | Exclude `iam:PassRole` from all sandbox IAM compilations unless explicitly listed in the profile's `identity.aws.allowedActions` |
| TLS private keys for EC2 key pairs stored in Terraform state | State file in S3 contains plaintext private key (visible to anyone with `s3:GetObject` on that prefix) | Use SSM Session Manager exclusively for access; do not create EC2 key pairs for sandboxes; remove SSH key generation from the sandbox module |

---

## UX Pitfalls

Common user experience mistakes in this domain.

| Pitfall | User Impact | Better Approach |
|---------|-------------|-----------------|
| `km create` blocks for 3-5 minutes with no output | User thinks CLI crashed; kills it mid-provision, leaving partial resources | Stream Terraform output in real time or print progress dots; print sandbox ID immediately and stream status |
| Validation errors are Terraform errors (HCL syntax) not YAML schema errors | User gets "Error: Invalid function argument" from Terraform when the YAML profile was the actual problem | Run `km validate` as a pre-flight inside `km create`; surface YAML errors before any Terraform call |
| Destroy requires the same YAML profile file that was used to create | Profile file moved/deleted = sandbox can never be destroyed | Store compiled Terragrunt inputs at provision time under `~/.km/sandboxes/<id>/`; destroy operates on stored state, not the profile file |
| `km list` shows sandbox IDs but no human context | Operator cannot remember which sandbox belongs to which workload | Store `metadata.name` and `metadata.labels` from the profile in local state; display them in list output |
| No warning before a sandbox is auto-destroyed by TTL | Workload in progress gets killed without notice | Emit a CloudWatch event / write to audit log 5 minutes before TTL expiry; if workload is active (based on recent audit log writes), optionally send a notification |

---

## "Looks Done But Isn't" Checklist

Things that appear complete but are missing critical pieces.

- [ ] **Network enforcement:** Proxy sidecar is running and filtering — but security group still allows direct internet egress. Verify: test with `curl` bypassing the proxy port; traffic should be blocked.
- [ ] **TTL lifecycle:** TTL timer fires and destroy is triggered — but destroy fails due to dependency violations, leaving orphaned resources. Verify: run destroy on an instance that has been active for 10+ minutes.
- [ ] **Audit logging:** Sidecar writes logs — but CloudWatch log group was never created. Verify: check log group exists before the instance sends its first log line.
- [ ] **IAM policy compilation:** Profile compiles to an IAM policy JSON — but the policy exceeds the 6,144 character IAM managed policy size limit for complex profiles. Verify: measure compiled policy size for the `open-dev` built-in profile.
- [ ] **Secrets injection:** Secrets are fetched from SSM at boot — but the IAM role allows `ssm:GetParameter` with `Resource: *`. Verify: confirm resource ARNs are scoped to the specific parameter paths listed in the profile.
- [ ] **Spot interruption:** Sandbox handles normal TTL destroy — but a spot interruption leaves the sandbox stuck in "running" state. Verify: terminate the underlying instance while Klanker Maker believes it's running; check that status reconciliation catches it.
- [ ] **Profile extends:** Child profile produces a narrower IAM policy than its parent — not an additive union. Verify: compile a minimal child of `open-dev` and assert the action list matches the child spec exactly.
- [ ] **Concurrent creates:** Two `km create` commands run simultaneously — Terragrunt provider cache is not corrupted. Verify: run a concurrency test in CI.

---

## Recovery Strategies

When pitfalls occur despite prevention, how to recover.

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| Orphaned resources after failed destroy | MEDIUM | 1. `aws resourcegroupstaggingapi get-resources --tag-filters Key=km:sandbox-id,Values=<id>` to find all tagged resources. 2. Terminate EC2 first, then delete security groups, then VPC. 3. Remove stale Terraform state with `terraform state rm`. |
| IAM policy too permissive due to inheritance bug | HIGH | 1. Immediately revoke the over-permissive role via SCP or explicit Deny. 2. Review CloudTrail for actions taken under the role. 3. Fix compiler inheritance semantics. 4. Recompile and re-apply all active sandbox roles. |
| Proxy bypass discovered (workload accessing unauthorized destinations) | HIGH | 1. Destroy the sandbox immediately. 2. Review CloudTrail for API calls from the instance profile. 3. Add security group egress rules to block direct internet access. 4. Verify enforcement model follows SG-first approach. |
| Terragrunt lock file corruption under concurrency | LOW | 1. Delete `.terraform.lock.hcl` in the affected sandbox working directory. 2. Re-run `terragrunt init`. 3. Re-apply. |
| Spot interruption leaves sandbox in "running" state | LOW | 1. `km status <id>` triggers reconciliation against EC2 API. 2. Mark sandbox as `interrupted`. 3. Retrieve logs from CloudWatch for the session. |
| Sandbox stuck destroying (security group dependency violation) | MEDIUM | 1. Identify the blocking ENI: `aws ec2 describe-network-interfaces --filters Name=group-id,Values=<sg-id>`. 2. Detach/delete the ENI manually. 3. Re-run `terragrunt destroy`. |

---

## Pitfall-to-Phase Mapping

How roadmap phases should address these pitfalls.

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| Sidecar is not a security boundary | Phase: Sidecar + Network Enforcement | Test: kill proxy process, verify outbound traffic blocked by SG |
| Terraform destroy orphans resources | Phase: Core Provisioning + Lifecycle | Test: destroy with running workload; run gc and verify zero orphans |
| IAM inheritance produces over-broad policies | Phase: YAML Schema + Compiler | Test: child profile IAM JSON contains only child's actions |
| Spot interruption ungraceful | Phase: Sidecar + Lifecycle | Test: terminate underlying instance; verify logs flushed and state updated |
| Profile cycle causes hang/crash | Phase: YAML Schema + Compiler | Test: validate circular profile; expect structured error not panic |
| Concurrent Terragrunt lock corruption | Phase: Core Provisioning | Test: two simultaneous creates; both succeed |
| IMDSv1 credential theft | Phase: Core Provisioning (module config) | Test: `curl http://169.254.169.254/latest/meta-data/iam/security-credentials/` returns 401 from workload |
| DNS proxy bypassed via resolv.conf edit | Phase: Sidecar Implementation | Test: workload writes `/etc/resolv.conf`, verify DNS still routes through proxy |
| TLS private keys in state | Phase: Core Provisioning | Verify: no `aws_key_pair` or `tls_private_key` resources in sandbox Terraform modules |

---

## Sources

- [Outbound sidecars are not secure enforcement points - howardjohn's blog](https://blog.howardjohn.info/posts/bypass-egress/) — authoritative breakdown of seven bypass techniques; architecture recommendation for egress gateway as true enforcement
- [EC2 IAM role STS credentials compromise via IMDS - ilyakobzar.com](https://www.ilyakobzar.com/p/ec2-iam-role-sts-credentials-compromise) — IMDS credential exfiltration mechanics
- [Get the full benefits of IMDSv2 - AWS Security Blog](https://aws.amazon.com/blogs/security/get-the-full-benefits-of-imdsv2-and-disable-imdsv1-across-your-aws-infrastructure/) — IMDSv2 enforcement guidance
- [IAM Least Privilege: What Everyone Gets Wrong - DEV Community](https://dev.to/aws-builders/iam-least-privilege-what-everyone-gets-wrong-and-how-to-fix-it-with-terraform-2k5j) — common IAM scoping mistakes
- [Terragrunt lock file corruption issues - GitHub](https://github.com/gruntwork-io/terragrunt/issues/2646) — concurrent init lock file bug tracker
- [Top 5 Most Common Terragrunt Issues - Scalr, May 2025](https://scalr.com/learning-center/top-5-most-common-terragrunt-issues-may-2025/) — Terragrunt 0.50.15 performance regression and concurrency issues
- [When Terraform Destroy Gets Stuck - Medium](https://rakeshkadam.medium.com/when-terraform-destroy-gets-stuck-debugging-aws-resource-dependencies-a630fc432ddd) — real-world destroy failure modes
- [Ephemeral sandbox environments 2026 guide - Northflank](https://northflank.com/blog/ephemeral-sandbox-environments) — lifecycle management patterns
- [Automating lifecycle management for ephemeral resources - AWS Cloud Operations Blog](https://aws.amazon.com/blogs/mt/automating-life-cycle-management-for-ephemeral-resources-using-aws-service-catalog/) — TTL and garbage collection patterns
- [AWS IAM Privilege Escalation Methods and Mitigation - Rhino Security Labs](https://rhinosecuritylabs.com/aws/aws-privilege-escalation-methods-mitigation/) — iam:PassRole and privilege escalation
- defcon.run.34 ec2spot module (`~/working/defcon.run.34/infra/terraform/modules/ec2spot/v1.0.0/main.tf`) — direct inspection of inherited module; identified `aws_spot_instance_request` teardown gap and open SSH egress in security group

---
*Pitfalls research for: policy-driven sandbox platform (Klanker Maker)*
*Researched: 2026-03-21*
