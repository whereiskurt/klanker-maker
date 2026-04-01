---
title: Root user exempt from DNAT allows yum install on sealed profile
area: security
created: 2026-03-26
---

### Problem
Root is exempt from iptables DNAT (needed for SSM agent). But yum runs as root, so package installs bypass the proxy entirely on sealed profiles. An agent with sudo (or running as root via km shell --root) can install arbitrary packages.

### Solution
Consider restricting yum/dnf for the sandbox user. The sandbox user already can't sudo, so this only affects --root sessions. Could also use SELinux or filesystem ACLs to lock down /usr/bin/yum for non-operator users.
