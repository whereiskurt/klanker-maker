# Phase 73: km vscode remote session via SSM — Context

**Gathered:** 2026-05-06 (rewritten 2026-05-06 after POC validation)
**Status:** Ready for planning
**Source:** Brainstorming dialogue + live POC. Original design was VS Code Web (`code serve-web`); rejected after POC because it forces a browser experience instead of letting the operator use their local desktop VS Code with all their themes/keybindings/extensions. Redesigned around VS Code Remote-SSH over SSM port-forward with **per-sandbox auto-generated keypairs**.

<domain>
## Phase Boundary

Add `km vscode start <sandbox-id>` so an operator can connect their local desktop VS Code to a
sandbox via the **Remote-SSH** extension. The command opens an SSM port-forward (sandbox port
22 → operator local port 2222), upserts a managed entry in `~/.ssh/config`, and tells the
operator how to connect from VS Code.

Sandboxes get an ed25519 keypair auto-generated **on the operator's laptop at `km create` time**.
The private key stays in `~/.km/keys/sb-<id>`; the public key is shipped into userdata and
written to `/home/sandbox/.ssh/authorized_keys` at sandbox boot. `km destroy` cleans up the
key files and the ssh-config block.

Use case is companion-style: operator runs `km agent run` (Claude rips on a task), then runs
`km vscode start` to review/edit the result with their full local IDE experience for "the next
couple of hours."

**Out of scope:**
- Browser-based VS Code (`code serve-web`) — explicitly rejected after POC.
- Microsoft Remote Tunnels (`code tunnel`) — bypasses the SSM security boundary, requires
  GitHub auth, no fit for klankermaker's trust model.
- Auto-stop on idle, `--duration` time-boxing (operator can compose with `km at` if they want
  scheduled stops; explicit deferral).
- DDB schema changes, new SSM Parameter Store entries, new Lambda, new sidecar binary.
- Cross-machine key portability (`km vscode export-key`/`import-key`) — deferred until it
  actually bites.
- Slack/email notification integration.
- Multiple concurrent operators sharing one sandbox via additional `authorized_keys` entries.
- Proper SSH host-key trust model (TOFU/cert-based) — v1 uses `StrictHostKeyChecking no` +
  `UserKnownHostsFile /dev/null`, deferred follow-up flagged in spec.
- Installation of a VS Code server binary on the sandbox — VS Code Remote-SSH auto-deploys
  `vscode-server` on first connect; nothing pre-installed by km.

</domain>

<decisions>
## Implementation Decisions

### Architecture (locked after POC)

**VS Code Remote-SSH over SSM port-forward**, with per-sandbox auto-generated ed25519 keypairs.
Operator's local desktop VS Code is the UI; `vscode-server` is auto-deployed by VS Code to the
sandbox on first connect (no pre-install needed).

The SSM tunnel is the security boundary; SSH layered on top adds key-based authentication,
encrypted file transfer, terminal access, and lets the existing VS Code Remote-SSH extension
"just work" without a custom protocol.

### Keypair lifecycle (per-sandbox, auto-generated)

| Stage | Action |
|-------|--------|
| `km create` | Locally generate ed25519 keypair using Go's `crypto/ed25519` + `golang.org/x/crypto/ssh` (no shelling out). Write `~/.km/keys/sb-<id>` (mode 0600) and `~/.km/keys/sb-<id>.pub` (mode 0644). Read pubkey content; pass to userdata as template variable `VSCodeSSHPubKey`. |
| Sandbox boot (cloud-init userdata) | Gated on `spec.cli.vscodeEnabled: true`: `systemctl enable --now sshd`; create `/home/sandbox/.ssh/` (0700, sandbox:sandbox); write pubkey to `authorized_keys` (0600, sandbox:sandbox); `restorecon -R -v /home/sandbox/.ssh` for AL2023 SELinux contexts. |
| `km vscode start <sb>` | Upsert managed block in `~/.ssh/config`; open foreground SSM port-forward via existing `buildPortForwardCmd`; print connection instructions; block until Ctrl-C. |
| `km destroy <sb>` | Remove the `Host km-sb-<id>` block from `~/.ssh/config`; delete `~/.km/keys/sb-<id>` + `.pub`. Mirrors how `km destroy` already cleans up DDB rows. |

Mirrors the existing `/sandbox/{id}/signing-key` SSM pattern for per-sandbox email signing keys
— operators already understand this idiom.

### CLI surface

```
km vscode start  <sandbox-id> [--local-port N]
km vscode status <sandbox-id>
```

**`start`:**
1. Resolve sandbox → instance ID + region (existing helper, see `shell.go`).
2. Verify sandbox has `vscodeEnabled: true` (via SSM `systemctl is-active sshd` + check that
   `/home/sandbox/.ssh/authorized_keys` exists). If not, fail fast with "VS Code not enabled
   in this sandbox's profile (set `spec.cli.vscodeEnabled: true`)."
3. Upsert managed block in `~/.ssh/config` (read existing, parse markers, replace or append
   the Host entry for this sandbox, preserve everything else).
4. Print connection instructions:
   ```
   ✓ Updated ~/.ssh/config (Host: km-sb-abc123)
   ✓ Forwarding localhost:2222 → sandbox:22

   In VS Code: F1 → "Remote-SSH: Connect to Host..." → km-sb-abc123
   Press Ctrl-C to close the tunnel (sshd keeps running on the sandbox).
   ```
5. Open the foreground SSM port-forward via `buildPortForwardCmd` (`shell.go:577`).

**Flags:**
- `--local-port N` overrides the local-side port if 2222 is taken on the operator's laptop.
  The `~/.ssh/config` entry's `Port` line is updated to match.

**`status`:** SSM query: `systemctl is-active sshd` + check `/home/sandbox/.ssh/authorized_keys`
exists + presence of the operator's pubkey. Returns non-zero if any check fails.

**No `stop` command in v1.** The session is the foreground port-forward; Ctrl-C ends it.
sshd stays running on the sandbox; reconnect is just rerunning `start`.

### Profile gate

- Field: `spec.cli.vscodeEnabled` (`bool*` pointer, omit ⇒ default `true`). Same name and
  semantics as the original Option A, different effect: now gates "sshd up + key dropped"
  instead of "systemd unit installed."
- Inheritance follows the existing `spec.cli.*` pattern.
- `false` ⇒ userdata skips the entire sshd-enable + key-drop block. `km vscode start` against
  such a sandbox fails fast with the clean error above.
- Schema change ⇒ `km init --sidecars` after rebuild (matches documented pattern in
  `memory/project_schema_change_requires_km_init.md`).

### `~/.ssh/config` management (operator-side)

km manages a region of `~/.ssh/config` between marker comments:

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

- The block is created if absent. `start` updates only the Host entry for the current sandbox;
  other entries inside the block are preserved.
- `km destroy <sb>` removes only that sandbox's Host entry.
- Anything outside the markers is never touched.
- File created with 0600 perms, in the operator's home directory.

### Auth & security model

- **SSM tunnel = real security boundary.** IAM-authenticated, encrypted, per-session.
- **SSH on top adds key-based authentication.** Per-sandbox ed25519; private key never leaves
  the operator's laptop; pubkey only ever exists on the operator's machine + the one sandbox
  it was generated for.
- **Compromise blast radius = 1 sandbox.** No shared key across sandboxes.
- **`StrictHostKeyChecking no` + `UserKnownHostsFile /dev/null` for v1.** Acceptable because
  the SSM tunnel already authenticates the target instance via IAM; the SSH host key is
  defense-in-depth. Proper TOFU/cert-based trust is a deferred follow-up.
- **No public exposure of port 22.** SG remains egress-only; SSM port-forward bypasses SG and
  reaches the SSM agent on the instance, which connects to localhost:22.

### POC lessons that the implementation must honor

These were learned during live POC validation against an `learn.yaml` sandbox; userdata MUST
incorporate all four:

1. **Enable AND start sshd.** AL2023 ships `openssh-server` but sshd is often disabled by
   default. `systemctl enable --now sshd` (both flags).
2. **Create `~/.ssh/` as sandbox user, not root.** If created by root then `chown`'d, file
   ownership is right, but if `chown` is omitted or scoped wrong, sshd refuses with "bad
   ownership."
3. **`restorecon -R -v /home/sandbox/.ssh` is mandatory on AL2023.** SELinux is enforcing.
   Without restorecon, sshd cannot read `authorized_keys` even when ownership and mode are
   correct, and fails silently with "Permission denied" in `journalctl -u sshd`.
4. **Pubkey one line, no trailing whitespace artifacts.** Heredoc form (`cat > ... << 'EOF'`)
   works correctly when the pubkey content has no embedded newlines (which `ssh-keygen` output
   guarantees).

</decisions>

<specifics>
## Specific Ideas

### File touch list

**New:**
- `internal/app/cmd/vscode.go` — cobra subcommand (`start`, `status`)
- `internal/app/cmd/vscode_test.go` — Wave 0 stubs + green tests
- `internal/app/cmd/sshconfig.go` — `~/.ssh/config` managed-block parser/writer (read existing, find markers, replace/append a Host entry, preserve everything else)
- `internal/app/cmd/sshconfig_test.go` — exhaustive coverage for the parser/writer (no markers, markers only, markers + entries, multiple sandboxes, weird whitespace, missing trailing newline)
- `pkg/sshkey/keygen.go` — Go-native ed25519 keypair generation, write OpenSSH-format private + public files. Wraps `crypto/ed25519` + `golang.org/x/crypto/ssh/marshal`. Single function `GenerateAndWrite(privPath, pubPath, comment string) (pubContent string, err error)`.
- `pkg/sshkey/keygen_test.go` — coverage for path creation, mode bits, public-key derivation, idempotency.
- `docs/vscode.md` — operator-facing setup guide: install Remote-SSH extension, usage of `km vscode start`, **network requirements section with the suffix table from "vscode-server network requirements" below** (operators must add these to their own `spec.network.egress.allowedDNSSuffixes` for hardened profiles)

**Modified:**
- `pkg/profile/types.go` (or wherever `CLISpec` lives) — add `VSCodeEnabled *bool` with default-true semantics
- `pkg/profile/sandbox_profile.schema.json` — add `vscodeEnabled` to the JSON schema
- `pkg/compiler/userdata.go` — conditional block writing sshd enable + authorized_keys + restorecon; add `VSCodeSSHPubKey string` field to userdata template input struct
- `pkg/compiler/userdata_test.go` — coverage for the new conditional block
- `internal/app/cmd/create.go` (or wherever the create command lives) — call `pkg/sshkey.GenerateAndWrite` before rendering userdata; populate `VSCodeSSHPubKey` template variable
- `internal/app/cmd/create_remote.go` (or equivalent for `--remote`) — pass `VSCodeSSHPubKey` to the management Lambda payload
- `internal/app/cmd/destroy.go` — add cleanup steps: remove `Host km-sb-<id>` from `~/.ssh/config`, delete `~/.km/keys/sb-<id>*`
- `internal/app/cmd/root.go` — register the new `vscode` subcommand (mirror `agent`/`slack`/`ami` pattern)
- `CLAUDE.md` — add `km vscode start/status` to the CLI command list
- The Lambda-side userdata renderer also needs the new `VSCodeSSHPubKey` variable threaded through (verify which Lambda handles `--remote` create — likely `km-create-handler`).

### Existing patterns to mirror

- **systemd enable + start in userdata.go:** existing patterns for `km-mail-poller`,
  `km-slack-inbound-poller` show how to write conditional sidecar blocks. Use the same shape
  for the sshd-enable + authorized_keys block.
- **SSM port-forward (verbatim reuse):** `buildPortForwardCmd` at `shell.go:577`. No new
  function needed for the port-forward primitive.
- **SSM SendCommand wrapper:** `sendSSMAndWait` at `agent.go:1067` for the `status` command's
  remote checks.
- **Profile flag inheritance:** follow `notifySlackEnabled`, `notifySlackPerSandbox`,
  `notifySlackTranscriptEnabled` for the bool-pointer + default-true semantics.
- **Daemon-reload + restart for AMI bakes (commit `4030fce`):** the new userdata block writes
  no new systemd unit (sshd ships pre-installed), so daemon-reload is not strictly required.
  But the `systemctl enable --now sshd` step still needs to be in the same defensive idiom —
  prefix the block with `systemctl daemon-reload` to be safe against AMI-baked stale state.
- **Per-sandbox key files in `~/.km/`:** the `~/.km/keys/` directory mirrors how the rest of
  km manages local operator state (km-config.yaml, etc. live under `~/.km/`).

### Edge cases (explicit handling required)

| Case | Behavior |
|------|----------|
| `~/.km/keys/` doesn't exist on first `km create` | Create with mode 0700; document in docs/vscode.md |
| Operator runs `km create` on machine A, `km vscode start` on machine B | Private key only exists on A. `start` fails with "private key not found at `~/.km/keys/sb-<id>`. If you created this sandbox on a different machine, copy the key files over." Document in docs/vscode.md |
| `~/.ssh/config` doesn't exist | Create it with mode 0600, add the managed block |
| `~/.ssh/config` exists but has no markers | Append managed block (with markers) plus first entry |
| `~/.ssh/config` has markers but no Host entry for this sandbox | Add inside the markers |
| `~/.ssh/config` has markers AND a Host entry for this sandbox | Replace just that entry, leave others intact |
| Operator manually edits inside the markers | We document "do not edit between markers" but don't enforce it; replacement is whole-Host-entry, so manual additions to other Host entries inside the block survive |
| Local port 2222 already in use | SSM port-forward exits with bind error → operator uses `--local-port N` |
| `km destroy` called but key files already gone | Idempotent removal; no error |
| `km destroy` called but ssh-config block doesn't exist for this sandbox | Idempotent removal; no error |
| `vscodeEnabled: false` profile | Userdata skips sshd-enable + key-drop. `start` detects this via failed SSM check and surfaces the clean error |

### Sample operator setup (target for `docs/vscode.md`)

```
# One-time: install VS Code's Remote-SSH extension.
# Open VS Code → Extensions → search "Remote - SSH" → Install (Microsoft, free).

# Per-sandbox lifecycle:
km create profiles/<your-profile>.yaml --alias my-poc
SB=$(km list | awk '/my-poc/ {print $1}')
km vscode start $SB
# (terminal blocks; in another window:)
# F1 → "Remote-SSH: Connect to Host..." → km-$SB
# VS Code installs vscode-server on first connect (~50MB), then opens an empty workspace.
# File → Open Folder → /workspace.

# When done:
# Ctrl-C the terminal running km vscode start.
# (sshd stays up; reconnect any time with km vscode start $SB)

km destroy $SB --remote --yes
# (removes the ssh-config block and the ~/.km/keys/sb-<id>* files automatically)
```

### vscode-server network requirements

Remote-SSH downloads `vscode-server` to the sandbox on first connect (~50MB),
and the running server fetches extensions + emits telemetry from the sandbox,
not the desktop. A sandbox with `vscodeEnabled: true` AND a strict
`spec.network.egress` allowlist that omits Microsoft endpoints fails
confusingly: SSH lands, but the bootstrap install hangs and VS Code reports
"Could not establish connection" with no useful diagnostic operator-side.

**Decision (favoring egress allowlist over local-server tunnel).** We extend
`spec.network.egress.allowedDNSSuffixes` in profiles that opt into vscode
rather than setting `remote.SSH.useLocalServer: true` to tunnel the server
download over SSH. The local-server path works but is reportedly flaky, and
it doesn't improve the trust model - the SSM tunnel already authenticates the
target instance via IAM. Documenting the egress surface is more honest than
hiding it behind an SSH tunnel.

**Suffix table for `docs/vscode.md`** (operators copy into their own profile;
all values go under `spec.network.egress.allowedDNSSuffixes` as leading-dot
suffixes per existing schema convention):

| Suffix | What for | Required? |
|---|---|---|
| `.visualstudio.com` | server download (`update.code.visualstudio.com`), marketplace API (`marketplace.visualstudio.com`) | yes |
| `vscode.download.prss.microsoft.com` | newer download CDN (Microsoft is migrating here) | yes |
| `.vsassets.io` | extension content + CDN (`*.gallery.vsassets.io`, `*.gallerycdn.vsassets.io`) | only when extensions installed |
| `.vscode-cdn.net`, `vscode.dev` | Settings Sync, vscode.dev | only if used |
| `.githubcopilot.com` | Copilot | only if used |
| `dc.services.visualstudio.com`, `vortex.data.microsoft.com`, `mobile.events.data.microsoft.com` | telemetry | **no** - disable in local VS Code instead (see below) |

**Operator-side telemetry hygiene.** `docs/vscode.md` should recommend
disabling telemetry in the operator's local VS Code settings before
connecting, so the sandbox-side allowlist stays narrow:

```json
"telemetry.telemetryLevel": "off",
"redhat.telemetry.enabled": false
```

Once disabled, vscode-server stops emitting to the data-platform endpoints,
so they never need to be allowlisted.

**Discovery for new extension stacks.** When operators adopt unfamiliar
bundles (Copilot, language servers, devcontainer tooling), the egress
surface grows. Supported recipe:

```bash
km create profiles/learn.yaml --alias vscode-learn
km shell vscode-learn --learn --ports 2222:22
# In another terminal: VS Code Remote-SSH → localhost:2222
# Install extensions, exercise the workflow, then exit the shell.
ls learned.vscode-learn-*.yaml   # generated profile with actual suffixes hit
```

**Phase 73 scope clarification.** Phase 73 ships `km vscode start/status` and
userdata changes only. It does **not** modify default built-in profiles to
include vscode egress. Operators add the suffixes to their own profile when
they want vscode on a hardened profile. The suffix table above lives in
`docs/vscode.md` (already on the file touch list, line 180).

### Deployment requirements

- Userdata template change ⇒ `make build` (embeds template in km binary).
- Schema change ⇒ `km init --sidecars` (refreshes management Lambda).
- Existing sandboxes that want VS Code ⇒ `km destroy` + `km create`.
- AMI bakes pick up the unit + wrapper automatically on next boot via cloud-init.

</specifics>

<deferred>
## Deferred Ideas

### Explicitly deferred from this phase

- **`--duration <D>` flag and auto-stop integration.** Operator can compose with `km at` for
  scheduled stops; no special wiring in `km vscode`.
- **`km doctor` checks for VS Code** (e.g., "every sandbox with `vscodeEnabled: true` has
  sshd active and authorized_keys present"). Skip until there's evidence of operator
  confusion.
- **Cross-machine key portability** (`km vscode export-key`/`import-key`). Document that keys
  are per-machine for now; revisit if this bites.
- **Multiple operators sharing a sandbox** via multi-line `authorized_keys`. v1 = one operator
  per sandbox.
- **Proper SSH host-key trust** (TOFU, ssh-cert with a per-install CA, etc.). v1 uses
  `StrictHostKeyChecking no` + `UserKnownHostsFile /dev/null`. Tracked as a security follow-up
  in the spec.
- **`km vscode stop` command.** Foreground tunnel + Ctrl-C is enough; if operators ask for
  detached + stop later, we add it.
- **Provisioning sshd config customization** (changing the port, restricting cipher suites,
  etc.). v1 ships with the AL2023 default sshd config. Operators who want to harden can
  add `initCommands` or override sshd_config in their profile's `configFiles`.
- **Browser fallback** (`code serve-web`) for use cases where the operator can't run VS Code
  locally. Only pursued if there's evidence of demand.
- **`vscodeEnabled` rename to `sshEnabled`.** The flag now gates SSH access broadly (which
  enables VS Code Remote-SSH but also vim-over-ssh, scp, rsync, etc.). For v1 we keep
  `vscodeEnabled` because that's the operator-facing name everyone agreed on; rename
  candidate for a future cleanup if it's confusing.

</deferred>

---

*Phase: 73-km-vscode-remote-session-via-ssm*
*Context gathered: 2026-05-06 via brainstorming dialogue + live POC validation*
