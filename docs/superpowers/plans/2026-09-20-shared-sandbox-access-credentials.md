# Shared Sandbox Access Credentials Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Any analyst with operator creds can `km vscode|desktop|herdr|tunnel start` a sandbox someone else created, because the per-sandbox SSH key and KasmVNC password live in SSM and every `start` reconciles the local cache to it.

**Architecture:** Two SecureStrings per sandbox under `/{prefix}/access/{id}/` (a namespace the sandbox role cannot read). One sync function (`syncSharedCredential`) runs before every verb's existing SSM pre-flight and implements a seven-row table (pull / refresh / silent / publish / error / fail-open). `rekey` publishes box → local → SSM; `create` publishes at generation; `CleanupSandboxIdentity` deletes; the pre-flight compares the box's `authorized_keys` to the synced key and names the fix on mismatch. A package-level seam (`NewSharedCredStoreFunc`) returns `nil` in the test binary so every existing test is byte-identical.

**Tech Stack:** Go 1.25, AWS SDK v2 (`ssm`), `golang.org/x/crypto/ssh`, cobra, Terraform (ttl-handler IAM).

**Spec:** `docs/superpowers/specs/2026-09-20-shared-sandbox-access-credentials-design.md`

## Global Constraints

- Parameter paths are exactly `/{prefix}/access/{sandbox-id}/ssh-key` and `/{prefix}/access/{sandbox-id}/desktop-cred`; SecureString; `Overwrite: true`; KMS key = `KM_PLATFORM_KMS_KEY_ARN` env else `cfg.GetPlatformKMSAlias()` (the same fallback `create.go` uses for `safe-phrase`).
- Never under `/{prefix}/sandbox/{id}/` (the instance role reads that path).
- `start` never invalidates a credential. Only `rekey` does.
- SSM read errors other than `ParameterNotFound` fail OPEN when a local copy exists (WARN, continue).
- Rekey publish order: box → local → SSM.
- No userdata, profile-schema, or sidecar change. Deploy = `make build`, plus `make build-lambdas` + `km init --dry-run=false` for the ttl-handler IAM grant only.
- Test seam: in `internal/app/cmd/main_test.go`'s `TestMain`, `NewSharedCredStoreFunc` returns `(nil, nil)`, so `syncSharedCredential` with a nil store behaves exactly as today's code.
- Every commit message ends with the attribution block from the session reminder. Never commit to `main`; work stays on `worktree-feat+shared-access-creds`.
- Run `go build ./...` before every commit. Tests under `internal/app/cmd` have 5 known pre-existing failures (`TestBootstrapSCP*`, `TestCluster*`) — they are not regressions.
- Use `git add <explicit paths>` for every commit (shared index across worktrees).

---

### Task 1: `sshkey.PublicKeyLine` — derive the authorized_keys line from a private PEM

**Files:**
- Modify: `pkg/sshkey/keygen.go`
- Test: `pkg/sshkey/keygen_test.go`

**Interfaces:**
- Produces: `func PublicKeyLine(privPEM []byte, comment string) (string, error)` — returns `"ssh-ed25519 <b64> <comment>"` (no trailing newline), same format `GenerateAndWrite` returns.

- [ ] **Step 1: Write the failing test**

Append to `pkg/sshkey/keygen_test.go` (create the file if it does not exist; check with `ls pkg/sshkey/`):

```go
package sshkey

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublicKeyLine_MatchesGenerateAndWrite(t *testing.T) {
	dir := t.TempDir()
	priv := filepath.Join(dir, "k")
	pub := priv + ".pub"
	want, err := GenerateAndWrite(priv, pub, "km-sbx-1")
	if err != nil {
		t.Fatal(err)
	}
	pem, err := os.ReadFile(priv)
	if err != nil {
		t.Fatal(err)
	}
	got, err := PublicKeyLine(pem, "km-sbx-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("PublicKeyLine = %q, want %q", got, want)
	}
}

func TestPublicKeyLine_RejectsGarbage(t *testing.T) {
	if _, err := PublicKeyLine([]byte("not a key"), "c"); err == nil {
		t.Error("expected error for garbage input")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/sshkey/ -run TestPublicKeyLine -v`
Expected: FAIL — `undefined: PublicKeyLine`

- [ ] **Step 3: Implement**

Append to `pkg/sshkey/keygen.go`:

```go
// PublicKeyLine derives the single-line authorized_keys entry
// ("ssh-ed25519 <base64> <comment>", no trailing newline) from an OpenSSH
// private-key PEM, so a laptop that pulls only the private key from SSM can
// rewrite the .pub file the fingerprint and doctor code read. The comment is
// supplied rather than recovered from the PEM so the output is byte-identical
// to what GenerateAndWrite returned for the same key.
func PublicKeyLine(privPEM []byte, comment string) (string, error) {
	signer, err := gossh.ParsePrivateKey(privPEM)
	if err != nil {
		return "", fmt.Errorf("sshkey: parse private key: %w", err)
	}
	return fmt.Sprintf("%s %s %s",
		signer.PublicKey().Type(),
		base64.StdEncoding.EncodeToString(signer.PublicKey().Marshal()),
		comment), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/sshkey/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/sshkey/keygen.go pkg/sshkey/keygen_test.go
git commit -m "feat(sshkey): PublicKeyLine derives the authorized_keys line from a private PEM"
```

---

### Task 2: `pkg/aws/access.go` — parameter paths, get, put

**Files:**
- Create: `pkg/aws/access.go`
- Test: `pkg/aws/access_test.go`

**Interfaces:**
- Consumes: `IdentitySSMAPI` (already in `pkg/aws/identity.go`).
- Produces:
  - `const AccessKindSSHKey = "ssh-key"`, `const AccessKindDesktopCred = "desktop-cred"`
  - `func AccessParamPrefix(resourcePrefix string) string` → `/{prefix}/access/`
  - `func AccessParamPath(resourcePrefix, sandboxID, kind string) string`
  - `func SSHKeyPath(resourcePrefix, sandboxID string) string`
  - `func DesktopCredPath(resourcePrefix, sandboxID string) string`
  - `func GetAccessParam(ctx context.Context, client IdentitySSMAPI, path string) (value string, found bool, err error)` — `("", false, nil)` on `*ssmtypes.ParameterNotFound`.
  - `func PutAccessParam(ctx context.Context, client IdentitySSMAPI, path, value, kmsKeyID string) error` — SecureString, `Overwrite: true`, `KeyId` set only when `kmsKeyID != ""`.

- [ ] **Step 1: Write the failing test**

Create `pkg/aws/access_test.go`:

```go
package aws

import (
	"context"
	"errors"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

func TestAccessParamPaths(t *testing.T) {
	if got, want := AccessParamPrefix("km2"), "/km2/access/"; got != want {
		t.Errorf("prefix = %q, want %q", got, want)
	}
	if got, want := SSHKeyPath("km", "learn-abc"), "/km/access/learn-abc/ssh-key"; got != want {
		t.Errorf("ssh path = %q, want %q", got, want)
	}
	if got, want := DesktopCredPath("km", "learn-abc"), "/km/access/learn-abc/desktop-cred"; got != want {
		t.Errorf("desktop path = %q, want %q", got, want)
	}
	// The sandbox instance role reads /{prefix}/sandbox/{id}/*; the access
	// namespace must never be under it.
	if got := SSHKeyPath("km", "x"); len(got) < 4 || got[:4] != "/km/" || got[4:12] == "sandbox/" {
		t.Errorf("access path must not live under /{prefix}/sandbox/: %q", got)
	}
}

func TestGetAccessParam_NotFoundIsAbsentNotError(t *testing.T) {
	m := &mockIdentitySSMAPI{getParameterErr: &ssmtypes.ParameterNotFound{}}
	v, found, err := GetAccessParam(context.Background(), m, "/km/access/x/ssh-key")
	if err != nil || found || v != "" {
		t.Errorf("got (%q,%v,%v), want (\"\",false,nil)", v, found, err)
	}
	if !awssdk.ToBool(awssdk.Bool(m.getParameterInput.WithDecryption != nil && *m.getParameterInput.WithDecryption)) {
		t.Error("GetAccessParam must request WithDecryption=true")
	}
}

func TestGetAccessParam_Present(t *testing.T) {
	m := &mockIdentitySSMAPI{getParameterValue: "kasm:hunter2"}
	v, found, err := GetAccessParam(context.Background(), m, "/km/access/x/desktop-cred")
	if err != nil || !found || v != "kasm:hunter2" {
		t.Errorf("got (%q,%v,%v)", v, found, err)
	}
}

func TestGetAccessParam_OtherErrorPropagates(t *testing.T) {
	boom := errors.New("throttled")
	m := &mockIdentitySSMAPI{getParameterErr: boom}
	_, _, err := GetAccessParam(context.Background(), m, "/km/access/x/ssh-key")
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want wraps %v", err, boom)
	}
}

func TestPutAccessParam_SecureStringOverwriteWithKey(t *testing.T) {
	m := &mockIdentitySSMAPI{}
	if err := PutAccessParam(context.Background(), m, "/km/access/x/ssh-key", "PEM", "alias/km-platform-km-use1"); err != nil {
		t.Fatal(err)
	}
	in := m.putParameterInput
	if awssdk.ToString(in.Name) != "/km/access/x/ssh-key" || awssdk.ToString(in.Value) != "PEM" {
		t.Errorf("name/value = %q/%q", awssdk.ToString(in.Name), awssdk.ToString(in.Value))
	}
	if in.Type != ssmtypes.ParameterTypeSecureString {
		t.Errorf("type = %v, want SecureString", in.Type)
	}
	if !awssdk.ToBool(in.Overwrite) {
		t.Error("Overwrite must be true — last rekey wins")
	}
	if awssdk.ToString(in.KeyId) != "alias/km-platform-km-use1" {
		t.Errorf("KeyId = %q", awssdk.ToString(in.KeyId))
	}
}

func TestPutAccessParam_EmptyKeyIDOmitsKeyId(t *testing.T) {
	m := &mockIdentitySSMAPI{}
	if err := PutAccessParam(context.Background(), m, "/km/access/x/ssh-key", "PEM", ""); err != nil {
		t.Fatal(err)
	}
	if m.putParameterInput.KeyId != nil {
		t.Error("KeyId must be nil when no key is given (SSM default key)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/aws/ -run 'TestAccessParam|TestGetAccessParam|TestPutAccessParam' -v`
Expected: FAIL — undefined symbols.

- [ ] **Step 3: Implement**

Create `pkg/aws/access.go`:

```go
package aws

import (
	"context"
	"errors"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// Shared sandbox access credentials.
//
// One SSH private key and one KasmVNC password per sandbox, shared by every
// operator of the install, stored as SecureStrings under
// /{prefix}/access/{sandbox-id}/. SSM is the source of truth; the files under
// ~/.km/keys and ~/.km/desktop are a per-laptop cache reconciled on every
// `km vscode|desktop|herdr|tunnel start`.
//
// The namespace is deliberately NOT /{prefix}/sandbox/{id}/: the instance role
// is granted ssm:GetParameter* on that whole path, and the box has no business
// holding its own operator private key. /{prefix}/access/ sits inside the
// operator policy's parameter/{prefix}/* grant, so laptops need no IAM change.
// See docs/superpowers/specs/2026-09-20-shared-sandbox-access-credentials-design.md.

const (
	AccessKindSSHKey      = "ssh-key"      // OpenSSH private-key PEM; the public key is derived from it
	AccessKindDesktopCred = "desktop-cred" // "user:pass", byte-identical to ~/.km/desktop/<id>
)

// AccessParamPrefix is the SSM path every shared access credential lives under.
func AccessParamPrefix(resourcePrefix string) string {
	return fmt.Sprintf("/%s/access/", resourcePrefix)
}

// AccessParamPath is the parameter for one credential kind of one sandbox.
func AccessParamPath(resourcePrefix, sandboxID, kind string) string {
	return AccessParamPrefix(resourcePrefix) + sandboxID + "/" + kind
}

// SSHKeyPath is the shared VS Code / herdr / tunnel ed25519 private key.
func SSHKeyPath(resourcePrefix, sandboxID string) string {
	return AccessParamPath(resourcePrefix, sandboxID, AccessKindSSHKey)
}

// DesktopCredPath is the shared KasmVNC "user:pass".
func DesktopCredPath(resourcePrefix, sandboxID string) string {
	return AccessParamPath(resourcePrefix, sandboxID, AccessKindDesktopCred)
}

// GetAccessParam reads one shared credential. ParameterNotFound is reported as
// (found=false, err=nil) because "nobody has published yet" is an ordinary
// state, not a failure; every other error is returned so the caller can
// decide whether to fail open on it.
func GetAccessParam(ctx context.Context, client IdentitySSMAPI, path string) (string, bool, error) {
	out, err := client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           awssdk.String(path),
		WithDecryption: awssdk.Bool(true),
	})
	if err != nil {
		var nf *ssmtypes.ParameterNotFound
		if errors.As(err, &nf) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get %s: %w", path, err)
	}
	if out == nil || out.Parameter == nil || out.Parameter.Value == nil {
		return "", false, nil
	}
	return *out.Parameter.Value, true, nil
}

// PutAccessParam writes one shared credential, overwriting whatever was there:
// the last rekey wins, by design (no version CAS). kmsKeyID may be a key id,
// ARN or alias name; empty means the SSM default key.
func PutAccessParam(ctx context.Context, client IdentitySSMAPI, path, value, kmsKeyID string) error {
	in := &ssm.PutParameterInput{
		Name:      awssdk.String(path),
		Value:     awssdk.String(value),
		Type:      ssmtypes.ParameterTypeSecureString,
		Overwrite: awssdk.Bool(true),
	}
	if kmsKeyID != "" {
		in.KeyId = awssdk.String(kmsKeyID)
	}
	if _, err := client.PutParameter(ctx, in); err != nil {
		return fmt.Errorf("put %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/aws/ -run 'TestAccessParam|TestGetAccessParam|TestPutAccessParam' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/aws/access.go pkg/aws/access_test.go
git commit -m "feat(aws): shared access-credential SSM parameters under /{prefix}/access/"
```

---

### Task 3: Cleanup on destroy + ttl-handler IAM pairing guard

**Files:**
- Modify: `pkg/aws/identity.go` (`CleanupSandboxIdentity`, ~line 843)
- Modify: `infra/modules/ttl-handler/v1.0.0/main.tf` (`IdentitySSMDelete` statement, ~line 849)
- Test: `pkg/aws/identity_test.go` (existing `TestCleanupSandboxIdentity*` — find with `grep -n "func TestCleanupSandboxIdentity" pkg/aws/identity_test.go`)
- Test: `cmd/ttl-handler/iam_ssm_delete_grants_test.go` (new)

**Interfaces:**
- Consumes: `SSHKeyPath`, `DesktopCredPath` (Task 2).

- [ ] **Step 1: Write the failing unit test for cleanup**

Append to `pkg/aws/identity_test.go`:

```go
func TestCleanupSandboxIdentity_DeletesSharedAccessParams(t *testing.T) {
	ssmMock := &mockIdentitySSMAPI{}
	ddb := &mockIdentityTableAPI{}
	if err := CleanupSandboxIdentity(context.Background(), ssmMock, ddb, "km-identities", "km", "sbx-1"); err != nil {
		t.Fatal(err)
	}
	deleted := map[string]bool{}
	for _, in := range ssmMock.deleteParameterInputs {
		deleted[*in.Name] = true
	}
	for _, want := range []string{SSHKeyPath("km", "sbx-1"), DesktopCredPath("km", "sbx-1")} {
		if !deleted[want] {
			t.Errorf("CleanupSandboxIdentity did not delete %s; deleted: %v", want, deleted)
		}
	}
}
```

(If `mockIdentityTableAPI`'s zero value cannot serve `DeleteItem`, look at how the existing `TestCleanupSandboxIdentity` test constructs it and copy that.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/aws/ -run TestCleanupSandboxIdentity_DeletesSharedAccessParams -v`
Expected: FAIL — the two paths are not deleted.

- [ ] **Step 3: Implement cleanup**

In `pkg/aws/identity.go`, inside `CleanupSandboxIdentity`, after the `safe-phrase` delete and before the DynamoDB `DeleteItem`:

```go
	// Shared access credentials (SSH key + desktop password) published for
	// km vscode|desktop|herdr|tunnel start. Same idempotency as the others.
	for _, p := range []string{SSHKeyPath(resourcePrefix, sandboxID), DesktopCredPath(resourcePrefix, sandboxID)} {
		if err := deleteSSMParameter(ctx, ssmClient, p); err != nil {
			return err
		}
	}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./pkg/aws/ -run TestCleanupSandboxIdentity -v`
Expected: PASS (all cleanup tests).

- [ ] **Step 5: Write the failing IAM pairing guard**

Create `cmd/ttl-handler/iam_ssm_delete_grants_test.go`:

```go
package main

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	awspkg "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// recordingSSM captures every DeleteParameter name CleanupSandboxIdentity issues.
type recordingSSM struct{ deleted []string }

func (r *recordingSSM) PutParameter(context.Context, *ssm.PutParameterInput, ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	return &ssm.PutParameterOutput{}, nil
}
func (r *recordingSSM) GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	return &ssm.GetParameterOutput{}, nil
}
func (r *recordingSSM) DeleteParameter(_ context.Context, in *ssm.DeleteParameterInput, _ ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	r.deleted = append(r.deleted, *in.Name)
	return &ssm.DeleteParameterOutput{}, nil
}

type noopDDB struct{}

func (noopDDB) PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}
func (noopDDB) GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{}, nil
}
func (noopDDB) DeleteItem(context.Context, *dynamodb.DeleteItemInput, ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	return &dynamodb.DeleteItemOutput{}, nil
}

// Every SSM parameter CleanupSandboxIdentity deletes must have a matching
// ssm:DeleteParameter ARN in the ttl-handler module — the grant there is per
// EXACT NAME, so a parameter added to cleanup without an ARN 403s silently on
// every TTL expiry (the handler logs cleanup errors non-fatally) and leaves an
// orphan. Name-agnostic: it runs the real cleanup against a recorder and maps
// each path to the ARN shape the module uses.
func TestTTLHandlerModule_EveryCleanupSSMParamHasADeleteGrant(t *testing.T) {
	raw, err := os.ReadFile(ttlHandlerModuleMainTF)
	if err != nil {
		t.Fatalf("read %s: %v", ttlHandlerModuleMainTF, err)
	}
	tf := string(raw)

	const prefix, sbx = "PFX", "SBX"
	rec := &recordingSSM{}
	if err := awspkg.CleanupSandboxIdentity(context.Background(), rec, noopDDB{}, "t", prefix, sbx); err != nil {
		t.Fatal(err)
	}
	if len(rec.deleted) == 0 {
		t.Fatal("CleanupSandboxIdentity deleted nothing — the recorder has drifted from the function")
	}

	arnRe := regexp.MustCompile(`parameter/\$\{var\.resource_prefix\}/([^"]+)"`)
	granted := map[string]bool{}
	for _, m := range arnRe.FindAllStringSubmatch(tf, -1) {
		granted[m[1]] = true
	}

	var missing []string
	for _, p := range rec.deleted {
		// "/PFX/sandbox/SBX/signing-key" -> "sandbox/*/signing-key"
		rel := strings.TrimPrefix(p, "/"+prefix+"/")
		want := strings.Replace(rel, sbx, "*", 1)
		if !granted[want] {
			missing = append(missing, want)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("CleanupSandboxIdentity deletes these parameters but %s grants no ssm:DeleteParameter on them: %v\n"+
			"Add \"arn:aws:ssm:*:${data.aws_caller_identity.current.account_id}:parameter/${var.resource_prefix}/<path>\" to the IdentitySSMDelete statement.",
			ttlHandlerModuleMainTF, missing)
	}
}
```

Note: `ttlHandlerModuleMainTF` is already declared in `cmd/ttl-handler/iam_table_grants_test.go` (same package) — do not redeclare it. Check the real `IdentityTableAPI` interface in `pkg/aws/identity.go` (~line 48) and give `noopDDB` exactly those methods.

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./cmd/ttl-handler/ -run TestTTLHandlerModule_EveryCleanupSSMParamHasADeleteGrant -v`
Expected: FAIL naming `access/*/ssh-key` and `access/*/desktop-cred`.

- [ ] **Step 7: Add the two ARNs**

In `infra/modules/ttl-handler/v1.0.0/main.tf`, the `IdentitySSMDelete` statement's `Resource` list gains:

```hcl
          "arn:aws:ssm:*:${data.aws_caller_identity.current.account_id}:parameter/${var.resource_prefix}/access/*/ssh-key",
          "arn:aws:ssm:*:${data.aws_caller_identity.current.account_id}:parameter/${var.resource_prefix}/access/*/desktop-cred",
```

Add a one-line comment above the statement: `# access/* — shared SSH key + desktop password (see pkg/aws/access.go); grant is per exact name, paired by TestTTLHandlerModule_EveryCleanupSSMParamHasADeleteGrant.`

- [ ] **Step 8: Run to verify it passes**

Run: `go test ./cmd/ttl-handler/ -run 'TestTTLHandlerModule' -v`
Expected: PASS. Then temporarily delete one of the two ARNs, re-run, confirm FAIL, restore it.

- [ ] **Step 9: Commit**

```bash
git add pkg/aws/identity.go pkg/aws/identity_test.go cmd/ttl-handler/iam_ssm_delete_grants_test.go infra/modules/ttl-handler/v1.0.0/main.tf
git commit -m "feat(destroy): clean up shared access params; ttl-handler delete grant + pairing guard"
```

---

### Task 4: `syncSharedCredential` — the store, the seam, the table

**Files:**
- Create: `internal/app/cmd/shared_cred.go`
- Test: `internal/app/cmd/shared_cred_test.go`
- Modify: `internal/app/cmd/main_test.go` (`TestMain`, seam)

**Interfaces:**
- Consumes: `kmaws.GetAccessParam/PutAccessParam/SSHKeyPath/DesktopCredPath` (Task 2), `sshkey.PublicKeyLine` (Task 1), `kmaws.LoadAWSConfig`, `cfg.GetResourcePrefix()`, `cfg.GetPlatformKMSAlias()`.
- Produces:
  - `type sharedCredStore struct { ssm kmaws.IdentitySSMAPI; prefix, kmsKey string }`
  - `var NewSharedCredStoreFunc func(ctx context.Context, cfg *config.Config) (*sharedCredStore, error)` — prod builds a real client; `(nil, nil)` means "no store, legacy behaviour".
  - `var sshKeyKind, desktopCredKind credKind`
  - `func syncSharedCredential(ctx context.Context, store *sharedCredStore, kind credKind, sandboxID string, w io.Writer) (localPath string, err error)`
  - `func publishSharedCredential(ctx context.Context, cfg *config.Config, kind credKind, sandboxID string, content []byte) error` — nil store ⇒ nil.
  - `func localCredPath(kind credKind, sandboxID string) (string, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/app/cmd/shared_cred_test.go`:

```go
package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/whereiskurt/klanker-maker/pkg/sshkey"
)

// fakeAccessSSM is an in-memory SSM for the shared-credential store.
type fakeAccessSSM struct {
	params map[string]string
	getErr error
	putErr error
	puts   int
}

func (f *fakeAccessSSM) GetParameter(_ context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	v, ok := f.params[*in.Name]
	if !ok {
		return nil, &ssmtypes.ParameterNotFound{}
	}
	return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: awssdk.String(v)}}, nil
}
func (f *fakeAccessSSM) PutParameter(_ context.Context, in *ssm.PutParameterInput, _ ...func(*ssm.Options)) (*ssm.PutParameterOutput, error) {
	f.puts++
	if f.putErr != nil {
		return nil, f.putErr
	}
	if f.params == nil {
		f.params = map[string]string{}
	}
	f.params[*in.Name] = *in.Value
	return &ssm.PutParameterOutput{}, nil
}
func (f *fakeAccessSSM) DeleteParameter(_ context.Context, in *ssm.DeleteParameterInput, _ ...func(*ssm.Options)) (*ssm.DeleteParameterOutput, error) {
	delete(f.params, *in.Name)
	return &ssm.DeleteParameterOutput{}, nil
}

func newTestStore(f *fakeAccessSSM) *sharedCredStore {
	return &sharedCredStore{ssm: f, prefix: "km", kmsKey: "alias/km-platform-km-use1"}
}

// genKeyPEM makes a real ed25519 OpenSSH PEM so PublicKeyLine can derive from it.
func genKeyPEM(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	if _, err := sshkey.GenerateAndWrite(filepath.Join(dir, "k"), filepath.Join(dir, "k.pub"), "km-sbx"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "k"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSyncSharedCredential_PullWhenLocalAbsent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pem := genKeyPEM(t)
	f := &fakeAccessSSM{params: map[string]string{"/km/access/sbx/ssh-key": string(pem)}}
	var out bytes.Buffer
	p, err := syncSharedCredential(context.Background(), newTestStore(f), sshKeyKind, "sbx", &out)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, pem) {
		t.Error("local private key != SSM value")
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("private key mode = %o, want 0600", st.Mode().Perm())
	}
	pub, err := os.ReadFile(p + ".pub")
	if err != nil {
		t.Fatalf(".pub not written: %v", err)
	}
	want, _ := sshkey.PublicKeyLine(pem, "km-sbx")
	if strings.TrimSpace(string(pub)) != want {
		t.Errorf(".pub = %q, want %q", pub, want)
	}
	if !strings.Contains(out.String(), "Pulled shared key from SSM") {
		t.Errorf("message: %q", out.String())
	}
}

func TestSyncSharedCredential_RefreshWhenLocalDiffers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := genKeyPEM(t)
	newer := genKeyPEM(t)
	local := filepath.Join(home, ".km", "keys", "sbx")
	os.MkdirAll(filepath.Dir(local), 0o700)
	os.WriteFile(local, old, 0o600)
	f := &fakeAccessSSM{params: map[string]string{"/km/access/sbx/ssh-key": string(newer)}}
	var out bytes.Buffer
	if _, err := syncSharedCredential(context.Background(), newTestStore(f), sshKeyKind, "sbx", &out); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(local)
	if !bytes.Equal(got, newer) {
		t.Error("local key was not refreshed from SSM")
	}
	if !strings.Contains(out.String(), "refreshed from SSM (rekeyed elsewhere)") {
		t.Errorf("message: %q", out.String())
	}
	if f.puts != 0 {
		t.Error("start must never write SSM when SSM already has a value")
	}
}

func TestSyncSharedCredential_SilentWhenSame(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pem := genKeyPEM(t)
	local := filepath.Join(home, ".km", "keys", "sbx")
	os.MkdirAll(filepath.Dir(local), 0o700)
	os.WriteFile(local, pem, 0o600)
	f := &fakeAccessSSM{params: map[string]string{"/km/access/sbx/ssh-key": string(pem) + "\n"}} // trailing newline must not count as "differs"
	var out bytes.Buffer
	if _, err := syncSharedCredential(context.Background(), newTestStore(f), sshKeyKind, "sbx", &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("expected silence, got %q", out.String())
	}
}

func TestSyncSharedCredential_PublishWhenSSMAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	local := filepath.Join(home, ".km", "desktop", "sbx")
	os.MkdirAll(filepath.Dir(local), 0o700)
	os.WriteFile(local, []byte("kasm:hunter2"), 0o600)
	f := &fakeAccessSSM{}
	var out bytes.Buffer
	if _, err := syncSharedCredential(context.Background(), newTestStore(f), desktopCredKind, "sbx", &out); err != nil {
		t.Fatal(err)
	}
	if f.params["/km/access/sbx/desktop-cred"] != "kasm:hunter2" {
		t.Errorf("SSM after publish = %v", f.params)
	}
	if !strings.Contains(out.String(), "Published existing desktop credential to SSM") {
		t.Errorf("message: %q", out.String())
	}
}

func TestSyncSharedCredential_PublishFailureIsWarnNotError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	local := filepath.Join(home, ".km", "desktop", "sbx")
	os.MkdirAll(filepath.Dir(local), 0o700)
	os.WriteFile(local, []byte("kasm:hunter2"), 0o600)
	f := &fakeAccessSSM{putErr: errors.New("denied")}
	var out bytes.Buffer
	p, err := syncSharedCredential(context.Background(), newTestStore(f), desktopCredKind, "sbx", &out)
	if err != nil || p != local {
		t.Fatalf("got (%q,%v)", p, err)
	}
	if !strings.Contains(out.String(), "[warn] could not publish desktop credential to SSM") {
		t.Errorf("message: %q", out.String())
	}
}

func TestSyncSharedCredential_BothAbsentNamesRekey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := syncSharedCredential(context.Background(), newTestStore(&fakeAccessSSM{}), sshKeyKind, "sbx", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "km vscode rekey sbx") {
		t.Errorf("err = %v, want it to name km vscode rekey sbx", err)
	}
	_, err = syncSharedCredential(context.Background(), newTestStore(&fakeAccessSSM{}), desktopCredKind, "sbx", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "km desktop rekey sbx") {
		t.Errorf("err = %v, want it to name km desktop rekey sbx", err)
	}
}

func TestSyncSharedCredential_ReadErrorFailsOpenWithLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pem := genKeyPEM(t)
	local := filepath.Join(home, ".km", "keys", "sbx")
	os.MkdirAll(filepath.Dir(local), 0o700)
	os.WriteFile(local, pem, 0o600)
	f := &fakeAccessSSM{getErr: errors.New("ThrottlingException")}
	var out bytes.Buffer
	p, err := syncSharedCredential(context.Background(), newTestStore(f), sshKeyKind, "sbx", &out)
	if err != nil || p != local {
		t.Fatalf("got (%q,%v), want local path and nil", p, err)
	}
	if !strings.Contains(out.String(), "[warn] could not read shared key from SSM") {
		t.Errorf("message: %q", out.String())
	}
}

func TestSyncSharedCredential_ReadErrorWithNoLocalIsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := &fakeAccessSSM{getErr: errors.New("ThrottlingException")}
	_, err := syncSharedCredential(context.Background(), newTestStore(f), sshKeyKind, "sbx", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "ThrottlingException") {
		t.Errorf("err = %v", err)
	}
}

func TestSyncSharedCredential_NilStoreIsLegacyBehaviour(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := syncSharedCredential(context.Background(), nil, sshKeyKind, "sbx", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "copy the ~/.km/keys/sbx* files over") {
		t.Errorf("nil store must keep today's error text; got %v", err)
	}
	_, err = syncSharedCredential(context.Background(), nil, desktopCredKind, "sbx", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "copy the ~/.km/desktop/sbx file over") {
		t.Errorf("nil store must keep today's error text; got %v", err)
	}
}

func TestPublishSharedCredential_NilStoreIsNoop(t *testing.T) {
	// TestMain sets NewSharedCredStoreFunc to return (nil, nil).
	if err := publishSharedCredential(context.Background(), nil, sshKeyKind, "sbx", []byte("PEM")); err != nil {
		t.Errorf("nil store must be a no-op, got %v", err)
	}
}

func TestPublishSharedCredential_WritesParam(t *testing.T) {
	f := &fakeAccessSSM{}
	saved := NewSharedCredStoreFunc
	NewSharedCredStoreFunc = func(context.Context, *config.Config) (*sharedCredStore, error) { return newTestStore(f), nil }
	defer func() { NewSharedCredStoreFunc = saved }()
	if err := publishSharedCredential(context.Background(), nil, desktopCredKind, "sbx", []byte("kasm:pw")); err != nil {
		t.Fatal(err)
	}
	if f.params["/km/access/sbx/desktop-cred"] != "kasm:pw" {
		t.Errorf("params = %v", f.params)
	}
}
```

Add `"github.com/whereiskurt/klanker-maker/internal/app/config"` to the test file's imports.

- [ ] **Step 2: Add the seam to TestMain**

In `internal/app/cmd/main_test.go`, inside `TestMain` after the `BuildLambdaZipsFunc` line:

```go
	// Shared access-credential store: nil means "no SSM, legacy local-file
	// behaviour", which keeps every pre-existing vscode/desktop/herdr/tunnel
	// test byte-identical. Tests exercising the sync override this locally.
	NewSharedCredStoreFunc = func(context.Context, *config.Config) (*sharedCredStore, error) { return nil, nil }
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/app/cmd/ -run 'TestSyncSharedCredential|TestPublishSharedCredential' 2>&1 | head`
Expected: compile FAIL — undefined `syncSharedCredential`, `sharedCredStore`, etc.

- [ ] **Step 4: Implement**

Create `internal/app/cmd/shared_cred.go`:

```go
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
	"github.com/whereiskurt/klanker-maker/pkg/sshkey"
)

// Shared sandbox access credentials — the per-laptop half.
//
// ~/.km/keys/<id> (ed25519 private key) and ~/.km/desktop/<id> ("user:pass")
// used to exist only on the laptop that ran km create. They are now a CACHE
// of the SSM parameters under /{prefix}/access/<id>/ (pkg/aws/access.go):
// every km vscode|desktop|herdr|tunnel start reconciles the local file to SSM
// first, so any operator of the install can hop into any sandbox, and a rekey
// by one analyst reaches the others on their next start. SSM is the truth;
// "start" never invalidates a credential — only "rekey" does.
//
// Design: docs/superpowers/specs/2026-09-20-shared-sandbox-access-credentials-design.md

// sharedCredStore is the SSM-backed store. A nil *sharedCredStore means "no
// store available" and every function here degrades to the pre-existing
// local-file-only behaviour — that is what the test binary uses.
type sharedCredStore struct {
	ssm    kmaws.IdentitySSMAPI
	prefix string
	kmsKey string
}

// NewSharedCredStoreFunc builds the store. Seam: TestMain replaces it with a
// func returning (nil, nil). A non-nil error is reported by callers as a WARN
// and treated as "no store" — the credential is a convenience, and an SSO or
// network hiccup must never stop a laptop that worked yesterday.
var NewSharedCredStoreFunc = func(ctx context.Context, cfg *config.Config) (*sharedCredStore, error) {
	awsCfg, err := kmaws.LoadAWSConfig(ctx, "klanker-terraform")
	if err != nil {
		return nil, err
	}
	// Same fallback create.go uses for the safe-phrase parameter: the resolved
	// platform key ARN when km init exported it, else the alias (SSM accepts
	// an alias name as KeyId).
	kmsKey := os.Getenv("KM_PLATFORM_KMS_KEY_ARN")
	if kmsKey == "" {
		kmsKey = cfg.GetPlatformKMSAlias()
	}
	return &sharedCredStore{ssm: ssm.NewFromConfig(awsCfg), prefix: cfg.GetResourcePrefix(), kmsKey: kmsKey}, nil
}

// credKind describes one of the two credentials: where it lives in SSM, where
// it lives locally, how to compare two copies, what to do after a pull, and
// the operator-facing words.
type credKind struct {
	name      string                                      // "key" / "desktop credential" — used in messages
	param     func(prefix, sandboxID string) string        // SSM path
	localRel  func(sandboxID string) string                // path under $HOME
	rekeyVerb string                                       // "km vscode rekey" / "km desktop rekey"
	legacyErr func(sandboxID, localPath string) error      // pre-existing "not found" error, kept verbatim for nil store
	normalize func(b []byte) string                        // equality basis
	afterPull func(localPath string, content []byte) error // side files (the .pub)
}

var sshKeyKind = credKind{
	name:  "key",
	param: kmaws.SSHKeyPath,
	localRel: func(id string) string { return filepath.Join(".km", "keys", id) },
	rekeyVerb: "km vscode rekey",
	legacyErr: func(id, p string) error {
		return fmt.Errorf("private key for %s not found at %s. If you created this sandbox on a different machine, copy the ~/.km/keys/%s* files over", id, p, id)
	},
	normalize: func(b []byte) string { return strings.TrimRight(string(b), "\n") },
	afterPull: func(localPath string, content []byte) error {
		id := filepath.Base(localPath)
		line, err := sshkey.PublicKeyLine(content, "km-"+id)
		if err != nil {
			return fmt.Errorf("derive public key: %w", err)
		}
		return os.WriteFile(localPath+".pub", []byte(line+"\n"), 0o644)
	},
}

var desktopCredKind = credKind{
	name:  "desktop credential",
	param: kmaws.DesktopCredPath,
	localRel: func(id string) string { return filepath.Join(".km", "desktop", id) },
	rekeyVerb: "km desktop rekey",
	legacyErr: func(id, p string) error {
		return fmt.Errorf("desktop credential for %s not found at %s. If you created this sandbox on a different machine, copy the ~/.km/desktop/%s file over", id, p, id)
	},
	normalize: func(b []byte) string { return strings.TrimSpace(string(b)) },
	afterPull: func(string, []byte) error { return nil },
}

// localCredPath is $HOME-relative resolution for one kind.
func localCredPath(kind credKind, sandboxID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, kind.localRel(sandboxID)), nil
}

// writeLocalCredential writes content atomically (dir 0700, file 0600, .new +
// rename) and runs the kind's afterPull.
func writeLocalCredential(kind credKind, localPath string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(localPath), err)
	}
	tmp := localPath + ".new"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, localPath); err != nil {
		return fmt.Errorf("commit %s: %w", localPath, err)
	}
	return kind.afterPull(localPath, content)
}

// syncSharedCredential reconciles the local file with SSM and returns the
// local path to use. The table (spec §4.1):
//
//	SSM      local    action
//	present  absent   pull        "✓ Pulled shared <kind> from SSM"
//	present  differs  overwrite   "✓ Local <kind> refreshed from SSM (rekeyed elsewhere)"
//	present  same     nothing
//	absent   present  publish     "✓ Published existing <kind> to SSM"   (publish failure = warn)
//	absent   absent   error naming <rekeyVerb> <id>
//	error    present  warn, use local (fail open)
//	error    absent   error
//
// A nil store is the legacy path: local file or the pre-existing error.
func syncSharedCredential(ctx context.Context, store *sharedCredStore, kind credKind, sandboxID string, w io.Writer) (string, error) {
	localPath, err := localCredPath(kind, sandboxID)
	if err != nil {
		return "", err
	}
	localBytes, readErr := os.ReadFile(localPath)
	localPresent := readErr == nil
	if readErr != nil && !os.IsNotExist(readErr) {
		return "", fmt.Errorf("read local %s: %w", kind.name, readErr)
	}

	if store == nil {
		if !localPresent {
			return "", kind.legacyErr(sandboxID, localPath)
		}
		return localPath, nil
	}

	path := kind.param(store.prefix, sandboxID)
	remote, found, getErr := kmaws.GetAccessParam(ctx, store.ssm, path)
	switch {
	case getErr != nil && localPresent:
		fmt.Fprintf(w, "  [warn] could not read shared %s from SSM (%v); using local copy\n", kind.name, getErr)
		return localPath, nil
	case getErr != nil:
		return "", fmt.Errorf("read shared %s for %s from SSM: %w", kind.name, sandboxID, getErr)
	case found && !localPresent:
		if err := writeLocalCredential(kind, localPath, []byte(remote)); err != nil {
			return "", err
		}
		fmt.Fprintf(w, "✓ Pulled shared %s from SSM\n", kind.name)
	case found && kind.normalize(localBytes) != kind.normalize([]byte(remote)):
		if err := writeLocalCredential(kind, localPath, []byte(remote)); err != nil {
			return "", err
		}
		fmt.Fprintf(w, "✓ Local %s refreshed from SSM (rekeyed elsewhere)\n", kind.name)
	case found:
		// identical — silent
	case localPresent:
		if err := kmaws.PutAccessParam(ctx, store.ssm, path, string(localBytes), store.kmsKey); err != nil {
			fmt.Fprintf(w, "  [warn] could not publish %s to SSM (%v); other analysts cannot connect until this succeeds\n", kind.name, err)
		} else {
			fmt.Fprintf(w, "✓ Published existing %s to SSM\n", kind.name)
		}
	default:
		return "", fmt.Errorf("no shared %s for %s in SSM and none at %s — run: %s %s", kind.name, sandboxID, localPath, kind.rekeyVerb, sandboxID)
	}
	return localPath, nil
}

// publishSharedCredential writes content to SSM for one kind. Used by rekey
// (after box + local are committed) and by create (after generation). A nil
// store (test binary) is a no-op; a store-construction error is returned so
// the caller can decide (create warns, rekey errors).
func publishSharedCredential(ctx context.Context, cfg *config.Config, kind credKind, sandboxID string, content []byte) error {
	store, err := NewSharedCredStoreFunc(ctx, cfg)
	if err != nil {
		return fmt.Errorf("shared credential store: %w", err)
	}
	if store == nil {
		return nil
	}
	return kmaws.PutAccessParam(ctx, store.ssm, kind.param(store.prefix, sandboxID), string(content), store.kmsKey)
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/app/cmd/ -run 'TestSyncSharedCredential|TestPublishSharedCredential' -v`
Expected: PASS (11 tests).

- [ ] **Step 6: Run the whole package to confirm nothing else moved**

Run: `go test ./internal/app/cmd/ 2>&1 | grep -E '^(--- FAIL|FAIL|ok)'`
Expected: only the 5 known pre-existing failures.

- [ ] **Step 7: Commit**

```bash
git add internal/app/cmd/shared_cred.go internal/app/cmd/shared_cred_test.go internal/app/cmd/main_test.go
git commit -m "feat(cmd): syncSharedCredential — reconcile ~/.km/{keys,desktop} with SSM"
```

---

### Task 5: Wire sync into every `start` (vscode, herdr, tunnel k8s/socks, desktop)

**Files:**
- Modify: `internal/app/cmd/tunnel.go:318` (`sandboxKeyPath`), `:165` (caller)
- Modify: `internal/app/cmd/tunnel_socks.go` (the `sandboxKeyPath(` caller — `grep -n sandboxKeyPath internal/app/cmd/tunnel_socks.go`)
- Modify: `internal/app/cmd/vscode.go:138` (`connectPrep`), `:310` (`runVSCodeStart`)
- Modify: `internal/app/cmd/herdr.go:206`, `:260` (`runHerdrStart`)
- Modify: `internal/app/cmd/desktop.go:138` (`runDesktopStart`)
- Test: `internal/app/cmd/herdr_test.go`, `internal/app/cmd/vscode_test.go` (signature updates only), `internal/app/cmd/shared_cred_test.go` (one integration test)

**Interfaces:**
- Changes: `sandboxKeyPath(ctx context.Context, cfg *config.Config, sandboxID string) (string, error)`; `connectPrep(ctx, cfg *config.Config, fetcher, sandboxID, localPort, portSuggestion)`; `runHerdrStart(ctx, cfg *config.Config, fetcher, execFn, ssmClient, sandboxID, localPort, noInstall, noAttach, session)`.

- [ ] **Step 1: Write the failing integration test**

Append to `internal/app/cmd/shared_cred_test.go`:

```go
func TestSandboxKeyPath_PullsFromSSM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pem := genKeyPEM(t)
	f := &fakeAccessSSM{params: map[string]string{"/km/access/sbx/ssh-key": string(pem)}}
	saved := NewSharedCredStoreFunc
	NewSharedCredStoreFunc = func(context.Context, *config.Config) (*sharedCredStore, error) { return newTestStore(f), nil }
	defer func() { NewSharedCredStoreFunc = saved }()

	p, err := sandboxKeyPath(context.Background(), nil, "sbx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("key not materialised at %s: %v", p, err)
	}
	if _, err := os.Stat(p + ".pub"); err != nil {
		t.Fatalf(".pub not materialised: %v", err)
	}
}

func TestSandboxKeyPath_StoreErrorFallsBackToLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	local := filepath.Join(home, ".km", "keys", "sbx")
	os.MkdirAll(filepath.Dir(local), 0o700)
	os.WriteFile(local, genKeyPEM(t), 0o600)
	saved := NewSharedCredStoreFunc
	NewSharedCredStoreFunc = func(context.Context, *config.Config) (*sharedCredStore, error) { return nil, errors.New("sso expired") }
	defer func() { NewSharedCredStoreFunc = saved }()

	p, err := sandboxKeyPath(context.Background(), nil, "sbx")
	if err != nil || p != local {
		t.Errorf("got (%q,%v), want local path and nil", p, err)
	}
}
```

(Add `"github.com/whereiskurt/klanker-maker/internal/app/config"` to the test file's imports.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/cmd/ -run TestSandboxKeyPath 2>&1 | head -5`
Expected: compile FAIL — too many arguments to `sandboxKeyPath`.

- [ ] **Step 3: Change `sandboxKeyPath`**

Replace the whole function in `internal/app/cmd/tunnel.go`:

```go
// sandboxKeyPath locates the per-sandbox private key, reconciling the local
// copy with the shared one in SSM first (syncSharedCredential) so a laptop
// that never ran km create for this sandbox still gets in. A store that cannot
// be built (no creds, no network) is a WARN and falls back to the local file.
func sandboxKeyPath(ctx context.Context, cfg *config.Config, sandboxID string) (string, error) {
	store, err := NewSharedCredStoreFunc(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [warn] shared credential store unavailable (%v); using the local key only\n", err)
		store = nil
	}
	return syncSharedCredential(ctx, store, sshKeyKind, sandboxID, os.Stdout)
}
```

Add `"github.com/whereiskurt/klanker-maker/internal/app/config"` to `tunnel.go`'s imports if not already present (it is — `runTunnelK8s` takes `cfg *config.Config`).

- [ ] **Step 4: Update the callers**

- `tunnel.go:165`: `keyPath, err := sandboxKeyPath(ctx, cfg, sandboxID)`
- `tunnel_socks.go`: same change at its call site (it has `cfg` in scope).
- `vscode.go` `connectPrep`: signature becomes `func connectPrep(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, sandboxID string, localPort, portSuggestion int) (...)` and inside: `privPath, err = sandboxKeyPath(ctx, cfg, sandboxID)`. Update the doc comment's first line to mention the sync.
- `vscode.go` `runVSCodeStart`: rename its `_ *config.Config` parameter to `cfg` and call `connectPrep(ctx, cfg, fetcher, sandboxID, localPort, 22122)`.
- `herdr.go:260`: `func runHerdrStart(ctx context.Context, cfg *config.Config, fetcher SandboxFetcher, execFn ShellExecFunc, ssmClient SSMSendAPI, sandboxID string, localPort int, noInstall, noAttach bool, session string) error` and inside `connectPrep(ctx, cfg, fetcher, sandboxID, localPort, localPort+100)`.
- `herdr.go:206`: `return runHerdrStart(ctx, cfg, f, e, s, sandboxID, localPort, noInstall, !attach, session)` — confirm `cfg` is the closure variable name in that RunE (`sed -n 180,210p internal/app/cmd/herdr.go`).
- `desktop.go` `runDesktopStart`: rename `_ *config.Config` to `cfg`; replace the block from `// Locate the local credential file` through the `os.Stat` error return with:

```go
	// Reconcile the local credential with the shared one in SSM (spec §4.1) —
	// this is what lets a laptop that never ran km create for this sandbox in.
	store, serr := NewSharedCredStoreFunc(ctx, cfg)
	if serr != nil {
		fmt.Fprintf(os.Stderr, "  [warn] shared credential store unavailable (%v); using the local credential only\n", serr)
		store = nil
	}
	credPath, err := syncSharedCredential(ctx, store, desktopCredKind, sandboxID, os.Stdout)
	if err != nil {
		return err
	}
```

- [ ] **Step 5: Update test call sites**

```bash
cd internal/app/cmd
sed -i '' 's/runHerdrStart(ctx, /runHerdrStart(ctx, nil, /g; s/runHerdrStart(context.Background(), /runHerdrStart(context.Background(), nil, /g' herdr_test.go
sed -i '' 's/connectPrep(ctx, /connectPrep(ctx, nil, /g; s/connectPrep(context.Background(), /connectPrep(context.Background(), nil, /g' vscode_test.go herdr_test.go
```

Then `go vet ./internal/app/cmd/` and fix any call site the sed missed (multi-line calls) by hand. `nil` cfg is safe: the TestMain seam never dereferences it.

- [ ] **Step 6: Run**

Run: `go build ./... && go test ./internal/app/cmd/ -run 'TestSandboxKeyPath|TestVSCode|TestHerdr|TestDesktop|TestTunnel' 2>&1 | grep -E '^(--- FAIL|FAIL|ok)'`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/app/cmd/tunnel.go internal/app/cmd/tunnel_socks.go internal/app/cmd/vscode.go internal/app/cmd/herdr.go internal/app/cmd/desktop.go internal/app/cmd/herdr_test.go internal/app/cmd/vscode_test.go internal/app/cmd/shared_cred_test.go
git commit -m "feat(cmd): vscode/herdr/tunnel/desktop start sync the shared credential from SSM"
```

---

### Task 6: Mismatch guard — box `authorized_keys` vs the synced key (vscode + herdr)

**Files:**
- Modify: `internal/app/cmd/shared_cred.go` (two helpers)
- Modify: `internal/app/cmd/vscode.go` (`parseVSCodeStatus`, `runVSCodeStart`)
- Modify: `internal/app/cmd/herdr.go` (`herdrBoxState`, `parseHerdrStatus`, `runHerdrStart`)
- Test: `internal/app/cmd/shared_cred_test.go`, `internal/app/cmd/vscode_test.go`

**Interfaces:**
- Produces: `func authorizedKeyMismatch(boxLine, localPubPath string) bool`; `func sharedKeyMismatchError(sandboxID string) error`; `func parseVSCodeStatusWithKey(out, sandboxID, localPubPath string) error` (existing `parseVSCodeStatus(out, id)` becomes `parseVSCodeStatusWithKey(out, id, "")`); `herdrBoxState.AuthKeysLine string`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/app/cmd/shared_cred_test.go`:

```go
func TestAuthorizedKeyMismatch(t *testing.T) {
	dir := t.TempDir()
	a, _ := sshkey.GenerateAndWrite(filepath.Join(dir, "a"), filepath.Join(dir, "a.pub"), "km-x")
	b, _ := sshkey.GenerateAndWrite(filepath.Join(dir, "b"), filepath.Join(dir, "b.pub"), "km-x")
	pubA := filepath.Join(dir, "a.pub")

	if authorizedKeyMismatch(a, pubA) {
		t.Error("same key must not mismatch")
	}
	if authorizedKeyMismatch(a+" trailing-comment-change", pubA) {
		t.Error("comment differences must not mismatch (fingerprint compare)")
	}
	if !authorizedKeyMismatch(b, pubA) {
		t.Error("different key must mismatch")
	}
	if authorizedKeyMismatch("", pubA) {
		t.Error("empty box line is not a mismatch")
	}
	if authorizedKeyMismatch("garbage not a key", pubA) {
		t.Error("unparseable box line is not a mismatch (pre-existing boxes must keep starting)")
	}
	if authorizedKeyMismatch(b, filepath.Join(dir, "missing.pub")) {
		t.Error("unreadable local pub is not a mismatch")
	}
	if authorizedKeyMismatch(b, "") {
		t.Error("empty local pub path disables the check")
	}
}

func TestParseVSCodeStatusWithKey_MismatchNamesRekey(t *testing.T) {
	dir := t.TempDir()
	_, _ = sshkey.GenerateAndWrite(filepath.Join(dir, "a"), filepath.Join(dir, "a.pub"), "km-sbx")
	other, _ := sshkey.GenerateAndWrite(filepath.Join(dir, "b"), filepath.Join(dir, "b.pub"), "km-sbx")
	out := "=== sshd ===\nactive\n=== authkeys exists ===\nyes\n=== authkeys content ===\n" + other + "\n"
	err := parseVSCodeStatusWithKey(out, "sbx", filepath.Join(dir, "a.pub"))
	if err == nil || !strings.Contains(err.Error(), "km vscode rekey sbx") {
		t.Errorf("err = %v", err)
	}
	if err := parseVSCodeStatusWithKey(out, "sbx", ""); err != nil {
		t.Errorf("no local pub path must skip the check; got %v", err)
	}
}

func TestParseHerdrStatus_CapturesAuthKeysLine(t *testing.T) {
	out := "=== sshd ===\nactive\n=== authkeys exists ===\nyes\n=== authkeys content ===\nssh-ed25519 AAAA km-sbx\n=== herdr path ===\n\n=== herdr version ===\n"
	st := parseHerdrStatus(out)
	if st.AuthKeysLine != "ssh-ed25519 AAAA km-sbx" {
		t.Errorf("AuthKeysLine = %q", st.AuthKeysLine)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/cmd/ -run 'TestAuthorizedKeyMismatch|TestParseVSCodeStatusWithKey|TestParseHerdrStatus_CapturesAuthKeysLine' 2>&1 | head -5`
Expected: compile FAIL.

- [ ] **Step 3: Implement the helpers**

Append to `internal/app/cmd/shared_cred.go` (add `gossh "golang.org/x/crypto/ssh"` to imports):

```go
// authorizedKeyMismatch reports whether the box's authorized_keys line is a
// parseable key whose fingerprint differs from the local public key. It is
// deliberately conservative: an empty or unparseable box line, an unreadable
// local .pub, or an empty localPubPath all report "no mismatch", so a box with
// odd content that started yesterday keeps starting today. The guard exists to
// turn an inexplicable "Permission denied (publickey)" inside VS Code or herdr
// into a named fix, not to add a new way to fail.
func authorizedKeyMismatch(boxLine, localPubPath string) bool {
	if localPubPath == "" || strings.TrimSpace(boxLine) == "" {
		return false
	}
	boxKey, _, _, _, err := gossh.ParseAuthorizedKey([]byte(boxLine))
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(localPubPath)
	if err != nil {
		return false
	}
	localKey, _, _, _, err := gossh.ParseAuthorizedKey(raw)
	if err != nil {
		return false
	}
	return gossh.FingerprintSHA256(boxKey) != gossh.FingerprintSHA256(localKey)
}

// sharedKeyMismatchError is the one message for a box whose authorized_keys
// disagrees with the shared key every laptop now holds.
func sharedKeyMismatchError(sandboxID string) error {
	return fmt.Errorf("the sandbox's authorized_keys does not match the shared key in SSM — someone rekeyed the box without publishing, or the box was restored from an AMI. Run: km vscode rekey %s", sandboxID)
}
```

- [ ] **Step 4: Wire into vscode**

In `vscode.go`, rename `parseVSCodeStatus` to `parseVSCodeStatusWithKey(out, sandboxID, localPubPath string) error`, and after the existing `switch` (i.e. once sshd + authorized_keys are known healthy), before `return nil`:

```go
	if authorizedKeyMismatch(strings.TrimSpace(sectionOf(out, "=== authkeys content ===")), localPubPath) {
		return sharedKeyMismatchError(sandboxID)
	}
```

(`sectionOf` lives in `herdr.go`, same package.) Add back a thin wrapper so `runVSCodeStatus` and existing tests keep compiling:

```go
func parseVSCodeStatus(out, sandboxID string) error { return parseVSCodeStatusWithKey(out, sandboxID, "") }
```

In `runVSCodeStart`, change `parseVSCodeStatus(out, sandboxID)` to `parseVSCodeStatusWithKey(out, sandboxID, privPath+".pub")`.

- [ ] **Step 5: Wire into herdr**

In `herdr.go`: add `AuthKeysLine string // first line of authorized_keys, "" when absent` to `herdrBoxState`; in `parseHerdrStatus` add `AuthKeysLine: strings.TrimSpace(sectionOf(out, "=== authkeys content ===")),`. In `runHerdrStart`, immediately after the existing health check (`if err := herdrHealthError(...)` — find with `grep -n herdrHealthError internal/app/cmd/herdr.go`):

```go
	if authorizedKeyMismatch(st.AuthKeysLine, privPath+".pub") {
		return sharedKeyMismatchError(sandboxID)
	}
```

(`st` is whatever the parsed-state variable is called there; read the function.)

- [ ] **Step 6: Run**

Run: `go build ./... && go test ./internal/app/cmd/ -run 'TestAuthorizedKeyMismatch|TestParseVSCodeStatus|TestParseHerdrStatus|TestVSCode|TestHerdr' 2>&1 | grep -E '^(--- FAIL|FAIL|ok)'`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
git add internal/app/cmd/shared_cred.go internal/app/cmd/shared_cred_test.go internal/app/cmd/vscode.go internal/app/cmd/herdr.go
git commit -m "feat(cmd): pre-flight names 'km vscode rekey' when the box key differs from the shared key"
```

---

### Task 7: `rekey` publishes box → local → SSM (vscode + desktop)

**Files:**
- Modify: `internal/app/cmd/vscode.go` (`runVSCodeRekey`, after Step 6 local commit)
- Modify: `internal/app/cmd/desktop.go` (`runDesktopRekey`, after its local commit ~line 525)
- Test: `internal/app/cmd/shared_cred_test.go`

- [ ] **Step 1: Write the failing test**

The rekey functions need a fetcher/ec2/ssm-send fake to reach the publish step; `vscode_test.go` already has a rekey test — find it (`grep -n "func TestVSCodeRekey\|func TestRunVSCodeRekey" internal/app/cmd/vscode_test.go`) and copy its fakes. Append to `shared_cred_test.go`:

```go
func TestVSCodeRekey_PublishesToSSMAfterLocalCommit(t *testing.T) {
	// Arrange exactly as the existing successful-rekey test does (same fakes,
	// same SSM readback stub that echoes the pushed key), then:
	f := &fakeAccessSSM{}
	saved := NewSharedCredStoreFunc
	NewSharedCredStoreFunc = func(context.Context, *config.Config) (*sharedCredStore, error) { return newTestStore(f), nil }
	defer func() { NewSharedCredStoreFunc = saved }()

	// ... call runVSCodeRekey(ctx, cfg, fetcher, ec2, ssmSend, "sbx", false, true) ...

	home, _ := os.UserHomeDir()
	local, _ := os.ReadFile(filepath.Join(home, ".km", "keys", "sbx"))
	if got := f.params["/km/access/sbx/ssh-key"]; got != string(local) {
		t.Errorf("SSM ssh-key != committed local key")
	}
}

func TestVSCodeRekey_PublishFailureNamesRerunAndKeepsLocal(t *testing.T) {
	// Same arrangement, but:
	f := &fakeAccessSSM{putErr: errors.New("denied")}
	// ... after runVSCodeRekey returns err:
	// assert err != nil && strings.Contains(err.Error(), "km vscode rekey sbx --yes")
	// assert ~/.km/keys/sbx exists and NO ~/.km/keys/sbx.new remains (local commit happened first)
}
```

Fill the elided parts from the existing rekey test's setup verbatim (copy, don't reference). The existing test's `HOME` handling (`t.Setenv("HOME", ...)`) is what makes the local path land in a temp dir.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/cmd/ -run 'TestVSCodeRekey_Publish' -v`
Expected: FAIL — SSM never written.

- [ ] **Step 3: Implement in vscode**

In `runVSCodeRekey`, after the two `os.Rename` calls (Step 6) and before "Step 7: Final output":

```go
	// Step 6b: publish to SSM so every other laptop picks the new key up on its
	// next start. Order is box → local → SSM on purpose: if this fails the
	// operator who ran rekey is working and everyone else is stale until it is
	// re-run; SSM-first would have handed everyone a key the box rejects had
	// the push failed.
	privBytes, err := os.ReadFile(privFinalPath)
	if err != nil {
		return fmt.Errorf("read committed key for publish: %w", err)
	}
	if err := publishSharedCredential(ctx, cfg, sshKeyKind, sandboxID, privBytes); err != nil {
		return fmt.Errorf("rekey applied on the sandbox and locally, but publishing to SSM failed: %w\nOther analysts will get a stale key until you re-run: km vscode rekey %s --yes", err, sandboxID)
	}
	fmt.Printf("✓ Published to SSM (%s)\n", kmaws.SSHKeyPath(cfg.GetResourcePrefix(), sandboxID))
```

(`cfg` is already a parameter of `runVSCodeRekey`. If `kmaws` is not imported in `vscode.go`, add `kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"` — check the alias other files use.)

- [ ] **Step 4: Implement in desktop**

In `runDesktopRekey`, after its local `os.Rename` commit and before the final output block:

```go
	// Step 5b: publish (box → local → SSM; see runVSCodeRekey for why this order).
	if err := publishSharedCredential(ctx, cfg, desktopCredKind, sandboxID, []byte(user+":"+newPass)); err != nil {
		return fmt.Errorf("rekey applied on the sandbox and locally, but publishing to SSM failed: %w\nOther analysts will get a stale password until you re-run: km desktop rekey %s --yes", err, sandboxID)
	}
	fmt.Printf("✓ Published to SSM (%s)\n", kmaws.DesktopCredPath(cfg.GetResourcePrefix(), sandboxID))
```

Check the local variable names (`user`, `newPass`) match what that function actually uses (`sed -n 450,535p internal/app/cmd/desktop.go`).

- [ ] **Step 5: Run**

Run: `go build ./... && go test ./internal/app/cmd/ -run 'Rekey' 2>&1 | grep -E '^(--- FAIL|FAIL|ok)'`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/app/cmd/vscode.go internal/app/cmd/desktop.go internal/app/cmd/shared_cred_test.go
git commit -m "feat(rekey): publish the new key/password to SSM after the box and local commit"
```

---

### Task 8: `create` publishes at generation time (4 sites)

**Files:**
- Modify: `internal/app/cmd/create.go` (`~1097` local SSH, `~1116` local desktop, `~3008` remote SSH, `~3027` remote desktop, `GenerateDesktopCredential` ~3792)
- Test: `internal/app/cmd/create_test.go` (4 existing `GenerateDesktopCredential` callers change shape; one new test)

**Interfaces:**
- Changes: `func GenerateDesktopCredential(homeDir, sandboxID string, network *compiler.NetworkConfig) (cred string, err error)` — `cred` is `"user:pass"` when generated on this machine, `""` on the `KM_DESKTOP_KASM_USER/PASS` env path (the create-handler subprocess, which must never publish).

- [ ] **Step 1: Write the failing test**

Append to `internal/app/cmd/create_test.go` (it is `package cmd_test` — note the `cmd.` qualifier used by its neighbours):

```go
func TestGenerateDesktopCredential_ReturnsCredOnlyWhenGenerated(t *testing.T) {
	home := t.TempDir()
	network := &compiler.NetworkConfig{}
	cred, err := cmd.GenerateDesktopCredential(home, "sbx-gen", network)
	if err != nil {
		t.Fatal(err)
	}
	if cred != network.DesktopKasmUser+":"+network.DesktopKasmPass || cred == ":" {
		t.Errorf("cred = %q, want user:pass of the generated credential", cred)
	}

	t.Setenv("KM_DESKTOP_KASM_USER", "kasm")
	t.Setenv("KM_DESKTOP_KASM_PASS", "frompipe")
	cred, err = cmd.GenerateDesktopCredential(home, "sbx-env", &compiler.NetworkConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cred != "" {
		t.Errorf("env path (create-handler subprocess) must return \"\" so nothing publishes; got %q", cred)
	}
}
```

Update the four existing callers: `if err := cmd.GenerateDesktopCredential(...)` → `if _, err := cmd.GenerateDesktopCredential(...)`.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/cmd/ -run TestGenerateDesktopCredential 2>&1 | head -5`
Expected: compile FAIL.

- [ ] **Step 3: Change `GenerateDesktopCredential`**

Signature → `(string, error)`. Env path: `return "", nil`. Local path: `return user + ":" + pass, nil`. Update the doc comment: "Returns the `user:pass` credential when it was generated here, so the caller can publish it to SSM; empty on the env path because the create-handler subprocess must not publish (it has no grant and the laptop already did)."

- [ ] **Step 4: Publish at the four sites**

Local SSH (`~1097`, inside the `else` branch after `GenerateAndWrite` and the `✓ VS Code keypair written` line):

```go
			if privBytes, rerr := os.ReadFile(privPath); rerr != nil {
				fmt.Fprintf(os.Stderr, "  [warn] could not read %s to publish: %v\n", privPath, rerr)
			} else if perr := publishSharedCredential(ctx, cfg, sshKeyKind, sandboxID, privBytes); perr != nil {
				fmt.Fprintf(os.Stderr, "  [warn] could not publish the VS Code key to SSM: %v — other analysts cannot connect until your next km vscode start %s (which republishes)\n", perr, sandboxID)
			}
```

Local desktop (`~1116`):

```go
		cred, err := GenerateDesktopCredential(homeDir, sandboxID, network)
		if err != nil {
			return fmt.Errorf("desktop credential: %w", err)   // keep whatever wrapping is there today
		}
		if cred != "" {
			if perr := publishSharedCredential(ctx, cfg, desktopCredKind, sandboxID, []byte(cred)); perr != nil {
				fmt.Fprintf(os.Stderr, "  [warn] could not publish the desktop credential to SSM: %v — other analysts cannot connect until your next km desktop start %s (which republishes)\n", perr, sandboxID)
			}
		}
```

Remote SSH (`~3008`) and remote desktop (`~3027`): identical two blocks (the remote function has `ctx` and `cfg` in scope — confirm with `sed -n 2960,3000p internal/app/cmd/create.go`).

- [ ] **Step 5: Run**

Run: `go build ./... && go test ./internal/app/cmd/ -run 'TestGenerateDesktopCredential|TestCreate' 2>&1 | grep -E '^(--- FAIL|FAIL|ok)'`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/app/cmd/create.go internal/app/cmd/create_test.go
git commit -m "feat(create): publish the VS Code key and desktop credential to SSM at generation"
```

---

### Task 9: `km doctor` — orphan `/{prefix}/access/*` parameters

**Files:**
- Create: `internal/app/cmd/doctor_access_params.go`
- Test: `internal/app/cmd/doctor_access_params_test.go`
- Modify: `internal/app/cmd/doctor.go` (register the check next to `checkStaleKMSKeys`, ~line 4325)

**Interfaces:**
- Produces: `func checkOrphanAccessParams(ctx context.Context, ssmRead SSMReadAPI, lister SandboxLister, resourcePrefix string) CheckResult`

- [ ] **Step 1: Write the failing test**

Create `internal/app/cmd/doctor_access_params_test.go`:

```go
package cmd

import (
	"context"
	"strings"
	"testing"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

type fakePathSSM struct{ names []string }

func (f fakePathSSM) GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	return &ssm.GetParameterOutput{}, nil
}
func (f fakePathSSM) GetParametersByPath(context.Context, *ssm.GetParametersByPathInput, ...func(*ssm.Options)) (*ssm.GetParametersByPathOutput, error) {
	var ps []ssmtypes.Parameter
	for _, n := range f.names {
		ps = append(ps, ssmtypes.Parameter{Name: awssdk.String(n)})
	}
	return &ssm.GetParametersByPathOutput{Parameters: ps}, nil
}

type fakeLister struct{ ids []string }

func (f fakeLister) ListSandboxes(context.Context, bool) ([]kmaws.SandboxRecord, error) {
	var out []kmaws.SandboxRecord
	for _, id := range f.ids {
		out = append(out, kmaws.SandboxRecord{SandboxID: id})
	}
	return out, nil
}

func TestCheckOrphanAccessParams(t *testing.T) {
	ssmc := fakePathSSM{names: []string{"/km/access/live/ssh-key", "/km/access/live/desktop-cred", "/km/access/gone/ssh-key"}}
	r := checkOrphanAccessParams(context.Background(), ssmc, fakeLister{ids: []string{"live"}}, "km")
	if r.Status != CheckWarn || !strings.Contains(r.Message, "gone") || strings.Contains(r.Message, "live") {
		t.Errorf("got %+v", r)
	}
	r = checkOrphanAccessParams(context.Background(), fakePathSSM{}, fakeLister{}, "km")
	if r.Status != CheckSkipped {
		t.Errorf("empty path must SKIP, got %+v", r)
	}
	r = checkOrphanAccessParams(context.Background(), ssmc, fakeLister{ids: []string{"live", "gone"}}, "km")
	if r.Status != CheckPass {
		t.Errorf("no orphans must PASS, got %+v", r)
	}
}
```

(Confirm the status constant names — `grep -n "CheckPass\|CheckWarn\|CheckSkipped" internal/app/cmd/doctor.go | head -3`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/cmd/ -run TestCheckOrphanAccessParams 2>&1 | head -3`
Expected: compile FAIL.

- [ ] **Step 3: Implement**

Create `internal/app/cmd/doctor_access_params.go`:

```go
package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

// checkOrphanAccessParams reports /{prefix}/access/<id>/* parameters whose
// sandbox has no row — a remote create that died after key generation, or a
// TTL expiry that ran before the ttl-handler's delete grant was deployed.
// WARN only; nothing here deletes. Silent SKIP when the namespace is empty so
// an install that never used the feature sees nothing.
func checkOrphanAccessParams(ctx context.Context, ssmRead SSMReadAPI, lister SandboxLister, resourcePrefix string) CheckResult {
	const name = "Shared access credentials"
	if ssmRead == nil || lister == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "SSM or sandbox lister not available"}
	}
	prefix := kmaws.AccessParamPrefix(resourcePrefix)
	ids := map[string]bool{}
	var next *string
	for {
		out, err := ssmRead.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
			Path:      awssdk.String(prefix),
			Recursive: awssdk.Bool(true),
			NextToken: next,
		})
		if err != nil {
			return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list %s: %v", prefix, err)}
		}
		for _, p := range out.Parameters {
			rel := strings.TrimPrefix(awssdk.ToString(p.Name), prefix)
			if id, _, ok := strings.Cut(rel, "/"); ok && id != "" {
				ids[id] = true
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		next = out.NextToken
	}
	if len(ids) == 0 {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "none published"}
	}
	records, err := lister.ListSandboxes(ctx, false)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list sandboxes: %v", err)}
	}
	known := map[string]bool{}
	for _, r := range records {
		known[r.SandboxID] = true
	}
	var orphans []string
	for id := range ids {
		if !known[id] {
			orphans = append(orphans, id)
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		return CheckResult{
			Name:        name,
			Status:      CheckWarn,
			Message:     fmt.Sprintf("%d sandbox(es) have shared credentials in SSM but no record: %s", len(orphans), strings.Join(orphans, ", ")),
			Remediation: fmt.Sprintf("aws ssm delete-parameters --names %s<id>/ssh-key %s<id>/desktop-cred", prefix, prefix),
			Details:     orphans,
		}
	}
	return CheckResult{Name: name, Status: CheckPass, Message: fmt.Sprintf("%d sandbox(es) published, all known", len(ids))}
}
```

(If `CheckResult.Details` is not `[]string`, drop that field.)

- [ ] **Step 4: Register**

In `doctor.go`, right after the stale-KMS-keys `checks = append(...)` block:

```go
	// Orphan shared access credentials (SSH key / desktop password in SSM
	// for a sandbox that no longer has a row).
	accessSSM := deps.SSMReadClient
	accessLister := deps.Lister
	accessPrefix := cfg.GetResourcePrefix()
	checks = append(checks, func(ctx context.Context) CheckResult {
		return checkOrphanAccessParams(ctx, accessSSM, accessLister, accessPrefix)
	})
```

- [ ] **Step 5: Run**

Run: `go build ./... && go test ./internal/app/cmd/ -run 'TestCheckOrphanAccessParams|TestDoctor' 2>&1 | grep -E '^(--- FAIL|FAIL|ok)'`
Expected: `ok`. If a doctor test counts checks by number, bump it.

- [ ] **Step 6: Commit**

```bash
git add internal/app/cmd/doctor_access_params.go internal/app/cmd/doctor_access_params_test.go internal/app/cmd/doctor.go
git commit -m "feat(doctor): warn on orphan shared access credentials in SSM"
```

---

### Task 10: Docs, skills, CLAUDE.md, plugin bump, spec correction

**Files:**
- Modify: `docs/vscode.md`, `docs/desktop.md`, `docs/herdr-remote-attach.md`, `docs/k8s-reverse-tunnel.md`
- Modify: `skills/vscode/SKILL.md`, `skills/desktop/SKILL.md`
- Modify: `CLAUDE.md` (new block at the top of the phase history + one "Where to look" row)
- Modify: `.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json` (0.4.16 → 0.4.17)
- Modify: `docs/superpowers/specs/2026-09-20-shared-sandbox-access-credentials-design.md` §4.3

- [ ] **Step 1: Spec correction**

In §4.3 replace "The same script and parser serve `km herdr` and `km tunnel` via the shared pre-flight, so all three get the guard." with: "`km herdr` runs the same check through its own pre-flight parser. `km tunnel` makes no SSM pre-flight (it SSHes straight through the forward), so it gets the sync but not the guard; a stale key there fails inside ssh with `Permission denied (publickey)` and the fix is the same `km vscode rekey`."

- [ ] **Step 2: `docs/vscode.md`** — add a section "Sharing a sandbox between analysts" after the rekey section, covering: the SSM namespace, what `start` prints in each of the four visible cases, "SSM is the truth / local is a cache", rekey publishes last-writer-wins, the mismatch message and what it means, `km doctor`'s orphan check, the deploy note (`make build` for laptops; ttl-handler grant needs `km init --dry-run=false`). Replace the existing "copy the `~/.km/keys/<id>*` files over" guidance with "run `km vscode start` — the key is pulled from SSM". ~40 lines.

- [ ] **Step 3: `docs/desktop.md`** — same shape, shorter, plus the honest note: no mismatch guard for the password (hashed on the box); a stale one is a browser login failure; `km desktop rekey` fixes it.

- [ ] **Step 4: `docs/herdr-remote-attach.md`, `docs/k8s-reverse-tunnel.md`** — one paragraph each pointing at the vscode section; tunnel notes "sync but no guard".

- [ ] **Step 5: Skills** — in `skills/vscode/SKILL.md` and `skills/desktop/SKILL.md` replace any "copy the key files over" instruction with "another analyst runs `start`; the credential is pulled from SSM", and mention `rekey` republishes.

- [ ] **Step 6: CLAUDE.md** — add a block above the `km list` block:

```
**Shared sandbox access credentials (2026-09-20):**
- **`km vscode|desktop|herdr|tunnel start` from a laptop that never ran `km create` now just works.** The per-sandbox ed25519 key and KasmVNC `user:pass` live in SSM SecureString at `/{prefix}/access/{id}/{ssh-key,desktop-cred}` (platform KMS key) and `~/.km/keys`/`~/.km/desktop` are a CACHE reconciled by `syncSharedCredential` (`internal/app/cmd/shared_cred.go`) before every pre-flight: pull if absent, refresh if different, publish if SSM is empty (self-backfills every pre-existing sandbox), name `rekey` if neither exists, WARN-and-continue on a read error. One shared credential, no per-analyst identity — decided, don't re-litigate: anyone who can run `start` already holds `km shell --root`.
- **NOT under `/{prefix}/sandbox/{id}/`** — the instance role reads that path. `/{prefix}/access/` is inside the operator policy's existing `parameter/{prefix}/*` grant; laptops need no IAM change.
- **`rekey` publishes box → local → SSM, last writer wins.** SSM-first would hand everyone a key the box rejects if the push failed. A failed publish leaves you working, others stale, and says so. The vscode/herdr pre-flight compares the box's `authorized_keys` to the synced key and names `km vscode rekey` on mismatch; the desktop password is hashed on the box so it has no guard.
- **Test seam:** `NewSharedCredStoreFunc` returns `(nil, nil)` in `TestMain`, and a nil store is byte-identical legacy behaviour.
- **Deploy = `make build`.** Plus `make build-lambdas` + `km init --dry-run=false` for the ttl-handler's two new `ssm:DeleteParameter` ARNs (`TestTTLHandlerModule_EveryCleanupSSMParamHasADeleteGrant` pairs them with `CleanupSandboxIdentity`). No userdata, schema, sidecar, or recreate. `km doctor` WARNs on orphan params. See `docs/vscode.md` § Sharing a sandbox between analysts.
```

And a "Where to look" row: `| Sharing a sandbox's VS Code / desktop / herdr access between analysts — SSM-held key + password, the sync table, rekey publish order, the mismatch guard | docs/vscode.md § Sharing a sandbox between analysts |`.

- [ ] **Step 7: Plugin bump** — `0.4.16` → `0.4.17` in both JSON files (`grep -n '"version"' .claude-plugin/*.json`).

- [ ] **Step 8: Commit**

```bash
git add docs/vscode.md docs/desktop.md docs/herdr-remote-attach.md docs/k8s-reverse-tunnel.md skills/vscode/SKILL.md skills/desktop/SKILL.md CLAUDE.md .claude-plugin/plugin.json .claude-plugin/marketplace.json docs/superpowers/specs/2026-09-20-shared-sandbox-access-credentials-design.md
git commit -m "docs: shared sandbox access credentials; plugin 0.4.17"
```

---

### Task 11: `km version` alias for `km --version`

**Files:**
- Create: `internal/app/cmd/version.go`
- Test: `internal/app/cmd/version_test.go`
- Modify: `internal/app/cmd/root.go` (register)

- [ ] **Step 1: Write the failing test**

```go
package cmd

import (
	"bytes"
	"testing"

	"github.com/whereiskurt/klanker-maker/internal/app/config"
)

func TestVersionSubcommand_MatchesVersionFlag(t *testing.T) {
	run := func(args ...string) string {
		cfg := &config.Config{Version: "9.9.9 (abc123)"}
		root := NewRootCmd(cfg)
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out.String()
	}
	flag := run("--version")
	sub := run("version")
	if flag == "" || flag != sub {
		t.Errorf("km version = %q, km --version = %q; must be identical", sub, flag)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/cmd/ -run TestVersionSubcommand -v`
Expected: FAIL — `unknown command "version"`.

- [ ] **Step 3: Implement**

Create `internal/app/cmd/version.go`:

```go
package cmd

import "github.com/spf13/cobra"

// NewVersionCmd returns `km version`, an alias for `km --version`. It renders
// cobra's own version template against the root command so the two can never
// print different things.
func NewVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "version",
		Short:        "Print the km version (same as km --version)",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(c *cobra.Command, _ []string) error {
			root := c.Root()
			tmpl := root.VersionTemplate()
			t, err := template.New("version").Parse(tmpl)
			if err != nil {
				return err
			}
			return t.Execute(root.OutOrStdout(), root)
		},
	}
}
```

Add `"text/template"` to the imports. In `root.go`, next to the other `root.AddCommand(...)` lines: `root.AddCommand(NewVersionCmd())`. Check cobra exposes `VersionTemplate()` in the vendored version (`grep -n "func (c \*Command) VersionTemplate" $(go env GOMODCACHE)/github.com/spf13/cobra@*/command.go`); if it does not, use the literal default template `{{with .Name}}{{printf "%s " .}}{{end}}{{printf "version %s" .Version}}\n` and add a comment saying it mirrors cobra's default.

- [ ] **Step 4: Run**

Run: `go test ./internal/app/cmd/ -run TestVersionSubcommand -v`
Expected: PASS. Also `go run ./cmd/km version` and `go run ./cmd/km --version` print the same line.

- [ ] **Step 5: Add to CLAUDE.md CLI list** — one line after `km info`: ``- `km version` — print the km version (alias for `km --version`)``.

- [ ] **Step 6: Commit**

```bash
git add internal/app/cmd/version.go internal/app/cmd/version_test.go internal/app/cmd/root.go CLAUDE.md
git commit -m "feat(cli): km version as an alias for km --version"
```

---

### Task 12: `km list --wide` — `expired` overflowed the 6-wide TTL column

**Files:**
- Modify: `internal/app/cmd/list.go` (~line 400, the `ttl :=` block in `printSandboxTable`)
- Test: `internal/app/cmd/list_test.go` (find the existing `printSandboxTable` tests: `grep -n "printSandboxTable(" internal/app/cmd/list_test.go | head -3`)

- [ ] **Step 1: Write the failing test**

Append to `internal/app/cmd/list_test.go` (match its package name and how it builds a cobra `cmd` with a captured writer — copy from a neighbouring test):

```go
func TestPrintSandboxTable_WideExpiredTTLStaysInColumn(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	recs := []kmaws.SandboxRecord{
		{SandboxID: "learn-aaaaaaaa", Alias: "a", Profile: "learn", Region: "us-east-1", Status: "stopped", TTLRemaining: "expired", TTLExpiry: &past},
		{SandboxID: "learn-bbbbbbbb", Alias: "b", Profile: "learn", Region: "us-east-1", Status: "stopped", TTLRemaining: "-"},
	}
	var out bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&out)
	if err := printSandboxTable(c, recs, true, nil, nil); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
	if strings.Contains(out.String(), "expired") {
		t.Error("wide table must render exp., not expired (7 chars overflows the 6-wide TTL column)")
	}
	// IDLE must start at the same column on the header, the expired row, and the normal row.
	col := func(s, marker string) int { return strings.Index(stripANSI(s), marker) }
	hdr := col(lines[0], "IDLE")
	if hdr < 0 {
		t.Fatalf("no IDLE header in %q", lines[0])
	}
	for _, row := range lines[1:] {
		// the cell after TTL is IDLE; for both rows it renders "-", so locate the
		// TTL cell start instead and assert the row's UP cell aligns with the header's.
		if got, want := col(row, "-"), -1; got == want {
			t.Fatalf("row has no cells: %q", row)
		}
	}
	// Simplest robust assertion: both rows have identical visual length up to the UP column.
	up := col(lines[0], "UP")
	for _, row := range lines[1:] {
		if len(stripANSI(row)) < up {
			t.Errorf("row shorter than header UP column: %q", row)
		}
	}
}
```

If a `stripANSI` helper does not already exist in the test package (`grep -n "func stripANSI" internal/app/cmd/*_test.go`), add:

```go
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }
```

Prefer a stronger assertion if the existing list tests already have an alignment helper — reuse it. The essential checks: no `expired` in wide output, and the row with `exp.` has its `UP` cell at the header's `UP` column (`col(row, "-")` after the `exp.` cell — walk it: find `exp.`, then the next non-space token start equals `hdr`).

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/app/cmd/ -run TestPrintSandboxTable_WideExpiredTTLStaysInColumn -v`
Expected: FAIL — output contains `expired`.

- [ ] **Step 3: Implement**

In `printSandboxTable`, the `ttl` block becomes:

```go
		ttl := r.TTLRemaining
		switch {
		case ttl == "":
			ttl = "-"
		case ttl == "expired":
			// computeTTLRemaining's "expired" is 7 chars in a %-6s column and
			// pushed IDLE/UP/💬 right on that one row. Display-only: --json
			// keeps "expired", and the narrow SHUTDOWN column (11 wide) is
			// untouched.
			ttl = "exp."
		case r.TTLExpiry != nil && time.Until(*r.TTLExpiry) >= 3*365*24*time.Hour:
			ttl = "∞" // same rung as compactDuration; --json keeps the numeric string
		}
```

- [ ] **Step 4: Run**

Run: `go test ./internal/app/cmd/ -run 'TestPrintSandboxTable|TestList' 2>&1 | grep -E '^(--- FAIL|FAIL|ok)'`
Expected: `ok`. Then eyeball: `make build && ./km list --wide` on the real install — the `exp.` row's IDLE/UP/💬 line up with the header.

- [ ] **Step 5: Commit**

```bash
git add internal/app/cmd/list.go internal/app/cmd/list_test.go
git commit -m "fix(list): render expired TTL as exp. so --wide columns stay aligned"
```

---

### Task 13: Full verification, live UAT, PR

- [ ] **Step 1: Full build + tests**

Run: `go build ./... && go vet ./... && go test ./... 2>&1 | grep -E '^(--- FAIL|FAIL|ok)' | grep -v '^ok'`
Expected: only the 5 known `internal/app/cmd` failures (`TestBootstrapSCP*`, `TestCluster*`).

- [ ] **Step 2: `make build`** and confirm `./km --version` shows the worktree's HEAD commit.

- [ ] **Step 3: Live UAT** (spec §9, against a running sandbox with vscode + desktop enabled; `AWS_PROFILE=klanker-application`):
1. `./km vscode start <id>` on this laptop → `✓ Published existing key to SSM`; Ctrl-C.
2. `HOME=/tmp/analyst2 ./km vscode start <id>` → `✓ Pulled shared key from SSM`; pre-flight passes; Ctrl-C.
3. `HOME=/tmp/analyst2 ./km vscode rekey <id> --yes` → `✓ Published to SSM`; then `./km vscode start <id>` → `✓ Local key refreshed from SSM (rekeyed elsewhere)`.
4. Same three with `km desktop`.
5. `./km shell --root <id>`, append a junk key as line 1 of `/home/sandbox/.ssh/authorized_keys`; `./km vscode start <id>` → the mismatch message. Restore with `./km vscode rekey <id> --yes`.
6. `aws ssm get-parameters-by-path --path /km/access/<id>/ --recursive --query 'Parameters[].Name'` shows both; after `./km destroy <id> --remote --yes`, empty.
Record results in the PR body.

- [ ] **Step 4: Push and open the PR**

```bash
git push -u origin worktree-feat+shared-access-creds
gh pr create --title "feat: shared sandbox access credentials in SSM; km version; list --wide alignment" --body-file <(cat <<'EOF'
## Summary
- `km vscode|desktop|herdr|tunnel start` from any operator laptop now works: the per-sandbox SSH key and KasmVNC password live in SSM at `/{prefix}/access/{id}/`, and `~/.km/{keys,desktop}` are a cache reconciled on every start (pull / refresh / publish / name-rekey / fail-open).
- `rekey` publishes box → local → SSM (last writer wins); `create` publishes at generation; destroy + ttl-handler clean up (pairing guard test); `km doctor` warns on orphans; the vscode/herdr pre-flight names `km vscode rekey` when the box key differs from the shared one.
- `km version` = `km --version`.
- `km list --wide`: `expired` → `exp.` so the TTL column no longer shifts IDLE/UP/💬.

Spec: `docs/superpowers/specs/2026-09-20-shared-sandbox-access-credentials-design.md`

## Deploy
`make build` for laptops. `make build-lambdas` + `km init --dry-run=false` for the ttl-handler's two new `ssm:DeleteParameter` ARNs. No userdata / schema / sidecar / recreate.

## Live UAT
(fill from Step 3)

🤖 Generated with [Claude Code](https://claude.com/claude-code)

https://claude.ai/code/session_01HqsRd1YjR39MayZUWAZKV7
EOF
)
```
