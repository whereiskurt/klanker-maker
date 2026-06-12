# GitHub App Integration

Klanker Maker uses a GitHub App to provide sandboxes with scoped, short-lived access tokens for git operations. This replaces personal access tokens (PATs) — tokens are generated per-sandbox, automatically refreshed, and never exposed as environment variables.

## Quick Start

### 1. Create the GitHub App

```bash
km configure github --setup
```

This opens a browser to create a GitHub App via the manifest flow. The App is created as public so it can be installed on multiple accounts. Credentials are stored in SSM automatically. (`km github init/manifest/status` manage the bridge config after the App exists.)

### 2. Install the App

After creation, install the App on the GitHub account(s) that own the repos your sandboxes need:

- Visit `https://github.com/apps/klanker-maker-sandbox`
- Click **Install** and select the target account/org
- Choose which repos to grant access to (or all repos)

Repeat for each account. Then discover the installations:

```bash
km configure github --discover --force
```

This stores a per-account installation key for each account that has the App installed.

### 3. Configure a Profile

```yaml
spec:
  sourceAccess:
    github:
      allowedRepos:
        - my-user/my-repo
        - my-org/another-repo
      allowedRefs:
        - main
        - "release/*"
```

### 4. Create a Sandbox

```bash
km create my-profile.yaml
```

The sandbox gets a scoped GitHub token that can only access the repos and refs listed in the profile.

## How It Works

### Token Lifecycle

Two completely independent loops keep the sandbox able to talk to GitHub: a
**writer loop** (a per-sandbox Lambda fed by EventBridge) and a **reader loop**
(a credential-helper sidecar inside the sandbox itself). They communicate only
through a single per-sandbox SSM SecureString — never directly. The Lambda
never sees the sandbox; the sandbox never holds long-lived GitHub creds.

```
                         GitHub App credentials (one set, shared by all sandboxes)
                         ───────────────────────────────────────────────
                         SSM  /{prefix}/config/github/app-client-id
                              /{prefix}/config/github/private-key   (KMS SecureString)
                              /{prefix}/config/github/installations/<owner>
                                                │
                                                │ ssm:GetParameter
                                                ▼
   ┌──── WRITER LOOP (refresher) ──────────────────────────────────────────────────┐
   │                                                                               │
   │  ┌─────────────────────────────┐  rate(45 minutes)    ┌─────────────────────┐ │
   │  │ EventBridge Scheduler       │ ──────────────────▶  │ {prefix}-github-    │ │
   │  │ {prefix}-github-token-{id}  │   invokes            │ token-refresher-    │ │
   │  │  per-sandbox; payload =     │   with JSON input    │{sandbox-id}         │ │
   │  │   { sandbox_id,             │                      │  (Lambda; Go,       │ │
   │  │     resource_prefix,        │                      │   provided.al2023,  │ │
   │  │     installation_id,        │                      │   arm64, 128MB,     │ │
   │  │     ssm_parameter_name,     │                      │   60s timeout)      │ │
   │  │     kms_key_arn,            │                      └──────────┬──────────┘ │
   │  │     allowed_repos,          │                                 │            │
   │  │     permissions  }          │                                 │            │
   │  └─────────────────────────────┘                                 │ 1. mint    │
   │                                                                  │   RS256    │
   │                                                                  │   JWT      │
   │                                                                  │ 2. POST    │
   │                                                                  ▼   GitHub   │
   │                                                            api.github.com     │
   │                                                            /app/installations/│
   │                                                            {id}/access_tokens │
   │                                                            (scoped to         │
   │                                                             allowed_repos +   │
   │                                                             permissions)      │
   │                                                                  │            │
   │                                                                  │ 3. PutParam│
   │                                                                  │   Secure-  │
   │                                                                  │   String   │
   │                                                                  ▼            │
   │                                            ┌───────────────────────────────┐  │
   │                                            │ SSM (per-sandbox KMS key)     │  │
   │                                            │ /sandbox/{id}/github-token    │  │
   │                                            │   alias/{prefix}-github-      │  │
   │                                            │   token-{sandbox-id}          │  │
   │                                            └───────────────┬───────────────┘  │
   └────────────────────────────────────────────────────────────│──────────────────┘
                                                                │
                                                                │ ssm:GetParameter
                                                                │  --with-decryption
                                                                │ (sandbox IAM role
                                                                │  scoped to its own
                                                                │  ARN ONLY)
                                                                ▼
   ┌──── READER LOOP (sandbox-side sidecar) ─────────────────────────────────────┐
   │                                                                             │
   │   Sandbox EC2 / Docker                                                      │
   │   ┌────────────────────────────────────────────────────────────────────┐    │
   │   │ git push / clone / fetch / pull                                    │    │
   │   │   │                                                                │    │
   │   │   ├─ via GIT_ASKPASS  ────▶ /opt/km/bin/km-git-askpass             │    │
   │   │   │                          (interactive shells, plain git)       │    │
   │   │   │                                                                │    │
   │   │   └─ via credential.helper ▶ /opt/km/bin/km-git-credential-helper  │    │
   │   │                              (Claude Code clears GIT_ASKPASS;      │    │
   │   │                               core.askpass is bypassed, so a       │    │
   │   │                               separate helper handles that path)   │    │
   │   │                                                                    │    │
   │   │     both shell scripts → aws ssm get-parameter                     │    │
   │   │                          --name /sandbox/{id}/github-token         │    │
   │   │                          --with-decryption                         │    │
   │   │     →  username=x-access-token                                     │    │
   │   │        password=<fresh installation token>                         │    │
   │   └────────────────────────────────────────────────────────────────────┘    │
   │                                  │ HTTPS (intercepted)                      │
   │                                  ▼                                          │
   │   ┌────────────────────────────────────────────────────────────────────┐    │
   │   │ http-proxy sidecar (MITM)                                          │    │
   │   │   - allow-list: github.com, api.github.com,                        │    │
   │   │     raw.githubusercontent.com, codeload.githubusercontent.com      │    │
   │   │   - URL filter: allowed_repos enforced at the request line         │    │
   │   │   - pre-push hook (separate): blocks refs outside allowedRefs      │    │
   │   └─────────────────────────────────┬──────────────────────────────────┘    │
   │                                     │ HTTPS                                 │
   └─────────────────────────────────────│───────────────────────────────────────┘
                                         ▼
                                  ┌────────────┐
                                  │   GitHub   │
                                  └────────────┘
```

GitHub installation tokens expire after 1 hour. The 45-minute refresh cycle
gives a ~15-minute overlap buffer — a sandbox that wakes up immediately after a
refresh has the full hour ahead of it, and any in-flight clone that holds a
nearly-expired token completes long before the next refresh writes the new one.

**Why two paths read from SSM, not one:** Claude Code launches `git` with
`GIT_TERMINAL_PROMPT=0` and clears `GIT_ASKPASS`, which makes
`core.askpass` ineffective. `credential.helper` is a separate code path that
Claude Code does not override, so `km-git-credential-helper` covers the agent's
git operations while `km-git-askpass` covers everything else (interactive
shells, scripts, `claude shell` sessions).

### Credential Helper (km-git-askpass)

Inside the sandbox, git operations use a credential helper at `/opt/km/bin/km-git-askpass`:

```bash
#!/bin/bash
TOKEN=$(aws ssm get-parameter \
  --name "/sandbox/${SANDBOX_ID}/github-token" \
  --with-decryption \
  --query "Parameter.Value" \
  --output text 2>/dev/null || echo "")
case "$1" in
  *Username*) echo "x-access-token" ;;
  *Password*) echo "$TOKEN" ;;
esac
```

The token is fetched from SSM at git-operation time via `GIT_ASKPASS`. It is never exported as an environment variable, so it can't leak through `env`, `/proc`, or process listings.

A second sidecar script, `/opt/km/bin/km-git-credential-helper`, is wired into
`git config --system credential.helper`. It exists because Claude Code clears
`GIT_ASKPASS` and bypasses `core.askpass`; `credential.helper` is a separate
git code path that the agent does not override. Both scripts read the SAME
per-sandbox SSM parameter — there is only ever one live token per sandbox.

### Ref Enforcement (Pre-push Hook)

When `allowedRefs` is set in the profile, a pre-push hook blocks pushes to unlisted branches:

```bash
# Profile with allowedRefs: ["main", "release/*"]

git push origin main          # allowed
git push origin release/v2    # allowed
git push origin dev           # blocked: "[km] Push to 'dev' denied"
```

The hook uses bash pattern matching, so wildcards like `release/*` and `v*` work.

## Multi-Account Installations

A single GitHub App can be installed on multiple GitHub accounts/orgs. Each installation gets its own ID, and Klanker Maker stores them separately in SSM.

### SSM Layout

```
/km/config/github/app-client-id                         # shared App credentials
/km/config/github/private-key                            # shared App PEM key
/km/config/github/installations/my-user                  # installation ID for my-user
/km/config/github/installations/my-org                   # installation ID for my-org
/km/config/github/installation-id                        # legacy (first installation, backward compat)
```

### Resolution at Create Time

When `km create` processes a profile with `allowedRepos: ["my-user/my-repo"]`:

1. Extracts owner `my-user` from the repo entry
2. Looks up `/km/config/github/installations/my-user` in SSM
3. Uses that installation ID to generate a scoped token
4. If per-account key not found, falls back to legacy `/km/config/github/installation-id`

This means sandboxes accessing `my-user` repos and sandboxes accessing `my-org` repos each get tokens from the correct installation automatically.

### Managing Installations

**Discover all installations:**

```bash
km configure github --discover --force
```

Fetches all installations from the GitHub API and stores per-account keys for each.

**Add a single installation manually:**

```bash
km configure github \
  --installation-id 12345678 \
  --account my-org \
  --app-client-id Iv1.abc123 \
  --private-key-file app.pem \
  --non-interactive --force
```

**View current installations:**

```bash
km doctor
```

The GitHub health check reports all per-account installations found.

## Configuration Reference

### km github / km configure github

| Flag | Description |
|------|-------------|
| `--setup` | One-click App creation via manifest flow (opens browser) |
| `--discover` | Auto-discover installation IDs from existing App credentials |
| `--installation-id ID` | Manually specify an installation ID |
| `--account LOGIN` | GitHub account/org login (used with `--installation-id`) |
| `--app-client-id ID` | GitHub App client ID |
| `--private-key-file PATH` | Path to App private key PEM file |
| `--force` | Overwrite existing SSM parameters |
| `--non-interactive` | Skip prompts, use flag values directly |

### Profile Fields

```yaml
spec:
  sourceAccess:
    github:
      allowedRepos:    # Required. Repos the sandbox can access.
        - owner/repo   # Single repo
        - org/*         # All repos in org (wildcard)
        - "*"           # All repos the installation can access
      allowedRefs:     # Optional. Refs allowed for push.
        - main         # Exact branch name
        - "release/*"  # Wildcard pattern
```

**Permissions** are derived from the profile:

| Profile Permission | GitHub API Permission |
|--------------------|----------------------|
| `clone`, `fetch` | `contents: read` |
| `push` | `contents: write` |

### Token Refresher Lambda

| Setting | Value |
|---------|-------|
| Runtime | `provided.al2023` (Go, ARM64) |
| Memory | 128 MB |
| Timeout | 60 seconds |
| Schedule | Every 45 minutes |
| Log retention | 30 days |

The Lambda receives a per-sandbox event payload containing `sandbox_id`, `installation_id`, `allowed_repos`, and `permissions`. It generates a fresh token and writes it to the sandbox's SSM parameter.

## Security Model

1. **Token scoping**: Each token is scoped to the specific repos and permissions in the profile. A sandbox with `allowedRepos: ["org/frontend"]` cannot access `org/backend`.

2. **No credential exposure**: The askpass helper fetches tokens from SSM on demand. No token is stored in environment variables, `.gitconfig`, or on disk.

3. **Per-sandbox encryption**: Each sandbox's token is encrypted with a unique KMS key (`km-github-token-{sandbox-id}`). Other sandboxes cannot decrypt it.

4. **Ref enforcement**: Pre-push hooks block writes to branches not in `allowedRefs`. This is enforced client-side via `core.hooksPath`.

5. **Network enforcement**: The HTTP proxy sidecar only allows traffic to GitHub hosts when `sourceAccess.github` is configured. MITM inspection enforces repo-level URL filtering at the network layer.

6. **Short-lived tokens**: Installation tokens expire after 1 hour. Even if a token leaks, the blast radius is time-limited.

7. **App is public**: The GitHub App is created as public so it can be installed across multiple accounts. This does not expose credentials — the App's private key remains in SSM, and installation requires explicit authorization by each account owner.

## Troubleshooting

**"ErrGitHubNotConfigured" during km create:**
Run `km github` to create and configure the App, then `km configure github --discover --force`.

**Token refresh failures (sandbox loses git access after ~1 hour):**
Check the Lambda logs: `km otel <sandbox-id> --events`. Common causes:
- Lambda's IAM role can't read `/km/config/github/*` SSM params
- GitHub App private key rotated but SSM not updated
- Installation was removed from the GitHub account

**"Push to 'branch' denied" errors:**
The profile's `allowedRefs` doesn't include the target branch. Update the profile or push to an allowed branch.

**Discover shows only one installation:**
Make sure the App is installed on the other account (not just created — you need to click Install from `https://github.com/apps/klanker-maker-sandbox`). The App must be public for cross-account installation.

**km doctor reports GitHub not configured:**
Either no installations exist, or SSM params are missing. Run `km configure github --discover --force` to re-sync.
