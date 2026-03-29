---
phase: quick
plan: 1
type: execute
wave: 1
depends_on: []
files_modified:
  - internal/app/config/config.go
  - internal/app/config/config_test.go
  - internal/app/cmd/configure.go
  - internal/app/cmd/configure_test.go
  - internal/app/cmd/create.go
  - internal/app/cmd/create_test.go
  - pkg/aws/ses.go
autonomous: true
requirements: []

must_haves:
  truths:
    - "km create rejects with clear error when active sandbox count >= max_sandboxes"
    - "km create --remote rejects with clear error when active sandbox count >= max_sandboxes"
    - "Operator receives email notification when sandbox limit is hit"
    - "max_sandboxes defaults to 10 when not set in km-config.yaml"
    - "km configure prompts for max_sandboxes value"
  artifacts:
    - path: "internal/app/config/config.go"
      provides: "MaxSandboxes field on Config struct, loaded from km-config.yaml"
      contains: "MaxSandboxes"
    - path: "internal/app/cmd/create.go"
      provides: "Sandbox count check in both runCreate and runCreateRemote"
      contains: "MaxSandboxes"
    - path: "pkg/aws/ses.go"
      provides: "SendLimitNotification helper for operator email"
      contains: "SendLimitNotification"
  key_links:
    - from: "internal/app/cmd/create.go"
      to: "pkg/aws/sandbox.go"
      via: "ListAllSandboxesByS3 to count active sandboxes"
      pattern: "ListAllSandboxesByS3"
    - from: "internal/app/cmd/create.go"
      to: "pkg/aws/ses.go"
      via: "SendLimitNotification on rejection"
      pattern: "SendLimitNotification"
---

<objective>
Add a max_sandboxes configuration field (default 10) that enforces a hard limit on concurrent sandbox count. Both runCreate and runCreateRemote check active sandbox count via S3 listing before provisioning. When the limit is hit, reject with a clear error and send an operator email notification.

Purpose: Prevent runaway sandbox creation from exhausting AWS resources or budget.
Output: Config field, enforcement in create paths, operator notification, configure wizard prompt, tests.
</objective>

<context>
@internal/app/config/config.go
@internal/app/cmd/create.go
@internal/app/cmd/configure.go
@pkg/aws/ses.go
@pkg/aws/sandbox.go

<interfaces>
From internal/app/config/config.go:
```go
type Config struct {
    // ... existing fields ...
    OperatorEmail string
    Domain        string
    StateBucket   string
}
func Load() (*Config, error)
```

From pkg/aws/sandbox.go:
```go
type SandboxRecord struct { SandboxID string; Status string; ... }
func ListAllSandboxesByS3(ctx context.Context, client S3ListAPI, bucket string) ([]SandboxRecord, error)
```

From pkg/aws/ses.go:
```go
type SESV2API interface { SendEmail(...) ... }
func SendLifecycleNotification(ctx context.Context, client SESV2API, operatorEmail, sandboxID, event, domain string) error
```

From internal/app/cmd/configure.go:
```go
type platformConfig struct { ... } // written to km-config.yaml
func runConfigure(...) error       // interactive wizard
```
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Add MaxSandboxes to Config, platformConfig, and configure wizard</name>
  <files>internal/app/config/config.go, internal/app/config/config_test.go, internal/app/cmd/configure.go, internal/app/cmd/configure_test.go, pkg/aws/ses.go, pkg/aws/ses_test.go</files>
  <behavior>
    - Config.MaxSandboxes defaults to 10 when not set in km-config.yaml
    - Config.MaxSandboxes=5 when km-config.yaml has max_sandboxes: 5
    - KM_MAX_SANDBOXES=3 env var overrides km-config.yaml value
    - km configure interactive mode prompts for "Maximum concurrent sandboxes" with default "10"
    - km configure --non-interactive --max-sandboxes=5 writes max_sandboxes: 5 to km-config.yaml
    - SendLimitNotification sends email with subject "km sandbox limit-reached: {attempted-sandbox}" and body containing current count and max
  </behavior>
  <action>
    1. In config.go:
       - Add `MaxSandboxes int` field to Config struct with comment explaining it (0 means unlimited, default 10)
       - Add `v.SetDefault("max_sandboxes", 10)` in Load()
       - Add `"max_sandboxes"` to the v2 merge loop (km-config.yaml keys list around line 178-199)
       - Add `MaxSandboxes: v.GetInt("max_sandboxes")` to the cfg construction around line 226

    2. In config_test.go:
       - Add TestMaxSandboxesDefault: Load() with no max_sandboxes in yaml returns 10
       - Add TestMaxSandboxesFromConfig: Load() with max_sandboxes: 5 returns 5
       - Add TestMaxSandboxesEnvOverride: KM_MAX_SANDBOXES=3 overrides config value

    3. In configure.go:
       - Add `MaxSandboxes int` field to platformConfig struct with yaml tag `max_sandboxes,omitempty`
       - Add `--max-sandboxes` flag (int, default 0 meaning "use default 10") to NewConfigureCmd
       - Add `maxSandboxes` variable to the flag/parameter list in newConfigureCmdWithIO and runConfigure
       - In interactive mode: prompt "Maximum concurrent sandboxes (0=unlimited)" with default "10", parse as int
       - In non-interactive mode: use flag value if provided
       - Set `pc.MaxSandboxes` in the platformConfig construction before writing yaml (only if > 0)

    4. In configure_test.go:
       - Add test that interactive configure with max_sandboxes input writes the field to km-config.yaml
       - Add test that --non-interactive --max-sandboxes=5 writes max_sandboxes: 5

    5. In ses.go:
       - Add `SendLimitNotification(ctx, client SESV2API, operatorEmail, sandboxID, domain string, currentCount, maxCount int) error`
       - From address: `notifications@{domain}`
       - Subject: `km sandbox limit-reached: {sandboxID}`
       - Body: "Sandbox creation rejected: limit reached.\nAttempted sandbox: {sandboxID}\nActive sandboxes: {currentCount}/{maxCount}\nTo increase, set max_sandboxes in km-config.yaml.\n\n-- {version.Header()}"

    6. In ses_test.go:
       - Add TestSendLimitNotification: verify correct subject, body content, from/to addresses using the existing mock pattern
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go test ./internal/app/config/... ./internal/app/cmd/... ./pkg/aws/... -run "MaxSandbox|LimitNotif|Configure" -count=1 -v 2>&1 | tail -40</automated>
  </verify>
  <done>MaxSandboxes field loads from config with default 10, env override works, configure wizard prompts for it, SendLimitNotification helper exists and is tested</done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Enforce sandbox limit in runCreate and runCreateRemote</name>
  <files>internal/app/cmd/create.go, internal/app/cmd/create_test.go</files>
  <behavior>
    - runCreate with 10 active sandboxes and MaxSandboxes=10 returns error containing "sandbox limit reached (10/10)"
    - runCreate with 9 active sandboxes and MaxSandboxes=10 proceeds normally
    - runCreateRemote with MaxSandboxes exceeded returns same error
    - MaxSandboxes=0 skips the check entirely (unlimited)
    - Operator email is sent (best-effort, log warning on SES failure) when limit is hit
  </behavior>
  <action>
    1. Create a helper function in create.go:
       ```go
       // checkSandboxLimit checks if the active sandbox count has reached the configured limit.
       // Returns (activeCount, error). Error is non-nil when limit is reached.
       // If maxSandboxes is 0, the check is skipped (unlimited).
       func checkSandboxLimit(ctx context.Context, s3Client awspkg.S3ListAPI, bucket string, maxSandboxes int) (int, error)
       ```
       - If maxSandboxes <= 0, return (0, nil) immediately
       - Call `awspkg.ListAllSandboxesByS3(ctx, s3Client, bucket)` to get current records
       - Count records where Status != "destroyed" (active sandboxes)
       - If activeCount >= maxSandboxes, return error: `fmt.Errorf("sandbox limit reached (%d/%d) — increase max_sandboxes in km-config.yaml or destroy unused sandboxes", activeCount, maxSandboxes)`

    2. In runCreate, after AWS credential validation (step 5, around line 184) and before Step 5b env export:
       - Create s3Client: `s3Client := s3.NewFromConfig(awsCfg)`
       - Call `checkSandboxLimit(ctx, s3Client, cfg.StateBucket, cfg.MaxSandboxes)`
       - On limit error: attempt `awspkg.SendLimitNotification(ctx, sesv2.NewFromConfig(awsCfg), cfg.OperatorEmail, sandboxID, cfg.Domain, activeCount, cfg.MaxSandboxes)` (log warning if SES fails, don't block), then return the limit error
       - Print clear message: `fmt.Fprintf(os.Stderr, "\nERROR: %s\n", err)`

    3. In runCreateRemote, after AWS credential validation (step 5, around line 899):
       - Same pattern: create s3Client, call checkSandboxLimit, send notification on limit, return error

    4. In create_test.go:
       - Add TestCheckSandboxLimit_AtLimit: 10 sandboxes, max 10 -> error
       - Add TestCheckSandboxLimit_BelowLimit: 9 sandboxes, max 10 -> nil
       - Add TestCheckSandboxLimit_Unlimited: max 0 -> nil (skips check)
       - Use a mock S3ListAPI that returns canned SandboxRecord slices
       - The mock needs to implement ListObjectsV2 and GetObject from the S3ListAPI interface in pkg/aws/sandbox.go
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go test ./internal/app/cmd/... -run "SandboxLimit" -count=1 -v 2>&1 | tail -30</automated>
  </verify>
  <done>Both runCreate and runCreateRemote reject at limit with clear error, operator gets notified, unlimited mode (0) works, all tests pass</done>
</task>

</tasks>

<verification>
```bash
cd /Users/khundeck/working/klankrmkr && go test ./internal/app/config/... ./internal/app/cmd/... ./pkg/aws/... -count=1 -v 2>&1 | tail -60
go build -o km ./cmd/km/ && echo "build OK"
```
</verification>

<success_criteria>
- MaxSandboxes field on Config with default 10, configurable via km-config.yaml and KM_MAX_SANDBOXES env
- km configure prompts for max_sandboxes (interactive and --non-interactive)
- runCreate checks active sandbox count before provisioning, rejects at limit
- runCreateRemote checks active sandbox count before provisioning, rejects at limit
- Operator email sent on limit rejection (best-effort)
- MaxSandboxes=0 means unlimited (no check)
- All new and existing tests pass
- km binary builds cleanly
</success_criteria>

<output>
After completion, rebuild km binary: `go build -o km ./cmd/km/`
</output>
