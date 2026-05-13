# GitHub Actions self-hosted runner — design

**Date:** 2026-05-13
**Status:** Draft — awaiting GSD phase planning

## Summary

A klanker sandbox can be marked as a self-hosted GitHub Actions runner for one or more repos. At boot, a sandbox-side service fetches per-repo registration tokens via a new Lambda (reusing the existing GitHub App), runs N `actions/runner` instances under systemd template units, and deregisters cleanly on shutdown. Per-repo registration is best-effort and independently retryable via a new `km runner` CLI subcommand. The sandbox's existing policy boundary (eBPF, proxy MITM, allowedDomains, budget, audit) wraps the workflow runtime — a workflow can only reach hosts the profile allows.

## Goals

1. A sandbox can be a self-hosted Actions runner for one or more GitHub repos, declared in its profile.
2. Reuse the existing GitHub App + JWT minting + SSM scoping pattern from `pkg/github/token.go`.
3. Per-repo registration failures are recoverable in-place without destroying the sandbox.
4. Clean teardown on `km destroy` — no ghost runners left registered on GitHub.
5. `km doctor` surfaces broken registrations and ghost runners.

## Non-goals (v1)

- Org-scoped or enterprise-scoped runner registration. Repo-only.
- Ephemeral / just-in-time (JIT) runners. Long-lived persistent only.
- On-demand sandbox provisioning from a GitHub webhook (no `workflow_job` listener Lambda).
- Hot-mutation of `runner.repos` on a live sandbox — requires `km destroy && km create` (immutable, matches every other profile field).
- Pre-provisioned runner pools.
- Non-GitHub runner providers (GitLab, Jenkins, etc.).

## User experience

```yaml
# profile.yaml
spec:
  sourceAccess:
    github:
      allowedRepos:                 # existing — what the sandbox can clone/push
        - my-org/repo-a
        - my-org/repo-b
      allowedRefs: [main]           # existing
      runner:                       # NEW
        enabled: true               # default false
        repos:                      # MUST be subset of allowedRepos
          - my-org/repo-a
          - my-org/repo-b
        labels: [gpu, cuda]         # appended to defaults; cannot override defaults
        runnerGroup: default        # optional; reserved for org scope (no-op in v1)
        maxRepos: 5                 # validation cap (default 5); guardrail
```

```bash
km validate profile.yaml           # validates runner.repos ⊆ allowedRepos, maxRepos cap, etc.
km create profile.yaml             # provisions sandbox; prints per-repo registration status on exit
km runner status <sandbox>         # show per-repo state (DDB-cached; --refresh hits GitHub API)
km runner reattach <sandbox>       # retry all failed registrations
km runner reattach <sandbox> --repo my-org/repo-a   # retry one
km runner detach <sandbox>         # manual deregister all (hot-swap path)
km runner detach <sandbox> --repo my-org/repo-a     # manual deregister one
km doctor                          # reports actions_runner_register_failures + actions_runner_ghosts
km destroy <sandbox> --remote --yes   # cleanly deregisters all runners before tearing down EC2
```

**`km create` operator output** when `runner.enabled: true`:
```
Runner registration: 2/3 repos online (1 failed)
  ✓ my-org/repo-a
  ✓ my-org/repo-c
  ✗ my-org/repo-b — registration token API returned 403; check App permissions
Hint: `km runner reattach <sandbox>` to retry failed registrations.
```

## Default labels (always applied, not removable)

| Label | Purpose |
|---|---|
| `self-hosted` | Required by GitHub |
| `klanker` | Workspace-wide selector |
| `klanker-<sandbox-id>` | Pin to one specific sandbox |
| `klanker-<alias>` | If `--alias` was set on `km create` |

User-supplied `runner.labels: [...]` is appended to the defaults. Workflows target runners via `runs-on: [self-hosted, klanker]` or any subset.

## Validation rules (`pkg/profile/validate.go`)

| Rule | Failure mode |
|---|---|
| `runner.repos ⊆ allowedRepos` (with wildcard expansion: `allowedRepos: ["org/*"]` matches any `runner.repos` entry under `org/`) | Hard error at `km validate` |
| `len(runner.repos) <= maxRepos` (default 5) | Hard error at `km validate` |
| `runner.enabled: true` ⇒ `allowedRepos` non-empty | Hard error at `km validate` |
| `runner.enabled: true` incompatible with `observability.learnMode: true` | Hard error at `km validate` |
| Every repo's owner has a known installation ID in SSM | Hard error at `km create` (validate is offline; can't check SSM) |

## Architecture

### New components

| Component | Type | Purpose |
|---|---|---|
| `cmd/km-actions-runner-token/` | Lambda (Go, ARM64, `provided.al2023`) | Action-dispatched: mints registration tokens, mints removal tokens, deletes runners, lists runners. Mirrors `cmd/github-token-refresher/` shape. |
| `pkg/github/runner.go` | Go package | Functions: `GetRegistrationToken`, `GetRemovalToken`, `DeleteRunner`, `ListRunners`. Alongside existing `token.go`. |
| `sidecars/km-actions-runner` | Sandbox-side binary (Go) | Per-repo orchestrator invoked from systemd units (see below). |
| `klanker-actions-runner-register@.service` | systemd template (oneshot) | Per-repo registration step. Calls token Lambda via sigv4, runs `config.sh`, writes runner_id to DDB. |
| `klanker-actions-runner@.service` | systemd template (long-running) | `Wants=`/`After=` register unit. Runs `./run.sh` from `/opt/km/actions-runner/<repo-slug>/`. |
| `klanker-actions-runner-shutdown.service` | systemd oneshot | `Type=oneshot`, `RemainAfterExit=yes`, `ExecStop=` runs deregister for each repo at shutdown. |
| `infra/modules/actions-runner-token/v1.0.0/` | Terraform module | Provisions Lambda + IAM role + Function URL (`AuthType: AWS_IAM`). |
| `internal/app/cmd/runner.go` | Go (CLI) | Implements `km runner status|reattach|detach`. |

### Reused components

- GitHub App credentials in SSM (`/km/config/github/{app-client-id,private-key,installations/<owner>}`)
- `pkg/github` JWT generation (`GenerateGitHubAppJWT`)
- Presence daemon (Phase 79) gains a sixth signal — `pgrep -f Runner.Listener` returns ≥1 process → emit presence heartbeat. Prevents idle-but-listening runner from being auto-paused.
- DDB `{prefix}-sandboxes` table — two additive fields, no schema migration required:
  - `actions_runner_repos: List<String>` — copied from profile at create
  - `actions_runners: Map<String, Map>` — per-repo state: `{repo: {runner_id, status, last_error, last_attempt}}`

### Data flow — registration at boot

```
sandbox boot (cloud-init)
  │
  ├─ /etc/km/runner-repos.json written by userdata
  │    [{"repo":"my-org/repo-a"},{"repo":"my-org/repo-b"}]
  │
  └─ For each repo slug from runner-repos.json:
        systemctl enable klanker-actions-runner-register@<repo-slug>.service
        systemctl enable klanker-actions-runner@<repo-slug>.service
        │
        ├─ register@<repo>.service (oneshot)
        │     ExecStart=/opt/km/bin/km-actions-runner register --repo <repo>
        │     │
        │     ├─ sigv4 POST to km-actions-runner-token Lambda Function URL
        │     │    payload: {sandbox_id, action: "register", repos: [<repo>]}
        │     │
        │     ├─ Lambda mints App JWT, calls
        │     │    POST /repos/{owner}/{repo}/actions/runners/registration-token
        │     │    returns [{repo, token, expires_at}]
        │     │
        │     ├─ mkdir /opt/km/actions-runner/<repo-slug>/
        │     │ cp -r /opt/km/actions-runner-template/* <dir>/
        │     │ ./config.sh --url https://github.com/<repo> --token <reg-token> \
        │     │             --name "klanker-<sandbox>-<repo-slug>" \
        │     │             --labels "self-hosted,klanker,klanker-<sandbox>,klanker-<alias>,<user-labels>" \
        │     │             --unattended --replace
        │     │
        │     └─ Read runner_id from .runner file → write DDB
        │           UpdateItem actions_runners.<repo> = {runner_id, status: "online", ...}
        │
        └─ @<repo>.service (long-running)
              Wants= + After= the register@ unit
              ExecStart=/opt/km/actions-runner/<repo-slug>/run.sh
              Restart=on-failure
```

### Data flow — teardown at km destroy

```
km destroy <sandbox> --remote --yes
  │
  ├─ Read actions_runner_repos + actions_runners from DDB
  │
  ├─ SSM RunCommand to sandbox (best-effort, 60s timeout):
  │     systemctl stop 'klanker-actions-runner@*.service'
  │     For each repo: km-actions-runner remove --repo <repo>
  │       └─ Lambda mints removal token → config.sh remove --token <X>
  │
  ├─ Belt-and-suspenders (always runs, even if SSM RunCommand fails):
  │     For each (repo, runner_id) in DDB.actions_runners:
  │       Operator-side calls km-actions-runner-token Lambda with action: "delete"
  │       Lambda mints App JWT, calls DELETE /repos/{owner}/{repo}/actions/runners/{runner_id}
  │
  └─ Normal terragrunt destroy proceeds
```

The belt-and-suspenders DELETE is the load-bearing teardown — SSM RunCommand can fail if the sandbox is unreachable, but the operator-side API call always runs and catches the ghost case.

### Data flow — registration failure recovery

```
km runner reattach <sandbox> [--repo R | --all-failed]
  │
  ├─ SSM RunCommand:
  │     systemctl reset-failed klanker-actions-runner-register@<repo-slug>.service
  │     systemctl restart klanker-actions-runner-register@<repo-slug>.service
  │
  └─ Poll DDB.actions_runners.<repo>.status until "online" or timeout (90s)
        Print per-repo result table to operator
```

`km runner status <sandbox>` (no `--refresh`) reads DDB only. With `--refresh`, also calls `GET /repos/{owner}/{repo}/actions/runners` via the token Lambda and reconciles — flags any drift (e.g., DDB says online but GitHub doesn't see the runner).

## Network policy

`actions/runner` requires several GitHub endpoints. The profile compiler auto-injects them into the sandbox's effective DNS/proxy allowlist when `runner.enabled: true`, so operators don't have to remember:

| Host | Purpose |
|---|---|
| `api.github.com` | Already allowed (App token refresher). No change. |
| `github.com` | Runner binary download + git clone. Typically already allowed. |
| `*.actions.githubusercontent.com` | Job dispatch + log streaming. **New.** |
| `*.pkg.actions.githubusercontent.com` | Action package downloads. **New.** |
| `objects.githubusercontent.com` | Artifact upload/download. **New.** |
| `codeload.github.com` | `actions/checkout` tarball path. **New.** |

These are appended in `pkg/compiler/` to the effective allowlist, not the user's YAML.

**Policy boundary statement:** Workflow jobs execute arbitrary shell under the sandbox user. Outbound traffic still traverses the sandbox's proxy + eBPF allowlist. A workflow cannot reach `evil.com` unless the profile allows it. This is the design's primary value proposition for compliance/security-sensitive operators.

## IAM

### Lambda role (`km-actions-runner-token`)

```
ssm:GetParameter on /km/config/github/app-client-id
ssm:GetParameter on /km/config/github/private-key (with KMS Decrypt grant)
ssm:GetParameter on /km/config/github/installations/*
dynamodb:UpdateItem on {prefix}-sandboxes
logs:CreateLogStream, logs:PutLogEvents on its own log group
```

### Per-sandbox instance role (additive)

```
lambda:InvokeFunctionUrl on arn:aws:lambda:<region>:<acct>:function:{prefix}-actions-runner-token
```

Same pattern as Phase 67's slack-bridge: sandbox calls Lambda Function URL with sigv4-signed requests. `AuthType: AWS_IAM` blocks the public internet.

### Lambda event schema

```go
type RunnerTokenEvent struct {
    SandboxID string   `json:"sandbox_id"`
    Action    string   `json:"action"`     // "register" | "remove" | "delete" | "list"
    Repos     []string `json:"repos"`      // for register/remove
    RunnerID  string   `json:"runner_id"`  // for delete
    Repo      string   `json:"repo"`       // for delete/list
}

type RunnerTokenResponse struct {
    Tokens  []TokenEntry `json:"tokens,omitempty"`   // register/remove
    Deleted bool         `json:"deleted,omitempty"`  // delete
    Runners []RunnerInfo `json:"runners,omitempty"`  // list
}

type TokenEntry struct {
    Repo      string `json:"repo"`
    Token     string `json:"token"`
    ExpiresAt string `json:"expires_at"`
}

type RunnerInfo struct {
    ID     int64    `json:"id"`
    Name   string   `json:"name"`
    Status string   `json:"status"`  // "online" | "offline"
    Labels []string `json:"labels"`
}
```

## GitHub App permission expansion

The existing App permissions cover only `contents: read/write`. Runner registration needs `administration: write` (repo-level).

**Operator one-time action:** re-install the App with the expanded permission set. Documented in `docs/github.md` § Runner permissions. `km doctor` adds a check `actions_runner_app_perms` that calls `GET /repos/{owner}/{repo}/actions/runners` against an App-signed request — 403 means the perm is missing, doctor flags it ERROR.

## CLI surface — `km runner`

```
km runner status <sandbox> [--refresh] [--json]
km runner reattach <sandbox> [--repo R | --all-failed]   # default: --all-failed
km runner detach <sandbox> [--repo R]                    # all repos if --repo omitted
```

Sandbox identifier accepts the same formats as other `km` subcommands: full ID (`lrn2-ee9499b5`), alias (`my-runner`), or list-row number.

## Doctor checks

| Check | Severity | Description |
|---|---|---|
| `actions_runner_app_perms` | ERROR | GitHub App missing `administration: write`; runner-enabled sandboxes can't register |
| `actions_runner_register_failures` | WARN | One or more `register@` units in `failed` state on a runner-enabled sandbox |
| `actions_runner_ghosts` | WARN | Runner named `klanker-*` registered on GitHub side with no matching live sandbox in DDB |
| `actions_runner_drift` | WARN | DDB says runner online but `GET /repos/.../actions/runners` doesn't list it (or vice versa) |

WARN severity matches the Phase 67 / 68 precedent for opt-in features: a missing/broken runner shouldn't fail global health.

## Migration / rollout

**Following the Phase 63 / 67 / 68 / 73 / 79 / 80 pattern:**

```bash
make build && make sidecars       # builds km CLI + km-actions-runner sidecar binary
km init --sidecars                # uploads sidecar, refreshes management Lambda userdata template
km init                           # deploys actions-runner-token Lambda + IAM
# One-time: re-install GitHub App with administration:write
km doctor                         # confirms actions_runner_app_perms green
```

Existing sandboxes do NOT get the runner feature retroactively — `km destroy && km create` from a profile with `runner.enabled: true`. Matches every prior sidecar phase.

Docker substrate: out of scope for v1. EC2 substrate only. Same rationale as presence daemon (Phase 79) — Docker can't run systemd cleanly.

## Testing strategy

| Layer | Test |
|---|---|
| `pkg/github/runner` (unit) | `GetRegistrationToken`, `GetRemovalToken`, `DeleteRunner` against `httptest` server. Cases: 201 happy path, 403 perms-missing, 404 repo-not-found, 401 JWT-expired. |
| `pkg/profile/validate` (unit) | `runner.repos ⊆ allowedRepos`, `maxRepos` cap, `learnMode`-incompatible, empty-allowedRepos guard |
| `pkg/compiler` (unit) | Profile with `runner.enabled: true` emits correct userdata fragment (systemd unit enables, `/etc/km/runner-repos.json`) + auto-injects the four new DNS allowlist hosts |
| `cmd/km-actions-runner-token` (unit) | Lambda handler with mocked SSM + mocked GitHub API; per-action dispatch correctness; partial-failure responses |
| `internal/app/cmd/runner` (unit) | DDB read path; `--refresh` GitHub API path; status reconciliation logic |
| Integration (manual) | E2E against a test GitHub repo: create runner-enabled sandbox, push a workflow with `runs-on: [self-hosted, klanker]` and `run: echo hello`, verify job completes, `km destroy`, verify runner gone from GitHub's runners list |
| Doctor (unit) | `actions_runner_register_failures` and `actions_runner_ghosts` against fixtures with mismatched DDB/GitHub state |

The integration test runs manually against a dedicated test org. Matches how Phase 67 Slack integration is tested today.

## Security model

1. **Token scoping:** Registration tokens are scoped to the specific repo at mint time. They expire after ~1 hour and are used once (consumed by `config.sh`). Removal tokens are similar.
2. **Lambda auth:** Function URL uses `AuthType: AWS_IAM`. Only sandboxes with `lambda:InvokeFunctionUrl` on this specific ARN can call. No public internet access.
3. **Workflow runtime policy boundary:** Workflow jobs execute under the sandbox user, subject to the full klanker policy stack — eBPF egress filtering, proxy MITM, allowedDomains, budget caps, audit log. A malicious workflow can't exfiltrate to arbitrary hosts.
4. **No PAT, no long-lived secrets on the sandbox:** Registration tokens transit through the Lambda → sandbox once per registration. They're not persisted. The runner's `.credentials` file (post-`config.sh`) contains a long-lived runner-specific OAuth token, owned by the sandbox user (mode 0600).
5. **Clean deregister:** Belt-and-suspenders `DELETE /actions/runners/{id}` on `km destroy` prevents accumulated ghost runners with stale credentials.
6. **App permission scope:** `administration: write` is repo-level (not org/account), so the blast radius is bounded by which repos the App is installed on.

## Known limitations

- One sandbox can host runners for multiple repos, but **all repos must be on accounts where the same App is installed**. Cross-account repos require multiple App installations (existing pattern).
- Workflow jobs are not isolated from each other on the same sandbox if multiple runners are configured — they share the sandbox filesystem outside the per-runner `_work/` directories. v1 acceptable; document.
- Action cache (`~/_work/_actions/`) is per-runner-directory, not shared. A sandbox running 3 repos pays disk cost 3×. Bounded by maxRepos default of 5.
- `runs-on:` selectors are GitHub-side — a workflow can target `runs-on: self-hosted` and any klanker runner with that label will accept it. Tightening requires repo-specific labels (`klanker-<sandbox-id>` or user-supplied), which workflows must opt into.

## Open questions for plan phase

These are intentionally deferred to GSD plan-phase decisions, not the design:

1. **Runner binary distribution:** Bake `actions/runner` tarball into the sandbox AMI (faster boot, larger AMI) vs. download at boot from `github.com/actions/runner/releases/...` (smaller AMI, slower boot, network dependency). Default expectation: bake into AMI for EC2 substrate.
2. **Lambda cold-start latency:** Token mint is ~500ms cold, ~50ms warm. Acceptable for boot-time registration. Doesn't affect job-running performance (runner has its own credentials post-registration).
3. **`km runner status --refresh` GitHub API cost:** Per-repo round-trip with JWT mint. For sandboxes with many repos and frequent polling, this could chew up App rate limit (5000/h shared). Not a v1 concern but worth noting.

## References

- Existing GitHub App integration: `pkg/github/token.go`, `docs/github.md`
- Sidecar pattern precedents: Phase 63 (Slack notify), Phase 67 (Slack inbound), Phase 68 (transcripts), Phase 73 (VS Code), Phase 79 (presence daemon)
- Lambda + Function URL + sigv4 from sandbox: Phase 67 slack-bridge
- Profile compiler allowlist injection: `pkg/compiler/`
- DDB schema (additive only): `{prefix}-sandboxes` table
- GitHub API: `POST /repos/{owner}/{repo}/actions/runners/registration-token`, `POST .../remove-token`, `DELETE .../runners/{id}`, `GET .../runners`
