# Deferred Items — Phase 70

## Pre-existing failures discovered during Plan 70-04 execution

### TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0

**File:** cmd/km-slack/main_test.go
**Discovered during:** Task 3 (sidecar changes)
**Status:** Pre-existing failure — confirmed failing on commit b99a75b BEFORE Task 3 changes

**Root cause:** The test expects `PostToBridge` to retry on 503, but `PostToBridge` documentation explicitly says "5xx responses → fail fast (same rationale: the bridge has already reserved the nonce)". The test expectation contradicts the documented behavior.

**Impact:** Low — does not affect Plan 70-04 functionality. The 503 fast-fail behavior is correct per design (replay protection).

**Recommendation:** Either update the test to match the documented 503-is-terminal behavior, or document an exception for 503 (Service Unavailable) as retry-eligible. Out of scope for Plan 70-04.
