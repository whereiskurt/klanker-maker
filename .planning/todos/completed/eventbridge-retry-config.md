---
title: Set MaximumRetryAttempts=0 on TTL EventBridge schedule
area: infra
created: 2026-03-26
---

### Problem
EventBridge retries TTL schedule events, causing multiple concurrent Lambda invocations that all try to terraform destroy the same sandbox. While -lock=false prevents deadlocks, it's noisy and wasteful.

### Solution
Set MaximumRetryAttempts: 0 in compiler.BuildTTLScheduleInput so the schedule fires exactly once. If the Lambda fails, the sandbox stays until manual destroy.

### Files
- pkg/compiler/lifecycle.go (BuildTTLScheduleInput)
