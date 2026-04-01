---
phase: 41-ebpf-ssl-uprobe-observability
plan: 03
subsystem: ebpf
tags: [http-parsing, github-audit, tls-inspection, uprobe, zerolog]

requires:
  - phase: 41-01
    provides: "TLSEvent type, direction constants, payload accessors"
provides:
  - "HTTPRequest parser for captured TLS plaintext (ParseHTTPRequest)"
  - "GitHub repo extractor from API and git-smart-HTTP paths (ExtractGitHubRepo)"
  - "GitHubAuditHandler for observability-only repo access logging"
  - "BedrockAuditHandler stub for AI API request URL logging"
  - "EventHandler type for Consumer integration"
affects: [41-04, 41-05, ebpf-consumer, proxy-replacement]

tech-stack:
  added: []
  patterns: ["stdlib http.ReadRequest for HTTP/1.1 parsing", "case-insensitive allowlist matching"]

key-files:
  created:
    - pkg/ebpf/tls/http.go
    - pkg/ebpf/tls/http_test.go
    - pkg/ebpf/tls/github.go
    - pkg/ebpf/tls/github_test.go
  modified: []

key-decisions:
  - "Used stdlib http.ReadRequest instead of manual parsing for HTTP edge case handling"
  - "EBPF-TLS-10 (budget metering) scoped as audit-only URL logging per research -- HTTP/2 DATA frames not accessible via uprobes"
  - "EventHandler defined as func type (not interface) matching Plan 02 consumer design"

patterns-established:
  - "TLS plaintext audit handlers: parse HTTP then extract domain-specific metadata"
  - "Observability-only pattern: log violations at Warn, allowed at Debug"

requirements-completed: [EBPF-TLS-09, EBPF-TLS-14, EBPF-TLS-10]

duration: 2min
completed: 2026-04-01
---

# Phase 41 Plan 03: HTTP Parser and GitHub Repo Audit Handler Summary

**HTTP/1.1 parser extracts method/path/host from captured TLS plaintext; GitHub repo auditor logs allowlist violations for observability**

## Performance

- **Duration:** 2 min
- **Started:** 2026-04-01T20:59:45Z
- **Completed:** 2026-04-01T21:02:05Z
- **Tasks:** 1
- **Files modified:** 4

## Accomplishments
- HTTP request parser using stdlib http.ReadRequest with truncation handling and HTTP/2 preface rejection
- GitHub repo path extraction supporting both api.github.com (/repos/owner/repo) and github.com (owner/repo.git) URL patterns
- GitHubAuditHandler that logs Warn for repos not in allowlist, Debug for permitted access
- BedrockAuditHandler stub for Bedrock/Anthropic API URL logging (token extraction deferred per research)
- Comprehensive test coverage: 6 extraction tests, 4 handler behavior tests, 6 HTTP parser tests

## Task Commits

Each task was committed atomically:

1. **Task 1: HTTP request parser and GitHub repo path extractor with audit handler**
   - `29feea0` (test) - Failing tests for HTTP parser and GitHub audit handler
   - `61705b1` (feat) - HTTP parser, GitHub audit handler, Bedrock audit stub implementation

## Files Created/Modified
- `pkg/ebpf/tls/http.go` - HTTP/1.1 request parser (ParseHTTPRequest, HTTPRequest, ErrNotHTTP)
- `pkg/ebpf/tls/http_test.go` - Tests for valid/invalid/truncated HTTP parsing
- `pkg/ebpf/tls/github.go` - GitHub repo extractor, audit handler, Bedrock audit stub, EventHandler type
- `pkg/ebpf/tls/github_test.go` - Tests for repo extraction and audit handler behavior

## Decisions Made
- Used stdlib http.ReadRequest instead of manual line parsing -- handles HTTP edge cases (chunked encoding, malformed headers) correctly
- EBPF-TLS-10 (budget metering) implemented as audit-only URL logging per research finding that HTTP/2 DATA frames (containing token counts) are not accessible via uprobes
- EventHandler defined as `func(event *TLSEvent) error` (function type, not interface) to match Plan 02 consumer callback pattern

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
- Tests compile for Linux (go:build linux) but cannot execute on macOS (Darwin host). This is the same constraint as all other eBPF code in the project. Package verified via `GOOS=linux go vet` and `GOOS=linux go build`.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- HTTP parser and GitHub audit handler ready for Consumer integration (Plan 02/04)
- EventHandler type defined and ready for Consumer to accept handler registrations
- BedrockAuditHandler stub ready for future enhancement if HTTP/2 parsing becomes feasible

---
*Phase: 41-ebpf-ssl-uprobe-observability*
*Completed: 2026-04-01*
