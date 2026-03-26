---
title: km list could show idle countdown column
area: cli
created: 2026-03-26
---

### Problem
km list shows TTL remaining but not idle status. You have to run km status to see the idle countdown. For multiple sandboxes, checking each one individually is tedious.

### Solution
Add an IDLE column to km list that shows the countdown (e.g. "3m", "12m") with color. Would require a CloudWatch GetLogEvents call per sandbox which adds latency — could be opt-in via --idle flag.
