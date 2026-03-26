---
title: km doctor could check sidecar health on running sandboxes
area: cli
created: 2026-03-26
---

### Problem
km doctor validates platform infrastructure but doesn't check if sidecars are actually running on active sandboxes. A sandbox could have crash-looping sidecars and doctor wouldn't report it.

### Solution
When active sandboxes exist, doctor could SSM SendCommand to check systemctl status of km-dns-proxy, km-http-proxy, km-audit-log on each instance. Report as warning if any sidecar is not active.
