# Phase 25: GitHub Source Access Restrictions — Research

**Researched:** 2026-03-26
**Domain:** Go testing, git access control enforcement, GIT_ASKPASS, ref filtering, HTTP proxy allowlists
**Confidence:** HIGH

---

## Summary

Phase 25 is a deep testing phase for a feature that is already structurally implemented. The GitHub
source access system was built in Phase 13 (token generation, Lambda refresh, GIT_ASKPASS credential
helper, service.hcl compiler integration) and Phase 2 (NETW-08 schema). The code is live. What is
missing is comprehensive behavioral test coverage that validates the end-to-end enforcement contracts:
allowlist enforcement (only repos in `allowedRepos` can be cloned), deny-by-default (repos not listed
are blocked), push enforcement (push permission only when `push` in `permissions`), and ref
enforcement (`allowedRefs` is stored in the profile schema but there is currently zero enforcement
code — it is schema-only data with no enforcement mechanism).

The gap is not implementation — it is correctness proofs (tests) and, for ref enforcement, both the
implementation and its tests.

**Primary recommendation:** Write tests for everything that should already work, implement ref
enforcement in the GIT_ASKPASS/credential layer, and write tests for that too. Keep all new code in
the existing package structure.

---

## Current State Audit (What Exists)

### What is implemented (Phase 13)

| Component | Location | Status |
|-----------|----------|--------|
| `GitHubAccess` struct with `AllowedRepos`, `AllowedRefs`, `Permissions` | `pkg/profile/types.go:152-157` | Schema only — `AllowedRefs` has no enforcement |
| `CompilePermissions()` — maps clone/fetch/push to GitHub API perms | `pkg/github/token.go:188-201` | Tested |
| `ExchangeForInstallationToken()` — scopes token to specific repos | `pkg/github/token.go:119-167` | Tested with httptest |
| `WriteTokenToSSM()` — stores token at `/sandbox/{id}/github-token` | `pkg/github/token.go:213-226` | Tested |
| `TokenRefreshHandler.HandleTokenRefresh()` — Lambda handler | `pkg/github/token.go:267-327` | Tested |
| GIT_ASKPASS credential helper in EC2 user-data | `pkg/compiler/userdata.go:131-152` | Tested (presence only) |
| `github_token_inputs` block in EC2+ECS service.hcl | `pkg/compiler/service_hcl.go` | Tested (presence only) |
| `generateGitHubTokenHCL()` — github-token/terragrunt.hcl | `pkg/compiler/github_token_hcl.go` | Tested (present/absent) |
| `km create` GitHub token generation + SSM write | `internal/app/cmd/create.go:545` | Smoke-tested only |
| Terraform module `infra/modules/github-token/` | `infra/modules/github-token/` | Deployed in Phase 13 |

### What is NOT tested or implemented

| Gap | Description | Risk |
|-----|-------------|------|
| **Allowlist enforcement behavior** | No test verifies that a token scoped to `[org/repo-a]` cannot be used to clone `org/repo-b` (GitHub API enforces this, but we don't test the scoping logic end-to-end) | MEDIUM — relies entirely on GitHub's enforcement |
| **Deny-by-default** | No test covers `sourceAccess.github = nil` or empty `allowedRepos` → no token generated at all | HIGH — easy to accidentally generate an unscoped token |
| **Ref enforcement** | `AllowedRefs` is a schema field with no enforcement code anywhere in the codebase | HIGH — field documented as a security control but does nothing |
| **Push prevention for clone-only profiles** | No test verifies that a `permissions: [clone]` token cannot push | MEDIUM — GitHub API enforces this if `contents:write` not granted |
| **Wildcard repo patterns** | `github.com/org/*` in allowedRepos — no test for pattern matching semantics vs exact match behavior | HIGH — current code passes patterns directly to GitHub API which may interpret them differently |
| **Token scope audit logging** | Lambda logs token generation but no test verifies the audit log contains repo scope | LOW |
| **ECS credential delivery** | userdata.go comment says "ECS credential helper delivery is deferred" — ECS sandboxes have no GIT_ASKPASS | HIGH — ECS substrate has no working GitHub access |
| **km configure github integration test** | No integration test verifying the full km create → token generate → SSM write → GIT_ASKPASS path | MEDIUM |

---

## Architecture Patterns

### Pattern 1: GitHub App token scoping as the primary enforcement layer

The GitHub App installation token is scoped to specific repositories at creation time. This is the
primary enforcement mechanism — the token simply cannot be used to clone repos not listed in the
`repositories` array when calling `POST /app/installations/{id}/access_tokens`.

```go
// Source: pkg/github/token.go:119
func ExchangeForInstallationToken(ctx context.Context, jwtToken, installationID string,
    repos []string, perms map[string]string) (string, error)
```

The `repos` list is derived from `profile.Spec.SourceAccess.GitHub.AllowedRepos` via
`CompilePermissions()`. This is clean and correct for the primary enforcement path.

**Critical nuance:** GitHub API expects short repo names (no org prefix). The current code strips
org prefixes via `repoShortName()`. However, the profile stores `github.com/org/repo` format.
The compiler passes the full `AllowedRepos` list to `ExchangeForInstallationToken()` which strips
on its own. This works but is untested for wildcard patterns — `github.com/org/*` becomes `*` after
stripping, which GitHub API may reject or misinterpret.

### Pattern 2: GIT_ASKPASS credential helper (EC2 only)

The credential helper script is written to `/opt/km/bin/km-git-askpass` at boot via user-data:

```bash
# Source: pkg/compiler/userdata.go:136-150
case "$1" in
  *Username*) echo "x-access-token" ;;
  *Password*) echo "$TOKEN" ;;
  *)          echo "" ;;
esac
```

Git invokes this helper when credentials are needed (HTTPS clone, push). The token is fetched from
SSM at git-operation time, never stored in env vars.

**This is EC2-only.** ECS sandboxes have `github_token_inputs` emitted in service.hcl to deploy the
Lambda/EventBridge, but no equivalent of GIT_ASKPASS injection into the ECS container environment.
This is documented in userdata.go as "deferred to a future phase."

### Pattern 3: AllowedRefs — schema data with no enforcement

`AllowedRefs` is defined in:
- `pkg/profile/types.go:155` — Go type
- JSON schema — validated as array of strings
- Stored in `TokenRefreshEvent.AllowedRepos` (but NOT `AllowedRefs` — refs are not passed to Lambda)
- Documented in `docs/profile-reference.md` and `docs/security-model.md` as a security control

There is **zero enforcement code**. No code checks refs against allowedRefs before allowing a push
or checkout. The profile reference docs claim "Supports wildcards (`feature/*`, `fix/*`)" but no
code implements this matching.

**Enforcement options for refs:**

1. **Git hooks (pre-push / pre-receive)**: Install a `pre-push` hook in `/workspace/.git/hooks/`
   or a global hook via `~/.gitconfig core.hooksPath`. The hook reads `$KM_ALLOWED_REFS` env var
   and rejects pushes to unlisted refs. This is the right approach for EC2 where we control the
   shell environment.

2. **HTTP proxy ref filtering**: The http-proxy sees the HTTPS CONNECT tunnel to `github.com:443`
   but cannot inspect git protocol within the TLS tunnel without MITM. This is not viable without
   decrypting all GitHub traffic.

3. **GIT_ASKPASS wrapper that denies based on target URL**: The credential helper is invoked per
   git operation and knows the remote URL. It could reject credential provision for refs not in the
   allowlist, but it only sees the URL, not the ref being pushed to.

4. **Compiler-injected gitconfig**: Set `git config --system receive.denyNonFastForwards true` and
   inject `~/.gitconfig` with per-repo refspec restrictions. However, gitconfig refspec restrictions
   apply to fetch, not push enforcement.

**Recommended approach:** Global git hook via `core.hooksPath` set to `/opt/km/hooks/`. The
compiler injects the hook at boot via user-data. The hook script:
- Reads `$KM_ALLOWED_REFS` env var (injected by compiler as part of user-data)
- On `pre-push`: parses refs being pushed, rejects any not matching allowed patterns
- Wildcard matching: `fnmatch`-style glob (`feature/*` matches `feature/my-branch`)

### Pattern 4: Deny-by-default testing strategy

The correct test structure for deny-by-default:

```go
// When GitHub config is nil → no token generated, no github_token_inputs in HCL
p.Spec.SourceAccess.GitHub = nil
// → compiler must NOT emit github_token_inputs
// → user-data must NOT contain GIT_ASKPASS section
// → km create must NOT call generateAndStoreGitHubToken()

// When AllowedRepos is empty slice → same as nil case (no access)
p.Spec.SourceAccess.GitHub = &profile.GitHubAccess{AllowedRepos: []string{}}
// → token scoped to empty repo list → GitHub API rejects or returns token with no repo access
```

The deny-by-default contract is: if `sourceAccess.github` is not set, or `allowedRepos` is empty,
no GitHub access is provisioned. This must be tested at the compiler level AND the create command level.

---

## Standard Stack

### Core (already in use, no changes needed)
| Library | Version | Purpose | Location |
|---------|---------|---------|----------|
| `golang-jwt/jwt/v5` | v5 | GitHub App JWT generation | `pkg/github/token.go` |
| `net/http/httptest` | stdlib | Mock GitHub API in tests | `pkg/github/token_test.go` |
| `go test` | stdlib | Test framework | all `_test.go` files |
| `strings.Contains` | stdlib | Source-level verification pattern | established pattern |

### For ref enforcement implementation
| Component | Purpose | Notes |
|-----------|---------|-------|
| `os.Getenv("KM_ALLOWED_REFS")` | Read allowed refs in git hook | Injected at boot by compiler |
| `path.Match` or `fnmatch`-style | Wildcard ref matching | stdlib `path.Match` supports `*` globs |
| `pre-push` git hook | Block pushes to unlisted refs | Written to `/opt/km/hooks/pre-push` by user-data |
| `git config --global core.hooksPath /opt/km/hooks` | Enable global hooks | Run during user-data bootstrap |

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead |
|---------|-------------|-------------|
| Repo access scoping | Custom HTTPS proxy that inspects git protocol | GitHub App token scoping (already done — it's the right layer) |
| Ref pattern matching | Custom glob engine | `path.Match()` from stdlib or `fnmatch` semantics — `*` matches all non-`/` chars which is correct for branch names |
| Token refresh | Custom refresh loop in sandbox | Existing Lambda + EventBridge 45-minute schedule (Phase 13) |
| HTTPS interception for git | MITM CA in http-proxy for github.com | Not needed — token scoping at GitHub API level is sufficient |

---

## Common Pitfalls

### Pitfall 1: Wildcard patterns passed directly to GitHub API
**What goes wrong:** Profile specifies `allowedRepos: ["github.com/my-org/*"]`. After `repoShortName()`
strips org prefix, `"*"` is sent to GitHub API. The GitHub API does not accept wildcards in the
`repositories` array — it expects exact short names. GitHub will return a 422 error or silently
ignore the wildcard.
**How to avoid:** The token scoping layer must expand or validate patterns before calling the API.
Wildcard patterns are a UI convenience for the profile author, not a GitHub API primitive.
**Warning signs:** Token creation succeeds but with unexpected empty or full access.

### Pitfall 2: AllowedRefs — schema field that does nothing (false security)
**What goes wrong:** Operators set `allowedRefs: ["main"]` expecting sandbox cannot push to other
branches. There is no enforcement. Agents can push to any ref the token has write access to.
**How to avoid:** Phase 25 must implement ref enforcement OR clearly document the field as
aspirational/future. The security model docs currently claim it works.
**Warning signs:** Security audit finds `allowedRefs` in profile but no hook files at runtime.

### Pitfall 3: ECS has no GIT_ASKPASS
**What goes wrong:** ECS sandbox with `sourceAccess.github` configured deploys the Lambda/EventBridge
infrastructure (token refresh works) but the container has no credential helper. `git clone` will
prompt for credentials and fail (no TTY).
**How to avoid:** Phase 25 should either implement ECS credential injection or document the gap.
For ECS, the token must be injected as a container environment variable (less secure) or via an
init container / entrypoint script that reads from SSM.
**Warning signs:** ECS sandbox has `github_token_inputs` in service.hcl but `git clone` fails with
"Authentication failed".

### Pitfall 4: Token has write access when profile says clone-only
**What goes wrong:** `CompilePermissions(["clone"])` returns `{"contents": "read"}`. If the token
exchange call is misconfigured (e.g., empty permissions map falls back to full access), the sandbox
can push.
**How to avoid:** Test that `permissions: [clone]` produces a token that fails on push (403 from
GitHub). This is a GitHub API enforcement test, not a unit test.

### Pitfall 5: Deny-by-default breaks when AllowedRepos is empty slice vs nil
**What goes wrong:** `AllowedRepos: []string{}` (empty) vs `nil` may be handled differently. If
code does `if len(gh.AllowedRepos) > 0` the empty slice case might still create a token (with no
repos scoped — which GitHub may treat as full access or reject).
**How to avoid:** Treat both nil and empty as "no GitHub access". Test both cases explicitly.

---

## Code Examples

### Existing test pattern for proxy enforcement (reference for new tests)
```go
// Source: sidecars/http-proxy/httpproxy/http_proxy_test.go:76
func TestHTTPProxy_BlockedHost(t *testing.T) {
    target := httptest.NewServer(...)
    proxy := httpproxy.NewProxy([]string{}, "test-sandbox") // empty allowlist = deny all
    _, proxyAddr := startProxyServer(t, proxy)
    client := proxyClient(t, proxyAddr)
    resp, err := client.Get(target.URL)
    // should get 403 or connection refused
}
```

### Existing pattern: deny-by-default in compiler (reference)
```go
// Source: pkg/compiler/compiler_test.go:749
func TestServiceHCLEC2NoGitHubInputs(t *testing.T) {
    p := loadTestProfile(t, "ec2-basic.yaml") // no github
    artifacts, err := compiler.Compile(p, id, false, testNetwork())
    if strings.Contains(artifacts.ServiceHCL, "github_token_inputs") {
        t.Errorf("EC2 ServiceHCL should NOT contain github_token_inputs when sourceAccess.github is nil")
    }
}
```

### New test: deny-by-default for empty AllowedRepos
```go
// To be written in pkg/compiler/compiler_test.go
func TestCompileEC2EmptyAllowedRepos_DenyByDefault(t *testing.T) {
    p := loadTestProfile(t, "ec2-basic.yaml")
    p.Spec.SourceAccess.GitHub = &profile.GitHubAccess{AllowedRepos: []string{}}
    artifacts, err := compiler.Compile(p, "sb-empty-repos", false, testNetwork())
    // empty allowedRepos should behave same as nil — no token infra
    if strings.Contains(artifacts.ServiceHCL, "github_token_inputs") {
        t.Errorf("empty allowedRepos must not emit github_token_inputs")
    }
}
```

### New test: CompilePermissions covers all valid combinations
```go
// To be written in pkg/github/token_test.go
func TestCompilePermissions_EmptySlice(t *testing.T) {
    perms := github.CompilePermissions([]string{})
    if len(perms) != 0 {
        t.Errorf("empty permissions should produce empty map, got %v", perms)
    }
}

func TestCompilePermissions_UnknownPermission(t *testing.T) {
    perms := github.CompilePermissions([]string{"write"}) // "write" is not valid — only "push"
    if len(perms) != 0 {
        t.Errorf("unknown permission should not produce a GitHub permission, got %v", perms)
    }
}
```

### Ref enforcement hook template (for user-data injection)
```bash
# /opt/km/hooks/pre-push — injected by compiler when allowedRefs is non-empty
#!/bin/bash
# km ref enforcement: block pushes to refs not in allowlist
ALLOWED_REFS="${KM_ALLOWED_REFS:-}"  # colon-separated list, e.g. "main:develop:feature/*"
if [ -z "$ALLOWED_REFS" ]; then
  exit 0  # no restriction
fi

while read local_ref local_sha remote_ref remote_sha; do
  branch="${remote_ref#refs/heads/}"
  allowed=false
  IFS=: read -ra PATTERNS <<< "$ALLOWED_REFS"
  for pattern in "${PATTERNS[@]}"; do
    if [[ "$branch" == $pattern ]]; then  # bash glob match
      allowed=true
      break
    fi
  done
  if [ "$allowed" = false ]; then
    echo "[km] Push to '$branch' denied — not in allowedRefs: $ALLOWED_REFS" >&2
    exit 1
  fi
done
exit 0
```

---

## Test Coverage Map

### What already has tests (Phase 13 coverage)

| Behavior | Test Function | File |
|----------|---------------|------|
| JWT generation PKCS1/PKCS8 | `TestGenerateGitHubAppJWT_PKCS1/PKCS8` | `pkg/github/token_test.go` |
| Non-RSA key rejection | `TestGenerateGitHubAppJWT_NonRSAKey` | `pkg/github/token_test.go` |
| JWT claims (iss, iat, exp) | `TestGenerateGitHubAppJWT_Claims` | `pkg/github/token_test.go` |
| clone → contents:read | `TestCompilePermissions_Clone` | `pkg/github/token_test.go` |
| push → contents:write | `TestCompilePermissions_Push` | `pkg/github/token_test.go` |
| write supersedes read | `TestCompilePermissions_ClonePush_WriteSupersedes` | `pkg/github/token_test.go` |
| Token exchange HTTP 201 | `TestExchangeForInstallationToken_Success` | `pkg/github/token_test.go` |
| Token exchange non-201 error | `TestExchangeForInstallationToken_NonCreatedStatus` | `pkg/github/token_test.go` |
| Org prefix stripping | `TestExchangeForInstallationToken_RepoShortNames` | `pkg/github/token_test.go` |
| SSM write path | `TestWriteTokenToSSM_Success` | `pkg/github/token_test.go` |
| Lambda audit log on success | `TestLambdaHandler_AuditLog_Success` | `pkg/github/token_test.go` |
| Lambda audit log on failure | `TestLambdaHandler_AuditLog_Failure` | `pkg/github/token_test.go` |
| GIT_ASKPASS injected in user-data | `TestCompileGitHubToken` | `pkg/compiler/compiler_test.go` |
| GIT_ASKPASS not injected when no github | `TestGitHubUserDataGITASKPASS` | `pkg/compiler/compiler_test.go` |
| Token not in env var | `TestGitHubUserDataNoGITHUBTOKENExport` | `pkg/compiler/compiler_test.go` |
| No app-token in SecretPaths | `TestCompileSecretsNoGitHubAppToken` | `pkg/compiler/compiler_test.go` |
| EC2 service.hcl has github_token_inputs | `TestServiceHCLEC2GitHubInputs` | `pkg/compiler/compiler_test.go` |
| ECS service.hcl has github_token_inputs | `TestServiceHCLECSGitHubInputs` | `pkg/compiler/compiler_test.go` |
| EC2 no github → no github_token_inputs | `TestServiceHCLEC2NoGitHubInputs` | `pkg/compiler/compiler_test.go` |
| ECS no github → no github_token_inputs | `TestServiceHCLECSNoGitHubInputs` | `pkg/compiler/compiler_test.go` |

### Gaps requiring new tests in Phase 25

| Behavior | Test to Write | File |
|----------|---------------|------|
| Empty allowedRepos → deny (EC2 service.hcl) | `TestCompileEC2EmptyAllowedRepos_DenyByDefault` | `pkg/compiler/compiler_test.go` |
| Empty allowedRepos → deny (ECS service.hcl) | `TestCompileECSEmptyAllowedRepos_DenyByDefault` | `pkg/compiler/compiler_test.go` |
| Empty allowedRepos → no user-data GIT_ASKPASS | `TestUserDataEmptyAllowedRepos_NoGITASKPASS` | `pkg/compiler/compiler_test.go` |
| Empty permissions map from CompilePermissions([]) | `TestCompilePermissions_EmptySlice` | `pkg/github/token_test.go` |
| Unknown permission string is ignored | `TestCompilePermissions_UnknownPermission` | `pkg/github/token_test.go` |
| AllowedRefs injected as KM_ALLOWED_REFS env var in user-data | `TestUserDataAllowedRefsEnvVar` | `pkg/compiler/compiler_test.go` |
| Empty allowedRefs → no KM_ALLOWED_REFS in user-data | `TestUserDataEmptyAllowedRefs_NoEnvVar` | `pkg/compiler/compiler_test.go` |
| Pre-push hook written when allowedRefs set | `TestUserDataPrePushHookPresent` | `pkg/compiler/compiler_test.go` |
| Pre-push hook absent when no allowedRefs | `TestUserDataPrePushHookAbsent` | `pkg/compiler/compiler_test.go` |
| Wildcard pattern in allowedRefs — hook uses correct glob syntax | `TestRefHook_WildcardPattern` | new file `pkg/compiler/ref_hook_test.go` or inline |
| km create skips token when no github config | `TestCreateGitHubSkip_NoSourceAccess` | `internal/app/cmd/create_test.go` |
| km create skips token when allowedRepos empty | `TestCreateGitHubSkip_EmptyRepos` | `internal/app/cmd/create_test.go` |

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | `go test` (stdlib) |
| Config file | `go.mod` / none — no separate test config |
| Quick run command | `go test ./pkg/github/... ./pkg/compiler/... -count=1` |
| Full suite command | `go test ./... -count=1` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command |
|----------|-----------|-------------------|
| Deny-by-default: nil github → no token infra | unit | `go test ./pkg/compiler/... -run TestServiceHCL` |
| Deny-by-default: empty repos → no token infra | unit | `go test ./pkg/compiler/... -run TestCompileEC2EmptyAllowedRepos` |
| Clone-only profile → contents:read permission | unit | `go test ./pkg/github/... -run TestCompilePermissions` |
| Push profile → contents:write permission | unit | `go test ./pkg/github/... -run TestCompilePermissions` |
| Ref enforcement hook written when allowedRefs set | unit | `go test ./pkg/compiler/... -run TestUserData` |
| Ref hook syntax correct for wildcard patterns | unit | `go test ./pkg/compiler/... -run TestRefHook` |
| AllowedRefs passed as env var to sandbox | unit | `go test ./pkg/compiler/... -run TestUserDataAllowedRefs` |

### Sampling Rate
- **Per task commit:** `go test ./pkg/github/... ./pkg/compiler/... -count=1`
- **Per wave merge:** `go test ./... -count=1`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] No test infrastructure gaps — test files already exist for `pkg/github/`, `pkg/compiler/`
- [ ] New test data profiles needed:
  - `testdata/profiles/ec2-empty-repos.yaml` — profile with `allowedRepos: []`
  - `testdata/profiles/ec2-with-allowed-refs.yaml` — profile with `allowedRefs: ["main", "feature/*"]`
- [ ] New production code needed:
  - `pkg/compiler/userdata.go` — inject `KM_ALLOWED_REFS` env var and pre-push hook when `AllowedRefs` non-empty
  - Compiler must set `core.hooksPath` in gitconfig and write `/opt/km/hooks/pre-push`
  - Possibly a helper function `compileRefHook(allowedRefs []string) string` for testability

---

## Architecture Decision: AllowedRefs Enforcement

This is the only area requiring new production code (not just tests). There are three choices:

### Option A: Git hooks (RECOMMENDED)
Inject `core.hooksPath=/opt/km/hooks` into global gitconfig and write a `pre-push` hook.
- Pros: Works at the git level before network I/O; sandbox user cannot bypass without shell access to `/opt/km/`; enforced by the km-sidecar user owning the hooks dir
- Cons: EC2 only (ECS deferred); requires bash in the hook; can be bypassed by `git push --no-verify` (but sandbox policy disables shell escapes)
- Blocked by: `--no-verify` bypass. Mitigation: `policy.allowShellEscape: false` already enforced.

### Option B: HTTP proxy ref header inspection
Parse `git push` protocol inside the proxy for HTTPS. Requires MITM CA and git protocol parsing.
- Pros: Cannot be bypassed by `--no-verify`
- Cons: Complex, fragile, requires TLS interception of github.com which is undesirable

### Option C: Document as "defense in depth, not primary control"
Mark `allowedRefs` as advisory — the GitHub App token scoping (by repo) is the primary control;
ref restrictions are a second layer that requires explicit operator action.
- Pros: Zero implementation risk
- Cons: Docs currently claim it works; silent security gap

**Decision for Phase 25:** Implement Option A (git hooks) for EC2. Document ECS limitation. Update
security-model.md to accurately describe the enforcement approach and its limitations.

---

## Open Questions

1. **ECS GIT_ASKPASS delivery**
   - What we know: ECS has no user-data script, container env vars are set in task definition
   - What's unclear: Whether Phase 25 should implement ECS token injection or defer it
   - Recommendation: Implement a minimal ECS token injection mechanism (env var from Secrets Manager
     reference or SSM secrets container) OR explicitly document the gap in security-model.md.
     ECS is a first-class substrate — leaving it broken is a v1 release blocker.

2. **Wildcard repo pattern semantics**
   - What we know: `github.com/org/*` is currently passed to `ExchangeForInstallationToken` which
     strips to `*` and sends to GitHub API
   - What's unclear: Does GitHub API accept `*` in the repositories array?
   - Recommendation: Test against GitHub API (or review GitHub App docs). If wildcards are not
     supported, the compiler must either reject wildcard patterns in `allowedRepos` or expand them
     to the full repo list. Based on GitHub docs (HIGH confidence), the `repositories` field in the
     installation token request requires exact repository names, not patterns.

3. **`--no-verify` bypass for git hooks**
   - What we know: Sandbox policy has `allowShellEscape: false` and allowedCommands list
   - What's unclear: Whether `git push --no-verify` is blocked by the allowedCommands enforcement
   - Recommendation: The `allowedCommands` enforcement is best-effort (not kernel-enforced). For
     truly paranoid profiles, the only bypass-proof control is the GitHub App token scoping.
     Document this in security-model.md.

---

## Sources

### Primary (HIGH confidence)
- Direct code inspection: `pkg/github/token.go`, `pkg/github/token_test.go`, `pkg/compiler/compiler_test.go`, `pkg/compiler/userdata.go`, `pkg/compiler/github_token_hcl.go`
- Direct code inspection: `pkg/profile/types.go`, `sidecars/http-proxy/httpproxy/proxy.go`
- Direct test inventory: `go test ./... -list '.*'`

### Secondary (MEDIUM confidence)
- GitHub Apps API documentation (training data): `POST /app/installations/{id}/access_tokens` accepts exact repo short names in `repositories` array, not wildcards
- Git hook documentation (training data): `pre-push` hook receives refs on stdin, `core.hooksPath` controls global hook directory

### Tertiary (LOW confidence)
- None

---

## Metadata

**Confidence breakdown:**
- Current state audit: HIGH — direct code inspection
- Test gap analysis: HIGH — complete test inventory from `go test -list`
- AllowedRefs enforcement approach: MEDIUM — git hook option well-established but wildcard GitHub API behavior needs verification
- ECS gap severity: HIGH — confirmed absent from userdata.go (EC2-only comment at line 128)

**Research date:** 2026-03-26
**Valid until:** 2026-04-25 (stable domain)
