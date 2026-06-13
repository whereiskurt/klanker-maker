# Deferred Items — Phase 110

## Pre-existing Test Failure (out of scope)

**Test:** `TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0` in `cmd/km-slack/main_test.go`

**Observed in:** 110-03

**Root cause:** The test expects `PostToBridge` to retry on 503 responses. However,
`PostToBridge` deliberately does NOT retry on 5xx (documented in the function comment:
"5xx responses → fail fast — same rationale: the bridge has already reserved the nonce").
The test was written before the fail-fast-on-5xx policy was hardened. The conflict is
pre-existing and was present before Phase 110.

**Status:** Pre-existing failure, out-of-scope for Phase 110. A future cleanup should
either remove the test or update `PostToBridge` to distinguish 503 (retriable) from
502/500 (fail-fast). Not blocking.
