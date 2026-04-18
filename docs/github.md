# GitHub App Integration

Klanker Maker uses a GitHub App to provide sandboxes with scoped, short-lived access tokens for git operations. This replaces personal access tokens (PATs) — tokens are generated per-sandbox, automatically refreshed, and never exposed as environment variables.

## Quick Start

### 1. Create the GitHub App

```bash
km github
```

This opens a browser to create a GitHub App via the manifest flow. The App is created as public so it can be installed on multiple accounts. Credentials are stored in SSM automatically.

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

```
km create
  |
  +-- Read App credentials from SSM
  |     /km/config/github/app-client-id
  |     /km/config/github/private-key
  |
  +-- Resolve installation ID
  |     Extract owner from allowedRepos (e.g., "my-user" from "my-user/my-repo")
  |     Look up /km/config/github/installations/my-user
  |     Fall back to /km/config/github/installation-id (legacy)
  |
  +-- Generate scoped installation token
  |     POST /app/installations/{id}/access_tokens
  |     Scoped to allowedRepos + permissions (read or write)
  |
  +-- Write token to SSM
  |     /sandbox/{sandbox-id}/github-token (SecureString, per-sandbox KMS key)
  |
  +-- Deploy token refresher
        EventBridge schedule: every 45 minutes
        Lambda reads App credentials, generates new token, writes to SSM
```

GitHub installation tokens expire after 1 hour. The 45-minute refresh cycle ensures continuous access with a 15-minute overlap buffer.

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
