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

## Pre-existing compiler test failures (out of scope)

Confirmed pre-existing on the Plan 74-01 final commit (0e3dcf6) via git checkout
+ test re-run. None are caused by any Plan 74 change; they predate this phase
(last touched in Phase 63-04 according to git log on `userdata_notify_test.go`).

- `TestUserDataNotifyEnvVars_NoneSet_NoEnvBlock` (userdata_notify_test.go:56) —
  expects `/etc/profile.d/km-notify-env.sh` NOT to be emitted when `Spec.CLI`
  is nil; current userdata.go always writes the env block.
- `TestUserDataNotifyEnv_NoChannelOverride_NoChannelID`
- `TestUserDataNotifyEnv_BridgeURLNeverEmittedAtCompileTime`
- `TestUserDataKMTracingServicectlStart`
- `TestAuditHookNonBlocking`
- `TestGitHubUserDataGITASKPASS`

These are tracked here so a future cleanup pass can either update the test
expectations to match current behavior or restore the conditional emission.
