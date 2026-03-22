---
phase: 03-sidecar-enforcement-lifecycle-management
plan: "00"
subsystem: test-stubs
tags: [tdd, wave-0, nyquist, stubs]
dependency_graph:
  requires: []
  provides:
    - dns-proxy test stubs (sidecars/dns-proxy/dnsproxy/dns_proxy_test.go)
    - http-proxy test stubs (sidecars/http-proxy/httpproxy/http_proxy_test.go)
    - audit-log test stubs (sidecars/audit-log/audit_log_test.go)
    - scheduler test stubs (pkg/aws/scheduler_test.go)
    - lifecycle test stubs (pkg/lifecycle/idle_test.go)
    - mlflow test stubs (pkg/aws/mlflow_test.go)
    - list command test stubs (internal/app/cmd/list_test.go)
    - status command test stubs (internal/app/cmd/status_test.go)
  affects: [03-01, 03-02, 03-03, 03-04, 03-05]
tech_stack:
  added: [github.com/miekg/dns, github.com/elazarl/goproxy, github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs]
  patterns: [external-test-package, nyquist-tdd, package-per-sidecar]
key_files:
  created:
    - sidecars/dns-proxy/dnsproxy/dns_proxy_test.go
    - sidecars/http-proxy/httpproxy/http_proxy_test.go
    - sidecars/audit-log/audit_log_test.go
    - pkg/aws/scheduler_test.go
    - pkg/lifecycle/idle_test.go
    - pkg/aws/mlflow_test.go
    - internal/app/cmd/list_test.go
    - internal/app/cmd/status_test.go
    - pkg/lifecycle/doc.go
    - sidecars/audit-log/auditlog.go
    - sidecars/dns-proxy/dnsproxy/proxy.go
    - sidecars/http-proxy/httpproxy/proxy.go
    - pkg/aws/cloudwatch.go
    - pkg/aws/mlflow.go
  modified:
    - go.mod
    - go.sum
decisions:
  - "Sidecar library packages use subdirectories (dnsproxy/, httpproxy/, auditlog/) with package main at parent — matches dns-proxy pattern and allows external test packages"
  - "audit-log sidecar binary moved to sidecars/audit-log/cmd/main.go to avoid package conflict with auditlog library at sidecars/audit-log/"
  - "Wave-0 pre-built implementations via linter: audit-log, dns-proxy, http-proxy stubs replaced with full implementations; plan goal of failing stubs still met for scheduler, lifecycle, list, status"
metrics:
  duration: "~15 minutes"
  completed: "2026-03-22"
  tasks_completed: 2
  files_created: 14
---

# Phase 03 Plan 00: Wave-0 Test Stubs Summary

**One-liner:** Wave-0 Nyquist test stubs for all Phase 3 packages — go build ./... clean, scheduler/lifecycle/list/status stubs fail red, sidecar packages pre-built by linter sessions.

## What Was Done

Created 8 test stub files establishing the Nyquist sampling contract for Phase 3 implementation plans (03-01 through 03-05). All stub files compile without errors; scheduler, lifecycle, list-cmd, and status-cmd stubs fail with `t.Fatal("not implemented")` as required by the TDD wave-0 pattern.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Sidecar test stubs (dns-proxy, http-proxy, audit-log) | 33a79b7 | sidecars/{dns-proxy,http-proxy,audit-log}/*_test.go |
| 2 | Package test stubs (scheduler, lifecycle, mlflow, list, status) | 53fad2e (pre-existing) | pkg/aws/{scheduler,mlflow}_test.go, pkg/lifecycle/idle_test.go, internal/app/cmd/{list,status}_test.go |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Sidecar packages needed library stubs to compile expanded test files**
- **Found during:** Task 1 — audit-log test, Task 2 — dns-proxy test
- **Issue:** Linter-expanded test files imported non-existent library packages (`auditlog`, `dnsproxy`, `httpproxy`)
- **Fix:** Created stub library files (`auditlog.go`, `proxy.go`) with `panic("not implemented")` bodies so tests compile
- **Files modified:** sidecars/audit-log/auditlog.go (stub, then replaced by linter with full impl), sidecars/dns-proxy/dnsproxy/proxy.go
- **Commit:** Inline with Task 1

**2. [Rule 3 - Blocking] pkg/aws missing CWLogsAPI, GetLogEvents, TailLogs**
- **Found during:** Task 2 — cloudwatch_test.go referenced functions not in pkg/aws
- **Issue:** `cloudwatch_test.go` (pre-committed by linter) required `EnsureLogGroup`, `PutLogEvents`, `GetLogEvents`, `TailLogs`
- **Fix:** Created `pkg/aws/cloudwatch.go` with stub implementations; linter then replaced with full implementation
- **Files modified:** pkg/aws/cloudwatch.go
- **Commit:** Inline with Task 2

**3. [Rule 3 - Blocking] Missing go.mod dependencies for miekg/dns and elazarl/goproxy**
- **Found during:** Task 1 (dns-proxy), Task 2 (http-proxy)
- **Issue:** Test files imported third-party DNS and proxy libraries not in go.mod
- **Fix:** `go get github.com/miekg/dns` and `go get github.com/elazarl/goproxy`
- **Files modified:** go.mod, go.sum
- **Commit:** Inline

### Architecture Note

Previous linter sessions (commits 419572e, 2aa78e4, 6abf41d, da8f46d, 9fa4b11, dd61fe3, 7c2f426, 5e81f8b, 4ac5026) had already implemented full working code for dns-proxy, audit-log, mlflow, and cloudwatch before this plan executed. The Task 2 stub files for those packages were already committed. The scheduler, lifecycle, list-cmd, and status-cmd stubs remain as intended failing stubs for plans 03-04 and 03-05 to implement.

## Success Criteria Verification

- [x] All 8 stub files exist at declared paths (dns_proxy_test.go moved to dnsproxy/ subpackage by Plan 03-01 refactor)
- [x] `go build ./...` produces no compilation errors
- [x] `go test ./pkg/aws/... -run "TestCreateTTLSchedule|TestDeleteTTLSchedule"` exits FAIL + "not implemented"
- [x] `go test ./internal/app/cmd/... -run "TestListCmd|TestStatusCmd"` exits FAIL + "not implemented"
- [x] Existing tests in pkg/aws/, internal/app/cmd/ still pass

## Self-Check: PASSED

Verified:
- pkg/aws/scheduler_test.go exists and fails: CONFIRMED
- pkg/lifecycle/idle_test.go exists and fails: CONFIRMED
- internal/app/cmd/list_test.go exists and fails: CONFIRMED
- internal/app/cmd/status_test.go exists and fails: CONFIRMED
- `go build ./...` succeeds: CONFIRMED
