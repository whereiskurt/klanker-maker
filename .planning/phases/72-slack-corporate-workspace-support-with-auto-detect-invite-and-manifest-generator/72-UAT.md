---
phase: 72
status: draft
last_run: never
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
| A. Dev-machine `go test ./...` green | ⬜ | |
| B0. Install ordering (init→manifest→install→init→doctor) | ⬜ | |
| B1. Manifest renders correctly | ⬜ | |
| B2. Install + init + doctor all-OK | ⬜ | |
| B3. `km slack invite --dry-run` classifies native vs external | ⬜ | |
| B4. `km slack invite` native + Connect | ⬜ | |
| B5. Full `km create` (useSlackConnect true + false) | ⬜ | |
| B6. Doctor scope drift | ⬜ | |

Phase 72 is COMPLETE when Part A is green and every Part B row passes. Operator initials: __________  Date: ________
