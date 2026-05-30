# profiles/secrets/

SOPS-encrypted secret bundles attached to SandboxProfiles via
`spec.secrets.sopsFile`. The full design + sandbox-side decrypt flow lives in
[`docs/sandbox-secrets.md`](../../docs/sandbox-secrets.md); this is the
short operator cheat sheet.

## Conventions

- Filenames **must** end in `.enc.yaml` (enforced by `km validate`).
- Bundle contents are flat `KEY: value` YAML. Each key becomes a sandbox
  environment variable exposed in every login shell.
- One bundle per blast radius, not per profile. Multiple profiles may share
  the same `*.enc.yaml` if they should see the same secrets.
- A local [`.gitignore`](.gitignore) in this folder blocks every file by
  default and whitelists only `README.md`, `EXAMPLE.yaml`, and `*.enc.yaml`.
  Plaintext bundles created here are ignored automatically; do not weaken
  the whitelist.

## Template

[`EXAMPLE.yaml`](EXAMPLE.yaml) is a reference template showing the shape of
a bundle (OpenAI, Anthropic, GitHub, and a custom-key block) plus the
encrypt recipe inline as comments. It is plaintext and committed on
purpose — copy values out of it, never into it.

## Prerequisites (one-time)

```bash
brew install sops                       # decrypt/encrypt CLI
km bootstrap --shared-secrets-key --dry-run=false   # creates alias/{prefix}-sandbox-secrets
km doctor                               # confirm ✓ Shared secrets KMS key
```

## Create a new bundle

```bash
# Resolve the KMS alias ARN for this install (don't hard-code the account ID)
KMS_ARN=$(aws kms list-aliases --region us-east-1 \
  --query "Aliases[?AliasName=='alias/$(yq -r .resource_prefix km-config.yaml)-sandbox-secrets'].AliasArn" \
  --output text)

# Write plaintext to /tmp (never inside the repo)
cat > /tmp/codex.yaml <<'EOF'
OPENAI_API_KEY: sk-proj-...
ANTHROPIC_API_KEY: sk-ant-...
EOF

sops --encrypt --kms "$KMS_ARN" \
     --input-type yaml --output-type yaml \
     /tmp/codex.yaml > profiles/secrets/codex.enc.yaml

rm /tmp/codex.yaml
```

## Edit an existing bundle

```bash
AWS_PROFILE=<your-profile> sops profiles/secrets/codex.enc.yaml
```

Opens `$EDITOR` on the decrypted contents; saves re-encrypt automatically.
Same command decrypts to stdout with `sops -d profiles/secrets/codex.enc.yaml`.

## Reference from a profile

```yaml
# profiles/codex.yaml
spec:
  secrets:
    sopsFile: ./secrets/codex.enc.yaml
```

Path is relative to the profile file. `km validate` rejects bundles that
don't end in `.enc.yaml`.

## Key rotation hazard

These bundles are sealed by a **specific KMS CMK**, not by the alias name.
If the underlying key is scheduled for deletion (e.g. `km uninit` or the
old stale-KMS sweeper bug), `km bootstrap --shared-secrets-key` will mint a
**new** CMK and re-point the alias — and every pre-existing `*.enc.yaml`
becomes undecryptable (`IncorrectKeyException`).

Mitigations:
- Keep an out-of-band copy of the plaintext for keys that must survive
  rotation, OR
- Before destroying the secrets KMS key, decrypt + re-encrypt every bundle
  under the new key, OR
- For lost-key recovery, `aws kms cancel-key-deletion` on the original
  CMK (while still in `PendingDeletion`), decrypt, then re-encrypt against
  the live alias.
