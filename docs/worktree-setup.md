# Git Worktree Setup for km

Git worktrees clone the tracked repo but not gitignored files. Several gitignored files are required for `km` to function. Copy them from the main repo into the worktree before running `km` commands.

## Required gitignored files

```bash
MAIN=~/working/klankrmkr
WT=<worktree-path>

# 1. km-config.yaml — platform configuration (accounts, region, SSO, etc.)
cp "$MAIN/km-config.yaml" "$WT/km-config.yaml"

# 2. Network outputs — VPC/subnet IDs for sandbox provisioning
mkdir -p "$WT/infra/live/use1/network/"
cp "$MAIN/infra/live/use1/network/outputs.json" "$WT/infra/live/use1/network/outputs.json"

# 3. EFS outputs — filesystem ID for shared mounts
mkdir -p "$WT/infra/live/use1/efs/"
cp "$MAIN/infra/live/use1/efs/outputs.json" "$WT/infra/live/use1/efs/outputs.json"
```

## Toolchain binary (Lambda create-handler)

The create-handler Lambda downloads `km` from `s3://km-artifacts-12345/toolchain/km`. This is separate from `sidecars/km` (used on EC2 instances). After code changes that affect userdata templates or the `km create` path:

```bash
# Rebuild and upload (Lambda runs on ARM64/Graviton)
GOOS=linux GOARCH=arm64 go build -ldflags "..." -o build/km-linux-arm64 ./cmd/km/
AWS_PROFILE=klanker-application aws s3 cp build/km-linux-arm64 s3://km-artifacts-12345/toolchain/km --region us-east-1

# Force Lambda cold start (sync.Once caches toolchain per container)
AWS_PROFILE=klanker-terraform aws lambda update-function-configuration \
  --region us-east-1 \
  --function-name km-create-handler \
  --environment "Variables={TOOLCHAIN_VERSION=$(date +%s)}"
```

## Sidecar binaries (EC2 instances)

Sidecars run on x86_64 EC2 instances. `make sidecars` handles cross-compilation:

```bash
KM_ARTIFACTS_BUCKET=km-artifacts-12345 AWS_PROFILE=klanker-application make sidecars
```

This uploads dns-proxy, http-proxy, audit-log, km (x86_64), otelcol-contrib, and tracing config to `s3://km-artifacts-12345/sidecars/`.

## Why two km binaries?

| Path | Arch | Used by | Purpose |
|------|------|---------|---------|
| `s3://sidecars/km` | linux/amd64 | EC2 instance bootstrap | `km ebpf-attach` on the sandbox |
| `s3://toolchain/km` | linux/arm64 | Lambda create-handler | Compiles userdata, runs terraform |
