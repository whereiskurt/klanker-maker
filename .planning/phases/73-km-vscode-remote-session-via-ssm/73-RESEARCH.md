# Phase 73: km vscode remote session via SSM — Research

**Researched:** 2026-05-06
**Domain:** Go ed25519 SSH keypair generation, `~/.ssh/config` managed-block editing, SSM port-forward
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Architecture (locked after POC):** VS Code Remote-SSH over SSM port-forward with per-sandbox auto-generated ed25519 keypairs. No `code serve-web`, no `code tunnel`, no systemd unit files written by km. VS Code Remote-SSH auto-deploys `vscode-server` to the sandbox on first connect.

**Keypair lifecycle:**
- `km create`: generate ed25519 keypair locally on operator laptop using Go (no ssh-keygen). Write `~/.km/keys/sb-<id>` (0600) and `~/.km/keys/sb-<id>.pub` (0644). Pass pubkey as `VSCodeSSHPubKey` template variable.
- Sandbox boot (cloud-init, gated on `vscodeEnabled: true`): `systemctl enable --now sshd`; `mkdir -p /home/sandbox/.ssh` (0700, sandbox:sandbox); write `authorized_keys` (0600, sandbox:sandbox); `restorecon -R -v /home/sandbox/.ssh`.
- `km vscode start <sb>`: upsert managed block in `~/.ssh/config`; open foreground SSM port-forward via existing `buildPortForwardCmd`; block until Ctrl-C.
- `km destroy <sb>`: remove `Host km-sb-<id>` block from `~/.ssh/config`; delete `~/.km/keys/sb-<id>*`.

**CLI surface:** `km vscode start <sandbox-id> [--local-port N]` and `km vscode status <sandbox-id>`. No `stop` in v1.

**Profile gate:** `spec.cli.vscodeEnabled` (`bool*` pointer, default `true` — omit means enabled). Same `*bool` pattern as `NotifyEmailEnabled` and `SlackArchiveOnDestroy`.

**`~/.ssh/config` managed block:** Markers `# BEGIN km vscode hosts (managed; do not edit between markers)` ... `# END km vscode hosts`. Host entry includes `StrictHostKeyChecking no`, `UserKnownHostsFile /dev/null`, `IdentitiesOnly yes`, `ServerAliveInterval 30`.

**No new IAM, DDB schema, SSM Parameters, Lambda, or sidecar binary.** Userdata template change requires `make build && km init --sidecars`; existing sandboxes need `km destroy && km create`.

**POC lessons (mandatory):**
1. `systemctl enable --now sshd` — both flags required on AL2023.
2. `restorecon -R -v /home/sandbox/.ssh` is mandatory on AL2023 (SELinux enforcing).
3. `chown -R sandbox:sandbox /home/sandbox/.ssh` after file writes.
4. `IdentitiesOnly yes` in ssh-config to prevent key exhaustion.
5. `StrictHostKeyChecking no` + `UserKnownHostsFile /dev/null` (fresh host key per sandbox).

### Claude's Discretion

- Default for `--local-port` is 2222.
- Single combined SSM `sendSSMAndWait` call for `status` (vs. 3 separate calls) — tradeoff: combined is simpler; separate gives cleaner failure attribution. Claude recommends: combined single script, parse output sections.
- When `km destroy` removes the last Host entry inside the markers, remove the markers too for tidiness.
- `restorecon` defensive wrap: `command -v restorecon >/dev/null 2>&1 && restorecon -R -v /home/sandbox/.ssh || true` to handle non-SELinux distros. Not strictly needed for AL2023 but cheap and prevents future Ubuntu breakage.
- `~/.km/keys/` dir: explicitly `chmod 0700` on creation (not relying on operator umask).

### Deferred Ideas (OUT OF SCOPE)

- `--duration` flag and auto-stop integration.
- `km doctor` checks for vscode.
- Cross-machine key portability (`km vscode export-key`/`import-key`).
- Multiple operators sharing a sandbox (multi-line authorized_keys).
- Proper SSH host-key trust (TOFU, cert-based).
- `km vscode stop` command.
- sshd config customization (port, ciphers).
- Browser fallback (`code serve-web`).
- `vscodeEnabled` rename to `sshEnabled`.
</user_constraints>

---

## Summary

Phase 73 adds `km vscode start | status` — connecting a local desktop VS Code to a sandbox via VS Code Remote-SSH over an SSM port-forward. The design was locked after live POC validation; the original VS Code Web design was rejected. This phase requires: (1) a new `pkg/sshkey` package for Go-native ed25519 keypair generation, (2) a new `internal/app/cmd/sshconfig.go` for `~/.ssh/config` managed-block editing, (3) a new `internal/app/cmd/vscode.go` cobra subcommand with `start` and `status` children, (4) a conditional userdata block in `pkg/compiler/userdata.go`, (5) `pkg/profile` schema additions, and (6) wiring through `create.go`, `destroy.go`, and `root.go`.

All dependencies (`golang.org/x/crypto v0.49.0`) are already in `go.mod`. No new AWS resources are provisioned. The SSH tunnel security boundary is IAM (SSM), not the SSH layer itself; SSH adds per-sandbox key authentication and encrypted file transfer that VS Code Remote-SSH requires. The operator's private key never leaves their laptop.

**Primary recommendation:** Implement in waves following the existing pattern: Wave 0 stubs tests, Wave A core packages (`pkg/sshkey`, `internal/app/cmd/sshconfig`), Wave B profile/compiler changes, Wave C CLI wiring (`vscode.go`, `create.go`, `destroy.go`), Wave D docs + CLAUDE.md update.

---

## Standard Stack

### Core
| Package | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `crypto/ed25519` | stdlib | Generate ed25519 keypair | Standard library; already used in `cmd/km-slack/main.go` and test files |
| `golang.org/x/crypto/ssh` | v0.49.0 | Marshal ed25519 public/private key to OpenSSH format | Already in `go.mod`; provides `MarshalAuthorizedKey`, `MarshalPrivateKey`, `NewPublicKey` |
| `encoding/pem` | stdlib | Write PEM file from `*pem.Block` returned by `MarshalPrivateKey` | Standard library |
| `github.com/spf13/cobra` | existing | CLI subcommand for `km vscode start | status` | Same as all other commands |

### Supporting
| Package | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `os`, `path/filepath` | stdlib | Create `~/.km/keys/` dir, write key files, stat for existence check | Throughout |
| `bufio`, `strings`, `bytes` | stdlib | `~/.ssh/config` line-by-line parser | `sshconfig.go` |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `ssh.MarshalPrivateKey` (OpenSSH PEM format) | `crypto/x509.MarshalPKCS8PrivateKey` + hand-roll PEM | `MarshalPrivateKey` produces the correct `-----BEGIN OPENSSH PRIVATE KEY-----` format that `ssh -i` and VS Code Remote-SSH expect. PKCS8 format works for TLS but not OpenSSH by default. Use `MarshalPrivateKey`. |
| Custom `~/.ssh/config` parser | `github.com/kevinburke/ssh_config` | The kevinburke library parses well but does not support managed-block semantics (write back only our region). Rolling our own marker-aware scanner is ~100 lines and tests are straightforward. |

**Installation:** No new packages needed. `golang.org/x/crypto v0.49.0` is already in `go.mod`.

---

## Architecture Patterns

### Recommended Project Structure
```
pkg/sshkey/
├── keygen.go          # GenerateAndWrite(privPath, pubPath, comment string) (pubContent string, err error)
└── keygen_test.go     # file mode bits, idempotency, pubkey parses back

internal/app/cmd/
├── sshconfig.go       # UpsertHost, RemoveHost, managed-block parser
├── sshconfig_test.go  # exhaustive parser cases
├── vscode.go          # NewVSCodeCmd, newVSCodeStartCmd, newVSCodeStatusCmd
└── vscode_test.go     # flag parsing, SSM command construction (mock), error paths

pkg/profile/
└── types.go           # add VSCodeEnabled *bool to CLISpec (line ~447)

pkg/profile/schemas/
└── sandbox_profile.schema.json  # add vscodeEnabled boolean to spec.cli

pkg/compiler/
└── userdata.go        # add VSCodeEnabled+VSCodeSSHPubKey fields to userDataParams;
                       # add conditional {{- if .VSCodeEnabled }} block near SlackInboundEnabled

docs/
└── vscode.md          # operator setup guide
```

### Pattern 1: ed25519 Keypair Generation (pkg/sshkey/keygen.go)

**What:** Pure-Go keypair generation using stdlib `crypto/ed25519` and `golang.org/x/crypto/ssh`.
**When to use:** Called by `create.go` when `vscodeEnabled` is true (or nil/default-true), before userdata compilation.

```go
// Source: golang.org/x/crypto v0.49.0 ssh/keys.go (MarshalPrivateKey, MarshalAuthorizedKey, NewPublicKey)
package sshkey

import (
    "crypto/ed25519"
    cryptorand "crypto/rand"
    "encoding/pem"
    "fmt"
    "os"
    "path/filepath"

    gossh "golang.org/x/crypto/ssh"
)

// GenerateAndWrite generates an ed25519 keypair and writes it to privPath (0600)
// and pubPath (0644). Parent directories are created with mode 0700 if absent.
// Returns the single-line OpenSSH public key content (without trailing newline).
// Overwrites existing files idempotently.
func GenerateAndWrite(privPath, pubPath, comment string) (pubContent string, err error) {
    // Create parent dir with 0700 (safe for ~/.km/keys/)
    if mkErr := os.MkdirAll(filepath.Dir(privPath), 0700); mkErr != nil {
        return "", fmt.Errorf("create key directory: %w", mkErr)
    }

    // Generate ed25519 keypair
    pub, priv, err := ed25519.GenerateKey(cryptorand.Reader)
    if err != nil {
        return "", fmt.Errorf("generate ed25519 key: %w", err)
    }

    // Marshal private key to OpenSSH PEM format (-----BEGIN OPENSSH PRIVATE KEY-----)
    privPEM, err := gossh.MarshalPrivateKey(priv, comment)
    if err != nil {
        return "", fmt.Errorf("marshal private key: %w", err)
    }
    privBytes := pem.EncodeToMemory(privPEM)

    // Marshal public key to OpenSSH authorized_keys format (single line, includes trailing \n)
    sshPub, err := gossh.NewPublicKey(pub)
    if err != nil {
        return "", fmt.Errorf("marshal public key: %w", err)
    }
    authorizedKey := string(gossh.MarshalAuthorizedKey(sshPub))
    // MarshalAuthorizedKey appends a \n; we want the bare line for the template variable
    // but also want to write the \n-terminated form to the .pub file.

    // Write private key (mode 0600 — strict; ssh refuses looser perms)
    if err := os.WriteFile(privPath, privBytes, 0600); err != nil {
        return "", fmt.Errorf("write private key: %w", err)
    }
    // Append comment (ssh-ed25519 AAAA... comment) — MarshalAuthorizedKey does NOT include comment;
    // use NewPublicKey + MarshalAuthorizedKey then append comment manually OR use
    // the comment parameter in the format: "ssh-ed25519 <base64> <comment>"
    // Actually: MarshalAuthorizedKey produces "ssh-ed25519 AAAA...\n" without comment.
    // To include comment, format manually: type + " " + base64(Marshal()) + " " + comment + "\n"
    // For authorized_keys the comment field is optional but useful for identification.
    // Write .pub file (mode 0644 — readable)
    if err := os.WriteFile(pubPath, []byte(authorizedKey), 0644); err != nil {
        return "", fmt.Errorf("write public key: %w", err)
    }

    return strings.TrimRight(authorizedKey, "\n"), nil
}
```

**IMPORTANT NOTE on comment in authorized_keys line:** `gossh.MarshalAuthorizedKey` produces `ssh-ed25519 AAAA...\n` with NO comment field. To include the comment (e.g., `km-sb-abc123`) for operator identification in `authorized_keys`, the planner must either:
- Use the formatted output from `gossh.MarshalPrivateKey`'s comment parameter (that goes into the private key file), AND separately construct the pubkey line as `sshPub.Type() + " " + base64(sshPub.Marshal()) + " " + comment + "\n"` — OR —
- Accept that `MarshalAuthorizedKey` produces a valid but comment-less line (still works for sshd).

Recommendation: produce `ssh-ed25519 <base64> km-sb-<id>` by constructing manually. See Code Examples below for the canonical form.

### Pattern 2: `~/.ssh/config` Managed-Block Parser (internal/app/cmd/sshconfig.go)

**What:** Line-by-line scanner that finds `# BEGIN km vscode hosts` ... `# END km vscode hosts` markers and upserts/removes one `Host <alias>` entry inside them.
**When to use:** Called by `km vscode start` (upsert) and `km destroy` (remove).

```go
// Source: hand-rolled (no external library); mirrors configure.go's YAML file management pattern
const (
    beginMarker = "# BEGIN km vscode hosts (managed; do not edit between markers)"
    endMarker   = "# END km vscode hosts"
)

// HostOptions specifies the SSH Host entry fields.
type HostOptions struct {
    HostName          string // "localhost"
    Port              int    // default 2222
    User              string // "sandbox"
    IdentityFile      string // "~/.km/keys/sb-abc123"
}

// UpsertHost inserts or replaces the Host entry for alias in the managed block.
// If the file does not exist, it is created (mode 0600) with the full managed block.
// If markers don't exist, the block is appended.
// Lines outside the markers are never modified.
func UpsertHost(configPath, alias string, opts HostOptions) error { ... }

// RemoveHost removes the Host entry for alias from the managed block.
// If the block becomes empty after removal, the markers are also removed.
// Idempotent: no error if alias not found.
func RemoveHost(configPath, alias string) error { ... }
```

**Parser algorithm:**
1. Read file (or treat as empty if not found).
2. Scan line by line; identify three regions: before-markers, inside-markers, after-markers.
3. Inside-markers: parse Host blocks. A Host block starts with `Host <name>` and runs until the next `Host` line, a marker line, or EOF.
4. For UpsertHost: replace the target Host block if found, insert before END marker if not found.
5. For RemoveHost: drop the target Host block. If no other Host blocks remain inside markers, drop the markers too.
6. Write result back atomically (write to temp file, rename over original).

### Pattern 3: `km vscode` Cobra Command (internal/app/cmd/vscode.go)

**What:** Parent `vscode` command with `start` and `status` children. Pattern mirrors `slack.go`: `NewVSCodeCmd` calls `newVSCodeCmdInternal(cfg, nil)` for testability.

```go
// Source: mirrors newSlackCmdInternal pattern in slack.go:120
func NewVSCodeCmd(cfg *config.Config) *cobra.Command {
    return newVSCodeCmdInternal(cfg, nil, nil, nil)
}

func newVSCodeCmdInternal(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
    cmd := &cobra.Command{
        Use:          "vscode",
        Short:        "Connect local VS Code to a sandbox via Remote-SSH over SSM",
        SilenceUsage: true,
    }
    cmd.AddCommand(newVSCodeStartCmd(cfg, fetcher, execFn))
    cmd.AddCommand(newVSCodeStatusCmd(cfg, fetcher, ssmClient))
    return cmd
}
```

Registration in `root.go` (line ~85, after `NewSlackCmd`):
```go
root.AddCommand(NewVSCodeCmd(cfg))
```

### Pattern 4: `vscodeEnabled` Profile Field (pkg/profile/types.go)

**What:** New `*bool` field on `CLISpec`, following `NotifyEmailEnabled` and `SlackArchiveOnDestroy` exactly.
**Where:** Insert after `NotifySlackTranscriptEnabled` (line ~446 in types.go):

```go
// Source: mirrors NotifyEmailEnabled *bool pattern at types.go:403
// VSCodeEnabled gates the sshd-enable + authorized_keys + restorecon userdata block.
// Pointer type: nil means default true (sshd provisioned); &false skips the block.
// Profile field: spec.cli.vscodeEnabled
VSCodeEnabled *bool `yaml:"vscodeEnabled,omitempty" json:"vscodeEnabled,omitempty"`
```

**Default-true helper pattern** (mirror of `ClaudeTelemetrySpec.IsEnabled()`):
```go
// IsVSCodeEnabled returns true when VSCodeEnabled is nil (default) or &true.
func isVSCodeEnabled(cli *CLISpec) bool {
    if cli == nil || cli.VSCodeEnabled == nil {
        return true // default true
    }
    return *cli.VSCodeEnabled
}
```

This helper lives in `pkg/compiler/userdata.go` (or `types.go`) and is called during `generateUserData`.

### Pattern 5: Userdata Conditional Block (pkg/compiler/userdata.go)

**What:** New conditional block near `SlackInboundEnabled` checks (line ~1207). Two new fields on `userDataParams`: `VSCodeEnabled bool` and `VSCodeSSHPubKey string`.

In `userDataParams` struct (after `SlackInboundEnabled bool` field, ~line 2867):
```go
// VSCodeEnabled gates the sshd-enable + authorized_keys + restorecon block.
// Set from profile.Spec.CLI.VSCodeEnabled (default true when nil). Phase 73.
VSCodeEnabled   bool
// VSCodeSSHPubKey is the single-line OpenSSH ed25519 public key content for the
// operator's keypair, embedded into /home/sandbox/.ssh/authorized_keys at boot.
// Empty when VSCodeEnabled is false.
VSCodeSSHPubKey string
```

In `generateUserData` function (after the `SlackInboundEnabled` assignment, ~line 3177):
```go
// Phase 73: VS Code Remote-SSH access via SSM port-forward.
// Default true (nil = enabled). Requires caller to populate VSCodeSSHPubKey.
if isVSCodeEnabled(p.Spec.CLI) {
    params.VSCodeEnabled = true
    // VSCodeSSHPubKey is passed in via generateUserData's signature extension
    // OR via a pre-populated network extra field — see integration design below.
}
```

**IMPORTANT:** `generateUserData` currently has no parameter for `VSCodeSSHPubKey`. The planner must decide the injection point. Recommended approach: add a `vscodeSSHPubKey string` parameter to `generateUserData`, or pass it via `NetworkConfig` (which already carries operator-time values like `ArtifactsBucket`). The cleanest option matching existing patterns: add it to `NetworkConfig` so `create.go` can pass it without changing every `generateUserData` call signature.

**Alternative:** Pass it as an extra variadic `...string` parameter to `generateUserData`, similar to how `emailDomainOverride` is passed. This requires only the callers that supply it to change.

Userdata template block (placed after the mail-poller systemd unit and before the `systemctl daemon-reload` line, i.e., after line ~1740 and before line ~2288):

```bash
{{- if .VSCodeEnabled }}
# Phase 73: VS Code Remote-SSH access (sshd + authorized_keys + SELinux context)
systemctl daemon-reload
systemctl enable --now sshd
mkdir -p /home/sandbox/.ssh
chmod 700 /home/sandbox/.ssh
cat > /home/sandbox/.ssh/authorized_keys << 'KEY'
{{ .VSCodeSSHPubKey }}
KEY
chmod 600 /home/sandbox/.ssh/authorized_keys
chown -R sandbox:sandbox /home/sandbox/.ssh
command -v restorecon >/dev/null 2>&1 && restorecon -R -v /home/sandbox/.ssh || true
echo "[km-bootstrap] VS Code Remote-SSH configured (authorized_keys written)"
{{- end }}
```

Note: `daemon-reload` before `enable --now sshd` is defensive (matches commit `4030fce` precedent). `restorecon` is wrapped with `command -v` guard for non-SELinux distros (Ubuntu). On AL2023 (the primary target), `restorecon` is always available via `policycoreutils`.

### Pattern 6: `km vscode start` Flow

```
1. ResolveSandboxID(ctx, cfg, args[0])
2. os.Stat(~/.km/keys/sb-<id>) → fail fast if missing (helpful error)
3. sendSSMAndWait(ctx, ssmClient, instanceID, statusScript) → parse for "active" + "yes"
4. UpsertHost(~/.ssh/config, "km-sb-<id>", HostOptions{Port: localPort, ...})
5. Print connection block
6. buildPortForwardCmd(ctx, instanceID, region, strconv.Itoa(localPort), "22") → execFn(cmd)
```

Step 3 status script (combined, single SSM call):
```bash
echo "=== sshd ==="; systemctl is-active sshd 2>&1 || true
echo "=== authkeys exists ==="; test -f /home/sandbox/.ssh/authorized_keys && echo yes || echo no
echo "=== authkeys content ==="; cat /home/sandbox/.ssh/authorized_keys 2>/dev/null | head -1 || true
```

Output parsing: look for `=== sshd ===\nactive` and `=== authkeys exists ===\nyes`. If sshd is inactive AND authkeys is absent → "VS Code not enabled in profile (set `spec.cli.vscodeEnabled: true` and recreate)". If sshd is active but authkeys is absent → "Unexpected state: sshd is running but no authorized_keys found. Recreate the sandbox."

### Anti-Patterns to Avoid
- **Do not use `html/template`** in userdata.go — it's `text/template` and HTML-escaping would corrupt the pubkey content. (Already confirmed: `userdata.go` imports `text/template`.)
- **Do not shell out to `ssh-keygen`** for keypair generation. Use `crypto/ed25519` + `golang.org/x/crypto/ssh` directly.
- **Do not set `StrictHostKeyChecking yes`** in the managed block — the sandbox has a fresh host key each time; it would fail on reconnect.
- **Do not add the `vscode` command to `registerEBPFCmds`** — it is not a Linux-only command.
- **Do not use `os.Create` for atomic file writes to `~/.ssh/config`** — write to a temp file in the same directory, then `os.Rename` to avoid corruption on interrupted writes.

---

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| ed25519 keypair generation | Custom crypto | `crypto/ed25519.GenerateKey` + `golang.org/x/crypto/ssh.MarshalPrivateKey` | stdlib + x/crypto already in go.mod; handles OpenSSH PEM format correctly |
| SSM command dispatch | New SSM client | `sendSSMAndWait` in `agent.go:1067` | Already battle-tested with mock interface; reuse verbatim |
| SSM port-forward command | New exec.Cmd | `buildPortForwardCmd` in `shell.go:577` | Exact function with correct `--document-name`, `--parameters` format; verbatim reuse |
| Sandbox instance ID resolution | Custom fetch | `fetcher.FetchSandbox` + `extractResourceID(rec.Resources, ":instance/")` | Same 2-step pattern used by `shell.go:495-500` and `runPortForward:500` |

**Key insight:** The entire port-forward and SSM machinery is already proven. `km vscode start` is essentially `km shell --ports 2222:22` with a pre-flight key check and an ssh-config upsert. Reuse 100% of the port-forward primitive.

---

## Common Pitfalls

### Pitfall 1: `MarshalAuthorizedKey` Does Not Include Comment
**What goes wrong:** `gossh.MarshalAuthorizedKey(sshPub)` produces `ssh-ed25519 AAAA...\n` with NO comment field. If you pass this directly as `VSCodeSSHPubKey`, the `authorized_keys` file works (comment is optional for sshd) but the operator can't tell which key belongs to which sandbox when troubleshooting.
**Why it happens:** `MarshalAuthorizedKey` is designed for minimal output; comment is a separate concern.
**How to avoid:** Construct the pubkey line manually:
```go
pubLine := fmt.Sprintf("%s %s %s", sshPub.Type(), base64.StdEncoding.EncodeToString(sshPub.Marshal()), comment)
```
**Warning signs:** `grep -r "MarshalAuthorizedKey" pkg/sshkey` returns a line that doesn't also set a comment.

### Pitfall 2: `gossh.MarshalAuthorizedKey` Returns Bytes WITH Trailing Newline
**What goes wrong:** The returned `[]byte` ends with `\n`. If you embed `string(gossh.MarshalAuthorizedKey(sshPub))` directly into the heredoc template as `{{ .VSCodeSSHPubKey }}`, the resulting `authorized_keys` file has a blank line after the key.
**Why it happens:** `MarshalAuthorizedKey` appends `\n` by convention.
**How to avoid:** `strings.TrimRight(string(gossh.MarshalAuthorizedKey(sshPub)), "\n")` before storing in `VSCodeSSHPubKey`. Or use the manually-constructed line (Pitfall 1 fix) which has no trailing newline.

### Pitfall 3: `text/template` Template Variable Whitespace in Heredoc
**What goes wrong:** Go's `text/template` renders `{{ .VSCodeSSHPubKey }}` with no surrounding whitespace, but if the template indentation uses tabs or spaces before `{{ .VSCodeSSHPubKey }}`, those characters are emitted verbatim into the heredoc. The `authorized_keys` file then has leading spaces before the pubkey line, and sshd silently rejects it.
**Why it happens:** `text/template` does not strip surrounding whitespace in the template source.
**How to avoid:** Place `{{ .VSCodeSSHPubKey }}` at the start of the line (column 0) in the template string — no leading indentation:
```
cat > /home/sandbox/.ssh/authorized_keys << 'KEY'
{{ .VSCodeSSHPubKey }}
KEY
```
(Confirmed: this is the exact form in the CONTEXT.md spec.)

### Pitfall 4: Lambda-Side Stale km Binary
**What goes wrong:** Operator runs `km create --remote` after Phase 73 ships but before running `km init --sidecars`. The management Lambda's embedded km binary does not know about `VSCodeSSHPubKey`. The userdata template renders with an empty `VSCodeSSHPubKey` variable; the `authorized_keys` file contains a blank line; SSH key auth fails silently.
**Why it happens:** `km init --sidecars` uploads the km binary to S3 for the Lambda to download; without it, old binary runs.
**How to avoid:** Two defenses: (1) The `generateUserData` function should validate that `VSCodeSSHPubKey != ""` when `VSCodeEnabled == true` and return an error. (2) Document prominently: "After Phase 73 ships: `make build && km init --sidecars`."

### Pitfall 5: `restorecon` Absent on Non-AL2023 Substrates
**What goes wrong:** If an operator uses an Ubuntu AMI (`spec.runtime.ami: ubuntu-24.04`), `restorecon` is not present (it's part of `policycoreutils` which Ubuntu doesn't install by default). Without the defensive wrap, userdata fails.
**Why it happens:** The spec targets AL2023 but the codebase supports Ubuntu AMIs.
**How to avoid:** Wrap with `command -v restorecon >/dev/null 2>&1 && restorecon -R -v /home/sandbox/.ssh || true`. The `|| true` ensures the cloud-init script continues even on non-SELinux systems.

### Pitfall 6: `~/.ssh/config` Concurrent Write Race
**What goes wrong:** Two `km vscode start` calls running simultaneously (two different terminals, two sandboxes) both read and write `~/.ssh/config` without coordination, corrupting the file.
**Why it happens:** No file locking on `~/.ssh/config`.
**How to avoid:** Write to a temp file in the same directory (`os.CreateTemp(filepath.Dir(configPath), ".km-ssh-config-*")`) and then `os.Rename` (atomic on macOS/Linux). This doesn't prevent the read-modify-write race entirely but prevents partial-write corruption. For v1, this is sufficient — true locking would require advisory locks (`flock`). Document: concurrent `km vscode start` calls for different sandboxes may conflict; run sequentially if needed.

### Pitfall 7: `km destroy` Runs Cleanup Even When Sandbox Had vscodeEnabled: false
**What goes wrong:** `runDestroy` calls `RemoveHost` and key-file deletion unconditionally. For sandboxes created before Phase 73, there are no key files and no ssh-config entry. If `os.Remove` returns an error for missing files, the error bubbles up.
**Why it happens:** Cleanup should be unconditional for simplicity (no need to store vscodeEnabled in DDB metadata), but file-not-found is a normal state.
**How to avoid:** Use `os.IsNotExist` checks or `os.Remove`'s non-error return for missing files: check error and ignore if `os.IsNotExist(err)`. Same pattern as how the rest of destroy uses `log.Warn` for non-fatal errors.

### Pitfall 8: `sendSSMAndWait` for `km vscode start` Pre-flight Takes 3-5s
**What goes wrong:** The pre-flight SSM check before opening the port-forward adds 3-5 seconds of latency (SSM GetCommandInvocation polling starts at 1s, then 2s intervals). For a command operators run frequently, this is noticeable.
**Why it happens:** `sendSSMAndWait` has a 1s initial delay + poll loop.
**How to avoid:** Accept the latency for v1 (it's a safety check). Document: "The pre-flight check takes 3-5s. If sshd is known to be running, re-runs of `km vscode start` have no way to skip it in v1." Alternatively, allow `--skip-check` flag in v1 (add to deferred list).

---

## Code Examples

### Exact `pkg/sshkey.GenerateAndWrite` Implementation

```go
// Source: golang.org/x/crypto v0.49.0 ssh/keys.go lines 309-330, 1220-1224
// + crypto/ed25519 stdlib

package sshkey

import (
    "crypto/ed25519"
    cryptorand "crypto/rand"
    "encoding/base64"
    "encoding/pem"
    "fmt"
    "os"
    "path/filepath"

    gossh "golang.org/x/crypto/ssh"
)

// GenerateAndWrite generates an ed25519 keypair and writes:
//   - privPath: OpenSSH private key PEM (-----BEGIN OPENSSH PRIVATE KEY-----), mode 0600
//   - pubPath:  single-line authorized_keys format with comment, mode 0644
//
// Returns the pubkey line (no trailing newline) for embedding in userdata.
// Overwrites any existing files. Creates parent directories (mode 0700) as needed.
func GenerateAndWrite(privPath, pubPath, comment string) (pubContent string, err error) {
    if err := os.MkdirAll(filepath.Dir(privPath), 0700); err != nil {
        return "", fmt.Errorf("sshkey: create key directory: %w", err)
    }

    pub, priv, err := ed25519.GenerateKey(cryptorand.Reader)
    if err != nil {
        return "", fmt.Errorf("sshkey: generate ed25519 key: %w", err)
    }

    // Private key: OpenSSH PEM format with comment embedded
    privPEM, err := gossh.MarshalPrivateKey(priv, comment)
    if err != nil {
        return "", fmt.Errorf("sshkey: marshal private key: %w", err)
    }
    if err := os.WriteFile(privPath, pem.EncodeToMemory(privPEM), 0600); err != nil {
        return "", fmt.Errorf("sshkey: write private key: %w", err)
    }

    // Public key: "ssh-ed25519 <base64> <comment>" (no trailing newline in returned string)
    sshPub, err := gossh.NewPublicKey(pub)
    if err != nil {
        return "", fmt.Errorf("sshkey: new public key: %w", err)
    }
    pubLine := fmt.Sprintf("ssh-ed25519 %s %s",
        base64.StdEncoding.EncodeToString(sshPub.Marshal()),
        comment)
    if err := os.WriteFile(pubPath, []byte(pubLine+"\n"), 0644); err != nil {
        return "", fmt.Errorf("sshkey: write public key: %w", err)
    }

    return pubLine, nil
}
```

**Note on `sshPub.Marshal()` vs `gossh.MarshalAuthorizedKey`:** `sshPub.Marshal()` returns the raw wire bytes (without the type prefix or base64). `gossh.MarshalAuthorizedKey` returns `type base64\n` without comment. The construction above produces the correct `ssh-ed25519 <base64> <comment>` format for `authorized_keys`.

### `buildPortForwardCmd` Verbatim Reuse in `km vscode start`

```go
// Source: shell.go:577 — verbatim reuse, no modification needed
// In vscode.go runVSCodeStart:
localPortStr := strconv.Itoa(localPort)  // default 2222
pfCmd := buildPortForwardCmd(ctx, instanceID, region, localPortStr, "22")
pfCmd.Stdin = os.Stdin
pfCmd.Stdout = os.Stdout
pfCmd.Stderr = os.Stderr
return execFn(pfCmd)
```

`buildPortForwardCmd` is an unexported function in package `cmd`. Since `vscode.go` is in the same package, it can call it directly without any export or adapter.

### `sendSSMAndWait` for Status Check

```go
// Source: agent.go:1067 — same interface, same call pattern
statusScript := strings.Join([]string{
    `echo "=== sshd ==="`,
    `systemctl is-active sshd 2>&1 || true`,
    `echo "=== authkeys exists ==="`,
    `test -f /home/sandbox/.ssh/authorized_keys && echo yes || echo no`,
    `echo "=== authkeys content ==="`,
    `cat /home/sandbox/.ssh/authorized_keys 2>/dev/null | head -1 || true`,
}, "\n")
output, err := sendSSMAndWait(ctx, ssmClient, instanceID, statusScript)
```

Parse result:
```go
sshdActive := strings.Contains(output, "=== sshd ===\nactive")
authkeysPresent := strings.Contains(output, "=== authkeys exists ===\nyes")
```

### `CLISpec.VSCodeEnabled` Addition

```go
// Source: types.go — insert after line 446 (NotifySlackTranscriptEnabled field)
// Mirrors NotifyEmailEnabled *bool `yaml:"notifyEmailEnabled,omitempty"` at line 403

// VSCodeEnabled gates the cloud-init block that enables sshd and writes the operator's
// ed25519 pubkey to /home/sandbox/.ssh/authorized_keys. Default true (nil = enabled).
// Set false to skip SSH provisioning entirely (e.g., for sandboxes that don't need VS Code).
// Schema addition requires km init --sidecars after rebuild.
VSCodeEnabled *bool `yaml:"vscodeEnabled,omitempty" json:"vscodeEnabled,omitempty"`
```

### JSON Schema Addition

```json
// Source: pkg/profile/schemas/sandbox_profile.schema.json
// Insert after "notifySlackTranscriptEnabled" block (line ~543), before closing `}`

"vscodeEnabled": {
  "type": "boolean",
  "description": "Phase 73: enable sshd + authorized_keys provisioning for VS Code Remote-SSH via SSM port-forward. Default true (nil = enabled). Set false to skip SSH provisioning."
}
```

### `km destroy` Cleanup Additions

```go
// Source: mirrors runDestroyDocker's homeDir pattern (destroy.go:579) and
// localnumber.Remove idempotent pattern (destroy.go:560)
// Insert AFTER "Sandbox N destroyed successfully" print, or just before it:

homeDir, _ := os.UserHomeDir()
keysDir := filepath.Join(homeDir, ".km", "keys")

// Phase 73: remove ssh-config entry and key files (idempotent — no error if missing)
sshConfigPath := filepath.Join(homeDir, ".ssh", "config")
if rmErr := RemoveHost(sshConfigPath, "km-"+sandboxID); rmErr != nil {
    log.Warn().Err(rmErr).Str("sandbox_id", sandboxID).
        Msg("failed to remove ssh-config entry for vs-code (non-fatal)")
}
for _, keyFile := range []string{
    filepath.Join(keysDir, sandboxID),
    filepath.Join(keysDir, sandboxID+".pub"),
} {
    if err := os.Remove(keyFile); err != nil && !os.IsNotExist(err) {
        log.Warn().Err(err).Str("file", keyFile).
            Msg("failed to remove vscode key file (non-fatal)")
    }
}
```

Note: The Host alias in `~/.ssh/config` is `km-sb-<id>` (i.e., `"km-"+sandboxID` where sandboxID is e.g. `sb-abc123`). Double-check: CONTEXT.md says `Host km-sb-abc123` → the alias is `km-` + sandboxID.

### `create.go` Integration Point

Keypair generation happens in `runCreate` after the sandbox ID is generated (Step 4) and before `compiler.Compile` (Step 7). For the local path:

```go
// Phase 73: generate VS Code SSH keypair if vscodeEnabled (default true)
var vscodeSSHPubKey string
if isVSCodeEnabled(resolvedProfile.Spec.CLI) {
    homeDir, _ := os.UserHomeDir()
    privPath := filepath.Join(homeDir, ".km", "keys", sandboxID)
    pubPath := privPath + ".pub"
    pubKey, keyErr := sshkey.GenerateAndWrite(privPath, pubPath, "km-"+sandboxID)
    if keyErr != nil {
        return fmt.Errorf("generate vscode ssh keypair: %w", keyErr)
    }
    vscodeSSHPubKey = pubKey
    fmt.Fprintf(os.Stderr, "  ✓ VS Code keypair written to %s\n", privPath)
}
```

The `vscodeSSHPubKey` value then needs to reach `generateUserData`. The recommended approach is to add a `VSCodeSSHPubKey string` field to `NetworkConfig` (in `pkg/compiler/compiler.go`), set it in `create.go` before `compiler.Compile`, and populate `params.VSCodeSSHPubKey` inside `generateUserData` from the network struct.

For the **remote create** path (`runCreateRemote`): the keypair is generated in the same location (after sandbox ID generation, before compilation), and `vscodeSSHPubKey` is already embedded in `artifacts.UserData` by the time S3 upload happens (because `compiler.Compile` calls `generateUserData` which bakes it into the userdata script). The Lambda does NOT need to know about it separately — the compiled userdata already contains the pubkey. This is the key insight: unlike Slack's runtime SSM fetch, the pubkey is compile-time static. No Lambda-side changes needed beyond having the updated km binary (via `km init --sidecars`).

---

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| VS Code Web (`code serve-web`) | VS Code Remote-SSH over SSM port-forward | 2026-05-06 (POC rejection) | Operators use their full local IDE; no browser dependency |
| Manual `ssh-keygen` invocation | `crypto/ed25519` + `golang.org/x/crypto/ssh` native Go | Phase 73 (new) | No external binary dependency; fully testable; runs in Lambda |

**Deprecated/outdated:**
- `code serve-web`: rejected after POC. Do not reference in docs or implementation.
- `code tunnel`: bypasses SSM security boundary. Not considered.

---

## Open Questions

1. **`NetworkConfig` vs extra variadic parameter for `VSCodeSSHPubKey` injection into `generateUserData`**
   - What we know: `generateUserData` already uses a variadic `...string` for `emailDomainOverride` (1-2 elements). Adding another variadic is possible but fragile.
   - What's unclear: Whether adding `VSCodeSSHPubKey` to `NetworkConfig` has unintended side effects on ECS/Docker compilation paths that also call `generateUserData`.
   - Recommendation: Add `VSCodeSSHPubKey string` to `NetworkConfig` (it's already the "operator-time config bundle"); ECS/Docker paths pass it as empty string and the template gate `{{- if .VSCodeEnabled }}` prevents it from being used. Only EC2 path uses it.

2. **`isVSCodeEnabled` helper location**
   - What we know: Could live in `pkg/profile/types.go` (alongside CLISpec) or `pkg/compiler/userdata.go` (alongside the template logic).
   - What's unclear: Whether other consumers (validate.go, future km doctor) need it.
   - Recommendation: Put it in `pkg/profile/types.go` as a package-level function (exported as `IsVSCodeEnabled(cli *CLISpec) bool`), mirroring `ClaudeTelemetrySpec.IsEnabled()`.

3. **Exact position of vscode userdata block in the 3000-line template**
   - What we know: It should be near `SlackInboundEnabled` block (around line 1207/1718) and BEFORE `systemctl daemon-reload` + `systemctl enable` lines (~2289-2297).
   - What's unclear: Whether to put it before or after the mail-poller/slack-inbound systemd unit definitions.
   - Recommendation: Place immediately after the `{{- end }}` closing the `SlackInboundEnabled` block at line 1740, before the `km-recv` heredoc. This groups all "optional service enablement" blocks together and ensures `systemctl enable --now sshd` runs BEFORE the final `systemctl daemon-reload` + bulk `systemctl enable` line.

---

## Validation Architecture

> `workflow.nyquist_validation` is not explicitly set to false in `.planning/config.json` — validation section included.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go testing (`testing` package) |
| Config file | none — `go test ./...` from repo root |
| Quick run command | `go test ./pkg/sshkey/... ./internal/app/cmd/ -run TestVSCode -timeout 30s` |
| Full suite command | `go test ./...` |

### Phase Requirements → Test Map
| Layer | Behavior | Test Type | Automated Command | File Exists? |
|-------|----------|-----------|-------------------|-------------|
| `pkg/sshkey` | GenerateAndWrite creates privkey mode 0600 | unit | `go test ./pkg/sshkey/... -run TestGenerateAndWrite_ModePriv -v` | ❌ Wave 0 |
| `pkg/sshkey` | GenerateAndWrite creates pubkey mode 0644 | unit | `go test ./pkg/sshkey/... -run TestGenerateAndWrite_ModePub -v` | ❌ Wave 0 |
| `pkg/sshkey` | GenerateAndWrite pubkey parses back via ssh.ParseAuthorizedKey | unit | `go test ./pkg/sshkey/... -run TestGenerateAndWrite_PubKeyParses -v` | ❌ Wave 0 |
| `pkg/sshkey` | GenerateAndWrite idempotency (second call overwrites) | unit | `go test ./pkg/sshkey/... -run TestGenerateAndWrite_Idempotent -v` | ❌ Wave 0 |
| `pkg/sshkey` | GenerateAndWrite creates parent dir with 0700 | unit | `go test ./pkg/sshkey/... -run TestGenerateAndWrite_CreatesParentDir -v` | ❌ Wave 0 |
| `internal/app/cmd/sshconfig` | UpsertHost creates file if absent | unit | `go test ./internal/app/cmd/ -run TestSSHConfig_UpsertCreates -v` | ❌ Wave 0 |
| `internal/app/cmd/sshconfig` | UpsertHost appends markers if absent | unit | `go test ./internal/app/cmd/ -run TestSSHConfig_UpsertAppendsMarkers -v` | ❌ Wave 0 |
| `internal/app/cmd/sshconfig` | UpsertHost replaces existing entry | unit | `go test ./internal/app/cmd/ -run TestSSHConfig_UpsertReplaces -v` | ❌ Wave 0 |
| `internal/app/cmd/sshconfig` | RemoveHost removes entry, preserves others | unit | `go test ./internal/app/cmd/ -run TestSSHConfig_RemovePreservesOthers -v` | ❌ Wave 0 |
| `internal/app/cmd/sshconfig` | RemoveHost removes markers when last entry removed | unit | `go test ./internal/app/cmd/ -run TestSSHConfig_RemoveCleansMarkers -v` | ❌ Wave 0 |
| `internal/app/cmd/sshconfig` | RemoveHost idempotent on missing entry | unit | `go test ./internal/app/cmd/ -run TestSSHConfig_RemoveIdempotent -v` | ❌ Wave 0 |
| `internal/app/cmd/sshconfig` | Content outside markers preserved | unit | `go test ./internal/app/cmd/ -run TestSSHConfig_PreservesOutsideMarkers -v` | ❌ Wave 0 |
| `pkg/profile` | VSCodeEnabled nil defaults true via IsVSCodeEnabled | unit | `go test ./pkg/profile/... -run TestVSCodeEnabled_DefaultTrue -v` | ❌ Wave 0 |
| `pkg/profile` | VSCodeEnabled false skips (IsVSCodeEnabled returns false) | unit | `go test ./pkg/profile/... -run TestVSCodeEnabled_False -v` | ❌ Wave 0 |
| `pkg/compiler` | Userdata contains sshd block when VSCodeEnabled true | unit | `go test ./pkg/compiler/... -run TestUserDataVSCodeEnabled -v` | ❌ Wave 0 |
| `pkg/compiler` | Userdata omits sshd block when VSCodeEnabled false | unit | `go test ./pkg/compiler/... -run TestUserDataVSCodeDisabled -v` | ❌ Wave 0 |
| `pkg/compiler` | Userdata embeds pubkey content correctly | unit | `go test ./pkg/compiler/... -run TestUserDataVSCodePubKey -v` | ❌ Wave 0 |
| `internal/app/cmd/vscode` | start fails with helpful error when private key missing | unit | `go test ./internal/app/cmd/ -run TestVSCodeStart_MissingKey -v` | ❌ Wave 0 |
| `internal/app/cmd/vscode` | start invokes buildPortForwardCmd with correct args | unit | `go test ./internal/app/cmd/ -run TestVSCodeStart_PortForwardArgs -v` | ❌ Wave 0 |
| `internal/app/cmd/vscode` | start prints correct connection instructions | unit | `go test ./internal/app/cmd/ -run TestVSCodeStart_Output -v` | ❌ Wave 0 |
| `internal/app/cmd/vscode` | status returns non-zero when sshd inactive | unit | `go test ./internal/app/cmd/ -run TestVSCodeStatus_SSHDInactive -v` | ❌ Wave 0 |
| `internal/app/cmd/vscode` | status returns clean error for pre-Phase-73 sandbox | unit | `go test ./internal/app/cmd/ -run TestVSCodeStatus_PrePhase73 -v` | ❌ Wave 0 |
| Manual smoke | km vscode start connects real sandbox, VS Code opens /workspace | e2e/manual | n/a | n/a — manual only |

### Sampling Rate
- **Per task commit:** `go test ./pkg/sshkey/... ./internal/app/cmd/ -run 'TestSSHConfig|TestVSCode|TestGenerateAndWrite' -timeout 30s`
- **Per wave merge:** `go test ./pkg/sshkey/... ./internal/app/cmd/... ./pkg/profile/... ./pkg/compiler/... -timeout 60s`
- **Phase gate:** `go test ./...` green before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `pkg/sshkey/keygen_test.go` — covers all GenerateAndWrite cases
- [ ] `pkg/sshkey/keygen.go` — stub (compiles, returns error "not implemented")
- [ ] `internal/app/cmd/sshconfig_test.go` — covers all UpsertHost/RemoveHost cases
- [ ] `internal/app/cmd/sshconfig.go` — stub (compiles, returns error "not implemented")
- [ ] `internal/app/cmd/vscode_test.go` — covers start/status command construction
- [ ] `internal/app/cmd/vscode.go` — stub with subcommands registered (compiles, RunE returns nil)
- [ ] `pkg/profile/types_test.go` (existing) — add `TestVSCodeEnabled_*` test cases
- [ ] `pkg/compiler/userdata_test.go` (existing) — add `TestUserDataVSCode*` test cases

*(No new framework install needed — `go test` is already the project test runner.)*

---

## Sources

### Primary (HIGH confidence)
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/shell.go:473-584` — `runPortForward`, `parsePortSpecs`, `buildPortForwardCmd` — verbatim verified
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/agent.go:29-39` — `SSMSendAPI` interface; `sendSSMAndWait` at line 1067 — verbatim verified
- `/Users/khundeck/working/klankrmkr/pkg/profile/types.go:354-447` — `CLISpec` struct, `NotifyEmailEnabled *bool`, `SlackArchiveOnDestroy *bool` patterns — verbatim verified
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go:2766-2885` — `userDataParams` struct fields — verbatim verified
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go:1207,1718` — `{{- if .SlackInboundEnabled }}` conditional blocks — insertion point verified
- `/Users/khundeck/working/klankrmkr/pkg/compiler/userdata.go:2289-2297` — `systemctl daemon-reload` + bulk enable lines — placement constraint verified
- `/Users/khundeck/working/klankrmkr/pkg/compiler/compiler.go:87` — `Compile` function signature; `NetworkConfig` struct usage — verbatim verified
- `/Users/khundeck/go/pkg/mod/golang.org/x/crypto@v0.49.0/ssh/keys.go:309-330,1209-1228` — `MarshalAuthorizedKey`, `MarshalPrivateKey`, `NewPublicKey` function signatures — verbatim verified from module cache
- `/Users/khundeck/working/klankrmkr/go.mod` — `golang.org/x/crypto v0.49.0` already present — confirmed
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/root.go:84-85` — `NewAMICmd` / `NewSlackCmd` registration pattern — verbatim verified
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/slack.go:111-132` — `NewSlackCmdWithDeps` / `newSlackCmdInternal` testable pattern — verbatim verified
- `/Users/khundeck/working/klankrmkr/pkg/profile/schemas/sandbox_profile.schema.json:517-543` — `notifySlack*` schema entry pattern — verbatim verified
- `/Users/khundeck/working/klankrmkr/internal/app/cmd/destroy.go:560-565` — destroy final cleanup and `localnumber.Remove` pattern — verbatim verified
- `/Users/khundeck/working/klankrmkr/.planning/phases/73-km-vscode-remote-session-via-ssm/73-CONTEXT.md` — locked design decisions — primary spec

### Secondary (MEDIUM confidence)
- `cmd/km-slack/main.go:21` and test files — existing `crypto/ed25519` usage pattern in codebase (confirms import path convention)
- `docs/superpowers/specs/2026-05-06-km-vscode-design.md` — full spec with ownership split and testing strategy

### Tertiary (LOW confidence)
- None — all critical claims verified from source code or module cache.

---

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all packages verified in go.mod and module cache; function signatures read from source
- Architecture: HIGH — all patterns verified by reading existing code (shell.go, agent.go, slack.go, types.go, userdata.go)
- Pitfalls: HIGH — POC lessons explicitly documented in CONTEXT.md; code-level pitfalls verified by reading the relevant functions
- Test patterns: HIGH — existing test files read and patterns confirmed

**Research date:** 2026-05-06
**Valid until:** 2026-06-06 (stable Go stdlib + x/crypto; low churn domain)
