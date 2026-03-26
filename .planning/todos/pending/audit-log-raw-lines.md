---
title: Audit-log sidecar silently drops non-JSON lines
area: sidecars
created: 2026-03-26
---

### Problem
The audit-log sidecar's Process() function skips lines that aren't valid JSON with a warning log but doesn't write them anywhere persistent. Raw shell output, error messages, or malformed events are lost.

### Solution
Log non-JSON lines to a separate "raw" CloudWatch stream or append to a local file. This preserves forensic data even when the JSON schema doesn't match.

### Files
- sidecars/audit-log/auditlog.go (Process function)
