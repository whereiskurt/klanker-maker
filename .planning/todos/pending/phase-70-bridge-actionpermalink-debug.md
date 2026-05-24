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
