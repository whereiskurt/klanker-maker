# Brokered Secret Unsealing — Wave 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop materialising SOPS secrets into every login shell; deliver them on demand, per request, into exactly the one process that needs them.

**Architecture:** A root daemon (`km-secretsd`) holds decrypt authority and answers a unix-socket RPC, decrypting live on every request and zeroing its buffers after. A sandbox-side CLI (`km-env`) asks the broker and `execve`s a single command with the returned keys in its environment. Root-owned shims in `/opt/km/shims`, first on PATH, route `claude`/`codex` through `km-env` so the agent process — not the turn shell — is what receives secrets. `/etc/sandbox-secrets.env` and `/etc/profile.d/zz-sandbox-secrets.sh` are deleted.

**Tech Stack:** Go 1.x (`github.com/whereiskurt/klanker-maker`), `github.com/getsops/sops/v3/decrypt`, `golang.org/x/sys/unix` (SO_PEERCRED), systemd, bash userdata templating (`text/template`).

**Spec:** `docs/superpowers/specs/2026-09-04-brokered-secret-unsealing-design.md`

## Global Constraints

- **Wave 1 only.** `spec.secrets.fenceIMDS`, `km-creds`, the iptables rule, `ec2spot/v1.7.0` and selftest assertion 6 are **Wave 2** and are out of scope here. Do not add them.
- **No `apiVersion` bump.** `spec.secrets.grants` is purely additive; profiles stay `klankermaker.ai/v1alpha2`.
- **Sidecars cross-compile with `CGO_ENABLED=0`, linux/amd64.** If `getsops/sops/v3/decrypt` will not build under it, use the exec fallback (Task 3) and record it as debt.
- **Secret values are `[]byte`, never `string`,** everywhere the broker or `km-env` controls them. Go strings cannot be zeroed.
- **Byte-identity rule:** every profile *without* `spec.secrets.sopsFile` must produce byte-identical userdata. All new template output is gated on `.SopsBundlePresent`.
- **Never edit a frozen golden by re-capturing it.** The pre-92 baseline strips `SubagentStop`; `CAPTURE_PRE92_BASELINE=1` writes the unstripped output and corrupts it. Hand-patch that one if it moves.
- **Never edit an existing `infra/modules/*/vN.N.N` directory in place.** Not needed in Wave 1 — there is no IAM change.
- **Test new profile fields through YAML → `ValidateSchema`,** never by setting the struct directly. A struct-level test passes while the field is absent from the JSON schema and therefore dead.
- **Check `go test` exit codes directly.** `go test ... | tail` returns tail's 0 and masks a FAIL.
- **Commit with pathspecs**, never a bare `git add -A`. Other sessions share this working tree.

---

## File Structure

**Create:**
- `pkg/secrets/protocol.go` — wire types + on-box path constants. Shared by daemon and CLI; no I/O, no AWS.
- `pkg/secrets/grants.go` — pure grant resolution (`Resolve`). No I/O.
- `pkg/secrets/grants_test.go`
- `pkg/secrets/wiring_guard_test.go` — mechanical guard over `userdata.go`.
- `cmd/km-secretsd/main.go` — verb dispatch (`serve`, `selftest`).
- `cmd/km-secretsd/bundle.go` — decrypt + zeroable `Bundle`.
- `cmd/km-secretsd/bundle_test.go`
- `cmd/km-secretsd/server.go` — socket listener, `Unseal` handling. Portable.
- `cmd/km-secretsd/peercred_linux.go` — `SO_PEERCRED` read. `//go:build linux`.
- `cmd/km-secretsd/peercred_other.go` — non-Linux stub. `//go:build !linux`.
- `cmd/km-secretsd/server_test.go`
- `cmd/km-secretsd/audit.go` — `secret_unseal` / `secret_selftest` events to `/run/km/audit-pipe`.
- `cmd/km-secretsd/audit_test.go`
- `cmd/km-secretsd/selftest.go` — boot assertions.
- `cmd/km-secretsd/selftest_test.go`
- `cmd/km-env/main.go` — `exec` / `list` verbs.
- `cmd/km-env/main_test.go`
- `docs/brokered-secrets.md` — operator runbook.

**Modify:**
- `pkg/profile/types.go` — `SecretsSpec.Grants`.
- `pkg/profile/schemas/sandbox_profile.schema.json` — `grants` property.
- `pkg/profile/validate.go` — grants warnings.
- `pkg/compiler/userdata.go` — delete env injection; add daemon, shims, PATH prepends, boot check.
- `internal/app/cmd/init.go` — two `sidecarBuilds()` entries.
- `docs/sandbox-secrets.md` — supersession note.
- `CLAUDE.md` — phase block.

**Responsibility boundaries:** `pkg/secrets` is pure and importable by anything (including tests on macOS). All Linux-only syscall work is confined to the two `peercred_*.go` files, so the
rest of the daemon compiles and tests on a dev machine. All AWS/KMS work is confined to `cmd/km-secretsd/bundle.go`. `cmd/km-env` never decrypts and never touches AWS — it is a socket client and an `execve`.

---

### Task 1: Shared protocol and grant resolution

Pure, dependency-free foundation. Everything else imports this.

**Files:**
- Create: `pkg/secrets/protocol.go`
- Create: `pkg/secrets/grants.go`
- Test: `pkg/secrets/grants_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `secrets.SocketPath`, `secrets.CiphertextPath`, `secrets.ShimDir`, `secrets.AuditPipePath`, `secrets.DefaultConsumers`, `secrets.Grants` (`map[string][]string`), `secrets.UnsealRequest{As string; Only []string}`, `secrets.UnsealResponse{Keys []string; Values map[string][]byte; Error string}`, `secrets.Resolve(available []string, grants Grants, as string, only []string) ([]string, error)`, `secrets.ErrUnknownConsumer`.

- [ ] **Step 1: Write the failing test**

```go
package secrets_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

func TestResolve(t *testing.T) {
	all := []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "DB_PASSWORD"}

	tests := []struct {
		name    string
		grants  secrets.Grants
		as      string
		only    []string
		want    []string
		wantErr error
	}{
		{
			// Migration case: today's behaviour, scoped to one process.
			name: "no grants and no identity yields the whole bundle",
			want: []string{"ANTHROPIC_API_KEY", "DB_PASSWORD", "OPENAI_API_KEY"},
		},
		{
			name: "no grants means an identity is not narrowing",
			as:   "claude",
			want: []string{"ANTHROPIC_API_KEY", "DB_PASSWORD", "OPENAI_API_KEY"},
		},
		{
			name:   "a granted identity receives only its keys",
			grants: secrets.Grants{"claude": {"ANTHROPIC_API_KEY"}},
			as:     "claude",
			want:   []string{"ANTHROPIC_API_KEY"},
		},
		{
			// An identity nobody granted is an error, not a silent full bundle.
			name:    "an ungranted identity is rejected once grants exist",
			grants:  secrets.Grants{"claude": {"ANTHROPIC_API_KEY"}},
			as:      "codex",
			wantErr: secrets.ErrUnknownConsumer,
		},
		{
			name:   "only narrows within a grant",
			grants: secrets.Grants{"claude": {"ANTHROPIC_API_KEY", "DB_PASSWORD"}},
			as:     "claude",
			only:   []string{"DB_PASSWORD"},
			want:   []string{"DB_PASSWORD"},
		},
		{
			// The load-bearing property: --only intersects, it never widens.
			name:   "only cannot widen a grant",
			grants: secrets.Grants{"claude": {"ANTHROPIC_API_KEY"}},
			as:     "claude",
			only:   []string{"DB_PASSWORD"},
			want:   []string{},
		},
		{
			name: "only narrows the full bundle when no grants exist",
			only: []string{"DB_PASSWORD"},
			want: []string{"DB_PASSWORD"},
		},
		{
			// A grant may name a key a later bundle revision dropped.
			name:   "a granted key absent from the bundle is dropped",
			grants: secrets.Grants{"claude": {"ANTHROPIC_API_KEY", "GONE"}},
			as:     "claude",
			want:   []string{"ANTHROPIC_API_KEY"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := secrets.Resolve(all, tc.grants, tc.as, tc.only)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Resolve() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveIsSorted(t *testing.T) {
	// Deterministic order keeps audit events diffable across unseals.
	got, err := secrets.Resolve([]string{"Z", "A", "M"}, nil, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"A", "M", "Z"}) {
		t.Errorf("Resolve() = %v, want sorted", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/secrets/ -run TestResolve -v; echo "exit=$?"`
Expected: FAIL — `package github.com/whereiskurt/klanker-maker/pkg/secrets is not in std` or undefined identifiers.

- [ ] **Step 3: Write the implementation**

`pkg/secrets/protocol.go`:

```go
// Package secrets carries the wire contract and grant arithmetic shared by
// km-secretsd (the root broker) and km-env (the sandbox-side client).
//
// It is deliberately pure: no AWS, no syscalls, no I/O. That keeps it testable
// on a dev machine, which the Linux-only broker internals are not.
package secrets

// On-box paths. These are the contract between the compiler-rendered userdata
// and the two binaries; changing one without the others silently breaks boot.
const (
	// SocketPath is the broker's unix socket, 0660 root:sandbox.
	SocketPath = "/run/km/secrets.sock"

	// CiphertextPath is the SOPS bundle at rest, 0400 root. Only ciphertext
	// is ever stored; there is no plaintext file anywhere on the box.
	CiphertextPath = "/etc/sandbox-secrets.enc.yaml"

	// ShimDir holds the generated per-consumer shims and is prepended to PATH.
	ShimDir = "/opt/km/shims"

	// AuditPipePath is the km-audit-log sidecar's input FIFO (mode 0666).
	AuditPipePath = "/run/km/audit-pipe"
)

// DefaultConsumers is the consumer set when a profile declares no grants.
// These get shims and, absent grants, the whole bundle.
var DefaultConsumers = []string{"claude", "codex"}

// Grants maps a consumer name to the keys it may receive. A key is BOTH the
// binary name intercepted on PATH and the identity presented to the broker.
type Grants map[string][]string

// UnsealRequest is the client's ask. As is the claimed consumer identity;
// Only narrows further and can never widen.
type UnsealRequest struct {
	As   string   `json:"as,omitempty"`
	Only []string `json:"only,omitempty"`
}

// UnsealResponse carries the values, or an Error string when the request was
// refused. Values are []byte so the broker can zero them; see bundle.go.
type UnsealResponse struct {
	Keys   []string          `json:"keys,omitempty"`
	Values map[string][]byte `json:"values,omitempty"`
	Error  string            `json:"error,omitempty"`
}
```

`pkg/secrets/grants.go`:

```go
package secrets

import (
	"errors"
	"fmt"
	"sort"
)

// ErrUnknownConsumer is returned when a caller claims an identity that the
// profile's grants map does not define. Returning the full bundle instead
// would make --as silently meaningless, which is worse than a refusal.
var ErrUnknownConsumer = errors.New("secrets: unknown consumer")

// Resolve computes the effective key set for one unseal.
//
// Precedence, narrowing only at every step:
//
//	base = available                     when grants is empty, or as is ""
//	base = available ∩ grants[as]        when as names a granted consumer
//	base = error                         when grants is non-empty and as is not in it
//	result = base ∩ only                 when only is non-empty
//
// `only` intersects and never widens: naming a key outside the identity's
// grant does not obtain it. This is the same disposition as km-netpolicy deny
// and pin — every runtime lever in this platform tightens and none loosens.
//
// The result is sorted so audit events are diffable across unseals.
func Resolve(available []string, grants Grants, as string, only []string) ([]string, error) {
	have := make(map[string]bool, len(available))
	for _, k := range available {
		have[k] = true
	}

	base := available
	if len(grants) > 0 && as != "" {
		granted, ok := grants[as]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnknownConsumer, as)
		}
		base = granted
	}

	allowed := make(map[string]bool, len(base))
	for _, k := range base {
		if have[k] { // a grant may name a key a later bundle revision dropped
			allowed[k] = true
		}
	}

	if len(only) > 0 {
		narrowed := make(map[string]bool, len(only))
		for _, k := range only {
			if allowed[k] {
				narrowed[k] = true
			}
		}
		allowed = narrowed
	}

	out := make([]string, 0, len(allowed))
	for k := range allowed {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/secrets/ -v; echo "exit=$?"`
Expected: PASS, `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add pkg/secrets/protocol.go pkg/secrets/grants.go pkg/secrets/grants_test.go
git commit -m "feat(secrets): wire protocol and narrowing-only grant resolution"
```

---

### Task 2: Profile schema — `spec.secrets.grants`

**Files:**
- Modify: `pkg/profile/types.go` (`SecretsSpec`, ~line 863)
- Modify: `pkg/profile/schemas/sandbox_profile.schema.json` (`spec.properties.secrets`)
- Modify: `pkg/profile/validate.go`
- Test: `pkg/profile/secrets_grants_test.go` (create)

**Interfaces:**
- Consumes: `secrets.Grants` from Task 1.
- Produces: `profile.SecretsSpec.Grants map[string][]string` (yaml/json tag `grants`).

- [ ] **Step 1: Write the failing test**

Create `pkg/profile/secrets_grants_test.go`. **These must go through YAML → schema validation**, because a struct-level test passes while the field is absent from the JSON schema and therefore dead.

```go
package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

const grantsProfileHeader = `apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: grants-test
spec:
  runtime:
    substrate: ec2spot
    instanceType: t3.medium
  execution:
    workdir: /workspace
  secrets:
`

// blocking filters out IsWarning entries. ValidateSchema and Validate both
// return []ValidationError, never error — warnings ride in the same slice.
func blocking(errs []profile.ValidationError) []profile.ValidationError {
	var out []profile.ValidationError
	for _, e := range errs {
		if !e.IsWarning {
			out = append(out, e)
		}
	}
	return out
}

func TestSecretsGrants_AcceptedBySchema(t *testing.T) {
	y := grantsProfileHeader + `    sopsFile: ./secrets/x.enc.yaml
    grants:
      claude: [ANTHROPIC_API_KEY]
      codex: [OPENAI_API_KEY]
`
	if errs := blocking(profile.ValidateSchema([]byte(y))); len(errs) > 0 {
		t.Fatalf("schema rejected a valid grants block: %+v", errs)
	}
	p, err := profile.Parse([]byte(y))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := p.Spec.Secrets.Grants["claude"]; len(got) != 1 || got[0] != "ANTHROPIC_API_KEY" {
		t.Errorf("Grants[claude] = %v, want [ANTHROPIC_API_KEY]", got)
	}
}

func TestSecretsGrants_AbsentIsValid(t *testing.T) {
	y := grantsProfileHeader + `    sopsFile: ./secrets/x.enc.yaml
`
	if errs := blocking(profile.ValidateSchema([]byte(y))); len(errs) > 0 {
		t.Fatalf("schema rejected a grants-less profile: %+v", errs)
	}
	p, err := profile.Parse([]byte(y))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Spec.Secrets.Grants != nil {
		t.Errorf("Grants = %v, want nil when absent", p.Spec.Secrets.Grants)
	}
}

func TestSecretsGrants_WrongShapeRejected(t *testing.T) {
	// A bare string instead of a list is the likely typo. additionalProperties
	// on the grants object must constrain the VALUE to an array of strings.
	y := grantsProfileHeader + `    sopsFile: ./secrets/x.enc.yaml
    grants:
      claude: ANTHROPIC_API_KEY
`
	errs := blocking(profile.ValidateSchema([]byte(y)))
	if len(errs) == 0 {
		t.Fatal("schema accepted a scalar where a list is required")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Path, "grants") || strings.Contains(e.Message, "grants") {
			found = true
		}
	}
	if !found {
		t.Errorf("error should name the offending path, got %+v", errs)
	}
}

func TestSecretsGrants_WithoutSopsFileWarns(t *testing.T) {
	// grants without a bundle cannot do anything: warn, never block.
	y := grantsProfileHeader + `    grants:
      claude: [ANTHROPIC_API_KEY]
`
	all := profile.Validate([]byte(y))
	found := false
	for _, e := range all {
		if e.IsWarning && strings.Contains(e.Path, "secrets.grants") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a warning on spec.secrets.grants, got %+v", all)
	}
	// It must be a WARNING, not a blocking error.
	for _, e := range blocking(all) {
		if strings.Contains(e.Path, "secrets.grants") {
			t.Errorf("grants-without-sopsFile blocked validation: %+v", e)
		}
	}
}
```

> **API note (preflight correction):** `pkg/profile` has **no** `ParseYAML` and
> **no** `ValidateWarnings`. The real API is `profile.Parse(data []byte)
> (*SandboxProfile, error)`, and both `profile.ValidateSchema(raw []byte)` and
> `profile.Validate(raw []byte)` return `[]ValidationError` — never `error`.
> Warnings are entries in that same slice carrying `IsWarning: true`. Use the
> names above verbatim.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/profile/ -run TestSecretsGrants -v; echo "exit=$?"`
Expected: FAIL — `p.Spec.Secrets.Grants` undefined.

- [ ] **Step 3: Write the implementation**

In `pkg/profile/types.go`, extend `SecretsSpec`:

```go
type SecretsSpec struct {
	// SopsFile is a path (relative to the profile YAML location) to a
	// SOPS-encrypted YAML bundle. Reserved keys "sops" and "_meta" are ignored.
	// Empty (the zero value) means no secret injection.
	//
	// Phase 133: the bundle is no longer decrypted into /etc/sandbox-secrets.env
	// at boot. km-secretsd decrypts it per request and km-env injects the result
	// into one child process. See docs/brokered-secrets.md.
	SopsFile string `yaml:"sopsFile,omitempty" json:"sopsFile,omitempty"`

	// Grants maps a consumer to the bundle keys it may receive (Phase 133).
	//
	// A key is BOTH the binary name intercepted on PATH (a shim is generated at
	// /opt/km/shims/<name>) and the identity presented to the broker. Absent
	// means claude and codex each receive the whole bundle — the identical
	// effective grant to Phase 89, merely scoped to one process rather than
	// every login shell.
	//
	// Grants are blast-radius hygiene and audit legibility, NOT containment:
	// anything running as the sandbox user can speak the broker protocol
	// directly. See the design doc section 6.
	Grants map[string][]string `yaml:"grants,omitempty" json:"grants,omitempty"`
}
```

In `pkg/profile/schemas/sandbox_profile.schema.json`, replace the `secrets` block's `properties` with:

```json
{
  "sopsFile": {
    "type": "string",
    "description": "Path (relative to profile YAML) to a SOPS-encrypted YAML bundle (.enc.yaml). Phase 133: decrypted on demand by km-secretsd, never written to disk in plaintext."
  },
  "grants": {
    "type": "object",
    "description": "Phase 133: maps a consumer name to the bundle keys it may receive. The name is both the binary intercepted on PATH and the identity presented to the broker. Absent means claude and codex each receive the whole bundle.",
    "additionalProperties": {
      "type": "array",
      "items": { "type": "string" }
    }
  }
}
```

In `pkg/profile/validate.go`, add a rule beside the existing Slack warning rules
(they are the pattern to copy — `errs` is the accumulator and warnings ride in it
with `IsWarning: true`):

```go
// Rule secrets-grants (warning, Phase 133): grants without a bundle is inert —
// there is nothing to grant.
if p.Spec.Secrets != nil && len(p.Spec.Secrets.Grants) > 0 && p.Spec.Secrets.SopsFile == "" {
	errs = append(errs, ValidationError{
		Path:      "spec.secrets.grants",
		Message:   "spec.secrets.grants has no effect when spec.secrets.sopsFile is empty (there is no bundle to grant from)",
		IsWarning: true,
	})
}
```

Place it where the profile struct is already in scope; follow the surrounding
guard style for how `p` is obtained in that function.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./pkg/profile/ -run TestSecretsGrants -v; echo "exit=$?"`
Expected: PASS, `exit=0`.

Then confirm nothing else regressed:
Run: `go test ./pkg/profile/; echo "exit=$?"`
Expected: `exit=0`.

Then the profile inventory gate:
Run: `./scripts/validate-all-profiles.sh; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add pkg/profile/types.go pkg/profile/schemas/sandbox_profile.schema.json \
        pkg/profile/validate.go pkg/profile/secrets_grants_test.go
git commit -m "feat(profile): spec.secrets.grants — per-consumer key subsets"
```

---

### Task 3: Zeroable bundle and SOPS decrypt

**Files:**
- Create: `cmd/km-secretsd/bundle.go`
- Test: `cmd/km-secretsd/bundle_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `type Bundle struct{...}`, `func LoadBundle(path string) (*Bundle, error)`, `(*Bundle).Keys() []string`, `(*Bundle).Get(key string) []byte`, `(*Bundle).Zero()`, `var decryptFile func(path, format string) ([]byte, error)` (test seam).

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"os"
	"reflect"
	"testing"
)

// stubDecrypt swaps the sops call out so these tests need no KMS and no age key.
func stubDecrypt(t *testing.T, yaml string, err error) {
	t.Helper()
	prev := decryptFile
	decryptFile = func(path, format string) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		return []byte(yaml), nil
	}
	t.Cleanup(func() { decryptFile = prev })
}

func writeCipher(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	if err := os.WriteFile(p, []byte("sops: {}\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadBundle_TopLevelKeys(t *testing.T) {
	stubDecrypt(t, "ANTHROPIC_API_KEY: sk-ant-xyz\nOPENAI_API_KEY: sk-oai-abc\n", nil)
	b, err := LoadBundle(writeCipher(t))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	defer b.Zero()

	if got := b.Keys(); !reflect.DeepEqual(got, []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY"}) {
		t.Errorf("Keys() = %v", got)
	}
	if got := b.Get("ANTHROPIC_API_KEY"); !bytes.Equal(got, []byte("sk-ant-xyz")) {
		t.Errorf("Get() = %q", got)
	}
}

func TestLoadBundle_DropsSopsMetadata(t *testing.T) {
	// sops embeds its own metadata; it is not a secret and must never be served.
	stubDecrypt(t, "API_KEY: v\nsops:\n  kms: []\n_meta: whatever\n", nil)
	b, err := LoadBundle(writeCipher(t))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	defer b.Zero()

	if got := b.Keys(); !reflect.DeepEqual(got, []string{"API_KEY"}) {
		t.Errorf("Keys() = %v, want only API_KEY", got)
	}
}

func TestLoadBundle_NonStringScalarsStringified(t *testing.T) {
	stubDecrypt(t, "PORT: 5432\nDEBUG: true\n", nil)
	b, err := LoadBundle(writeCipher(t))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	defer b.Zero()

	if got := b.Get("PORT"); !bytes.Equal(got, []byte("5432")) {
		t.Errorf("PORT = %q, want 5432", got)
	}
	if got := b.Get("DEBUG"); !bytes.Equal(got, []byte("true")) {
		t.Errorf("DEBUG = %q, want true", got)
	}
}

func TestZero_OverwritesValues(t *testing.T) {
	// The whole point: values are []byte precisely so they CAN be overwritten.
	stubDecrypt(t, "API_KEY: supersecret\n", nil)
	b, err := LoadBundle(writeCipher(t))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	v := b.Get("API_KEY")
	b.Zero()

	if bytes.Contains(v, []byte("supersecret")) {
		t.Error("Zero() left plaintext in the backing array")
	}
	if len(b.Keys()) != 0 {
		t.Error("Zero() left keys behind")
	}
}

func TestLoadBundle_DecryptErrorPropagates(t *testing.T) {
	// Fail closed and loudly; never return a partial or empty bundle as success.
	sentinel := errors.New("kms 403")
	stubDecrypt(t, "", sentinel)
	if _, err := LoadBundle(writeCipher(t)); err == nil {
		t.Fatal("LoadBundle succeeded despite a decrypt failure")
	}
}

func TestLoadBundle_MissingCiphertext(t *testing.T) {
	stubDecrypt(t, "API_KEY: v\n", nil)
	if _, err := LoadBundle(filepath.Join(t.TempDir(), "absent.enc.yaml")); err == nil {
		t.Fatal("LoadBundle succeeded with no ciphertext on disk")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/km-secretsd/ -v; echo "exit=$?"`
Expected: FAIL — `LoadBundle` / `decryptFile` undefined.

- [ ] **Step 3: Write the implementation**

```go
package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/getsops/sops/v3/decrypt"
	"gopkg.in/yaml.v3"
)

// decryptFile is the sops entry point, as a variable so tests can stub it.
// Using the Go API rather than shelling to /opt/km/bin/sops is deliberate: the
// plaintext never transits a pipe or a second process's memory, and the buffer
// stays ours to zero.
//
// If getsops/sops/v3/decrypt will not build under CGO_ENABLED=0 (required for
// the sidecar cross-compile), replace this with an exec of /opt/km/bin/sops and
// record it as debt — the zeroing guarantee then covers only our side of the pipe.
var decryptFile = decrypt.File

// reservedKeys are sops' own metadata, never secrets, never served.
var reservedKeys = map[string]bool{"sops": true, "_meta": true}

// Bundle holds decrypted secrets as []byte so they can actually be overwritten.
//
// Go strings are immutable and may be copied freely by the runtime, so a
// map[string]string cannot be zeroed at all and the claim would be decorative.
// Zeroing here covers the decrypted YAML buffer and these per-key values; it
// cannot cover a response already serialised onto a socket, nor the environment
// of the child process.
type Bundle struct {
	vals map[string][]byte
	raw  []byte // the decrypted YAML, retained solely so Zero can overwrite it
}

// LoadBundle decrypts the SOPS bundle at path. The caller MUST call Zero.
func LoadBundle(path string) (*Bundle, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("ciphertext unreadable at %s: %w", path, err)
	}

	plain, err := decryptFile(path, "yaml")
	if err != nil {
		return nil, fmt.Errorf("sops decrypt %s: %w", path, err)
	}

	var doc map[string]any
	if err := yaml.Unmarshal(plain, &doc); err != nil {
		zero(plain)
		return nil, fmt.Errorf("parse decrypted bundle: %w", err)
	}

	b := &Bundle{vals: make(map[string][]byte, len(doc)), raw: plain}
	for k, v := range doc {
		if reservedKeys[k] {
			continue
		}
		switch v.(type) {
		case map[string]any, []any, nil:
			// Only top-level scalars become env vars, matching the Phase 89
			// --output-type dotenv behaviour this replaces.
			continue
		}
		b.vals[k] = []byte(fmt.Sprint(v))
	}
	return b, nil
}

// Keys returns the bundle's key names, sorted. Names only — never values.
func (b *Bundle) Keys() []string {
	out := make([]string, 0, len(b.vals))
	for k := range b.vals {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Get returns the value for key, or nil. The slice aliases the bundle's
// storage: it is invalid after Zero.
func (b *Bundle) Get(key string) []byte { return b.vals[key] }

// Zero overwrites every value and the decrypted YAML buffer, then drops them.
func (b *Bundle) Zero() {
	for k, v := range b.vals {
		zero(v)
		delete(b.vals, k)
	}
	zero(b.raw)
	b.raw = nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go mod tidy && go test ./cmd/km-secretsd/ -v; echo "exit=$?"`
Expected: PASS, `exit=0`.

Then prove the Global Constraint holds:
Run: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/km-secretsd/; echo "exit=$?"`
Expected: `exit=0`. **If this fails**, switch `decryptFile` to an exec of `/opt/km/bin/sops decrypt --output-type yaml <path>`, keep every test passing unchanged, and note the debt in the commit body.

- [ ] **Step 5: Commit**

```bash
git add cmd/km-secretsd/bundle.go cmd/km-secretsd/bundle_test.go go.mod go.sum
git commit -m "feat(km-secretsd): zeroable bundle backed by the sops Go API"
```

---

### Task 4: Audit emission

**Files:**
- Create: `cmd/km-secretsd/audit.go`
- Test: `cmd/km-secretsd/audit_test.go`

**Interfaces:**
- Consumes: `secrets.AuditPipePath` (Task 1).
- Produces: `type AuditWriter interface{ Emit(eventType string, detail map[string]any) error }`, `type PipeAudit struct{ Path, SandboxID string }`, `func (*PipeAudit) Emit(string, map[string]any) error`, `type NopAudit struct{}`.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPipeAudit_EmitsCanonicalSchema(t *testing.T) {
	// A regular file stands in for the FIFO: the writer must not care.
	p := filepath.Join(t.TempDir(), "audit-pipe")
	a := &PipeAudit{Path: p, SandboxID: "sb-a1b2c3d4"}

	if err := a.Emit("secret_unseal", map[string]any{
		"as":   "claude",
		"keys": []string{"ANTHROPIC_API_KEY"},
		"pid":  4242,
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var ev struct {
		Timestamp string         `json:"timestamp"`
		SandboxID string         `json:"sandbox_id"`
		EventType string         `json:"event_type"`
		Source    string         `json:"source"`
		Detail    map[string]any `json:"detail"`
	}
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("audit line is not JSON: %v (%q)", err, raw)
	}
	if ev.SandboxID != "sb-a1b2c3d4" || ev.EventType != "secret_unseal" || ev.Source != "km-secretsd" {
		t.Errorf("bad envelope: %+v", ev)
	}
	if ev.Timestamp == "" {
		t.Error("timestamp missing")
	}
	if ev.Detail["as"] != "claude" {
		t.Errorf("detail lost: %+v", ev.Detail)
	}
	if raw[len(raw)-1] != '\n' {
		t.Error("audit line must be newline-terminated JSONL")
	}
}

func TestPipeAudit_MissingPipeIsNotFatal(t *testing.T) {
	// Losing an audit line must never cost an agent turn. The unseal is the
	// product; the record is best-effort, exactly like uploadCapture.
	a := &PipeAudit{Path: filepath.Join(t.TempDir(), "nope", "audit-pipe"), SandboxID: "sb-1"}
	if err := a.Emit("secret_unseal", nil); err != nil {
		t.Errorf("Emit returned an error for an absent pipe: %v", err)
	}
}

func TestPipeAudit_NeverLogsValues(t *testing.T) {
	// Guard against a future refactor passing values instead of names.
	p := filepath.Join(t.TempDir(), "audit-pipe")
	a := &PipeAudit{Path: p, SandboxID: "sb-1"}
	if err := a.Emit("secret_unseal", map[string]any{"keys": []string{"API_KEY"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if len(raw) == 0 {
		t.Fatal("nothing written")
	}
	// The detail map is the only channel; assert the writer adds nothing itself.
	var ev map[string]any
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatal(err)
	}
	if _, bad := ev["values"]; bad {
		t.Error("audit envelope carries a values field")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/km-secretsd/ -run TestPipeAudit -v; echo "exit=$?"`
Expected: FAIL — `PipeAudit` undefined.

- [ ] **Step 3: Write the implementation**

```go
package main

import (
	"encoding/json"
	"os"
	"time"
)

// AuditWriter records what the broker did. Every unseal is logged because the
// broker's value is that theft becomes an active, attributable, recorded act
// rather than a passive scoop that leaves no trace.
type AuditWriter interface {
	Emit(eventType string, detail map[string]any) error
}

// PipeAudit writes JSONL to the km-audit-log sidecar's input FIFO, which ships
// it to the sandbox's CloudWatch audit stream. The envelope is the schema
// locked in sidecars/audit-log/auditlog.go.
type PipeAudit struct {
	Path      string
	SandboxID string
}

// Emit writes one event. It is BEST-EFFORT by construction: an absent or
// blocked pipe returns nil, because losing an audit line must never cost an
// agent turn. Detail carries key NAMES only; values never reach this path.
func (a *PipeAudit) Emit(eventType string, detail map[string]any) error {
	if detail == nil {
		detail = map[string]any{}
	}
	line, err := json.Marshal(map[string]any{
		"timestamp":  time.Now().UTC().Format(time.RFC3339),
		"sandbox_id": a.SandboxID,
		"event_type": eventType,
		"source":     "km-secretsd",
		"detail":     detail,
	})
	if err != nil {
		return nil
	}

	f, err := os.OpenFile(a.Path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
	return nil
}

// NopAudit discards events. Used in tests and when no pipe is configured.
type NopAudit struct{}

// Emit discards the event.
func (NopAudit) Emit(string, map[string]any) error { return nil }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/km-secretsd/ -v; echo "exit=$?"`
Expected: PASS, `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add cmd/km-secretsd/audit.go cmd/km-secretsd/audit_test.go
git commit -m "feat(km-secretsd): best-effort secret_unseal audit to the audit pipe"
```

---

### Task 5: The broker server

**Files:**
- Create: `cmd/km-secretsd/server.go`
- Create: `cmd/km-secretsd/peercred_linux.go` (`//go:build linux`)
- Create: `cmd/km-secretsd/peercred_other.go` (`//go:build !linux`)
- Create: `cmd/km-secretsd/main.go`
- Test: `cmd/km-secretsd/server_test.go`

**Interfaces:**
- Consumes: `secrets.UnsealRequest`/`UnsealResponse`/`Resolve`/`Grants` (Task 1), `LoadBundle`/`Bundle` (Task 3), `AuditWriter` (Task 4).
- Produces: `type Server struct{ CiphertextPath string; Grants secrets.Grants; Audit AuditWriter }`, `func (*Server) Handle(conn net.Conn, uid, pid uint32)`, `func (*Server) Serve(ctx context.Context, ln net.Listener) error`.

> **Keep the build tag as narrow as the platform dependency.** Only the
> `SO_PEERCRED` read is Linux-specific, so it lives alone in `peercred_linux.go`
> with a `!linux` stub beside it. Tagging the whole server would stop
> `cmd/km-secretsd` compiling on macOS and silently take Tasks 3 and 7's tests
> out of the dev-machine run — the `pkg/ebpf/audit` shape that let the Phase 132
> endianness bug survive. Step 4 still runs everything under Docker, because the
> real peer-credential path can only be exercised on Linux.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

type recordingAudit struct{ events []map[string]any }

func (r *recordingAudit) Emit(t string, d map[string]any) error {
	e := map[string]any{"event_type": t}
	for k, v := range d {
		e[k] = v
	}
	r.events = append(r.events, e)
	return nil
}

func serverWith(t *testing.T, yaml string, g secrets.Grants) (*Server, *recordingAudit) {
	t.Helper()
	stubDecrypt(t, yaml, nil)
	aud := &recordingAudit{}
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	if err := osWriteFile(p); err != nil {
		t.Fatal(err)
	}
	return &Server{CiphertextPath: p, Grants: g, Audit: aud}, aud
}

// roundTrip runs one request through Handle over a socketpair.
func roundTrip(t *testing.T, s *Server, req secrets.UnsealRequest, uid, pid uint32) secrets.UnsealResponse {
	t.Helper()
	c1, c2 := net.Pipe()
	go func() {
		defer c2.Close()
		s.Handle(c2, uid, pid)
	}()
	defer c1.Close()
	if err := json.NewEncoder(c1).Encode(req); err != nil {
		t.Fatal(err)
	}
	var resp secrets.UnsealResponse
	if err := json.NewDecoder(c1).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestHandle_ReturnsGrantedKeysOnly(t *testing.T) {
	s, _ := serverWith(t, "A: 1\nB: 2\n", secrets.Grants{"claude": {"A"}})
	resp := roundTrip(t, s, secrets.UnsealRequest{As: "claude"}, 1000, 4242)

	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if _, ok := resp.Values["B"]; ok {
		t.Error("returned B, which claude was never granted")
	}
	if string(resp.Values["A"]) != "1" {
		t.Errorf("A = %q", resp.Values["A"])
	}
}

func TestHandle_UnknownConsumerRefused(t *testing.T) {
	s, _ := serverWith(t, "A: 1\n", secrets.Grants{"claude": {"A"}})
	resp := roundTrip(t, s, secrets.UnsealRequest{As: "codex"}, 1000, 4242)

	if resp.Error == "" {
		t.Fatal("an ungranted identity was served")
	}
	if len(resp.Values) != 0 {
		t.Error("values returned alongside an error")
	}
}

func TestHandle_AuditsEveryUnsealWithNamesNotValues(t *testing.T) {
	s, aud := serverWith(t, "A: supersecret\n", nil)
	roundTrip(t, s, secrets.UnsealRequest{As: "claude"}, 1000, 4242)

	if len(aud.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(aud.events))
	}
	ev := aud.events[0]
	if ev["event_type"] != "secret_unseal" {
		t.Errorf("event_type = %v", ev["event_type"])
	}
	if ev["uid"] != uint32(1000) || ev["pid"] != uint32(4242) {
		t.Errorf("peer credentials not recorded: %+v", ev)
	}
	blob, _ := json.Marshal(ev)
	if string(blob) != "" && contains(string(blob), "supersecret") {
		t.Error("audit event contains a secret VALUE")
	}
}

func TestHandle_AuditsRefusals(t *testing.T) {
	// A refused request is the more interesting security event, not the less.
	s, aud := serverWith(t, "A: 1\n", secrets.Grants{"claude": {"A"}})
	roundTrip(t, s, secrets.UnsealRequest{As: "nobody"}, 1000, 4242)

	if len(aud.events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(aud.events))
	}
	if aud.events[0]["event_type"] != "secret_unseal_refused" {
		t.Errorf("event_type = %v", aud.events[0]["event_type"])
	}
}

func TestHandle_DecryptFailureIsRefusalNotEmptySuccess(t *testing.T) {
	stubDecrypt(t, "", errDecryptStub)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	if err := osWriteFile(p); err != nil {
		t.Fatal(err)
	}
	s := &Server{CiphertextPath: p, Audit: &recordingAudit{}}
	resp := roundTrip(t, s, secrets.UnsealRequest{}, 1000, 1)

	if resp.Error == "" {
		t.Fatal("a KMS failure was reported as success with no keys")
	}
}
```

Add these helpers to `bundle_test.go` so both files share them:

```go
import "errors"

var errDecryptStub = errors.New("kms 403")

func osWriteFile(p string) error { return os.WriteFile(p, []byte("sops: {}\n"), 0o400) }

func contains(hay, needle string) bool { return bytes.Contains([]byte(hay), []byte(needle)) }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/km-secretsd/ -v; echo "exit=$?"`
Expected: FAIL — `Server` undefined. (On macOS the Linux-only file is skipped; the helpers still fail to compile against a missing `Server`, which is the signal.)

- [ ] **Step 3: Write the implementation**

`cmd/km-secretsd/server.go` (portable — no build tag):

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

// Server answers unseal requests on a unix socket.
//
// It is a LOGGED DOOR, not a wall. Uid sandbox must reach the socket for any of
// this to work, so anything running as that uid can speak this protocol
// directly rather than going through a shim. What changes is the character of
// the theft: it becomes an active, authenticated, pid-attributed request with a
// CloudWatch record and a discrete CloudTrail kms:Decrypt, instead of a silent
// read of a file that was sitting there anyway.
type Server struct {
	CiphertextPath string
	Grants         secrets.Grants
	Audit          AuditWriter
}

// Serve accepts connections until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue // a bad peer must never take the daemon down
		}
		uid, pid := peerCred(conn)
		go func() {
			defer conn.Close()
			s.Handle(conn, uid, pid)
		}()
	}
}

// Handle serves exactly one request, then returns. Decryption happens here and
// nowhere else, and the bundle is zeroed before this function returns.
func (s *Server) Handle(conn net.Conn, uid, pid uint32) {
	var req secrets.UnsealRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		s.refuse(conn, uid, pid, req, fmt.Sprintf("malformed request: %v", err))
		return
	}

	bundle, err := LoadBundle(s.CiphertextPath)
	if err != nil {
		// Fail closed. An empty success here would hand the agent a confusing
		// 401 instead of a diagnosable error.
		s.refuse(conn, uid, pid, req, fmt.Sprintf("decrypt failed: %v", err))
		return
	}
	defer bundle.Zero()

	keys, err := secrets.Resolve(bundle.Keys(), s.Grants, req.As, req.Only)
	if err != nil {
		s.refuse(conn, uid, pid, req, err.Error())
		return
	}

	resp := secrets.UnsealResponse{Keys: keys, Values: make(map[string][]byte, len(keys))}
	for _, k := range keys {
		v := bundle.Get(k)
		cp := make([]byte, len(v))
		copy(cp, v)
		resp.Values[k] = cp
	}

	_ = s.Audit.Emit("secret_unseal", map[string]any{
		"as": req.As, "keys": keys, "uid": uid, "pid": pid, "exe": exeOf(pid),
	})
	_ = json.NewEncoder(conn).Encode(resp)
	for _, v := range resp.Values {
		zero(v)
	}
}

func (s *Server) refuse(conn net.Conn, uid, pid uint32, req secrets.UnsealRequest, msg string) {
	// A refusal is the more interesting security event, not the less.
	_ = s.Audit.Emit("secret_unseal_refused", map[string]any{
		"as": req.As, "uid": uid, "pid": pid, "exe": exeOf(pid), "reason": msg,
	})
	_ = json.NewEncoder(conn).Encode(secrets.UnsealResponse{Error: msg})
}

// exeOf resolves the caller's binary for the audit record. Advisory only: uid
// sandbox can exec any binary it likes, so this names the asker, it does not
// authenticate them.
func exeOf(pid uint32) string {
	if pid == 0 {
		return ""
	}
	p, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return ""
	}
	return p
}
```

`cmd/km-secretsd/peercred_linux.go`:

```go
//go:build linux

package main

import (
	"net"

	"golang.org/x/sys/unix"
)

// peerCred reads SO_PEERCRED. The values are recorded for ATTRIBUTION only and
// are NOT an authorization check: every legitimate caller is uid sandbox, and so
// is any malware on the box. Naming the asker is the product here, not gating it.
func peerCred(conn net.Conn) (uid, pid uint32) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, 0
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, 0
	}
	_ = raw.Control(func(fd uintptr) {
		if cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil {
			uid, pid = cred.Uid, cred.Pid
		}
	})
	return uid, pid
}
```

`cmd/km-secretsd/peercred_other.go`:

```go
//go:build !linux

package main

import "net"

// peerCred has no portable equivalent. The daemon only ever runs on the sandbox
// (Linux); this exists so the package compiles and tests on a dev machine.
func peerCred(net.Conn) (uid, pid uint32) { return 0, 0 }
```

`cmd/km-secretsd/main.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: km-secretsd serve")
		os.Exit(2)
	}

	grants := secrets.Grants{}
	if raw := os.Getenv("KM_SECRETS_GRANTS"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &grants); err != nil {
			fmt.Fprintf(os.Stderr, "km-secretsd: bad KM_SECRETS_GRANTS: %v\n", err)
			os.Exit(1)
		}
	}
	srv := &Server{
		CiphertextPath: secrets.CiphertextPath,
		Grants:         grants,
		Audit:          &PipeAudit{Path: secrets.AuditPipePath, SandboxID: os.Getenv("KM_SANDBOX_ID")},
	}

	switch os.Args[1] {
	case "serve":
		os.Exit(runServe(srv))
	// The selftest verb is added in Task 7, once Selftest exists.
	default:
		fmt.Fprintf(os.Stderr, "km-secretsd: unknown verb %q\n", os.Args[1])
		os.Exit(2)
	}
}

func runServe(srv *Server) int {
	// Remove a stale socket from an unclean shutdown; systemd restarts us and a
	// leftover inode would make every bind fail.
	_ = os.Remove(secrets.SocketPath)

	ln, err := net.Listen("unix", secrets.SocketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "km-secretsd: listen: %v\n", err)
		return 1
	}
	// 0660 root:sandbox — the sandbox user must reach it; km-sidecar and world
	// must not. Group ownership is set by the systemd unit's ExecStartPost.
	if err := os.Chmod(secrets.SocketPath, 0o660); err != nil {
		fmt.Fprintf(os.Stderr, "km-secretsd: chmod socket: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := srv.Serve(ctx, ln); err != nil {
		fmt.Fprintf(os.Stderr, "km-secretsd: serve: %v\n", err)
		return 1
	}
	return 0
}
```

- [ ] **Step 4: Run the tests, including on Linux**

macOS — everything except the real `SO_PEERCRED` read now runs here:
Run: `go test ./cmd/km-secretsd/ ./pkg/secrets/; echo "exit=$?"`
Expected: `exit=0`.

Linux — cross-compile the test binary and run it under Docker, because compiling inside qemu crashes the Go compiler and `CGO_ENABLED` must be 0:
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o /tmp/km-secretsd.test ./cmd/km-secretsd/
docker run --rm -v /tmp/km-secretsd.test:/t --platform linux/amd64 alpine /t -test.v
echo "exit=$?"
```
Expected: PASS, `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add cmd/km-secretsd/server.go cmd/km-secretsd/peercred_linux.go \
        cmd/km-secretsd/peercred_other.go cmd/km-secretsd/main.go \
        cmd/km-secretsd/server_test.go cmd/km-secretsd/bundle_test.go
git commit -m "feat(km-secretsd): unix-socket unseal broker with peer attribution"
```

---

### Task 6: `km-env`

**Files:**
- Create: `cmd/km-env/main.go`
- Test: `cmd/km-env/main_test.go`

**Interfaces:**
- Consumes: `secrets.SocketPath`, `secrets.UnsealRequest`, `secrets.UnsealResponse` (Task 1).
- Produces: `func unseal(socketPath string, req secrets.UnsealRequest) (secrets.UnsealResponse, error)`, `func parseArgs(argv []string) (verb string, req secrets.UnsealRequest, cmd []string, err error)`, `func buildEnv(base []string, vals map[string][]byte) []string`.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

func TestParseArgs_Exec(t *testing.T) {
	verb, req, cmd, err := parseArgs([]string{"exec", "--as", "claude", "--only", "A,B", "--", "claude", "-p", "hi"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if verb != "exec" || req.As != "claude" {
		t.Errorf("verb=%q as=%q", verb, req.As)
	}
	if !reflect.DeepEqual(req.Only, []string{"A", "B"}) {
		t.Errorf("only = %v", req.Only)
	}
	if !reflect.DeepEqual(cmd, []string{"claude", "-p", "hi"}) {
		t.Errorf("cmd = %v", cmd)
	}
}

func TestParseArgs_ExecRequiresCommand(t *testing.T) {
	if _, _, _, err := parseArgs([]string{"exec", "--as", "claude"}); err == nil {
		t.Fatal("exec with no -- command was accepted")
	}
}

// The single most important test in this file. km-env must never grow a verb
// that puts the bundle back into a shell — that is the entire thing being
// removed. Same disposition as km-netpolicy having no un-deny verb.
func TestNoShellExportVerbExists(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{`"export"`, `"env"`, `"eval"`, `"dump"`, `"shell"`, `"source"`} {
		if strings.Contains(string(src), banned) {
			t.Errorf("km-env appears to define a %s verb: a form that emits secrets to a "+
				"shell defeats the whole phase. Remove it.", banned)
		}
	}
}

func TestBuildEnv_OverlaysAndPreserves(t *testing.T) {
	got := buildEnv([]string{"PATH=/bin", "API_KEY=stale"}, map[string][]byte{"API_KEY": []byte("fresh")})
	want := map[string]bool{"PATH=/bin": true, "API_KEY=fresh": true}
	if len(got) != 2 {
		t.Fatalf("buildEnv() = %v, want 2 entries", got)
	}
	for _, e := range got {
		if !want[e] {
			t.Errorf("unexpected entry %q", e)
		}
	}
}

func TestUnseal_RoundTrip(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var req secrets.UnsealRequest
		_ = json.NewDecoder(c).Decode(&req)
		_ = json.NewEncoder(c).Encode(secrets.UnsealResponse{
			Keys:   []string{"A"},
			Values: map[string][]byte{"A": []byte("v")},
		})
	}()

	resp, err := unseal(sock, secrets.UnsealRequest{As: "claude"})
	if err != nil {
		t.Fatalf("unseal: %v", err)
	}
	if string(resp.Values["A"]) != "v" {
		t.Errorf("Values = %v", resp.Values)
	}
}

func TestUnseal_AbsentSocketFailsClosed(t *testing.T) {
	// A broker-less box must produce a diagnosable error, never a silent
	// exec with no secrets that dies later on a confusing 401.
	if _, err := unseal(filepath.Join(t.TempDir(), "absent.sock"), secrets.UnsealRequest{}); err == nil {
		t.Fatal("unseal succeeded with no broker listening")
	}
}

func TestUnseal_BrokerErrorSurfaces(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, _ := net.Listen("unix", sock)
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var req secrets.UnsealRequest
		_ = json.NewDecoder(c).Decode(&req)
		_ = json.NewEncoder(c).Encode(secrets.UnsealResponse{Error: "unknown consumer"})
	}()

	_, err := unseal(sock, secrets.UnsealRequest{As: "nope"})
	if err == nil || !strings.Contains(err.Error(), "unknown consumer") {
		t.Fatalf("err = %v, want the broker's message", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/km-env/ -v; echo "exit=$?"`
Expected: FAIL — `parseArgs` / `unseal` / `buildEnv` undefined.

- [ ] **Step 3: Write the implementation**

```go
// Command km-env asks km-secretsd for secrets and execs one command with them
// in its environment.
//
// There is deliberately NO export verb and no `eval $(km-env)` form: that would
// put the bundle straight back into a shell, which is the entire thing this
// phase removes. See TestNoShellExportVerbExists.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

func main() {
	verb, req, cmd, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "km-env: %v\n", err)
		fmt.Fprintln(os.Stderr, "usage: km-env exec [--as NAME] [--only K1,K2] -- <cmd> [args...]")
		fmt.Fprintln(os.Stderr, "       km-env list")
		os.Exit(2)
	}

	resp, err := unseal(secrets.SocketPath, req)
	if err != nil {
		// Fail closed and loudly. Running the command without its secrets
		// produces a confusing 401 far from the real cause.
		fmt.Fprintf(os.Stderr, "km-env: %v\n", err)
		fmt.Fprintf(os.Stderr, "km-env: is km-secretsd running? try: systemctl status km-secretsd\n")
		os.Exit(1)
	}

	if verb == "list" {
		for _, k := range resp.Keys {
			fmt.Println(k) // NAMES only, never values
		}
		return
	}

	bin, err := exec.LookPath(cmd[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "km-env: %v\n", err)
		os.Exit(127)
	}
	env := buildEnv(os.Environ(), resp.Values)
	// execve replaces this process, so km-env never lingers holding secrets.
	if err := syscall.Exec(bin, cmd, env); err != nil {
		fmt.Fprintf(os.Stderr, "km-env: exec %s: %v\n", bin, err)
		os.Exit(126)
	}
}

func parseArgs(argv []string) (string, secrets.UnsealRequest, []string, error) {
	var req secrets.UnsealRequest
	if len(argv) == 0 {
		return "", req, nil, errors.New("missing verb")
	}
	verb := argv[0]
	if verb != "exec" && verb != "list" {
		return "", req, nil, fmt.Errorf("unknown verb %q", verb)
	}

	rest := argv[1:]
	var cmd []string
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--as":
			if i+1 >= len(rest) {
				return "", req, nil, errors.New("--as needs a value")
			}
			i++
			req.As = rest[i]
		case "--only":
			if i+1 >= len(rest) {
				return "", req, nil, errors.New("--only needs a value")
			}
			i++
			for _, k := range strings.Split(rest[i], ",") {
				if k = strings.TrimSpace(k); k != "" {
					req.Only = append(req.Only, k)
				}
			}
		case "--":
			cmd = rest[i+1:]
			i = len(rest)
		default:
			return "", req, nil, fmt.Errorf("unexpected argument %q", rest[i])
		}
	}

	if verb == "exec" && len(cmd) == 0 {
		return "", req, nil, errors.New("exec needs a command after --")
	}
	return verb, req, cmd, nil
}

func unseal(socketPath string, req secrets.UnsealRequest) (secrets.UnsealResponse, error) {
	var resp secrets.UnsealResponse

	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return resp, fmt.Errorf("cannot reach the secrets broker at %s: %w", socketPath, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return resp, fmt.Errorf("send request: %w", err)
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return resp, fmt.Errorf("read response: %w", err)
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// buildEnv overlays unsealed values onto base, replacing any same-named entry.
func buildEnv(base []string, vals map[string][]byte) []string {
	out := make([]string, 0, len(base)+len(vals))
	for _, e := range base {
		name, _, found := strings.Cut(e, "=")
		if found {
			if _, shadowed := vals[name]; shadowed {
				continue
			}
		}
		out = append(out, e)
	}
	for k, v := range vals {
		out = append(out, k+"="+string(v))
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/km-env/ -v; echo "exit=$?"`
Expected: PASS, `exit=0`.

Run: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/km-env/; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 5: Commit**

```bash
git add cmd/km-env/main.go cmd/km-env/main_test.go
git commit -m "feat(km-env): unseal into one child process, with no export verb"
```

---

### Task 7: `km-secretsd selftest`

Restores the boot-time loudness the deleted `sops decrypt` FATAL provided, and adds the assertions that catch the new silent failure modes.

**Files:**
- Create: `cmd/km-secretsd/selftest.go`
- Test: `cmd/km-secretsd/selftest_test.go`

**Interfaces:**
- Consumes: `Server`, `LoadBundle`, `AuditWriter`.
- Produces: `type Check struct{ Name string; OK bool; Fatal bool; Detail string }`, `func (s *Server) Selftest(opts SelftestOpts) []Check`, `type SelftestOpts struct{ ShimDir string; Consumers []string; SocketPath string; LookPathAs func(consumer string) (string, error) }`, `func runSelftest(s *Server) int`.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func opts(t *testing.T, shimDir string, consumers []string, resolve func(string) (string, error)) SelftestOpts {
	t.Helper()
	return SelftestOpts{
		ShimDir:    shimDir,
		Consumers:  consumers,
		SocketPath: filepath.Join(t.TempDir(), "s.sock"),
		LookPathAs: resolve,
	}
}

func find(checks []Check, name string) *Check {
	for i := range checks {
		if checks[i].Name == name {
			return &checks[i]
		}
	}
	return nil
}

func TestSelftest_LiveUnsealFailureIsFatal(t *testing.T) {
	// The class that aborts boot today: KMS 403, wrong alias, missing grant.
	stubDecrypt(t, "", errDecryptStub)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	c := find(s.Selftest(opts(t, t.TempDir(), nil, nil)), "unseal")
	if c == nil {
		t.Fatal("no unseal check ran")
	}
	if c.OK || !c.Fatal {
		t.Errorf("unseal check = %+v, want failed and fatal", c)
	}
}

func TestSelftest_ReportsKeyNamesNeverValues(t *testing.T) {
	stubDecrypt(t, "API_KEY: supersecret\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	c := find(s.Selftest(opts(t, t.TempDir(), nil, nil)), "unseal")
	if !c.OK {
		t.Fatalf("unseal check failed: %s", c.Detail)
	}
	if !contains(c.Detail, "API_KEY") {
		t.Errorf("detail should name the key, got %q", c.Detail)
	}
	if contains(c.Detail, "supersecret") {
		t.Error("selftest detail leaked a secret VALUE")
	}
}

func TestSelftest_ShimPointingAtMissingTargetIsFatal(t *testing.T) {
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	shims := t.TempDir()
	// A shim whose baked target no longer exists — the stale-path case after a
	// claude reinstall relocates the real binary.
	if err := os.WriteFile(filepath.Join(shims, "claude"),
		[]byte("#!/bin/sh\nexec /opt/km/bin/km-env exec --as claude -- /gone/claude \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	c := find(s.Selftest(opts(t, shims, []string{"claude"}, nil)), "shim:claude")
	if c == nil || c.OK || !c.Fatal {
		t.Errorf("shim check = %+v, want failed and fatal", c)
	}
}

func TestSelftest_ShimLosingThePATHRaceIsFatal(t *testing.T) {
	// THE highest-value assertion. If nvm's bin dir wins, claude runs with no
	// key and dies on a 401, and nothing else in the system notices.
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	shims := t.TempDir()
	target := filepath.Join(t.TempDir(), "claude")
	_ = os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755)
	_ = os.WriteFile(filepath.Join(shims, "claude"),
		[]byte("#!/bin/sh\nexec /opt/km/bin/km-env exec --as claude -- "+target+" \"$@\"\n"), 0o755)
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	// Resolution returns nvm's copy, not the shim: the race is lost.
	lost := s.Selftest(opts(t, shims, []string{"claude"}, func(string) (string, error) {
		return "/home/sandbox/.nvm/versions/node/v22/bin/claude", nil
	}))
	c := find(lost, "path:claude")
	if c == nil || c.OK || !c.Fatal {
		t.Errorf("path check = %+v, want failed and fatal", c)
	}

	// Resolution returns the shim: the race is won.
	won := s.Selftest(opts(t, shims, []string{"claude"}, func(string) (string, error) {
		return filepath.Join(shims, "claude"), nil
	}))
	if c := find(won, "path:claude"); c == nil || !c.OK {
		t.Errorf("path check = %+v, want OK when the shim resolves first", c)
	}
}

func TestSelftest_GrantedConsumerWithNoBinaryWarnsNotFails(t *testing.T) {
	// initCommandsAppend may install it later; no shim is generated, so nothing
	// is silently broken.
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = osWriteFile(p)
	s := &Server{CiphertextPath: p, Grants: map[string][]string{"latertool": {"A"}}, Audit: NopAudit{}}

	c := find(s.Selftest(opts(t, t.TempDir(), []string{"latertool"}, nil)), "shim:latertool")
	if c == nil {
		t.Fatal("no check for the ungenerated consumer")
	}
	if c.Fatal {
		t.Errorf("check = %+v, want non-fatal warning", c)
	}
}

func TestSelftest_CiphertextPermissionsChecked(t *testing.T) {
	stubDecrypt(t, "A: 1\n", nil)
	p := filepath.Join(t.TempDir(), "secrets.enc.yaml")
	_ = os.WriteFile(p, []byte("sops: {}\n"), 0o644) // world-readable
	s := &Server{CiphertextPath: p, Audit: NopAudit{}}

	c := find(s.Selftest(opts(t, t.TempDir(), nil, nil)), "ciphertext")
	if c == nil || c.OK {
		t.Errorf("ciphertext check = %+v, want failure on 0644", c)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/km-secretsd/ -run TestSelftest -v; echo "exit=$?"`
Expected: FAIL — `Selftest` / `Check` / `SelftestOpts` undefined.

- [ ] **Step 3: Write the implementation**

```go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

// Check is one selftest assertion.
type Check struct {
	Name   string
	OK     bool
	Fatal  bool // a failed Fatal check aborts the boot
	Detail string
}

// SelftestOpts injects the filesystem and PATH facts so the assertions are
// testable off-box.
type SelftestOpts struct {
	ShimDir    string
	Consumers  []string
	SocketPath string
	// LookPathAs reports what `command -v <consumer>` resolves to for the
	// sandbox user. Nil means run it for real via runuser.
	LookPathAs func(consumer string) (string, error)
}

// Selftest answers one question: will agents fail?
//
// It runs in two places off this one verb. At boot it is called from userdata,
// which runs under set -euo pipefail, so a non-zero exit aborts the boot exactly
// as the Phase 89 sops-decrypt FATAL did. On resume it is called by
// km-secrets-check.service — userdata does not re-run on stop/start, and a
// resumed box can meet a rotated bundle or a revoked grant. There is no boot to
// abort on resume, so the unit simply fails red and emits the audit event.
func (s *Server) Selftest(o SelftestOpts) []Check {
	var checks []Check

	// 1. Ciphertext present, 0400 root, sops-shaped.
	if fi, err := os.Stat(s.CiphertextPath); err != nil {
		checks = append(checks, Check{"ciphertext", false, true, err.Error()})
	} else if perm := fi.Mode().Perm(); perm != 0o400 {
		checks = append(checks, Check{"ciphertext", false, true,
			fmt.Sprintf("%s is %04o, want 0400", s.CiphertextPath, perm)})
	} else {
		checks = append(checks, Check{"ciphertext", true, true, s.CiphertextPath})
	}

	// 2. Socket present with the right mode (skipped when it has not been bound,
	//    e.g. under test).
	if fi, err := os.Stat(o.SocketPath); err == nil {
		ok := fi.Mode().Perm() == 0o660
		checks = append(checks, Check{"socket", ok, true,
			fmt.Sprintf("%s is %04o, want 0660", o.SocketPath, fi.Mode().Perm())})
	}

	// 3. Live end-to-end unseal. NAMES only in the detail, never values.
	bundle, err := LoadBundle(s.CiphertextPath)
	if err != nil {
		checks = append(checks, Check{"unseal", false, true, err.Error()})
	} else {
		names := bundle.Keys()
		bundle.Zero()
		checks = append(checks, Check{"unseal", true, true,
			fmt.Sprintf("%d keys: %s", len(names), strings.Join(names, ", "))})
	}

	// 4 and 5. Per consumer: the shim's target exists and is not itself, and
	//          the shim wins the PATH race for the sandbox user.
	for _, c := range o.Consumers {
		shim := filepath.Join(o.ShimDir, c)
		body, err := os.ReadFile(shim)
		if err != nil {
			// No shim generated. Warn: initCommandsAppend may install the binary
			// later, and because no shim exists nothing is silently broken.
			checks = append(checks, Check{"shim:" + c, false, false,
				"no shim generated (consumer binary absent at boot)"})
			continue
		}

		target := shimTarget(string(body))
		switch {
		case target == "":
			checks = append(checks, Check{"shim:" + c, false, true, "cannot parse shim target"})
			continue
		case target == shim:
			checks = append(checks, Check{"shim:" + c, false, true, "shim targets itself: would recurse"})
			continue
		}
		if _, err := os.Stat(target); err != nil {
			checks = append(checks, Check{"shim:" + c, false, true,
				fmt.Sprintf("target %s missing: %v", target, err)})
			continue
		}
		checks = append(checks, Check{"shim:" + c, true, true, target})

		resolved, err := resolveForSandbox(o, c)
		if err != nil {
			checks = append(checks, Check{"path:" + c, false, true, err.Error()})
			continue
		}
		if resolved != shim {
			checks = append(checks, Check{"path:" + c, false, true,
				fmt.Sprintf("%s resolves to %s, not %s: the shim lost the PATH race and "+
					"%s would run with no secrets", c, resolved, shim, c)})
			continue
		}
		checks = append(checks, Check{"path:" + c, true, true, resolved})
	}

	return checks
}

// shimTarget extracts the absolute path a generated shim execs.
func shimTarget(body string) string {
	for _, line := range strings.Split(body, "\n") {
		idx := strings.Index(line, "-- ")
		if !strings.HasPrefix(strings.TrimSpace(line), "exec ") || idx < 0 {
			continue
		}
		fields := strings.Fields(line[idx+3:])
		if len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

func resolveForSandbox(o SelftestOpts, consumer string) (string, error) {
	if o.LookPathAs != nil {
		return o.LookPathAs(consumer)
	}
	// A login shell, because that is exactly how dispatch_as_sandbox invokes it.
	out, err := exec.Command("runuser", "-u", "sandbox", "--",
		"bash", "-lc", "command -v "+consumer).Output()
	if err != nil {
		return "", fmt.Errorf("resolving %s as the sandbox user: %w", consumer, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func runSelftest(s *Server) int {
	// secrets.DefaultConsumers is a package-level var. Never reslice it and
	// append — consumers[:0] aliases its backing array, so the appends would
	// overwrite the package's own defaults for the rest of the process. Always
	// build a fresh slice.
	consumers := secrets.DefaultConsumers
	if len(s.Grants) > 0 {
		consumers = make([]string, 0, len(s.Grants))
		for c := range s.Grants {
			consumers = append(consumers, c)
		}
		sort.Strings(consumers) // deterministic check order and audit output
	}

	checks := s.Selftest(SelftestOpts{
		ShimDir:    secrets.ShimDir,
		Consumers:  consumers,
		SocketPath: secrets.SocketPath,
	})

	failed := 0
	detail := map[string]any{}
	for _, c := range checks {
		status := "ok"
		switch {
		case c.OK:
		case c.Fatal:
			status = "FAIL"
			failed++
		default:
			status = "warn"
		}
		fmt.Fprintf(os.Stderr, "[km-secrets-check] %-16s %-4s %s\n", c.Name, status, c.Detail)
		detail[c.Name] = status
	}
	detail["failed"] = failed
	_ = s.Audit.Emit("secret_selftest", detail)

	if failed > 0 {
		fmt.Fprintf(os.Stderr, "[km-secrets-check] %d fatal check(s) failed: agents would fail\n", failed)
		return 1
	}
	return 0
}
```

- [ ] **Step 4: Wire the verb into main.go**

In `cmd/km-secretsd/main.go`, restore the dispatch case now that `runSelftest`
exists, and the usage line with it:

```go
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: km-secretsd serve|selftest")
		os.Exit(2)
	}
```

```go
	switch os.Args[1] {
	case "serve":
		os.Exit(runServe(srv))
	case "selftest":
		os.Exit(runSelftest(srv))
	default:
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./cmd/km-secretsd/ -v; echo "exit=$?"`
Expected: PASS, `exit=0`.

Run: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/km-secretsd/; echo "exit=$?"`
Expected: `exit=0`.

- [ ] **Step 6: Commit**

```bash
git add cmd/km-secretsd/selftest.go cmd/km-secretsd/selftest_test.go cmd/km-secretsd/main.go
git commit -m "feat(km-secretsd): boot selftest answering whether agents will fail"
```

---

### Task 8: Userdata — replace env injection with brokered injection

The largest task, and the one the guard test exists to protect. It ships with its own guard because a reviewer would not approve the template change without it.

**Files:**
- Modify: `pkg/compiler/userdata.go` — delete section 5.5's env write (lines ~1226-1241); add the broker, shims, PATH prepends and boot check; add `SopsConsumers` to the params struct and its population near line 6798.
- Create: `pkg/secrets/wiring_guard_test.go`
- Modify: golden fixtures under `pkg/compiler/testdata/` as the test output dictates.

**Interfaces:**
- Consumes: `secrets.ShimDir`, `secrets.SocketPath`, `secrets.CiphertextPath` (Task 1); the `km-secretsd` and `km-env` binaries (Tasks 3-7).
- Produces: template params `.SopsConsumers []string` and `.SopsGrantsJSON string`; on-box `/opt/km/shims/*`, `/etc/systemd/system/km-secretsd.service`, `/etc/systemd/system/km-secrets-check.service`, `/etc/profile.d/zz-km-shims.sh`.

- [ ] **Step 1: Write the failing guard test**

Create `pkg/secrets/wiring_guard_test.go`:

```go
package secrets_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test directory")
		}
		dir = parent
	}
}

// Every agent dispatch site must put the shim directory on PATH.
//
// dispatch_as_sandbox is defined once per poller (slack, github, h1, webhook)
// plus a tmux twin, and covers eighteen call sites. The shim is what makes
// km-env innermost — the turn shell gets a PATH entry, the agent process gets
// the secrets. Miss one definition and that poller's agent runs with no key and
// dies on a 401, with nothing else in the system noticing.
//
// Relying on /etc/profile.d ordering is not enough and this guard is why:
// nvm's own profile.d script PREPENDS its bin directory and would win, and the
// tmux dispatch uses `bash -c` (non-login) which never sources profile.d at all.
// This is the Phase 131/132 shape — dead under the default configuration,
// invisible to tests that assert mere string presence.
//
// Name-agnostic on purpose: a sixth poller added later is covered without
// editing this test.
func TestEveryDispatchSitePrependsShimDir(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "pkg/compiler/userdata.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)

	defs := regexp.MustCompile(`dispatch_as_sandbox\(\) \{`).FindAllStringIndex(src, -1)
	if len(defs) == 0 {
		t.Fatal("no dispatch_as_sandbox definitions found — this guard needs updating")
	}

	for i, loc := range defs {
		end := len(src)
		if i+1 < len(defs) {
			end = defs[i+1][0]
		}
		// The function body is short; 600 bytes covers it comfortably.
		window := src[loc[1]:min(loc[1]+600, end)]
		if !strings.Contains(window, "/opt/km/shims") {
			t.Errorf("dispatch_as_sandbox definition #%d (byte %d) does not prepend "+
				"/opt/km/shims: agents dispatched there would run with no secrets",
				i+1, loc[0])
		}
	}
}

// The plaintext env file and its profile.d hook are the whole point of the
// phase. If either comes back, every login shell carries the bundle again.
func TestPlaintextEnvInjectionIsGone(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "pkg/compiler/userdata.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)

	for _, banned := range []string{"/etc/sandbox-secrets.env", "zz-sandbox-secrets.sh"} {
		if strings.Contains(src, banned) {
			t.Errorf("userdata still references %s: secrets would be readable from "+
				"every login shell again", banned)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/secrets/ -run 'TestEveryDispatchSite|TestPlaintextEnv' -v; echo "exit=$?"`
Expected: FAIL on both — the shim prepend does not exist yet, and `/etc/sandbox-secrets.env` is still referenced.

- [ ] **Step 3: Change the template**

**3a. Replace section 5.5** (userdata.go lines ~1209-1242). Keep the ciphertext download; delete the decrypt, the `.env` write and the profile.d hook:

```
{{- if .SopsBundlePresent }}

# ============================================================
# 5.5. Brokered secret unsealing (Phase 133, replaces Phase 89 env injection)
# ============================================================
# The bundle is NEVER decrypted to disk. km-secretsd decrypts per request and
# km-env injects the result into one child process. /etc/sandbox-secrets.env and
# /etc/profile.d/zz-sandbox-secrets.sh are deliberately gone: they put the whole
# bundle into every login shell, which is what an env dump collects.
echo "[km-bootstrap] Installing secrets broker..."
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/binaries/sops" /opt/km/bin/sops
chmod +x /opt/km/bin/sops
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-secretsd" /opt/km/bin/km-secretsd
chmod +x /opt/km/bin/km-secretsd
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-env" /opt/km/bin/km-env
chmod +x /opt/km/bin/km-env
ln -sf /opt/km/bin/km-env /usr/local/bin/km-env

aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sandboxes/{{ .SandboxID }}/secrets.enc.yaml" /etc/sandbox-secrets.enc.yaml
chown root:root /etc/sandbox-secrets.enc.yaml
chmod 0400 /etc/sandbox-secrets.enc.yaml

cat > /etc/systemd/system/km-secretsd.service << 'KMSECRETSD'
[Unit]
Description=km secrets broker (decrypt on demand, never at rest)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Environment=AWS_REGION={{ .AWSRegion }}
Environment=KM_SANDBOX_ID={{ .SandboxID }}
Environment=KM_SECRETS_GRANTS={{ .SopsGrantsJSON }}
ExecStart=/opt/km/bin/km-secretsd serve
# The socket must be reachable by the sandbox user and by nobody else.
ExecStartPost=/bin/sh -c 'for i in $(seq 1 50); do [ -S /run/km/secrets.sock ] && break; sleep 0.1; done; chgrp sandbox /run/km/secrets.sock && chmod 0660 /run/km/secrets.sock'
Restart=always
RestartSec=5

[Unit]
StartLimitIntervalSec=60
StartLimitBurst=5

[Install]
WantedBy=multi-user.target
KMSECRETSD
systemctl daemon-reload
systemctl enable --now km-secretsd.service
echo "[km-bootstrap] km-secretsd started"
{{- end }}
```

> **`StartLimitIntervalSec`/`StartLimitBurst` belong in `[Unit]`, not `[Service]`.** Since systemd v230 the `[Service]` section only recognises the pre-rename `StartLimitInterval=` spelling, so the renamed keys placed there are silently dropped and a permanently broken daemon hides behind an endless restart loop. This is the `km-execlog.service` lesson.

**3b. Generate shims and the PATH prepend**, placed AFTER `initCommands` run (so `claude`/`codex` exist), before the boot check:

```
{{- if .SopsBundlePresent }}

# ============================================================
# 5.6. Consumer shims (Phase 133)
# ============================================================
# A shim makes km-env INNERMOST: the turn shell receives a PATH entry, the agent
# process receives the secrets. Wrapping dispatch_as_sandbox instead would hand
# the whole bash -lc the bundle, which is the thing being removed.
mkdir -p /opt/km/shims
{{- range .SopsConsumers }}
KM_SHIM_TARGET="$(sudo -u sandbox bash -lc 'command -v {{ . }}' 2>/dev/null || true)"
if [ -n "$KM_SHIM_TARGET" ]; then
  cat > /opt/km/shims/{{ . }} << KMSHIM
#!/bin/sh
# Phase 133: exec an ABSOLUTE path, never the bare name — resolving by name here
# would re-find this shim on PATH and recurse. If the baked target has since
# moved (userdata re-runs Claude's install.cjs idempotently), fall back to a
# PATH search with the shim directory removed.
KM_REAL="$KM_SHIM_TARGET"
if [ ! -x "\$KM_REAL" ]; then
  KM_REAL="\$(PATH="\$(echo "\$PATH" | tr ':' '\n' | grep -v '^/opt/km/shims$' | paste -sd: -)" command -v {{ . }} 2>/dev/null)"
fi
[ -x "\$KM_REAL" ] || { echo "km-shim: cannot locate the real {{ . }}" >&2; exit 127; }
exec /opt/km/bin/km-env exec --as {{ . }} -- "\$KM_REAL" "\$@"
KMSHIM
  chmod 0755 /opt/km/shims/{{ . }}
  chown root:root /opt/km/shims/{{ . }}
  echo "[km-bootstrap] shim: {{ . }} -> $KM_SHIM_TARGET"
else
  echo "[km-bootstrap] WARNING: no {{ . }} on PATH; no shim generated" >&2
fi
{{- end }}

# Interactive km shell. zz- sorts last so this prepend wins over nvm's own
# profile.d script, which also prepends.
cat > /etc/profile.d/zz-km-shims.sh << 'KMSHIMPATH'
case ":$PATH:" in
  *":/opt/km/shims:"*) ;;
  *) PATH="/opt/km/shims:$PATH"; export PATH ;;
esac
KMSHIMPATH
chmod 0644 /etc/profile.d/zz-km-shims.sh
{{- end }}
```

**3c. Prepend the shim dir in all five `dispatch_as_sandbox` definitions.** In each, change the `exec runuser` line. For the four `bash -lc` pollers:

```
    exec runuser -u sandbox -- bash -lc "{{ if .SopsBundlePresent }}PATH=/opt/km/shims:\$PATH; {{ end }}$1"
```

and for the tmux twin at ~line 3904 (`bash -c`, non-login — profile.d never runs there, which is exactly why the explicit prepend is load-bearing):

```
    exec runuser -u sandbox -- bash -c "{{ if .SopsBundlePresent }}PATH=/opt/km/shims:\$PATH; {{ end }}$1"
```

**3d. Boot check**, immediately after 5.6. Under `set -euo pipefail`, a non-zero exit aborts the boot exactly as the Phase 89 FATAL did:

```
{{- if .SopsBundlePresent }}

# ============================================================
# 5.7. Secrets boot check (Phase 133)
# ============================================================
# Answers one question: will agents fail? A non-zero exit aborts the boot, the
# same disposition as the Phase 89 sops-decrypt FATAL this replaces. A box that
# looks healthy and fails at first turn is worse, because an autonomous trigger
# will find it first.
echo "[km-bootstrap] Running secrets self-test..."
KM_SANDBOX_ID={{ .SandboxID }} AWS_REGION={{ .AWSRegion }} \
  KM_SECRETS_GRANTS='{{ .SopsGrantsJSON }}' \
  /opt/km/bin/km-secretsd selftest

# Resume coverage: userdata does NOT re-run on stop/start (cloud-init is
# per-instance), and a resumed box can meet a rotated bundle or a revoked grant.
# There is no boot to abort on resume, so this fails red and audits instead.
cat > /etc/systemd/system/km-secrets-check.service << 'KMSECRETSCHECK'
[Unit]
Description=km secrets self-test (resume coverage)
After=km-secretsd.service
Requires=km-secretsd.service

[Service]
Type=oneshot
RemainAfterExit=yes
User=root
Environment=AWS_REGION={{ .AWSRegion }}
Environment=KM_SANDBOX_ID={{ .SandboxID }}
Environment=KM_SECRETS_GRANTS={{ .SopsGrantsJSON }}
ExecStart=/opt/km/bin/km-secretsd selftest

[Install]
WantedBy=multi-user.target
KMSECRETSCHECK
systemctl daemon-reload
systemctl enable km-secrets-check.service
echo "[km-bootstrap] secrets self-test passed"
{{- end }}
```

**3e. Populate the new params.** `pkg/compiler/userdata.go` already imports
`encoding/json` and `fmt` but **not `sort`** — add it. In the params struct add:

```go
	// Phase 133: consumers to generate shims for — the grants keys, or
	// claude+codex when grants is absent.
	SopsConsumers []string
	// SopsGrantsJSON is the grants map as compact JSON for the daemon's
	// KM_SECRETS_GRANTS env. Empty object when no grants are declared.
	SopsGrantsJSON string
```

and beside the existing `params.SopsBundlePresent` assignment (~line 6798):

```go
	// Phase 133: shim set and broker grants.
	if params.SopsBundlePresent {
		params.SopsConsumers = secrets.DefaultConsumers
		params.SopsGrantsJSON = "{}"
		if g := p.Spec.Secrets.Grants; len(g) > 0 {
			params.SopsConsumers = make([]string, 0, len(g))
			for name := range g {
				params.SopsConsumers = append(params.SopsConsumers, name)
			}
			sort.Strings(params.SopsConsumers) // deterministic userdata
			blob, err := json.Marshal(g)
			if err != nil {
				return nil, fmt.Errorf("marshal secrets grants: %w", err)
			}
			params.SopsGrantsJSON = string(blob)
		}
	}
```

- [ ] **Step 4: Run the guard and the goldens**

Run: `go test ./pkg/secrets/ -v; echo "exit=$?"`
Expected: PASS, `exit=0`.

Run: `go test ./pkg/compiler/ 2>&1 | tail -40; echo "compiler-exit=${PIPESTATUS[0]}"`
Expected: golden diffs on sops-bearing fixtures only.

Inspect each diff and confirm it is exactly the intended change, then regenerate **only** the non-frozen goldens with the repo's sanctioned capture flags. **Do not** re-capture `userdata_learn_v2_pre92_baseline.golden.sh` with `CAPTURE_PRE92_BASELINE=1` — that writes the unstripped output and corrupts the frozen baseline; hand-patch it if it moves.

Then prove byte-identity for the dormant case:
```bash
go test ./pkg/compiler/ -run 'ByteIdentity|Golden' -v; echo "exit=$?"
```
Expected: `exit=0`, with **no** diff on any profile lacking `spec.secrets.sopsFile`.

Then the whole suite:
```bash
go test ./...; echo "exit=$?"
```
Expected: `exit=0` apart from the five known-red Bootstrap/Cluster tests documented in `project_cmd_suite_pre_existing_failures`. Confirm any failure you see is on that list before treating it as pre-existing.

- [ ] **Step 5: Commit**

```bash
git add pkg/compiler/userdata.go pkg/secrets/wiring_guard_test.go pkg/compiler/testdata/
git commit -m "feat(userdata): broker secrets instead of exporting them into every shell"
```

---

### Task 9: Deploy wiring and docs

Without the `sidecarBuilds()` entries the userdata `s3 cp` 404s and **every SOPS sandbox fails to boot**. This task is not optional polish.

**Files:**
- Modify: `internal/app/cmd/init.go` (`sidecarBuilds()`, ~line 3800)
- Create: `docs/brokered-secrets.md`
- Modify: `docs/sandbox-secrets.md`
- Modify: `CLAUDE.md`
- Test: `internal/app/cmd/init_sidecars_test.go` (create)

**Interfaces:**
- Consumes: everything above.
- Produces: `km-secretsd` and `km-env` uploaded to `s3://<artifacts>/sidecars/`.

- [ ] **Step 1: Write the failing test**

```go
package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/cmd"
)

// A sidecar the userdata downloads but km init never uploads 404s the gated
// download and aborts bootstrap. This pairs the two mechanically.
func TestSidecarBuilds_CoversEverySecretsBinary(t *testing.T) {
	have := map[string]bool{}
	for _, n := range cmd.SidecarBuildNames() {
		have[n] = true
	}
	for _, want := range []string{"km-secretsd", "km-env"} {
		if !have[want] {
			t.Errorf("sidecarBuilds() omits %s: userdata downloads it and boot would 404", want)
		}
	}
}

func TestUserdataDownloadsMatchSidecarBuilds(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "pkg", "compiler", "userdata.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	built := map[string]bool{}
	for _, n := range cmd.SidecarBuildNames() {
		built[n] = true
	}
	for _, n := range []string{"km-secretsd", "km-env"} {
		if !strings.Contains(src, "sidecars/"+n) {
			t.Errorf("userdata never downloads %s", n)
		}
		if !built[n] {
			t.Errorf("userdata downloads %s but km init never uploads it", n)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app/cmd/ -run 'TestSidecarBuilds_CoversEverySecrets|TestUserdataDownloadsMatch' -v; echo "exit=$?"`
Expected: FAIL — both names missing from `sidecarBuilds()`.

- [ ] **Step 3: Write the implementation**

In `internal/app/cmd/init.go`, append to the `sidecarBuilds()` slice:

```go
		// Phase 133 — the secrets broker and its client. Userdata downloads both
		// when spec.secrets.sopsFile is set; a missing upload here 404s that
		// gated download and aborts bootstrap. They ship together with the
		// create-handler-rendered unit: half a deploy crash-loops the daemon.
		{name: "km-secretsd", srcDir: "cmd/km-secretsd"},
		{name: "km-env", srcDir: "cmd/km-env"},
```

Create `docs/brokered-secrets.md` covering: what changed from Phase 89 and why; the `grants` field with a worked example; `km-env exec` / `km-env list` usage and why there is no export verb; reading `secret_unseal` events out of the CloudWatch audit stream and joining them against `km-netpolicy execs`; the boot check and how to read `systemctl status km-secrets-check`; troubleshooting (broker down, shim lost the PATH race, ungranted consumer); the deploy surface from §9 of the spec; and — verbatim from spec §6 — that the broker is a logged door rather than a wall, that `grants` is hygiene not containment, and that `privileged: true` defeats all of it.

In `docs/sandbox-secrets.md`, add a note at the top: Phase 89's env-var delivery is superseded by Phase 133; `/etc/sandbox-secrets.env` and `/etc/profile.d/zz-sandbox-secrets.sh` no longer exist; `sopsFile` and the KMS/bootstrap prerequisites are unchanged; see `docs/brokered-secrets.md`.

In `CLAUDE.md`, add a Phase 133 block above Phase 132 recording: the shell-dump problem and the finding that the instance role permanently holds decrypt authority; decrypt-per-request with `[]byte` zeroing and its honest limit; shims making `km-env` innermost and why wrapping `dispatch_as_sandbox` was rejected; the nvm PATH race and the guard test; the boot-fatal/resume-red asymmetry and why; that `grants` is hygiene not containment; **deploy = `make build` + `make build-lambdas` + `km init --dry-run=false`, NOT `--sidecars`, do not split the deploy**; and that Wave 2 (`fenceIMDS`, `km-creds`, `ec2spot/v1.7.0`) is still outstanding.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app/cmd/ -run 'TestSidecarBuilds|TestUserdataDownloads' -v; echo "exit=$?"`
Expected: PASS, `exit=0`.

Run: `make build; echo "exit=$?"`
Expected: `exit=0`.

Run: `go test ./...; echo "exit=$?"`
Expected: `exit=0` apart from the five known-red Bootstrap/Cluster tests.

- [ ] **Step 5: Commit**

```bash
git add internal/app/cmd/init.go internal/app/cmd/init_sidecars_test.go \
        docs/brokered-secrets.md docs/sandbox-secrets.md CLAUDE.md
git commit -m "feat(init): upload km-secretsd and km-env; document brokered secrets"
```

---

## Live UAT (required before merge)

None of this is provable on a dev machine. `pkg/secrets` and the pure logic test on macOS; the broker's syscall paths only run under Linux; and the PATH race only exists on a real nvm box. Run against a real sandbox built from a profile with `spec.secrets.sopsFile`:

1. `km create <profile-with-sops>` → boots. Then `systemctl status km-secretsd` → active.
2. `km shell <id>` → `cat /etc/sandbox-secrets.env` → **no such file**; `ls /etc/profile.d/ | grep sandbox-secrets` → **empty**.
3. In that shell: `env | grep -c ANTHROPIC_API_KEY` → **0**. This is the observed attack, reproduced and defeated.
4. `command -v claude` → `/opt/km/shims/claude`. **The PATH race, live.**
5. `km-env list` → key names, no values. `km-env exec -- env | grep ANTHROPIC_API_KEY` → present.
6. Trigger a real autonomous turn (Slack @-mention). The agent authenticates. During the turn, from a second shell: `cat /proc/$(pgrep -f claude)/environ | tr '\0' '\n' | grep ANTHROPIC` → present (the accepted one-turn window, §6); the same grep against any other process → absent.
7. `km logs <id>` → `secret_unseal` events with `as`, `keys`, `uid`, `pid`, `exe` — and **no values**.
8. Break it on purpose: `systemctl stop km-secretsd`, run `claude` → a clear broker-unreachable error, not a 401.
9. `km pause <id> && km resume <id>` → `systemctl status km-secrets-check` → active/exited, and a `secret_selftest` event in the audit stream.
10. A profile **without** `sopsFile` → `diff` its rendered userdata against a pre-Phase-133 render → **byte-identical**.

---

## Self-Review Notes

**Spec coverage:** §3.1 broker → Tasks 5, 8. §3.2 decrypt-per-request + zeroing → Task 3. §3.3 wrapped consumers → Tasks 6, 8. §3.4 grants → Tasks 1, 2. §3.5 broker default-on → Task 8 (no gate added). §3.6 fatal at boot → Task 7, 8. §4.1 daemon → Tasks 3-5. §4.2 km-env + no-export → Task 6. §4.3 shims + five prepends → Task 8. §4.4 fence → **Wave 2, out of scope by Global Constraint.** §5 schema → Task 2. §6 honesty → documented in Task 9 and in code comments. §7 boot check → Task 7 (assertions 1-5, 7; assertion 6 is Wave 2). §8 testing → guard test Task 8, no-export Task 6, goldens Task 8, Docker Task 5, live UAT above. §9 deploy → Task 9.

**Known deferrals inside Wave 1:** selftest assertion 6 and its fence clauses are Wave 2. `km-secretsd selftest`'s socket check is skipped when the socket is unbound, which is correct at boot-check time under userdata (the daemon has already started) and keeps the check testable off-box.
