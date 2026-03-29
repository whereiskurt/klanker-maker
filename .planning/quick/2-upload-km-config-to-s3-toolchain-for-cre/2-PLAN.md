---
phase: quick
plan: 2
type: execute
wave: 1
depends_on: []
files_modified:
  - internal/app/cmd/init.go
  - cmd/create-handler/main.go
  - internal/app/config/config.go
autonomous: true
requirements: []
must_haves:
  truths:
    - "km init uploads km-config.yaml as a standalone toolchain/ file to S3"
    - "create-handler Lambda downloads km-config.yaml at cold start alongside binaries"
    - "km create subprocess inside Lambda reads config from the downloaded km-config.yaml via KM_CONFIG_PATH"
  artifacts:
    - path: "internal/app/cmd/init.go"
      provides: "Upload km-config.yaml to s3://bucket/toolchain/km-config.yaml"
      contains: "toolchain/km-config.yaml"
    - path: "cmd/create-handler/main.go"
      provides: "Download km-config.yaml at cold start, set KM_CONFIG_PATH env var for subprocess"
      contains: "KM_CONFIG_PATH"
    - path: "internal/app/config/config.go"
      provides: "Config loader honors KM_CONFIG_PATH for explicit config file path"
      contains: "KM_CONFIG_PATH"
  key_links:
    - from: "internal/app/cmd/init.go"
      to: "s3://bucket/toolchain/km-config.yaml"
      via: "s3Upload call"
      pattern: "toolchain/km-config"
    - from: "cmd/create-handler/main.go"
      to: "internal/app/config/config.go"
      via: "KM_CONFIG_PATH env var passed to subprocess"
      pattern: "KM_CONFIG_PATH"
---

<objective>
Upload km-config.yaml to S3 toolchain/ during km init and have the create-handler Lambda
download it at cold start, passing KM_CONFIG_PATH to the km create subprocess so all config
fields (max_sandboxes, domain, operator_email, etc.) are available without hardcoding env vars.

Purpose: The Lambda currently relies on KM_REPO_ROOT + infra.tar.gz extraction containing
km-config.yaml implicitly. This makes the config path explicit and reliable -- a standalone
toolchain file downloaded alongside km, terraform, and terragrunt.

Output: Three modified files that wire up the full config path from init -> S3 -> Lambda -> subprocess.
</objective>

<context>
@internal/app/cmd/init.go
@cmd/create-handler/main.go
@internal/app/config/config.go
</context>

<interfaces>
<!-- Existing upload pattern from init.go (line 778): -->
func s3Upload(localPath, bucket, s3Key string) error

<!-- Existing download pattern from create-handler main.go (line 216): -->
func downloadS3File(ctx context.Context, client S3GetAPI, bucket, key, localPath string) error

<!-- Config loader already searches KM_REPO_ROOT (config.go line 179): -->
if repoRoot := os.Getenv("KM_REPO_ROOT"); repoRoot != "" {
    v2.AddConfigPath(repoRoot)
}
</interfaces>

<tasks>

<task type="auto">
  <name>Task 1: Add KM_CONFIG_PATH support to config loader and upload km-config.yaml in init</name>
  <files>internal/app/config/config.go, internal/app/cmd/init.go</files>
  <action>
1. In config.go Load(), add KM_CONFIG_PATH support BEFORE the existing km-config.yaml v2 search.
   After the primary config read and before the v2 block (~line 173), add:
   ```
   // Explicit config path override (used by Lambda cold start)
   if configPath := os.Getenv("KM_CONFIG_PATH"); configPath != "" {
       v2.SetConfigFile(configPath)
   }
   ```
   This means when KM_CONFIG_PATH is set, viper uses that exact file instead of searching
   directories. The existing v2.AddConfigPath("." ) and KM_REPO_ROOT logic remains as fallback
   when KM_CONFIG_PATH is not set.

   IMPORTANT: The v2 viper instance already exists. Just add SetConfigFile before ReadInConfig
   is called on v2, and only when the env var is set. Do NOT change the existing merge logic.

2. In init.go uploadCreateHandlerToolchain(), after the infra.tar.gz upload (around line 772),
   add a step to upload km-config.yaml as a standalone toolchain file:
   ```
   // 5. Upload km-config.yaml for Lambda cold start
   kmConfigPath := filepath.Join(repoRoot, "km-config.yaml")
   if _, err := os.Stat(kmConfigPath); err == nil {
       s3Upload(kmConfigPath, bucket, "toolchain/km-config.yaml")
       fmt.Printf("  Uploaded toolchain/km-config.yaml\n")
   } else {
       fmt.Printf("  Warning: km-config.yaml not found at %s, skipping toolchain config upload\n", kmConfigPath)
   }
   ```
   Use the existing s3Upload helper. Non-fatal if km-config.yaml is missing (warn only).
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go build ./internal/app/... && go build ./cmd/km/</automated>
  </verify>
  <done>config.go honors KM_CONFIG_PATH env var; init.go uploads km-config.yaml to toolchain/km-config.yaml in S3</done>
</task>

<task type="auto">
  <name>Task 2: Download km-config.yaml at Lambda cold start and pass KM_CONFIG_PATH to subprocess</name>
  <files>cmd/create-handler/main.go</files>
  <action>
1. In downloadToolchain(), after extracting infra.tar.gz and before the return (around line 213),
   add download of km-config.yaml:
   ```
   // Download km-config.yaml for subprocess config
   kmConfigKey := "toolchain/km-config.yaml"
   kmConfigPath := filepath.Join(h.ToolchainDir, "km-config.yaml")
   if err := downloadS3File(ctx, h.S3Client, bucket, kmConfigKey, kmConfigPath); err != nil {
       log.Warn().Err(err).Msg("km-config.yaml not found in toolchain (non-fatal, subprocess will use defaults)")
   } else {
       log.Info().Msg("downloaded km-config.yaml")
   }
   ```
   Make this non-fatal (Warn, not error return) since the config may not exist for older deployments
   and the subprocess can still function via KM_REPO_ROOT fallback.

2. In Handle(), where the subprocess env is built (around line 135-143), add KM_CONFIG_PATH
   pointing to the downloaded config file:
   ```
   "KM_CONFIG_PATH="+filepath.Join(h.ToolchainDir, "km-config.yaml"),
   ```
   Add this line alongside the existing KM_ARTIFACTS_BUCKET, KM_REMOTE_CREATE, etc. env vars.
   The km subprocess config loader will pick this up and use the explicit file path.
  </action>
  <verify>
    <automated>cd /Users/khundeck/working/klankrmkr && go build ./cmd/create-handler/ && go test ./cmd/create-handler/ -v -count=1</automated>
  </verify>
  <done>Lambda downloads km-config.yaml at cold start (non-fatal if missing); subprocess env includes KM_CONFIG_PATH pointing to /tmp/toolchain/km-config.yaml</done>
</task>

</tasks>

<verification>
- `go build ./...` compiles without errors
- `go test ./cmd/create-handler/ -v` passes (existing tests still work)
- `go test ./internal/app/config/ -v` passes (if config tests exist)
- grep confirms "KM_CONFIG_PATH" appears in all three files
- grep confirms "toolchain/km-config.yaml" appears in init.go and main.go
</verification>

<success_criteria>
- km init uploads km-config.yaml to s3://bucket/toolchain/km-config.yaml
- create-handler downloads km-config.yaml during cold start (non-fatal if missing)
- Subprocess env includes KM_CONFIG_PATH=/tmp/toolchain/km-config.yaml
- Config loader uses KM_CONFIG_PATH when set, falls back to existing behavior otherwise
- All existing tests pass without modification
</success_criteria>

<output>
After completion, create `.planning/quick/2-upload-km-config-to-s3-toolchain-for-cre/2-SUMMARY.md`
</output>
