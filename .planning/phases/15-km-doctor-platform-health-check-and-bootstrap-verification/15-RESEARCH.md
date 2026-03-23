# Phase 15: km doctor — Platform Health Check and Bootstrap Verification - Research

**Researched:** 2026-03-22
**Domain:** Go CLI health-check command design, AWS SDK parallel probes, GitHub App manifest flow
**Confidence:** HIGH

---

## Summary

Phase 15 introduces two new capabilities: `km doctor` (a structured platform health check) and `km configure github --setup` (a one-click GitHub App manifest flow). Both build exclusively on patterns and packages already established in the codebase — no new external Go dependencies are required. The doctor command is architecturally similar to `km status` but broader in scope (platform-wide vs per-sandbox) and shares the same colored terminal output approach (`isTerminal` + ANSI constants already defined in `status.go`).

The most important architectural decision is **check isolation**: each check runs independently and non-fatally, even when AWS calls fail. Checks that can run concurrently (credential checks for different profiles, per-region checks) use goroutines with a result channel to fan out and collect. The `--json` flag outputs a structured array that the `--quiet` flag filters; the exit code is driven by the presence of any `ERROR` status results.

For the GitHub App manifest flow (`km configure github --setup`), the GitHub API is already used in `pkg/github/token.go` and the SSM write pattern is in `configure_github.go`. The manifest flow adds a browser-open step, an HTTP server to receive the `code` callback (or prompt for manual code entry), and a `POST /app-manifests/{code}/conversions` exchange. After exchange, it calls the same `runConfigureGitHub` logic already in `configure_github.go` — no code duplication.

The two new AWS SDK packages required (`kms` v1.50.3, `organizations` v1.50.5) have already been added to `go.mod` as indirect dependencies during research. They must be promoted to direct in the plan.

**Primary recommendation:** Implement `km doctor` as a single file `internal/app/cmd/doctor.go` with a `CheckResult` struct, parallel check execution via `sync.WaitGroup` + mutex-protected slice, and a `runDoctor()` function that aggregates results. The manifest flow belongs in a new `runConfigureGitHubSetup()` function in `configure_github.go`, appended to the existing `--setup` subcommand flag on the `km configure github` command.

---

## Standard Stack

### Core (zero new external dependencies)
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/aws/aws-sdk-go-v2/service/sts` | v1.41.9 | `GetCallerIdentity` for credential checks | Already imported in `pkg/aws/client.go`; `ValidateCredentials()` already exists |
| `github.com/aws/aws-sdk-go-v2/service/s3` | v1.97.1 | `HeadBucket` for state bucket check | Already imported in `pkg/aws/sandbox.go` |
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | v1.57.0 | `DescribeTable` for lock table + budget table + identity table | Already imported in `pkg/aws/budget.go` |
| `github.com/aws/aws-sdk-go-v2/service/kms` | v1.50.3 | `DescribeKey` by alias for KMS key check | Added to go.mod during research |
| `github.com/aws/aws-sdk-go-v2/service/organizations` | v1.50.5 | `ListPoliciesForTarget` for SCP check | Added to go.mod during research |
| `github.com/aws/aws-sdk-go-v2/service/ec2` | v1.296.0 | `DescribeVpcs` + `DescribeSubnets` for per-region VPC check | Already imported for EC2 substrate |
| `github.com/aws/aws-sdk-go-v2/service/ssm` | v1.68.3 | `GetParameter` for GitHub App config check | Already imported in `configure_github.go` |
| `sync` | stdlib | `WaitGroup` for parallel checks | No new dependency |
| `net/http` | stdlib | Local callback server for manifest code exchange | Already used in `pkg/github/token.go` |
| `os/exec` | stdlib | `open`/`xdg-open`/`start` for browser open | Already used in shell.go test infra |
| `encoding/json` | stdlib | `--json` output and manifest exchange response | Already used throughout |

### AWS SDK Packages — Key Operations

| Check | Service | Operation | Notes |
|-------|---------|-----------|-------|
| Credential (management) | STS | `GetCallerIdentity` | Profile: `klanker-management` |
| Credential (terraform) | STS | `GetCallerIdentity` | Profile: `klanker-terraform` |
| Credential (application) | STS | `GetCallerIdentity` | Profile: `klanker-application` |
| State bucket | S3 | `HeadBucket` | `cfg.StateBucket` |
| DynamoDB lock table | DynamoDB | `DescribeTable` | `km-terraform-lock` |
| KMS key | KMS | `DescribeKey` | `alias/km-terraform-state` |
| SCP policy | Organizations | `ListPoliciesForTarget` | Target: application account OU; management creds only |
| GitHub App config | SSM | `GetParameter` | `/km/config/github/app-id`, `/km/config/github/installation-id` |
| VPC (per region) | EC2 | `DescribeVpcs` | Tag filter: `km:managed=true` |
| Subnets (per region) | EC2 | `DescribeSubnets` | Filter: VPC ID from above |
| Budget DynamoDB | DynamoDB | `DescribeTable` | `cfg.BudgetTableName` (default: `km-budgets`) |
| Identity DynamoDB | DynamoDB | `DescribeTable` | `km-identities` |

**No new `go get` required** — all packages are now in `go.mod`. The kms and organizations packages need to be promoted from `// indirect` to direct imports in `go.mod` by the implementation plan.

---

## Architecture Patterns

### Recommended File Structure

```
internal/app/cmd/
├── doctor.go           # New: km doctor command + CheckResult + runDoctor
├── doctor_test.go      # New: unit tests with fake check functions
configure_github.go     # Extended: --setup flag + runConfigureGitHubSetup()
```

No new packages. All code in `internal/app/cmd` following the established pattern.

### Pattern 1: CheckResult Struct and Status Constants

Every check produces a `CheckResult`. The aggregator collects these and renders them.

```go
// CheckStatus is the result classification for a single doctor check.
type CheckStatus string

const (
    CheckOK      CheckStatus = "OK"
    CheckWarn    CheckStatus = "WARN"
    CheckError   CheckStatus = "ERROR"
    CheckSkipped CheckStatus = "SKIPPED"
)

// CheckResult is the output of a single doctor check.
type CheckResult struct {
    Name        string      `json:"name"`
    Status      CheckStatus `json:"status"`
    Message     string      `json:"message"`
    Remediation string      `json:"remediation,omitempty"`
}
```

**Why this shape:** The `--json` output maps directly to a JSON array of `CheckResult`. The `--quiet` flag filters on `Status != CheckOK`. Exit code is 1 if any result is `CheckError`.

### Pattern 2: Parallel Check Execution

Fan-out with goroutines, collect into slice via mutex, sort for stable output:

```go
func runChecks(ctx context.Context, checks []func(context.Context) CheckResult) []CheckResult {
    var mu sync.Mutex
    var results []CheckResult
    var wg sync.WaitGroup

    for _, check := range checks {
        wg.Add(1)
        go func(fn func(context.Context) CheckResult) {
            defer wg.Done()
            r := fn(ctx)
            mu.Lock()
            results = append(results, r)
            mu.Unlock()
        }(check)
    }
    wg.Wait()
    // Sort for stable output order
    sort.Slice(results, func(i, j int) bool {
        return results[i].Name < results[j].Name
    })
    return results
}
```

**Why goroutines not errgroup:** Checks are independent and non-fatal — one failing must not cancel others. `errgroup` cancels on first error. A plain `WaitGroup` is correct here.

**Parallelization grouping:**
- Wave 1 (fully parallel): all three credential checks
- Wave 2 (parallel, after creds confirmed): bootstrap checks (S3, DynamoDB, KMS), SCP check, GitHub App check, sandbox list
- Wave 3 (parallel): per-region checks (one goroutine per region)

In practice, all checks can be launched in a single fan-out — failed credential checks produce `CheckError` results but don't block other checks. The implementation launches all checks together.

### Pattern 3: TTY-Aware Colored Output

Reuse the existing `isTerminal` helper in `status.go` (same package). Define check symbols as constants, applying color only when TTY:

```go
func formatCheckLine(r CheckResult, isTTY bool) string {
    sym, color := symbolFor(r.Status, isTTY)
    line := fmt.Sprintf("  %s %s: %s", sym, r.Name, r.Message)
    if r.Remediation != "" {
        line += "\n    -> " + r.Remediation
    }
    return color + line + resetIf(isTTY)
}
```

Symbols: `checkOKSymbol = "✓"`, `checkWarnSymbol = "⚠"`, `checkErrorSymbol = "✗"`, `checkSkippedSymbol = "-"`.

Colors: green for OK, yellow for WARN, red for ERROR, no color for SKIPPED.

**Key pattern from status.go:** The ANSI constants (`ansiGreen`, `ansiYellow`, `ansiRed`, `ansiReset`) are already defined at package scope in `status.go`. Doctor.go is in the same package, so it reuses them directly without redeclaring.

### Pattern 4: DI for Testability

Every check function that calls AWS must accept injected clients via a `DoctorDeps` struct:

```go
type DoctorDeps struct {
    STSManagement   STSCallerAPI     // nil => use real AWS
    STSTerraform    STSCallerAPI
    STSApplication  STSCallerAPI
    S3Client        S3HeadBucketAPI
    DynamoClient    DynamoDescribeAPI
    KMSClient       KMSDescribeAPI
    OrgsClient      OrgsListPoliciesAPI
    SSMReadClient   SSMReadAPI
    EC2Clients      map[string]EC2DescribeAPI // keyed by region
    Lister          SandboxLister // reuse from list.go
}
```

Real clients are initialized in `RunE` when deps are nil. Tests inject fakes.

**New narrow interfaces needed:**

```go
type STSCallerAPI interface {
    GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput, ...) (*sts.GetCallerIdentityOutput, error)
}
type S3HeadBucketAPI interface {
    HeadBucket(ctx context.Context, in *s3.HeadBucketInput, ...) (*s3.HeadBucketOutput, error)
}
type DynamoDescribeAPI interface {
    DescribeTable(ctx context.Context, in *dynamodb.DescribeTableInput, ...) (*dynamodb.DescribeTableOutput, error)
}
type KMSDescribeAPI interface {
    DescribeKey(ctx context.Context, in *kms.DescribeKeyInput, ...) (*kms.DescribeKeyOutput, error)
}
type OrgsListPoliciesAPI interface {
    ListPoliciesForTarget(ctx context.Context, in *organizations.ListPoliciesForTargetInput, ...) (*organizations.ListPoliciesForTargetOutput, error)
}
type SSMReadAPI interface {
    GetParameter(ctx context.Context, in *ssm.GetParameterInput, ...) (*ssm.GetParameterOutput, error)
}
type EC2DescribeAPI interface {
    DescribeVpcs(ctx context.Context, in *ec2.DescribeVpcsInput, ...) (*ec2.DescribeVpcsOutput, error)
    DescribeSubnets(ctx context.Context, in *ec2.DescribeSubnetsInput, ...) (*ec2.DescribeSubnetsOutput, error)
}
```

### Pattern 5: GitHub App Manifest Flow

The `km configure github --setup` flag adds a new code path to the existing `configure_github.go` file. The flow:

1. Build manifest JSON struct with required fields (name, url, permissions, no webhook, no events).
2. URL-encode the manifest JSON as a form parameter.
3. Open browser to `https://github.com/settings/apps/new?manifest=<encoded>`.
4. Start a local HTTP server on a random port (`:0`) to receive the callback at `/github-app-setup`.
5. Wait for the `code` query parameter in the callback (timeout: 5 minutes).
6. POST to `https://api.github.com/app-manifests/{code}/conversions` with empty body, `Accept: application/vnd.github+json`.
7. Parse response: `id` (App ID, int64), `pem` (private key, string), `client_id` (string), `webhook_secret` (string), `html_url` (string).
8. Call `runConfigureGitHub()` with the extracted `client_id`, PEM written to a temp file, and installation ID (obtained by calling `GET /app/installations` with the App JWT — or prompt the operator if no installations found yet).
9. Shut down the local server.

**GitHub manifest JSON minimum fields:**
```json
{
  "name": "klanker-maker-sandbox",
  "url": "https://github.com/whereiskurt/klankrmkr",
  "public": false,
  "default_permissions": {
    "contents": "write"
  },
  "hook_attributes": {"url": "https://example.com", "active": false}
}
```

Note: `hook_attributes.url` is documented as required in the manifest object even if webhooks are not used. Setting `active: false` and using a placeholder URL disables webhook delivery without GitHub rejecting the manifest.

**Browser open function** (cross-platform):
```go
func openBrowser(url string) error {
    var cmd string
    var args []string
    switch runtime.GOOS {
    case "darwin":
        cmd, args = "open", []string{url}
    case "linux":
        cmd, args = "xdg-open", []string{url}
    case "windows":
        cmd, args = "cmd", []string{"/c", "start", url}
    default:
        return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
    }
    return exec.Command(cmd, args...).Start()
}
```

**Manifest conversion API response** (from GitHub docs + source verification via WebFetch):
- `id` (int64) — GitHub App ID (numeric, used for JWT `iss` claim)
- `client_id` (string) — GitHub App client ID (e.g. `Iv1.abc123`)
- `pem` (string) — RSA private key in PEM format
- `webhook_secret` (string) — auto-generated webhook secret
- `html_url` (string) — URL of the created App
- `installations_count` (int) — number of installations

**SSM parameter stored:** After manifest exchange, the same three SSM parameters as `km configure github` are written:
- `/km/config/github/app-client-id` — `client_id` from response
- `/km/config/github/private-key` — `pem` from response (SecureString)
- `/km/config/github/installation-id` — obtained post-creation (operator may need to install the App manually before this is available)

**Installation ID retrieval:** Call `GET https://api.github.com/app/installations` using App JWT. If one installation exists, use it. If zero, print instructions and skip SSM write for installation-id. If multiple, prompt operator to select.

### Anti-Patterns to Avoid

- **Canceling all checks when one fails:** Use `WaitGroup`, not `errgroup` — independence is required.
- **Blocking output:** Don't buffer all output until checks complete; but for the current design (parallel checks), output is written after all complete for cleaner formatting. Streaming output per check is not needed.
- **Hardcoding profile names:** The three AWS profiles (`klanker-management`, `klanker-terraform`, `klanker-application`) are the project convention, but they should be constants or derived from config, not magic strings scattered across checks.
- **Nil pointer panic in parallel goroutines:** Every check function must recover from panics or handle AWS SDK nil-client gracefully. The `CheckError` result is always safe to return.
- **Browser blocking:** `exec.Command(openCmd).Start()` (not `.Run()`) — don't block waiting for browser to close.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| TTY detection | Custom `isatty` syscall code | `isTerminal(w io.Writer)` already in `status.go` (same package) | Already handles `*os.File` character device check |
| ANSI colors | Color library (`fatih/color`, etc.) | `ansiGreen/Yellow/Red/Reset` constants already in `status.go` | Package-scoped; doctor.go in same package sees them directly |
| Parallel fan-out | Custom worker pool | `sync.WaitGroup` + mutex slice | This use case is textbook `WaitGroup`; no need for errgroup or channels |
| SSM write to store GitHub creds | New SSM helper | `putSSMParam()` already in `configure_github.go` | Identical signature; reuse directly |
| Manifest JSON encoding | Custom serialization | `encoding/json.Marshal` | Standard; struct with json tags |
| Browser open | Shell script or os.StartProcess | `openBrowser()` helper using `exec.Command` | 3 lines; covers all three OSes |
| GitHub API HTTP calls | SDK | `net/http` with `http.NewRequestWithContext` | GitHub has no official Go SDK worth adding; `pkg/github/token.go` already uses raw `http.Client` |

---

## Common Pitfalls

### Pitfall 1: SCP Check Requires Management Credentials
**What goes wrong:** `organizations:ListPoliciesForTarget` is only callable from the management account. If management credentials are absent or unconfigured, the call fails.
**Why it happens:** Organizations API is management-account-only.
**How to avoid:** If `cfg.ManagementAccountID == ""` or management credential check failed, skip the SCP check and return `CheckSkipped` with message "management credentials not configured".
**Warning signs:** `AccessDeniedException` from Organizations; check status.go for precedent — the bootstrap command already handles this via `if loadedCfg.ManagementAccountID != ""`.

### Pitfall 2: KMS Key Check Needs Correct Alias Format
**What goes wrong:** `kms:DescribeKey` with alias input requires the prefix `alias/` — passing `km-terraform-state` without the prefix returns `NotFoundException`.
**Why it happens:** KMS accepts both key IDs and alias ARNs; alias names require the `alias/` prefix.
**How to avoid:** Use `alias/km-terraform-state` as the `KeyId` parameter value.

### Pitfall 3: GitHub Manifest Webhook URL is Required Even If Unused
**What goes wrong:** GitHub rejects manifest creation if `hook_attributes.url` is empty or absent.
**Why it happens:** GitHub's manifest schema requires a webhook URL field even when `active: false`.
**How to avoid:** Include `hook_attributes: {url: "https://example.com", active: false}` in the manifest JSON. The placeholder URL is never called when inactive.

### Pitfall 4: Manifest Conversion Race — Code Expires in 1 Hour
**What goes wrong:** If the operator is slow to click "Create GitHub App" in the browser, the `code` in the callback expires.
**Why it happens:** GitHub temporary codes expire after 1 hour.
**How to avoid:** Show a clear prompt with the timeout. The local HTTP server should timeout after 5 minutes with a friendly error suggesting the operator re-run `--setup`.

### Pitfall 5: Per-Region EC2 Checks Need Region-Specific AWS Configs
**What goes wrong:** `LoadAWSConfig` in `pkg/aws/client.go` hardcodes `us-east-1`. A per-region VPC check on `us-west-2` gets the wrong region.
**Why it happens:** `const awsRegion = "us-east-1"` in `pkg/aws/client.go`.
**How to avoid:** Load a second AWS config with the target region using `config.WithRegion(region)` override. The region check must use `awssdk.Config` with the correct region, not the shared one from `LoadAWSConfig`.

### Pitfall 6: Identity Table Not Necessarily Deployed
**What goes wrong:** Phase 14 adds `km-identities` DynamoDB table, but it may not be deployed on all environments. `DescribeTable` returns `ResourceNotFoundException` for a missing table.
**Why it happens:** Not all operators will have run Phase 14 infrastructure.
**How to avoid:** Report `CheckWarn` (not `CheckError`) if `km-identities` does not exist. Message: "Identity table km-identities not found — run 'km bootstrap' or deploy Phase 14 infra to enable signed email."

### Pitfall 7: `--json` + `--quiet` Flag Interaction
**What goes wrong:** `--quiet` in JSON mode could suppress JSON output entirely, or output partial arrays.
**How to avoid:** In JSON mode, `--quiet` still outputs the full array but only includes non-OK entries. This is consistent with CI usage where the full machine-readable report is expected but filtered.

### Pitfall 8: Exit Code Must Flow Through Cobra
**What goes wrong:** Cobra's `RunE` returns `error` — returning nil always exits 0. But doctor needs exit code 1 on errors even when the human-readable report was printed cleanly.
**How to avoid:** If any result has `Status == CheckError`, return a sentinel error from `RunE` that Cobra propagates: `fmt.Errorf("platform health check failed: %d error(s) found", errorCount)`. Cobra's `SilenceUsage: true` prevents usage from being printed for this error.

---

## Code Examples

### Check Function Signature Pattern
```go
// Source: established in this phase; follows SandboxFetcher pattern from list.go
func checkStateBucket(ctx context.Context, client S3HeadBucketAPI, bucketName string) CheckResult {
    if client == nil || bucketName == "" {
        return CheckResult{
            Name:        "bootstrap/state-bucket",
            Status:      CheckSkipped,
            Message:     "state bucket not configured (set KM_STATE_BUCKET)",
        }
    }
    _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
        Bucket: aws.String(bucketName),
    })
    if err != nil {
        return CheckResult{
            Name:        "bootstrap/state-bucket",
            Status:      CheckError,
            Message:     fmt.Sprintf("bucket %s not accessible: %v", bucketName, err),
            Remediation: "Run 'km bootstrap --dry-run=false' to provision the state bucket",
        }
    }
    return CheckResult{
        Name:    "bootstrap/state-bucket",
        Status:  CheckOK,
        Message: fmt.Sprintf("bucket %s accessible", bucketName),
    }
}
```

### Credential Check Pattern (STS)
```go
// Source: extends ValidateCredentials() in pkg/aws/client.go
func checkCredential(ctx context.Context, client STSCallerAPI, profile string) CheckResult {
    name := "credentials/" + profile
    if client == nil {
        return CheckResult{Name: name, Status: CheckSkipped, Message: "no client provided"}
    }
    out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
    if err != nil {
        return CheckResult{
            Name:        name,
            Status:      CheckError,
            Message:     fmt.Sprintf("auth failure: %v", err),
            Remediation: fmt.Sprintf("Run 'aws sso login --profile %s' and retry", profile),
        }
    }
    return CheckResult{
        Name:    name,
        Status:  CheckOK,
        Message: fmt.Sprintf("authenticated as %s (account %s)", aws.ToString(out.Arn), aws.ToString(out.Account)),
    }
}
```

### JSON Output Pattern
```go
// Source: follows list.go json output pattern
if jsonOutput {
    filtered := results
    if quietMode {
        filtered = filterNonOK(results)
    }
    return json.NewEncoder(cmd.OutOrStdout()).Encode(filtered)
}
```

### Cobra Command Shape
```go
func NewDoctorCmd(cfg *config.Config) *cobra.Command {
    return NewDoctorCmdWithDeps(cfg, nil)
}

func NewDoctorCmdWithDeps(cfg *config.Config, deps *DoctorDeps) *cobra.Command {
    var jsonOutput bool
    var quietMode bool

    cmd := &cobra.Command{
        Use:          "doctor",
        Short:        "Check platform health and bootstrap verification",
        Long:         helpText("doctor"),
        SilenceUsage: true,
        RunE: func(cmd *cobra.Command, args []string) error {
            return runDoctor(cmd, cfg, deps, jsonOutput, quietMode)
        },
    }
    cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output as JSON array of check results")
    cmd.Flags().BoolVar(&quietMode, "quiet", false, "Show only failures and warnings")
    return cmd
}
```

### Manifest Flow — Local Callback Server
```go
// Source: pattern from net/http stdlib; follows httptest server approach
func receiveManifestCode(ctx context.Context, timeout time.Duration) (string, int, error) {
    mux := http.NewServeMux()
    codeCh := make(chan string, 1)

    mux.HandleFunc("/github-app-setup", func(w http.ResponseWriter, r *http.Request) {
        code := r.URL.Query().Get("code")
        if code != "" {
            codeCh <- code
            fmt.Fprintf(w, "<h1>GitHub App created. Return to your terminal.</h1>")
        }
    })

    srv := &http.Server{Addr: ":0", Handler: mux}
    ln, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        return "", 0, fmt.Errorf("listen for callback: %w", err)
    }
    port := ln.Addr().(*net.TCPAddr).Port
    go srv.Serve(ln) //nolint:errcheck

    ctx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()

    select {
    case code := <-codeCh:
        _ = srv.Shutdown(context.Background())
        return code, port, nil
    case <-ctx.Done():
        _ = srv.Shutdown(context.Background())
        return "", port, fmt.Errorf("timed out waiting for GitHub App creation (5m)")
    }
}
```

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Manual GitHub App registration (copy/paste App ID + PEM file) | Manifest flow (`--setup`) opens browser, exchanges code, writes SSM automatically | GitHub manifest API introduced 2018; this project adds it in Phase 15 | Zero manual copy-paste |
| Sequential health checks (one AWS call at a time) | Parallel goroutines for independent checks | Standard Go concurrency idiom | Sub-second total check time vs. 3-5s serial |
| Exit code 0 always (operators must read output) | Exit code 1 on any CheckError | Phase 15 | Enables `km doctor && km create` CI gating |

---

## Open Questions

1. **Organizations API target: OU ID vs Account ID**
   - What we know: `organizations:ListPoliciesForTarget` takes a `TargetId` which can be an account ID or OU ID.
   - What's unclear: The km SCP is attached to the OU containing the application account, not directly to the account. The OU ID is not stored in `km-config.yaml`.
   - Recommendation: Use the Application account ID as the target ID for `ListPoliciesForTarget` — SCPs attached to the account directly or to a parent OU are returned. Alternatively, filter `ListPolicies(Filter: SERVICE_CONTROL_POLICY)` for `km-sandbox-containment` and verify it exists. The simpler check (just verify the policy exists in the org) may be more reliable than verifying attachment.

2. **Installation ID retrieval after manifest exchange**
   - What we know: After creating the App via manifest, the operator must install it on their org before an installation ID exists. The conversion API response does not include an installation ID.
   - What's unclear: Whether to block in `--setup` waiting for installation, or write SSM without installation-id and require a follow-up `km configure github` call.
   - Recommendation: After manifest exchange and App creation, check `GET /app/installations` (requires App JWT). If installations exist, use the first one. If none, print instructions ("Install the App at https://github.com/apps/klanker-maker-sandbox and run 'km configure github --installation-id <ID>'") and exit 0. Do not block waiting.

3. **Per-region regions list source**
   - What we know: The phase spec says "each initialized region." The config currently only has `PrimaryRegion`. Replica region is read from `KM_REPLICA_REGION` env var (Phase 12 decision).
   - What's unclear: Whether `km doctor` should check only `PrimaryRegion`, or both `PrimaryRegion` + `KM_REPLICA_REGION`, or read from `infra/live/` directory structure.
   - Recommendation: Check `cfg.PrimaryRegion` always. If `KM_REPLICA_REGION` is set, also check that region. Reading `infra/live/` directory structure is fragile — use env vars.

---

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing` package |
| Config file | none (no separate config; uses `go test ./...`) |
| Quick run command | `go test ./internal/app/cmd/ -run TestDoctor -v` |
| Full suite command | `go test ./...` |

### Phase Requirements to Test Map

| Behavior | Test Type | Automated Command | File Exists? |
|----------|-----------|-------------------|-------------|
| `km doctor` command registered in root | unit (compile-time) | `go test ./internal/app/cmd/ -run TestDoctorCmd_CommandShape` | Wave 0 |
| `--json` flag produces valid JSON array | unit | `go test ./internal/app/cmd/ -run TestDoctorCmd_JSONOutput` | Wave 0 |
| `--quiet` flag suppresses OK results | unit | `go test ./internal/app/cmd/ -run TestDoctorCmd_QuietMode` | Wave 0 |
| Exit code 1 when any check is ERROR | unit | `go test ./internal/app/cmd/ -run TestDoctorCmd_ExitCodeOnError` | Wave 0 |
| Failed credential check returns CheckError | unit | `go test ./internal/app/cmd/ -run TestCheckCredential_Failure` | Wave 0 |
| Missing state bucket returns CheckError with remediation | unit | `go test ./internal/app/cmd/ -run TestCheckStateBucket_Missing` | Wave 0 |
| Missing GitHub SSM params returns CheckSkipped/WARN, not ERROR | unit | `go test ./internal/app/cmd/ -run TestCheckGitHubConfig_NotConfigured` | Wave 0 |
| SCP check skipped when management creds absent | unit | `go test ./internal/app/cmd/ -run TestCheckSCP_SkippedWhenNoCreds` | Wave 0 |
| `km configure github --setup` flag registered | unit | `go test ./internal/app/cmd/ -run TestConfigureGitHubSetup_FlagRegistered` | Wave 0 |
| Per-region check uses correct region | unit | `go test ./internal/app/cmd/ -run TestCheckRegion_UsesCorrectRegion` | Wave 0 |
| Identity table missing returns WARN not ERROR | unit | `go test ./internal/app/cmd/ -run TestCheckIdentityTable_MissingIsWarn` | Wave 0 |

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/ -run TestDoctor -v`
- **Per wave merge:** `go test ./...`
- **Phase gate:** Full suite green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `internal/app/cmd/doctor_test.go` — covers all check unit tests listed above
- [ ] `internal/app/cmd/help/doctor.txt` — help text file (follows existing pattern)

---

## Sources

### Primary (HIGH confidence)
- `pkg/aws/client.go` — `ValidateCredentials()` pattern; `LoadAWSConfig()` region limitation
- `internal/app/cmd/status.go` — `isTerminal()`, ANSI constants, `BudgetFetcher` DI pattern
- `internal/app/cmd/list.go` — `SandboxLister` interface; JSON output; `--json` flag pattern
- `internal/app/cmd/configure_github.go` — SSM write pattern; `putSSMParam()` helper; DI constructor pattern
- `internal/app/cmd/bootstrap.go` — Management account conditional logic; config validation flow
- `go.mod` — dependency versions; kms v1.50.3 and organizations v1.50.5 added during research
- `pkg/github/token.go` — raw HTTP GitHub API client pattern; `GitHubAPIBaseURL` var for test injection

### Secondary (MEDIUM confidence)
- GitHub App Manifest API docs (WebFetch via official GitHub docs) — manifest fields, conversion response structure, code expiry behavior

### Tertiary (LOW confidence)
- Organizations `ListPoliciesForTarget` behavior with account vs OU target ID — needs verification at plan/implementation time

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all SDK packages are direct imports or newly added to go.mod; no external Go deps
- Architecture patterns: HIGH — directly derived from existing codebase patterns in status.go, list.go, configure_github.go
- Pitfalls: HIGH for AWS-specific (documented SDK behavior); MEDIUM for manifest flow (based on GitHub docs)
- GitHub manifest flow: MEDIUM — API documented; local callback server pattern is standard Go but not battle-tested in this codebase

**Research date:** 2026-03-22
**Valid until:** 2026-04-22 (stable APIs; go.mod deps won't drift significantly)
