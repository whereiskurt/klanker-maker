# SOPS secret injection for sandboxes (Phase 89)

Declarative SOPS-encrypted secrets attached to a SandboxProfile. At boot, the
sandbox decrypts the bundle using a shared per-install KMS key and exposes the
keys as environment variables in login shells (`/etc/sandbox-secrets.env` +
`/etc/profile.d/zz-sandbox-secrets.sh`).

## When to use SOPS secrets vs `spec.execution.secrets`

- **`spec.secrets.sopsFile`** (Phase 89): declarative bundle versioned with
  profiles, decrypted at boot, env-var exposed. Right answer for API keys
  (`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GITHUB_TOKEN`) that downstream
  tooling reads from environment variables.
- **`spec.execution.secrets`** (legacy SSM path): list of SSM SecureString paths
  injected at sandbox-side runtime. Right answer for secrets owned by other AWS
  systems (e.g., RDS connection strings provisioned by a separate stack).

## Prerequisites

1. `km bootstrap --shared-secrets-key` (or `km bootstrap --all`) to provision
   the per-install KMS key. Verify with `km doctor` — look for
   `✓ Shared secrets KMS key healthy`.
2. Install `sops` locally (operator workstation):
   `brew install sops` (Mac) or download from
   `https://github.com/getsops/sops/releases`.
   The sandbox-side binary is auto-pushed to S3 by `km init --sidecars`.
3. Optional but recommended: `brew install age` for offline encryption keys
   used in CI/CD testing.

## Repo layout convention

`km configure` writes two lines to `.gitignore`:

```
/secrets/*
!/secrets/*.enc.yaml
```

Encrypted bundles version with profiles but plaintext workdir files never leak.
Encrypted bundles live under `secrets/`; names are operator-chosen (e.g.,
`secrets/codex.enc.yaml`, `secrets/shared.enc.yaml`). Operators decide
profile-to-bundle topology based on blast radius — km does NOT enforce 1:1 or
1:N relationships.

## Workflow

1. Bootstrap the shared KMS key (one-time per install):
   ```bash
   km bootstrap --shared-secrets-key
   km doctor   # verify: ✓ Shared secrets KMS key healthy
   ```

2. Create and encrypt a secrets bundle:
   ```bash
   # Find the KMS alias
   aws kms list-aliases --query \
     'Aliases[?starts_with(AliasName, `alias/km-sandbox-secrets`)]'

   # Create plaintext bundle (never commit this file)
   cat > /tmp/codex.yaml <<EOF
   OPENAI_API_KEY: sk-proj-...
   ANTHROPIC_API_KEY: sk-ant-...
   EOF

   # Encrypt with the KMS alias (NOT the raw key ARN)
   sops --encrypt \
        --kms 'arn:aws:kms:us-east-1:<account-id>:alias/km-sandbox-secrets' \
        --input-type yaml --output-type yaml \
        /tmp/codex.yaml > secrets/codex.enc.yaml

   # Remove the plaintext
   rm /tmp/codex.yaml
   ```

3. Reference from a SandboxProfile:
   ```yaml
   spec:
     secrets:
       sopsFile: ./secrets/codex.enc.yaml
   ```

4. Validate and provision:
   ```bash
   km validate profiles/codex.yaml
   km create profiles/codex.yaml
   ```

5. Inside the sandbox, secrets are available as environment variables in all
   login shells:
   ```bash
   echo $OPENAI_API_KEY
   ```

## Troubleshooting

**Decrypt 403 at boot**
Check: (a) the sandbox IAM role has `kms:Decrypt` with a
`kms:ResourceAliases` condition matching
`alias/${resource_prefix}-sandbox-secrets` (auto-emitted by ec2spot module
v1.2.0+); (b) the alias is attached to the key — run
`aws kms list-aliases --query 'Aliases[?AliasName==\`alias/km-sandbox-secrets\`]'`.

**Sandbox boots but env vars missing**
Check `/var/log/cloud-init-output.log` for the
`[km-bootstrap] FATAL: sops decrypt failed` line.
Check `cat /etc/sandbox-secrets.env` (root-readable only). If the file exists
but is empty, the dotenv conversion failed — check the bundle for non-ASCII
values or values with embedded newlines.

**`km validate` fails with "missing 'sops:' metadata block"**
The file is plaintext, not sops-encrypted. Re-run `sops --encrypt`.

**First-boot timing pitfall**
The decrypt block runs after sidecar startup so instance profile credentials
are available. If you observe transient 401 errors on very fast instance types,
check whether IMDS is ready before the decrypt runs
(`/var/log/km-bootstrap.log`).

## Security model (v1 and v2)

**v1 (Phase 89):** One shared KMS key per install
(`alias/${resource_prefix}-sandbox-secrets`). Any sandbox in the install can
decrypt any bundle it receives (per-sandbox isolation comes from S3 IAM scoping
to `sandboxes/${sandbox_id}/secrets.enc.yaml`, NOT KMS). Blast radius: a
compromised sandbox can decrypt its own bundle only (S3 ARN scope), but the
shared key is broadly usable within the install.

**v2 (deferred):** Per-profile or per-sandbox KMS key isolation via key policy
and `aws:PrincipalTag`. Schema-compatible — v1 profiles keep working when v2
ships.

## Operator commands cheat-sheet

| Command | What it does |
|---------|---|
| `km bootstrap --shared-secrets-key` | Provision the shared KMS key (one-time per install) |
| `km bootstrap --shared-secrets-key --plan` | Preview with Phase 84.2 destroy-class gate |
| `km doctor` | Verifies alias exists and warns on orphan sibling aliases |
| `km uninit` | Deletes own-prefix alias and schedule-deletes own key (7-day window); sibling installs untouched |
| `km init --sidecars` | Pushes sops binary to S3 (one-time per phase or sops-version bump) |
