# Deferred Items — Phase 77

## Pre-existing out-of-scope issues discovered during execution

### [Out of scope] IPv6 vet warning in sidecars/http-proxy

**File:** `sidecars/http-proxy/httpproxy/transparent.go:204`
**Warning:** `address format "%s:%d" does not work with IPv6 (passed to net.Dial at L238)`
**Discovered during:** Plan 77-01 Task 2 final verification
**Status:** Pre-existing before this phase — present on commit 3e959e9 (before 77-01 implementation)
**Action required:** A follow-up fix should use `net.JoinHostPort(host, port)` or `fmt.Sprintf("[%s]:%d", host, port)` for IPv6-safe address formatting in `transparent.go`.
**Priority:** Low — affects IPv6 connectivity only; IPv4 sandboxes unaffected.
