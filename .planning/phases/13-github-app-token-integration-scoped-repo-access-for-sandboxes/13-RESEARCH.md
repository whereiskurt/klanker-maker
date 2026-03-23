# Phase 13: GitHub App Token Integration — Research

**Researched:** 2026-03-22
**Domain:** GitHub App authentication, JWT generation, SSM Parameter Store token lifecycle, Lambda token refresh, Go AWS SDK v2, GIT_ASKPASS credential helper
**Confidence:** HIGH (GitHub App API verified from official docs; SSM/Lambda patterns verified from existing codebase and AWS SDK v2 docs; GIT_ASKPASS pattern verified from official git documentation)

---

## Summary

Phase 13 adds short-lived, repo-scoped GitHub App installation tokens to sandbox provisioning. When a profile includes `sourceAccess.github`, the `km create` flow generates a GitHub App installation token at sandbox creation time, stores it in SSM Parameter Store at a per-sandbox path, and deploys a Lambda (`km-github-token-refresher-{sandbox-id}`) that refreshes the token every 45 minutes via EventBridge Scheduler. The sandbox reads the token at git-operation time via a `GIT_ASKPASS` credential helper script that calls SSM — the token never touches environment variables or user-data.

The `km configure github` subcommand (a new sub-tree under the existing `configure` cobra command) stores the GitHub App ID, private key PEM, and installation ID in SSM at well-known operator-level paths (`/km/config/github/app-id`, `/km/config/github/private-key`, `/km/config/github/installation-id`). At `km create` time these are read, a JWT is minted (RS256, 10-minute expiry), the JWT is exchanged for an installation access token via `POST /app/installations/{id}/access_tokens` with repository and permission scoping derived from `sourceAccess.github.allowedRepos`, and the resulting token is written to `/sandbox/{sandbox-id}/github-token` in SSM (SecureString, per-sandbox KMS key reusing the existing secrets module KMS key pattern).

The implementation follows the budget-enforcer module pattern exactly: a new `infra/modules/github-token/v1.0.0/` Terraform module encapsulates the Lambda + EventBridge Scheduler + SSM IAM; the compiler emits a `github_token_inputs` block in service.hcl when `sourceAccess.github` is set; `km create` applies a sibling `github-token/` Terragrunt directory after main sandbox provisioning; `km destroy` cleans up the SSM parameter, EventBridge schedule, and Lambda before the main sandbox destroy.

**Primary recommendation:** Follow the budget-enforcer module pattern (per-sandbox Lambda + EventBridge Scheduler + per-sandbox IAM) for the token refresher. Use `golang-jwt/jwt/v5` with RS256 for GitHub JWT generation. Store the GitHub private key in SSM SecureString under a global `/km/config/` prefix, not in km-config.yaml. The `GIT_ASKPASS` script reads SSM at git-operation time — no token in userdata or environment.

---

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `golang-jwt/jwt/v5` | v5.x (latest) | GitHub App JWT generation (RS256) | The maintained successor to dgrijalva/jwt-go; v5 has breaking API improvements and is actively maintained; GitHub App requires RS256 |
| `net/http` (stdlib) | Go stdlib | HTTP call to GitHub API for token exchange | No external HTTP client needed; standard library is sufficient for a single POST |
| `aws-sdk-go-v2/service/ssm` | v1.68.3 (already in go.mod) | PutParameter/GetParameter for token storage | Already imported in this repo |
| `aws-sdk-go-v2/service/scheduler` | v1.17.21 (already in go.mod) | EventBridge Scheduler create/delete | Already imported; same pattern as TTL and budget-enforcer schedules |
| `aws-lambda-go` | v1.53.0 (already in go.mod) | Lambda runtime for token refresher | Already imported for budget-enforcer |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `crypto/rsa` + `crypto/x509` (stdlib) | Go stdlib | Parse PEM private key for JWT signing | No external library needed to decode PKCS#1/PKCS#8 PEM |
| `encoding/pem` (stdlib) | Go stdlib | PEM block decode before x509 parse | Part of standard Go crypto toolkit |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `golang-jwt/jwt/v5` | `google/go-github` JWT helper | go-github bundles JWT generation but adds a large transitive dependency for a single function; jwt/v5 is purpose-built and already common in Go ecosystems |
| SSM Parameter Store | AWS Secrets Manager | Secrets Manager has automatic rotation built-in but costs more per secret and is harder to reference from Lambda + credential helper; SSM is already the pattern for all other secrets in this repo |
| EventBridge Scheduler | EventBridge cron rule | Scheduler supports per-target flexible windows and is already used for budget-enforcer and TTL; consistent pattern |

**Installation:**
```bash
go get github.com/golang-jwt/jwt/v5
```

---

## Architecture Patterns

### Recommended Project Structure

```
cmd/github-token-refresher/
└── main.go              # Lambda entry point (same pattern as cmd/budget-enforcer/main.go)

pkg/github/
├── token.go             # GenerateJWT(), ExchangeForInstallationToken(), WriteTokenToSSM()
└── token_test.go        # unit tests (stub HTTP server for GitHub API)

infra/modules/github-token/
└── v1.0.0/
    ├── main.tf           # Lambda + IAM + EventBridge Scheduler
    ├── variables.tf
    └── outputs.tf

internal/app/cmd/
└── configure_github.go   # km configure github subcommand
```

### Pattern 1: GitHub App JWT → Installation Token Exchange

**What:** Two-step authentication. First mint a short-lived JWT signed with the App's RSA private key (10-minute max). Use that JWT to call `POST /app/installations/{id}/access_tokens` with repo and permission scoping. The response contains the installation token (1-hour expiry).

**When to use:** Any time a new token is needed: at sandbox creation and every 45-minute refresh cycle.

**JWT Claims (verified from GitHub docs):**
- `iss`: GitHub App client ID (or application ID)
- `iat`: `time.Now().Add(-60 * time.Second).Unix()` — 60 seconds in the past to absorb clock drift
- `exp`: `time.Now().Add(10 * time.Minute).Unix()` — maximum allowed by GitHub

**Example:**
```go
// Source: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app
// and pkg.go.dev/github.com/golang-jwt/jwt/v5

import (
    "crypto/x509"
    "encoding/pem"
    "github.com/golang-jwt/jwt/v5"
    "time"
)

func GenerateGitHubAppJWT(appClientID string, privateKeyPEM []byte) (string, error) {
    block, _ := pem.Decode(privateKeyPEM)
    if block == nil {
        return "", fmt.Errorf("failed to decode PEM block")
    }
    privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
    if err != nil {
        // Try PKCS#8 (newer GitHub App key format)
        key, err2 := x509.ParsePKCS8PrivateKey(block.Bytes)
        if err2 != nil {
            return "", fmt.Errorf("parse private key: %w", err)
        }
        var ok bool
        privateKey, ok = key.(*rsa.PrivateKey)
        if !ok {
            return "", fmt.Errorf("private key is not RSA")
        }
    }
    now := time.Now()
    claims := jwt.RegisteredClaims{
        IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)),
        ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
        Issuer:    appClientID,
    }
    token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
    return token.SignedString(privateKey)
}
```

### Pattern 2: Installation Token Request with Repo + Permission Scoping

**What:** POST to GitHub API with the JWT, requesting a token scoped to specific repos and permissions.

**Permission mapping** from `sourceAccess.github.allowedRepos[].permissions`:
- `clone` or `fetch` → `"contents": "read"`
- `push` → `"contents": "write"` (supersedes read)

**Example:**
```go
// Source: https://docs.github.com/en/rest/apps/installations#create-an-installation-access-token-for-an-app

type tokenRequest struct {
    Repositories []string          `json:"repositories,omitempty"` // repo names without org prefix
    Permissions  map[string]string `json:"permissions,omitempty"`
}

type tokenResponse struct {
    Token     string    `json:"token"`
    ExpiresAt time.Time `json:"expires_at"`
}

func ExchangeForInstallationToken(ctx context.Context, jwt string, installationID string, repos []string, perms map[string]string) (string, error) {
    body, _ := json.Marshal(tokenRequest{Repositories: repos, Permissions: perms})
    req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
        fmt.Sprintf("https://api.github.com/app/installations/%s/access_tokens", installationID),
        bytes.NewReader(body))
    req.Header.Set("Authorization", "Bearer "+jwt)
    req.Header.Set("Accept", "application/vnd.github+json")
    req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
    // ... handle response, unmarshal tokenResponse
}
```

### Pattern 3: SSM Parameter Storage (per-sandbox path)

**Path:** `/sandbox/{sandbox-id}/github-token`

**SSM type:** `SecureString` encrypted with the per-sandbox KMS key (same key already provisioned by the existing `secrets` Terraform module).

**Write (at km create and Lambda refresh):**
```go
// Uses existing aws-sdk-go-v2/service/ssm — already imported in pkg/aws
_, err = ssmClient.PutParameter(ctx, &ssm.PutParameterInput{
    Name:      aws.String("/sandbox/" + sandboxID + "/github-token"),
    Value:     aws.String(token),
    Type:      types.ParameterTypeSecureString,
    KeyId:     aws.String(kmsKeyARN),
    Overwrite: aws.Bool(true), // refresh path requires overwrite
})
```

**Read (SSM client already exists in the codebase):** The existing `aws ssm get-parameter --with-decryption` call pattern in userdata.go already handles this. No new Go SSM read code needed in the sandbox; the credential helper script handles it.

### Pattern 4: GIT_ASKPASS Credential Helper (EC2)

**What:** A shell script set as `GIT_ASKPASS` reads the token from SSM at git-operation time. Git invokes the script with a prompt string ("Username" or "Password"); the script echoes the token as the password and `x-access-token` as the username.

**Why GIT_ASKPASS over environment variable:** Token never appears in `ps aux`, `/proc/PID/environ`, or CloudTrail userdata logs. The token is fetched fresh from SSM on each git operation, so short-lived nature is preserved even without Lambda refresh.

**Script injected by userdata.go template:**
```bash
# Written to /opt/km/bin/km-git-askpass (not executable from userdata env directly)
#!/bin/bash
# Called by git with a prompt string on $1
# Prompt contains "Username" or "Password"
TOKEN=$(aws ssm get-parameter \
  --name "/sandbox/${SANDBOX_ID}/github-token" \
  --with-decryption \
  --query "Parameter.Value" \
  --output text 2>/dev/null)
case "$1" in
  *Username*) echo "x-access-token" ;;
  *Password*) echo "$TOKEN" ;;
esac
```

**Environment variable in bootstrap (not the token itself):**
```bash
export GIT_ASKPASS=/opt/km/bin/km-git-askpass
```

**ECS containers:** Inject via `environment` block in the task definition container spec. The credential helper script is baked into the sandbox image or injected as a volume mount. Token is read from SSM at runtime using the task role.

### Pattern 5: Lambda Token Refresher (budget-enforcer pattern)

**Structure:** Identical to `cmd/budget-enforcer/main.go`. Lambda receives an EventBridge payload with `sandbox_id`, `installation_id`, `ssm_parameter_name`, `kms_key_arn`, `allowed_repos`, `permissions`. It re-reads the private key from SSM (`/km/config/github/private-key`), mints a new JWT, exchanges for a token, writes the token to SSM.

**Failure mode:** Non-fatal. If refresh fails, log warning to CloudWatch. The existing token continues working until its 1-hour expiry. The 45-minute schedule leaves 15 minutes of buffer.

**EventBridge Scheduler:** `rate(45 minutes)` — matches the description. Same `aws_scheduler_schedule` + `aws_iam_role` pattern used for budget-enforcer.

### Pattern 6: km configure github Subcommand

**What:** A Cobra subcommand `km configure github` that prompts for App ID, Client ID, private key PEM path, and installation ID, then writes them to SSM under `/km/config/github/`.

**Integration:** Adds a `NewConfigureGitHubCmd` function that is registered as a subcommand of `configure` in `root.go`. Follows the same `io.Reader`/`io.Writer` injection pattern as `configure.go` for testability.

**SSM paths written:**
- `/km/config/github/app-client-id` (String) — GitHub App client ID (used as JWT issuer)
- `/km/config/github/private-key` (SecureString, KMS-encrypted) — RSA private key PEM
- `/km/config/github/installation-id` (String) — Installation ID for the org/account

**Why SSM not km-config.yaml:** Private key must not be in a YAML file that operators might commit. SSM SecureString with KMS satisfies the "encrypted at rest" requirement and follows the existing secrets module pattern.

### Pattern 7: Compiler Emission (github_token_inputs block)

**When:** `p.Spec.SourceAccess.GitHub != nil`

**What the compiler emits** (appended to service.hcl after `budget_enforcer_inputs`):
```hcl
  # GitHub token refresher: Lambda + EventBridge every 45 minutes
  # Requires sourceAccess.github to be set in the profile.
  github_token_inputs = {
    sandbox_id        = "{{ .SandboxID }}"
    ssm_parameter_name = "/sandbox/{{ .SandboxID }}/github-token"
    kms_key_arn       = "" # populated at apply time from secrets module output
    allowed_repos     = [{{ joinStrings .GitHubAllowedRepos }}]
    permissions       = {{ .GitHubPermissions }} # "contents:read" or "contents:write"
    installation_id   = "" # populated at apply time from SSM /km/config/github/installation-id
  }
```

**km create applies** the `github-token/` Terragrunt directory after the main sandbox apply (Step 15, following the budget-enforcer Step 12c pattern).

### Pattern 8: Ref Enforcement (defense in depth)

**Primary control:** GitHub App token is scoped to specific repos at issuance time. Repo not in `allowedRepos` → token rejected by GitHub.

**Secondary control (ref enforcement):** A wrapper script around `git push` checks the target ref against `allowedRepos[].refs`. If the ref is not allowed, the push is blocked before the GitHub API call. This is a local enforcement layer — it does not replace GitHub App scoping.

**Implementation for EC2:** The `GIT_ASKPASS` helper can refuse to provide credentials for disallowed refs by checking `GIT_PUSH_OPTION_COUNT` or by wrapping `git` itself in a thin shell script that validates refs before calling the real `git`.

**Simpler approach (recommended for v1):** Ref enforcement via `git config receive.denyNonFastForwards` and branch protection at the GitHub level. The per-sandbox token's repo scoping is the primary control. Ref checking in the credential helper adds complexity for limited security gain if GitHub App permissions are tightly scoped.

### Anti-Patterns to Avoid

- **Token in userdata:** Never pass the GitHub token as a userdata variable or environment variable. Use SSM + GIT_ASKPASS.
- **Long-lived private key in km-config.yaml:** The App private key must go in SSM SecureString, not a YAML file.
- **Single SSM path for all sandboxes:** Each sandbox gets `/sandbox/{sandbox-id}/github-token` — not a shared path. Isolation is critical.
- **GITHUB_TOKEN env var:** The existing userdata.go stub (section 4) exports `GITHUB_TOKEN` to the environment. This must be removed/replaced with the `GIT_ASKPASS` approach in Phase 13.
- **JWT expiry > 10 minutes:** GitHub enforces a 10-minute maximum for GitHub App JWTs. Setting a longer expiry causes token exchange to fail.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| JWT RS256 signing | Custom HMAC or crypto code | `golang-jwt/jwt/v5` | PEM parsing, RSA signing, and claim validation have well-known edge cases (clock skew, PKCS#1 vs PKCS#8 key formats) |
| HTTP retry with backoff for GitHub API | Custom retry loop | Standard `net/http` with simple retry (≤3 attempts) | GitHub rate limits are generous for installation token generation; over-engineering retry logic adds bugs |
| Token expiry tracking | In-memory expiry cache | EventBridge Scheduler 45-minute rate | Lambda is stateless; the schedule is the clock |
| SSM parameter cleanup | Custom tag-based scanner | Explicit delete in `km destroy` | Tag-based scanners miss orphan parameters; explicit delete by known path is reliable |

**Key insight:** The GitHub App token flow (JWT → installation token) has two clearly separable operations. Keep them in separate functions so the JWT generation can be unit-tested with a fake HTTP server.

---

## Common Pitfalls

### Pitfall 1: PKCS#1 vs PKCS#8 Private Key Format

**What goes wrong:** GitHub App private keys downloaded from the GitHub UI are PKCS#1 format (header `-----BEGIN RSA PRIVATE KEY-----`). Some tooling generates PKCS#8 (header `-----BEGIN PRIVATE KEY-----`). `x509.ParsePKCS1PrivateKey` fails silently on PKCS#8.

**Why it happens:** Go's `crypto/x509` has two separate parse functions; callers must try both.

**How to avoid:** Attempt `ParsePKCS1PrivateKey` first; on failure, try `ParsePKCS8PrivateKey` and assert the result to `*rsa.PrivateKey`. Document which format `km configure github` accepts.

**Warning signs:** "asn1: structure error" in Lambda logs during JWT generation.

### Pitfall 2: Clock Drift Causing JWT Rejection

**What goes wrong:** GitHub's servers reject JWTs where `iat` is in the future relative to GitHub's clock. Lambda execution environments can have minor clock drift.

**Why it happens:** Lambda containers may have stale time synchronization. GitHub requires `iat` to be in the past.

**How to avoid:** Set `iat = time.Now().Add(-60 * time.Second)` as documented in GitHub's official guidance. Do not set `iat = time.Now()`.

**Warning signs:** 401 from GitHub API with message about JWT not yet valid.

### Pitfall 3: SSM Overwrite on Refresh Requires Overwrite=true

**What goes wrong:** `PutParameter` with `Overwrite: false` (the default) fails with `ParameterAlreadyExists` on the second write.

**Why it happens:** SSM's default is to reject overwrites to prevent accidental value replacement.

**How to avoid:** Lambda token refresher must pass `Overwrite: aws.Bool(true)`. Initial write at `km create` can use `false` as a sanity check (if the parameter already exists, something is wrong).

**Warning signs:** Lambda refresh logs `ParameterAlreadyExists` error; sandbox continues with stale token until 1-hour GitHub expiry.

### Pitfall 4: Existing userdata.go GITHUB_TOKEN Section Must Be Replaced

**What goes wrong:** The existing userdata.go template section 4 (`{{- if .HasGitHub }}`) exports `GITHUB_TOKEN` as a shell environment variable. This leaks the token into the process environment of the sandbox.

**Why it happens:** Section 4 was a placeholder stub from earlier phases before Phase 13 defined the proper GIT_ASKPASS approach.

**How to avoid:** Phase 13 replaces section 4 with: writing the `km-git-askpass` script to `/opt/km/bin/`, setting it executable, and exporting `GIT_ASKPASS=/opt/km/bin/km-git-askpass`. The token is never exported as an environment variable.

**Warning signs:** `GITHUB_TOKEN` visible in `/proc/1/environ` inside the sandbox.

### Pitfall 5: GitHub API Repository Name Format

**What goes wrong:** The `repositories` field in the token request takes repo names without the org prefix (e.g. `"my-repo"` not `"myorg/my-repo"`). Passing full `org/repo` format causes a 422 validation error.

**Why it happens:** The `repository_ids` field takes numeric IDs; the `repositories` field takes short names. The profile uses the `org/repo` convention.

**How to avoid:** Strip the org prefix from `allowedRepos` values before constructing the token request. The installation ID already scopes the token to the correct org.

**Warning signs:** 422 Unprocessable Entity from GitHub API token exchange.

### Pitfall 6: SCP Must Allow github-token-refresher Lambda (Phase 10 Dependency)

**What goes wrong:** The SCP from Phase 10 carves out named km system roles from Deny statements. The new `km-github-token-refresher-{sandbox-id}` Lambda role must be added to the carve-out lists for SSM and IAM statements.

**Why it happens:** SCP Deny statements match by role ARN pattern. A new Lambda role is denied by default.

**How to avoid:** The SCP carve-out locals (established in Phase 10) use wildcard patterns like `km-budget-enforcer-*`. Either extend the pattern to also match `km-github-token-refresher-*`, or add a new carve-out entry. Verify in the SCP module's `trusted_arns_ssm` local.

**Warning signs:** Lambda execution fails with `AccessDeniedException` from SSM API call in CloudWatch logs.

### Pitfall 7: KMS Key ARN Threading

**What goes wrong:** The `github-token` Terraform module needs the per-sandbox KMS key ARN to write a SecureString parameter. The KMS key is provisioned by the `secrets` module. If the sandbox doesn't use the secrets module (e.g. no `identity.allowedSecretPaths`), there may be no KMS key.

**Why it happens:** The secrets module creates a per-sandbox KMS key only when `identity.allowedSecretPaths` is set. A sandbox with GitHub access but no other secrets would lack the key.

**How to avoid:** The `github-token` module creates its own KMS key for the GitHub token parameter, independent of the secrets module key. This ensures isolation: the GitHub token KMS key has a narrow key policy allowing only the token refresher Lambda and sandbox IAM role to decrypt. Follow the pattern from `infra/modules/secrets/v1.0.0/main.tf` (a per-sandbox KMS key with `aws_iam_policy_document` key policy).

---

## Code Examples

### GitHub App JWT Generation (complete pattern)

```go
// pkg/github/token.go
// Source: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app

func GenerateGitHubAppJWT(appClientID string, privateKeyPEM []byte) (string, error) {
    block, _ := pem.Decode(privateKeyPEM)
    if block == nil {
        return "", fmt.Errorf("no PEM block found in private key")
    }
    var rsaKey *rsa.PrivateKey
    // Try PKCS#1 first (GitHub UI format)
    if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
        rsaKey = k
    } else {
        // Fall back to PKCS#8
        k8, err := x509.ParsePKCS8PrivateKey(block.Bytes)
        if err != nil {
            return "", fmt.Errorf("parse RSA private key (tried PKCS#1 and PKCS#8): %w", err)
        }
        var ok bool
        rsaKey, ok = k8.(*rsa.PrivateKey)
        if !ok {
            return "", fmt.Errorf("private key is not RSA")
        }
    }
    now := time.Now()
    claims := jwt.RegisteredClaims{
        Issuer:    appClientID,
        IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)), // clock drift buffer
        ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),  // GitHub max
    }
    return jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(rsaKey)
}
```

### Installation Token Exchange with Repo Scoping

```go
// pkg/github/token.go
// Source: https://docs.github.com/en/rest/apps/installations#create-an-installation-access-token-for-an-app

type installationTokenRequest struct {
    Repositories []string          `json:"repositories,omitempty"`
    Permissions  map[string]string `json:"permissions,omitempty"`
}

type installationTokenResponse struct {
    Token     string    `json:"token"`
    ExpiresAt time.Time `json:"expires_at"`
}

// repoShortName strips the "org/" prefix: "myorg/myrepo" -> "myrepo"
func repoShortName(fullName string) string {
    parts := strings.SplitN(fullName, "/", 2)
    if len(parts) == 2 {
        return parts[1]
    }
    return fullName
}

func ExchangeForInstallationToken(ctx context.Context, appJWT, installationID string, allowedRepos []string, perms map[string]string) (string, error) {
    shortRepos := make([]string, len(allowedRepos))
    for i, r := range allowedRepos {
        shortRepos[i] = repoShortName(r)
    }
    body, _ := json.Marshal(installationTokenRequest{
        Repositories: shortRepos,
        Permissions:  perms,
    })
    req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
        "https://api.github.com/app/installations/"+installationID+"/access_tokens",
        bytes.NewReader(body))
    req.Header.Set("Authorization", "Bearer "+appJWT)
    req.Header.Set("Accept", "application/vnd.github+json")
    req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
    req.Header.Set("Content-Type", "application/json")

    resp, err := http.DefaultClient.Do(req)
    if err != nil {
        return "", fmt.Errorf("GitHub API request: %w", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusCreated {
        body, _ := io.ReadAll(resp.Body)
        return "", fmt.Errorf("GitHub API status %d: %s", resp.StatusCode, body)
    }
    var result installationTokenResponse
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return "", fmt.Errorf("decode GitHub API response: %w", err)
    }
    return result.Token, nil
}
```

### Permission Mapping from Profile

```go
// pkg/github/token.go

// CompilePermissions maps profile sourceAccess.github.allowedRepos permissions
// to GitHub App installation token permission format.
// clone/fetch -> contents:read; push -> contents:write (supersedes read).
func CompilePermissions(profilePerms []string) map[string]string {
    perms := map[string]string{}
    for _, p := range profilePerms {
        switch p {
        case "clone", "fetch":
            if perms["contents"] != "write" { // write supersedes read
                perms["contents"] = "read"
            }
        case "push":
            perms["contents"] = "write"
        }
    }
    return perms
}
```

### GIT_ASKPASS Script (written by userdata template)

```bash
#!/bin/bash
# /opt/km/bin/km-git-askpass
# Called by git with prompt string on $1.
# Reads GitHub token from SSM — token never in environment.
TOKEN=$(aws ssm get-parameter \
  --name "/sandbox/${SANDBOX_ID}/github-token" \
  --with-decryption \
  --query "Parameter.Value" \
  --output text 2>/dev/null || echo "")
case "$1" in
  *Username*) echo "x-access-token" ;;
  *Password*) echo "$TOKEN" ;;
  *)          echo "" ;;
esac
```

Bootstrap sets:
```bash
export GIT_ASKPASS=/opt/km/bin/km-git-askpass
```

### Terraform Module: github-token (abbreviated)

```hcl
# infra/modules/github-token/v1.0.0/main.tf
# Pattern: mirrors budget-enforcer/v1.0.0/main.tf

resource "aws_iam_role" "github_token_refresher" {
  name = "km-github-token-refresher-${var.sandbox_id}"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{ Effect = "Allow", Principal = { Service = "lambda.amazonaws.com" },
                   Action = "sts:AssumeRole" }]
  })
  tags = { "km:component" = "github-token-refresher", "km:sandbox_id" = var.sandbox_id }
}

resource "aws_lambda_function" "github_token_refresher" {
  function_name = "km-github-token-refresher-${var.sandbox_id}"
  role          = aws_iam_role.github_token_refresher.arn
  runtime       = "provided.al2023"
  handler       = "bootstrap"
  filename      = var.lambda_zip_path
  architectures = ["arm64"]
  timeout       = 60
  memory_size   = 128
  environment {
    variables = {
      KM_GITHUB_SSM_CONFIG_PREFIX = "/km/config/github"
    }
  }
}

resource "aws_scheduler_schedule" "github_token_refresh" {
  name                         = "km-github-token-${var.sandbox_id}"
  schedule_expression          = "rate(45 minutes)"
  schedule_expression_timezone = "UTC"
  flexible_time_window { mode = "OFF" }
  target {
    arn      = aws_lambda_function.github_token_refresher.arn
    role_arn = aws_iam_role.scheduler_invoke.arn
    input    = jsonencode({
      sandbox_id         = var.sandbox_id
      installation_id    = var.installation_id
      ssm_parameter_name = var.ssm_parameter_name
      kms_key_arn        = aws_kms_key.github_token.arn
      allowed_repos      = var.allowed_repos
      permissions        = var.permissions
    })
    retry_policy { maximum_retry_attempts = 0 }
  }
}
```

### KMS Key Policy for GitHub Token Parameter

```hcl
# infra/modules/github-token/v1.0.0/main.tf

resource "aws_kms_key" "github_token" {
  description             = "km github-token SSM encryption — sandbox ${var.sandbox_id}"
  deletion_window_in_days = 7
  enable_key_rotation     = true
  # Key policy: root admin + Lambda refresher (encrypt) + sandbox IAM role (decrypt)
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Long-lived PATs/SSH deploy keys | GitHub App installation tokens (1-hour expiry) | GitHub's recommended approach as of 2021+ | Tokens are repo-scoped and auto-expire; no manual rotation needed |
| dgrijalva/jwt-go | golang-jwt/jwt/v5 | 2021 (security maintenance transfer) | v5 has breaking API improvements and is the actively maintained fork |
| `GITHUB_TOKEN` env var in userdata | `GIT_ASKPASS` credential helper reads SSM at runtime | Phase 13 replaces Phase 2 stub | Token never appears in process environment or CloudTrail userdata logs |

**Deprecated/outdated in this codebase:**
- userdata.go section 4 (`{{- if .HasGitHub }}`): Exports `GITHUB_TOKEN` as env var. Phase 13 replaces this with the GIT_ASKPASS pattern. The `HasGitHub` template field and `userDataParams.HasGitHub` field remain but the template output changes.
- `security.go` `compileSecrets()`: Returns `/km/github/app-token` as a secret path. Phase 13 changes the path to `/sandbox/{sandbox-id}/github-token` (per-sandbox path, not a shared operator path).

---

## Codebase Integration Points

These are the specific existing code locations Phase 13 must modify:

### 1. `pkg/compiler/userdata.go` — Template Section 4

Current section 4 exports `GITHUB_TOKEN` as environment variable. Replace with:
- Write `km-git-askpass` script to `/opt/km/bin/km-git-askpass`
- `chmod +x /opt/km/bin/km-git-askpass`
- `export GIT_ASKPASS=/opt/km/bin/km-git-askpass`
- Remove `export GITHUB_TOKEN` entirely

### 2. `pkg/compiler/security.go` — `compileSecrets()`

Current stub: `paths = append(paths, "/km/github/app-token")` when GitHub is configured. This was a placeholder.

Replace with nothing — the GitHub token SSM path is per-sandbox and not an injected secret via the `SecretPaths` mechanism. The token is read at runtime via GIT_ASKPASS. Remove the `/km/github/app-token` stub.

### 3. `pkg/compiler/service_hcl.go` — Both `ec2HCLParams` and `ecsHCLParams`

Add `github_token_inputs` block to both templates, guarded by `{{- if .HasGitHub }}`. Add corresponding template fields:
```go
HasGitHub           bool
GitHubAllowedRepos  []string
GitHubPermissions   string // HCL-serialized map
```

### 4. `pkg/compiler/compiler.go` — `CompiledArtifacts`

Add `GitHubTokenHCL string` field, populated from a new `generateGitHubTokenHCL(sandboxID)` function when `p.Spec.SourceAccess.GitHub != nil`.

### 5. `internal/app/cmd/create.go` — Step sequence

Add a new step after the main sandbox apply (after Step 12c budget-enforcer deploy):
- Step 13a: Generate GitHub App installation token (call SSM for config, mint JWT, exchange for token, write to SSM)
- Step 13b: Deploy `github-token/` Terragrunt directory (Lambda + EventBridge schedule)

### 6. `internal/app/cmd/destroy.go` — Cleanup sequence

Add cleanup before the main sandbox destroy (mirroring budget-enforcer pattern):
- Delete EventBridge schedule `km-github-token-{sandbox-id}` (non-fatal)
- Destroy `github-token/` Terragrunt directory (non-fatal)
- Delete SSM parameter `/sandbox/{sandbox-id}/github-token` (non-fatal)
- Delete KMS key scheduled deletion (7-day window — set on the key itself)

### 7. `internal/app/cmd/root.go` — Command registration

Register `NewConfigureGitHubCmd(cfg)` as a subcommand of the `configure` command. `km configure github` becomes the operator-facing entry point.

### 8. `Makefile` — `build-lambdas` target

Add:
```makefile
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o build/bootstrap ./cmd/github-token-refresher/
cd build && zip -j github-token-refresher.zip bootstrap && rm bootstrap
```

### 9. `infra/modules/scp/v1.0.0/main.tf` — Trusted ARN carve-outs

Extend `trusted_arns_ssm` local to include `"arn:aws:iam::${var.application_account_id}:role/km-github-token-refresher-*"`. This ensures the Phase 10 SCP does not block the token refresher Lambda's SSM calls.

---

## Open Questions

1. **Cobra subcommand structure for `km configure github`**
   - What we know: `configure.go` implements a single runE function; `km configure` is a standalone subcommand.
   - What's unclear: Whether `km configure github` should be a separate top-level command or a Cobra subcommand of `configure`. The phase description says "subcommand."
   - Recommendation: Add `configure.AddCommand(NewConfigureGitHubCmd(cfg))` in `root.go`. This mirrors how `km shell`, `km budget`, etc. are registered.

2. **ECS credential helper delivery**
   - What we know: ECS containers don't use userdata; the `km-git-askpass` script must be present inside the sandbox image.
   - What's unclear: Whether the Phase 13 scope includes ECS or only EC2 for the GIT_ASKPASS credential helper. ECS sandboxes need the script baked into the image or delivered via a sidecar volume.
   - Recommendation: For Phase 13 v1, the credential helper is EC2-only (injected via userdata). ECS sandboxes use `GIT_ASKPASS` set in the ECS container environment pointing to a script in the image. Document this as a Phase 13 constraint; the sidecar pipeline (Phase 8) can include the script in a future phase.

3. **Private key rotation**
   - What we know: GitHub App private keys can be rotated; SSM SecureString can be overwritten.
   - What's unclear: Whether `km configure github` should detect an existing key and prompt before overwriting.
   - Recommendation: `km configure github` with `--force` flag to overwrite; interactive mode warns if key exists. Lambda always reads the key fresh from SSM on each invocation, so rotation is effective immediately.

---

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go `testing` package + `go test ./...` |
| Config file | none (native Go test tooling) |
| Quick run command | `go test ./pkg/github/... -v -run TestGenerateGitHubAppJWT` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command | Notes |
|----------|-----------|-------------------|-------|
| JWT generation with RS256 | unit | `go test ./pkg/github/... -run TestGenerateGitHubAppJWT` | Uses fake RSA key; verifies claims structure |
| PKCS#1 and PKCS#8 key parsing | unit | `go test ./pkg/github/... -run TestParsePrivateKey` | Tests both PEM formats |
| Permission mapping clone/fetch/push → contents:{read,write} | unit | `go test ./pkg/github/... -run TestCompilePermissions` | Table-driven |
| Repo short-name stripping | unit | `go test ./pkg/github/... -run TestRepoShortName` | "org/repo" → "repo" |
| Token exchange HTTP request (stub server) | unit | `go test ./pkg/github/... -run TestExchangeForInstallationToken` | httptest.NewServer stub |
| Compiler emits github_token_inputs when sourceAccess.github set | unit | `go test ./pkg/compiler/... -run TestGitHubTokenHCL` | String assertion on rendered HCL |
| Compiler does NOT emit github_token_inputs when no sourceAccess.github | unit | `go test ./pkg/compiler/... -run TestNoGitHubTokenHCL` | Nil check |
| userdata.go GIT_ASKPASS script injection | unit | `go test ./pkg/compiler/... -run TestUserDataGitAskpass` | Verify script content, no GITHUB_TOKEN env export |
| km configure github writes SSM parameters | integration (manual) | Manual verification with `aws ssm get-parameter` | Cannot mock SSM in unit test without credentials |

### Sampling Rate

- **Per task commit:** `go test ./pkg/github/... ./pkg/compiler/... -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `pkg/github/token.go` — new package; must be created before any test runs
- [ ] `pkg/github/token_test.go` — covers JWT generation, permission mapping, repo name stripping, HTTP stub exchange
- [ ] `cmd/github-token-refresher/main.go` — Lambda entry point (new)
- [ ] `infra/modules/github-token/v1.0.0/main.tf` — Terraform module (new)
- [ ] Build target `build/github-token-refresher.zip` in `Makefile`

---

## Sources

### Primary (HIGH confidence)

- [GitHub Docs — Generating an installation access token](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app) — API endpoint, repository scoping, permission format, 1-hour expiry
- [GitHub Docs — Generating a JWT for a GitHub App](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app) — iss claim (client ID), iat 60-second buffer, 10-minute max expiry, RS256 requirement
- [pkg.go.dev/github.com/golang-jwt/jwt/v5](https://pkg.go.dev/github.com/golang-jwt/jwt/v5) — RegisteredClaims, SigningMethodRS256, NewWithClaims API
- Existing codebase: `infra/modules/budget-enforcer/v1.0.0/main.tf` — Lambda + EventBridge Scheduler + IAM pattern (HIGH — primary template)
- Existing codebase: `infra/modules/secrets/v1.0.0/main.tf` — KMS + SSM SecureString pattern (HIGH — primary template)
- Existing codebase: `pkg/compiler/userdata.go` — userdata template structure and `HasGitHub` stub (HIGH — defines change scope)
- Existing codebase: `pkg/compiler/security.go` — `compileSecrets()` GitHub placeholder (HIGH — defines change scope)
- Existing codebase: `internal/app/cmd/create.go` — create step numbering (HIGH — integration pattern)
- Existing codebase: `internal/app/cmd/destroy.go` — cleanup pattern (HIGH — integration pattern)

### Secondary (MEDIUM confidence)

- [git-scm.com/docs/gitcredentials](https://git-scm.com/docs/gitcredentials) — GIT_ASKPASS invocation protocol (Username/Password prompt strings)
- [AWS SSM PutParameter API](https://github.com/aws/aws-sdk-go-v2/blob/main/service/ssm/api_op_PutParameter.go) — Overwrite field, KMS key ID parameter

### Tertiary (LOW confidence)

- Community discussion re: per-repo permission scoping with different permissions per repo — current API supports one permission level per token across all listed repos

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — golang-jwt/jwt/v5 verified from pkg.go.dev; SSM/Lambda/EventBridge patterns directly observed in existing codebase
- Architecture: HIGH — patterns directly mirror budget-enforcer which is already implemented and deployed
- GitHub API: HIGH — verified from official GitHub Docs (JWT claims, token exchange endpoint, repo scoping, 1-hour expiry)
- Pitfalls: MEDIUM-HIGH — PKCS#1/PKCS#8 and clock drift are well-known; org-prefix stripping verified from GitHub API docs; GITHUB_TOKEN env replacement is directly observed in userdata.go

**Research date:** 2026-03-22
**Valid until:** 2026-09-22 (GitHub App API is stable; SSM/Lambda patterns are stable)
