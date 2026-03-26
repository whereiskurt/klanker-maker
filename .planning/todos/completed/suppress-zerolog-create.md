---
title: Suppress zerolog JSON output in km create non-verbose mode
area: cli
created: 2026-03-26
---

### Problem
km create in non-verbose mode still leaks {"level":"warn"...} and {"level":"info"...} JSON lines between the progress indicators. The destroy command already suppresses these by redirecting log.Logger to io.Discard when not verbose.

### Solution
Apply the same pattern from runDestroy to runCreate: set log.Logger = zerolog.New(io.Discard) when !verbose, defer restore.

### Files
- internal/app/cmd/create.go
