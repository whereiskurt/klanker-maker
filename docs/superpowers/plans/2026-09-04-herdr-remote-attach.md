# `km herdr` Remote Attach Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `km herdr start|status`, a `base/tools/herdr` profile fragment fed from an S3-mirrored Herdr release, and km-presence signal 8, so agent panes survive operator detach without the idle reaper stopping the box mid-run.

**Architecture:** `km herdr` is a thin sibling of `km vscode` — same SSM→sshd transport, same per-sandbox ed25519 keypair, same `~/.ssh/config` entry — differing only in pre-flight (herdr binary present) and banner. The Herdr binary reaches the box from `s3://{bucket}/binaries/herdr` (the `fetchAndUploadSops` pattern), pulled either at boot by a profile fragment or on demand by `km herdr start`. km-presence gains an eighth signal that asks Herdr's own socket whether any pane is doing work.

**Tech Stack:** Go 1.x, cobra, AWS SDK v2 (SSM, EC2, S3), `golang.org/x/crypto/ssh`, Herdr v0.8.2 (newline-delimited JSON over a unix socket).

**Spec:** `docs/superpowers/specs/2026-09-04-herdr-remote-attach-design.md`

## Global Constraints

- **Herdr version pin: `0.8.2`.** Asset `herdr-linux-x86_64` from `github.com/herdrdev/herdr` releases. The release publishes **no checksum file**, so the version pin is the entire supply-chain control.
- **S3 key contract: `binaries/herdr`.** This exact literal appears in three places (mirror, fragment, ensure-install) and is pinned by a test in Task 1.
- **On-box install path: `/usr/local/bin/herdr`, mode `0755`, root-owned.** Not `~/.local/bin/herdr`.
- **Go module path:** `github.com/whereiskurt/klanker-maker`.
- **AWS profile literal:** `"klanker-terraform"` — the project-wide convention in `init.go`; do not introduce a config helper here.
- **Test package for `internal/app/cmd`:** `package cmd` (internal tests; `parseVSCodeStatus` and friends are unexported).
- **`km-presence` runs as `User=root`** and shells out only through the `commandRunner` seam (`Output(name string, args ...string)`), which has **no env support** — pass configuration as arguments, do not widen the seam.
- **Every km-presence signal fails idle.** Any error, missing binary, absent socket, or malformed output returns `false`. A signal that can never go negative silently disables idle teardown fleet-wide.
- **`runuser`, never `sudo`/`su`,** for dropping to the `sandbox` user (matches Phase 132's 15 dispatch sites).
- **Deploy surface for the whole plan is `make build` + `km init --sidecars`.** No Terraform module, IAM policy, DynamoDB table, Lambda, or `pkg/compiler/userdata.go` template change. **Never run `km init --dry-run=false` for this work.**
- **`km init --sidecars` needs `build/*.zip` present, and fails AFTER seeding herdr if they are absent.** Its last step, `uploadCreateHandlerToolchain`, tars `build/budget-enforcer.zip` and friends into `toolchain/infra.tar.gz`; those come from `make build-lambdas`, which a fresh worktree has never run. The command then exits **non-zero** even though every sidecar, `binaries/sops`, `binaries/herdr`, and `toolchain/{km,terraform,terragrunt}` uploaded successfully — verified live on 2026-09-04. This is safe (the failing step leaves the previous `infra.tar.gz` in place rather than writing a partial one) but the red exit is misleading. If you only need the herdr seed, `grep 'Uploaded herdr'` in the output is the check that matters; run `make build-lambdas` first if you want a clean exit.
- **`km init --sidecars` uploads EVERY sidecar from the working tree, not just herdr's.** Since Phase 133 that set includes `km-secretsd` and `km-env`, which the deployed install already depends on. Running it from a tree behind `origin/main` rolls those binaries backward on a live install. **Always `git pull --rebase` and confirm `git log -1 origin/main` matches `HEAD` before any `km init --sidecars` in this plan** (Task 6 Step 1 and Task 9 Step 1).
- **Commit scoping:** another agent may be working in this repo concurrently. Every `git add` in this plan names explicit paths. Never run a bare `git add`. Before starting, run `git pull --rebase` and re-run the test suite.
- **Check `go test` exit status directly**, not the exit status of a pipe into `tail`/`head`.

---

### Task 1: Mirror the Herdr release to S3

**Files:**
- Modify: `internal/app/cmd/init.go` (add `herdrVersion` const beside `sopsVersion`; add `fetchAndUploadHerdr`; call it from `buildAndUploadSidecars`)
- Test: `internal/app/cmd/herdr_mirror_test.go` (create)

**Interfaces:**
- Consumes: nothing.
- Produces: `func FetchAndUploadHerdr(buildDir, bucket string) error` (exported test wrapper) and `func fetchAndUploadHerdr(buildDir, bucket string) error`; the const `herdrVersion = "0.8.2"`; and the S3 key literal `binaries/herdr`, which Tasks 2 and 3 both depend on.

- [ ] **Step 1: Write the failing test**

Create `internal/app/cmd/herdr_mirror_test.go`:

```go
// Package cmd — herdr_mirror_test.go
// Pins the binaries/herdr S3 key contract across every site that uses it.
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHerdrS3Key_PinnedAcrossAllSites is the mechanical pairing guard. The S3
// key is written by init.go, read by the profile fragment, and read again by
// the ensure-install path in herdr.go. A rename in one place and not the others
// produces a sandbox that boots without herdr and an install that 404s, both
// silently. Compare against a single literal here rather than trusting three
// separate string constants to stay in sync.
func TestHerdrS3Key_PinnedAcrossAllSites(t *testing.T) {
	const key = "binaries/herdr"

	sites := []string{
		"init.go",                          // fetchAndUploadHerdr upload target
		"herdr.go",                         // ensure-install download source
		"../../../profiles/base/tools/herdr.yaml", // boot-time fetch
	}
	for _, rel := range sites {
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(raw), key) {
			t.Errorf("%s does not contain the S3 key %q", rel, key)
		}
	}
}

// TestHerdrVersion_Pinned asserts the version constant is a concrete pin, not
// "latest". An unpinned third-party binary means a bad upstream release reaches
// every new sandbox at once — the same reasoning as base/security/wiz.yaml's
// WIZ_SENSOR_VERSION comment.
func TestHerdrVersion_Pinned(t *testing.T) {
	if herdrVersion == "" || herdrVersion == "latest" {
		t.Fatalf("herdrVersion = %q; want a concrete version pin", herdrVersion)
	}
}

// TestFetchAndUploadHerdr_SkipsDownloadWhenCached asserts the cached-binary
// branch is taken when build/herdr already exists, so a repeated km init does
// not re-download. The upload still runs and will fail without AWS creds, so
// this asserts only that the error is NOT a download error.
func TestFetchAndUploadHerdr_SkipsDownloadWhenCached(t *testing.T) {
	dir := t.TempDir()
	cached := filepath.Join(dir, "herdr")
	if err := os.WriteFile(cached, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("seed cached binary: %v", err)
	}
	err := fetchAndUploadHerdr(dir, "km-artifacts-test-does-not-exist")
	if err != nil && strings.Contains(err.Error(), "download herdr") {
		t.Fatalf("download was attempted despite cached binary: %v", err)
	}
	// The cached file must survive a failed upload — deleting a cached artifact
	// on the failure path is the buildLambdaZips defect (see CLAUDE.md Phase 126
	// operator-image findings) pointed at a different file.
	if _, statErr := os.Stat(cached); statErr != nil {
		t.Fatalf("cached binary was removed on the upload-failure path: %v", statErr)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/cmd/ -run 'TestHerdr|TestFetchAndUploadHerdr' -v`
Expected: FAIL to compile — `undefined: herdrVersion`, `undefined: fetchAndUploadHerdr`, and `read herdr.go: no such file or directory`.

- [ ] **Step 3: Add the version constant and the mirror function**

In `internal/app/cmd/init.go`, immediately after the existing `const sopsVersion = "3.13.1"`:

```go
// herdrVersion pins the Herdr release mirrored to s3://{bucket}/binaries/herdr.
//
// The release publishes bare per-platform binaries and NO checksum file, so
// there is no published digest to verify against — this pin is the entire
// supply-chain control. Bump it deliberately, like any other dependency.
const herdrVersion = "0.8.2"
```

Then, after `fetchAndUploadSops`:

```go
// FetchAndUploadHerdr downloads herdr v{herdrVersion} linux/amd64 (cached in
// build/) and uploads it to s3://{bucket}/binaries/herdr.
//
// Exported so it can be tested from the _test package (cmd_test).
func FetchAndUploadHerdr(buildDir, bucket string) error {
	return fetchAndUploadHerdr(buildDir, bucket)
}

// fetchAndUploadHerdr is the internal implementation — see FetchAndUploadHerdr.
//
// Mirrors fetchAndUploadSops. Herdr ships a bare binary rather than a tarball,
// so there is no extraction step.
func fetchAndUploadHerdr(buildDir, bucket string) error {
	binaryPath := filepath.Join(buildDir, "herdr")
	if _, err := os.Stat(binaryPath); err == nil {
		fmt.Printf("  herdr already in %s (skip download)\n", binaryPath)
	} else {
		url := fmt.Sprintf("https://github.com/herdrdev/herdr/releases/download/v%s/herdr-linux-x86_64",
			herdrVersion)
		fmt.Printf("  Downloading herdr v%s...\n", herdrVersion)
		dlCmd := exec.Command("curl", "-fsSL", url, "-o", binaryPath)
		if out, err := dlCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("download herdr: %s: %w", string(out), err)
		}
		if err := os.Chmod(binaryPath, 0o755); err != nil {
			return fmt.Errorf("chmod herdr: %w", err)
		}
	}
	s3Key := "binaries/herdr"
	fmt.Printf("  Uploading herdr to s3://%s/%s...\n", bucket, s3Key)
	uploadCmd := exec.Command("aws", "s3", "cp", binaryPath,
		fmt.Sprintf("s3://%s/%s", bucket, s3Key),
		"--profile", "klanker-terraform")
	if out, err := uploadCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("upload herdr: %s: %w", string(out), err)
	}
	fmt.Printf("  Uploaded herdr\n")
	return nil
}
```

- [ ] **Step 4: Wire it into `buildAndUploadSidecars`**

In `internal/app/cmd/init.go`, immediately after the `fetchAndUploadSops` call block, add:

```go
	// Fetch and upload the herdr binary for km herdr / base/tools/herdr.
	// Non-fatal, unlike sops: a missing sops aborts boot because secrets are
	// load-bearing, whereas a missing herdr costs one `km herdr start`, which
	// re-ensures the binary over SSM anyway.
	if err := fetchAndUploadHerdr(buildDir, bucket); err != nil {
		fmt.Printf("  [warn] herdr upload failed: %v\n", err)
	}
```

- [ ] **Step 5: Create the fragment and command stubs the pinning test reads**

The pinning test reads three files. Create the two that do not exist yet so the test can go green; Tasks 2 and 3 fill them in.

```bash
mkdir -p profiles/base/tools
printf 'apiVersion: klankermaker.ai/v1alpha2\nkind: SandboxProfile\nmetadata:\n  name: base-tools-herdr\n  abstract: true\nspec:\n  execution:\n    initCommandsAppend:\n      - "true # binaries/herdr — filled in by Task 2"\n' > profiles/base/tools/herdr.yaml
printf '// Package cmd — herdr.go\n// km herdr: see docs/superpowers/specs/2026-09-04-herdr-remote-attach-design.md\npackage cmd\n\n// herdrS3Key is the single S3 key contract shared with base/tools/herdr.yaml\n// and fetchAndUploadHerdr. Pinned by TestHerdrS3Key_PinnedAcrossAllSites.\nconst herdrS3Key = "binaries/herdr"\n' > internal/app/cmd/herdr.go
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/app/cmd/ -run 'TestHerdr|TestFetchAndUploadHerdr' -v; echo "exit=$?"`
Expected: PASS, `exit=0`.

- [ ] **Step 7: Commit**

```bash
git add internal/app/cmd/init.go internal/app/cmd/herdr.go internal/app/cmd/herdr_mirror_test.go profiles/base/tools/herdr.yaml
git commit -m "feat(herdr): mirror pinned herdr release to s3 binaries/herdr"
```

---

### Task 2: `base/tools/herdr` profile fragment

**Files:**
- Modify: `profiles/base/tools/herdr.yaml` (created as a stub in Task 1)
- Test: `pkg/compiler/userdata_herdr_fragment_test.go` (create)

**Interfaces:**
- Consumes: the `binaries/herdr` S3 key from Task 1.
- Produces: an abstract fragment named `base-tools-herdr` that leaf profiles reach via `extends: [base/tools/herdr]`. No Go symbols.

- [ ] **Step 1: Write the guard test**

Create `pkg/compiler/userdata_herdr_fragment_test.go`:

```go
package compiler

import (
	"strings"
	"testing"
)

// herdrFetchLine is the initCommand the base/tools/herdr fragment appends.
const herdrFetchLine = `aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/binaries/herdr" /usr/local/bin/herdr`

// profileDumpSection returns the body of the Phase 113 /opt/km/.km-profile.yaml
// heredoc — the yaml.Marshal of userDataParams that userdata.go renders via
// {{ .ProfileYAML }}. Returns ok=false if the block is absent.
func profileDumpSection(ud string) (string, bool) {
	const open = "cat > /opt/km/.km-profile.yaml << 'KM_PROFILE_EOF'\n"
	i := strings.Index(ud, open)
	if i < 0 {
		return "", false
	}
	rest := ud[i+len(open):]
	j := strings.Index(rest, "\nKM_PROFILE_EOF")
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}

// TestInitCommandsAppend_DeltaConfinedToProfileDump is the guard the herdr
// fragment's deploy story rests on.
//
// Adding an initCommand DOES change rendered userdata — Phase 113 sets
// params.ProfileYAML to a yaml.Marshal of the whole userDataParams struct
// (userdata.go:7070) and renders it verbatim into the /opt/km/.km-profile.yaml
// heredoc. Because that happens by reflection, no template token names
// InitCommands and grepping for it cannot find the reference. An earlier
// version of this test asserted the two renders were byte-identical; that was
// false and it failed, which is how the interaction was found.
//
// What actually matters is narrower and stronger: the delta must be CONFINED to
// that profile dump, so no executable bootstrap logic changes. If a future edit
// ever leaks initCommands into runnable script, this test fails.
func TestInitCommandsAppend_DeltaConfinedToProfileDump(t *testing.T) {
	base := baseProfile()
	base.Spec.Execution.InitCommands = []string{"echo one"}

	withHerdr := baseProfile()
	withHerdr.Spec.Execution.InitCommands = []string{"echo one", herdrFetchLine}

	a, err := generateUserData(base, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("render base: %v", err)
	}
	b, err := generateUserData(withHerdr, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("render with herdr: %v", err)
	}

	dumpA, okA := profileDumpSection(a)
	dumpB, okB := profileDumpSection(b)
	if !okA || !okB {
		t.Fatal("the Phase 113 /opt/km/.km-profile.yaml block is absent from rendered userdata; " +
			"this test would otherwise pass vacuously")
	}

	// The fetch line must appear in the dump...
	if !strings.Contains(dumpB, "binaries/herdr") {
		t.Errorf("expected the herdr fetch line inside the profile dump; it was not there")
	}
	// ...and NOWHERE else in the rendered bootstrap.
	outsideB := strings.Replace(b, dumpB, "", 1)
	if strings.Contains(outsideB, "binaries/herdr") {
		t.Errorf("the herdr initCommand leaked OUTSIDE the profile dump into executable bootstrap")
	}

	// The decisive assertion: strip each render's dump and the remaining
	// bootstrap must be byte-identical.
	outsideA := strings.Replace(a, dumpA, "", 1)
	if outsideA != outsideB {
		t.Fatalf("adding an initCommand changed executable bootstrap outside the Phase 113 " +
			"profile dump; the fragment's no-create-handler-rebuild claim needs re-examining")
	}
}

// TestInitCommandsGate_EmptyToNonEmptyDoesChangeUserdata is the negative
// control. It proves the test above measures something real: going from zero
// commands to one flips the {{- if or .InitCommands .InitScripts }} presence
// gate and adds the km-init.sh download block to executable bootstrap.
func TestInitCommandsGate_EmptyToNonEmptyDoesChangeUserdata(t *testing.T) {
	none := baseProfile()
	none.Spec.Execution.InitCommands = nil

	one := baseProfile()
	one.Spec.Execution.InitCommands = []string{"echo one"}

	a, err := generateUserData(none, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("render none: %v", err)
	}
	b, err := generateUserData(one, "test-sb", nil, "my-bucket", false, nil)
	if err != nil {
		t.Fatalf("render one: %v", err)
	}
	if a == b {
		t.Fatal("expected the InitCommands presence gate to change userdata; it did not")
	}
}
```

`baseProfile()` and `generateUserData` are the existing helpers used by
`TestUserdataAdditionalVolumeOnly_GoldenByteIdentical` in the same package. Do not add new ones.

- [ ] **Step 2: Run the guard tests**

Run: `go test ./pkg/compiler/ -run 'TestInitCommands' -v; echo "exit=$?"`
Expected: both PASS. These guard existing compiler behaviour rather than driving new behaviour.

A failure of `TestInitCommandsAppend_DeltaConfinedToProfileDump` means an initCommand now reaches
executable bootstrap somewhere, which would genuinely change the deploy surface — stop and report
BLOCKED. A failure of the negative control means the presence gate is gone.


- [ ] **Step 3: Write the real fragment**

Replace `profiles/base/tools/herdr.yaml` entirely:

```yaml
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: base-tools-herdr
  abstract: true

# Herdr — persistent terminal multiplexer for agent panes.
#
# Opt in with `extends: [base/tools/herdr]`. See docs/herdr-remote-attach.md
# and `km herdr start`.
#
# The binary comes from S3 over the instance role, NOT from herdr.dev. That is
# the decisive difference from base/security/wiz.yaml, which needs a
# ".wiz.io" allowedDNSSuffixes entry because its installer downloads over the
# public internet — and DNS is the one enforcement layer root does NOT bypass
# (userdata.go rewrites /etc/resolv.conf to 127.0.0.1 for the whole box, and the
# eBPF resolver NXDOMAINs non-allowlisted names with no uid or cgroup check).
# Fetching from S3 keeps this fragment free of any egress change at all.
#
# Deliberately absent:
#   spec.network  — no allowlist entry is needed; see above.
#   spec.runtime  — Phase 117 bool zero-value trap; mixed-bool blocks stay in leaves.
#   a systemd unit — the herdr server starts itself on first attach. A unit would
#                 run a server nobody is attached to and defeat km-presence
#                 signal 8's negative case, latching the box awake forever.
#   pane_history  — Herdr's scrollback persistence writes pane output (secrets,
#                 tokens, prompts) to session-history.json at rest on the box.
#                 It is off by default upstream and km does not turn it on.
spec:
  execution:
    # Appends after the merged initCommands rather than replacing them.
    initCommandsAppend:
      # KM_ARTIFACTS_BUCKET is exported at the top of the bootstrap and inherited
      # by /tmp/km-init.sh, so it resolves here.
      #
      # The `|| echo` is load-bearing: km-init.sh runs under `set -e`, so a hard
      # failure here aborts every LATER initCommand and surfaces only as
      # "No init script found in S3 (skipped)" — the trap documented in
      # base/security/wiz.yaml. `km herdr start` re-ensures the binary over SSM,
      # so a soft failure here is fully recoverable and a hard one is not.
      # KM_ARTIFACTS_BUCKET is NOT inherited here: km-init.sh runs under cloud-init,
      # which never sources /etc/profile.d. Proven live — the bare form expanded to
      # `s3:///binaries/herdr` and the fetch failed silently into the boot log.
      # km-identity.sh carries the bucket on every sandbox.
      - "[ -n \"${KM_ARTIFACTS_BUCKET:-}\" ] || . /etc/profile.d/km-identity.sh; aws s3 cp \"s3://${KM_ARTIFACTS_BUCKET}/binaries/herdr\" /usr/local/bin/herdr && chmod 0755 /usr/local/bin/herdr || echo '[km] herdr fetch failed; run km herdr start to install' >&2"
```

- [ ] **Step 4: Verify the fragment is skipped by the validator and parses**

Run:
```bash
make build
./km validate profiles/base/tools/herdr.yaml; echo "exit=$?"
```
Expected: exit 0 with a SKIP message — `km validate` skips `metadata.abstract: true` fragments. If it instead reports a schema error, the fragment is malformed; fix it before continuing.

- [ ] **Step 5: Verify a leaf can compose it**

Run:
```bash
cat > /tmp/herdr-compose-check.yaml <<'YAML'
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: herdr-compose-check
extends:
  - base/platform
  - base/os/debian
  - base/network/locked
  - base/tools/herdr
YAML
./km validate /tmp/herdr-compose-check.yaml; echo "exit=$?"
```

Expected: exit 0.

**The `extends:` list alone is NOT enough** — verified the hard way. `profiles/wiz-demo.yaml` is
not just a four-fragment `extends:` list; it also carries its own leaf `spec:` block, and none of
`base/platform` / `base/os/debian` / `base/network/locked` supply those required fields. A
metadata-plus-`extends:`-only file fails with nine schema errors
(`spec.execution.workingDir`, `spec.execution.shell`, `spec.lifecycle.{ttl,idleTimeout,teardownPolicy}`,
`spec.runtime.{substrate,instanceType,region}`, `spec.sourceAccess.mode`).

So copy `profiles/wiz-demo.yaml` wholesale, swap `base/security/wiz` for `base/tools/herdr` in its
`extends:` list, and keep its `spec:` block as-is. The point of this step is to prove
`base/tools/herdr` merges cleanly, not to design a profile: do NOT commit this file.

- [ ] **Step 6: Run the guard tests**

Run: `go test ./pkg/compiler/ -run 'TestInitCommands' -v; echo "exit=$?"`
Expected: PASS, `exit=0`.

- [ ] **Step 7: Run the full profile inventory gate**

Run: `./scripts/validate-all-profiles.sh; echo "exit=$?"`
Expected: exit 0. The script excludes `profiles/base/` automatically, so the new fragment must not appear in its output.

- [ ] **Step 8: Commit**

```bash
git add profiles/base/tools/herdr.yaml pkg/compiler/userdata_herdr_fragment_test.go
git commit -m "feat(herdr): base/tools/herdr fragment fetching the binary from s3"
rm -f /tmp/herdr-compose-check.yaml
```

---

### Task 3: `km herdr start`

**Files:**
- Modify: `internal/app/cmd/herdr.go` (stub from Task 1 — this is where the command lives)
- Modify: `internal/app/cmd/vscode.go` (extract the shared connect body)
- Modify: `internal/app/cmd/root.go` (register the command beside `NewVSCodeCmd` / `NewTunnelCmd`)
- Test: `internal/app/cmd/herdr_test.go` (create)

**Interfaces:**
- Consumes: `herdrS3Key` (Task 1); from the existing codebase — `SandboxFetcher.FetchSandbox`, `SSMSendAPI`, `ShellExecFunc`, `sendSSMAndWait(ctx, ssmClient, instanceID, script) (string, error)`, `extractResourceID(rec.Resources, ":instance/")`, `UpsertHost(configPath, alias string, opts HostOptions) error`, `HostOptions{HostName, Port, User, IdentityFile}`, `runReconnectingPortForward`, `buildPortForwardCmd`, `sshBannerTunnelProbe`, `ResolveSandboxID`, `resolveVSCodeDeps`.
- Produces:
  - `func NewHerdrCmd(cfg *config.Config) *cobra.Command`
  - `const herdrStatusScript string`
  - `type herdrBoxState struct { SSHDActive bool; AuthKeysPresent bool; HerdrPath string; HerdrVersion string }`
  - `func parseHerdrStatus(out string) herdrBoxState`
  - `func herdrInstallScript() string`
  - `func herdrBanner(w io.Writer, sandboxID, alias string, localPort int, st herdrBoxState)`

- [ ] **Step 1: Write the failing test**

Create `internal/app/cmd/herdr_test.go`:

```go
// Package cmd — herdr_test.go
// Tests for km herdr start / km herdr status.
package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseHerdrStatus_AllPresent(t *testing.T) {
	out := `=== sshd ===
active
=== authkeys exists ===
yes
=== authkeys content ===
ssh-ed25519 AAAAC3Nz km-sb-abc123
=== herdr path ===
/usr/local/bin/herdr
=== herdr version ===
herdr 0.8.2
`
	st := parseHerdrStatus(out)
	if !st.SSHDActive {
		t.Error("SSHDActive = false; want true")
	}
	if !st.AuthKeysPresent {
		t.Error("AuthKeysPresent = false; want true")
	}
	if st.HerdrPath != "/usr/local/bin/herdr" {
		t.Errorf("HerdrPath = %q; want /usr/local/bin/herdr", st.HerdrPath)
	}
	if st.HerdrVersion != "0.8.2" {
		t.Errorf("HerdrVersion = %q; want 0.8.2", st.HerdrVersion)
	}
}

func TestParseHerdrStatus_HerdrAbsent(t *testing.T) {
	out := `=== sshd ===
active
=== authkeys exists ===
yes
=== authkeys content ===
ssh-ed25519 AAAAC3Nz km-sb-abc123
=== herdr path ===
=== herdr version ===
`
	st := parseHerdrStatus(out)
	if !st.SSHDActive || !st.AuthKeysPresent {
		t.Fatal("sshd/authkeys should still parse as healthy when herdr is absent")
	}
	if st.HerdrPath != "" {
		t.Errorf("HerdrPath = %q; want empty", st.HerdrPath)
	}
	if st.HerdrVersion != "" {
		t.Errorf("HerdrVersion = %q; want empty", st.HerdrVersion)
	}
}

// TestParseHerdrStatus_VersionWithExtraFields covers `herdr --version` printing
// more than "herdr X.Y.Z" (a commit hash, a build date). Only the semver-looking
// token is kept, so a chattier upstream does not make km think herdr is absent.
func TestParseHerdrStatus_VersionWithExtraFields(t *testing.T) {
	out := `=== sshd ===
active
=== authkeys exists ===
yes
=== herdr path ===
/usr/local/bin/herdr
=== herdr version ===
herdr 0.8.2 (build abc1234, 2026-08-01)
`
	if got := parseHerdrStatus(out).HerdrVersion; got != "0.8.2" {
		t.Errorf("HerdrVersion = %q; want 0.8.2", got)
	}
}

// TestHerdrStatusScript_ProbesAllFour asserts the single SSM round trip covers
// every fact the banner and status output need. A missing probe here shows up as
// a silently-empty field rather than an error.
func TestHerdrStatusScript_ProbesAllFour(t *testing.T) {
	for _, want := range []string{
		"=== sshd ===",
		"=== authkeys exists ===",
		"=== herdr path ===",
		"=== herdr version ===",
	} {
		if !strings.Contains(herdrStatusScript, want) {
			t.Errorf("herdrStatusScript missing marker %q", want)
		}
	}
}

// TestHerdrInstallScript_UsesS3AndCorrectPath pins the install to the shared S3
// key and the system path. Installing to ~/.local/bin would work for the sandbox
// user but leave root's PATH without herdr, which km-presence signal 8 needs.
func TestHerdrInstallScript_UsesS3AndCorrectPath(t *testing.T) {
	s := herdrInstallScript()
	if !strings.Contains(s, herdrS3Key) {
		t.Errorf("install script does not reference %q", herdrS3Key)
	}
	if !strings.Contains(s, "/usr/local/bin/herdr") {
		t.Error("install script does not install to /usr/local/bin/herdr")
	}
	if !strings.Contains(s, "chmod 0755 /usr/local/bin/herdr") {
		t.Error("install script does not chmod the binary executable")
	}
}

// TestHerdrBanner_PrintsAttachCommandAndSSHConfigOptOut asserts the two things
// the operator cannot discover on their own: the exact attach command, and the
// fact that herdr will fight km over ~/.ssh/config unless told not to.
func TestHerdrBanner_PrintsAttachCommandAndSSHConfigOptOut(t *testing.T) {
	var buf bytes.Buffer
	st := herdrBoxState{SSHDActive: true, AuthKeysPresent: true,
		HerdrPath: "/usr/local/bin/herdr", HerdrVersion: "0.8.2"}
	herdrBanner(&buf, "sb-abc123", "km-sb-abc123", 2224, st)
	got := buf.String()

	if !strings.Contains(got, "herdr --remote km-sb-abc123") {
		t.Errorf("banner missing the attach command; got:\n%s", got)
	}
	if !strings.Contains(got, "manage_ssh_config = false") {
		t.Errorf("banner missing the ssh-config opt-out; got:\n%s", got)
	}
	if !strings.Contains(got, "ctrl+b q") {
		t.Errorf("banner missing the detach keybinding; got:\n%s", got)
	}
}

// Construct with &config.Config{}, never nil — the house style in
// vscode_test.go:410 and tunnel_test.go:62. cfg is only captured in RunE
// closures today, so nil would work, but an empty struct cannot panic if a
// future edit dereferences it at construction time.
//
// TestHerdrDefaultLocalPort_DoesNotCollide pins 2224. km vscode owns 2222 and
// both km tunnel modes own 2223; being attached to a box with more than one of
// these at once is a plausible combination, and a collision surfaces as a
// confusing "Connection closed by 127.0.0.1" rather than a bind error.
func TestHerdrDefaultLocalPort_DoesNotCollide(t *testing.T) {
	cmd := NewHerdrCmd(&config.Config{})
	start, _, err := cmd.Find([]string{"start"})
	if err != nil {
		t.Fatalf("find start subcommand: %v", err)
	}
	f := start.Flags().Lookup("local-port")
	if f == nil {
		t.Fatal("start has no --local-port flag")
	}
	if f.DefValue != "2224" {
		t.Errorf("--local-port default = %s; want 2224 (2222=vscode, 2223=tunnel)", f.DefValue)
	}
}

// TestNewHerdrCmd_HasStartAndStatusOnly asserts the surface stays minimal.
// Rekey is deliberately absent: herdr shares the vscode keypair, and two verbs
// rotating one key is a footgun.
func TestNewHerdrCmd_HasStartAndStatusOnly(t *testing.T) {
	cmd := NewHerdrCmd(&config.Config{})
	got := map[string]bool{}
	for _, c := range cmd.Commands() {
		got[c.Name()] = true
	}
	if !got["start"] || !got["status"] {
		t.Fatalf("expected start and status subcommands; got %v", got)
	}
	if got["rekey"] {
		t.Error("km herdr must NOT define rekey — km vscode rekey rotates the shared keypair")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/cmd/ -run 'TestParseHerdr|TestHerdr|TestNewHerdrCmd' -v; echo "exit=$?"`
Expected: FAIL to compile — `undefined: parseHerdrStatus`, `herdrStatusScript`, `herdrInstallScript`, `herdrBanner`, `herdrBoxState`, `NewHerdrCmd`.

- [ ] **Step 3: Extract the shared connect body from `vscode.go`**

`runVSCodeStart` and `runHerdrStart` differ only in pre-flight and banner. Copying it would fork the port probe, the key-not-found message, the `UpsertHost` call, and the reconnect wrapper — four things that must not drift.

In `internal/app/cmd/vscode.go`, add:

```go
// connectPrep is everything km vscode start and km herdr start do identically:
// probe the local port, resolve the sandbox and its instance, locate the local
// private key, and upsert the ~/.ssh/config entry. It stops short of the SSM
// pre-flight (each command probes different facts) and of the banner.
//
// Returns the instance id, the AWS region, the ssh-config alias, and the local
// private key path.
func connectPrep(ctx context.Context, fetcher SandboxFetcher, sandboxID string, localPort int) (instanceID, region, alias, privPath string, err error) {
	probeLn, probeErr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if probeErr != nil {
		return "", "", "", "", fmt.Errorf("local port %d is already in use — pick a different one with --local-port (e.g. %d)", localPort, localPort+100)
	}
	probeLn.Close()

	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return "", "", "", "", fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err = extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return "", "", "", "", fmt.Errorf("find EC2 instance: %w", err)
	}

	privPath, err = sandboxKeyPath(sandboxID)
	if err != nil {
		return "", "", "", "", err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", "", "", fmt.Errorf("locate home directory: %w", err)
	}
	alias = "km-" + sandboxID
	if err := UpsertHost(filepath.Join(home, ".ssh", "config"), alias, HostOptions{
		HostName:     "localhost",
		Port:         localPort,
		User:         "sandbox",
		IdentityFile: privPath,
	}); err != nil {
		return "", "", "", "", fmt.Errorf("upsert ssh-config: %w", err)
	}

	return instanceID, rec.Region, alias, privPath, nil
}
```

Then rewrite `runVSCodeStart`'s body to use it, keeping its existing SSM pre-flight (`vsCodeStatusScript` + `parseVSCodeStatus`) and its existing banner text **unchanged**, and ending with the same `runReconnectingPortForward` call. Note `sandboxKeyPath` (in `tunnel.go`) already produces the exact cross-laptop error message `runVSCodeStart` had inline; reuse it rather than keeping two copies.

Run the existing vscode tests before moving on:

Run: `go test ./internal/app/cmd/ -run 'VSCode' -v; echo "exit=$?"`
Expected: PASS, `exit=0`. If any fail, the extraction changed behaviour — fix it here, not later.

- [ ] **Step 4: Write `herdr.go`**

Replace `internal/app/cmd/herdr.go` entirely:

```go
// Package cmd — herdr.go
// km herdr: prepare a sandbox for a Herdr remote attach and hold the transport
// open. See docs/superpowers/specs/2026-09-04-herdr-remote-attach-design.md.
//
// This is a sibling of km vscode, not a km tunnel mode. km tunnel carries a
// network path from the workstation INTO the sandbox, and its security note
// (km's egress enforcement does not see traffic crossing the tunnel) follows
// from that direction. Herdr points the other way: the operator reaches in over
// a transport km already terminates, and nothing new becomes reachable from the
// box. Filing it under tunnel would attach a caveat that is false here.
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

// herdrS3Key is the single S3 key contract shared with base/tools/herdr.yaml
// and fetchAndUploadHerdr. Pinned by TestHerdrS3Key_PinnedAcrossAllSites.
const herdrS3Key = "binaries/herdr"

// herdrStatusScript is the single combined SSM script sent by both
// `km herdr start` and `km herdr status`. One round trip, four facts.
const herdrStatusScript = `echo "=== sshd ==="
systemctl is-active sshd 2>&1 || true
echo "=== authkeys exists ==="
test -f /home/sandbox/.ssh/authorized_keys && echo yes || echo no
echo "=== authkeys content ==="
cat /home/sandbox/.ssh/authorized_keys 2>/dev/null | head -1 || true
echo "=== herdr path ==="
command -v herdr 2>/dev/null || true
echo "=== herdr version ==="
herdr --version 2>/dev/null || true`

// herdrBoxState is the parsed result of herdrStatusScript.
type herdrBoxState struct {
	SSHDActive      bool
	AuthKeysPresent bool
	HerdrPath       string // "" when absent
	HerdrVersion    string // "" when absent or unparseable
}

// herdrSemverRe extracts the first semver-looking token from `herdr --version`
// output, so a chattier upstream (commit hash, build date) does not read as
// "herdr absent".
var herdrSemverRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// parseHerdrStatus interprets herdrStatusScript's output. It never errors:
// callers decide which missing facts are fatal, because `start` can repair a
// missing herdr binary while `status` only reports it.
func parseHerdrStatus(out string) herdrBoxState {
	return herdrBoxState{
		SSHDActive:      strings.Contains(out, "=== sshd ===\nactive"),
		AuthKeysPresent: strings.Contains(out, "=== authkeys exists ===\nyes"),
		HerdrPath:       strings.TrimSpace(sectionOf(out, "=== herdr path ===")),
		HerdrVersion:    herdrSemverRe.FindString(sectionOf(out, "=== herdr version ===")),
	}
}

// sectionOf returns the text between marker and the next "=== " marker (or EOF).
func sectionOf(out, marker string) string {
	i := strings.Index(out, marker)
	if i < 0 {
		return ""
	}
	rest := out[i+len(marker):]
	if j := strings.Index(rest, "\n=== "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// herdrInstallScript pulls the pinned binary from S3 using the instance role.
// Deliberately NOT `curl https://herdr.dev/install.sh` — the interactive
// installer needs public egress, which on an ebpf/both profile means adding a
// ".herdr.dev" DNS suffix (DNS is the one layer root does not bypass), and
// Herdr's own docs say non-interactive installs fail outright.
func herdrInstallScript() string {
	return fmt.Sprintf(`set -e
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/%s" /usr/local/bin/herdr
chmod 0755 /usr/local/bin/herdr
echo "=== herdr path ==="
command -v herdr 2>/dev/null || true
echo "=== herdr version ==="
herdr --version 2>/dev/null || true`, herdrS3Key)
}

// herdrBanner prints the connection block before the blocking port-forward opens.
func herdrBanner(w io.Writer, sandboxID, alias string, localPort int, st herdrBoxState) {
	fmt.Fprintf(w, "✓ Updated ~/.ssh/config (Host: %s)\n", alias)
	fmt.Fprintf(w, "✓ herdr v%s present at %s\n", st.HerdrVersion, st.HerdrPath)
	fmt.Fprintf(w, "✓ Forwarding localhost:%d → sandbox:22\n\n", localPort)
	fmt.Fprintf(w, "In another terminal:\n\n")
	fmt.Fprintf(w, "    herdr --remote %s\n\n", alias)
	fmt.Fprintf(w, "  Named session:  herdr --remote %s --session agents\n", alias)
	fmt.Fprintf(w, "  Detach:         ctrl+b q          (panes keep running)\n")
	fmt.Fprintf(w, "  Reattach:       rerun the same command\n\n")
	fmt.Fprintf(w, "  Herdr rewrites ~/.ssh/config by default and km owns the %s block\n", alias)
	fmt.Fprintf(w, "  in that file. Add this to ~/.config/herdr/config.toml so they do not\n")
	fmt.Fprintf(w, "  fight over it:\n\n")
	fmt.Fprintf(w, "      [remote]\n      manage_ssh_config = false\n\n")
	fmt.Fprintf(w, "The tunnel auto-reconnects if it drops; Ctrl-C closes it. Detached panes\n")
	fmt.Fprintf(w, "survive both — but `km herdr status %s` shows what the idle timer sees.\n\n", sandboxID)
}

// NewHerdrCmd returns the `km herdr` parent command.
func NewHerdrCmd(cfg *config.Config) *cobra.Command {
	return newHerdrCmdInternal(cfg, nil, nil, nil)
}

func newHerdrCmdInternal(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	parent := &cobra.Command{
		Use:          "herdr",
		Short:        "Attach a Herdr terminal multiplexer to a sandbox over SSM",
		SilenceUsage: true,
	}
	parent.AddCommand(newHerdrStartCmd(cfg, fetcher, execFn, ssmClient))
	parent.AddCommand(newHerdrStatusCmd(cfg, fetcher, ssmClient))
	return parent
}

func newHerdrStartCmd(cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI) *cobra.Command {
	var localPort int
	var noInstall bool
	cmd := &cobra.Command{
		Use:   "start <sandbox-id>",
		Short: "Hold open the transport for a Herdr remote attach",
		Long: `Prepare a sandbox to accept a Herdr remote attach and hold the transport open.

Run this in one terminal and `+"`herdr --remote km-<id>`"+` in another. Panes keep
running when you detach with ctrl+b q, and survive this command being Ctrl-C'd —
but a Herdr session does NOT survive a reboot, so km stop (and an idle stop under
teardownPolicy: stop) kills every pane's process. km pause hibernates and keeps them.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			f, e, s, err := resolveVSCodeDeps(c.Context(), cfg, fetcher, execFn, ssmClient)
			if err != nil {
				return err
			}
			sandboxID, err := ResolveSandboxID(c.Context(), cfg, args[0])
			if err != nil {
				return err
			}
			return runHerdrStart(c.Context(), f, e, s, sandboxID, localPort, noInstall)
		},
	}
	// 2224: km vscode owns 2222, both km tunnel modes own 2223.
	cmd.Flags().IntVar(&localPort, "local-port", 2224, "Local port for the SSM forward to sshd")
	cmd.Flags().BoolVar(&noInstall, "no-install", false, "Fail instead of installing herdr when it is absent from the sandbox")
	return cmd
}

func newHerdrStatusCmd(cfg *config.Config, fetcher SandboxFetcher, ssmClient SSMSendAPI) *cobra.Command {
	return &cobra.Command{
		Use:          "status <sandbox-id>",
		Short:        "Report sshd, authorized_keys, herdr version, and what the idle timer sees",
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
			return runHerdrStatus(c.Context(), f, s, sandboxID)
		},
	}
}

func runHerdrStart(ctx context.Context, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string, localPort int, noInstall bool) error {
	instanceID, region, alias, _, err := connectPrep(ctx, fetcher, sandboxID, localPort)
	if err != nil {
		return err
	}

	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, herdrStatusScript)
	if err != nil {
		return fmt.Errorf("ssm pre-flight check: %w", err)
	}
	st := parseHerdrStatus(out)

	// Reuse the vscode parser for the sshd/authorized_keys half so the two
	// commands give identical diagnoses for identical failures.
	if err := parseVSCodeStatus(out, sandboxID); err != nil {
		return err
	}

	if st.HerdrPath == "" {
		if noInstall {
			return fmt.Errorf("herdr is not installed on %s and --no-install was set; install it with `km herdr start %s` or add `extends: [base/tools/herdr]` to the profile", sandboxID, sandboxID)
		}
		fmt.Printf("herdr not found on %s — installing from s3://.../%s\n", sandboxID, herdrS3Key)
		instOut, instErr := sendSSMAndWait(ctx, ssmClient, instanceID, herdrInstallScript())
		if instErr != nil {
			return fmt.Errorf("install herdr on sandbox: %w", instErr)
		}
		st = parseHerdrStatus(instOut)
		if st.HerdrPath == "" {
			return fmt.Errorf("herdr install ran but the binary is still absent on %s — check that `km init --sidecars` has seeded s3://<artifacts>/%s", sandboxID, herdrS3Key)
		}
		fmt.Printf("✓ Installed herdr v%s\n", st.HerdrVersion)
	}

	herdrBanner(os.Stdout, sandboxID, alias, localPort, st)

	buildPF := func(c context.Context) *exec.Cmd {
		return buildPortForwardCmd(c, instanceID, region, strconv.Itoa(localPort), "22")
	}
	return runReconnectingPortForward(ctx, execFn, buildPF, sshBannerTunnelProbe(localPort), true, os.Stdout)
}
```

`runHerdrStatus` is written in Task 4; add a temporary stub so this compiles:

```go
func runHerdrStatus(ctx context.Context, fetcher SandboxFetcher, ssmClient SSMSendAPI, sandboxID string) error {
	return fmt.Errorf("not implemented")
}
```

- [ ] **Step 5: Register the command**

In `internal/app/cmd/root.go`, beside the existing `NewVSCodeCmd` / `NewDesktopCmd` / `NewTunnelCmd` registrations:

```go
	root.AddCommand(NewHerdrCmd(cfg))
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/app/cmd/ -run 'TestParseHerdr|TestHerdr|TestNewHerdrCmd|VSCode' -v; echo "exit=$?"`
Expected: PASS, `exit=0`. The `VSCode` tests are included deliberately — Step 3 refactored their code path.

- [ ] **Step 7: Verify the command is reachable**

Run: `make build && ./km herdr start --help`
Expected: help text showing `--local-port` (default 2224) and `--no-install`.

- [ ] **Step 8: Commit**

```bash
git add internal/app/cmd/herdr.go internal/app/cmd/herdr_test.go internal/app/cmd/vscode.go internal/app/cmd/root.go
git commit -m "feat(herdr): km herdr start with ensure-install and shared connect prep"
```

---

### Task 4: `km herdr status`

**Files:**
- Modify: `internal/app/cmd/herdr.go` (replace the stub)
- Test: `internal/app/cmd/herdr_test.go` (append)

**Interfaces:**
- Consumes: `herdrBoxState`, `parseHerdrStatus`, `herdrStatusScript` (Task 3).
- Produces: `func herdrStatusReport(w io.Writer, sandboxID string, st herdrBoxState, presenceHasSignal8 bool) error` — returns a non-nil error when the sandbox is not ready, so the command exits non-zero.

- [ ] **Step 1: Write the failing test**

Append to `internal/app/cmd/herdr_test.go`:

```go
func TestHerdrStatusReport_ReadyIsNilError(t *testing.T) {
	var buf bytes.Buffer
	st := herdrBoxState{SSHDActive: true, AuthKeysPresent: true,
		HerdrPath: "/usr/local/bin/herdr", HerdrVersion: "0.8.2"}
	if err := herdrStatusReport(&buf, "sb-abc123", st, true); err != nil {
		t.Fatalf("healthy sandbox reported an error: %v", err)
	}
	if !strings.Contains(buf.String(), "0.8.2") {
		t.Errorf("report omits the herdr version; got:\n%s", buf.String())
	}
}

func TestHerdrStatusReport_HerdrAbsentIsError(t *testing.T) {
	var buf bytes.Buffer
	st := herdrBoxState{SSHDActive: true, AuthKeysPresent: true}
	err := herdrStatusReport(&buf, "sb-abc123", st, true)
	if err == nil {
		t.Fatal("expected a non-nil error when herdr is absent")
	}
	if !strings.Contains(err.Error(), "km herdr start") {
		t.Errorf("error should name the repair command; got %q", err)
	}
}

// TestHerdrStatusReport_WarnsOnPreSignal8Presence is the load-bearing one.
// km-presence is fetched at boot, so a sandbox created before signal 8 shipped
// keeps the seven-signal daemon and a detached herdr session on it is STILL
// reapable. Saying so is the difference between "the box vanished" and "I could
// see it coming".
func TestHerdrStatusReport_WarnsOnPreSignal8Presence(t *testing.T) {
	var buf bytes.Buffer
	st := herdrBoxState{SSHDActive: true, AuthKeysPresent: true,
		HerdrPath: "/usr/local/bin/herdr", HerdrVersion: "0.8.2"}
	if err := herdrStatusReport(&buf, "sb-abc123", st, false); err != nil {
		t.Fatalf("a pre-signal-8 daemon is a warning, not an error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "km destroy") {
		t.Errorf("warning should name the recreate that fixes it; got:\n%s", got)
	}
	if !strings.Contains(strings.ToLower(got), "idle") {
		t.Errorf("warning should say the box is still idle-reapable; got:\n%s", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/cmd/ -run 'TestHerdrStatusReport' -v; echo "exit=$?"`
Expected: FAIL to compile — `undefined: herdrStatusReport`.

- [ ] **Step 3: Implement the report and the command body**

In `internal/app/cmd/herdr.go`, replace the `runHerdrStatus` stub:

```go
// herdrPresenceSignal8Script reports whether the box's km-presence binary
// carries signal 8. `strings` on the binary is crude but honest: the daemon
// exposes no version verb, and the alternative (a DynamoDB field recording the
// km version at create time) would be a schema change for a warning line.
const herdrPresenceSignal8Script = `echo "=== presence signal8 ==="
if strings /opt/km/bin/km-presence 2>/dev/null | grep -q herdr; then echo yes; else echo no; fi`

// herdrStatusReport prints the readiness summary and returns a non-nil error
// when the sandbox cannot accept an attach.
func herdrStatusReport(w io.Writer, sandboxID string, st herdrBoxState, presenceHasSignal8 bool) error {
	if !st.SSHDActive || !st.AuthKeysPresent {
		return parseVSCodeStatusState(st, sandboxID)
	}
	if st.HerdrPath == "" {
		return fmt.Errorf("herdr is not installed on %s — run `km herdr start %s` to install it, or add `extends: [base/tools/herdr]` to the profile and recreate", sandboxID, sandboxID)
	}

	fmt.Fprintf(w, "✓ herdr ready on %s (sshd active, authorized_keys present, herdr v%s at %s)\n",
		sandboxID, st.HerdrVersion, st.HerdrPath)

	if !presenceHasSignal8 {
		fmt.Fprintf(w, "\n⚠ This sandbox runs a km-presence daemon without signal 8.\n")
		fmt.Fprintf(w, "  A DETACHED herdr session running work is invisible to the idle timer,\n")
		fmt.Fprintf(w, "  so the box can still be stopped mid-run. Under teardownPolicy: stop that\n")
		fmt.Fprintf(w, "  kills every pane's process — herdr sessions do not survive a reboot.\n")
		fmt.Fprintf(w, "  Fix: km destroy %s && km create <profile>\n", sandboxID)
	}
	return nil
}

// parseVSCodeStatusState renders the same diagnoses parseVSCodeStatus gives, from
// an already-parsed state rather than raw output.
func parseVSCodeStatusState(st herdrBoxState, sandboxID string) error {
	switch {
	case !st.SSHDActive && !st.AuthKeysPresent:
		return fmt.Errorf("sshd is not running and authorized_keys is absent on %s — this profile likely has spec.runtime.vscode.enabled: false; set it true and recreate the sandbox", sandboxID)
	case !st.AuthKeysPresent:
		return fmt.Errorf("unexpected state: sshd is running but /home/sandbox/.ssh/authorized_keys is absent — recreate the sandbox")
	default:
		return fmt.Errorf("sshd is not running on the sandbox; try `km shell %s -- sudo systemctl start sshd`", sandboxID)
	}
}

func runHerdrStatus(ctx context.Context, fetcher SandboxFetcher, ssmClient SSMSendAPI, sandboxID string) error {
	rec, err := fetcher.FetchSandbox(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("fetch sandbox: %w", err)
	}
	instanceID, err := extractResourceID(rec.Resources, ":instance/")
	if err != nil {
		return fmt.Errorf("find EC2 instance: %w", err)
	}

	out, err := sendSSMAndWait(ctx, ssmClient, instanceID, herdrStatusScript+"\n"+herdrPresenceSignal8Script)
	if err != nil {
		return fmt.Errorf("ssm status check: %w", err)
	}
	st := parseHerdrStatus(out)
	// Read the probe's own section. A bare strings.Contains(out, "yes") would
	// also match the "=== authkeys exists ===\nyes" section above it and report
	// every sandbox as signal-8-capable, silencing the warning that matters most.
	hasS8 := strings.TrimSpace(sectionOf(out, "=== presence signal8 ===")) == "yes"
	return herdrStatusReport(os.Stdout, sandboxID, st, hasS8)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/app/cmd/ -run 'Herdr|VSCode' -v; echo "exit=$?"`
Expected: PASS, `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add internal/app/cmd/herdr.go internal/app/cmd/herdr_test.go
git commit -m "feat(herdr): km herdr status with pre-signal-8 idle warning"
```

---

### Task 5: `km doctor` — Herdr ssh-config conflict check

**Files:**
- Modify: `internal/app/cmd/doctor_sshconfig.go`
- Test: `internal/app/cmd/doctor_sshconfig_test.go` (append)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `func checkHerdrSSHConfigConflict(herdrConfigPath, sshConfigPath string) CheckResult` — matching the package's existing check convention (`CheckResult{Name, Status, Message, Remediation}` with `CheckOK`/`CheckWarn`, as `checkStaleSSHConfig` in the same file returns).

- [ ] **Step 1: Write the failing test**

Append to `internal/app/cmd/doctor_sshconfig_test.go`:

```go
func TestCheckHerdrSSHConfigConflict_WarnsWhenBothManage(t *testing.T) {
	dir := t.TempDir()
	herdrCfg := filepath.Join(dir, "config.toml")
	sshCfg := filepath.Join(dir, "config")
	if err := os.WriteFile(herdrCfg, []byte("[remote]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sshCfg, []byte("Host km-sb-abc123\n  HostName localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := checkHerdrSSHConfigConflict(herdrCfg, sshCfg)
	if res.Status != CheckWarn {
		t.Fatalf("Status = %v; want CheckWarn when herdr manages ssh config and a km- Host block exists", res.Status)
	}
	if !strings.Contains(res.Remediation, "manage_ssh_config = false") {
		t.Errorf("Remediation should name the fix; got %q", res.Remediation)
	}
}

func TestCheckHerdrSSHConfigConflict_SilentWhenOptedOut(t *testing.T) {
	dir := t.TempDir()
	herdrCfg := filepath.Join(dir, "config.toml")
	sshCfg := filepath.Join(dir, "config")
	if err := os.WriteFile(herdrCfg, []byte("[remote]\nmanage_ssh_config = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sshCfg, []byte("Host km-sb-abc123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := checkHerdrSSHConfigConflict(herdrCfg, sshCfg); res.Status != CheckOK {
		t.Fatalf("Status = %v; want CheckOK once manage_ssh_config = false is set", res.Status)
	}
}

// TestCheckHerdrSSHConfigConflict_SilentWhenHerdrUnused asserts the check costs
// nothing for an operator who has never run herdr. Every km doctor group that is
// not configured must skip silently rather than emit noise.
func TestCheckHerdrSSHConfigConflict_SilentWhenHerdrUnused(t *testing.T) {
	dir := t.TempDir()
	sshCfg := filepath.Join(dir, "config")
	if err := os.WriteFile(sshCfg, []byte("Host km-sb-abc123\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := checkHerdrSSHConfigConflict(filepath.Join(dir, "absent.toml"), sshCfg); res.Status != CheckOK {
		t.Fatalf("Status = %v; want CheckOK when no herdr config exists", res.Status)
	}
}

// TestCheckHerdrSSHConfigConflict_SilentWithoutKmHosts asserts the two tools only
// conflict when both actually write the file.
func TestCheckHerdrSSHConfigConflict_SilentWithoutKmHosts(t *testing.T) {
	dir := t.TempDir()
	herdrCfg := filepath.Join(dir, "config.toml")
	sshCfg := filepath.Join(dir, "config")
	if err := os.WriteFile(herdrCfg, []byte("[remote]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sshCfg, []byte("Host bastion\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := checkHerdrSSHConfigConflict(herdrCfg, sshCfg); res.Status != CheckOK {
		t.Fatalf("Status = %v; want CheckOK when ~/.ssh/config has no km- Host block", res.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/cmd/ -run 'TestCheckHerdrSSHConfigConflict' -v; echo "exit=$?"`
Expected: FAIL to compile — `undefined: checkHerdrSSHConfigConflict`.

- [ ] **Step 3: Implement**

Add to `internal/app/cmd/doctor_sshconfig.go`:

```go
// checkHerdrSSHConfigConflict reports whether Herdr and km are both managing
// ~/.ssh/config. Herdr rewrites it by default (adding keepalive fallbacks) and
// km's UpsertHost owns the `Host km-<id>` blocks — two writers, one file.
//
// km deliberately does NOT write the fix itself. ~/.config/herdr/config.toml is
// a workstation-global file km does not own; silently editing it to win a
// conflict is how you produce a bug report about km breaking an unrelated remote
// host. Report it and let the operator decide.
//
// Skips silently when herdr has never been configured, or when ~/.ssh/config has
// no km- Host block — there is no conflict until both tools write the file.
func checkHerdrSSHConfigConflict(herdrConfigPath, sshConfigPath string) CheckResult {
	const name = "Herdr ssh-config conflict"
	ok := CheckResult{Name: name, Status: CheckOK, Message: "herdr is not managing ~/.ssh/config"}

	herdrRaw, err := os.ReadFile(herdrConfigPath)
	if err != nil {
		return CheckResult{Name: name, Status: CheckOK, Message: "herdr not configured on this workstation"}
	}
	if strings.Contains(strings.ReplaceAll(string(herdrRaw), " ", ""), "manage_ssh_config=false") {
		return ok
	}
	sshRaw, err := os.ReadFile(sshConfigPath)
	if err != nil {
		return ok
	}
	if !strings.Contains(string(sshRaw), "Host km-") {
		return ok
	}
	return CheckResult{
		Name:    name,
		Status:  CheckWarn,
		Message: fmt.Sprintf("herdr is managing %s, where km owns the `Host km-*` blocks", sshConfigPath),
		Remediation: fmt.Sprintf("Add to %s:\n\n    [remote]\n    manage_ssh_config = false\n", herdrConfigPath),
	}
}
```

- [ ] **Step 4: Wire it into the doctor run**

Find where `checkStaleSSHConfig` is invoked in the doctor run (grep for it outside `doctor_sshconfig.go`) and append `checkHerdrSSHConfigConflict` to the same `[]CheckResult` collection, called as:

```go
	checkHerdrSSHConfigConflict(
		filepath.Join(home, ".config", "herdr", "config.toml"),
		filepath.Join(home, ".ssh", "config"),
	)
```

It returns `CheckWarn` at worst and never an error, so it needs no gating flag and cannot fail a doctor run.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/app/cmd/ -run 'Doctor|Herdr' -v; echo "exit=$?"`
Expected: PASS, `exit=0`.

- [ ] **Step 6: Commit**

```bash
git add internal/app/cmd/doctor_sshconfig.go internal/app/cmd/doctor_sshconfig_test.go
git commit -m "feat(herdr): km doctor warns when herdr and km both manage ssh config"
```

---

### Task 6: Live probe — capture Herdr API fixtures

**This task requires a real sandbox and a real Herdr server. It cannot be completed from the dev machine.** Its output is committed test fixtures that Task 7 parses. Spec §6.4 exists precisely because the choice between "one call per session" and "one call per pane" is not answerable from documentation.

**It does NOT require an interactive attach.** `herdr server` starts a headless
background server (herdr.dev/docs/how-to-work, and the "Live updates without killing
your terminal processes" post: "launch herdr in any terminal or as a headless daemon";
"when no client is attached, the server uses a 120x40 virtual terminal for layout and
newly created panes"). `herdr workspace create`, `herdr pane split`, and
`herdr pane run` drive panes from the command line. Every step below runs over
`km shell` / SSM. A pane created this way with nothing attached IS the detached-busy
state signal 8 exists to detect.

**Files:**
- Create: `cmd/km-presence/testdata/herdr_pane_list.json`
- Create: `cmd/km-presence/testdata/herdr_agent_list.json`
- Create: `cmd/km-presence/testdata/herdr_pane_list_idle.json`
- Create: `cmd/km-presence/testdata/README.md`

**Interfaces:**
- Consumes: `km herdr start` (Task 3).
- Produces: the four fixture files above, and a recorded decision in `testdata/README.md` naming which of spec §6.4's three options applies.

- [ ] **Step 1: Deploy what exists so far and create a probe sandbox**

```bash
git pull --rebase
git log --oneline -1 HEAD && git log --oneline -1 origin/main   # must share a base
make build
km init --sidecars       # a NON-ZERO exit is expected in a worktree that has never run
                         # `make build-lambdas` — the toolchain tar step fails AFTER the
                         # herdr upload. Confirm `Uploaded herdr` in the output instead.
aws s3 ls s3://<artifacts-bucket>/binaries/herdr --profile klanker-terraform
```

**The probe profile MUST extend the fragment.** `profiles/learner.yaml` does NOT extend
`base/tools/herdr`, so a box created from it has no herdr binary and Step 2 fails at its first
command. Create a throwaway leaf that does — which also makes this probe the fragment's first
end-to-end live proof, the only place `base/tools/herdr` gets exercised before the PR:

```bash
sed -e 's/^  name: learner$/  name: herdrprobe/' \
    -e 's|^  - base/userinit$|  - base/userinit\n  - base/tools/herdr|' \
    profiles/learner.yaml > /tmp/herdrprobe.yaml
grep -n "base/tools/herdr" /tmp/herdrprobe.yaml    # must print a line; if not, edit by hand
./km validate /tmp/herdrprobe.yaml
./km create /tmp/herdrprobe.yaml herdrprobe --wait
```

Then confirm the FRAGMENT delivered the binary — not a later manual step. This is the assertion
the fragment's existence rests on:

```bash
./km shell herdrprobe     # on the box:  ls -l /usr/local/bin/herdr && herdr --version
```

If it is absent, grep `/var/log/cloud-init-output.log` for `[km] herdr fetch failed`. The
fail-soft `|| echo` means a missing binary is a warning in the boot log rather than a boot
failure, so that log line is the only place it surfaces.


- [ ] **Step 2: Start a headless server and capture the IDLE fixture first**

Over `km shell herdrprobe`, as the `sandbox` user:

```bash
setsid herdr server >/tmp/herdr-server.log 2>&1 < /dev/null &
sleep 3
ls -la ~/.config/herdr/            # herdr.sock must exist
herdr workspace create --label probe
herdr pane list --json
```

Capture the idle fixture **before** creating any work, so it is unambiguously a
server with panes and nothing running:

```bash
herdr api schema --output /tmp/herdr-api.schema.json
herdr pane list --json > /tmp/herdr_pane_list_idle.json
```

If `herdr server` will not start without a TTY, or no socket appears, **stop this
task**, record exactly what happened in the report, and skip to Task 8. The
fallback the operator authorised is to ship Tasks 1-5 plus docs rather than write
signal 8 against unverified JSON.

- [ ] **Step 3: Create a busy pane and capture the BUSY fixtures**

```bash
herdr pane list --json                       # note a pane target, e.g. w1:p1
herdr pane run <pane-target> "sleep 900"
sleep 2
herdr pane list --json  > /tmp/herdr_pane_list.json
herdr agent list --json > /tmp/herdr_agent_list.json
cat /tmp/herdr_pane_list.json
```

`sleep 900` is deliberately not an agent: the whole point of signal 8 is that it
does not depend on recognising an agent by name.

- [ ] **Step 4: Answer the §6.4 question from what you captured**

Diff the busy and idle fixtures and decide:

- Do the two `pane list --json` outputs actually DIFFER — i.e. does a pane object
  already carry foreground-process or busy information? (Option 1)
- If they are byte-identical, does `/tmp/herdr-api.schema.json` show
  `pane.process_info` as a separate per-pane call, and does `agent list --json`
  differ instead? (Option 2 or 3)

**If busy and idle `pane list` are byte-identical, signal 8 cannot be built on
`pane list` alone.** Say so explicitly and name the option Task 7 must take.


- [ ] **Step 5: Bring the fixtures back and record the decision**

Copy the three JSON files to `cmd/km-presence/testdata/` (redact any cwd or argv
that names a private path — these are committed). Then write
`cmd/km-presence/testdata/README.md`:

```markdown
# Herdr API fixtures for km-presence signal 8

Captured from herdr v0.8.2 on a live sandbox on <DATE>, per Task 6 of
docs/superpowers/plans/2026-09-04-herdr-remote-attach.md.

- `herdr_pane_list.json`      — a pane running `sleep 900`, captured headlessly
- `herdr_pane_list_idle.json` — the same server with every pane at a bare shell
- `herdr_agent_list.json`     — same moment as herdr_pane_list.json

Captured from a headless `herdr server` driven by `herdr pane run` over SSM. No
interactive client was ever attached, which is exactly the state signal 8 targets.

## §6.4 decision

Option taken: <1 | 2 | 3>

<One paragraph: what `pane list --json` actually returns, whether busy state is
present in it, and therefore how many subprocess calls signal 8 costs per tick.>
```

- [ ] **Step 6: Tear down the probe sandbox**

```bash
km destroy herdrprobe --remote --yes
```

- [ ] **Step 7: Commit**

```bash
git add cmd/km-presence/testdata/
git commit -m "test(herdr): capture live herdr API fixtures for presence signal 8"
```

---

### Task 7: km-presence signal 8 — pane busy

**Files:**
- Modify: `cmd/km-presence/runner.go` (add `checkHerdrPaneBusy`; extend `tick`)
- Modify: `cmd/km-presence/main.go` (add `defaultHerdrConfigDir`; pass it to `tick`; fix the "seven signals" comment at line 4)
- Test: `cmd/km-presence/main_test.go` (append)

**Interfaces:**
- Consumes: the fixtures and the §6.4 decision from Task 6; `commandRunner`.
- Produces:
  - `const defaultHerdrConfigDir = "/home/sandbox/.config/herdr"`
  - `func herdrSocketPaths(configDir string) []string`
  - `func checkHerdrPaneBusy(r commandRunner, configDir string) bool`
  - `tick` signature becomes `tick(r commandRunner, sandboxID, mailDir, slackStampPath, presenceStampPath, herdrConfigDir string) (bool, bool)`

**Deviation note for the implementer:** the struct below is written against Herdr's documented `pane.process_info` shape (shell pid, foreground process group id, foreground processes with pid/name/argv/cwd). If Task 6's fixtures show different field names, **change the JSON tags to match the fixtures** — the fixtures are ground truth and the docs are not. This is a bounded, mechanical deviation; the signal's semantics do not change.

- [ ] **Step 1: Write the failing test**

Append to `cmd/km-presence/main_test.go`:

```go
// =============================================================================
// Signal 8: Herdr pane busy
// =============================================================================

// herdrPaneCmd builds the fakeRunner key for a pane-list call against one socket.
func herdrPaneCmd(sock string) string {
	return `runuser -u sandbox -- bash -lc herdr pane list --json --socket "` + sock + `"`
}

func TestSignal_HerdrPaneBusy_Positive(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "herdr.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/herdr_pane_list.json")
	if err != nil {
		t.Fatalf("read busy fixture: %v", err)
	}
	r := &fakeRunner{responses: map[string][]byte{herdrPaneCmd(sock): raw}}
	if !checkHerdrPaneBusy(r, dir) {
		t.Fatal("expected positive when a pane has a foreground process")
	}
}

// TestSignal_HerdrPaneBusy_NegativeAllIdle is the load-bearing negative case.
// If this ever returns true, idle teardown is silently disabled on every box that
// has ever run herdr, and instances leak indefinitely.
func TestSignal_HerdrPaneBusy_NegativeAllIdle(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "herdr.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/herdr_pane_list_idle.json")
	if err != nil {
		t.Fatalf("read idle fixture: %v", err)
	}
	r := &fakeRunner{responses: map[string][]byte{herdrPaneCmd(sock): raw}}
	if checkHerdrPaneBusy(r, dir) {
		t.Fatal("expected negative when every pane sits at a bare shell — a signal " +
			"that cannot go negative disables idle teardown fleet-wide")
	}
}

func TestSignal_HerdrPaneBusy_NegativeNoSocket(t *testing.T) {
	if checkHerdrPaneBusy(&fakeRunner{}, t.TempDir()) {
		t.Fatal("expected negative when no herdr socket exists")
	}
}

func TestSignal_HerdrPaneBusy_NegativeCommandError(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "herdr.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{
		responses: map[string][]byte{},
		errors:    map[string]error{herdrPaneCmd(sock): errors.New("exit status 1")},
	}
	if checkHerdrPaneBusy(r, dir) {
		t.Fatal("expected negative (fail idle) when herdr exits non-zero")
	}
}

func TestSignal_HerdrPaneBusy_NegativeMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "herdr.sock")
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{responses: map[string][]byte{herdrPaneCmd(sock): []byte("not json")}}
	if checkHerdrPaneBusy(r, dir) {
		t.Fatal("expected negative (fail idle) on malformed output")
	}
}

// TestHerdrSocketPaths_FindsDefaultAndNamed asserts named sessions are queried
// too. Discovery is a filesystem glob rather than `herdr session list` — signals
// 3 and 4 already read the filesystem directly, and a glob needs no subprocess.
func TestHerdrSocketPaths_FindsDefaultAndNamed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "herdr.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(dir, "sessions", "agents")
	if err := os.MkdirAll(named, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(named, "herdr.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got := herdrSocketPaths(dir)
	if len(got) != 2 {
		t.Fatalf("herdrSocketPaths returned %d paths, want 2: %v", len(got), got)
	}
}

// TestSignal_HerdrPaneBusy_IndependentOfSSHAndVNC pins that signal 8 never fires
// from signal 6's or 7's inputs, matching the cross-independence discipline the
// VNC/SSH signals already follow.
func TestSignal_HerdrPaneBusy_IndependentOfSSHAndVNC(t *testing.T) {
	r := &fakeRunner{responses: map[string][]byte{
		"ss -tnHp state established": []byte(`ESTAB 0 0 10.0.0.1:22 10.0.0.2:5000 users:(("sshd",pid=1,fd=3))`),
	}}
	if checkHerdrPaneBusy(r, t.TempDir()) {
		t.Fatal("signal 8 fired from signal 7's input")
	}
}
```

Add `"errors"` to that file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/km-presence/ -run 'HerdrPaneBusy|HerdrSocketPaths' -v; echo "exit=$?"`
Expected: FAIL to compile — `undefined: checkHerdrPaneBusy`, `undefined: herdrSocketPaths`.

- [ ] **Step 3: Implement the signal**

Add to `cmd/km-presence/runner.go`:

```go
// herdrSocketPaths returns every Herdr server socket under configDir: the
// default session plus one per named session.
//
// Discovery is a filesystem glob rather than `herdr session list` because a glob
// needs no subprocess and no seam — the same reasoning that has signals 3 and 4
// reading the filesystem directly.
func herdrSocketPaths(configDir string) []string {
	var out []string
	if _, err := os.Stat(filepath.Join(configDir, "herdr.sock")); err == nil {
		out = append(out, filepath.Join(configDir, "herdr.sock"))
	}
	named, _ := filepath.Glob(filepath.Join(configDir, "sessions", "*", "herdr.sock"))
	return append(out, named...)
}

// herdrPane is the subset of `herdr pane list --json` this signal reads.
// Field names track cmd/km-presence/testdata/herdr_pane_list.json, which was
// captured from a live server — the API docs are not authoritative here.
type herdrPane struct {
	ID                string `json:"id"`
	ShellPID          int    `json:"shell_pid"`
	ForegroundPGID    int    `json:"foreground_pgid"`
	ForegroundProcess []struct {
		PID  int    `json:"pid"`
		Name string `json:"name"`
	} `json:"foreground_processes"`
}

// checkHerdrPaneBusy returns true when any Herdr session has a pane doing work.
// Signal 8.
//
// Herdr's whole purpose here is persistence across detach: an operator closes
// the laptop and expects agent panes to keep running. Signal 7 sees an ATTACHED
// client (a herdr client is an SSH session), and signal 5 sees a running
// claude/codex by name — but a DETACHED pane running anything else (a build, a
// test suite, an agent whose name is not in signal 5's pgrep list) is invisible,
// and under teardownPolicy: stop the idle reaper then kills the work. This
// signal closes that, and does it without naming any agent, so swapping agents
// costs no edit here.
//
// Detects the WORK, never the server. `herdr server stop` is the only thing that
// ends a Herdr server, so "a server is running" would latch the box awake forever
// — exactly the pgrep vscode-server trap that checkSSHSessions was written to
// avoid.
//
// Fails idle: an absent socket, a missing binary, a non-zero exit, or malformed
// JSON all return false. A reaped box costs one `km resume`; a signal that can
// never go negative silently disables idle teardown fleet-wide and leaks
// instances.
//
// Runs the sandbox user's herdr via runuser (not sudo/su), matching the dispatch
// convention established when agent traffic had to keep its cgroup.
//
// `bash -lc`, not a bare `herdr`, and this is load-bearing. Verified on a live
// sandbox: base/userinit.yaml installs herdr to /home/sandbox/.local/bin/herdr
// and `command -v herdr` as root finds nothing. km-presence runs as User=root,
// so a non-login runuser would fail to resolve the binary, Output would error,
// and this signal would fail idle PERMANENTLY — reaping every box with a live
// detached session. Note the negative-case test cannot catch that: a signal
// stuck at false passes it perfectly. A login shell picks up ~/.local/bin.
func checkHerdrPaneBusy(r commandRunner, configDir string) bool {
	for _, sock := range herdrSocketPaths(configDir) {
		out, err := r.Output("runuser", "-u", "sandbox", "--",
			"bash", "-lc", fmt.Sprintf("herdr pane list --json --socket %q", sock))
		if err != nil || len(bytes.TrimSpace(out)) == 0 {
			continue
		}
		var panes []herdrPane
		if err := json.Unmarshal(out, &panes); err != nil {
			continue
		}
		for _, p := range panes {
			// A pane whose foreground process group differs from its shell's is
			// running a foreground job. Herdr also reports the processes directly
			// when the platform exposes them; either is sufficient evidence.
			if len(p.ForegroundProcess) > 0 {
				return true
			}
			if p.ForegroundPGID != 0 && p.ShellPID != 0 && p.ForegroundPGID != p.ShellPID {
				return true
			}
		}
	}
	return false
}
```

Add `"encoding/json"` to `runner.go`'s imports.

- [ ] **Step 4: Extend `tick`**

In `cmd/km-presence/runner.go`, change `tick`'s signature and body:

```go
func tick(r commandRunner, sandboxID, mailDir, slackStampPath, presenceStampPath, herdrConfigDir string) (bool, bool) {
	s1 := checkLoginShells(r)
	s2 := checkTmuxClients(r)
	s3 := checkInboundEmail(mailDir, presenceStampPath)
	s4 := checkInboundSlack(slackStampPath, presenceStampPath)
	s5 := checkAgentProcess(r)
	s6 := checkVNCClients(r)
	s7 := checkSSHSessions(r)
	s8 := checkHerdrPaneBusy(r, herdrConfigDir)

	active := s1 || s2 || s3 || s4 || s5 || s6 || s7 || s8
```

Update the doc comment above `tick` from "seven signals" to "eight signals".

In `cmd/km-presence/main.go`: add `defaultHerdrConfigDir = "/home/sandbox/.config/herdr"` to the const block, pass it as the final argument to the `tick` call, and change the package doc comment at line 4 from "seven concrete signals" to "eight concrete signals".

Update the two existing `tick` call sites in `main_test.go` (`TestTick_NoEmitWhenAllNegative`, `TestTick_EmitWhenAnyPositive`) to pass `t.TempDir()` as the new argument — an empty directory has no socket, so signal 8 is negative and neither test's expectation changes.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./cmd/km-presence/ -v; echo "exit=$?"`
Expected: PASS, `exit=0`, with every signal-8 test green — above all `TestSignal_HerdrPaneBusy_NegativeAllIdle`.

- [ ] **Step 6: Commit**

```bash
git add cmd/km-presence/runner.go cmd/km-presence/main.go cmd/km-presence/main_test.go
git commit -m "feat(herdr): km-presence signal 8 keeps a busy detached herdr session alive"
```

---

### Task 8: Documentation

**Files:**
- Create: `docs/herdr-remote-attach.md`
- Modify: `CLAUDE.md` (new dated block; two "Where to look" rows; the `/opt/km/bin` helper table)
- Modify: `docs/desktop.md`, `docs/vscode.md` (seven → eight signals)

**Interfaces:**
- Consumes: everything above, including Task 6's recorded §6.4 decision.
- Produces: no code.

- [ ] **Step 1: Sweep the stale signal count**

Run: `grep -rn "seven signals\|seven concrete signals\|five signals" --include='*.md' --include='*.go' . | grep -v '^./.git'`

Update every hit to "eight" **except** historical narrative in CLAUDE.md that describes what was true at the time (e.g. the 2026-08-23 VNC/SSH block's "five original signals"), which is a record of a past state and must not be rewritten.

- [ ] **Step 2: Write `docs/herdr-remote-attach.md`**

Cover, in this order:
1. What `km herdr start` does and the two-terminal workflow.
2. Prerequisites: `spec.runtime.vscode.enabled` (default true, so sshd and the keypair already exist), and either `extends: [base/tools/herdr]` or the automatic ensure-install.
3. **Lifecycle, stated loudly** — Herdr sessions do not survive a reboot; `km pause` (hibernate) keeps panes, `km stop` kills them, and `teardownPolicy: stop` on idle is therefore the destructive path. This is why signal 8 exists.
4. Signal 8: what makes a pane count as busy, that a detached-and-quiet session is still reaped by design, and that a pre-signal-8 sandbox needs `km destroy && km create` (which `km herdr status` reports).
5. The `~/.ssh/config` conflict and the `manage_ssh_config = false` opt-out, including why km does not write it for you.
6. `pane_history` — off by default upstream, km does not enable it, and why (pane output at rest on the box includes secrets, tokens, and prompts).
7. Security posture: no new credential path, no new egress, the socket is the sandbox user's so panes are mutually inspectable on a `privileged: true` box, and km's egress enforcement/metering/census are all unaffected — unlike `km tunnel`.
8. Deploy surface, as the three-row table from spec §10.
9. Troubleshooting: herdr absent, `--no-install`, port collisions with vscode/tunnel, and a box reaped mid-detach.

- [ ] **Step 3: Add the CLAUDE.md block**

Add a dated block at the top of the phase-history section, in the established voice: what shipped, the non-obvious decisions (the fragment needs no create-handler rebuild because it changes no TEMPLATE and no SCHEMA — NOT because rendered userdata is unchanged, which is false: Phase 113 yaml.Marshals `InitCommands` into the `/opt/km/.km-profile.yaml` dump by reflection, so no template token names it and grepping the template misses it; signal 8 detects work not the server; `km herdr` is a vscode sibling not a tunnel mode and why), the lifecycle trap, the deploy surface (`make build` + `km init --sidecars`, **NOT** `km init --dry-run=false`), and that existing sandboxes need `km destroy && km create` for signal 8 only.

Add two "Where to look" rows:

```markdown
| Persistent agent panes that survive detach — `km herdr start`, the base/tools/herdr fragment, km-presence signal 8, the pause-vs-stop lifecycle trap, the ssh-config conflict, deploy surface | `docs/herdr-remote-attach.md` |
| Why a detached herdr session did or did not keep a sandbox awake — signal 8's busy-pane rule, why it detects work rather than the server, and why a quiet session is still reaped | `docs/herdr-remote-attach.md` § Signal 8 |
```

Add a row to the `/opt/km/bin` helper table:

```markdown
| **Terminal multiplexer** | `herdr` (in `/usr/local/bin`, not `/opt/km/bin`) | Third-party; mirrored from S3 by `km init --sidecars`, installed by `base/tools/herdr` or `km herdr start`. Attach from the operator's laptop with `herdr --remote km-<id>`. |
```

- [ ] **Step 4: Verify the docs match reality**

Run:
```bash
make build
./km herdr --help
./km herdr start --help
./km herdr status --help
```
Read each flag and default against what `docs/herdr-remote-attach.md` claims. Fix the doc, not the code, on any mismatch — the code is under test and the doc is not.

- [ ] **Step 5: Full suite**

Run: `go test ./... 2>&1 | tail -40; echo "GO_TEST_EXIT=${PIPESTATUS[0]}"`

Expected: `GO_TEST_EXIT=0` apart from the five known-red `Bootstrap`/`Cluster` tests that fail deterministically on a dev machine because of the AWS fast-fail seam. Check any FAIL against that known list before treating it as a regression.

- [ ] **Step 6: Commit**

```bash
git add docs/herdr-remote-attach.md CLAUDE.md docs/desktop.md docs/vscode.md
git commit -m "docs(herdr): operator runbook, CLAUDE.md block, eight-signal sweep"
```

---

### Task 9: Live UAT

**No task in this plan is verified until this one passes.** Spec §11 states it plainly: signal 8's negative case can only be trusted after watching a real detached session actually get reaped on schedule, and §6.4's parser was written against fixtures rather than a running system.

**Files:**
- Create: `.planning/phases/<N>-herdr-remote-attach/<N>-UAT.md`

- [ ] **Step 1: Deploy**

```bash
git pull --rebase
git log --oneline -1 HEAD && git log --oneline -1 origin/main   # must share a base
make build
km init --sidecars       # see the Global Constraints note: a non-zero exit here is
                         # EXPECTED in a worktree with no build/*.zip, and harmless
```

Confirm the mirror landed — this, not the exit code, is the check that matters:
```bash
aws s3 ls "s3://<artifacts-bucket>/binaries/herdr" --profile klanker-terraform
```

- [ ] **Step 2: Create a sandbox that extends the fragment**

Add `base/tools/herdr` to a test profile's `extends:` list, then:
```bash
km create <profile>.yaml herdruat --wait
./km herdr status herdruat
```
Expected: herdr reported present **without** an ensure-install (the fragment installed it at boot), and **no** pre-signal-8 warning.

- [ ] **Step 3: Prove the ensure-install path independently**

```bash
km shell herdruat --root      # then: rm -f /usr/local/bin/herdr
./km herdr status herdruat    # expected: non-zero exit, names `km herdr start`
./km herdr start herdruat     # expected: installs, then opens the forward
```

- [ ] **Step 4: Prove signal 8 positive (headless)**

Use a UAT profile with a SHORT `idleTimeout` — 10m makes this observable in one
sitting rather than four hours. Over `km shell`, as the `sandbox` user:

```bash
setsid herdr server >/tmp/herdr-server.log 2>&1 < /dev/null &
sleep 3
herdr workspace create --label uat
herdr pane run <pane-target> "sleep 3600"
```

Nothing is attached — this is precisely the detached-busy state signal 8 exists
for. Then watch:

```bash
km logs herdruat --follow --stream <presence-stream>
```

Expected: heartbeats continue past `idleTimeout` and the box stays running.
**Then cross-check that signal 8 is the one carrying it** — stop the pane's job
and confirm heartbeats stop. A box kept alive by signal 1 or signal 5 proves
nothing about signal 8, and that confusion is the easiest way to pass this UAT
while shipping a dead signal.

- [ ] **Step 5: Prove signal 8 negative — the one that matters**

Kill the `sleep`, leave the headless Herdr server running with only idle panes,
and wait longer than `idleTimeout`. Close every `km shell` session first: a live
shell holds the box awake through signal 1 and would mask the result.

Expected: heartbeats stop, and the box is stopped on schedule. **If it stays
alive, signal 8 cannot go negative and idle teardown is broken fleet-wide —
stop and fix before merging.**

- [ ] **Step 5b: OPERATOR-RUN — interactive attach round-trip**

Everything above drives Herdr headlessly. One claim remains unverified by
automation: that an interactive `herdr --remote km-<id>` attach, a `ctrl+b q`
detach, and a reattach behave identically to a headless server. Record it in the
UAT file as **OPERATOR-RUN, NOT YET VERIFIED**, and state the same limitation in
the PR body rather than implying the signal was proven against a hand-attached
session.

- [ ] **Step 6: Prove the lifecycle claim**

```bash
km pause herdruat && km resume herdruat
```
Reattach; expected: panes and their processes survive (hibernation preserves RAM).

```bash
km stop herdruat && km resume herdruat
```
Reattach; expected: pane processes are **gone**, confirming the doc's warning.

- [ ] **Step 7: Record the UAT and tear down**

Write the UAT file with each step's actual observed output, including anything
that disagreed with the plan. Then:

```bash
km destroy herdruat --remote --yes
git add .planning/phases/<N>-herdr-remote-attach/
git commit -m "docs(herdr): live UAT record"
```

---

## Notes for the executor

- **Rebase first.** Another agent is merging a PR in this repo. Run `git pull --rebase` before Task 1 and again before Task 9. Never run a bare `git add` — every commit in this plan names explicit paths, because a bare add sweeps another agent's staged files.
- **Task 6 blocks Task 7.** The fixtures are ground truth for the parser. Do not write `herdrPane`'s JSON tags from the API documentation. If Task 6 cannot start a headless server, Task 7 does not get written — skip to Task 8 and ship Tasks 1-5 plus docs.
- **Tasks 1–5 are independently useful** and ship a working `km herdr start` on existing sandboxes. Tasks 6–9 are what make the persistence promise real.
- **Never run `km init --dry-run=false` for this work.** Nothing here touches Terraform, IAM, a Lambda, or the userdata template.
