---
name: user
description: Operator CLI guide for the km command — creating sandboxes, running agents, learning traffic, managing lifecycle
---

# Klanker Maker Operator Guide

This skill guides usage of the `km` CLI on the operator's workstation. It covers platform setup, sandbox creation, agent execution, learn mode, and lifecycle management.

## Getting Started

### Prerequisites

- AWS CLI configured with a `klanker-terraform` profile
- Go 1.21+ (for building from source)
- `km` binary built: `make build`

### Configuration

The platform configuration lives in `km-config.yaml`. See `docs/km-config.example.yaml` for the full template. Required fields:

- `artifacts_bucket` — S3 bucket for sandbox artifacts
- `state_bucket` — S3 bucket for Terraform state
- `github_app_id` / `github_installation_id` — GitHub App credentials (for source access)
- `operator_email` — Operator inbox address

### Health Check

Always start by verifying platform health:

```bash
km doctor
```

This runs 17 checks: config, credentials, SES, Lambda, VPC, stale resources, etc. Fix any failures before proceeding.

### Platform Info

```bash
km info
```

Shows: platform config, AWS accounts, SES quota, current AWS spend, DynamoDB tables, storage.

## Bootstrap / Init

### Full Infrastructure Deploy

```bash
km init
```

Deploys all regional infrastructure: VPC, subnets, security groups, Lambda functions, DynamoDB tables, SES, EventBridge rules.

### Fast Deploys (After Code Changes)

```bash
km init --sidecars    # Rebuild and upload sidecar binaries + km binary only
km init --lambdas     # Redeploy Lambda functions only
```

Always run `km doctor` after init to verify.

## Creating Sandboxes

### Default: Use the Learn Profile

When the user doesn't specify a profile, or is exploring/getting started, use the learn profile:

```bash
km create profiles/learn.yaml --alias my-sandbox
```

The learn profile (`profiles/learn.yaml`) is designed for exploration:
- Wildcard DNS and host allowlists (`"*"`) — all network traffic allowed
- Wildcard GitHub repos and refs (`"*"`) — all repos accessible
- eBPF enforcement in `both` mode with full observability
- `privileged: true` for sudo access
- `teardownPolicy: stop` — pause instead of destroy on TTL
- Pre-installed tools: claude-code, goose, codex, git, node, python

### Validate Before Creating

```bash
km validate <profile.yaml>
```

Always validate custom profiles before creating sandboxes.

### Common Create Flags

```bash
km create <profile.yaml> [flags]
```

| Flag | Description |
|------|-------------|
| `--alias <name>` | Human-friendly name (used in hostname, tips, email display name) |
| `--on-demand` | Use on-demand EC2 instead of spot (enables pause/hibernate) |
| `--docker` | Create as local Docker container instead of EC2 |
| `--no-bedrock` | Skip Bedrock configuration (use direct API) |
| `--ttl <duration>` | Override profile TTL (e.g., `--ttl 4h`) |
| `--idle <duration>` | Override idle timeout (e.g., `--idle 30m`) |

### Clone an Existing Sandbox

```bash
km clone <source> <alias>              # Clone with workspace copy
km clone <source> <alias> --no-copy    # Clone profile only (fresh workspace)
km clone <source> <alias> --count 3    # Create 3 clones (alias-1, alias-2, alias-3)
```

## Agent Execution

### Fire-and-Forget

```bash
km agent run <sandbox> --prompt "fix the failing tests"
```

Returns immediately. Agent runs in a persistent tmux session.

### Wait for Completion

```bash
km agent run <sandbox> --prompt "What model are you?" --wait
```

Blocks until done, prints JSON result with `result`, `total_cost_usd`, token usage.

### Interactive (Live Attach)

```bash
km agent run <sandbox> --prompt "refactor the auth module" --interactive
```

Creates tmux session and attaches you. Detach with `Ctrl-B d` — agent keeps running.

### Attach to Running Agent

```bash
km agent attach <sandbox>
```

### Fetch Results

```bash
km agent results <sandbox>                          # Latest run
km agent results <sandbox> --run 20260410T143000Z   # Specific run
km agent results <sandbox> | jq '.result'           # Just the answer
km agent results <sandbox> | jq '.total_cost_usd'   # Cost
```

### List Runs

```bash
km agent list <sandbox>
```

### Direct API (Skip Bedrock)

```bash
km agent run <sandbox> --prompt "..." --no-bedrock --wait
```

Requires `claude login` on the sandbox first, or set `spec.cli.noBedrock: true` in the profile.

### Auto-Start Paused Sandboxes

```bash
km agent run <sandbox> --prompt "..." --auto-start
```

Resumes the sandbox if it's paused/stopped before running the agent.

## Learn Mode

Generate a minimal SandboxProfile from observed traffic:

### Step 1: Create a Learn Sandbox

```bash
km create profiles/learn.yaml --alias learn-1
```

### Step 2: Shell In with Learn Flag

```bash
km shell --learn learn-1
```

This starts an SSM session with eBPF traffic recording. All DNS queries, HTTP hosts, and GitHub repos are observed.

### Step 3: Work Normally

Inside the sandbox, do whatever the target workload does — clone repos, install packages, call APIs. The observer records everything.

### Step 4: Exit and Generate Profile

When you exit the shell, the observer flushes observations to S3 and generates an annotated profile:

```
observed-profile.yaml
```

The generated profile includes:
- `allowedDNSSuffixes` collapsed from observed DNS domains
- `allowedHosts` for hosts not covered by DNS suffixes
- `allowedRepos` from observed GitHub clone/fetch operations
- `allowedRefs` from observed Git ref operations
- Annotations showing which domains mapped to each suffix

### Step 5: Review and Customize

```bash
km validate observed-profile.yaml
```

Review the generated profile, tighten the allowlists, adjust lifecycle settings, then use it for production sandboxes.

## Lifecycle Management

### List Sandboxes

```bash
km list                # Narrow view: #, alias, sandbox-id, status, TTL
km list --wide         # All columns including substrate, region, profile
```

### Pause / Resume

```bash
km pause <sandbox>     # Hibernate (on-demand) or stop (spot)
km resume <sandbox>    # Restart a paused/stopped sandbox
```

### Stop / Destroy

```bash
km stop <sandbox>      # Stop without destroying infrastructure
km destroy <sandbox>   # Full teardown (remote by default)
```

### Lock / Unlock

```bash
km lock <sandbox>      # Prevent accidental destroy/stop/pause
km unlock <sandbox>    # Remove safety lock (requires confirmation)
```

### Scheduling

```bash
km at 'in 2 hours' destroy <sandbox>           # Deferred destroy
km at '5pm tomorrow' agent run <sandbox> --prompt "nightly tests" --auto-start
km at 'every day at 9am' agent run <sandbox> --prompt "daily check" --auto-start
km at list                                      # List scheduled operations
km at cancel <schedule-name>                    # Cancel a schedule
```

## Monitoring

### OTEL Telemetry

```bash
km otel <sandbox>              # Summary: AI spend, token usage
km otel <sandbox> --prompts    # All prompts sent to AI models
km otel <sandbox> --events     # Lifecycle events
km otel <sandbox> --tools      # Tool usage breakdown
km otel <sandbox> --timeline   # Chronological activity timeline
```

### Shell Access

```bash
km shell <sandbox>             # SSM shell as sandbox user
km shell <sandbox> --root      # Root shell
km shell <sandbox> --ports 8080:8080  # Port forwarding
```

## Email (Operator Side)

### Send Email

```bash
km email send --to <sandbox> --subject "task spec" --body spec.md
km email send --from <sandbox-a> --to <sandbox-b> --subject "results" --attach output.tar.gz
```

### Read Mailbox

```bash
km email read <sandbox>              # Table format with signature verification
km email read <sandbox> --json       # JSON for scripting
km email read <sandbox> --mark-read  # Mark as processed
```

## Quick Reference

| Task | Command |
|------|---------|
| Validate platform | `km doctor` |
| Create sandbox | `km create profiles/learn.yaml --alias name` |
| Shell in | `km shell name` |
| Run agent | `km agent run name --prompt "..." --wait` |
| Check results | `km agent results name` |
| Pause | `km pause name` |
| Resume | `km resume name` |
| Destroy | `km destroy name` |
| Schedule | `km at 'time' command args` |
| Monitor | `km otel name` |
