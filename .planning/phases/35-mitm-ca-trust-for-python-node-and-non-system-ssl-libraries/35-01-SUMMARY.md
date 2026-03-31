---
phase: 35
plan: 01
status: complete
---

# Phase 35-01 Summary: MITM CA Trust for Python, Node, and Non-System SSL Libraries

## What was done

Added CA trust environment variables to the EC2 user-data template so that tools bundling their own CA stores (Python certifi, Node.js, Rust) trust the km proxy CA for MITM interception.

## Changes

**`pkg/compiler/userdata.go`** — After the `update-ca-trust` block installs the km proxy CA into the system trust store, a new block:

1. Detects the merged CA bundle path:
   - `/etc/pki/tls/certs/ca-bundle.crt` (RHEL / Amazon Linux 2023)
   - `/etc/ssl/certs/ca-certificates.crt` (Debian / Ubuntu)
2. Appends four env vars to `/etc/profile.d/km-audit.sh`:
   - `SSL_CERT_FILE` — Python `ssl` module, many Rust crates
   - `REQUESTS_CA_BUNDLE` — Python `requests` (overrides certifi)
   - `CURL_CA_BUNDLE` — curl explicit override
   - `NODE_EXTRA_CA_CERTS` — Node.js (appends to compiled-in store)

## Verification

- `go build -o km ./cmd/km/` — clean build
- `go test ./pkg/compiler/...` — all tests pass
- No schema changes, `km validate` unaffected
