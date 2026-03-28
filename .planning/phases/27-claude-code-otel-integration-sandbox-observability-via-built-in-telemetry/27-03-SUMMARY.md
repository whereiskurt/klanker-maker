---
phase: 27-claude-code-otel-integration-sandbox-observability-via-built-in-telemetry
plan: "03"
subsystem: compiler, otel-collector
tags: [otel, telemetry, compiler, ec2, user-data, systemd, otelcol-contrib]
dependency_graph:
  requires:
    - 27-01 (ClaudeTelemetrySpec in profile types)
    - 27-02 (OTEL env var injection in EC2 user-data)
  provides:
    - otelcol-contrib binary downloaded from S3 artifacts bucket on EC2 startup
    - km-tracing.service systemd unit installed and enabled on EC2 sandboxes
    - SANDBOX_ID, OTEL_S3_BUCKET, AWS_REGION env vars in km-tracing systemd unit
    - otelcol-contrib started alongside other sidecars (km-dns-proxy, km-http-proxy, km-audit-log)
  affects:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go
tech_stack:
  added: []
  patterns:
    - TDD RED/GREEN cycle for template-driven code generation tests
    - explicit chmod for binary not matching km-* glob
    - sidecar-line disambiguation in test assertions (match km-dns-proxy to avoid SSM line)
key_files:
  created: []
  modified:
    - pkg/compiler/userdata.go
    - pkg/compiler/userdata_test.go
decisions:
  - "otelcol-contrib needs explicit chmod +x because the existing glob chmod +x /opt/km/bin/km-* only matches km-prefixed binaries; otelcol-contrib requires a separate chmod line"
  - "OTEL_S3_BUCKET maps to KMArtifactsBucket (same bucket as other sidecar artifacts): telemetry output (traces/, logs/, metrics/ prefixes) lives alongside other sandbox artifacts, not in a separate bucket"
  - "km-tracing.service RestartSec=5 (vs RestartSec=2 for dns/http/audit) since OTel Collector startup is heavier and benefits from slightly longer restart delay"
  - "Test assertions for systemctl enable/start match the sidecar line via km-dns-proxy presence to distinguish it from the SSM agent enable line"
metrics:
  duration: 148s
  completed_date: "2026-03-28"
  tasks_completed: 2
  files_modified: 2
---

# Phase 27 Plan 03: km-tracing OTel Collector Systemd Unit Summary

**One-liner:** otelcol-contrib binary downloaded from S3 and km-tracing.service systemd unit added to EC2 user-data, closing the gap between OTEL env var injection (Plan 27-02) and an actually-listening OTLP receiver on localhost:4317.

## What Was Built

### Task 1: otelcol-contrib binary download (TDD)

Added one download line and one chmod line to section 5 of `userDataTemplate` in `userdata.go`:

- `aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/otelcol-contrib" /opt/km/bin/otelcol-contrib`
  — placed after `tracing/config.yaml` fetch, before systemd unit creation section
- `chmod +x /opt/km/bin/otelcol-contrib`
  — explicit chmod because the existing `chmod +x /opt/km/bin/km-*` glob does not match `otelcol-contrib`

3 tests added in `userdata_test.go`:
1. Download line is present in rendered user-data
2. Download appears after existing sidecar downloads and before unit creation section
3. `chmod +x /opt/km/bin/otelcol-contrib` is present

### Task 2: km-tracing.service systemd unit and enable/start (TDD)

Added `km-tracing.service` unit to `userDataTemplate` after km-audit-log.service:

```
[Unit]
Description=Klankrmkr OTel Collector sidecar
After=network.target
[Service]
User=km-sidecar
Environment=SANDBOX_ID={{ .SandboxID }}
Environment=OTEL_S3_BUCKET={{ .KMArtifactsBucket }}
Environment=AWS_REGION={{ .AWSRegion }}
ExecStart=/opt/km/bin/otelcol-contrib --config /etc/km/tracing/config.yaml
Restart=always
RestartSec=5
[Install]
WantedBy=multi-user.target
```

Updated systemctl enable/start lines:
- `systemctl enable km-dns-proxy km-http-proxy km-audit-log km-tracing{{ if .SandboxEmail }} km-mail-poller{{ end }}`
- `systemctl start km-dns-proxy km-http-proxy km-audit-log km-tracing{{ if .SandboxEmail }} km-mail-poller{{ end }}`

No new `userDataParams` fields were needed — `SandboxID`, `KMArtifactsBucket`, and `AWSRegion` were already present from Plans 27-01/27-02 and earlier phases.

7 tests added covering all plan behaviors:
1. `km-tracing.service` unit file written to `/etc/systemd/system/km-tracing.service`
2. `User=km-sidecar` in unit
3. `SANDBOX_ID`, `OTEL_S3_BUCKET`, `AWS_REGION` env vars with correct values
4. `ExecStart=/opt/km/bin/otelcol-contrib --config /etc/km/tracing/config.yaml`
5. `systemctl enable` sidecar line includes `km-tracing`
6. `systemctl start` sidecar line includes `km-tracing`
7. `Environment=OTEL_S3_BUCKET=test-artifacts-bucket` confirms KMArtifactsBucket mapping

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Test assertions for systemctl enable/start needed disambiguation**
- **Found during:** Task 2 GREEN phase
- **Issue:** The test iterated all lines with `systemctl enable ` prefix and hit `systemctl enable amazon-ssm-agent` first (which does not contain `km-tracing`), causing false failures. The plan's test description did not account for multiple `systemctl enable` lines in the template.
- **Fix:** Updated test assertions to match the sidecar-specific enable/start line by requiring the line also contains `km-dns-proxy`, which unambiguously identifies the sidecar batch vs the SSM agent line.
- **Files modified:** `pkg/compiler/userdata_test.go`
- **Commit:** 9869f1b (included in the GREEN commit)

## Self-Check

Files exist:
- FOUND: pkg/compiler/userdata.go
- FOUND: pkg/compiler/userdata_test.go

Commits exist:
- 3c556cb: test(27-03): add failing tests for otelcol-contrib binary download in user-data
- ed0b6ed: feat(27-03): add otelcol-contrib binary download to EC2 user-data
- aea3055: test(27-03): add failing tests for km-tracing.service systemd unit in user-data
- 9869f1b: feat(27-03): add km-tracing.service systemd unit to EC2 user-data

## Self-Check: PASSED
