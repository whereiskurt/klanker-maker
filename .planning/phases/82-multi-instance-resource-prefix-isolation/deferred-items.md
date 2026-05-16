# Deferred Items — Phase 82

## From Plan 82-02

### pkg/compiler/service_hcl.go:784 — literal "km-slack-stream-messages" fallback

**Status:** RESOLVED — closed by Phase 82.1-02 (commit 42f7f25).

**Discovered during:** Task 2 (grep audit)
**File:** `pkg/compiler/service_hcl.go` line 784
**Issue:** Same `km-slack-stream-messages` literal fallback pattern as the three
sites fixed in `userdata.go`. This site is in `generateServiceHCL` (or similar
function in `service_hcl.go`) and was not identified in the plan's scope
(plan explicitly listed only 3 sites in `userdata.go`).
**Fix needed:** Replace `streamTable = "km-slack-stream-messages"` with
`streamTable = resourcePrefix + "-slack-stream-messages"` (same pattern as
`userdata.go` fix). Confirm `resourcePrefix` or `KM_RESOURCE_PREFIX` is
available in that function's scope first.
**Priority:** Medium — affects sandbox HCL generation, same blast radius as
the userdata.go sites.

**Resolution:** Phase 82.1-02 replaced the literal with
`ec2StreamPrefix + "-slack-stream-messages"` where `ec2StreamPrefix` reads
`KM_RESOURCE_PREFIX` (defaulting to `"km"`). Test
`TestEC2ServiceHCL_StreamTableUsesResourcePrefix` covers the custom-prefix,
default-prefix, and explicit-override branches.
