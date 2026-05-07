# km vscode — local desktop VS Code → sandbox via Remote-SSH over SSM

**Status:** Draft (rewrite after POC validation; pending phase planning)
**Date:** 2026-05-06
**Target phase:** 73
**Supersedes:** Original v1 (VS Code Web / `code serve-web`) — rejected after POC because it
forced a browser experience instead of letting operators use their local desktop VS Code.

## Summary

Add `km vscode start | status` so an operator can connect their **local desktop VS Code**
(via the Remote-SSH extension) to a sandbox over SSM port-forward.

km auto-generates a per-sandbox ed25519 keypair on the operator's laptop at `km create` time,
ships the pubkey to the sandbox via userdata, and writes it into `/home/sandbox/.ssh/authorized_keys`
at boot. `km vscode start <sb>` opens a foreground SSM port-forward (sandbox port 22 →
operator local port 2222), upserts a managed entry in `~/.ssh/config`, and prints "F1 →
Remote-SSH → connect to host `km-sb-<id>`."

VS Code's Remote-SSH extension auto-deploys `vscode-server` to the sandbox on first connect.
**Nothing related to VS Code installs on the sandbox itself.** km only manages sshd + an
authorized_keys entry.

## Non-goals

- **Browser-based VS Code (`code serve-web`).** Explicitly rejected after POC.
- **Microsoft Remote Tunnels (`code tunnel`).** Bypasses the SSM security boundary, requires
  GitHub auth.
- **Auto-stop / `--duration` time-boxing.** Operator can compose with `km at`.
- **DDB schema changes, new SSM Parameter Store entries, new Lambda, new sidecar binary.**
- **Cross-machine key portability** (`km vscode export-key`/`import-key`). Document that
  keys are per-machine; revisit if it bites.
- **Multiple operators sharing one sandbox** via multi-line authorized_keys.
- **Proper SSH host-key trust** (TOFU, cert-based). v1 uses `StrictHostKeyChecking no` +
  `UserKnownHostsFile /dev/null`. Flagged as security follow-up.
- **`km vscode stop`.** Foreground port-forward + Ctrl-C is enough; sshd stays up.
- **Customizing sshd config** (port, ciphers). Operators who want to harden can use
  `spec.execution.configFiles`.
- **Pre-installing any VS Code component on the sandbox.** Remote-SSH handles `vscode-server`
  deployment.

## Architecture

```
operator laptop                                      sandbox EC2 (via SSM)
─────────────                                        ─────────────────────
km create <profile>
  ├─ generate ed25519 keypair locally
  │    ~/.km/keys/sb-<id>      (priv, 0600)
  │    ~/.km/keys/sb-<id>.pub  (pub,  0644)
  └─ pass pubkey content to userdata template ──▶  cloud-init writes:
                                                    /home/sandbox/.ssh/authorized_keys
                                                    + systemctl enable --now sshd
                                                    + restorecon (AL2023 SELinux)

km vscode start <sb>
  ├─ upsert ~/.ssh/config Host entry
  ├─ open SSM port-forward (foreground) ────────▶  sshd:22 ◀── ssm port-forward
  └─ print "F1 → Remote-SSH → km-sb-<id>"

VS Code on operator laptop
  └─ Remote-SSH connects via ~/.ssh/config ─────▶  vscode-server (auto-deployed)
                                                   filesystem at /workspace
                                                   terminal as 'sandbox' user

km destroy <sb>
  ├─ remove Host km-sb-<id> from ~/.ssh/config
  └─ delete ~/.km/keys/sb-<id>*
```

## Ownership split

| Layer | What it owns |
|-------|--------------|
| `pkg/sshkey/` (new) | ed25519 keypair generation + OpenSSH-format file writing |
| `internal/app/cmd/create.go` (modified) | Call keypair generator, populate `VSCodeSSHPubKey` template variable |
| `internal/app/cmd/sshconfig.go` (new) | `~/.ssh/config` managed-block parser/writer |
| `pkg/profile/` (modified) | `spec.cli.vscodeEnabled` (`bool*`, default `true`) |
| `pkg/compiler/userdata.go` (modified) | Conditional block: enable+start sshd, write authorized_keys, restorecon |
| `internal/app/cmd/vscode.go` (new) | `start` and `status` subcommands |
| `internal/app/cmd/destroy.go` (modified) | Cleanup ssh-config block + key files |
| `docs/vscode.md` (new) | Operator setup guide |

## Sandbox-side contract

Provisioned at sandbox boot via cloud-init userdata, gated on `spec.cli.vscodeEnabled: true`:

| Path | Purpose | Mode | Owner |
|------|---------|------|-------|
| `/home/sandbox/.ssh/` | Standard SSH user dir | 0700 | sandbox:sandbox |
| `/home/sandbox/.ssh/authorized_keys` | Operator pubkey (single line) | 0600 | sandbox:sandbox |

sshd state: `enabled` and `started` via `systemctl enable --now sshd`. SELinux contexts
applied via `restorecon -R -v /home/sandbox/.ssh` (mandatory on AL2023).

### Userdata block (illustrative)

```bash
{{- if .VSCodeEnabled }}
# VS Code Remote-SSH access (Phase 73)
systemctl enable --now sshd
mkdir -p /home/sandbox/.ssh
chmod 700 /home/sandbox/.ssh
cat > /home/sandbox/.ssh/authorized_keys << 'KEY'
{{ .VSCodeSSHPubKey }}
KEY
chmod 600 /home/sandbox/.ssh/authorized_keys
chown -R sandbox:sandbox /home/sandbox/.ssh
restorecon -R -v /home/sandbox/.ssh   # AL2023 SELinux contexts
{{- end }}
```

`VSCodeEnabled` and `VSCodeSSHPubKey` are new fields on the userdata template input struct,
populated from the profile + the locally-generated pubkey.

## Operator-side contract

### Local key files

- `~/.km/keys/` — directory, 0700, owned by operator.
- `~/.km/keys/sb-<id>` — ed25519 private key, OpenSSH PEM format, mode 0600.
- `~/.km/keys/sb-<id>.pub` — single-line OpenSSH-format public key (`ssh-ed25519 AAAA... km-sb-<id>`),
  mode 0644.

Generated by `pkg/sshkey.GenerateAndWrite(privPath, pubPath, comment string) (pubContent string, err error)`
during `km create`. Implementation: `crypto/ed25519` + `golang.org/x/crypto/ssh.MarshalAuthorizedKey`
+ `crypto/x509.MarshalPKCS8PrivateKey` (or equivalent OpenSSH PEM) — no shelling to `ssh-keygen`.

### `~/.ssh/config` managed block

Created on first `km vscode start`. Located in operator's `~/.ssh/config` (created with mode
0600 if absent). Contents between markers:

```
# BEGIN km vscode hosts (managed; do not edit between markers)
Host km-sb-abc123
  HostName localhost
  Port 2222
  User sandbox
  IdentityFile ~/.km/keys/sb-abc123
  IdentitiesOnly yes
  StrictHostKeyChecking no
  UserKnownHostsFile /dev/null
  ServerAliveInterval 30
# END km vscode hosts
```

Parser/writer rules:

- If `~/.ssh/config` doesn't exist: create with mode 0600, add markers + entry.
- If markers don't exist: append markers + entry.
- If markers exist + entry for this sandbox: replace just that entry.
- If markers exist + no entry for this sandbox: insert before END marker.
- Anything outside the markers is never read or modified.
- Manual edits inside the markers (e.g., adding other Host entries or per-host comments) are
  preserved as long as the targeted Host's entry is replaced wholesale (not line-edited).

## CLI surface

```
km vscode start  <sandbox-id> [--local-port N]
km vscode status <sandbox-id>
```

### `start`

1. Resolve sandbox → instance ID + region (existing helper).
2. Verify `~/.km/keys/sb-<id>` exists. If not, fail with: "Private key for sb-<id> not found
   at `~/.km/keys/sb-<id>`. If you created this sandbox on a different machine, copy the
   `~/.km/keys/sb-<id>*` files over."
3. SSM check: `systemctl is-active sshd` (must return `active`) AND `test -f /home/sandbox/.ssh/authorized_keys`.
   If either fails, surface "VS Code not enabled in this sandbox's profile (set
   `spec.cli.vscodeEnabled: true` and recreate the sandbox)."
4. Upsert `~/.ssh/config` managed block via the parser/writer.
5. Print connection block:
   ```
   ✓ Updated ~/.ssh/config (Host: km-sb-abc123)
   ✓ Forwarding localhost:2222 → sandbox:22

   In VS Code: F1 → "Remote-SSH: Connect to Host..." → km-sb-abc123
   Press Ctrl-C to close the tunnel (sshd keeps running on the sandbox).
   ```
6. Open the foreground SSM port-forward via existing `buildPortForwardCmd` (`shell.go:577`).

Flags:
- `--local-port N` overrides 2222 if taken on the operator's laptop. The `~/.ssh/config`
  entry's `Port` line is updated to match.

### `status`

SSM checks: `systemctl is-active sshd`, `test -f /home/sandbox/.ssh/authorized_keys`,
`grep -F "<operator pubkey>" /home/sandbox/.ssh/authorized_keys`. Returns non-zero if any
check fails. Output is a one-line summary.

### No `stop` in v1

Foreground port-forward + Ctrl-C is enough. sshd stays up; reconnect with another `start`.

## `km create` integration

Two modes:

- **Local create** (`km create profile.yaml`, no `--remote`): generate keypair locally,
  populate `VSCodeSSHPubKey` directly into the userdata template input struct.
- **Remote create** (`km create profile.yaml --remote`, the default for EC2): generate keypair
  locally on the operator's machine **before** invoking the management Lambda. Pass the pubkey
  content as part of the existing Lambda invocation payload (alongside profile data). The
  Lambda's userdata renderer threads it through.

Either path: when `vscodeEnabled: false`, key generation is skipped (saves disk; nothing to
authenticate).

## `km destroy` integration

Existing destroy flow gets two cleanup steps:

1. Read `~/.ssh/config`, find the `Host km-sb-<id>` entry inside the managed block, remove it.
   If markers + entry both exist and this is the only entry, optionally remove the markers
   too (keeps the file tidy).
2. Delete `~/.km/keys/sb-<id>` and `~/.km/keys/sb-<id>.pub`. Idempotent (no error if already
   gone, e.g., for sandboxes created before Phase 73 lands).

Cleanup runs unconditionally — `km destroy` doesn't need to know whether the sandbox had
vscode enabled. Missing files are non-errors.

## Profile schema

New field under `spec.cli`:

| Field | Type | Default | Effect |
|-------|------|---------|--------|
| `vscodeEnabled` | `bool*` (pointer-to-bool, omit ⇒ default `true`) | `true` | Userdata enables sshd, drops authorized_keys, restorecons SELinux |

Inheritance follows the existing `spec.cli.*` pattern. Schema addition follows the same
JSON-schema + types.go + validate.go pattern as `notifySlackEnabled`,
`notifySlackPerSandbox`, `notifySlackTranscriptEnabled`.

## Auth & security model

- **SSM tunnel = real security boundary.** IAM-authenticated, encrypted, per-session.
- **SSH on top adds key auth.** Per-sandbox ed25519; private never leaves operator's laptop;
  pubkey only ever exists on operator + the one sandbox it was generated for.
- **Compromise blast radius = 1 sandbox.** No shared key.
- **`StrictHostKeyChecking no` + `UserKnownHostsFile /dev/null` for v1.** Acceptable because
  SSM IAM already authenticates the target instance; SSH host key is defense-in-depth. Proper
  TOFU/cert-based trust is a deferred follow-up.
- **No public exposure of port 22.** SG remains egress-only; SSM port-forward bypasses SG and
  reaches the SSM agent on the instance, which connects to localhost:22.
- **Operator's laptop disk security** is the operator's responsibility — same as their AWS
  credentials.

## POC lessons (mandatory in implementation)

These were learned during live POC validation:

1. **Enable AND start sshd.** `systemctl enable --now sshd` (both flags). AL2023 ships sshd
   but does not start it.
2. **`restorecon -R -v /home/sandbox/.ssh` is mandatory on AL2023.** SELinux is enforcing.
   Without restorecon, sshd silently rejects with "Permission denied" reading
   authorized_keys.
3. **Ownership matters.** `chown -R sandbox:sandbox /home/sandbox/.ssh` after the file write.
4. **`IdentitiesOnly yes` in operator's `~/.ssh/config`.** Without it, SSH offers all keys in
   sequence and may rate-limit or hit a wrong-key rejection before the right key is tried.
5. **`StrictHostKeyChecking no` + `UserKnownHostsFile /dev/null`** in operator's
   `~/.ssh/config` because each sandbox has a fresh host key.

## Edge cases

| Case | Behavior |
|------|----------|
| `km create` with `vscodeEnabled: false` | No keypair generated, no `~/.km/keys/sb-<id>*` files written, userdata block skipped |
| Operator runs `km create` on machine A, `km vscode start` on machine B | Private key only on A. `start` fails fast with portable-key hint |
| `~/.ssh/config` doesn't exist | Created with mode 0600, gets the managed block |
| `~/.ssh/config` has the markers but no entry for this sandbox | New entry inserted inside the markers |
| `~/.ssh/config` has the markers and an entry for this sandbox | Entry replaced wholesale |
| Local port 2222 already in use | SSM port-forward exits with bind error → operator uses `--local-port` |
| `km destroy` called for a sandbox with no key files | Idempotent removal; no error |
| `km destroy` called when the ssh-config entry doesn't exist | Idempotent removal; no error |
| `vscodeEnabled: false` profile + `km vscode start` | `start` detects via SSM check, prints "VS Code not enabled in this sandbox's profile (set `spec.cli.vscodeEnabled: true` and recreate)." |
| Sandbox created before Phase 73 lands, then `km vscode start` | SSM check finds no `authorized_keys` file → same clean error as `vscodeEnabled: false`. Fix is `km destroy && km create` |
| Operator runs `km vscode start` with no `~/.km/keys/` directory yet | `start` should create the parent dir lazily on first use during create; for `start` itself, missing key file is the trigger for the helpful error |

## Deployment requirements

- Userdata template change ⇒ `make build` (embeds template in km binary).
- Schema change (new profile field) ⇒ `km init --sidecars` (refreshes management Lambda).
- Existing sandboxes that want VS Code ⇒ `km destroy` + `km create`.
- AMI bakes pick up the new userdata via cloud-init on next boot (the unit lives in userdata,
  not in the baked image).
- Operators must install VS Code's **Remote - SSH** extension once on their laptop (one-time;
  documented in `docs/vscode.md`).

## Testing

| Layer | Test |
|-------|------|
| `pkg/sshkey/keygen_test.go` | Keypair generation: file paths created, mode bits correct, public key parses, private key parses, idempotency (regen overwrites) |
| `internal/app/cmd/sshconfig_test.go` | Parser/writer: no markers / markers only / markers+entry / multiple entries / weird whitespace / missing trailing newline / preserve content outside markers |
| `internal/app/cmd/vscode_test.go` | Flag parsing, error messages, SSM command construction (mock the SSM client like `agent_test.go` / `shell_test.go`), error path when private key missing locally |
| `pkg/profile/` | `vscodeEnabled` default-true and inheritance (matches `notifySlack*` patterns) |
| `pkg/compiler/userdata_test.go` | Userdata conditional block: emits when `VSCodeEnabled: true`, omits when `false`, embedded pubkey content is correct |
| `internal/app/cmd/create_test.go` | Create generates keypair, writes correct files, populates template variable |
| `internal/app/cmd/destroy_test.go` | Destroy removes ssh-config block, deletes key files, idempotent on missing |
| Manual smoke | Spin up sandbox with default profile, run `km vscode start`, connect from desktop VS Code Remote-SSH, edit a file, verify edits land via `km shell`. Then `km destroy` and verify cleanup |

No live-EC2 integration tests in CI — manual smoke is captured in the phase plan's
verification steps.

## Open questions (deferred to phase planning)

- Should we ship a `km doctor` check for vscode (e.g., "every sandbox with `vscodeEnabled:
  true` has sshd active and authorized_keys present"), or wait for evidence of operator
  confusion? Lean: defer.
- Cleanup behavior on `~/.ssh/config` when the last `Host` entry is removed — leave empty
  markers, or remove markers too? Lean: remove markers too for tidiness, but only when zero
  entries remain.
- Storage path naming: `~/.km/keys/sb-<id>` vs `~/.km/keys/sb-<id>.pem`? Lean: no extension
  (matches OpenSSH convention) plus `.pub` for public.
- Whether to `chmod 0700` the operator's `~/.km/keys/` directory at first key-write, or rely
  on the operator having a sane umask. Lean: explicitly `chmod 0700` for safety.
- For `--remote` create, the management Lambda renders userdata. Confirm that the Lambda's
  km binary version (refreshed via `km init --sidecars`) supports the new
  `VSCodeSSHPubKey` template variable; otherwise old Lambda + new client fail at create
  time with a confusing error. Lean: include a Lambda-side validation that errors clearly
  if the template variable is missing.

## File touch list

**New:**
- `pkg/sshkey/keygen.go`
- `pkg/sshkey/keygen_test.go`
- `internal/app/cmd/sshconfig.go`
- `internal/app/cmd/sshconfig_test.go`
- `internal/app/cmd/vscode.go`
- `internal/app/cmd/vscode_test.go`
- `docs/vscode.md`

**Modified:**
- `pkg/profile/types.go` — `VSCodeEnabled *bool` on `CLISpec`
- `pkg/profile/sandbox_profile.schema.json` — `vscodeEnabled` JSON schema entry
- `pkg/compiler/userdata.go` — conditional block + new template fields (`VSCodeEnabled`, `VSCodeSSHPubKey`)
- `pkg/compiler/userdata_test.go` — coverage for the new block
- `internal/app/cmd/create.go` — call keypair generator, populate template variable
- `internal/app/cmd/create_remote.go` (or equivalent) — pass pubkey to Lambda payload
- `internal/app/cmd/destroy.go` — cleanup ssh-config block + key files
- `internal/app/cmd/root.go` — register `vscode` subcommand
- `CLAUDE.md` — add `km vscode start/status` to CLI command list
- `.planning/ROADMAP.md` — Phase 73 entry already exists; goal text updated to reflect new design
