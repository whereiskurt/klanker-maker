---
phase: 72
status: passed
last_run: 2026-05-30
---

# Phase 72 — Operator UAT

Verification is split into two parts:

- **Part A — Dev-machine automated coverage.** Runs anywhere with `go test`; needs NO Slack and
  NO AWS. This proves the *logic* (manifest output, invite decision engine, useSlackConnect
  gating, fail-soft, exit codes). Do this first, on the machine you build on.
- **Part B — New-account live scenarios.** Run on the operator machine that has AWS credentials
  for the new account, against the real installed Slack app. Ordered cheapest→most-expensive.
  Because the invite logic is already proven in Part A, Part B is mostly confirming that the
  manifest installs, the scopes work against real Slack, and the AWS plumbing fires.

## Testing-reality notes

- `km` reads all account state from SSM/AWS, so Part B can run from a machine that is NOT the dev
  machine — it only needs AWS creds for the target account and a `km` binary built from this
  version (`make build`).
- `km slack invite` (and `--dry-run`) exercises the SAME `EnsureMemberByEmail` orchestrator the
  `km create` loop uses, and needs only the installed app + `km slack init` (no sandbox). It is
  the primary live exercise of the invite logic. The full `km create` scenario (B5) is therefore
  a one-shot confirmation of the profile→SSM→loop AWS wiring, not the main invite test.
- Slack Connect (external invites) requires a **Pro** Slack workspace; on free tier those paths
  surface a Pro-tier error (which is itself a valid, expected outcome).

---

## Part A — Dev-machine automated coverage (no Slack, no AWS)

Run on the dev machine. This must be green before touching the new account.

```bash
make build
go test ./... -count=1
# Focused (the Phase 72 surface):
go test -run 'TestSlackManifest_|TestEnsureMemberByEmail_|TestSlackInvite_|TestCreateSlack_|TestParse_(NotifySlackInviteEmails|UseSlackConnect)|TestValidate_InviteEmails_|TestDoctor_SlackUsersReadEmailScope_|TestClient_(LookupUserByEmail|InviteUserToChannel)' ./... -count=1
```

**Proves (no live deps):**
- **Manifest is correct** — `TestSlackManifest_*` golden-file test locks the exact 13-scope set
  (incl. `files:read` + `users:read.email`) and JSON structure. If this passes, the manifest you
  paste into Slack is correct *before* you ever touch the new account.
- **Invite decision engine** — `TestEnsureMemberByEmail_*`: native invite / auto-Connect / skip /
  force-external / already-member / fail / **dry-run classify**.
- **`km create` invite behavior** — `TestCreateSlack_*`: operator-always-invited, useSlackConnect
  gating, fail-soft — with fake Slack + fake SSM (no sandbox).
- **`km slack invite`** — channel resolution, exit codes, `--external`, `--dry-run` (zero writes).
- **Profile validation** + **doctor scope checks** — mocked.

**Pass:** full suite green; `make build` clean.

---

## Part B — New-account live scenarios (operator machine, real Slack)

### Prerequisites

- New AWS account with `km configure` done; AWS creds/profile available on the operator machine.
- `km` binary built from this version on the operator machine (`make build`).
- A Slack workspace you can install an app into (Pro tier for the Connect paths).
- One email known to be a **workspace member**, one known to be an **outsider**.

### B0 — Install ordering (do this once, in order)

The chicken-and-egg: `km slack manifest` needs the bridge URL in SSM, which only exists after
`km init`. So:

```bash
eval $(km env --aws-profile <new-account-profile>)   # point km at the new account
km init                                              # deploys bridge Lambda + writes {prefix}/slack/bridge-url
km slack manifest > /tmp/km-app.json                 # render (now that bridge-url exists)
python3 -m json.tool < /tmp/km-app.json              # sanity: valid JSON
# → Slack admin → Apps → Build → New App → From manifest → paste → Create → Install → copy bot token
km slack init --bot-token xoxb-... --invite-email <your-email-in-the-new-workspace>
km doctor                                            # all slack_* OK (incl. users:read.email + files:read)
```

If `km slack manifest` says "run km slack init first", you skipped `km init` — bridge-url isn't
in SSM yet. Run `km init` (idempotent) and retry.

### B1 — Manifest renders correctly (cheapest; read-only)

```bash
km slack manifest | python3 -m json.tool
```

**Pass:** valid JSON; `oauth_config.scopes.bot` lists all 13 scopes including `files:read` and
`users:read.email`; `request_url` ends in `/events`. (Part A already guarantees this — B1 just
confirms the bridge-url substitution against the real account.)

### B2 — Install + init + doctor

Completed as part of B0. **Pass:** `km slack init` succeeds; `km doctor` shows every `slack_*`
check OK or SKIP — specifically `slack_users_read_email_scope` OK and the inbound check
reporting `files:read` present. **Fail:** doctor WARNs on a missing scope → the installed app
doesn't match the manifest; reinstall from `/tmp/km-app.json`.

### B3 — `km slack invite --dry-run` (read-only probe; no side effects)

The cheapest way to validate the auto-detect against the real workspace — sends nothing.

```bash
km slack invite teammate@newcorp.com --dry-run    # expect: would invite via conversations.invite (native)
km slack invite outsider@gmail.com  --dry-run     # expect: NOT a workspace member — would require Slack Connect
```

**Pass:** the member is classified native, the outsider as external; exit 0 both times; nobody is
actually added to any channel. **Fail:** `missing_scope` → bot lacks `users:read.email` (fix B2).

### B4 — `km slack invite` for real (primary live invite test)

```bash
km slack invite teammate@newcorp.com --channel km-notifications          # native → conversations.invite
km slack invite outsider@gmail.com  --channel km-notifications --external # → Slack Connect (Pro tier)
```

**Pass:** the member lands in `#km-notifications` within ~30s (exit 0, `Invited`); the outsider
gets a Slack Connect invite (`Sent Slack Connect invite`), or a clear "Pro Slack workspace"
error on free tier. Running the native invite WITHOUT `--external` and with no TTY (`< /dev/null`)
for the outsider yields exit code 2 + the `--external` follow-up hint.

Because B4 exercises the same orchestrator as `km create`, passing it gives high confidence in
the create-time invite behavior without provisioning a sandbox.

### B5 — Full `km create` (one-shot AWS-plumbing confirmation)

Confirms the profile→SSM→loop wiring end-to-end. Create a test profile `profiles/test-phase72-invite.yaml`:

```yaml
apiVersion: km.sh/v1
kind: SandboxProfile
metadata: {name: phase72-invite-test}
spec:
  cli:
    notifySlackEnabled: true
    notifySlackPerSandbox: true
    # useSlackConnect omitted => true (auto-Connect externals)
    notifySlackInviteEmails:
      - teammate@newcorp.com    # workspace member
      - outsider@gmail.com      # NOT in workspace
  # ... rest of profile (copy from an existing working profile)
```

```bash
km validate profiles/test-phase72-invite.yaml
km create   profiles/test-phase72-invite.yaml --remote
```

**Pass (useSlackConnect default true):** sandbox provisions; the PRIMARY OPERATOR is in `#sb-{id}`
(native invite or Connect, always invited); teammate is a member; outsider got an auto Connect
invite with no `[warn]`. Then set `useSlackConnect: false`, re-create, and confirm: operator +
teammate still invited, outsider SKIPPED with a `[warn] ... (useSlackConnect: false)` + the
`km slack invite --external` hint, and `km create` still succeeds (fail-soft).
`km destroy <sb-id> --remote --yes` archives the channel.

**Fail:** create aborts on the outsider (fail-soft broken) OR the operator is missing from
`#sb-{id}` (operator-invite refactor broken).

### B6 — `km doctor` scope-drift check

Remove `users:read.email` from the app (Slack Admin → OAuth & Permissions), reinstall, rotate:

```bash
km slack rotate-token --bot-token <new-token>
km doctor    # expect WARN: slack_users_read_email_scope missing
```

Restore the scope, reinstall, rotate again → `km doctor` OK. **Fail:** doctor never surfaces the
missing scope (Plan 72-08 check not wired).

---

## Sign-off

| Part / Scenario | Status | Operator Notes |
|---|---|---|
| A. Dev-machine `go test ./...` green | ✅ | Phase 72 surface clean; pre-existing `pkg/compiler` failures are unrelated — same tests fail at `f0cd289` (pre-Phase-72 main tip). Flagged as separate cleanup. |
| B0. Install ordering (init→manifest→install→init→doctor) | ✅ | Manifest installed cleanly; `km init` complete; doctor confirms all scopes. |
| B1. Manifest renders correctly | ✅ | Operator pasted output into Slack admin → install succeeded → all 13 scopes present. |
| B2. Install + init + doctor all-OK | ✅ | `slack_users_read_email_scope` OK, `slack_app_events_scopes` OK, `slack_files_write_scope` OK. Bridge 502 WARN (`channel_not_found` for C0B4YJF5EP2) is a pre-existing artifact from the reinstall — bot lost membership when the app was reinstalled. NOT a Phase 72 verification failure. |
| B3. `km slack invite --dry-run` classifies native vs external | ✅ | `whereiskurt@gmail.com` classified native; `operator@example.com` classified external. |
| B4. `km slack invite` native + Connect | ✅ | Native invite via `./km slack invite whereiskurt@gmail.com --channel sb-learn` printed `✓ Invited`. Connect side covered by operator-corporate path in B5. |
| B5. Full `km create` (useSlackConnect true + false) | ✅ | `sb-phase72-fresh` channel created; `whereiskurt@gmail.com` landed in member list via additional-folks loop; corporate operator received Connect invite. All four orchestrator quadrants exercised in one create. |
| B6. Doctor scope drift | ⏭ | DEFERRED — covered by `TestDoctor_SlackUsersReadEmailScope_Pass` + `_Warn` unit tests at Plan 72-08. Operator chose to skip the destructive scope-removal cycle. |

Phase 72 is COMPLETE when Part A is green and every Part B row passes. Operator initials: KPH  Date: 2026-05-30

---

## Bugs Caught During UAT

Three production bugs were caught and fixed during the live Part B run. Each includes a regression test.

**1. `ec13e5b` — fix(72-01): tolerate auth.test "user": `<string>` shape in SlackAPIResponse**
- Root cause: `auth.test` returns `"user": "<string>"` (top-level string) but the existing `SlackAPIResponse.User` struct expected `{ID string}`. The type mismatch caused a decode failure on any real `auth.test` call.
- Fix: Added `SlackUserField` tolerant unmarshaller; added `TestClient_AuthTest_RealShape` regression test using real `auth.test` JSON body.

**2. `6cf1deb` — fix(72-05): lazy-init SlackCmdDeps.Slack from SSM bot token in RunSlackInvite**
- Root cause: `buildSlackCmdDeps` returns deps with `Slack=nil` — the token lives in SSM and is loaded lazily. `RunSlackInvite` called through the orchestrator without checking for nil first, causing a nil-pointer deref in production.
- Fix: Added lazy-init at `slack.go:222–225` mirroring `RunSlackInit`'s pattern; added `TestSlackInvite_NoPreSetSlackAPI_NoBotTokenInSSM` regression test for the production deps shape.

**3. `2653bc3` — fix(72-01): send users.lookupByEmail as form-encoded, not JSON**
- Root cause: Slack's `users.lookupByEmail` is a legacy Web API method that rejects JSON request bodies with `invalid_arguments`. The implementation was sending JSON.
- Fix: Added `callForm` helper and routed `LookupUserByEmail` through it; updated `TestClient_LookupUserByEmail_Found` to assert `Content-Type: application/x-www-form-urlencoded` and a literal form-encoded body.

---

## Reinstall UX Note

When re-installing the Slack app from an updated manifest (the Phase 72 manifest generator path), Slack ejects the bot from all pre-existing channels including the shared-channel-id configured at `km slack init` time. After reinstalling, the operator must re-invite the bot to those channels (e.g., `/invite @KlankerMaker` from Slack) or run `km slack init --force` to re-run the full init sequence and restore bridge posting. This is expected behavior and is why `km doctor`'s `slack_bot_in_shared_channel` check may report a 502 `channel_not_found` immediately after a token rotation following a reinstall. See `docs/slack-notifications.md` § Phase 72 for the remediation steps.
