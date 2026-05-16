---
name: sandbox
description: Detect sandbox environment, discover email policy, verify email and Slack tooling
---

# Sandbox Detection & Identity

This skill detects whether you are running inside a Klanker Maker sandbox, discovers the email security policy, and verifies that email and Slack tooling is available.

**Invoke this skill first** before using `klanker:email`, `klanker:operator`, or `klanker:slack`.

## Cross-references

- `klanker:email` — send/receive email between sandboxes and to the operator
- `klanker:slack` — post from inside a sandbox to its per-sandbox Slack channel
- `klanker:operator` — natural-language requests to the operator inbox
- `klanker:init` — operator-side companion (one-time platform setup that provisions the env this skill detects)

## Step 1: Detect Environment

Check for the presence of KM environment variables:

```bash
echo "KM_SANDBOX_ID=$KM_SANDBOX_ID"
echo "KM_SANDBOX_EMAIL=$KM_SANDBOX_EMAIL"
echo "KM_EMAIL_ADDRESS=$KM_EMAIL_ADDRESS"
echo "KM_OPERATOR_EMAIL=$KM_OPERATOR_EMAIL"
echo "KM_SANDBOX_ALIAS=$KM_SANDBOX_ALIAS"
echo "KM_SANDBOX_DOMAIN=$KM_SANDBOX_DOMAIN"
echo "KM_ARTIFACTS_BUCKET=$KM_ARTIFACTS_BUCKET"
echo "KM_ALIAS_EMAIL=$KM_ALIAS_EMAIL"
# Slack-related (only populated when notifySlackEnabled: true in profile)
echo "KM_NOTIFY_SLACK_ENABLED=$KM_NOTIFY_SLACK_ENABLED"
echo "KM_SLACK_CHANNEL_ID=$KM_SLACK_CHANNEL_ID"
echo "KM_SLACK_BRIDGE_URL=$KM_SLACK_BRIDGE_URL"
echo "KM_SLACK_THREAD_TS=$KM_SLACK_THREAD_TS"
```

If `KM_SANDBOX_ID` is empty, you are **not in a KM sandbox**. Stop here and inform the user. The `klanker:email`, `klanker:operator`, and `klanker:slack` skills require a sandbox environment.

If `KM_NOTIFY_SLACK_ENABLED` is empty or `0`, Slack posting is not provisioned for this sandbox — `klanker:slack` will fail. Email-based skills still work.

## Step 2: Discover Email Policy

The sandbox profile controls signing, encryption, and sender verification. Read the deployed profile:

```bash
cat /opt/km/.km-profile.yaml 2>/dev/null || echo "NO_PROFILE"
```

Extract the `spec.email` block. The relevant fields are:

| Field | Values | Meaning |
|-------|--------|---------|
| `signing` | `required` / `optional` / `off` | Whether outbound email must be Ed25519-signed |
| `verifyInbound` | `required` / `optional` / `off` | Whether inbound signatures must verify |
| `encryption` | `required` / `optional` / `off` | Whether outbound email must be NaCl-encrypted |
| `allowedSenders` | `["self"]`, `["*"]`, `["sb-xxx"]` | Who can send email to this sandbox |

**If no `spec.email` block exists:** Email policy is not enforced — signing is off, verification is skipped, all senders are accepted. This is the default for legacy sandboxes.

**If `signing: required`:** Every `km-send` call will sign automatically. If signing fails, the send fails. This is correct behavior — do not try to bypass it.

**If `verifyInbound: required`:** When reading email with `km-recv --json`, check the `signature` field. Reject or warn on any message where signature is not `"OK"`.

**If `encryption: required`:** Outbound email is encrypted automatically by `km-send` when the recipient's encryption public key is available. If the recipient has no encryption key, the send will fail.

## Step 3: Verify Tooling

Confirm the email utilities are available:

```bash
test -x /opt/km/bin/km-send  && echo "km-send: OK"  || echo "km-send: MISSING"
test -x /opt/km/bin/km-recv  && echo "km-recv: OK"  || echo "km-recv: MISSING"
test -x /opt/km/bin/km-slack && echo "km-slack: OK" || echo "km-slack: MISSING (Slack disabled or pre-Phase-63 sandbox)"
```

km-send and km-recv should be at `/opt/km/bin/` and on the PATH. If missing, email functionality is not available in this sandbox.

km-slack is only present when the sandbox was created after `km init --sidecars` shipped Phase 63. If `KM_NOTIFY_SLACK_ENABLED=1` but the binary is missing, the sandbox needs to be recreated.

## Step 4: Verify Signing Key Access

```bash
aws ssm get-parameter --name "/sandbox/$KM_SANDBOX_ID/signing-key" --with-decryption --query 'Parameter.Value' --output text > /dev/null 2>&1 && echo "signing-key: OK" || echo "signing-key: MISSING"
```

If the signing key is missing and `signing: required`, outbound email will fail. The same key is also used by `km-slack` to sign Slack envelopes — a missing key breaks both `klanker:email` (sandbox-to-sandbox) and `klanker:slack`.

**External email exception:** If you are sending to a non-sandbox recipient (Gmail, corporate email, etc.), use `km-send --no-sign` — it skips the signing key fetch entirely. The signing key is only required for sandbox-to-sandbox communication. See `klanker:email` skill section "Sending to External Recipients" for details.

## Identity Summary

After running the above checks, you should know:

- **Who you are:** `$KM_SANDBOX_ID` (alias: `$KM_SANDBOX_ALIAS`)
- **Your email:** `$KM_SANDBOX_EMAIL` (also `$KM_EMAIL_ADDRESS`)
- **Operator email:** `$KM_OPERATOR_EMAIL`
- **Email policy:** signing/encryption/verification settings
- **Tooling status:** km-send, km-recv, km-slack availability
- **Slack status:** whether `KM_NOTIFY_SLACK_ENABLED=1` and the channel/bridge env vars are populated

- **External email policy:** For non-sandbox recipients use `km-send --no-sign`. Inbound replies from non-sandbox senders must include `KM-AUTH: <safe-phrase>` in the body — otherwise the `km-mail-poller` filter drops them silently to `/var/mail/km/skipped/`.

Use this context when invoking `klanker:email`, `klanker:operator`, or `klanker:slack`.
