# Phase 76: km vscode rekey — Context

**Gathered:** 2026-05-09
**Status:** Ready for planning

<domain>
## Phase Boundary

Add `km vscode rekey <sandbox-id>` as a new subcommand under the existing
`vscode` parent group. It rotates the per-sandbox VS Code Remote-SSH ed25519
keypair on a running sandbox without `km destroy && km create`. Solves three
operator pain points surfaced by Phase 73 in production:

1. **Baked-AMI relaunch carries stale authorized_keys** — `km shell --learn --ami`
   bakes `/home/sandbox/.ssh/authorized_keys` into the image; on relaunch from
   that AMI, cloud-init may mark itself "done" and skip the userdata block.
2. **Cross-laptop portability** — Phase 73 keys live on the creation machine
   only. `rekey` lets a second laptop issue itself a fresh key without manual
   file copy.
3. **Post-incident rotation** — if a private key is suspected compromised, rotate
   without rebuilding the sandbox.

**Out of scope (deferred):**
- Userdata template change. The architectural fix (relocating the
  `authorized_keys` write into `km-bootstrap.service`) is a separate phase.
- DDB schema changes, new SSM Parameter Store entries, new Lambda, new sidecar.
- `km ami bake` modifications to scrub authorized_keys before bake.
- Multi-operator support (multiple `authorized_keys` lines).
- `km vscode rekey-all` bulk rotation, scheduling via `km at` (composable later).
- `km doctor` checks specific to rekey (e.g., "drift between local pubkey and
  remote authkeys"). Skip until evidence of operator confusion.
- `--dry-run` (deferred — confirmation prompt + fingerprint diff covers 90%).
- Lockfile to prevent concurrent rekey of the same sandbox (concurrent rekey
  is a footgun, not a flow we need to defend; last-writer-wins is acceptable).

</domain>

<decisions>
## Implementation Decisions

### CLI surface

```
km vscode rekey <sandbox-id> [--force] [--yes]
```

- `--force` — override the `km lock` safety lock (rekey otherwise refuses on
  locked sandboxes; lock = "hands off this sandbox" applies to key material).
- `--yes` — skip the confirmation prompt (for scripted use).
- No `--dry-run`. No `--remote`. No `--bootstrap`. No `--rotate`. The single
  argument and two flags are the entire surface.

Same identifier formats as other `km vscode` subcommands: full sandbox ID,
alias, or list-row number (uses existing `ResolveSandboxID`).

### Pre-flight checks (in order; any failure = hard error, no key changes)

1. **EC2 instance state** — `ec2.DescribeInstances` → require
   `InstanceStateNameRunning`. Pattern from `list.go:418`, `pause.go:139`,
   `resume.go:90`. Error message points at `km resume <id>` for stopped/paused.
2. **Lock check** — read DDB lock row; if locked AND `--force` not provided,
   refuse with `km vscode rekey: sandbox is locked. Use --force to override or
   km unlock <id> first.`
3. **Remote SSM pre-flight** — single SSM round-trip via the existing
   `vsCodeStatusScript` (`vscode.go:21`); pass result through the existing
   `parseVSCodeStatus` (`vscode.go:209`).
   - sshd inactive AND authkeys absent → "VS Code not enabled in this sandbox's
     profile" (same error as `vscode start`).
   - authkeys absent (sshd active) → "unexpected state" (same error as
     `vscode start`).
   - sshd inactive (authkeys present) → "sshd not running" (same error as
     `vscode start`).
4. **Local key state classification** — based on local `~/.km/keys/<id>` and
   the remote authkeys signal returned by step 3:
   | Local key | Remote authkeys | Verdict |
   |---|---|---|
   | present | present | Normal rotation — proceed |
   | absent | present | Cross-laptop bootstrap — generate fresh + push |
   | absent | absent | Pre-Phase-73 sandbox — hard error pointing at `km destroy && km create` (the parseVSCodeStatus error already covers this case via path 4.3 above) |
   | present | absent | Inconsistent state — refuse with `authorized_keys missing on sandbox; vscodeEnabled may have been false at create time, or the sandbox's home dir was wiped. Run km destroy && km create with vscodeEnabled:true.` |

### Confirmation flow

Before generating the new key, print:

```
Rotating VS Code key for sb-abc12345
  Old: SHA256:OldFingerprint... (~/.km/keys/sb-abc12345)
  New: (will be generated)
Continue? [y/N]
```

- Bypassed by `--yes`.
- Old fingerprint computed from the local pubkey file when present; line
  shown as `Old: (no local key — cross-laptop bootstrap)` when absent.
- New fingerprint shown only AFTER generation, in the step markers (operator
  has already consented at this point).

### Key generation

- Reuse `pkg/sshkey.GenerateAndWrite` (already used by `internal/app/cmd/create.go:529,1920`).
- Comment: `km-<sandbox-id>` (matches Phase 73 pattern).
- Two new files written: `~/.km/keys/<id>.new` (private, mode 0600) and
  `~/.km/keys/<id>.pub.new` (public, mode 0644). Both committed via atomic
  rename only after remote push verifies successfully.
- Pre-existing `.new` / `.pub.new` files (from a crashed prior rekey) are
  unconditionally overwritten — they are mid-flight scratch, not state.

### SSM push (single round-trip, install + readback verify)

The single `aws ssm send-command` payload sent via the existing
`sendSSMAndWait` helper (`agent.go:1067`):

```bash
set -e
mkdir -p /home/sandbox/.ssh
chmod 700 /home/sandbox/.ssh
chown sandbox:sandbox /home/sandbox/.ssh
cat > /home/sandbox/.ssh/authorized_keys << 'KEY'
{NEW_PUBKEY_LINE}
KEY
chmod 600 /home/sandbox/.ssh/authorized_keys
chown sandbox:sandbox /home/sandbox/.ssh/authorized_keys
restorecon -R -v /home/sandbox/.ssh
echo "=== READBACK ==="
head -1 /home/sandbox/.ssh/authorized_keys
echo "=== SHA256 ==="
sha256sum /home/sandbox/.ssh/authorized_keys | awk '{print $1}'
```

**Why single round-trip:** avoids race where another process touches authkeys
between install and verify. AL2023 SELinux requires `restorecon` (Phase 73
POC lesson #3).

### Verification matcher

After SSM returns, parse the `=== READBACK ===` block:
- **Exact byte-for-byte match** of the readback line vs. the locally generated
  pubkey line. No trimming, no normalization beyond stripping a single trailing
  `\n` from the readback.
- Verification fail → hard error:
  `Remote key install verification failed. Old key still active locally.
  Inspect via: km shell <id> -- cat ~/.ssh/authorized_keys
  Re-run: km vscode rekey <id>`
- No auto-retry. Operator decides whether to retry.

### Local commit ordering (post-verification)

```
1. os.Rename(~/.km/keys/<id>.pub.new, ~/.km/keys/<id>.pub)
2. os.Rename(~/.km/keys/<id>.new, ~/.km/keys/<id>)
```

`.pub` first because the private key is what `IdentityFile` in `~/.ssh/config`
actually points at — if step 2 fails, ssh keeps using the old private key
and existing access is preserved. `.pub` mismatch will be detected on next
rekey via the fingerprint-diff display (operator can confirm whether to
re-rotate). Both renames are atomic on POSIX.

### `~/.ssh/config` (no change)

The Phase 73 managed block already has `IdentityFile ~/.km/keys/<id>` —
the path is stable across rekeys. No `UpsertHost` call from rekey;
ssh-config is untouched.

### Output (✓ step markers — match `vscode start` shape)

Success path:
```
✓ EC2 instance running (i-0xxxxxxxxxxxxxxxx in us-east-1)
✓ Pre-flight check passed (sshd active, authorized_keys present)
✓ New keypair generated (SHA256:NewFingerprint...)
✓ Pushed to sandbox via SSM (verified — readback matches)
✓ Local key replaced atomically (~/.km/keys/sb-abc12345)

Rekey complete. Active VS Code sessions stay on the old key until reconnect.
```

Each `✓` line gates the next step. If pre-flight fails, neither generation
nor push happens.

### Active VS Code session handling

- No detection logic. sshd doesn't re-read authorized_keys for already-
  authenticated sessions, so existing connections survive. New connections
  (operator quits VS Code window and reconnects, or starts a new
  Remote-SSH host) pick up the new key transparently because `IdentityFile`
  path is unchanged.
- Documented in command help text and `docs/vscode.md`:
  *"Active VS Code Remote-SSH sessions stay connected with the old key
  until you reconnect."*

### Claude's Discretion

- Exact wording of all error messages (within the contract above: must
  point operator at the right next-step command).
- Choice of SHA256 fingerprint format for display (raw `sha256sum` output
  vs. ssh-keygen `SHA256:base64` style — pick whichever matches what the
  operator sees in `ssh-keygen -lf`, almost certainly the latter).
- Whether to surface the AWS region in the EC2 step marker.
- Test-injection structure for SSMSendAPI mocks (mirror existing vscode_test.go
  patterns).

</decisions>

<specifics>
## Specific Ideas

### File touch list

**New code (no new packages):**
- `internal/app/cmd/vscode.go` — add `newVSCodeRekeyCmd` constructor, register
  it in `newVSCodeCmdInternal` (alongside `start`, `status`). Add `runVSCodeRekey`
  function and a small helper for fingerprint computation.
- `internal/app/cmd/vscode_test.go` — add coverage for: pre-flight failures
  (stopped instance, locked sandbox without --force, vscodeEnabled:false,
  sshd-down + authkeys-present, all four local/remote key-state combinations),
  SSM verification mismatch, atomic rename ordering, --yes flag.

**Modified:**
- `CLAUDE.md` — add `km vscode rekey <sandbox-id>` to the VS Code Remote-SSH
  section's command list, with the `--force` and `--yes` flag descriptions.
- `docs/vscode.md` — add a new section "Rotating a sandbox key" covering the
  three pain-point scenarios, command usage, the lock-override flag, and the
  active-session note.

**No changes to:**
- `pkg/sshkey/` — `GenerateAndWrite` is reused as-is.
- `pkg/profile/` — no schema changes.
- `pkg/compiler/userdata.go` — no userdata template changes.
- `internal/app/cmd/sshconfig.go` — `~/.ssh/config` block is unchanged.
- `internal/app/cmd/create.go` / `destroy.go` — keypair lifecycle unchanged
  on the create/destroy paths.
- `infra/modules/` — no infra changes.
- Any Lambda — rekey is operator-laptop-side only, no remote execution.

### Existing patterns being mirrored

| Pattern | Source | Usage in rekey |
|---|---|---|
| ed25519 keypair generation | `pkg/sshkey/keygen.go:24` | New keypair generation |
| Local atomic rename | Unix `os.Rename` | `.new` files + commit ordering |
| SSM SendCommand round-trip | `agent.go:1067` (`sendSSMAndWait`) | Combined install + verify script |
| `vsCodeStatusScript` + `parseVSCodeStatus` | `vscode.go:21,209` | Pre-flight remote check |
| Sandbox identifier resolution | `ResolveSandboxID` | Accept ID/alias/list-number |
| `km lock` consultation + `--force` | `unlock.go` confirmation pattern | Safety-lock override |
| `--yes` to skip prompts | `destroy.go`, `unlock.go` | Confirmation bypass |
| `✓ step markers` | `vscode.go:171-174` (start), `slack.go:602-630` (rotate-token) | Output format |
| Sandbox state via `DescribeInstances` | `list.go:418`, `pause.go:139`, `resume.go:90` | EC2-running pre-flight |

### Edge cases (explicit handling required)

| Case | Behavior |
|---|---|
| Sandbox doesn't exist in DDB | `ResolveSandboxID` errors first; no rekey work happens |
| Sandbox in DDB but EC2 instance terminated | DescribeInstances pre-flight catches it ("not running"); error points at `km list` to confirm state |
| EC2 in `pending` state (just resumed) | Treated as not-running; error suggests retrying after `km list` shows running |
| `~/.km/keys/<id>.new` exists from a crashed prior rekey | Overwritten unconditionally |
| `~/.km/keys/<id>.pub.new` exists but `<id>.new` doesn't (mid-rename crash) | Both regenerated; old `.pub.new` overwritten |
| SSM SendCommand times out | Local key untouched; error includes the SSM command ID for retry/inspection |
| SSM exit 0 but readback line is empty | Verification mismatch path; hard error |
| Local pubkey file corrupt or missing when computing old fingerprint | Display `Old: (unable to read local pubkey)` and continue to confirmation |
| Operator answers "n" at confirmation | Exit cleanly, no changes, exit code 0 (consistent with `unlock` no-op cancel) |
| `~/.km/keys/` directory has wrong perms (not 0700) | `GenerateAndWrite` already fixes this via `os.MkdirAll(..., 0o700)` |

### Sample operator interactions

**Normal rotation (incident response):**
```
$ km vscode rekey sb-abc12345
✓ EC2 instance running (i-0... in us-east-1)
✓ Pre-flight check passed (sshd active, authorized_keys present)

Rotating VS Code key for sb-abc12345
  Old: SHA256:7w2fQ... (~/.km/keys/sb-abc12345)
  New: (will be generated)
Continue? [y/N] y

✓ New keypair generated (SHA256:K9m4Z...)
✓ Pushed to sandbox via SSM (verified — readback matches)
✓ Local key replaced atomically (~/.km/keys/sb-abc12345)

Rekey complete. Active VS Code sessions stay on the old key until reconnect.
```

**Cross-laptop bootstrap (no local key on this machine):**
```
$ km vscode rekey sb-abc12345 --yes
✓ EC2 instance running (i-0... in us-east-1)
✓ Pre-flight check passed (sshd active, authorized_keys present)
✓ New keypair generated (SHA256:K9m4Z...)
✓ Pushed to sandbox via SSM (verified — readback matches)
✓ Local key created atomically (~/.km/keys/sb-abc12345)

Rekey complete. Reconnect VS Code to pick up the new key.
```

**Pre-Phase-73 sandbox:**
```
$ km vscode rekey sb-old00001
✓ EC2 instance running (i-0... in us-east-1)
✗ VS Code not enabled in this sandbox's profile (set spec.cli.vscodeEnabled: true and recreate the sandbox)

Hint: this sandbox predates Phase 73 or was created with vscodeEnabled:false.
Rekey can't enable VS Code retroactively. Run: km destroy sb-old00001 --remote --yes && km create <profile.yaml>
```

**Locked sandbox:**
```
$ km vscode rekey sb-abc12345
✓ EC2 instance running (i-0... in us-east-1)
✗ Sandbox is locked. Use --force to override or run: km unlock sb-abc12345
```

### Deployment requirements

- `make build` — picks up the new subcommand registration. No userdata
  template change so AMI bakes are unaffected.
- **No `km init --sidecars` required** — there's no schema change, so the
  management Lambda's km binary doesn't need refresh. (Confirmed against
  `memory/project_schema_change_requires_km_init.md`.)
- **No `km init --lambdas` required** — no Lambda code change.
- Existing sandboxes can be rekeyed immediately after the new binary lands;
  this is the entire point of the phase.

</specifics>

<code_context>
## Existing Code Insights

### Reusable Assets

- `pkg/sshkey.GenerateAndWrite(privPath, pubPath, comment string)` —
  ed25519 keypair generator already used by `km create`. Returns the OpenSSH
  pubkey line string for direct embedding into the SSM script.
- `vsCodeStatusScript` (`internal/app/cmd/vscode.go:21`) — the combined
  sshd/authkeys/first-key SSM probe. Reused verbatim for pre-flight.
- `parseVSCodeStatus` (`internal/app/cmd/vscode.go:209`) — interprets the
  probe output and emits the canonical Phase 73 errors. Reused verbatim.
- `sendSSMAndWait` (`internal/app/cmd/agent.go:1067`) — the SSM SendCommand
  helper used everywhere; handles polling and timeout.
- `ResolveSandboxID` — the standard ID/alias/list-number resolver.
- `ec2.DescribeInstances` pattern — established in `list.go:379-428`,
  `pause.go:139-200`, `resume.go:90-150`. Same shape for the running-state
  check.
- `cleanupVSCodeState` (`destroy.go:755`) — symmetric counterpart to what
  rekey writes; not called by rekey, but worth referencing in tests as
  "the inverse operation" so the file paths stay consistent.

### Established Patterns

- **Per-sandbox ed25519 keys at `~/.km/keys/<id>` (priv 0600) + `<id>.pub`
  (0644)** — Phase 73's lifecycle convention. Rekey writes to the same paths.
- **`~/.ssh/config` managed block via `UpsertHost`/`RemoveHost`** — Phase 73's
  ssh-config invariant. Rekey doesn't touch it because `IdentityFile`
  is path-stable.
- **Atomic rename for credential rotation** — Slack's
  `RunSlackRotateToken` (`slack.go:585`) follows validate→persist→cold-start→
  smoke-test, which is the same shape as rekey's pre-flight→generate→push→
  verify→commit pattern.
- **AL2023 SELinux requires `restorecon -R -v /home/sandbox/.ssh`** —
  Phase 73 POC lesson #3, baked into the userdata template at
  `pkg/compiler/userdata.go:1814-1826`. Rekey's SSM script must do the same
  after writing authorized_keys.
- **`km lock` is an atomic DynamoDB conditional write** — operations that
  must respect lock check the DDB row before mutating; rekey adds key
  material as one of those operations.

### Integration Points

- `newVSCodeCmdInternal` (`vscode.go:34`) — register the new `rekey`
  subcommand alongside `start` and `status`. Mirror the constructor signature
  (`SandboxFetcher`, `ShellExecFunc`, `SSMSendAPI`) for test-injection.
- `resolveVSCodeDeps` (`vscode.go:90`) — already wires up the AWS clients
  needed by rekey (state bucket, fetcher, SSM client). Reuse.
- `cobra` command tree — `km vscode` already exists; rekey just adds a leaf.
- DDB lock-row reader — find the existing helper used by `unlock.go` /
  `destroy.go` and reuse rather than re-implement.

</code_context>

<deferred>
## Deferred Ideas

- **Architectural fix: relocate `authorized_keys` write into
  `km-bootstrap.service`** — root cause of pain point #1. Solves
  AMI-baked-stale-keys at the source by re-running every boot. Tracked as
  the next vscode-related phase after 76 ships.
- **`km vscode rekey-all`** — bulk rotation across all running sandboxes.
  Composable today via `km list | awk '...' | xargs -n1 km vscode rekey --yes`.
- **`km doctor` checks for vscode key drift** — flag sandboxes where the
  local pubkey hash doesn't match the remote authorized_keys hash. Skip
  until evidence of operator confusion.
- **Multiple operators sharing one sandbox** via multi-line authorized_keys —
  v1 = one operator per sandbox (Phase 73 deferred this; rekey inherits the
  decision).
- **`km vscode export-key` / `import-key`** — formal cross-machine key
  transport. Rekey makes this less urgent because the cross-laptop bootstrap
  flow now exists.
- **`--dry-run`** — confirmation prompt + fingerprint diff covers the same
  affordance.
- **Lockfile against concurrent rekey of the same sandbox** — last-writer-wins
  is acceptable; concurrent rekey is a footgun, not a workflow.
- **Scheduled periodic rotation via `km at`** — composable today
  (`km at 'monday 9am' vscode rekey sb-xxx --yes`). No special integration
  needed beyond confirming `km at` accepts the subcommand string.
- **`km ami bake` scrubs authorized_keys before snapshot** — would prevent
  pain point #1 from existing for new AMIs. Out of scope for rekey; capture
  as a candidate for the bootstrap-service phase.
- **SHA256 fingerprint in `km vscode status` output** — small UX win, not
  required by rekey itself. Tack on if convenient during implementation.

</deferred>

---

*Phase: 76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox*
*Context gathered: 2026-05-09*
