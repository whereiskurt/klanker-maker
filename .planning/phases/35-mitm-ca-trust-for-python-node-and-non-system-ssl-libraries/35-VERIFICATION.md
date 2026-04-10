# Phase 35 Verification: MITM CA Trust for Python, Node, and Non-System SSL Libraries

## Phase Goal

Sandbox processes using Python (`requests`, `pip`), Node.js, Rust, curl, and the AWS SDK trust the km proxy CA so MITM-intercepted HTTPS connections (Bedrock metering, GitHub repo filtering) succeed without `SSLCertVerificationError`.

## Verification Results

| # | Criterion | Status | Evidence |
|---|-----------|--------|----------|
| 1 | Userdata installs proxy CA into system trust store via `update-ca-trust` / `update-ca-certificates` | VERIFIED | `pkg/compiler/userdata.go:1465-1468` — distro-aware detection and trust store update |
| 2 | Distro detection resolves correct merged bundle path (RHEL vs Debian) | VERIFIED | `userdata.go:1477-1481` — checks `/etc/pki/tls/certs/ca-bundle.crt` (Amazon Linux) then `/etc/ssl/certs/ca-certificates.crt` (Debian) |
| 3 | `SSL_CERT_FILE` env var set for Python `ssl` module and Rust crates | VERIFIED | `userdata.go:1486` |
| 4 | `REQUESTS_CA_BUNDLE` env var set for Python `requests` library | VERIFIED | `userdata.go:1487` |
| 5 | `CURL_CA_BUNDLE` env var set for curl | VERIFIED | `userdata.go:1488` |
| 6 | `NODE_EXTRA_CA_CERTS` env var set for Node.js | VERIFIED | `userdata.go:1489` |
| 7 | `AWS_CA_BUNDLE` env var set for AWS SDK (boto3, aws-sdk-js) | VERIFIED | `userdata.go:1490` — added beyond original plan scope |
| 8 | Env vars written to `/etc/profile.d/` for interactive shells | VERIFIED | `userdata.go:1484-1491` — written inside profile.d script block |
| 9 | Goose profile confirms working in practice (Claude Code, Goose, Codex all use Node/Python through proxy) | VERIFIED | `profiles/goose.yaml` — initCommands append proxy CA to `ca-bundle.crt` and set `SSL_CERT_FILE`; live sandboxes run Claude Code (Node) and Goose (Python) through MITM proxy without SSL errors |

## Summary

All planned env vars are implemented plus `AWS_CA_BUNDLE` (not in original plan). The implementation covers both RHEL/Amazon Linux and Debian/Ubuntu distros. Live goose sandboxes confirm Python (Goose), Node.js (Claude Code, Codex), and curl all work through the MITM proxy without certificate errors.

**Phase 35: COMPLETE**
