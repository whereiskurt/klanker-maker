---
title: Phase 70 follow-up — bridge ActionPermalink returns "(unavailable)"
area: slack-bridge
created: 2026-05-24
origin: Phase 70 SC-10 UAT 2026-05-24
---

### Problem
The cross-agent switch sequence (Plan 70-06 Task 3) calls `km-slack permalink --channel C --ts T` to fetch the OLD thread permalink before posting the new top-level body. During SC-10 live UAT, the call returned a literal `(unavailable)` placeholder — the documented graceful-degradation path in CONTEXT.md "Failure modes". Switch sequence continued correctly, but the user-visible handoff post read `Switching to claude → continuing in this thread (unavailable)` instead of carrying a working Slack permalink. New top-level message similarly contained `Continuing from (unavailable)` instead of a back-link.

### Likely causes (in priority order)
1. **Bridge `ActionPermalink` handler bug** — Plan 70-04 added this action constant in `pkg/slack/payload.go` and wired the handler in `pkg/slack/bridge/handler.go`. The `chat.getPermalink` REST call may be misconfigured, or the auth scope may be missing.
2. **Bot scope `chat:read` or `channels:history` missing** — `chat.getPermalink` requires the bot to be able to read the channel; verify scopes via Slack app config.
3. **km-slack sidecar passes wrong args** — `permalink --channel X --ts Y` shape may not match what the handler expects in the envelope.

### Investigation steps
1. SSM into a sandbox: `sudo -u sandbox bash -lc '/opt/km/bin/km-slack permalink --channel $KM_SLACK_CHANNEL_ID --ts 1779653798.428019 2>&1'` — see what error comes back.
2. Bridge Lambda CloudWatch logs (`/aws/lambda/km-slack-bridge`) filtered to `action=permalink` for the time window of the SC-10 UAT (~20:52 UTC on 2026-05-24).
3. Verify the Slack app has `chat.getPermalink` scope; if not, add it.

### Files
- `pkg/slack/bridge/handler.go` (ActionPermalink case)
- `pkg/slack/client.go` (`GetPermalink` HTTP wrapper)
- `cmd/km-slack/main.go` (`runPermalink` / `runPermalinkWith`)

### Verification
After fix: SC-10 retry on a fresh sandbox should produce a handoff post containing a clickable Slack permalink (e.g. `https://<workspace>.slack.com/archives/C.../p<ts>`) instead of `(unavailable)`.

### Resolution (2026-05-24)
Root cause was option (3) from the priority list, with a twist: the bridge's `SlackPosterAdapter.GetPermalink` was sending the request as **POST with `application/json` body**, copying the wire-format pattern from the adjacent `PostMessage` / `ArchiveChannel` methods. `chat.getPermalink` is one of Slack's older read-only methods that does not accept JSON POST — Slack silently returns an empty permalink (or an error) instead of parsing the body. The bash fallback in `pkg/compiler/userdata.go` then substitutes the literal `(unavailable)` placeholder.

The slack-go SDK and Slack's own docs example use **GET with query-string args** for this method. Fix switches `GetPermalink` to GET + query string in both:

- `pkg/slack/bridge/aws_adapters.go` — the production path (the bridge calls this).
- `pkg/slack/client.go` — the operator-side Slack client, for consistency.

The Plan 70-04 `ActionPermalink` envelope handling itself was correct: km-slack `runPermalink` / `runPermalinkWith` properly set `env.MessageTS` after `BuildEnvelope` (which has no MessageTS parameter), the signing includes it, and the bridge handler's empty-MessageTS guard at `handler.go:404` would have caught a missing value. Bot scope `channels:history` was also already present (see `docs/slack-notifications.md` § Slack App scopes) — not the cause.

New tests pin the wire format:
- `TestSlackPosterAdapter_GetPermalink_UsesGETWithQueryString` — asserts HTTP method is GET, path is `/chat.getPermalink`, args are URL query parameters, and Content-Type is NOT `application/json`. Regression-guards a future "let's unify all Slack calls onto callJSON" refactor.
- `TestSlackPosterAdapter_GetPermalink_SlackError` — surfaces Slack's error code (e.g. `message_not_found`) when ok=false.

Also fixed two long-standing `go vet ./cmd/km-slack-bridge/` failures: `*stubSlackPoster` (in `main_test.go`) and `*uploadStubSlack` (in `main_upload_test.go`) were missing `GetPermalink` and `UpdateMessage` from the Phase 70 `bridge.SlackPoster` interface extension. Both stubs now return fixed values.

**Deploy:** `km init --sidecars` to push the bridge zip with the corrected `GetPermalink` to `km-slack-bridge` Lambda. No IAM or env changes required — same scopes, same Slack token.
