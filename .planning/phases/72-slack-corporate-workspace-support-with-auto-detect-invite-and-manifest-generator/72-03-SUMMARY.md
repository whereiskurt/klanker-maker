---
phase: 72-slack-corporate-workspace-support-with-auto-detect-invite-and-manifest-generator
plan: "03"
subsystem: slack-manifest
tags: [slack, manifest, cobra, embed, tdd, golden-file]
dependency_graph:
  requires: [72-00]
  provides: [km-slack-manifest-command, RenderSlackManifest, SlackManifestData]
  affects: [km-slack-help, km-slack-init, km-doctor-scope-check]
tech_stack:
  added: [text/template, embed]
  patterns: [golden-file-test, tdd-red-green, cobra-subcommand-registration, ssm-prefixed-lookup]
key_files:
  created:
    - internal/app/cmd/slack_manifest.go
  modified:
    - internal/app/cmd/slack_manifest_test.go
    - internal/app/cmd/slack.go
    - internal/app/cmd/slack_test.go
decisions:
  - "Export SlackManifestData (option a) for clean test injection without a wrapper helper"
  - "Print banner to stderr so stdout remains pure JSON and pipeable"
  - "Fix pre-existing TestSlackInit_ScopeCheck_MissingOne by adding files:read to test input"
metrics:
  duration: "526s"
  completed: "2026-05-29"
  tasks_completed: 2
  files_modified: 4
---

# Phase 72 Plan 03: km slack manifest Command Summary

Implemented `km slack manifest` — a cobra command that renders a deployment-specific Slack App manifest (JSON) to stdout. Operators run `km slack manifest > app.json` and paste the file into Slack admin "From manifest" UI to install or drift-check their app.

## What Was Built

**`internal/app/cmd/slack_manifest.go`** — new file providing:
- `SlackManifestData` (exported struct with `AppName` and `EventsURL` fields)
- `RenderSlackManifest(w io.Writer, data SlackManifestData) error` — renders `//go:embed slack_manifest_template.json` via `text/template`
- `SlackManifestOpts` — holds `--app-name` flag value
- `RunSlackManifest(ctx, deps, cfg, opts, w)` — reads bridge URL from SSM `{ssmPrefix}slack/bridge-url`, derives `/events` suffix, calls `RenderSlackManifest`
- `newSlackManifestCmd(cfg, deps)` — cobra command with `--app-name` string flag

**`internal/app/cmd/slack.go`** — added `slackCmd.AddCommand(newSlackManifestCmd(cfg, deps))` in `newSlackCmdInternal`.

## Scope Set (13 scopes)

The embedded template (`slack_manifest_template.json`) emits exactly 13 bot scopes:

```
chat:write, channels:manage, channels:join, channels:read, channels:history,
groups:write, groups:history, conversations.connect:write, reactions:read,
reactions:write, files:write, files:read, users:read.email
```

- `files:read` — Phase 75 inbound requirement, enforced by `km doctor`'s inbound-scope check (`required = [channels:history, groups:history, reactions:write, files:read]`). The rendered manifest's scope set is a strict superset of `km doctor`'s required list.
- `users:read.email` — Phase 72 addition for `LookupUserByEmail` (Plan 72-01).

## Golden Fixture

File: `internal/app/cmd/testdata/slack_manifest_golden.json`

Inputs:
- `AppName = "KlankerMaker-test"`
- `EventsURL = "https://example.lambda-url.us-east-1.on.aws/events"`

The golden fixture was seeded in Wave 0 and is byte-equal to the template with the two placeholders substituted.

## Data Export Decision

`SlackManifestData` is **exported** (option a). This allows tests to inject known values directly via `cmd.RenderSlackManifest(&buf, cmd.SlackManifestData{...})` without any wrapper helper. Godoc reflects this.

## Tests (6 — all PASS)

| Test | Assertion |
|------|-----------|
| `TestSlackManifest_Golden` | Byte-equal comparison to golden fixture |
| `TestSlackManifest_AppNameOverride` | Custom name appears in display_information.name and bot_user.display_name |
| `TestSlackManifest_BridgeURLFromSSM` | Bridge URL from SSM gets `/events` suffix (trailing slash trimmed) |
| `TestSlackManifest_ScopesIncludeUsersReadEmail` | Both `users:read.email` and `files:read` present |
| `TestSlackManifest_OutputIsValidJSON` | `json.Unmarshal` succeeds |
| `TestSlackManifest_MissingBridgeURL` | Error contains "km slack init" remediation pointer |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed pre-existing TestSlackInit_ScopeCheck_MissingOne**
- **Found during:** Task 2 regression check
- **Issue:** `VerifyEventsAPIScopes` requires 4 scopes (channels:history, groups:history, reactions:write, files:read) after Phase 75 added `files:read`. The test input `["chat:write", "channels:history", "reactions:write"]` omits `files:read`, so 2 scopes were missing — but the test asserted `len(missing) == 1`.
- **Fix:** Added `files:read` to the test input so exactly one scope (`groups:history`) is missing, matching the test's documented intent.
- **Files modified:** `internal/app/cmd/slack_test.go`
- **Commit:** d705b91

## Note for Plan 72-09 (Docs)

Document `km slack manifest` in `OPERATOR-GUIDE.md` with the following install runbook:

```bash
# Generate the Slack App manifest for your install
km slack manifest > app.json

# Then in Slack admin:
# Apps → Build → New App → From manifest → paste app.json
# Install the app and copy the bot token for km slack init
```

Also note: re-running `km slack manifest` and diffing the scope list against an installed app's scopes is the recommended UAT for Pitfall 1 (missing scope drift). The manifest is the canonical source of truth.

## Self-Check: PASSED

Files created/modified:
- FOUND: internal/app/cmd/slack_manifest.go
- FOUND: internal/app/cmd/testdata/slack_manifest_golden.json (pre-existing from Wave 0)
- FOUND: internal/app/cmd/slack_manifest_test.go (updated)
- FOUND: internal/app/cmd/slack.go (updated)
- FOUND: internal/app/cmd/slack_test.go (bug fix)

Commits:
- FOUND: 4e2a8ae (test RED — failing tests committed before implementation)
- FOUND: 194c6e0 (feat GREEN — RenderSlackManifest + newSlackManifestCmd)
- FOUND: d705b91 (feat — wire into slack command tree + fix pre-existing test)
