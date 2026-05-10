# Phase 76: km vscode rekey — Research

**Researched:** 2026-05-09
**Domain:** Go CLI extension to existing `km vscode` command group — ed25519 keypair rotation via SSM SendCommand
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**CLI surface:**
```
km vscode rekey <sandbox-id> [--force] [--yes]
```
- `--force` overrides the `km lock` safety lock
- `--yes` skips the confirmation prompt
- No `--dry-run`, no `--remote`, no `--bootstrap`, no `--rotate`
- Same identifier formats as other `km vscode` subcommands (full sandbox ID, alias, or list-row number via `ResolveSandboxID`)

**Pre-flight checks (in order; any failure = hard error, no key changes):**
1. EC2 instance state — `ec2.DescribeInstances` → require `InstanceStateNameRunning`
2. Lock check — read DDB lock row; if locked AND `--force` not provided, refuse
3. Remote SSM pre-flight — reuse `vsCodeStatusScript` + `parseVSCodeStatus` verbatim
4. Local key state classification (four cases: normal, cross-laptop, pre-Phase-73, inconsistent)

**Key generation:** Reuse `pkg/sshkey.GenerateAndWrite` as-is. Write to `~/.km/keys/<id>.new` and `~/.km/keys/<id>.pub.new`; commit via atomic rename only after remote push verifies.

**SSM push:** Single round-trip via `sendSSMAndWait`. Script installs authorized_keys, calls `restorecon`, readback-verifies.

**Verification:** Exact byte-for-byte match of readback line vs locally generated pubkey line. No auto-retry.

**Local commit ordering:** `.pub.new` rename before `.new` rename (pub first; private key is what IdentityFile points at).

**`~/.ssh/config`:** Not touched by rekey (path is stable across rekeys).

**Output:** `✓ step markers` matching `vscode start` shape.

**Active VS Code session handling:** No detection. Existing sessions survive; new sessions pick up the new key transparently.

**Deployment:** `make build` only. No `km init --sidecars`, no `km init --lambdas`. No schema changes, no Lambda changes.

### Claude's Discretion

- Exact wording of all error messages (must point operator at correct next-step command)
- SHA256 fingerprint format for display (research confirms: use `gossh.FingerprintSHA256` — matches `ssh-keygen -lf` output as `SHA256:<base64>`)
- Whether to surface AWS region in EC2 step marker
- Test-injection structure for SSMSendAPI mocks (mirror existing `vscode_test.go` patterns)

### Deferred Ideas (OUT OF SCOPE)

- Architectural fix: relocate `authorized_keys` write into `km-bootstrap.service`
- `km vscode rekey-all` bulk rotation
- `km doctor` checks for vscode key drift
- Multiple operators sharing one sandbox (multi-line authorized_keys)
- `km vscode export-key` / `import-key`
- `--dry-run`
- Lockfile against concurrent rekey
- Scheduled periodic rotation via `km at`
- `km ami bake` scrubs authorized_keys before snapshot
- SHA256 fingerprint in `km vscode status` output
</user_constraints>

---

## Summary

Phase 76 is a pure CLI extension phase. No new packages, no new AWS resources, no schema changes, no Lambda changes. The entire implementation is confined to:
- One new function `runVSCodeRekey` (and one small `newVSCodeRekeyCmd` constructor) added to the existing `internal/app/cmd/vscode.go`
- New test cases added to `internal/app/cmd/vscode_test.go` using the existing mock infrastructure
- Documentation additions to `CLAUDE.md` and `docs/vscode.md`

All primitive operations are already implemented and proven in Phase 73: keypair generation (`pkg/sshkey.GenerateAndWrite`), SSM dispatch (`sendSSMAndWait`), remote status probing (`vsCodeStatusScript` + `parseVSCodeStatus`), lock checking (`CheckSandboxLock`), EC2 running-state check (`ec2.DescribeInstances` + filter), and the ssh-config management layer (`UpsertHost`/`RemoveHost`) are all production-proven. Rekey assembles these primitives in a new sequence with `.new` scratch-file staging and atomic rename ordering to ensure the operator's old key remains valid until the remote push is verified.

The key research finding that was absent in Phase 73 research: `gossh.FingerprintSHA256(pubKey PublicKey) string` exists in `golang.org/x/crypto v0.49.0 ssh/keys.go:1819` and returns the `SHA256:` prefix format that matches `ssh-keygen -lf` output. This is the correct function for the confirmation prompt's "Old: SHA256:..." fingerprint display. The function takes a `gossh.PublicKey`, which is obtained by parsing the local `.pub` file with `gossh.ParseAuthorizedKey`.

**Primary recommendation:** Implement as a single focused plan. All dependencies are in place; the complexity is in the ordering discipline (pre-flight gates, `.new` scratch files, rename ordering) not in building new primitives.

---

## Standard Stack

### Core (all already in go.mod, all already used by vscode.go / agent.go)
| Package | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `golang.org/x/crypto/ssh` | v0.49.0 | `FingerprintSHA256`, `ParseAuthorizedKey`, `NewPublicKey` | Already in go.mod; used by keygen.go |
| `crypto/ed25519` | stdlib | Keypair generation (via GenerateAndWrite) | Already used in pkg/sshkey |
| `github.com/aws/aws-sdk-go-v2/service/ec2` | existing | `DescribeInstances` running-state check | Same client used by pause.go, resume.go |
| `github.com/aws/aws-sdk-go-v2/service/dynamodb` | existing | Lock check via `CheckSandboxLock` | Same DDB path used by destroy.go, pause.go |
| `github.com/spf13/cobra` | existing | `--force` and `--yes` flag registration | Same as all other commands |
| `os` | stdlib | Atomic rename (`.new` → final paths), stat for local key presence | Already used throughout vscode.go |

### No New Packages Required
Installation: `make build` — no dependency additions.

---

## Architecture Patterns

### Pattern 1: `newVSCodeRekeyCmd` Constructor (mirrors `newVSCodeStartCmd`)

**What:** A new leaf command registered in `newVSCodeCmdInternal` alongside `start` and `status`. Uses the same dependency-injection signature (`cfg`, `fetcher`, `ssmClient`).

**Flags:**
- `--force` (bool) — passed to `runVSCodeRekey` to skip lock refusal
- `--yes` (bool) — passed to `runVSCodeRekey` to skip confirmation prompt

```go
// Source: internal/app/cmd/vscode.go — mirrors newVSCodeStartCmd pattern
func newVSCodeRekeyCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
    var force, yes bool
    cmd := &cobra.Command{
        Use:          "rekey <sandbox-id>",
        Short:        "Rotate the VS Code Remote-SSH ed25519 keypair for a running sandbox",
        Args:         cobra.ExactArgs(1),
        SilenceUsage: true,
        RunE: func(c *cobra.Command, args []string) error {
            f, _, s, err := resolveVSCodeDeps(c.Context(), cfg, fetcher, nil, ssmClient)
            if err != nil {
                return err
            }
            sandboxID, err := ResolveSandboxID(c.Context(), cfg, args[0])
            if err != nil {
                return err
            }
            return runVSCodeRekey(c.Context(), cfg, f, s, sandboxID, force, yes)
        },
    }
    cmd.Flags().BoolVar(&force, "force", false, "Override the km lock safety lock")
    cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
    return cmd
}
```

Registration in `newVSCodeCmdInternal` (add after `start` and `status`):
```go
parent.AddCommand(newVSCodeRekeyCmd(cfg, fetcher, ssmClient))
```

### Pattern 2: EC2 Running-State Pre-flight

**What:** `ec2.DescribeInstances` with `instance-state-name: running` filter, then check len > 0.

**Verified from:** `pause.go:139-154`, `resume.go:90-98`. Both filter on `instance-state-name` in the API call rather than checking the state field on the returned instance.

```go
// Source: internal/app/cmd/pause.go:139 — same filter shape
ec2Client := ec2.NewFromConfig(awsCfg)
descOut, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
    Filters: []ec2types.Filter{
        {Name: aws.String("tag:km:sandbox-id"), Values: []string{sandboxID}},
        {Name: aws.String("instance-state-name"), Values: []string{"running"}},
    },
})
if err != nil {
    return fmt.Errorf("describe instances: %w", err)
}
// Extract instance ID + region from the first reservation
var instanceID, region string
for _, res := range descOut.Reservations {
    for _, inst := range res.Instances {
        instanceID = aws.ToString(inst.InstanceId)
        region = aws.ToString(inst.Placement.AvailabilityZone)
        // region from AZ: strip trailing letter; or use fetcher.FetchSandbox rec.Region
    }
}
if instanceID == "" {
    return fmt.Errorf("sandbox %s is not running — check status with km list, then km resume %s", sandboxID, sandboxID)
}
```

**IMPORTANT:** `runVSCodeRekey` also needs the instance ID for SSM calls. The cleanest approach (matching `runVSCodeStart`) is to call `fetcher.FetchSandbox` first for instance ID + region, then validate the EC2 state separately. This avoids needing to extract region from availability-zone strings. Pattern:

```go
rec, err := fetcher.FetchSandbox(ctx, sandboxID)   // gets instanceID + region from DDB/S3
instanceID, err := extractResourceID(rec.Resources, ":instance/")
// then DescribeInstances with instanceID filter OR check rec.Status == "running"
```

The CONTEXT.md specifies DescribeInstances API call explicitly (not DDB status field), so the implementation must call the EC2 API. The fetcher gives the instance ID; DescribeInstances confirms live state.

### Pattern 3: Lock Check with `--force` Override

**What:** `CheckSandboxLock` already exists in `lock.go:134`. It returns an error message `"sandbox X is locked — run 'km unlock X' first"`. Rekey intercepts this to substitute its own message and `--force` bypass.

**Verified from:** `lock.go:134-165`, `destroy.go:161`, `pause.go:79`.

`CheckSandboxLock` does NOT accept a `--force` flag parameter; it returns an error unconditionally when locked. The rekey command must call `CheckSandboxLock` only when `--force` is false:

```go
// Lock check — honoring --force override
if !force {
    if err := CheckSandboxLock(ctx, cfg, sandboxID); err != nil {
        // Replace the generic lock message with a rekey-specific one
        return fmt.Errorf("sandbox is locked. Use --force to override or run: km unlock %s", sandboxID)
    }
}
```

This is the correct pattern: skip `CheckSandboxLock` entirely when `--force` is set (rather than calling it and ignoring the error, which would be confusing).

### Pattern 4: SSM Install + Readback Script (single round-trip)

**What:** One `sendSSMAndWait` call that installs the new pubkey into `authorized_keys`, calls `restorecon`, then reads back the first line and SHA256 of the file.

```go
// Source: internal/app/cmd/vscode.go:149 (sendSSMAndWait call pattern)
// + pkg/compiler/userdata.go:1814-1826 (restorecon requirement from Phase 73 POC)
installScript := fmt.Sprintf(`set -e
mkdir -p /home/sandbox/.ssh
chmod 700 /home/sandbox/.ssh
chown sandbox:sandbox /home/sandbox/.ssh
cat > /home/sandbox/.ssh/authorized_keys << 'KEY'
%s
KEY
chmod 600 /home/sandbox/.ssh/authorized_keys
chown sandbox:sandbox /home/sandbox/.ssh/authorized_keys
command -v restorecon >/dev/null 2>&1 && restorecon -R -v /home/sandbox/.ssh || true
echo "=== READBACK ==="
head -1 /home/sandbox/.ssh/authorized_keys
echo "=== SHA256 ==="
sha256sum /home/sandbox/.ssh/authorized_keys | awk '{print $1}'`, newPubKeyLine)

output, err := sendSSMAndWait(ctx, ssmClient, instanceID, installScript)
```

Parse readback:
```go
const readbackMarker = "=== READBACK ==="
idx := strings.Index(output, readbackMarker)
if idx < 0 {
    return fmt.Errorf("remote key install: readback marker absent in SSM output")
}
after := strings.TrimPrefix(output[idx:], readbackMarker)
after = strings.TrimLeft(after, "\n")
// Extract first non-empty line
readbackLine := ""
for _, l := range strings.SplitN(after, "\n", 3) {
    if l != "" {
        readbackLine = l
        break
    }
}
if readbackLine != newPubKeyLine {
    return fmt.Errorf("remote key install verification failed. Old key still active locally.\n"+
        "Inspect via: km shell %s -- cat ~/.ssh/authorized_keys\nRe-run: km vscode rekey %s",
        sandboxID, sandboxID)
}
```

**Why `set -e` at top:** Ensures the script exits non-zero if any step fails (e.g., chown fails). `sendSSMAndWait` will surface the non-zero exit as an error via `CommandInvocationStatusFailed`.

### Pattern 5: Fingerprint Display for Confirmation Prompt

**What:** `gossh.FingerprintSHA256(pubKey)` from `golang.org/x/crypto v0.49.0 ssh/keys.go:1819`. Takes a `gossh.PublicKey`, returns `"SHA256:<base64>"`. This matches `ssh-keygen -lf` output format exactly (confirmed from x/crypto source).

To compute fingerprint of the existing local `.pub` file:

```go
// Source: golang.org/x/crypto v0.49.0 ssh/keys.go:1819
import gossh "golang.org/x/crypto/ssh"

func pubkeyFingerprint(pubPath string) string {
    raw, err := os.ReadFile(pubPath)
    if err != nil {
        return "(unable to read local pubkey)"
    }
    pk, _, _, _, err := gossh.ParseAuthorizedKey(raw)
    if err != nil {
        return "(unable to parse local pubkey)"
    }
    return gossh.FingerprintSHA256(pk)
}
```

`gossh.ParseAuthorizedKey` expects a trailing newline — `keygen.go` writes the `.pub` file with `pubLine+"\n"`, so the raw bytes parse correctly as-is.

### Pattern 6: Atomic `.new` File Staging + Rename Ordering

**What:** Two scratch files written first; both committed via `os.Rename` only after SSM verification succeeds. `.pub.new` renamed first (it's informational); `.new` (private key) renamed second (it's what `IdentityFile` points at).

```go
// Write to scratch paths
home, _ := os.UserHomeDir()
keysDir := filepath.Join(home, ".km", "keys")
privNewPath := filepath.Join(keysDir, sandboxID+".new")
pubNewPath := filepath.Join(keysDir, sandboxID+".pub.new")

newPubKeyLine, err := sshkey.GenerateAndWrite(privNewPath, pubNewPath, "km-"+sandboxID)
if err != nil {
    return fmt.Errorf("generate new keypair: %w", err)
}
// ... SSM push + verification ...

// Commit (both renames are atomic on POSIX)
if err := os.Rename(pubNewPath, filepath.Join(keysDir, sandboxID+".pub")); err != nil {
    return fmt.Errorf("commit new public key: %w", err)
}
if err := os.Rename(privNewPath, filepath.Join(keysDir, sandboxID)); err != nil {
    return fmt.Errorf("commit new private key: %w", err)
}
```

**Why this ordering matters:** If the process is interrupted between the two renames, the private key file still holds the old key. SSH will succeed with the old private key against the remote `authorized_keys` (which now holds the new pubkey) — this means access is temporarily broken. This is acceptable per the CONTEXT.md design decision: "`.pub` mismatch will be detected on next rekey via the fingerprint-diff display."

### Pattern 7: Confirmation Prompt

**What:** Print fingerprint info before generating the new key; prompt with `[y/N]` default-no. Skip when `--yes`.

```go
if !yes {
    oldFP := pubkeyFingerprint(filepath.Join(keysDir, sandboxID+".pub"))
    fmt.Printf("Rotating VS Code key for %s\n", sandboxID)
    fmt.Printf("  Old: %s (~/.km/keys/%s)\n", oldFP, sandboxID)
    fmt.Printf("  New: (will be generated)\n")
    fmt.Printf("Continue? [y/N] ")
    var answer string
    fmt.Scanln(&answer)
    if answer != "y" && answer != "Y" && answer != "yes" {
        fmt.Println("Aborted.")
        return nil // exit code 0 (consistent with unlock cancel)
    }
} else if localKeyAbsent {
    // Cross-laptop: no old key, --yes bypasses prompt entirely — no special message needed
    // because the step markers tell the story
}
```

**Cross-laptop case:** When local `.pub` file is absent (cross-laptop bootstrap case), show `"Old: (no local key — cross-laptop bootstrap)"` instead. This is a visual-only difference; the flow proceeds identically.

### Output Format (✓ step markers)

Success path (normal rotation):
```
✓ EC2 instance running (i-0xxxxxxxxxxxxxxxx in us-east-1)
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

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| ed25519 keypair generation | Custom crypto | `pkg/sshkey.GenerateAndWrite` | Already used by create.go; handles file modes, parent dir creation, comment in pubkey line |
| SSM command dispatch | New SSM poller | `sendSSMAndWait` (agent.go:1067) | Already battle-tested; handles polling, timeout, mock interface |
| Remote status probe | New SSM script | `vsCodeStatusScript` + `parseVSCodeStatus` | Verbatim reuse; canonical Phase 73 error messages |
| SSH public-key fingerprint | Manual SHA256 | `gossh.FingerprintSHA256` (x/crypto v0.49.0) | Returns `SHA256:` prefix format matching `ssh-keygen -lf`; one-liner |
| SSH public-key parsing | Manual line parsing | `gossh.ParseAuthorizedKey` | Handles all edge cases; already used in `keygen_test.go` |
| Lock check | DDB GetItem | `CheckSandboxLock(ctx, cfg, sandboxID)` | Includes DDB + S3 fallback, fail-open semantics; already used by destroy/pause/stop |
| Sandbox identifier resolution | Custom lookup | `ResolveSandboxID(ctx, cfg, ref)` (sandbox_ref.go:25) | Accepts ID/alias/list-number; already used by start/status |
| Sandbox fetch (instance ID) | Tag-based EC2 scan | `fetcher.FetchSandbox(ctx, sandboxID)` + `extractResourceID` | Same 2-step pattern as runVSCodeStart; gives instance ID + region |
| Atomic file write | `os.Create` + direct write | `os.Rename` after writing to `.new` temp path | Prevents partial-write corruption; POSIX-atomic |

**Key insight:** Phase 76 is purely a sequencing and error-handling problem. Every primitive already exists. The planner should NOT be tempted to build new SSM helpers, new DDB clients, or new file utilities.

---

## Common Pitfalls

### Pitfall 1: Calling `CheckSandboxLock` When `--force` Is Set

**What goes wrong:** If `CheckSandboxLock` is called unconditionally and its return value ignored when `--force` is true, the function still performs a DDB read that may fail if the operator has limited credentials. More subtly, if `--force` is handled by checking the error string, future error-message changes break the bypass silently.

**How to avoid:** Gate the call: `if !force { if err := CheckSandboxLock(...); err != nil { return customError } }`. Skip the call entirely when `--force` is set.

### Pitfall 2: `parseVSCodeStatus` Returns Error for Pre-Phase-73 Sandbox — Rekey Must Not Continue

**What goes wrong:** The CONTEXT.md pre-flight flow says `parseVSCodeStatus` covers the "sshd inactive AND authkeys absent" case (path 4.3). This function is reused verbatim — if it returns a non-nil error, rekey must NOT proceed to key generation. The planner may be tempted to add a separate pre-Phase-73 check before calling `parseVSCodeStatus`.

**How to avoid:** Call `parseVSCodeStatus` and propagate its error unchanged. The function already returns the canonical error messages. The local key state classification (step 4 in pre-flight) distinguishes the cross-laptop case (local absent + remote present) from the inconsistent case (local present + remote absent) AFTER `parseVSCodeStatus` succeeds (remote sshd active + authkeys present confirmed).

**Order matters:**
1. `parseVSCodeStatus` FIRST (gates on remote state)
2. Local key classification SECOND (gates on local file presence given remote is healthy)

### Pitfall 3: `set -e` in SSM Script Requires `restorecon` Guard

**What goes wrong:** With `set -e`, if `restorecon` is absent (Ubuntu sandbox), the script exits non-zero and `sendSSMAndWait` returns an error, causing rekey to fail even though the authorized_keys write succeeded.

**How to avoid:** Use `command -v restorecon >/dev/null 2>&1 && restorecon -R -v /home/sandbox/.ssh || true`. The `|| true` ensures the pipeline succeeds even when restorecon is absent. Confirmed pattern from Phase 73 CONTEXT.md POC lesson #3.

### Pitfall 4: `.new` File Left on Disk After Crash Mid-Rename

**What goes wrong:** If the process crashes between `os.Rename(pubNewPath, pubFinalPath)` and `os.Rename(privNewPath, privFinalPath)`, a subsequent `km vscode rekey` call finds a `.pub` that reflects the new pubkey but a private key that is old. The next pre-flight sees the inconsistency.

**How to avoid:** Per CONTEXT.md design decision: `.new` / `.pub.new` files from a crashed prior rekey are unconditionally overwritten. `GenerateAndWrite` writes with `os.WriteFile` which already overwrites. The operator observes this as: "rekey from a different terminal generates a different keypair and overwrites the scratch files — the second rekey completes cleanly." This is intentional last-writer-wins behavior.

### Pitfall 5: Readback Parsing Is Brittle Against Trailing `\r` or Extra Whitespace

**What goes wrong:** Some SSM output paths (especially on certain OS versions) may append `\r` to line endings. An exact byte-for-byte comparison against `newPubKeyLine` (which has no `\r`) would fail.

**How to avoid:** Strip exactly one trailing `\n` per the CONTEXT.md spec. Also strip `\r` for cross-platform safety: `readbackLine = strings.TrimRight(readbackLine, "\r\n")`. The comparison then checks the trimmed string against `newPubKeyLine` (which `GenerateAndWrite` returns without trailing newline).

### Pitfall 6: EC2 Region for the Step Marker

**What goes wrong:** The CONTEXT.md output example shows `(i-0xxxxxxxxxxxxxxxx in us-east-1)`. The region must come from somewhere reliable. `DescribeInstances` returns `Placement.AvailabilityZone` (e.g., `us-east-1a`), not the region directly. `fetcher.FetchSandbox` returns `rec.Region` which is the full region string.

**How to avoid:** Use `rec.Region` from `fetcher.FetchSandbox` (same approach as `runVSCodeStart` at `vscode.go:177`). Do not derive region from the AZ string.

### Pitfall 7: `resolveVSCodeDeps` Does Not Initialize `ec2.Client`

**What goes wrong:** `resolveVSCodeDeps` initializes `fetcher` and `ssmClient` but not an EC2 client. The rekey pre-flight needs an EC2 client for `DescribeInstances`. If the implementation calls `resolveVSCodeDeps` and then tries to get an EC2 client from the returned `fetcher`, it will fail — `SandboxFetcher` is an S3/DDB interface, not an EC2 interface.

**How to avoid:** The EC2 client must be created separately inside `runVSCodeRekey` using `kmaws.LoadAWSConfig` (same pattern as `runPause`). This is an additional AWS client initialization that `resolveVSCodeDeps` does not cover.

---

## Code Examples

### Fingerprint Computation from Local `.pub` File

```go
// Source: golang.org/x/crypto v0.49.0 ssh/keys.go:1819 (FingerprintSHA256)
// + pkg/sshkey/keygen_test.go:57 (ParseAuthorizedKey pattern)
import gossh "golang.org/x/crypto/ssh"

func pubkeyFingerprint(pubPath string) string {
    raw, err := os.ReadFile(pubPath)
    if err != nil {
        return "(unable to read local pubkey)"
    }
    pk, _, _, _, err := gossh.ParseAuthorizedKey(raw)
    if err != nil {
        return "(unable to parse local pubkey)"
    }
    return gossh.FingerprintSHA256(pk)
}
```

`FingerprintSHA256` signature: `func FingerprintSHA256(pubKey PublicKey) string` — returns `"SHA256:<unpadded_base64>"`.

### `sendSSMAndWait` Call Signature (verified)

```go
// Source: internal/app/cmd/agent.go:1071
func sendSSMAndWait(ctx context.Context, ssmClient SSMSendAPI, instanceID, shellCmd string) (string, error)
```

Returns full stdout from `StandardOutputContent`. Polls until `CommandInvocationStatusSuccess` or error status. Timeout: 5 minutes (150 attempts × 2s).

### `CheckSandboxLock` Call Signature (verified)

```go
// Source: internal/app/cmd/lock.go:134
func CheckSandboxLock(ctx context.Context, cfg *config.Config, sandboxID string) error
```

Returns error message `"sandbox X is locked — run 'km unlock X' first"` when locked. Fail-open: returns nil when DDB/S3 unavailable. Used verbatim in destroy.go:161, pause.go:79, stop.go:64.

### `extractResourceID` Call Pattern (verified)

```go
// Source: internal/app/cmd/vscode.go:129 — same exact call as runVSCodeStart
instanceID, err := extractResourceID(rec.Resources, ":instance/")
```

`rec.Resources` is `[]string` of ARNs from `fetcher.FetchSandbox`. `":instance/"` extracts EC2 instance ID from ARNs like `"arn:aws:ec2:us-east-1:123456789012:instance/i-0abc..."`.

### `newVSCodeCmdInternal` Registration Point (verified)

```go
// Source: internal/app/cmd/vscode.go:34-43
func newVSCodeCmdInternal(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
    parent := &cobra.Command{ ... }
    parent.AddCommand(newVSCodeStartCmd(cfg, fetcher, execFn, ssmClient))
    parent.AddCommand(newVSCodeStatusCmd(cfg, fetcher, ssmClient))
    // ADD: parent.AddCommand(newVSCodeRekeyCmd(cfg, fetcher, ssmClient))
    return parent
}
```

`newVSCodeRekeyCmd` does not need `execFn` (no port-forward to exec). The constructor signature is `(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI)`.

### Key File Paths (consistent with cleanupVSCodeState)

```go
// Source: internal/app/cmd/destroy.go:755-779 (cleanupVSCodeState)
home, _ := os.UserHomeDir()
keysDir := filepath.Join(home, ".km", "keys")
privPath    := filepath.Join(keysDir, sandboxID)          // ~/.km/keys/sb-abc123
pubPath     := filepath.Join(keysDir, sandboxID+".pub")   // ~/.km/keys/sb-abc123.pub
privNewPath := filepath.Join(keysDir, sandboxID+".new")   // ~/.km/keys/sb-abc123.new
pubNewPath  := filepath.Join(keysDir, sandboxID+".pub.new") // ~/.km/keys/sb-abc123.pub.new
```

The `cleanupVSCodeState` function (destroy.go:755) uses `sandboxID` (not `"km-"+sandboxID`) for key file names, and uses `"km-"+sandboxID` only for the ssh-config Host alias. Rekey writes to the same key file paths.

### Full `runVSCodeRekey` Skeleton

```go
func runVSCodeRekey(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI, sandboxID string, force, yes bool) error {
    // 1. Fetch sandbox record (instance ID + region)
    rec, err := fetcher.FetchSandbox(ctx, sandboxID)
    if err != nil { return fmt.Errorf("fetch sandbox: %w", err) }
    instanceID, err := extractResourceID(rec.Resources, ":instance/")
    if err != nil { return fmt.Errorf("find EC2 instance: %w", err) }

    // 2. EC2 running-state check
    awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
    if err != nil { return fmt.Errorf("load AWS config: %w", err) }
    ec2Client := ec2.NewFromConfig(awsCfg)
    descOut, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
        Filters: []ec2types.Filter{
            {Name: aws.String("tag:km:sandbox-id"), Values: []string{sandboxID}},
            {Name: aws.String("instance-state-name"), Values: []string{"running"}},
        },
    })
    if err != nil { return fmt.Errorf("describe instances: %w", err) }
    running := false
    for _, r := range descOut.Reservations {
        if len(r.Instances) > 0 { running = true }
    }
    if !running {
        return fmt.Errorf("sandbox %s is not running — check with km list, then km resume %s", sandboxID, sandboxID)
    }
    fmt.Printf("✓ EC2 instance running (%s in %s)\n", instanceID, rec.Region)

    // 3. Lock check
    if !force {
        if err := CheckSandboxLock(ctx, cfg, sandboxID); err != nil {
            return fmt.Errorf("sandbox is locked. Use --force to override or run: km unlock %s", sandboxID)
        }
    }

    // 4. Remote SSM pre-flight
    out, err := sendSSMAndWait(ctx, ssmClient, instanceID, vsCodeStatusScript)
    if err != nil { return fmt.Errorf("ssm pre-flight: %w", err) }
    if err := parseVSCodeStatus(out, sandboxID); err != nil { return err }
    fmt.Printf("✓ Pre-flight check passed (sshd active, authorized_keys present)\n")

    // 5. Local key state classification
    home, _ := os.UserHomeDir()
    keysDir := filepath.Join(home, ".km", "keys")
    localPubPath := filepath.Join(keysDir, sandboxID+".pub")
    _, localPubErr := os.Stat(localPubPath)
    localKeyAbsent := os.IsNotExist(localPubErr)

    // 6. Confirmation prompt
    if !yes {
        oldFP := "(no local key — cross-laptop bootstrap)"
        if !localKeyAbsent {
            oldFP = fmt.Sprintf("%s (~/.km/keys/%s)", pubkeyFingerprint(localPubPath), sandboxID)
        }
        fmt.Printf("\nRotating VS Code key for %s\n", sandboxID)
        fmt.Printf("  Old: %s\n", oldFP)
        fmt.Printf("  New: (will be generated)\n")
        fmt.Printf("Continue? [y/N] ")
        var answer string
        fmt.Scanln(&answer)
        if answer != "y" && answer != "Y" && answer != "yes" {
            fmt.Println("Aborted.")
            return nil
        }
        fmt.Println()
    }

    // 7. Generate new keypair to scratch paths
    privNewPath := filepath.Join(keysDir, sandboxID+".new")
    pubNewPath  := filepath.Join(keysDir, sandboxID+".pub.new")
    newPubKeyLine, err := sshkey.GenerateAndWrite(privNewPath, pubNewPath, "km-"+sandboxID)
    if err != nil { return fmt.Errorf("generate new keypair: %w", err) }
    newFP := pubkeyFingerprint(pubNewPath)
    fmt.Printf("✓ New keypair generated (%s)\n", newFP)

    // 8. SSM push + readback verify
    installScript := fmt.Sprintf(`set -e
mkdir -p /home/sandbox/.ssh
chmod 700 /home/sandbox/.ssh
chown sandbox:sandbox /home/sandbox/.ssh
cat > /home/sandbox/.ssh/authorized_keys << 'KEY'
%s
KEY
chmod 600 /home/sandbox/.ssh/authorized_keys
chown sandbox:sandbox /home/sandbox/.ssh/authorized_keys
command -v restorecon >/dev/null 2>&1 && restorecon -R -v /home/sandbox/.ssh || true
echo "=== READBACK ==="
head -1 /home/sandbox/.ssh/authorized_keys`, newPubKeyLine)
    ssmOut, err := sendSSMAndWait(ctx, ssmClient, instanceID, installScript)
    if err != nil { return fmt.Errorf("ssm install: %w", err) }

    // Parse readback
    idx := strings.Index(ssmOut, "=== READBACK ===")
    if idx < 0 {
        return fmt.Errorf("remote key install: readback marker absent — inspect with: km shell %s -- cat ~/.ssh/authorized_keys", sandboxID)
    }
    readbackSection := strings.TrimLeft(ssmOut[idx+len("=== READBACK ==="):], "\n")
    readbackLine := ""
    for _, l := range strings.SplitN(readbackSection, "\n", 3) {
        trimmed := strings.TrimRight(l, "\r\n")
        if trimmed != "" { readbackLine = trimmed; break }
    }
    if readbackLine != newPubKeyLine {
        return fmt.Errorf("remote key install verification failed. Old key still active locally.\nInspect via: km shell %s -- cat ~/.ssh/authorized_keys\nRe-run: km vscode rekey %s", sandboxID, sandboxID)
    }
    fmt.Printf("✓ Pushed to sandbox via SSM (verified — readback matches)\n")

    // 9. Atomic local commit (.pub first, then private)
    privFinalPath := filepath.Join(keysDir, sandboxID)
    pubFinalPath  := filepath.Join(keysDir, sandboxID+".pub")
    if err := os.Rename(pubNewPath, pubFinalPath); err != nil {
        return fmt.Errorf("commit new public key: %w", err)
    }
    if err := os.Rename(privNewPath, privFinalPath); err != nil {
        return fmt.Errorf("commit new private key: %w", err)
    }

    actionWord := "replaced"
    if localKeyAbsent { actionWord = "created" }
    fmt.Printf("✓ Local key %s atomically (~/.km/keys/%s)\n\n", actionWord, sandboxID)
    fmt.Printf("Rekey complete. Active VS Code sessions stay on the old key until reconnect.\n")
    return nil
}
```

---

## State of the Art

| Old Approach | Current Approach (Phase 76) | Notes |
|---|---|---|
| Manual `scp` of key files between laptops | `km vscode rekey --yes` on second laptop | Cross-laptop bootstrap now a single command |
| `km destroy && km create` after baked-AMI stale-key relaunch | `km vscode rekey` on running sandbox | No rebuild required |
| No post-incident key rotation without sandbox rebuild | `km vscode rekey` with optional `km lock` bypass | Incident response path now available |

**Predecessor:** Phase 73 (complete) established the per-sandbox ed25519 convention, `pkg/sshkey.GenerateAndWrite`, `vsCodeStatusScript`/`parseVSCodeStatus`, `UpsertHost`/`RemoveHost`, and `cleanupVSCodeState`. Phase 76 inherits all of it.

---

## Open Questions

1. **EC2 client initialization inside `runVSCodeRekey`**
   - What we know: `resolveVSCodeDeps` initializes `fetcher` and `ssmClient` but not an `ec2.Client`. The DescribeInstances call requires a separate EC2 client.
   - What's unclear: Whether to add `ec2Client` to the `resolveVSCodeDeps` return signature (would require touching start/status) or initialize it inline inside `runVSCodeRekey`.
   - Recommendation: Initialize inline inside `runVSCodeRekey` (same pattern as `runPause` and `runResume` which initialize their own EC2 clients). Do not extend `resolveVSCodeDeps` — it's a narrow vscode-specific helper.

2. **EC2 instance ID: use `fetcher.FetchSandbox` or `DescribeInstances` filter**
   - What we know: `runVSCodeStart` uses `fetcher.FetchSandbox` to get the instance ID, then makes a separate SSM call. `runPause` and `runResume` use `DescribeInstances` with a tag filter to get the instance ID.
   - Recommendation: Use `fetcher.FetchSandbox` first (matches start/status pattern, enables mock injection in tests), then confirm running-state with `DescribeInstances`. This gives the planner both instance ID and region from the fetcher, and the running-state confirmation from EC2 API.

---

## Validation Architecture

> `workflow.nyquist_validation` is `true` in `.planning/config.json` — validation section included.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (`testing` package) |
| Config file | none — `go test ./...` from repo root |
| Quick run command | `go test ./internal/app/cmd/ -run 'TestVSCodeRekey' -timeout 30s -v` |
| Full suite command | `go test ./internal/app/cmd/ -run 'TestVSCode' -timeout 60s` |

### Phase Requirements → Test Map

| Req | Behavior | Test Type | Automated Command | File Exists? |
|-----|----------|-----------|-------------------|-------------|
| CLI surface | `newVSCodeRekeyCmd` registered under `km vscode` | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_CommandRegistered -v` | ❌ Wave 0 |
| Pre-flight: EC2 not running | Returns error pointing at `km resume` | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_NotRunning -v` | ❌ Wave 0 |
| Pre-flight: sandbox locked, no --force | Returns error pointing at `km unlock` | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_Locked_NoForce -v` | ❌ Wave 0 |
| Pre-flight: sandbox locked, --force | Proceeds past lock check | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_Locked_WithForce -v` | ❌ Wave 0 |
| Pre-flight: vscodeEnabled:false (sshd+authkeys both absent) | Returns vscode-not-enabled error | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_VSCodeDisabled -v` | ❌ Wave 0 |
| Pre-flight: sshd active, authkeys absent (inconsistent) | Returns unexpected-state error | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_Inconsistent -v` | ❌ Wave 0 |
| Pre-flight: sshd inactive, authkeys present | Returns sshd-not-running error | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_SSHDDown -v` | ❌ Wave 0 |
| Local key state: local absent, remote present | Cross-laptop bootstrap path succeeds | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_CrossLaptop -v` | ❌ Wave 0 |
| Local key state: local present, remote present | Normal rotation path | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_NormalRotation -v` | ❌ Wave 0 |
| Confirmation prompt: --yes skips prompt | No stdin read when --yes set | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_YesFlag -v` | ❌ Wave 0 |
| Confirmation prompt: "n" answer aborts cleanly | Exit code 0, no key changes | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_ConfirmNo -v` | ❌ Wave 0 |
| SSM verification mismatch | Verification fail error, old key preserved | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_VerifyMismatch -v` | ❌ Wave 0 |
| Atomic rename ordering | `.pub.new` renamed before `.new` (private) | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_RenameOrdering -v` | ❌ Wave 0 |
| Existing `.new` files overwritten | Pre-existing scratch files silently overwritten | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_OverwritesScratch -v` | ❌ Wave 0 |
| Success output format | Step markers printed in correct order | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_OutputMarkers -v` | ❌ Wave 0 |
| `--force` flag registered | Flag accessible on `km vscode rekey` cobra command | unit | `go test ./internal/app/cmd/ -run TestVSCodeRekey_FlagsExist -v` | ❌ Wave 0 |

### Test Infrastructure (already exists, reuse verbatim)

These mock types from `vscode_test.go` are reused as-is:
- `vsCodeSSMMock` — controls `StandardOutputContent` returned by every `GetCommandInvocation` call
- `vsCodeFetcherMock` — returns a fixed `SandboxRecord`
- `newVSCodeEC2Sandbox(id)` — builds a minimal running EC2 sandbox record
- `healthySSMOutput` — const string for healthy sshd + authkeys state
- `captureStdout(fn)` — captures stdout for output-format assertions

For rekey-specific SSM mock needs: the mock needs to serve two different outputs (status script output for pre-flight, install script output for push). Options:
- Sequence mock (returns different outputs on successive calls)
- Or: use two separate mock instances in different test functions (each test targets one code path)

Recommendation: use two separate mock instances — one for the pre-flight call (returns `healthySSMOutput`), one for the install call (returns the install+readback output). Each test injects the mock that covers its specific failure/success path. This avoids stateful mock complexity.

### Sampling Rate
- **Per task commit:** `go test ./internal/app/cmd/ -run 'TestVSCodeRekey' -timeout 30s`
- **Per wave merge:** `go test ./internal/app/cmd/ -run 'TestVSCode' -timeout 60s`
- **Phase gate:** `go test ./...` green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `internal/app/cmd/vscode_test.go` — add all `TestVSCodeRekey_*` test stubs (file already exists; append to it)
- [ ] No new files needed — all test infrastructure is already in `vscode_test.go`

*(No new test infrastructure needed — existing `vsCodeSSMMock`, `vsCodeFetcherMock`, `newVSCodeEC2Sandbox`, `healthySSMOutput`, and `captureStdout` are ready to reuse.)*

---

## Sources

### Primary (HIGH confidence)

- `/Users/khundeck/working/klankrmkr/internal/app/cmd/vscode.go:21,34,88,209` — `vsCodeStatusScript`, `newVSCodeCmdInternal`, `resolveVSCodeDeps`, `parseVSCodeStatus` — read verbatim
- `/Users/khundeck/working/klankrmkr/pkg/sshkey/keygen.go:24` — `GenerateAndWrite` function — read verbatim
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/agent.go:1068-1118` — `sendSSMAndWait` function — read verbatim
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/lock.go:129-165` — `CheckSandboxLock` function — read verbatim
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/destroy.go:159-163,751-780` — lock-check call pattern and `cleanupVSCodeState` key-file paths — read verbatim
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/pause.go:79,139-154` — lock-check pattern and `DescribeInstances` running-state check — read verbatim
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/vscode_test.go:1-199` — existing mock infrastructure — read verbatim
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/sandbox_ref.go:25` — `ResolveSandboxID` location — confirmed
- `/Users/khundeck/go/pkg/mod/golang.org/x/crypto@v0.49.0/ssh/keys.go:1814-1823` — `FingerprintSHA256` function signature and return format — read verbatim
- `/Users/khundeck/working/klankrmkr/pkg/sshkey/keygen_test.go:57` — `ParseAuthorizedKey` usage pattern — read verbatim
- `.planning/phases/76-km-vscode-rekey-rotate-ed25519-keypair-for-an-existing-sandbox/76-CONTEXT.md` — locked design decisions — primary spec

### Secondary (MEDIUM confidence)

- `.planning/phases/73-km-vscode-remote-session-via-ssm/73-CONTEXT.md` — POC lessons (restorecon, AL2023 sshd patterns) — confirmed inherited
- `.planning/phases/73-km-vscode-remote-session-via-ssm/73-RESEARCH.md` — Phase 73 architecture patterns — cross-referenced

### Tertiary (LOW confidence)

- None — all critical claims verified from source code or module cache.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all packages verified in go.mod and module cache; function signatures read from source
- Architecture: HIGH — all patterns verified by reading vscode.go, agent.go, lock.go, pause.go, resume.go, destroy.go source
- Pitfalls: HIGH — root-cause traced to verified code paths; AL2023 restorecon requirement verified from Phase 73 POC lessons
- Test patterns: HIGH — existing test file read; mock types identified and confirmed reusable

**Research date:** 2026-05-09
**Valid until:** 2026-06-09 (stable Go stdlib + x/crypto; low-churn domain; all dependencies already shipped in Phase 73)
