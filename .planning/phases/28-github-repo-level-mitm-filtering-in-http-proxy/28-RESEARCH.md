# Phase 28: GitHub Repo-Level MITM Filtering in HTTP Proxy - Research

**Researched:** 2026-03-28
**Domain:** Go HTTP proxy MITM path inspection, goproxy handler registration, compiler env-var wiring
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**MITM Pattern**
- Mirror the Bedrock/Anthropic MITM pattern in `proxy.go` — register GitHub-specific `HandleConnectFunc` (MITM) and `OnRequest.DoFunc` (path inspection + allowlist check) before the general CONNECT handler
- Use the existing custom CA infrastructure already in place for Bedrock MITM

**GitHub Host Coverage**
- MITM the following hosts to inspect URL paths:
  - `github.com` — web UI and git-over-HTTPS
  - `api.github.com` — REST API
  - `raw.githubusercontent.com` — raw file access
  - `codeload.githubusercontent.com` — archive/tarball downloads

**Repo Extraction from URL Paths**
- `github.com/{owner}/{repo}[.git]/*` — first two path segments
- `api.github.com/repos/{owner}/{repo}/*` — segments after `/repos/`
- `raw.githubusercontent.com/{owner}/{repo}/*` — first two path segments
- `codeload.githubusercontent.com/{owner}/{repo}/*` — first two path segments
- Non-repo URLs (e.g., `api.github.com/rate_limit`, `github.com/login`) pass through — only enforce when a repo is identifiable

**Allowlist Format**
- `allowedRepos` uses `owner/repo` format (already defined in profile schema)
- Case-insensitive matching
- Consider supporting org wildcards (`whereiskurt/*`) for org-wide access

**Token Handling**
- Passthrough only — allow existing tokens (gh client, git credential helper) on requests
- No injection — proxy does NOT inject GitHub App tokens into requests (deferred to future phase)
- Tokens already on the sandbox filesystem via SSM + git credential helper continue to work as-is

**Blocked Request Behavior**
- Return 403 with a clear message identifying the blocked `owner/repo` (similar to budget enforcement blocked responses)
- Log blocked repo access for audit trail

**Configuration**
- The proxy receives `allowedRepos` list via environment variable (similar to how `ALLOWED_HOSTS` is passed today)
- Compiled from `spec.sourceAccess.github.allowedRepos` in the profile

### Claude's Discretion
- Internal code structure (separate file vs inline in proxy.go)
- Exact regex patterns for host matching
- Test structure and coverage approach
- Error message formatting details

### Deferred Ideas (OUT OF SCOPE)
- Token injection — proxy injects GitHub App token into requests for app-authenticated repos (future phase)
- Two-class repos — distinguishing `auth: app` vs `auth: public` in profile schema (future, tied to token injection)
- Ref filtering — enforcing `allowedRefs` at the proxy level (currently only enforced via git hooks)
- Method filtering — restricting HTTP methods per repo (e.g., read-only = GET only)
</user_constraints>

---

## Summary

This phase adds repo-level path inspection to the HTTP proxy sidecar for GitHub hosts. The proxy currently enforces only host-level allow/deny via `IsHostAllowed`. This leaves a gap: if `github.com` is in `allowedHosts`, a sandbox can access any public repo, not just the ones in `sourceAccess.github.allowedRepos`.

The fix follows the exact Bedrock/Anthropic MITM pattern already shipping in `proxy.go`. For the four GitHub hosts (`github.com`, `api.github.com`, `raw.githubusercontent.com`, `codeload.githubusercontent.com`), the proxy registers a `HandleConnectFunc` that returns `goproxy.MitmConnect` and an `OnRequest.DoFunc` that extracts `owner/repo` from the URL path and checks the allowlist. Requests with no identifiable repo pass through. Requests to a blocked repo return 403 with a JSON body identifying the repo.

The allowedRepos list travels from the profile (`spec.sourceAccess.github.allowedRepos`) through the compiler as a new `KM_GITHUB_ALLOWED_REPOS` env var — passed into the proxy sidecar alongside the existing `ALLOWED_HOSTS`. Both the EC2 userdata template and the ECS service.hcl template need updating. The proxy `main.go` reads the new env var and passes it to `NewProxy` via a new `WithGitHubRepoFilter` option.

**Primary recommendation:** Implement as a separate `github.go` file inside `sidecars/http-proxy/httpproxy/`, mirroring the structure of `bedrock.go` and `anthropic.go`. Register GitHub MITM handlers before the general CONNECT handler in `NewProxy`, controlled by a `WithGitHubRepoFilter` option.

---

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/elazarl/goproxy` | v1.8.2 (go.mod) | MITM HTTP proxy framework | Already used for Bedrock/Anthropic MITM |
| `regexp` (stdlib) | — | Host matching regex | Used for `bedrockHostRegex`, `anthropicHostRegex` |
| `strings` (stdlib) | — | Path splitting, case normalization | Used throughout proxy.go |
| `net/http` (stdlib) | — | HTTP response construction | Used in blocked response builders |

### No New Dependencies Required
All needed libraries are already present in go.mod. No additions needed.

---

## Architecture Patterns

### Recommended File Structure

New file inside the existing package:
```
sidecars/http-proxy/httpproxy/
├── proxy.go          (existing — add WithGitHubRepoFilter option registration)
├── bedrock.go        (existing — unchanged)
├── anthropic.go      (existing — unchanged)
├── github.go         (NEW — GitHub MITM helpers + option)
├── github_test.go    (NEW — unit tests for ExtractRepoFromPath, IsRepoAllowed, etc.)
├── http_proxy_test.go (existing — add integration tests for MITM blocking)
└── budget_cache.go   (existing — unchanged)
```

### Pattern 1: goproxy Handler Registration Order (CRITICAL)

`goproxy` uses **first-match semantics** for `HandleConnectFunc`. GitHub MITM handlers MUST be registered BEFORE the general `OkConnect` handler in `NewProxy`. This is already the established pattern for Bedrock and Anthropic.

```go
// Source: proxy.go (lines 162–196) — existing Bedrock pattern to mirror

// GitHub MITM (MUST register BEFORE general CONNECT handler)
proxy.OnRequest(goproxy.ReqHostMatches(githubHostsRegex)).HandleConnectFunc(
    func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
        return goproxy.MitmConnect, host
    })

proxy.OnRequest(goproxy.ReqHostMatches(githubHostsRegex)).DoFunc(
    func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
        repo := ExtractRepoFromPath(req.Host, req.URL.Path)
        if repo == "" {
            return req, nil // non-repo URL, pass through
        }
        if !IsRepoAllowed(repo, allowedRepos) {
            // log + return 403
            return req, GitHubBlockedResponse(req, sandboxID, repo)
        }
        return req, nil
    })

// General CONNECT (registered AFTER, handles non-GitHub hosts)
proxy.OnRequest().HandleConnectFunc(func(host string, ctx *goproxy.ProxyCtx) ...)
```

### Pattern 2: Single Regex Covering All GitHub Hosts

Rather than four separate regexes (one per host), a single regex covering all four target hosts is simpler and keeps registration order obvious. The Bedrock pattern uses one regex per service type; for GitHub we can consolidate since all hosts share the same blocking logic.

```go
// Source: proxy.go existing pattern (lines 30–34)
var githubHostsRegex = regexp.MustCompile(
    `^(github\.com|api\.github\.com|raw\.githubusercontent\.com|codeload\.githubusercontent\.com)(:\d+)?$`,
)
```

### Pattern 3: ExtractRepoFromPath — Host-Discriminated Extraction

Different GitHub hosts use different path structures. The extraction function takes both host and path:

```go
// Source: CONTEXT.md + analysis of GitHub URL patterns
func ExtractRepoFromPath(host, urlPath string) string {
    // Strip port from host (CONNECT uses host:port)
    h := strings.ToLower(stripPort(host))
    // Normalize: remove .git suffix, trim leading slash
    path := strings.TrimPrefix(urlPath, "/")
    segments := strings.SplitN(path, "/", 4)

    switch {
    case h == "api.github.com":
        // /repos/{owner}/{repo}[/...]
        if len(segments) >= 3 && segments[0] == "repos" {
            return normalizeRepo(segments[1] + "/" + segments[2])
        }
    default:
        // github.com, raw.githubusercontent.com, codeload.githubusercontent.com
        // /{owner}/{repo}[.git][/...]
        if len(segments) >= 2 {
            repo := strings.TrimSuffix(segments[1], ".git")
            return normalizeRepo(segments[0] + "/" + repo)
        }
    }
    return "" // non-repo URL, pass through
}

func normalizeRepo(r string) string {
    return strings.ToLower(strings.TrimSuffix(r, ".git"))
}
```

### Pattern 4: IsRepoAllowed — Org Wildcard Support

The allowedRepos list uses `owner/repo` format. Supporting `owner/*` wildcards is a discrete addition. The CONTEXT.md marks this as "consider" rather than locked, but it is cheap to implement at the same time.

```go
func IsRepoAllowed(repo string, allowed []string) bool {
    repo = strings.ToLower(repo)
    for _, a := range allowed {
        a = strings.ToLower(a)
        if strings.HasSuffix(a, "/*") {
            // org wildcard: "whereiskurt/*" matches "whereiskurt/anyrepo"
            org := strings.TrimSuffix(a, "/*")
            if strings.HasPrefix(repo, org+"/") {
                return true
            }
        } else if a == repo {
            return true
        }
    }
    return false
}
```

### Pattern 5: ProxyOption (WithGitHubRepoFilter)

Follow the `WithBudgetEnforcement` functional option pattern from `proxy.go`:

```go
// Source: proxy.go WithBudgetEnforcement pattern (lines 64–74)
func WithGitHubRepoFilter(allowedRepos []string) ProxyOption {
    return func(proxy *goproxy.ProxyHttpServer, cfg *proxyConfig) {
        cfg.githubRepos = allowedRepos
    }
}
```

Activation in `proxyConfig`:
```go
type proxyConfig struct {
    budget      *budgetEnforcementOptions
    githubRepos []string // nil means no GitHub repo filtering
}
```

When `cfg.githubRepos` is nil or empty, GitHub MITM handlers are NOT registered — the proxy falls back to host-level filtering only. This preserves backward compatibility for sandboxes without `sourceAccess.github` configured.

### Pattern 6: Blocked Response (mirrors BedrockBlockedResponse)

```go
// Source: bedrock.go BedrockBlockedResponse pattern (lines 160–170)
type githubBlockedResponseBody struct {
    Error  string `json:"error"`
    Repo   string `json:"repo"`
    Reason string `json:"reason"`
}

func GitHubBlockedResponse(req *http.Request, sandboxID, repo string) *http.Response {
    body := githubBlockedResponseBody{
        Error:  "repo_not_allowed",
        Repo:   repo,
        Reason: fmt.Sprintf("repo %q is not in allowedRepos for sandbox %s", repo, sandboxID),
    }
    encoded, _ := json.Marshal(body)
    return goproxy.NewResponse(req, "application/json", http.StatusForbidden, string(encoded))
}
```

### Pattern 7: Compiler Wiring

Two compiler touchpoints mirror the existing `AllowedHTTPHosts` pattern:

**EC2 — `pkg/compiler/userdata.go`:**
1. Add `GitHubAllowedRepos string` field to `userDataParams` struct (line ~625)
2. Populate from `strings.Join(p.Spec.SourceAccess.GitHub.AllowedRepos, ",")` (line ~704)
3. Add `Environment=KM_GITHUB_ALLOWED_REPOS={{ .GitHubAllowedRepos }}` to the `km-http-proxy.service` unit (line ~267)

**ECS — `pkg/compiler/service_hcl.go`:**
1. `GitHubAllowedRepos` is already present in `ECSServiceParams` as `GitHubAllowedRepos []string` (line 386) — but it currently stores the slice for the GitHub token Lambda, NOT for the proxy sidecar env var
2. Add a `GitHubAllowedReposCSV string` field to `ECSProxyParams` (a new or existing struct for the proxy container)
3. Add `{ name = "KM_GITHUB_ALLOWED_REPOS", value = "{{ .GitHubAllowedReposCSV }}" }` to the `km-http-proxy` container environment block (line ~189)

**Proxy `main.go`:**
```go
// Read KM_GITHUB_ALLOWED_REPOS (comma-separated) and pass to NewProxy
if allowedReposRaw := os.Getenv("KM_GITHUB_ALLOWED_REPOS"); allowedReposRaw != "" {
    var repos []string
    for _, r := range strings.Split(allowedReposRaw, ",") {
        r = strings.TrimSpace(r)
        if r != "" {
            repos = append(repos, r)
        }
    }
    proxyOpts = append(proxyOpts, httpproxy.WithGitHubRepoFilter(repos))
}
```

### Anti-Patterns to Avoid

- **Registering GitHub MITM after general CONNECT:** goproxy first-match means the general handler will swallow GitHub CONNECT requests before the GitHub MITM handler sees them. Always register GitHub before the general handler.
- **Blocking all non-repo GitHub URLs:** `api.github.com/rate_limit`, `github.com/login`, `github.com/session` etc. must pass through. Only block when `ExtractRepoFromPath` returns a non-empty string and that repo is not allowed.
- **Blocking when allowedRepos is empty/nil:** If `WithGitHubRepoFilter` receives an empty list, don't register handlers at all (deny-by-default is handled at the host level via `IsHostAllowed` — if `github.com` isn't in `allowedHosts`, the general handler already blocks it).
- **Case-sensitive matching:** GitHub usernames and repo names are case-insensitive. Always normalize to lowercase before comparing.
- **Matching `codeload.githubusercontent.com` with the same path regex as `github.com`:** This works since the URL structure is the same (`/{owner}/{repo}/...`), but needs explicit test coverage.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| TLS interception CA management | Custom cert generation code | `goproxy.MitmConnect` + existing `WithCustomCA` | goproxy already handles TLS cert generation for intercepted connections using the loaded CA |
| CONNECT tunnel interception | Raw TCP hijacking | `goproxy.HandleConnectFunc` returning `MitmConnect` | goproxy provides the full MITM machinery; the CA cert loaded via `WithCustomCA` is already trusted by the sandbox |
| Host regex matching | `strings.Contains` | `goproxy.ReqHostMatches(regexp)` | Built-in goproxy condition, same as Bedrock/Anthropic |
| 403 response construction | Manual `http.Response` | `goproxy.NewResponse(req, contentType, statusCode, body)` | Correctly sets all required fields for goproxy compatibility |

**Key insight:** The existing Bedrock/Anthropic MITM infrastructure — custom CA cert loaded at proxy start, `goproxy.MitmConnect`, `WithCustomCA` — is completely reusable for GitHub. No new CA infrastructure is needed.

---

## Common Pitfalls

### Pitfall 1: goproxy First-Match Semantics for HandleConnect

**What goes wrong:** GitHub CONNECT requests fall through to the general `OkConnect` handler, and URL path inspection never runs because goproxy establishes a pass-through tunnel before OnRequest fires.

**Why it happens:** `OnRequest.DoFunc` only fires for MITM-intercepted connections. If `HandleConnect` returns `OkConnect`, goproxy establishes a raw TLS tunnel — it never decrypts the HTTPS traffic, so `OnRequest` does not execute for that request.

**How to avoid:** Register `proxy.OnRequest(githubHostsRegex).HandleConnectFunc(...)` returning `goproxy.MitmConnect` BEFORE the general `proxy.OnRequest().HandleConnectFunc(...)`. This is already the pattern in `proxy.go` for Bedrock and Anthropic.

**Warning signs:** GitHub requests are allowed even when the repo is not in the allowlist; no `github_mitm_connect` log entries appear in proxy output.

### Pitfall 2: CONNECT Uses host:port, Not Just Hostname

**What goes wrong:** Regex `^github\.com$` does not match `github.com:443` as presented in the CONNECT request.

**Why it happens:** HTTPS CONNECT tunnels specify `Host: github.com:443` with the port included. `goproxy.ReqHostMatches` passes the full `host:port` to the regex.

**How to avoid:** Include optional port in the regex: `(:\d+)?$`. Alternatively use `goproxy.ReqHostMatches` which handles this internally — but verify against the actual goproxy behavior with the project's version (v1.8.2). The existing `bedrockHostRegex = regexp.MustCompile("^bedrock-runtime\\..+\\.amazonaws\\.com")` does NOT include a port qualifier but works because goproxy strips the port before matching — verify this assumption against goproxy source for v1.8.2.

**Warning signs:** GitHub CONNECT requests fall through to the general handler; `github_mitm_connect` never logs.

### Pitfall 3: URL Path in CONNECT vs Decrypted Request

**What goes wrong:** `req.URL.Path` is empty or `/` in the `HandleConnectFunc`; repo extraction fails silently.

**Why it happens:** During the CONNECT phase, `req.URL` contains only the target host (e.g., `github.com:443`), not the full URL path. The path becomes available in the SUBSEQUENT decrypted request after MITM intercepts the tunnel.

**How to avoid:** Path extraction happens in `OnRequest.DoFunc` (the decrypted request handler), NOT in `HandleConnectFunc`. `HandleConnectFunc` only needs to return `MitmConnect`. This is already the Bedrock pattern: `HandleConnectFunc` just does MITM, `OnRequest.DoFunc` inspects the decrypted request.

**Warning signs:** `ExtractRepoFromPath` always returns `""` causing all GitHub requests to pass through.

### Pitfall 4: Git-over-HTTPS Send Credentials Header

**What goes wrong:** Adding MITM to `github.com` causes git to reject the CA cert for HTTPS clone/fetch/push.

**Why it happens:** The custom CA cert must be trusted by the git client inside the sandbox. The bootstrap script already runs `update-ca-certificates` with the proxy CA cert installed at sandbox start.

**How to avoid:** No additional action needed — the existing CA infrastructure handles this. But verify in integration: `git clone https://github.com/...` via the proxy must work for allowed repos after MITM is added.

**Warning signs:** `git clone` fails with `SSL certificate problem: unable to get local issuer certificate`.

### Pitfall 5: allowedRepos Format Inconsistency

**What goes wrong:** Some profiles use `github.com/owner/repo` (full URL prefix) and others use `owner/repo` (short form). The test YAML `ec2-with-allowed-refs.yaml` uses `"github.com/myorg/myrepo"` while `ecs-with-github.yaml` uses `"myorg/myrepo"`.

**Why it happens:** The profile schema doesn't currently enforce a canonical format for `allowedRepos` entries.

**How to avoid:** Normalize `allowedRepos` entries during extraction — strip a leading `github.com/` prefix if present before comparison. `ExtractRepoFromPath` returns `owner/repo` format, so the normalization should happen in `IsRepoAllowed` or during the `WithGitHubRepoFilter` option setup.

**Warning signs:** Repos in `owner/repo` format in the allowlist don't match when the profile uses `github.com/owner/repo` format (or vice versa).

---

## Code Examples

### Example 1: GitHub MITM Handler Block in NewProxy

```go
// Source: proxy.go existing Bedrock pattern (lines 162–196) — mirror this exactly
// Inside NewProxy, BEFORE the general CONNECT handler:

if len(cfg.githubRepos) > 0 {
    gr := cfg.githubRepos

    // MITM: intercept CONNECT tunnels to GitHub hosts
    proxy.OnRequest(goproxy.ReqHostMatches(githubHostsRegex)).HandleConnectFunc(
        func(host string, ctx *goproxy.ProxyCtx) (*goproxy.ConnectAction, string) {
            log.Info().
                Str("event_type", "github_mitm_connect").
                Str("sandbox_id", sandboxID).
                Str("host", host).
                Msg("")
            return goproxy.MitmConnect, host
        })

    // OnRequest: inspect decrypted request, enforce repo allowlist
    proxy.OnRequest(goproxy.ReqHostMatches(githubHostsRegex)).DoFunc(
        func(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
            repo := ExtractRepoFromPath(req.Host, req.URL.Path)
            if repo == "" {
                return req, nil // non-repo URL (login, rate_limit, etc.) — pass through
            }
            if !IsRepoAllowed(repo, gr) {
                log.Info().
                    Str("event_type", "github_repo_blocked").
                    Str("sandbox_id", sandboxID).
                    Str("host", req.Host).
                    Str("repo", repo).
                    Msg("")
                return req, GitHubBlockedResponse(req, sandboxID, repo)
            }
            log.Info().
                Str("event_type", "github_repo_allowed").
                Str("sandbox_id", sandboxID).
                Str("repo", repo).
                Msg("")
            return req, nil
        })
}
```

### Example 2: Host Regex

```go
// Source: proxy.go bedrockHostRegex/anthropicHostRegex patterns (lines 30–34)
var githubHostsRegex = regexp.MustCompile(
    `^(github\.com|api\.github\.com|raw\.githubusercontent\.com|codeload\.githubusercontent\.com)(:\d+)?$`,
)
```

### Example 3: EC2 userdata systemd unit (userdata.go template)

```
cat > /etc/systemd/system/km-http-proxy.service << 'UNIT'
[Unit]
Description=Klankrmkr HTTP proxy sidecar
After=network.target
[Service]
User=km-sidecar
Environment=SANDBOX_ID={{ .SandboxID }}
Environment=ALLOWED_HOSTS={{ .AllowedHTTPHosts }}
Environment=KM_GITHUB_ALLOWED_REPOS={{ .GitHubAllowedRepos }}
Environment=PROXY_PORT=3128
ExecStart=/opt/km/bin/km-http-proxy
Restart=always
RestartSec=2
[Install]
WantedBy=multi-user.target
UNIT
```

### Example 4: ECS service_hcl.go container environment block

```
environment = [
  { name = "SANDBOX_ID",                 value = "{{ .SandboxID }}" },
  { name = "ALLOWED_HOSTS",              value = "{{ .AllowedHTTPHosts }}" },
  { name = "KM_GITHUB_ALLOWED_REPOS",    value = "{{ .GitHubAllowedReposCSV }}" },
  { name = "PROXY_PORT",                 value = "3128" },
]
```

### Example 5: Existing AllowedHTTPHosts wiring (compiler reference)

```go
// Source: pkg/compiler/userdata.go line 704 — existing pattern to follow
AllowedHTTPHosts: strings.Join(append(p.Spec.Network.Egress.AllowedHosts,
    p.Spec.Network.Egress.AllowedDNSSuffixes...), ","),

// New field follows same pattern:
GitHubAllowedRepos: joinGitHubAllowedRepos(p),
```

```go
func joinGitHubAllowedRepos(p *profile.SandboxProfile) string {
    if p.Spec.SourceAccess.GitHub == nil {
        return ""
    }
    return strings.Join(p.Spec.SourceAccess.GitHub.AllowedRepos, ",")
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Host-level GitHub allowlist only | Host + repo path inspection via MITM | Phase 28 | Closes gap: sandboxes can no longer access repos outside `allowedRepos` even if `github.com` is in `allowedHosts` |
| No GitHub-specific MITM | GitHub MITM using same CA infrastructure as Bedrock | Phase 28 | Reuses existing CA cert, no new infrastructure |

**Not changing:**
- Token passthrough behavior — gh client / git credential helper tokens on requests are unaffected
- Token injection — still deferred to a future phase
- Ref enforcement — still handled via git hooks (not proxy)

---

## Open Questions

1. **goproxy port stripping in ReqHostMatches**
   - What we know: `bedrockHostRegex` does not include `(:\d+)?` but Bedrock MITM works in production
   - What's unclear: Whether goproxy v1.8.2 strips port before passing to `ReqHostMatches`, or whether `bedrock-runtime.us-east-1.amazonaws.com` never presents with a port in CONNECT
   - Recommendation: Add `(:\d+)?$` to the GitHub regex defensively; test with a raw CONNECT to `github.com:443` in the test suite

2. **allowedRepos format normalization (github.com/ prefix)**
   - What we know: `ec2-with-allowed-refs.yaml` uses `"github.com/myorg/myrepo"` while `ecs-with-github.yaml` uses `"myorg/myrepo"`
   - What's unclear: Whether existing profiles in real deployments use the long or short form
   - Recommendation: Normalize in `IsRepoAllowed` — strip a leading `github.com/` prefix before comparison; add test coverage for both formats

3. **Org wildcard (`owner/*`) — include or defer**
   - What we know: CONTEXT.md marks this as "consider supporting"
   - What's unclear: Whether any current profiles need wildcard access
   - Recommendation: Implement since it adds minimal complexity at authoring time and is harder to retrofit; test with `"whereiskurt/*"` pattern

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing stdlib (`go test`) |
| Config file | none — standard `go test ./...` |
| Quick run command | `go test ./sidecars/http-proxy/httpproxy/... -run TestGitHub -v` |
| Full suite command | `go test ./sidecars/http-proxy/... ./pkg/compiler/...` |

### Phase Requirements → Test Map

| Behavior | Test Type | Automated Command | File Exists? |
|----------|-----------|-------------------|-------------|
| `ExtractRepoFromPath` — github.com paths | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestExtractRepoFromPath` | ❌ Wave 0 |
| `ExtractRepoFromPath` — api.github.com paths | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestExtractRepoFromPath` | ❌ Wave 0 |
| `ExtractRepoFromPath` — non-repo URLs return "" | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestExtractRepoFromPath` | ❌ Wave 0 |
| `IsRepoAllowed` — exact match | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestIsRepoAllowed` | ❌ Wave 0 |
| `IsRepoAllowed` — org wildcard | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestIsRepoAllowed` | ❌ Wave 0 |
| `IsRepoAllowed` — case-insensitive | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestIsRepoAllowed` | ❌ Wave 0 |
| `IsRepoAllowed` — github.com/ prefix normalization | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestIsRepoAllowed` | ❌ Wave 0 |
| `GitHubBlockedResponse` — 403 with JSON body | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestGitHubBlockedResponse` | ❌ Wave 0 |
| NewProxy — allowed repo passes through | integration | `go test ./sidecars/http-proxy/httpproxy/... -run TestHTTPProxy_GitHub` | ❌ Wave 0 |
| NewProxy — blocked repo returns 403 | integration | `go test ./sidecars/http-proxy/httpproxy/... -run TestHTTPProxy_GitHub` | ❌ Wave 0 |
| NewProxy — no githubRepos (nil) — no MITM registered | unit | `go test ./sidecars/http-proxy/httpproxy/... -run TestHTTPProxy_GitHub` | ❌ Wave 0 |
| Compiler EC2 — GitHubAllowedRepos in userdata | unit | `go test ./pkg/compiler/... -run TestUserData.*GitHub` | ❌ Wave 0 |
| Compiler ECS — KM_GITHUB_ALLOWED_REPOS in service.hcl | unit | `go test ./pkg/compiler/... -run TestCompileECS.*GitHub` | ❌ Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./sidecars/http-proxy/httpproxy/... -run TestGitHub`
- **Per wave merge:** `go test ./sidecars/http-proxy/... ./pkg/compiler/...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `sidecars/http-proxy/httpproxy/github.go` — `ExtractRepoFromPath`, `IsRepoAllowed`, `GitHubBlockedResponse`, `WithGitHubRepoFilter`, regex
- [ ] `sidecars/http-proxy/httpproxy/github_test.go` — unit tests for all github.go functions
- [ ] New integration test functions in `sidecars/http-proxy/httpproxy/http_proxy_test.go` — `TestHTTPProxy_GitHubAllowed`, `TestHTTPProxy_GitHubBlocked`, `TestHTTPProxy_GitHubNonRepoPassthrough`
- [ ] Compiler test data: `pkg/compiler/testdata/ec2-with-github-repos.yaml`, `pkg/compiler/testdata/ecs-with-github-repos.yaml` (may already exist partially — `ecs-with-github.yaml` covers some)

---

## Sources

### Primary (HIGH confidence)
- Direct code inspection: `sidecars/http-proxy/httpproxy/proxy.go` — full MITM pattern for Bedrock/Anthropic
- Direct code inspection: `sidecars/http-proxy/httpproxy/bedrock.go` — `ExtractModelID`, `BedrockBlockedResponse` patterns
- Direct code inspection: `sidecars/http-proxy/httpproxy/anthropic.go` — `AnthropicBlockedResponse`, `staticAnthropicRates` patterns
- Direct code inspection: `sidecars/http-proxy/main.go` — env var reading pattern (`ALLOWED_HOSTS`, `KM_BUDGET_ENABLED`, `KM_PROXY_CA_CERT`)
- Direct code inspection: `pkg/compiler/userdata.go` — `AllowedHTTPHosts` field and systemd unit template
- Direct code inspection: `pkg/compiler/service_hcl.go` — ECS container environment block, `GitHubAllowedRepos []string` field
- Direct code inspection: `pkg/profile/types.go` — `GitHubAccess.AllowedRepos []string`
- Direct code inspection: `go.mod` — `github.com/elazarl/goproxy v1.8.2`, no new dependencies needed
- `go doc github.com/elazarl/goproxy ProxyHttpServer.OnRequest` — confirmed first-match semantics doc

### Secondary (MEDIUM confidence)
- `28-CONTEXT.md` — locked design decisions from conversation research; authoritative for this project

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — code already uses all required libraries; no new deps
- Architecture: HIGH — exact pattern defined by existing Bedrock/Anthropic MITM code; differences are path extraction logic only
- Pitfalls: HIGH — most pitfalls derived from direct code inspection; one (goproxy port stripping) is a LOW-confidence assumption flagged as open question

**Research date:** 2026-03-28
**Valid until:** 2026-09-28 (stable — goproxy API is stable; GitHub URL paths are stable)
