# Klanker Maker

This file serves as the Terragrunt repo root anchor for `find_in_parent_folders("CLAUDE.md")`.

## Project

Policy-driven sandbox platform. See `.planning/PROJECT.md` for details.

## CLI

- `km validate <profile.yaml>` — validate a SandboxProfile
- `km create <profile.yaml>` — provision a sandbox
- `km destroy <sandbox-id>` — teardown a sandbox (--remote by default; `km kill` is an alias)
- `km pause <sandbox-id>` — hibernate/pause an EC2 or Docker instance (preserves infra)
- `km resume <sandbox-id>` — resume a paused or stopped sandbox
- `km lock <sandbox-id>` — safety lock preventing destroy/stop/pause (atomic DynamoDB)
- `km unlock <sandbox-id>` — remove safety lock (requires confirmation or --yes)
- `km list` — list sandboxes (narrow default, --wide for all columns)
- `km otel <sandbox-id>` — OTEL telemetry + AI spend summary (--prompts, --events, --tools, --timeline)
- `km info` — platform configuration, accounts, operator email, email-to-create

## Architecture

- `cmd/km/` — CLI entry point
- `internal/app/cmd/` — Cobra commands
- `pkg/profile/` — Schema, validation, inheritance
- `pkg/compiler/` — Profile → Terragrunt artifacts
- `pkg/terragrunt/` — Terragrunt runner
- `pkg/aws/` — AWS SDK helpers
- `infra/modules/` — Terraform modules
- `infra/live/` — Terragrunt hierarchy
- `profiles/` — Built-in SandboxProfile YAML files
