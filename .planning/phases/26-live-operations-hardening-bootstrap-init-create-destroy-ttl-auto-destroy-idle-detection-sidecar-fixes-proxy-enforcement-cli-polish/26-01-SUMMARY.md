# Phase 26: Live Operations Hardening — Summary

**Status:** Complete
**Date:** 2026-03-24 through 2026-03-26
**Commits:** ~60 commits across the session

## Overview

End-to-end operational hardening driven by live testing of the km CLI against real AWS infrastructure. Every fix was discovered by running the actual commands and hitting real errors.

## Bootstrap & Configuration

- **SOPS eager evaluation fix** — replaced HCL ternary with shell-based conditional in site.hcl to avoid calling sops on non-existent files
- **Duplicate generate "provider" blocks** — removed root include from management/scp and s3-replication terragrunt configs (Terragrunt 0.99 incompatibility)
- **Bootstrap env var passthrough** — km bootstrap now exports KM_ACCOUNTS_*, KM_DOMAIN, KM_REGION from km-config.yaml before running Terragrunt
- **Artifacts bucket in configure** — added artifacts_bucket prompt and flag to km configure
- **Artifacts bucket creation in bootstrap** — km bootstrap creates the S3 artifacts bucket with versioning
- **Route53 auto-setup** — km init auto-creates sandboxes.{domain} hosted zone and NS delegation to management account
- **SCP trusted role carve-out** — added operator role exemption to DenyOutsideRegion for cross-region operations (S3 replication)
- **S3 replication delete_marker_replication** — required by V2 replication config schema

## km init Improvements

- **Auto-build Lambda zips** — km init cross-compiles ttl-handler, budget-enforcer, github-token-refresher for linux/arm64
- **Always rebuild** — removed skip-if-exists logic; init always builds fresh
- **Terraform binary bundling** — downloads terraform arm64 and bundles in ttl-handler.zip for Lambda-based destroy
- **Auto-build and upload sidecars** — cross-compiles dns-proxy, http-proxy, audit-log for linux/amd64 and uploads to S3
- **Proxy CA cert generation** — generates ECDSA P-256 CA cert+key for MITM proxy and uploads to S3
- **Structured output** — section headers with ── separators, "done" indicators per module

## TTL Auto-Destroy (Lambda-based terraform destroy)

- **Terraform binary in Lambda** — bundled terraform arm64 in ttl-handler.zip
- **Ephemeral storage** — increased to 2GB for provider download
- **Memory** — increased to 1536MB (terraform + AWS provider needs ~1GB)
- **source_code_hash** — added to force Lambda code redeployment when zip changes
- **-lock=false** — prevents concurrent Lambda invocations from deadlocking on state lock
- **Work directory cleanup** — os.RemoveAll before MkdirAll to handle OOM crash leftovers
- **Module variables** — pass required ec2spot module variables (km_label, region_label, etc.) for terraform plan
- **ec2:DescribeVolumes, DescribeInstanceTypes, DescribeInstanceAttribute** — added missing IAM permissions
- **Budget-enforcer cleanup** — Lambda deletes budget-enforcer Lambda, EventBridge schedule, IAM roles, log group after main destroy
- **S3 metadata cleanup** — deletes metadata.json so km list stays accurate
- **CloudWatch log group cleanup** — deletes sandbox log group after destroy
- **logs:DeleteLogGroup IAM permission** — added to Lambda role

## Idle Detection

- **Audit-log sidecar FIFO** — created /run/km/audit-pipe for stdin, unblocked with `sleep infinity >` writer
- **PROMPT_COMMAND audit hook** — writes JSON events to FIFO on every shell command
- **Background heartbeat** — 60-second keepalive while shell sessions are open (prevents idle kill during long commands)
- **RFC3339 timestamp fix** — PROMPT_COMMAND was writing Unix millis (number) but AuditEvent expects RFC3339 string; every event was silently dropped
- **Empty log stream grace period** — idle detector waits full IdleTimeout before declaring idle on startup (prevents immediate kill)
- **chmod 666 on audit pipe** — allows sandbox user (non-root) to write events
- **EventBridge idle rule** — km-sandbox-idle routes SandboxIdle events to TTL Lambda

## Sidecar & Proxy Fixes

- **iptables-nft installation** — AL2023 doesn't include iptables; install iptables-nft compatibility layer
- **iptables negation syntax** — `! -m owner` → `-m owner !` for nft compatibility
- **Root user DNAT exemption** — uid 0 exempt from DNAT so SSM agent and system services bypass proxy
- **Proxy env vars** — set http_proxy/https_proxy/no_proxy in /etc/profile.d for explicit HTTPS proxy support
- **Mail poller sidecar** — new systemd service polls S3 mail/ prefix every 60s, delivers to /var/mail/km/new/
- **CloudWatch log retention** — 7-day retention on sandbox log groups
- **AUDIT_LOG_DEST=cloudwatch** — sidecar writes to CloudWatch instead of stdout

## CLI Polish

- **Numbered sandbox list** — km list shows #1-N, all commands accept numbers
- **km destroy confirmation** — prompts "are you sure?", --yes to skip
- **Stale lock detection** — km destroy detects state locks and offers to clear with -lock=false
- **--yes auto-clears locks** — no prompt for lock clearing when --yes is set
- **km conf alias** — shortcut for km configure
- **km github shortcut** — shortcut for km configure github --setup
- **GitHub manifest flow fix** — POST form instead of GET URL for proper redirect
- **--discover flag** — auto-discover GitHub App installation ID from SSM credentials
- **km extend command** — add time to running sandbox TTL
- **km stop command** — stop EC2 instance without destroying infrastructure
- **km shell --root** — operator access (default is restricted sandbox user)
- **km shell --ports** — SSM port forwarding with Docker-style syntax
- **--remote flag** — destroy, extend, stop can dispatch to Lambda via EventBridge
- **Progress dots** — animated dots during km create provisioning
- **Elapsed time** — shown at end of km create
- **Local timezone** — all times displayed in operator's timezone
- **Spot capacity error** — friendly message with --on-demand suggestion
- **km uninit** — reverse of init, destroys all regional infrastructure with confirmation
- **km list live status** — checks actual EC2 instance state (shows "killed" for spot reclamation)
- **km status idle countdown** — colored countdown to idle kill (green/yellow/red)
- **km doctor header/footer** — consistent styling across commands
- **km doctor SCP check** — assumes km-org-admin role in management account
- **km doctor VPC check** — fixed tag filter to match actual network module tags

## Security

- **Per-sandbox security group names** — km-ec2spot-{sandbox_id}-{region} prevents collisions
- **Restricted sandbox user** — km shell connects as non-root 'sandbox' user by default
- **sandbox_iam_role_arn** — wired to github-token and budget-enforcer modules
- **Hardcoded bucket removal** — all artifact bucket references use config, not hardcoded defaults

## Infrastructure Fixes

- **Duplicate provider blocks** — removed from s3-replication and management/scp terragrunt configs
- **aws_scheduler_schedule tags** — removed unsupported tags attribute (AWS provider v6)
- **aws_region.current.name** — replaced deprecated attribute with .id
- **Budget-enforcer dependency block** — removed Terragrunt 0.99 incompatible mock_outputs_allowed_on_destroy
- **Destroy minimal service.hcl** — includes substrate_module and module_inputs for state resolution
- **S3 metadata deletion on destroy** — km list no longer shows destroyed sandboxes
- **Orphaned resource cleanup** — manual cleanup of SGs, IAM roles, log groups, S3 state from failed early sandboxes
