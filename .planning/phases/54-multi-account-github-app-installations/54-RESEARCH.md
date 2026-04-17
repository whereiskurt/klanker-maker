# Phase 54 Research: Multi-account GitHub App Installations

## Problem

The `km github` (aka `km configure github --setup`) flow creates a GitHub App and stores a single installation ID at `/km/config/github/installation-id` in SSM. When a second GitHub user installs the same App on their account, running `--discover` or `--installation-id` overwrites the first user's ID. Only one user's repos can be accessed at a time.

## Current Architecture

### Storage (SSM Parameter Store)
```
/km/config/github/app-client-id       — String (shared, one App)
/km/config/github/private-key         — SecureString (shared, one App)
/km/config/github/installation-id     — String (SINGLE installation — the problem)
```

### Key Files
- `internal/app/cmd/configure_github.go` — setup/discover/manual flows, all write single installation-id
- `internal/app/cmd/create.go:1791` — reads `/km/config/github/installation-id` at sandbox create time
- `pkg/github/token.go` — JWT generation, token exchange (already takes installationID as param)
- `internal/app/cmd/doctor.go:408` — health check validates single installation-id exists
- `cmd/github-token-refresher/main.go` — Lambda receives installation_id per-event (already multi-install aware)

### Token Flow at Create Time (create.go ~line 1780)
1. Read app-client-id from SSM
2. Read private-key from SSM
3. Read **single** installation-id from SSM  ← bottleneck
4. Generate JWT from app-client-id + private-key
5. Exchange JWT for scoped installation token (repos + perms from profile)
6. Write token to `/sandbox/{id}/github-token`

### Discover Flow (configure_github.go:238)
1. Fetches ALL installations from GitHub API via `/app/installations`
2. Displays them all but **stores only the first one** ← problem

## Proposed Solution

### New SSM Layout
```
/km/config/github/app-client-id                    — unchanged
/km/config/github/private-key                      — unchanged
/km/config/github/installation-id                  — DEPRECATED (keep for backward compat)
/km/config/github/installations/{account-login}    — NEW, one per installed account
```

Example:
```
/km/config/github/installations/userA    → "12345678"
/km/config/github/installations/userB    → "87654321"
```

### Resolution at Create Time
1. Profile specifies repos like `userA/my-repo`, `userB/other-repo`
2. Extract unique owners from repo list
3. For each owner, look up `/km/config/github/installations/{owner}`
4. If not found, fall back to legacy `/km/config/github/installation-id`
5. Generate separate tokens per installation (each scoped to that installation's repos)
6. If a sandbox needs repos from multiple installations, generate multiple tokens

**Simplification:** Most sandboxes access repos from a single owner. For v1, we can require all repos in a profile to share one owner (or use wildcard `*`), and generate one token from the matching installation. Multi-owner support can be a follow-up.

### Changes Required

#### Plan 1: SSM Storage Migration
- `configure_github.go` — setup flow writes per-account keys for all discovered installations
- `configure_github.go` — discover flow writes ALL installations, not just first
- `configure_github.go` — manual `--installation-id` flow adds `--account` flag
- Backward compat: continue writing legacy key with first/only installation for old code paths

#### Plan 2: Create-time Resolution
- `create.go` — `generateAndStoreGitHubToken` extracts owner from repos, looks up per-account SSM key
- Fall back to legacy single key if per-account not found
- Error clearly if owner has no installation ("GitHub App not installed on account 'userB'")

#### Plan 3: Doctor + Lambda + Tests
- `doctor.go` — check for at least one installation (per-account or legacy)
- Token refresher Lambda already receives installation_id per-event — no change needed
- Integration tests for multi-installation discover, create with different owners

## Edge Cases
- Repo format `org/repo` vs bare `repo` — use org prefix; bare repos fall back to legacy
- Wildcard `*` in repos — use legacy single installation or require `--account` flag
- GitHub org vs user account — both work the same way via installations API
- App installed then uninstalled — discover will remove stale entries; create will fail with clear error from GitHub API
