---
name: klanker:sandbox
description: Detect sandbox environment, discover email policy, verify tooling
---

# Sandbox Detection & Identity

This skill detects whether you are running inside a Klanker Maker sandbox, discovers the email security policy, and verifies that email tooling is available.

**Invoke this skill first** before using `klanker:email` or `klanker:operator`.

## Step 1: Detect Environment

Check for the presence of KM environment variables:

```bash
echo "KM_SANDBOX_ID=$KM_SANDBOX_ID"
echo "KM_EMAIL_ADDRESS=$KM_EMAIL_ADDRESS"
echo "KM_OPERATOR_EMAIL=$KM_OPERATOR_EMAIL"
echo "KM_SANDBOX_ALIAS=$KM_SANDBOX_ALIAS"
echo "KM_SANDBOX_DOMAIN=$KM_SANDBOX_DOMAIN"
echo "KM_ARTIFACTS_BUCKET=$KM_ARTIFACTS_BUCKET"
```

If `KM_SANDBOX_ID` is empty, you are **not in a KM sandbox**. Stop here and inform the user. The `klanker:email` and `klanker:operator` skills require a sandbox environment.

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
test -x /opt/km/bin/km-send && echo "km-send: OK" || echo "km-send: MISSING"
test -x /opt/km/bin/km-recv && echo "km-recv: OK" || echo "km-recv: MISSING"
```

Both should be at `/opt/km/bin/` and on the PATH. If missing, email functionality is not available in this sandbox.

## Step 4: Verify Signing Key Access

```bash
aws ssm get-parameter --name "/sandbox/$KM_SANDBOX_ID/signing-key" --with-decryption --query 'Parameter.Value' --output text > /dev/null 2>&1 && echo "signing-key: OK" || echo "signing-key: MISSING"
```

If the signing key is missing and `signing: required`, outbound email will fail.

## Identity Summary

After running the above checks, you should know:

- **Who you are:** `$KM_SANDBOX_ID` (alias: `$KM_SANDBOX_ALIAS`)
- **Your email:** `$KM_EMAIL_ADDRESS`
- **Operator email:** `$KM_OPERATOR_EMAIL`
- **Email policy:** signing/encryption/verification settings
- **Tooling status:** km-send and km-recv availability

Use this context when invoking `klanker:email` or `klanker:operator`.
