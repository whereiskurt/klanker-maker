---
title: Bump sealed profile idleTimeout from 5m to 15-30m
area: profiles
created: 2026-03-26
---

### Problem
The sealed profile's idleTimeout of 5m is very aggressive for testing and development. Sandboxes get killed while you're thinking, reading docs, or running long commands.

### Solution
Change profiles/sealed.yaml idleTimeout from "5m" to "15m" or "30m". The 5m value was useful for testing the idle detection pipeline but is impractical for real use.

### Files
- profiles/sealed.yaml
