---
title: Add multiple AZs for spot capacity or default to on-demand for testing
area: infra
created: 2026-03-26
---

### Problem
Spot capacity in us-east-1a is frequently unavailable, causing km create to fail. The ec2spot module only tries one AZ (the first subnet). AWS suggests us-east-1b, 1c, 1d, 1f as alternatives.

### Solution
Either: (1) have the ec2spot module try multiple AZs on spot capacity failure, (2) add more subnets to the network module across more AZs, or (3) default profiles to on-demand for reliability during development. The network module currently creates subnets in only 2 AZs.

### Files
- infra/modules/ec2spot/v1.0.0/main.tf
- infra/modules/network/v1.0.0/main.tf
