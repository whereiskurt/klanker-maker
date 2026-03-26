---
title: SOCKS proxy support via km shell --socks
area: cli
created: 2026-03-26
---

### Problem
No SOCKS proxy support for tunneling arbitrary TCP traffic through a sandbox. Port forwarding covers specific ports but SOCKS would allow dynamic routing.

### Solution
Install a lightweight SOCKS5 server (microsocks or Go-based) as part of userdata. km shell --socks starts port forwarding to the SOCKS port. Configure via profile sidecar settings.
