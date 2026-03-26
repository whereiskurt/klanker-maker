---
title: Transparent HTTPS proxying doesn't work — only explicit proxy via env vars
area: sidecars
created: 2026-03-26
---

### Problem
iptables DNAT redirects port 443 to the HTTP proxy on 3128, but the proxy receives raw TLS bytes instead of an HTTP CONNECT request. Transparent HTTPS interception requires MITM TLS termination. Currently relying on http_proxy/https_proxy env vars for HTTPS traffic, which only covers tools that respect proxy env vars.

### Solution
Either implement MITM in the HTTP proxy using the generated CA cert (already in S3), or accept explicit proxy as the design and remove the port 443 DNAT rule to avoid confusion. The CA cert infrastructure is already in place from km init.

### Files
- pkg/compiler/userdata.go (iptables rules)
- sidecars/http-proxy/main.go
