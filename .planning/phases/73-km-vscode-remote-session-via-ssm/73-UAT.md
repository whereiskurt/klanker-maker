---
phase: 73
slug: km-vscode-remote-session-via-ssm
status: pending
scenarios: 6
---

# Phase 73 — Operator UAT Checklist

> Run all six scenarios against a real AWS account + local VS Code installation.
> Each scenario is self-contained and repeatable. Tick the checkbox when it passes.
>
> **Required environment:**
> - Configured AWS account with `km init` regional infrastructure already provisioned
> - VS Code with the **Remote - SSH** extension installed (Microsoft, free)
> - AWS SSM Session Manager plugin installed locally (existing requirement for `km shell`)
> - Binary refreshed: `make build && km init --sidecars` (one-time after Phase 73 ships)
>
> **Wall-clock estimate:** ~20-30 minutes (most time spent waiting for sandbox create + vscode-server first install)
>
> **If a scenario fails:** Document the failure under that scenario, stop, run `/gsd:verify-work --gaps` to plan gap closure. Do NOT mark the phase complete.

---

## Scenario 1: End-to-end Remote-SSH from local desktop VS Code

**Goal:** Verify that `km vscode start` enables a full VS Code Remote-SSH session with file edits persisting on the sandbox.

### Pre-conditions

- `make build && km init --sidecars` completed (binary + Lambda refreshed)
- VS Code with Remote - SSH extension installed

### Steps

1. Create a sandbox:
   ```bash
   km create profiles/learn.yaml --alias vscode-smoke
   SB=$(km list | awk '/vscode-smoke/ {print $1}')
   echo "Sandbox ID: $SB"
   ```

2. Start the VS Code tunnel (terminal blocks):
   ```bash
   km vscode start $SB
   ```
   Copy the printed Host alias (`km-$SB`).

3. In VS Code: press F1, type "Remote-SSH: Connect to Host...", select `km-$SB`.

4. Confirm vscode-server installs (~50MB on first connect, ~30-60s). Watch the "Setting up VS Code" progress bar complete.

5. In VS Code: File → Open Folder → `/workspace`

6. Create or edit a file:
   ```
   # In the VS Code terminal or editor, create a test file
   echo "UAT test $(date)" > /workspace/uat-test.txt
   ```

7. Press Ctrl-C in the `km vscode start` terminal to close the tunnel.

8. Confirm the edit persisted:
   ```bash
   km shell $SB -- cat /workspace/uat-test.txt
   ```

9. Destroy the sandbox:
   ```bash
   km destroy $SB --remote --yes
   ```

### Expected Outcome

- Step 2: `km vscode start` prints "✓ Updated ~/.ssh/config (Host: km-$SB)" and "✓ Forwarding localhost:2222 → sandbox:22" before blocking
- Step 3-4: VS Code connects and vscode-server installs without error
- Step 5: `/workspace` opens and shows sandbox file tree
- Step 6: File save succeeds
- Step 7: Ctrl-C exits cleanly with no crash
- Step 8: `km shell` output contains "UAT test" (edit persisted)
- Step 9: Destroy completes, prints cleanup confirmation

- [ ] Scenario 1 passed

---

## Scenario 2: Per-sandbox keypair lifecycle

**Goal:** Verify that keypairs are generated at `km create` and cleaned up at `km destroy`, with correct file modes.

### Pre-conditions

- No pre-existing keypair for the test sandbox in `~/.km/keys/`

### Steps

1. Verify no prior keys exist for a sandbox you are about to create:
   ```bash
   ls ~/.km/keys/ 2>/dev/null | grep "kp-test" || echo "(no prior keys — good)"
   ```

2. Create the sandbox:
   ```bash
   km create profiles/learn.yaml --alias kp-test
   SB=$(km list | awk '/kp-test/ {print $1}')
   echo "Sandbox ID: $SB"
   ```

3. Verify keypair was generated with correct modes:
   ```bash
   ls -la ~/.km/keys/$SB
   ls -la ~/.km/keys/$SB.pub
   ```
   Expected: `$SB` at mode `-rw-------` (0600), `$SB.pub` at mode `-rw-r--r--` (0644).

4. Verify the public key is in authorized_keys format:
   ```bash
   cat ~/.km/keys/$SB.pub
   ```
   Expected: single line starting with `ssh-ed25519`, ending with `km-$SB`.

5. Destroy the sandbox:
   ```bash
   km destroy $SB --remote --yes
   ```

6. Verify keys were cleaned up:
   ```bash
   ls ~/.km/keys/$SB* 2>&1
   ```
   Expected: "No such file or directory" (both files gone).

### Expected Outcome

- Step 2: `km create` prints "✓ VS Code keypair written to ~/.km/keys/$SB" on stderr
- Step 3: Private key is mode 0600; public key is mode 0644
- Step 4: Public key line starts with `ssh-ed25519` and includes `km-$SB` as the comment
- Step 6: Both `~/.km/keys/$SB` and `~/.km/keys/$SB.pub` are absent

- [ ] Scenario 2 passed

---

## Scenario 3: ssh-config managed-block lifecycle

**Goal:** Verify that `km vscode start` writes a Host block into `~/.ssh/config` within markers, and `km destroy` removes it cleanly without touching surrounding content.

### Pre-conditions

- A sandbox created with Phase 73 binary (keypair exists)

### Steps

1. Back up current ssh config:
   ```bash
   cp ~/.ssh/config ~/.ssh/config.uat-bak 2>/dev/null || echo "(no existing config)"
   ```

2. Create a sandbox and start the tunnel (Ctrl-C immediately after the host block prints):
   ```bash
   km create profiles/learn.yaml --alias ssh-lifecycle
   SB=$(km list | awk '/ssh-lifecycle/ {print $1}')
   km vscode start $SB
   # Press Ctrl-C as soon as you see "✓ Updated ~/.ssh/config"
   ```

3. Inspect `~/.ssh/config` — verify the managed block was written:
   ```bash
   cat ~/.ssh/config
   ```
   Expected: file contains `# BEGIN km vscode hosts (managed; do not edit between markers)`, the Host entry for `km-$SB`, and `# END km vscode hosts`.

4. Verify the Host entry has the correct fields:
   ```bash
   grep -A 10 "Host km-$SB" ~/.ssh/config
   ```
   Expected: HostName localhost, Port 2222, User sandbox, IdentityFile pointing to `~/.km/keys/$SB`, IdentitiesOnly yes.

5. Verify any pre-existing content outside the markers is intact:
   ```bash
   diff ~/.ssh/config.uat-bak <(grep -v "km vscode\|km-$SB\|BEGIN km\|END km\|HostName localhost\|IdentitiesOnly\|StrictHostKeyChecking\|UserKnownHostsFile\|ServerAliveInterval" ~/.ssh/config) 2>/dev/null || echo "(compare manually if diff unavailable)"
   ```

6. Destroy the sandbox:
   ```bash
   km destroy $SB --remote --yes
   ```

7. Inspect `~/.ssh/config` — verify the Host entry and markers were removed:
   ```bash
   cat ~/.ssh/config
   ```
   Expected: `km-$SB` Host block absent; markers absent (if $SB was the only entry); pre-existing content unchanged.

8. Restore backup if needed:
   ```bash
   # Only if something went wrong — do NOT run this to "pass" the scenario
   # mv ~/.ssh/config.uat-bak ~/.ssh/config
   ```

### Expected Outcome

- Step 3: Managed block present in `~/.ssh/config` with correct markers
- Step 4: Host entry has all six standard fields including IdentitiesOnly yes
- Step 5: Content outside markers matches the backup exactly
- Step 7: After destroy, `km-$SB` block is gone; markers removed if no other km entries; surrounding content byte-for-byte identical to backup

- [ ] Scenario 3 passed

---

## Scenario 4: vscodeEnabled false produces clean error

**Goal:** Verify that a sandbox provisioned with `vscodeEnabled: false` produces an operator-actionable error from `km vscode start` rather than a raw SSM failure.

### Pre-conditions

- Ability to create a custom profile YAML

### Steps

1. Create a profile with vscodeEnabled disabled:
   ```bash
   cat > /tmp/no-vscode.yaml << 'EOF'
   extends: learn
   spec:
     cli:
       vscodeEnabled: false
   EOF
   ```

2. Validate the profile:
   ```bash
   km validate /tmp/no-vscode.yaml
   ```
   Expected: validation passes (boolean false is valid).

3. Create the sandbox:
   ```bash
   km create /tmp/no-vscode.yaml --alias no-vscode-test
   SB=$(km list | awk '/no-vscode-test/ {print $1}')
   echo "Sandbox ID: $SB"
   ```
   Note: since VSCodeEnabled=false, **no keypair will be generated** (no ~/.km/keys/$SB file expected).

4. Attempt to start a VS Code session:
   ```bash
   km vscode start $SB
   echo "Exit code: $?"
   ```

5. Destroy the sandbox:
   ```bash
   km destroy $SB --remote --yes
   ```

### Expected Outcome

- Step 2: `km validate` succeeds with no errors
- Step 3: Create succeeds; no "VS Code keypair written" message on stderr (keypair not generated)
- Step 4: `km vscode start` fails with exit code non-zero AND the error message contains:
  - "private key" and "not found" (because no keypair was generated — the missing key is the first gate)
  - OR "VS Code not enabled in this sandbox's profile (set spec.cli.vscodeEnabled: true and recreate)"
  - In either case: NO raw AWS SDK error, NO SSM timeout — a clear operator-actionable message

- [ ] Scenario 4 passed

---

## Scenario 5: Local port collision behavior

**Goal:** Verify that `--local-port` override works when port 2222 is already in use, and that the ssh-config Host entry reflects the chosen port.

### Pre-conditions

- A sandbox with VS Code enabled (keypair present)

### Steps

1. Create a sandbox:
   ```bash
   km create profiles/learn.yaml --alias port-test
   SB=$(km list | awk '/port-test/ {print $1}')
   ```

2. Bind port 2222 to simulate a collision:
   ```bash
   nc -l 2222 &
   NC_PID=$!
   echo "nc PID: $NC_PID"
   ```

3. Attempt to start on the occupied port (expect bind failure from SSM):
   ```bash
   km vscode start $SB
   # This should fail because port 2222 is occupied
   ```

4. Start on an alternate port:
   ```bash
   km vscode start $SB --local-port 9222
   # This should succeed and block — Ctrl-C after the instructions print
   ```

5. Verify the ssh-config Host entry uses the override port:
   ```bash
   grep -A 5 "Host km-$SB" ~/.ssh/config | grep Port
   ```
   Expected: `Port 9222`

6. Kill the nc process and destroy the sandbox:
   ```bash
   kill $NC_PID 2>/dev/null || true
   km destroy $SB --remote --yes
   ```

### Expected Outcome

- Step 3: `km vscode start` fails (SSM bind error for port 2222) — not a crash, a clear error
- Step 4: `km vscode start --local-port 9222` succeeds, prints "✓ Forwarding localhost:9222 → sandbox:22"
- Step 5: `grep Port` shows `Port 9222` in the Host block
- Step 6: Cleanup completes; Host block and keypair files removed by destroy

- [ ] Scenario 5 passed

---

## Scenario 6: Cross-machine portability gap is informative

**Goal:** Verify that attempting `km vscode start` from a machine that didn't create the sandbox produces a helpful error (not a silent failure or raw SSM error) pointing at the missing key file and suggesting a copy.

### Pre-conditions

- A sandbox created by the current machine (keypair exists in `~/.km/keys/`)
- Ability to simulate a missing key by temporarily renaming/removing the key file

### Steps

1. Create a sandbox:
   ```bash
   km create profiles/learn.yaml --alias cross-machine-test
   SB=$(km list | awk '/cross-machine-test/ {print $1}')
   echo "Sandbox ID: $SB"
   ls ~/.km/keys/$SB
   ```

2. Simulate the cross-machine scenario by moving the key file:
   ```bash
   mv ~/.km/keys/$SB /tmp/km-uat-priv-backup
   mv ~/.km/keys/$SB.pub /tmp/km-uat-pub-backup
   echo "Keys moved (simulating machine B)"
   ```

3. Attempt to start VS Code:
   ```bash
   km vscode start $SB
   echo "Exit code: $?"
   ```

4. Verify the error message is helpful:
   The error should contain ALL of:
   - The word "private key" (tells operator what is missing)
   - The exact file path `~/.km/keys/$SB` or the expanded equivalent (tells operator WHERE it should be)
   - A reference to "different machine" or "copy" (tells operator HOW to fix it)

5. Restore the key and destroy:
   ```bash
   mv /tmp/km-uat-priv-backup ~/.km/keys/$SB
   mv /tmp/km-uat-pub-backup ~/.km/keys/$SB.pub
   km destroy $SB --remote --yes
   ```

### Expected Outcome

- Step 3: `km vscode start` fails with non-zero exit code
- Step 4: Error message is operator-actionable — tells them exactly which file is missing and that they need to copy it from the creation machine; no raw SSM error, no "nil pointer dereference", no silent failure
- Step 5: After restoring keys, destroy runs cleanly

- [ ] Scenario 6 passed

---

## Sign-off

Once all six scenarios pass:

1. Tick the checkbox above for each passing scenario.
2. Tick the master sign-off below.
3. Update this file's frontmatter: change `status: pending` to `status: approved`.
4. Return the "approved" resume signal to the GSD orchestrator.

- [ ] All six scenarios passed

**Operator sign-off:** _________________________  Date: _______________  Initials: _____
