# Phase 74 Deferred Items

## Pre-existing test failure (out of scope)

**TestKmSlackPost_BridgeReturns503ThenSuccess_Exit0** in `cmd/km-slack/main_test.go`

The test expects the bridge client to retry on HTTP 503, but `PostToBridge` in
`pkg/slack/client.go` (line 447-451) explicitly does NOT retry on 5xx responses
to avoid nonce-replay issues. This test was already failing before Plan 74-01
(confirmed via git stash). It is out of scope for this plan.

Tracked here so a follow-up can decide: fix the test to reflect actual behavior
(fail-fast on 5xx) OR change PostToBridge to retry 503/502 before the nonce is
consumed. The latter requires nonce lifecycle changes.
