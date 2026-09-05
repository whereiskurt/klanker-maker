# Phase 133 Wave 2 — The IMDS fence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `spec.secrets.fenceIMDS: true` cut uid `sandbox` off the instance
metadata service while every km helper that reads AWS as that uid keeps working,
on credentials that are definitionally the instance role minus the two grants
that open the secrets bundle.

**Architecture:** An iptables `filter`-table REJECT blocks uid `sandbox` from
`169.254.169.254`. The helpers keep working because the sandbox user's
`~/.aws/config` names a `credential_process` (`km-creds`) that asks the already-
running root broker for credentials; the broker self-assumes the instance role
with an inline session policy carrying two explicit Denies. Selftest assertion 6
proves the fence with a negative control — the narrowed credentials must FAIL to
decrypt the bundle.

**Tech Stack:** Go 1.x (`CGO_ENABLED=0` cross-compile), aws-sdk-go-v2 STS,
systemd, iptables, Terraform/Terragrunt.

**Spec:** `docs/superpowers/specs/2026-09-04-brokered-secret-unsealing-design.md`
(§4.4 the fence, §7.1 assertion 6, §9 deploy surface, §10.3 the follow-on flip).
Wave 1's plan — `docs/superpowers/plans/2026-09-04-brokered-secret-unsealing-wave1.md` —
is the shipped baseline this builds on.

---

## Global Constraints

- **Dormant by default.** `fenceIMDS` defaults `false`. A profile that does not
  set it must render byte-identical userdata and byte-identical Terraform to
  Wave 1. No `apiVersion` bump.
- **New immutable module dir `infra/modules/ec2spot/v1.7.0`.** Never edit
  `v1.6.0` in place. Bump `locals.substrate_module_versions` in
  `infra/templates/sandbox/terragrunt.hcl` and the `ec2spotModuleDir` constant in
  `pkg/compiler/ec2spot_timeout_test.go` in the same commit.
- **Secret values are `[]byte`, never `string`,** anywhere the broker controls
  them (Wave 1 rule, unchanged). STS credentials are the deliberate exception:
  they are short-lived, refreshable, and must be JSON-marshalled anyway.
- **Deploy surface:** `make build` + `make build-lambdas` + `km init --dry-run=false`.
  NOT `--sidecars` — the IAM change needs a full terragrunt apply and the userdata
  rides in the create-handler zip. Do not split the deploy: `km-creds` is uploaded
  by `buildAndUploadSidecars` while the unit that invokes it is rendered by the
  create-handler.
- **Broker internals are Linux-only** (unix sockets, `SO_PEERCRED`). `go test ./...`
  on macOS does not cover them. Use the cross-compiled-binary-under-Docker
  technique ([[project_linux_only_test_via_crosscompiled_binary]]).
- **Test new profile fields through YAML → `ValidateSchema`,** never by setting the
  struct directly — a struct-level test greens while the field is absent from the
  JSON schema and therefore dead ([[project_struct_level_tests_bypass_schema]]).

---

## Three corrections to the spec, settled before planning

These are recorded here because an executor reading §4.4 alone would build
something that does not work.

### C1. A role cannot name its own ARN in its own trust policy

§4.4 says the Terraform cycle is "broken by constructing the ARN string from
`data.aws_caller_identity` plus the deterministic role name rather than
referencing the resource." That breaks the *Terraform* cycle but not the *IAM*
one: IAM resolves a principal ARN to a unique principal ID when the policy is
saved, and a role that does not exist yet cannot be resolved. Verified live
against the application account on 2026-09-04:

```
$ aws iam create-role --role-name km-spike-selfassume-133 \
    --assume-role-policy-document '{... "Principal":{"AWS":"arn:aws:iam::<acct>:role/km-spike-selfassume-133"} ...}'
An error occurred (MalformedPolicyDocument) when calling the CreateRole operation:
Invalid principal in policy: "AWS":"arn:aws:iam::<acct>:role/km-spike-selfassume-133"
```

**The replacement, also verified live end to end:** the account-root principal
narrowed by an `aws:PrincipalArn` condition naming the role, plus an
identity-based `sts:AssumeRole` grant on its own ARN. Both halves are required —
root-principal delegation means the resource policy alone does not authorize.

`aws:PrincipalArn` is a global condition key AWS populates on **every** request,
so this is not the unsatisfiable-condition trap recorded in
[[project_cross_account_gpu_launch]] (`aws:RequestTag` on `ec2:RunInstances`,
which AWS never populates for the `instance/*` resource). The spike confirmed
enforcement rather than a simulator verdict:

```
$ aws sts assume-role --role-arn .../km-spike-selfassume-133 --role-session-name narrowed \
    --policy file://sesspol.json          # session policy with an explicit Deny
$ aws s3api list-buckets                  # the denied action, on narrowed creds
An error occurred (AccessDenied) ... is not authorized to perform: s3:ListAllMyBuckets
with an explicit deny in a session policy
$ aws kms list-aliases                    # the allowed control, same creds
(succeeds)
```

The security property §4.4 argues for is preserved exactly: the narrowed
credentials are still *definitionally* the instance role minus two Denies, so the
two can never drift. **Update the spec's §4.4 as part of Task 7.**

### C2. The fence must be a systemd unit, not a bare userdata command

There is **no iptables persistence anywhere in this repo** — `grep -n
'iptables-save\|iptables-restore\|netfilter-persistent' pkg/compiler/userdata.go`
returns nothing. Userdata does not re-run on stop/start (cloud-init is
per-instance), but `km-secretsd.service` and every shim *do* come back, because
they are enabled systemd units. A userdata-only `iptables -A` would therefore
leave a resumed box with a live broker, live shims, a passing-looking boot, and
**no fence** — uid `sandbox` back on IMDS with nothing saying so.

So the fence ships as `km-imds-fence.service` (`Type=oneshot`,
`RemainAfterExit=yes`, `WantedBy=multi-user.target`), the same resume-coverage
reasoning that produced `km-secrets-check.service` in Wave 1.
`km-secrets-check.service` gains `After=km-imds-fence.service` so assertion 6
never races the rule it is asserting.

(Pre-existing and deliberately **out of scope**: the Phase-6 nat-table DNAT rules
under `enforcement: proxy` have the same evaporate-on-resume property. Note it,
do not fix it here.)

### C3. `km-creds` cannot reach IMDS itself — which is why the broker has the RPC

`km-creds` runs as uid `sandbox` (the SDK spawns it), so it is on the wrong side
of the fence and cannot call `AssumeRole` with instance-role credentials. It is a
thin client of the broker's `Credentials()` RPC (§4.1), and the broker — root,
unfenced — does the assume. This is what §4.1 already specifies; it is restated
because it is the load-bearing reason `km-creds` is not simply an
`aws sts assume-role` wrapper.

---

## File Structure

| File | Responsibility |
|---|---|
| `pkg/profile/types.go` (mod) | `SecretsSpec.FenceIMDS *bool` |
| `pkg/profile/schemas/sandbox_profile.schema.json` (mod) | `fenceIMDS` property |
| `pkg/secrets/protocol.go` (mod) | `Op` discriminator, `Credentials` struct, `CredsSocketOp` constants |
| `pkg/secrets/fence.go` (new) | `SessionPolicy()` — the one place the two Denies are written |
| `pkg/secrets/fence_test.go` (new) | Deny shape, alias/bucket interpolation |
| `cmd/km-secretsd/credentials.go` (new) | Role-ARN derivation, `AssumeRole`, expiry-aware cache |
| `cmd/km-secretsd/credentials_test.go` (new) | Fake STS seam |
| `cmd/km-secretsd/server.go` (mod) | Dispatch on `Op`; refuse credentials when fence off |
| `cmd/km-secretsd/selftest.go` (mod) | Assertion 6, three clauses |
| `cmd/km-creds/main.go` (new) | `credential_process` client; prints the broker's JSON verbatim |
| `cmd/km-creds/main_test.go` (new) | Round-trip against a fake broker socket |
| `internal/app/cmd/init.go` (mod) | `sidecarBuilds()` entry for `km-creds` |
| `pkg/compiler/service_hcl.go` (mod) | `FenceIMDS` param → `fence_imds` module input |
| `pkg/compiler/userdata.go` (mod) | §5.6 fence unit + `~/.aws/config`; km-creds install; broker env |
| `infra/modules/ec2spot/v1.7.0/**` (new) | Copy of v1.6.0 + gated trust statement + self-assume grant |
| `infra/templates/sandbox/terragrunt.hcl` (mod) | Pin bump to v1.7.0 |
| `docs/brokered-secrets.md` (mod) | Fence runbook |

---

### Task 1: `spec.secrets.fenceIMDS` — schema, type, compiler input

**Files:**
- Modify: `pkg/profile/types.go` (`SecretsSpec`)
- Modify: `pkg/profile/schemas/sandbox_profile.schema.json` (the `secrets` block)
- Modify: `pkg/compiler/service_hcl.go` (params struct, template, populate)
- Test: `pkg/compiler/service_hcl_fence_test.go` (new)

**Interfaces:**
- Consumes: nothing.
- Produces: `profile.SecretsSpec.FenceIMDS *bool`; helper
  `profile.IsFenceIMDSEnabled(s *SecretsSpec) bool` (nil-safe, default `false`);
  service.hcl `module_inputs.fence_imds` (emitted only when true).

`*bool` rather than `bool` deliberately: §10.3 flips this default in a follow-on
phase, and a tri-state distinguishes "operator said false" from "operator said
nothing" at that point. Same disposition as
`notification.slack.inbound.mentionOnly`.

- [ ] **Step 1: Write the failing tests**

```go
// pkg/compiler/service_hcl_fence_test.go
package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// A profile that does not mention fenceIMDS must emit NO fence_imds line, so
// every pre-Wave-2 sandbox renders byte-identical Terraform.
func TestServiceHCL_NoFenceInputWhenUnset(t *testing.T) {
	got := renderServiceHCLForTest(t, nil)
	if strings.Contains(got, "fence_imds") {
		t.Errorf("service.hcl carries fence_imds for a profile that never set it;\n%s", got)
	}
}

func TestServiceHCL_FenceInputWhenTrue(t *testing.T) {
	on := true
	got := renderServiceHCLForTest(t, &on)
	if !strings.Contains(got, "fence_imds = true") {
		t.Errorf("service.hcl missing `fence_imds = true`;\n%s", got)
	}
}

// Explicit false is dormant too: the module default is false, so emitting the
// input would be noise and would break byte-identity for a profile that only
// documented its intent.
func TestServiceHCL_NoFenceInputWhenExplicitFalse(t *testing.T) {
	off := false
	got := renderServiceHCLForTest(t, &off)
	if strings.Contains(got, "fence_imds") {
		t.Errorf("explicit false must render no fence_imds input;\n%s", got)
	}
}

func TestIsFenceIMDSEnabled(t *testing.T) {
	on, off := true, false
	cases := []struct {
		name string
		in   *profile.SecretsSpec
		want bool
	}{
		{"nil spec", nil, false},
		{"no field", &profile.SecretsSpec{}, false},
		{"explicit false", &profile.SecretsSpec{FenceIMDS: &off}, false},
		{"explicit true", &profile.SecretsSpec{FenceIMDS: &on}, true},
	}
	for _, c := range cases {
		if got := profile.IsFenceIMDSEnabled(c.in); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
```

Write `renderServiceHCLForTest(t *testing.T, fence *bool) string` as a helper in
the same file: load the smallest existing fixture profile the package already
uses for service.hcl tests (copy the construction from
`pkg/compiler/service_hcl_secret_paths_test.go`, which does exactly this for
`secret_paths`), set `p.Spec.Secrets = &profile.SecretsSpec{SopsFile: "x.enc.yaml", FenceIMDS: fence}`
when `fence != nil` (and `SopsFile` only when fence is nil, so the dormant case
still has a bundle), compile, and return `got.ServiceHCL`.

- [ ] **Step 2: Write the failing schema test**

```go
// pkg/profile/secrets_fence_schema_test.go
package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

// Through YAML and ValidateSchema, never by setting the struct: a struct-level
// test greens while the field is absent from the JSON schema and therefore dead
// on every real profile. See the spec.otp precedent.
func TestSchema_AcceptsFenceIMDS(t *testing.T) {
	y := minimalProfileYAML(t) + `
  secrets:
    sopsFile: ./secrets/x.enc.yaml
    fenceIMDS: true
`
	if err := profile.ValidateSchema([]byte(y)); err != nil {
		t.Fatalf("schema rejected fenceIMDS: %v", err)
	}
}

func TestSchema_RejectsNonBoolFenceIMDS(t *testing.T) {
	y := minimalProfileYAML(t) + `
  secrets:
    sopsFile: ./secrets/x.enc.yaml
    fenceIMDS: "yes"
`
	err := profile.ValidateSchema([]byte(y))
	if err == nil {
		t.Fatal("schema accepted a string fenceIMDS")
	}
	if !strings.Contains(err.Error(), "fenceIMDS") {
		t.Errorf("error does not name the offending field: %v", err)
	}
}

func TestSchema_ParsesFenceIMDSIntoTheStruct(t *testing.T) {
	y := minimalProfileYAML(t) + `
  secrets:
    sopsFile: ./secrets/x.enc.yaml
    fenceIMDS: true
`
	p, err := profile.Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	if !profile.IsFenceIMDSEnabled(p.Spec.Secrets) {
		t.Fatal("fenceIMDS: true did not reach the struct")
	}
}
```

`minimalProfileYAML(t)` — reuse the package's existing helper if one exists
(`grep -rn "func minimalProfileYAML\|func minimalProfile" pkg/profile/*_test.go`);
otherwise write it returning the smallest `apiVersion: klankermaker.ai/v1alpha2`
document that already validates, ending just before `spec:`'s child keys so the
test can append a block. Assert on `err.Error()` naming the field — the schema
path is what makes a rejection diagnosable.

- [ ] **Step 3: Run both test files to verify they fail**

```bash
go test ./pkg/compiler/ -run 'Fence' -v 2>&1 | tail -20
go test ./pkg/profile/ -run 'FenceIMDS' -v 2>&1 | tail -20
```

Expected: FAIL — `FenceIMDS` undefined, `IsFenceIMDSEnabled` undefined,
`fenceIMDS` rejected by `additionalProperties: false`.

- [ ] **Step 4: Add the field to the type**

In `pkg/profile/types.go`, inside `SecretsSpec`, after `Grants`:

```go
	// FenceIMDS blocks uid sandbox from 169.254.169.254 and re-homes every
	// helper that reads AWS as that uid onto credential_process credentials
	// the broker mints (Phase 133 Wave 2, design doc section 4.4).
	//
	// Opt-in. A pointer rather than a plain bool because the default flips in a
	// follow-on phase (design doc section 10.3), and at that point "the
	// operator said false" and "the operator said nothing" must be
	// distinguishable. Nil and false are identical today.
	//
	// Only meaningful alongside SopsFile: with no bundle there is no broker to
	// mint credentials from, so the fence would strand every helper. km
	// validate WARNs on that combination.
	FenceIMDS *bool `yaml:"fenceIMDS,omitempty" json:"fenceIMDS,omitempty"`
```

And a nil-safe accessor beside the other `Is*Enabled` helpers in the same file:

```go
// IsFenceIMDSEnabled reports whether the IMDS fence is on. Nil-safe: both a nil
// SecretsSpec and an unset field mean off.
func IsFenceIMDSEnabled(s *SecretsSpec) bool {
	return s != nil && s.FenceIMDS != nil && *s.FenceIMDS
}
```

- [ ] **Step 5: Add the schema property**

In `pkg/profile/schemas/sandbox_profile.schema.json`, in the `secrets` block's
`properties`, after `sopsFile` and before `grants`:

```json
            "fenceIMDS": {
              "type": "boolean",
              "description": "Phase 133 Wave 2: block uid sandbox from 169.254.169.254 and re-home helper AWS access onto broker-minted credentials narrowed by two explicit Denies. Opt-in; default false. Only meaningful alongside sopsFile."
            },
```

- [ ] **Step 6: Emit the module input**

In `pkg/compiler/service_hcl.go`, add to the params struct beside `SecretPaths`:

```go
	// FenceIMDS gates the ec2spot v1.7.0 self-assume trust statement and the
	// sts:AssumeRole grant. Emitted only when true so a dormant profile renders
	// byte-identical Terraform.
	FenceIMDS bool
```

In the template, immediately after the `secret_paths` block:

```
{{- if .FenceIMDS }}

    # spec.secrets.fenceIMDS. Adds the self-assume trust statement and the
    # sts:AssumeRole grant the broker needs to mint narrowed credentials
    # (ec2spot v1.7.0). Absent means the module's own default, false.
    fence_imds = true
{{- end }}
```

And where the params are populated (beside `SecretPaths: secretPaths`):

```go
		FenceIMDS: profile.IsFenceIMDSEnabled(p.Spec.Secrets),
```

- [ ] **Step 7: Run the tests to verify they pass**

```bash
go test ./pkg/compiler/ -run 'Fence' -v 2>&1 | tail -20
go test ./pkg/profile/ -run 'FenceIMDS' -v 2>&1 | tail -20
```

Expected: PASS, all seven.

- [ ] **Step 8: Run the full package suites for regressions**

```bash
go test ./pkg/profile/ ./pkg/compiler/ 2>&1 | tail -20; echo "exit=$?"
```

Check the command's own exit code, not a pipe's
([[feedback_check_go_test_exit_not_pipe]]). Expected: `ok` for both.

- [ ] **Step 9: Commit**

```bash
git add pkg/profile/types.go pkg/profile/schemas/sandbox_profile.schema.json \
        pkg/profile/secrets_fence_schema_test.go \
        pkg/compiler/service_hcl.go pkg/compiler/service_hcl_fence_test.go
git commit -m "feat(profile): spec.secrets.fenceIMDS — opt-in IMDS fence

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01LJgXTjthChRMjYCLp7RD8Z"
```

---

### Task 2: The session policy — one place the two Denies are written

**Files:**
- Create: `pkg/secrets/fence.go`
- Test: `pkg/secrets/fence_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `secrets.SessionPolicy(resourcePrefix, artifactsBucket, sandboxID string) (string, error)`
  returning a compact JSON IAM session policy document.

This lives in `pkg/secrets` (pure, no AWS, no syscalls — the package's stated
contract) so the broker and the tests read the same bytes, and so the two Denies
have exactly one definition.

- [ ] **Step 1: Write the failing test**

```go
// pkg/secrets/fence_test.go
package secrets_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

type policyDoc struct {
	Version   string `json:"Version"`
	Statement []struct {
		Sid       string          `json:"Sid"`
		Effect    string          `json:"Effect"`
		Action    json.RawMessage `json:"Action"`
		Resource  json.RawMessage `json:"Resource"`
		Condition map[string]map[string]string
	} `json:"Statement"`
}

func parsePolicy(t *testing.T, s string) policyDoc {
	t.Helper()
	var d policyDoc
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		t.Fatalf("session policy is not valid JSON: %v\n%s", err, s)
	}
	return d
}

// A session policy with no Allow grants nothing at all: session policies
// INTERSECT with the role's identity policies, so the Allow must be present or
// the narrowed credentials can do nothing and every helper breaks.
func TestSessionPolicy_HasAnAllowOrItGrantsNothing(t *testing.T) {
	got, err := secrets.SessionPolicy("km", "km-artifacts-1", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	d := parsePolicy(t, got)
	var allows int
	for _, s := range d.Statement {
		if s.Effect == "Allow" {
			allows++
		}
	}
	if allows == 0 {
		t.Fatal("no Allow statement: narrowed credentials would grant nothing " +
			"and every km helper would break")
	}
}

func TestSessionPolicy_DeniesTheSecretsKMSAlias(t *testing.T) {
	got, err := secrets.SessionPolicy("km2", "b", "s1")
	if err != nil {
		t.Fatal(err)
	}
	d := parsePolicy(t, got)
	for _, s := range d.Statement {
		if s.Effect != "Deny" || !strings.Contains(string(s.Action), "kms:Decrypt") {
			continue
		}
		if s.Condition["StringEquals"]["kms:ResourceAliases"] != "alias/km2-sandbox-secrets" {
			t.Fatalf("kms Deny condition = %v, want alias/km2-sandbox-secrets",
				s.Condition["StringEquals"])
		}
		return
	}
	t.Fatal("no Deny on kms:Decrypt")
}

// The Deny must be CONDITIONED, never blanket. An unconditional kms:Decrypt Deny
// would also kill the SSM SecureString reads km-github/km-slack/km-h1 depend on
// (a different key, conditioned on kms:ViaService=ssm), which is precisely the
// breakage the fence exists to avoid.
func TestSessionPolicy_KMSDenyIsNotBlanket(t *testing.T) {
	got, err := secrets.SessionPolicy("km", "b", "s1")
	if err != nil {
		t.Fatal(err)
	}
	d := parsePolicy(t, got)
	for _, s := range d.Statement {
		if s.Effect == "Deny" && strings.Contains(string(s.Action), "kms:Decrypt") {
			if len(s.Condition) == 0 {
				t.Fatal("unconditional kms:Decrypt Deny would break every helper's " +
					"SSM SecureString read")
			}
		}
	}
}

func TestSessionPolicy_DeniesTheBundleObjectExactly(t *testing.T) {
	got, err := secrets.SessionPolicy("km", "km-artifacts-1", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	want := `arn:aws:s3:::km-artifacts-1/sandboxes/abc123/secrets.enc.yaml`
	if !strings.Contains(got, want) {
		t.Fatalf("session policy does not deny %s\n%s", want, got)
	}
	if strings.Contains(got, "sandboxes/*") {
		t.Fatal("bundle Deny is wildcarded; it must name this sandbox's object only")
	}
}

func TestSessionPolicy_RejectsEmptyInputs(t *testing.T) {
	for _, c := range []struct{ prefix, bucket, id string }{
		{"", "b", "s"}, {"km", "", "s"}, {"km", "b", ""},
	} {
		if _, err := secrets.SessionPolicy(c.prefix, c.bucket, c.id); err == nil {
			t.Errorf("SessionPolicy(%q,%q,%q) returned no error; an empty component "+
				"would silently produce a Deny that matches nothing, which is a fence "+
				"that does not fence", c.prefix, c.bucket, c.id)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./pkg/secrets/ -run SessionPolicy -v 2>&1 | tail -15
```

Expected: FAIL — `undefined: secrets.SessionPolicy`.

- [ ] **Step 3: Write the implementation**

```go
// pkg/secrets/fence.go
package secrets

import (
	"encoding/json"
	"fmt"
)

// SessionPolicy is the inline policy the broker attaches when it self-assumes
// the instance role to mint credentials for a fenced sandbox (design doc §4.4).
//
// The result is the instance role MINUS exactly two things: the ability to
// decrypt the secrets bundle's KMS key, and the ability to fetch the bundle
// object from S3. Everything else the instance role can do, these credentials
// can still do — which is what keeps km-github, km-slack, km-h1 and the git
// credential helpers working behind the fence.
//
// Self-assume rather than a parallel role is the load-bearing choice: the
// narrowed credentials are DEFINITIONALLY the instance role minus two Denies, so
// the two can never drift. A parallel role would need the instance role's whole
// policy set duplicated and kept in sync forever.
//
// Both Denies are CONDITIONED or exact, never blanket:
//
//   - kms:Decrypt is denied only where kms:ResourceAliases names this install's
//     sandbox-secrets alias. The separate grant that lets helpers read SSM
//     SecureStrings targets a different key and is conditioned on
//     kms:ViaService=ssm, so it matches neither and keeps working.
//   - s3:GetObject is denied on this sandbox's bundle object only, not a prefix.
//
// The Allow is not decoration. A session policy INTERSECTS with the role's
// identity policies, so a document containing only Denies would grant nothing at
// all and every helper would break instantly.
func SessionPolicy(resourcePrefix, artifactsBucket, sandboxID string) (string, error) {
	// An empty component would interpolate into a Deny that matches nothing —
	// a fence that reports success and does not fence. Refuse instead.
	switch {
	case resourcePrefix == "":
		return "", fmt.Errorf("secrets: session policy needs a resource prefix")
	case artifactsBucket == "":
		return "", fmt.Errorf("secrets: session policy needs an artifacts bucket")
	case sandboxID == "":
		return "", fmt.Errorf("secrets: session policy needs a sandbox id")
	}

	doc := map[string]any{
		"Version": "2012-10-17",
		"Statement": []any{
			map[string]any{
				"Sid":      "InheritTheInstanceRole",
				"Effect":   "Allow",
				"Action":   "*",
				"Resource": "*",
			},
			map[string]any{
				"Sid":      "DenySecretsBundleKMS",
				"Effect":   "Deny",
				"Action":   "kms:Decrypt",
				"Resource": "*",
				"Condition": map[string]any{
					"StringEquals": map[string]any{
						"kms:ResourceAliases": "alias/" + resourcePrefix + "-sandbox-secrets",
					},
				},
			},
			map[string]any{
				"Sid":    "DenySecretsBundleObject",
				"Effect": "Deny",
				"Action": "s3:GetObject",
				"Resource": fmt.Sprintf("arn:aws:s3:::%s/sandboxes/%s/secrets.enc.yaml",
					artifactsBucket, sandboxID),
			},
		},
	}

	b, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("secrets: marshal session policy: %w", err)
	}
	return string(b), nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./pkg/secrets/ -run SessionPolicy -v 2>&1 | tail -15
```

Expected: PASS, five tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/secrets/fence.go pkg/secrets/fence_test.go
git commit -m "feat(secrets): the fence session policy, in one place

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01LJgXTjthChRMjYCLp7RD8Z"
```

---

### Task 3: Broker — the `Credentials` RPC

**Files:**
- Modify: `pkg/secrets/protocol.go`
- Create: `cmd/km-secretsd/credentials.go`
- Modify: `cmd/km-secretsd/server.go` (dispatch on `Op`)
- Modify: `cmd/km-secretsd/main.go` (wire the new Server fields from env)
- Test: `cmd/km-secretsd/credentials_test.go`

**Interfaces:**
- Consumes: `secrets.SessionPolicy` (Task 2).
- Produces:
  - `secrets.OpUnseal = ""`, `secrets.OpCredentials = "credentials"`
  - `secrets.Request{Op, As, Only}` — `UnsealRequest` gains an `Op` field and
    keeps its name; empty `Op` means unseal.
  - `secrets.UnsealResponse.Credentials *secrets.Credentials`
  - `secrets.Credentials{Version, AccessKeyID, SecretAccessKey, SessionToken, Expiration}`
    with **exactly** the `credential_process` JSON field names, so `km-creds`
    prints the broker's bytes verbatim rather than re-marshalling into a second
    shape that could drift.
  - `Server.FenceEnabled bool`, `Server.ResourcePrefix`, `Server.ArtifactsBucket`,
    `Server.SandboxID string`, `Server.STS STSAPI`
  - `(*Server).mintCredentials(ctx) (*secrets.Credentials, error)`

- [ ] **Step 1: Extend the protocol**

In `pkg/secrets/protocol.go`:

```go
// Op names the RPC. The zero value means OpUnseal, so the request km-env has
// always written keeps meaning what it meant.
const (
	OpUnseal      = ""
	OpCredentials = "credentials"
)

// Credentials is the credential_process contract (schema Version 1). The field
// names and their casing ARE the contract: km-creds prints this struct straight
// to stdout, so there is exactly one shape and it cannot drift from what the
// AWS SDKs parse.
type Credentials struct {
	Version         int    `json:"Version"`
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration"` // RFC3339
}
```

Add `Op string \`json:"op,omitempty"\`` as the first field of `UnsealRequest`, and
`Credentials *Credentials \`json:"credentials,omitempty"\`` to `UnsealResponse`.

- [ ] **Step 2: Write the failing test**

```go
// cmd/km-secretsd/credentials_test.go
package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type fakeSTS struct {
	callerArn  string
	callerErr  error
	assumeIn   *sts.AssumeRoleInput
	assumeErr  error
	expiresIn  time.Duration
	assumeCall int
}

func (f *fakeSTS) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	if f.callerErr != nil {
		return nil, f.callerErr
	}
	acct := "052251888500"
	return &sts.GetCallerIdentityOutput{Arn: &f.callerArn, Account: &acct}, nil
}

func (f *fakeSTS) AssumeRole(_ context.Context, in *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	f.assumeCall++
	f.assumeIn = in
	if f.assumeErr != nil {
		return nil, f.assumeErr
	}
	exp := time.Now().Add(f.expiresIn)
	return &sts.AssumeRoleOutput{Credentials: &sts.Credentials{
		AccessKeyId: aws.String("AKIATEST"), SecretAccessKey: aws.String("sk"),
		SessionToken: aws.String("tok"), Expiration: &exp,
	}}, nil
}

func fencedServer(f *fakeSTS) *Server {
	return &Server{
		FenceEnabled: true, ResourcePrefix: "km",
		ArtifactsBucket: "km-artifacts-1", SandboxID: "abc123",
		STS: f, Audit: &nopAudit{},
	}
}

// The role ARN is DERIVED from the caller's own identity, not configured, so it
// cannot drift from the role the instance actually runs as.
func TestMintCredentials_DerivesTheRoleARNFromItsOwnIdentity(t *testing.T) {
	f := &fakeSTS{
		callerArn: "arn:aws:sts::052251888500:assumed-role/km-ec2spot-ssm-abc123-use1/i-0f00",
		expiresIn: time.Hour,
	}
	if _, err := fencedServer(f).mintCredentials(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := "arn:aws:iam::052251888500:role/km-ec2spot-ssm-abc123-use1"
	if got := aws.ToString(f.assumeIn.RoleArn); got != want {
		t.Fatalf("RoleArn = %q, want %q", got, want)
	}
}

// Role chaining caps a session at one hour. Asking for more is a hard STS error,
// so the request must never exceed it.
func TestMintCredentials_NeverExceedsTheRoleChainingCap(t *testing.T) {
	f := &fakeSTS{
		callerArn: "arn:aws:sts::052251888500:assumed-role/km-ec2spot-ssm-abc123-use1/i-0f00",
		expiresIn: time.Hour,
	}
	if _, err := fencedServer(f).mintCredentials(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := aws.ToInt32(f.assumeIn.DurationSeconds); d > 3600 {
		t.Fatalf("DurationSeconds = %d, exceeds the 3600s role-chaining cap", d)
	}
}

// The session policy must actually be attached. Without it the "narrowed"
// credentials are the instance role in full and the fence is decorative.
func TestMintCredentials_AttachesTheSessionPolicy(t *testing.T) {
	f := &fakeSTS{
		callerArn: "arn:aws:sts::052251888500:assumed-role/km-ec2spot-ssm-abc123-use1/i-0f00",
		expiresIn: time.Hour,
	}
	if _, err := fencedServer(f).mintCredentials(context.Background()); err != nil {
		t.Fatal(err)
	}
	pol := aws.ToString(f.assumeIn.Policy)
	if pol == "" {
		t.Fatal("no session policy attached: the credentials would be the full instance role")
	}
	for _, want := range []string{"alias/km-sandbox-secrets", "sandboxes/abc123/secrets.enc.yaml", `"Deny"`} {
		if !strings.Contains(pol, want) {
			t.Errorf("session policy missing %q:\n%s", want, pol)
		}
	}
}

// A live credential is reused rather than re-minted on every aws invocation:
// the pollers shell out to `aws` in loops, and a fresh AssumeRole per call is a
// self-inflicted STS throttle.
func TestMintCredentials_ReusesALiveCredential(t *testing.T) {
	f := &fakeSTS{
		callerArn: "arn:aws:sts::052251888500:assumed-role/km-ec2spot-ssm-abc123-use1/i-0f00",
		expiresIn: time.Hour,
	}
	s := fencedServer(f)
	for i := 0; i < 3; i++ {
		if _, err := s.mintCredentials(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.assumeCall != 1 {
		t.Fatalf("AssumeRole called %d times, want 1 (cache did not hold)", f.assumeCall)
	}
}

// ...and is NOT reused once it is close enough to expiry that a consumer could
// still be using it when it dies.
func TestMintCredentials_RefreshesNearExpiry(t *testing.T) {
	f := &fakeSTS{
		callerArn: "arn:aws:sts::052251888500:assumed-role/km-ec2spot-ssm-abc123-use1/i-0f00",
		expiresIn: 2 * time.Minute, // inside the refresh margin
	}
	s := fencedServer(f)
	for i := 0; i < 2; i++ {
		if _, err := s.mintCredentials(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.assumeCall != 2 {
		t.Fatalf("AssumeRole called %d times, want 2 (stale credential was reused)", f.assumeCall)
	}
}

// Fence off means no credentials RPC at all — never a silent fall-through that
// hands out credentials on a box whose IAM was never provisioned for it.
func TestCredentials_RefusedWhenFenceOff(t *testing.T) {
	s := fencedServer(&fakeSTS{})
	s.FenceEnabled = false
	if _, err := s.mintCredentials(context.Background()); err == nil {
		t.Fatal("mintCredentials succeeded with the fence off")
	}
}

func TestMintCredentials_RejectsAnUnexpectedCallerArn(t *testing.T) {
	for _, arn := range []string{"", "arn:aws:iam::052251888500:user/someone", "garbage"} {
		f := &fakeSTS{callerArn: arn, expiresIn: time.Hour}
		if _, err := fencedServer(f).mintCredentials(context.Background()); err == nil {
			t.Errorf("caller arn %q was accepted; a misparse would assume the wrong role", arn)
		}
	}
}
```

Add `type nopAudit struct{}` with `func (nopAudit) Emit(string, map[string]any) error { return nil }`
if the package does not already have one — check `cmd/km-secretsd/server_test.go`
first and reuse whatever fake it already defines rather than adding a duplicate.

- [ ] **Step 3: Run the test to verify it fails**

```bash
go test ./cmd/km-secretsd/ -run 'Credentials' -v 2>&1 | tail -20
```

Expected: FAIL — `mintCredentials` undefined, `Server.STS` undefined.

- [ ] **Step 4: Write the implementation**

```go
// cmd/km-secretsd/credentials.go
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

// chainedSessionSeconds is the ceiling STS allows for role chaining — assuming
// a role using credentials that are themselves a role's. Asking for more is a
// hard error, not a silent clamp, so this is a real bound and not a preference.
const chainedSessionSeconds = 3600

// refreshMargin is how long before expiry a cached credential stops being
// handed out. A consumer that receives a credential must have time to finish
// using it; handing out one with seconds left produces an expiry failure inside
// somebody else's request, far from this code.
const refreshMargin = 5 * time.Minute

// STSAPI is the slice of STS the broker uses, as an interface so the tests can
// prove the ARN derivation and the session policy without an account.
type STSAPI interface {
	GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
	AssumeRole(context.Context, *sts.AssumeRoleInput, ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// mintCredentials returns credentials that are the instance role minus the two
// grants that open the secrets bundle (pkg/secrets.SessionPolicy).
//
// The broker does this, not km-creds, for a structural reason: km-creds runs as
// uid sandbox and is therefore on the far side of the fence, with no way to
// reach IMDS for the credentials an AssumeRole call needs. The broker is root
// and unfenced.
func (s *Server) mintCredentials(ctx context.Context) (*secrets.Credentials, error) {
	if !s.FenceEnabled {
		// Never fall through to un-narrowed credentials. A box whose profile
		// did not ask for the fence has no v1.7.0 self-assume trust either, so
		// the AssumeRole would fail anyway — refusing here makes the reason
		// legible instead of surfacing as an opaque STS AccessDenied.
		return nil, errors.New("credentials: the IMDS fence is not enabled on this sandbox")
	}

	s.credMu.Lock()
	defer s.credMu.Unlock()
	if s.cachedCreds != nil && time.Until(s.cachedExpiry) > refreshMargin {
		return s.cachedCreds, nil
	}

	api, err := s.stsAPI(ctx)
	if err != nil {
		return nil, err
	}

	who, err := api.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("credentials: whoami: %w", err)
	}
	roleARN, err := roleARNFromCallerARN(aws.ToString(who.Arn))
	if err != nil {
		return nil, err
	}

	policy, err := secrets.SessionPolicy(s.ResourcePrefix, s.ArtifactsBucket, s.SandboxID)
	if err != nil {
		return nil, fmt.Errorf("credentials: %w", err)
	}

	out, err := api.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleARN),
		RoleSessionName: aws.String("km-fenced-" + s.SandboxID),
		Policy:          aws.String(policy),
		DurationSeconds: aws.Int32(chainedSessionSeconds),
	})
	if err != nil {
		return nil, fmt.Errorf("credentials: assume %s: %w "+
			"(is spec.secrets.fenceIMDS set, and was km init run since? the "+
			"self-assume trust statement lives in ec2spot v1.7.0)", roleARN, err)
	}
	if out.Credentials == nil {
		return nil, errors.New("credentials: STS returned no credentials")
	}

	exp := aws.ToTime(out.Credentials.Expiration)
	creds := &secrets.Credentials{
		Version:         1,
		AccessKeyID:     aws.ToString(out.Credentials.AccessKeyId),
		SecretAccessKey: aws.ToString(out.Credentials.SecretAccessKey),
		SessionToken:    aws.ToString(out.Credentials.SessionToken),
		Expiration:      exp.UTC().Format(time.RFC3339),
	}
	s.cachedCreds, s.cachedExpiry = creds, exp
	return creds, nil
}

// roleARNFromCallerARN turns the caller's assumed-role ARN into the IAM role ARN
// it came from.
//
// Deriving this instead of configuring it is deliberate: the answer is by
// construction the role the instance is actually running as, so it cannot drift
// from what Terraform provisioned. A misparse would make the broker assume some
// OTHER role, so every unexpected shape is an error rather than a best guess.
//
//	arn:aws:sts::ACCT:assumed-role/NAME/SESSION  ->  arn:aws:iam::ACCT:role/NAME
func roleARNFromCallerARN(caller string) (string, error) {
	parts := strings.Split(caller, ":")
	if len(parts) != 6 || parts[2] != "sts" {
		return "", fmt.Errorf("credentials: caller %q is not an STS ARN — the broker "+
			"must run under the instance role", caller)
	}
	res := strings.Split(parts[5], "/")
	if len(res) < 2 || res[0] != "assumed-role" {
		return "", fmt.Errorf("credentials: caller %q is not an assumed role", caller)
	}
	return fmt.Sprintf("arn:%s:iam::%s:role/%s", parts[1], parts[4], res[1]), nil
}

// stsAPI returns the injected client, or builds a real one on first use.
func (s *Server) stsAPI(ctx context.Context) (STSAPI, error) {
	if s.STS != nil {
		return s.STS, nil
	}
	s.stsOnce.Do(func() {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			s.stsErr = fmt.Errorf("credentials: load AWS config: %w", err)
			return
		}
		s.STS = sts.NewFromConfig(cfg)
	})
	return s.STS, s.stsErr
}
```

Add to the `Server` struct in `server.go`:

```go
	// Fence mode (Phase 133 Wave 2). All five are set from the unit's
	// Environment; FenceEnabled false leaves every other field unread.
	FenceEnabled    bool
	ResourcePrefix  string
	ArtifactsBucket string
	SandboxID       string
	STS             STSAPI

	credMu       sync.Mutex
	cachedCreds  *secrets.Credentials
	cachedExpiry time.Time
	stsOnce      sync.Once
	stsErr       error
```

- [ ] **Step 5: Dispatch on `Op` in `Handle`**

In `server.go`, immediately after the request decodes and **before** the decrypt
semaphore (a credentials request performs no decrypt and must not consume a
decrypt slot):

```go
	if req.Op == secrets.OpCredentials {
		s.handleCredentials(conn, uid, pid, req)
		return
	}
```

And the handler beside `refuse`:

```go
// handleCredentials answers the credential_process RPC km-creds speaks.
//
// Audited like every other broker action, and for the same reason: behind the
// fence this is the ONLY way uid sandbox obtains AWS credentials at all, so the
// record of who asked is the whole audit trail for the box's AWS activity.
func (s *Server) handleCredentials(conn net.Conn, uid, pid uint32, req secrets.UnsealRequest) {
	creds, err := s.mintCredentials(context.Background())
	if err != nil {
		s.refuse(conn, uid, pid, req, err.Error())
		return
	}
	_ = s.Audit.Emit("secret_credentials", map[string]any{
		"uid": uid, "pid": pid, "exe": exeOf(pid), "expires": creds.Expiration,
	})
	_ = json.NewEncoder(conn).Encode(secrets.UnsealResponse{Credentials: creds})
}
```

Add `"context"` to `server.go`'s imports.

- [ ] **Step 6: Wire the env in `main.go`**

In `cmd/km-secretsd/main.go`, in the `srv := &Server{...}` literal:

```go
		FenceEnabled:    os.Getenv("KM_FENCE_IMDS") == "true",
		ResourcePrefix:  os.Getenv("KM_RESOURCE_PREFIX"),
		ArtifactsBucket: os.Getenv("KM_ARTIFACTS_BUCKET"),
		SandboxID:       os.Getenv("KM_SANDBOX_ID"),
```

- [ ] **Step 7: Run the tests to verify they pass**

```bash
go test ./cmd/km-secretsd/ ./pkg/secrets/ -v 2>&1 | tail -30; echo "exit=$?"
```

Expected: PASS. If `go.mod` lacks `service/sts`, run
`go get github.com/aws/aws-sdk-go-v2/service/sts` first — check with
`grep 'service/sts' go.mod` before assuming.

- [ ] **Step 8: Commit**

```bash
git add pkg/secrets/protocol.go cmd/km-secretsd/credentials.go \
        cmd/km-secretsd/credentials_test.go cmd/km-secretsd/server.go \
        cmd/km-secretsd/main.go go.mod go.sum
git commit -m "feat(km-secretsd): mint credentials narrowed by two explicit Denies

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01LJgXTjthChRMjYCLp7RD8Z"
```

---

### Task 4: `km-creds` — the `credential_process` helper

**Files:**
- Create: `cmd/km-creds/main.go`
- Test: `cmd/km-creds/main_test.go`
- Modify: `internal/app/cmd/init.go` (`sidecarBuilds()`)

**Interfaces:**
- Consumes: `secrets.OpCredentials`, `secrets.UnsealResponse.Credentials`,
  `secrets.SocketPath`.
- Produces: the `/opt/km/bin/km-creds` binary. Prints one JSON object on stdout
  and nothing else; non-zero exit with a diagnosable stderr line on any failure.

- [ ] **Step 1: Write the failing test**

```go
// cmd/km-creds/main_test.go
package main

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

// fakeBroker answers one request with resp and records what it was asked.
func fakeBroker(t *testing.T, resp secrets.UnsealResponse) (string, *secrets.UnsealRequest) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := &secrets.UnsealRequest{}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = json.NewDecoder(conn).Decode(got)
		_ = json.NewEncoder(conn).Encode(resp)
	}()
	return sock, got
}

func TestFetch_AsksForCredentials(t *testing.T) {
	sock, asked := fakeBroker(t, secrets.UnsealResponse{
		Credentials: &secrets.Credentials{Version: 1, AccessKeyID: "AKIA"},
	})
	if _, err := fetch(sock); err != nil {
		t.Fatal(err)
	}
	if asked.Op != secrets.OpCredentials {
		t.Fatalf("Op = %q, want %q", asked.Op, secrets.OpCredentials)
	}
}

// The output must be exactly the credential_process schema, with AWS's own
// casing. A single wrong key and every SDK on the box silently falls back to
// the credential chain — and behind the fence there is nothing to fall back to.
func TestRender_EmitsTheCredentialProcessSchema(t *testing.T) {
	out, err := render(&secrets.Credentials{
		Version: 1, AccessKeyID: "AKIA", SecretAccessKey: "sk",
		SessionToken: "tok", Expiration: "2026-09-04T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"Version", "AccessKeyId", "SecretAccessKey", "SessionToken", "Expiration"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing credential_process key %q in %s", k, out)
		}
	}
	if v, _ := m["Version"].(float64); v != 1 {
		t.Errorf("Version = %v, want 1", m["Version"])
	}
}

// Fail closed and loudly. Printing nothing on stdout with a zero exit would make
// the SDK report an unparseable credential_process response rather than the real
// cause.
func TestFetch_ErrorsWhenTheBrokerRefuses(t *testing.T) {
	sock, _ := fakeBroker(t, secrets.UnsealResponse{Error: "the IMDS fence is not enabled"})
	_, err := fetch(sock)
	if err == nil {
		t.Fatal("a refusal was not surfaced as an error")
	}
	if !strings.Contains(err.Error(), "fence") {
		t.Errorf("error lost the broker's reason: %v", err)
	}
}

func TestFetch_ErrorsWhenTheBrokerIsAbsent(t *testing.T) {
	if _, err := fetch(filepath.Join(t.TempDir(), "nope.sock")); err == nil {
		t.Fatal("a dead broker was not surfaced as an error")
	}
}

// A malformed response must not become an empty-but-valid credential object.
func TestFetch_ErrorsOnAResponseWithNoCredentials(t *testing.T) {
	sock, _ := fakeBroker(t, secrets.UnsealResponse{})
	if _, err := fetch(sock); err == nil {
		t.Fatal("a response carrying no credentials was accepted")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./cmd/km-creds/ -v 2>&1 | tail -10
```

Expected: FAIL — no such package / `fetch` undefined.

- [ ] **Step 3: Write the implementation**

```go
// Command km-creds is the credential_process helper the sandbox user's
// ~/.aws/config names when spec.secrets.fenceIMDS is on.
//
// It is deliberately the dumbest component in the phase: it asks the broker for
// credentials and prints the answer verbatim. It never calls STS, never caches,
// never retries, and never touches IMDS — it CANNOT touch IMDS, because it runs
// as uid sandbox, which is exactly what the fence blocks. All of the policy
// lives in km-secretsd, which is root and unfenced.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/whereiskurt/klanker-maker/pkg/secrets"
)

func main() {
	creds, err := fetch(secrets.SocketPath)
	if err != nil {
		// Fail closed and loudly. An empty stdout with a zero exit reaches the
		// operator as "unparseable credential_process output", which names the
		// wrong component.
		fmt.Fprintf(os.Stderr, "km-creds: %v\n", err)
		fmt.Fprintln(os.Stderr, "km-creds: is km-secretsd running? try: systemctl status km-secretsd")
		os.Exit(1)
	}
	out, err := render(creds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "km-creds: %v\n", err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}

// fetch asks the broker for narrowed credentials.
func fetch(socketPath string) (*secrets.Credentials, error) {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the secrets broker at %s: %w", socketPath, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	if err := json.NewEncoder(conn).Encode(secrets.UnsealRequest{Op: secrets.OpCredentials}); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	var resp secrets.UnsealResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	if resp.Credentials == nil {
		return nil, errors.New("broker returned no credentials")
	}
	return resp.Credentials, nil
}

// render serialises the credential_process response. The struct's json tags ARE
// the schema, so there is one shape and no chance of drift between what the
// broker mints and what the SDKs parse.
func render(c *secrets.Credentials) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("marshal credentials: %w", err)
	}
	return append(b, '\n'), nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./cmd/km-creds/ -v 2>&1 | tail -15
```

Expected: PASS, six tests.

- [ ] **Step 5: Add the sidecar build entry**

In `internal/app/cmd/init.go`, in `sidecarBuilds()`, directly after the `km-env`
entry (which sits after `km-secretsd`):

```go
		{name: "km-creds", srcDir: "cmd/km-creds"},
```

Verify it took:

```bash
grep -n 'km-secretsd\|km-env\|km-creds' internal/app/cmd/init.go
```

Expected: three consecutive entries. A binary missing from this list is never
uploaded, and userdata's `s3 cp` then fails at boot
([[project_new_lambda_needs_live_unit_and_init_list]] is the Lambda twin of this
trap).

- [ ] **Step 6: Verify it cross-compiles the way the uploader will**

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/km-creds ./cmd/km-creds && \
  file /tmp/km-creds && find /tmp -maxdepth 1 -name km-creds -delete
```

Expected: `ELF 64-bit LSB executable, x86-64`.

- [ ] **Step 7: Commit**

```bash
git add cmd/km-creds internal/app/cmd/init.go
git commit -m "feat(km-creds): credential_process helper for fenced sandboxes

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01LJgXTjthChRMjYCLp7RD8Z"
```

---

### Task 5: `infra/modules/ec2spot/v1.7.0` — the self-assume trust

**Files:**
- Create: `infra/modules/ec2spot/v1.7.0/{main.tf,variables.tf,outputs.tf}` (copy of v1.6.0 + edits)
- Modify: `infra/templates/sandbox/terragrunt.hcl` (`substrate_module_versions`)
- Modify: `pkg/compiler/ec2spot_timeout_test.go` (`ec2spotModuleDir`)
- Test: `pkg/compiler/ec2spot_fence_iam_test.go` (new)

**Interfaces:**
- Consumes: `fence_imds` from service.hcl `module_inputs` (Task 1).
- Produces: `variable "fence_imds"` (bool, default `false`); a second trust
  statement on `aws_iam_role.ec2spot_ssm` and an
  `aws_iam_role_policy.ec2spot_fence_self_assume`, both gated on it.

- [ ] **Step 1: Create the new immutable module version**

```bash
cp -R infra/modules/ec2spot/v1.6.0 infra/modules/ec2spot/v1.7.0
git add infra/modules/ec2spot/v1.7.0
git diff --cached --stat -- infra/modules/ec2spot/v1.7.0 | tail -3
```

Never edit `v1.6.0`: sandboxes created against it keep rendering against it, and
`km destroy` reuses the create-time `terragrunt.hcl`.

- [ ] **Step 2: Write the failing guard test**

```go
// pkg/compiler/ec2spot_fence_iam_test.go
package compiler

import (
	"os"
	"strings"
	"testing"
)

func fenceModuleMainTF(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(ec2spotModuleDir + "/main.tf")
	if err != nil {
		t.Fatalf("cannot read the live ec2spot module: %v", err)
	}
	return string(b)
}

// A role cannot name its own ARN as a trust principal — IAM resolves a principal
// to a unique id at save time and rejects one that does not exist yet
// (MalformedPolicyDocument, verified live 2026-09-04). The account-root
// principal narrowed by aws:PrincipalArn is the single-pass equivalent.
func TestEC2Spot_SelfAssumeTrustDoesNotNameItsOwnARNAsPrincipal(t *testing.T) {
	src := fenceModuleMainTF(t)
	if !strings.Contains(src, "aws:PrincipalArn") {
		t.Fatal("no aws:PrincipalArn condition: the self-assume trust either is " +
			"missing or names its own ARN as a principal, which IAM rejects at " +
			"CreateRole time")
	}
	if strings.Contains(src, `"AWS" = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.resource_prefix}-ec2spot-ssm`) {
		t.Fatal("trust policy names the role's own ARN as a principal: CreateRole " +
			"fails with MalformedPolicyDocument")
	}
}

// Root-principal delegation authorizes nothing on its own: the calling principal
// also needs an identity-based sts:AssumeRole on the role. Ship one half and the
// assume fails with an AccessDenied that names neither.
func TestEC2Spot_SelfAssumeHasBothHalves(t *testing.T) {
	src := fenceModuleMainTF(t)
	if !strings.Contains(src, "ec2spot_fence_self_assume") {
		t.Fatal("no identity-based sts:AssumeRole policy: root-principal trust " +
			"alone does not authorize the assume")
	}
}

// Dormant by default: fence_imds unset creates no fence resources at all.
func TestEC2Spot_FenceResourcesAreCountGated(t *testing.T) {
	src := fenceModuleMainTF(t)
	block := extractResourceBlock(t, src, `resource "aws_iam_role_policy" "ec2spot_fence_self_assume"`)
	if !strings.Contains(block, "var.fence_imds") {
		t.Error("the self-assume policy is not gated on var.fence_imds: every " +
			"sandbox would gain it, breaking the dormant case")
	}
	if !strings.Contains(block, "count") {
		t.Error("the self-assume policy has no count: it cannot be dormant")
	}
}

func TestEC2Spot_FenceVarDefaultsToFalse(t *testing.T) {
	b, err := os.ReadFile(ec2spotModuleDir + "/variables.tf")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, `variable "fence_imds"`)
	if i < 0 {
		t.Fatal(`no variable "fence_imds" in the live module`)
	}
	end := i + 400
	if end > len(src) {
		end = len(src)
	}
	if !strings.Contains(src[i:end], "default     = false") &&
		!strings.Contains(src[i:end], "default = false") {
		t.Error("fence_imds does not default to false: pre-Wave-2 callers would " +
			"fail to plan")
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

```bash
go test ./pkg/compiler/ -run 'EC2Spot_(SelfAssume|Fence)' -v 2>&1 | tail -20
```

Expected: FAIL on all four — the copied v1.7.0 is still v1.6.0's content.
(`ec2spotModuleDir` still points at v1.6.0 at this step; that is fine, both
directories are identical right now.)

- [ ] **Step 4: Add the variable**

Append to `infra/modules/ec2spot/v1.7.0/variables.tf`:

```hcl
# Phase 133 Wave 2 — the IMDS fence.
#
# When true the instance role additionally trusts ITSELF, so km-secretsd (root,
# and therefore unfenced) can self-assume with an inline session policy that
# subtracts the two grants opening the secrets bundle. Uid sandbox is blocked
# from 169.254.169.254 by an iptables rule in userdata, and every helper that
# reads AWS as that uid goes through km-creds instead.
#
# Default false: a sandbox that does not ask for the fence creates none of these
# resources and its role is byte-identical to v1.6.0.
variable "fence_imds" {
  description = "Add the self-assume trust and sts:AssumeRole grant km-secretsd needs to mint narrowed credentials (spec.secrets.fenceIMDS)."
  type        = bool
  default     = false
}
```

- [ ] **Step 5: Add the trust statement and the grant**

In `infra/modules/ec2spot/v1.7.0/main.tf`, replace the `assume_role_policy` of
`resource "aws_iam_role" "ec2spot_ssm"` with:

```hcl
  # The second statement is the self-assume trust the IMDS fence needs.
  #
  # It uses the ACCOUNT-ROOT principal narrowed by aws:PrincipalArn rather than
  # naming the role's own ARN directly, and that is not a style choice: IAM
  # resolves a principal ARN to a unique principal id when the policy is SAVED,
  # so a role cannot name itself at CreateRole time. Verified live 2026-09-04 —
  # `MalformedPolicyDocument: Invalid principal in policy`. The account root
  # always exists, and aws:PrincipalArn is a global condition key AWS populates
  # on every request, so the two forms are equally narrow. (Contrast the
  # aws:RequestTag trap on ec2:RunInstances, where AWS never populates the key
  # and the condition is unsatisfiable — see the Phase 126 findings.)
  #
  # Root-principal delegation does not authorize on its own; the matching
  # identity-based grant is aws_iam_role_policy.ec2spot_fence_self_assume below.
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat(
      [
        {
          Action    = "sts:AssumeRole"
          Effect    = "Allow"
          Principal = { Service = "ec2.amazonaws.com" }
        }
      ],
      var.fence_imds ? [
        {
          Sid       = "SelfAssumeForFencedCredentials"
          Action    = "sts:AssumeRole"
          Effect    = "Allow"
          Principal = { AWS = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root" }
          Condition = {
            StringEquals = {
              "aws:PrincipalArn" = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.resource_prefix}-ec2spot-ssm-${var.sandbox_id}-${var.region_label}"
            }
          }
        }
      ] : []
    )
  })
```

The condition's role name is the literal of `aws_iam_role.ec2spot_ssm.name` one
line above. It is spelled out rather than referenced because referencing the
resource from its own argument is a Terraform cycle — that half of §4.4 is
correct and preserved.

Then append, next to the other sandbox-secrets policies:

```hcl
# ============================================================
# Phase 133 Wave 2 — the IMDS fence: self-assume grant
# ============================================================
# The identity-based half of the self-assume. The trust statement above uses the
# account-root principal, which delegates rather than authorizes, so without this
# the AssumeRole fails with an AccessDenied naming neither half.
#
# Narrowing happens at RUNTIME, in the inline session policy km-secretsd attaches
# (pkg/secrets.SessionPolicy), not here — which is the point of self-assume: the
# narrowed credentials are definitionally this role minus two Denies, so the two
# can never drift the way a parallel role's duplicated policy set would.
resource "aws_iam_role_policy" "ec2spot_fence_self_assume" {
  count = (local.create_instance_role && var.fence_imds) ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-fence-self-assume"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "SelfAssumeForFencedCredentials"
        Effect   = "Allow"
        Action   = "sts:AssumeRole"
        Resource = "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.resource_prefix}-ec2spot-ssm-${var.sandbox_id}-${var.region_label}"
      }
    ]
  })
}
```

- [ ] **Step 6: Bump the pin and the test constant together**

In `infra/templates/sandbox/terragrunt.hcl`:

```hcl
  substrate_module_versions = {
    ec2spot = "v1.7.0"
    ecs     = "v1.0.0"
  }
```

In `pkg/compiler/ec2spot_timeout_test.go`, extend the comment history and update
the constant:

```go
// v1.5.0 -> v1.6.0 (execs/ S3 grant, Phase 132), and again when it moved
// v1.6.0 -> v1.7.0 (IMDS-fence self-assume trust, Phase 133 Wave 2).
const ec2spotModuleDir = "../../infra/modules/ec2spot/v1.7.0"
```

`TestEC2SpotModuleDir_TracksLivePin` exists precisely to make this pair
mandatory: a stale constant does not fail, it silently stops testing the live
module, which reads as coverage.

- [ ] **Step 7: Validate the Terraform and run the guards**

```bash
terraform -chdir=infra/modules/ec2spot/v1.7.0 fmt -check -diff
terraform -chdir=infra/modules/ec2spot/v1.7.0 init -backend=false >/dev/null && \
terraform -chdir=infra/modules/ec2spot/v1.7.0 validate
go test ./pkg/compiler/ -run 'EC2Spot' -v 2>&1 | tail -25; echo "exit=$?"
```

Expected: `Success! The configuration is valid.` and PASS on every `EC2Spot`
test including `TracksLivePin`. If `terraform init` leaves a
`.terraform.lock.hcl` under the module dir, delete it — a stray module-source
lock gets re-copied into the terragrunt cache and causes provider-checksum drift
([[project_module_source_lock_drift]]):

```bash
find infra/modules/ec2spot/v1.7.0 -name '.terraform.lock.hcl' -delete
find infra/modules/ec2spot/v1.7.0 -name '.terraform' -type d -exec rm -rf {} + 2>/dev/null || true
```

- [ ] **Step 8: Commit**

```bash
git add infra/modules/ec2spot/v1.7.0 infra/templates/sandbox/terragrunt.hcl \
        pkg/compiler/ec2spot_timeout_test.go pkg/compiler/ec2spot_fence_iam_test.go
git commit -m "feat(ec2spot): v1.7.0 — self-assume trust for the IMDS fence

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01LJgXTjthChRMjYCLp7RD8Z"
```

---

### Task 6: Userdata — the fence unit, `km-creds`, and `~/.aws/config`

**Files:**
- Modify: `pkg/compiler/userdata.go` (params struct, §5.5 install, new §5.6, §7.9 ordering)
- Test: `pkg/compiler/userdata_fence_test.go` (new)
- Modify: goldens if and only if a golden profile turns the fence on (none should)

**Interfaces:**
- Consumes: `profile.IsFenceIMDSEnabled` (Task 1); the `km-creds` binary (Task 4).
- Produces: `userDataParams.FenceIMDS bool`; on-box
  `/etc/systemd/system/km-imds-fence.service`, `/opt/km/bin/km-creds`,
  `/home/sandbox/.aws/config`.

**The template is a bash program inside a Go raw string** — a wrong or lost edit
compiles fine ([[project_userdata_template_invisible_to_go_build]]). Render and
read the output, do not trust the diff.

- [ ] **Step 1: Write the failing test**

```go
// pkg/compiler/userdata_fence_test.go
package compiler

import (
	"strings"
	"testing"
)

// Dormant: a bundle-carrying profile that never mentions fenceIMDS must render
// no fence artefacts at all.
func TestUserdata_NoFenceArtefactsWhenUnset(t *testing.T) {
	got := renderUserdataForTest(t, false)
	for _, forbidden := range []string{"km-imds-fence", "km-creds", "credential_process"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("dormant userdata contains %q", forbidden)
		}
	}
}

// The rule must live in the FILTER table. The nat-table IMDS exemption is a
// DIFFERENT rule with the opposite purpose (it RETURNs IMDS traffic so IMDSv2
// keeps working), it only exists under enforcement: proxy, and a fence written
// there would be absent under ebpf/both — the schema's other two modes.
func TestUserdata_FenceIsInTheFilterTable(t *testing.T) {
	got := renderUserdataForTest(t, true)
	if !strings.Contains(got, "iptables -A OUTPUT -d 169.254.169.254 -m owner --uid-owner sandbox -j REJECT") {
		t.Error("no filter-table REJECT for uid sandbox to 169.254.169.254")
	}
	if strings.Contains(got, "-t nat -A OUTPUT -d 169.254.169.254 -m owner --uid-owner sandbox") {
		t.Error("the fence was written into the nat table, where it would be absent " +
			"under ebpf/both enforcement")
	}
}

// REJECT, never DROP: the SDK's IMDS probe against a DROP waits out a full
// connect timeout on every call, turning the fence into a performance cliff that
// looks like a hang.
func TestUserdata_FenceRejectsRatherThanDrops(t *testing.T) {
	got := renderUserdataForTest(t, true)
	if strings.Contains(got, "--uid-owner sandbox -j DROP") {
		t.Error("the fence DROPs; it must REJECT so the SDK fails fast")
	}
}

// The fence must survive a resume. There is no iptables persistence anywhere in
// this repo, but km-secretsd.service and every shim ARE enabled units and do come
// back — so a userdata-only rule leaves a resumed box looking healthy with no
// fence at all.
func TestUserdata_FenceIsASystemdUnit(t *testing.T) {
	got := renderUserdataForTest(t, true)
	if !strings.Contains(got, "km-imds-fence.service") {
		t.Fatal("the fence is not a systemd unit: it would vanish on km resume")
	}
	if !strings.Contains(got, "systemctl enable") || !strings.Contains(got, "km-imds-fence") {
		t.Error("km-imds-fence.service is never enabled, so it will not run on resume")
	}
}

// Assertion 6 must not race the rule it asserts.
func TestUserdata_SecretsCheckIsOrderedAfterTheFence(t *testing.T) {
	got := renderUserdataForTest(t, true)
	i := strings.Index(got, "KMSECRETSCHECK")
	if i < 0 {
		t.Fatal("km-secrets-check unit heredoc not found")
	}
	unit := got[i:min(i+900, len(got))]
	if !strings.Contains(unit, "After=km-imds-fence.service") {
		t.Error("km-secrets-check.service is not ordered after km-imds-fence.service: " +
			"assertion 6 can run before the rule exists")
	}
}

// credential_process is what keeps km-github/km-slack/km-h1 working behind the
// fence. Without it the fence is not a boundary, it is an outage.
func TestUserdata_WritesTheSandboxAWSConfig(t *testing.T) {
	got := renderUserdataForTest(t, true)
	if !strings.Contains(got, "/home/sandbox/.aws/config") {
		t.Fatal("no ~/.aws/config for the sandbox user")
	}
	if !strings.Contains(got, "credential_process = /opt/km/bin/km-creds") {
		t.Error("~/.aws/config does not name km-creds as its credential_process")
	}
}

func TestUserdata_InstallsKMCreds(t *testing.T) {
	got := renderUserdataForTest(t, true)
	if !strings.Contains(got, "sidecars/km-creds") {
		t.Error("km-creds is never fetched from S3; the credential_process would " +
			"be a missing binary and every AWS call as uid sandbox would fail")
	}
}

// The broker needs the fence flag and the two interpolation inputs for the
// session policy, or mintCredentials refuses.
func TestUserdata_BrokerUnitCarriesFenceEnv(t *testing.T) {
	got := renderUserdataForTest(t, true)
	i := strings.Index(got, "KMSECRETSD")
	if i < 0 {
		t.Fatal("km-secretsd unit heredoc not found")
	}
	unit := got[i:min(i+1800, len(got))]
	for _, want := range []string{"KM_FENCE_IMDS=true", "KM_RESOURCE_PREFIX=", "KM_ARTIFACTS_BUCKET="} {
		if !strings.Contains(unit, want) {
			t.Errorf("km-secretsd.service is missing %s", want)
		}
	}
}
```

Write `renderUserdataForTest(t *testing.T, fence bool) string` in the same file,
copying the construction from the nearest existing userdata test (find one with
`grep -rn "func Test.*Userdata" pkg/compiler/*_test.go | head`). It must set
`Spec.Secrets = &profile.SecretsSpec{SopsFile: "x.enc.yaml", FenceIMDS: &fence}`
so the dormant case still carries a bundle — otherwise
`TestUserdata_NoFenceArtefactsWhenUnset` would pass for the wrong reason (no §5.5
block at all). Add a local `min(a, b int) int` helper if the package has none.

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./pkg/compiler/ -run 'Userdata.*Fence|Fence.*Userdata|TestUserdata_' -v 2>&1 | tail -25
```

Expected: the dormant test PASSES (nothing is rendered yet), the seven
fence-on tests FAIL.

- [ ] **Step 3: Add the param**

In `userDataParams` beside `SopsGrantsJSON`:

```go
	// FenceIMDS gates section 5.6 — the km-imds-fence unit, the km-creds
	// install, and the sandbox user's credential_process config. Requires
	// SopsBundlePresent: there is no broker to mint credentials from without a
	// bundle, so a fence there would strand every helper.
	FenceIMDS bool
```

And where `SopsBundlePresent` is populated:

```go
	// Phase 133 Wave 2: the fence, only ever alongside a bundle.
	params.FenceIMDS = params.SopsBundlePresent && profile.IsFenceIMDSEnabled(p.Spec.Secrets)
```

- [ ] **Step 4: Fetch km-creds and add the broker env**

In section 5.5, after the `km-env` symlink line:

```
{{- if .FenceIMDS }}
aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/sidecars/km-creds" /opt/km/bin/km-creds
chmod +x /opt/km/bin/km-creds
{{- end }}
```

In the `km-secretsd.service` heredoc, after the `KM_SECRETS_GRANTS` line:

```
{{- if .FenceIMDS }}
# Fence mode. The broker mints credentials narrowed by pkg/secrets.SessionPolicy,
# which interpolates the prefix and the bucket, so both must reach it here.
Environment=KM_FENCE_IMDS=true
Environment=KM_RESOURCE_PREFIX={{ .ResourcePrefix }}
Environment=KM_ARTIFACTS_BUCKET={{ .KMArtifactsBucket }}
{{- end }}
```

- [ ] **Step 5: Add section 5.6**

Immediately after section 5.5's `echo "[km-bootstrap] km-secretsd started"` and
inside the same `{{- if .SopsBundlePresent }}` block:

```
{{- if .FenceIMDS }}

# ============================================================
# 5.6. IMDS fence (Phase 133 Wave 2)
# ============================================================
# Block uid sandbox from the instance metadata service, and re-home every helper
# that reads AWS as that uid onto credentials km-secretsd mints — the instance
# role minus the two grants that open the secrets bundle.
#
# A SYSTEMD UNIT, not a bare iptables call, and that is load-bearing. There is no
# iptables persistence anywhere in this codebase, and userdata does not re-run on
# stop/start (cloud-init is per-instance) — but km-secretsd.service and every
# shim ARE enabled units and do come back. A userdata-only rule would leave a
# resumed box with a live broker, working agents, and no fence, with nothing
# saying so.
#
# FILTER table, not nat: the nat-table IMDS rule at section 6 is a different rule
# with the opposite purpose (it RETURNs IMDS so IMDSv2 keeps working) and it only
# exists under enforcement: proxy. A fence written there would be silently absent
# under ebpf and both.
#
# REJECT, not DROP: an SDK probing IMDS against a DROP waits out a full connect
# timeout on every call, which reads as a hang rather than a policy.
cat > /usr/local/sbin/km-imds-fence.sh << 'KMFENCE'
#!/bin/sh
set -eu
if ! command -v iptables >/dev/null 2>&1; then
  # enforcement: ebpf and both install no iptables at all.
  (yum install -y iptables-nft || yum install -y iptables || \
   apt-get install -y --no-install-recommends iptables) >/dev/null 2>&1 || true
fi
command -v iptables >/dev/null 2>&1 || { echo "km-imds-fence: no iptables" >&2; exit 1; }
# -C first so a restart cannot stack duplicates.
iptables -C OUTPUT -d 169.254.169.254 -m owner --uid-owner sandbox -j REJECT 2>/dev/null || \
  iptables -A OUTPUT -d 169.254.169.254 -m owner --uid-owner sandbox -j REJECT
KMFENCE
chmod 0755 /usr/local/sbin/km-imds-fence.sh

cat > /etc/systemd/system/km-imds-fence.service << 'KMFENCEUNIT'
[Unit]
Description=km IMDS fence (block uid sandbox from 169.254.169.254)
# Before the broker so a fenced box is never briefly unfenced while agents
# could already be dispatched.
Before=km-secretsd.service
After=network-pre.target
Wants=network-pre.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/sbin/km-imds-fence.sh

[Install]
WantedBy=multi-user.target
KMFENCEUNIT
systemctl daemon-reload
systemctl enable --now km-imds-fence.service
echo "[km-bootstrap] IMDS fence active for uid sandbox"

# credential_process is the other half. Without it the fence is not a boundary,
# it is an outage: km-github, km-slack, km-h1 and the git credential helpers all
# read SSM as uid sandbox and would lose their credentials entirely.
#
# A config-file credential_process outranks the IMDS provider in every AWS SDK's
# chain, and userdata sets no AWS_ACCESS_KEY_ID for this user, so nothing
# outranks it in turn.
install -d -m 0700 -o sandbox -g sandbox /home/sandbox/.aws
cat > /home/sandbox/.aws/config << 'KMAWSCONFIG'
[default]
credential_process = /opt/km/bin/km-creds
KMAWSCONFIG
printf 'region = %s\n' '{{ .AWSRegion }}' >> /home/sandbox/.aws/config
chown sandbox:sandbox /home/sandbox/.aws/config
chmod 0600 /home/sandbox/.aws/config
echo "[km-bootstrap] sandbox AWS config points at km-creds"
{{- end }}
```

- [ ] **Step 6: Order the resume check after the fence**

In section 7.9's `km-secrets-check.service` heredoc, extend the `[Unit]` block:

```
After=km-secretsd.service
Requires=km-secretsd.service
{{- if .FenceIMDS }}
# Assertion 6 asserts the fence; it must not run before the rule exists.
After=km-imds-fence.service
Requires=km-imds-fence.service
{{- end }}
```

- [ ] **Step 7: Run the tests to verify they pass**

```bash
go test ./pkg/compiler/ -run 'Userdata' -v 2>&1 | tail -30; echo "exit=$?"
```

Expected: PASS, all eight.

- [ ] **Step 8: Read the rendered output — do not trust the diff**

```bash
go test ./pkg/compiler/ -run TestUserdata_FenceIsASystemdUnit -v 2>&1 | tail -5
cat > /tmp/dumpud_test.go <<'EOF'
EOF
find /tmp -maxdepth 1 -name dumpud_test.go -delete
```

Then render a fenced profile for real and read section 5.6 end to end:

```bash
go test ./pkg/compiler/ -run 'Userdata.*Fence' -v 2>&1 | grep -c FAIL
```

Add a temporary `t.Log(got)` to `renderUserdataForTest` if needed, read the
output, and remove it before committing. Confirm by eye: the heredoc sentinels
are single-quoted (`<< 'KMFENCE'`), so no `$PATH`/`$1` inside them is expanded at
render time; the `{{ .AWSRegion }}` interpolation is OUTSIDE the quoted heredoc
(hence the separate `printf`); and section 5.6 sits inside the
`{{- if .SopsBundlePresent }}` block. A heredoc-escaping mistake here aborts the
boot ([[project_userdata_template_invisible_to_go_build]]).

- [ ] **Step 9: Confirm the goldens are untouched**

```bash
go test ./pkg/compiler/ 2>&1 | tail -20; echo "exit=$?"
git status --short pkg/compiler/testdata/
```

Expected: `ok`, and **no** modified goldens — no golden profile sets `fenceIMDS`.
If a golden did change, stop: something is rendering in the dormant case. Never
regenerate the frozen `userdata_learn_v2_pre92_baseline.golden.sh` with
`CAPTURE_PRE92_BASELINE=1` ([[project_frozen_byte_identity_golden_capture_trap]]).

- [ ] **Step 10: Commit**

```bash
git add pkg/compiler/userdata.go pkg/compiler/userdata_fence_test.go
git commit -m "feat(userdata): the IMDS fence, as a unit that survives resume

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01LJgXTjthChRMjYCLp7RD8Z"
```

---

### Task 7: Selftest assertion 6, and the spec correction

**Files:**
- Modify: `cmd/km-secretsd/selftest.go`
- Modify: `cmd/km-secretsd/selftest_test.go`
- Modify: `docs/superpowers/specs/2026-09-04-brokered-secret-unsealing-design.md` (§4.4)

**Interfaces:**
- Consumes: `Server.FenceEnabled`, `mintCredentials` (Task 3).
- Produces: `SelftestOpts.FenceProbe func() (imdsBlocked, stsWorks, decryptDenied bool, detail string)`
  — a single injectable seam so the three clauses are testable off-box; nil means
  run them for real.

Assertion 6 is `fence`-named and **fatal**, and its three clauses are, in order:
uid-sandbox IMDS fails; `sts:GetCallerIdentity` as uid sandbox succeeds; the
narrowed credentials FAIL to decrypt the bundle.

**The third clause is a negative control and must stay one.** Do not substitute
`iam simulate-principal-policy`: AWS reports an unsatisfiable condition
identically to a missing statement, so a simulator verdict proves what the policy
SAYS, not what AWS ENFORCES ([[project_cross_account_gpu_launch]]).

- [ ] **Step 1: Write the failing test**

```go
// append to cmd/km-secretsd/selftest_test.go

func findCheck(checks []Check, name string) (Check, bool) {
	for _, c := range checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

// No fence, no assertion: a profile that never opted in must not gain a check it
// can only fail.
func TestSelftest_NoFenceCheckWhenFenceOff(t *testing.T) {
	s := &Server{CiphertextPath: writeTestBundle(t), Audit: &nopAudit{}}
	checks := s.Selftest(SelftestOpts{SocketPath: "", LookPathAs: noLookPath})
	if _, ok := findCheck(checks, "fence"); ok {
		t.Error("a fence check appeared with the fence off")
	}
}

func TestSelftest_FencePassesWhenAllThreeClausesHold(t *testing.T) {
	s := &Server{CiphertextPath: writeTestBundle(t), Audit: &nopAudit{}, FenceEnabled: true}
	checks := s.Selftest(SelftestOpts{
		SocketPath: "", LookPathAs: noLookPath,
		FenceProbe: func() (bool, bool, bool, string) { return true, true, true, "ok" },
	})
	c, ok := findCheck(checks, "fence")
	if !ok {
		t.Fatal("no fence check with the fence on")
	}
	if !c.OK {
		t.Fatalf("fence check failed with all clauses holding: %s", c.Detail)
	}
}

// Each clause must be able to fail the check ALONE, and the detail must name
// which one — a fence failure that says only "fence: FAIL" costs an operator an
// hour.
func TestSelftest_EachFenceClauseFailsIndependently(t *testing.T) {
	cases := []struct {
		name                            string
		imds, sts, denied               bool
		wantDetailContains              string
	}{
		{"imds still reachable", false, true, true, "IMDS"},
		{"helpers broken", true, false, true, "sts"},
		{"narrowed creds still decrypt", true, true, false, "decrypt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{CiphertextPath: writeTestBundle(t), Audit: &nopAudit{}, FenceEnabled: true}
			checks := s.Selftest(SelftestOpts{
				SocketPath: "", LookPathAs: noLookPath,
				FenceProbe: func() (bool, bool, bool, string) {
					return tc.imds, tc.sts, tc.denied, "probe detail"
				},
			})
			c, ok := findCheck(checks, "fence")
			if !ok {
				t.Fatal("no fence check")
			}
			if c.OK {
				t.Fatal("fence check passed with a clause failing")
			}
			if !c.Fatal {
				t.Error("fence check is not fatal; a broken fence must abort the boot")
			}
			if !strings.Contains(strings.ToLower(c.Detail), strings.ToLower(tc.wantDetailContains)) {
				t.Errorf("detail %q does not name the failing clause (%q)", c.Detail, tc.wantDetailContains)
			}
		})
	}
}
```

Reuse the file's existing helpers rather than adding duplicates: check what
`selftest_test.go` already defines for a bundle fixture and a no-op `LookPathAs`
(`grep -n 'func writeTestBundle\|LookPathAs:' cmd/km-secretsd/selftest_test.go`)
and rename the references above to match.

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./cmd/km-secretsd/ -run 'Fence' -v 2>&1 | tail -20
```

Expected: FAIL — `SelftestOpts.FenceProbe` undefined.

- [ ] **Step 3: Implement the assertion**

Add to `SelftestOpts`:

```go
	// FenceProbe runs assertion 6's three clauses and reports
	// (imdsBlocked, stsWorks, decryptDenied, detail). Nil means run them for
	// real via runFenceProbe. Injectable because none of the three can be
	// exercised off a real box.
	FenceProbe func() (bool, bool, bool, string)
```

Append to `Selftest`, after the per-consumer loop and before `return checks`:

```go
	// 6. Fence only. Three clauses, and the third is the one that matters.
	//
	// Clause 1 proves the rule is there; clause 2 proves it did not take the
	// helpers with it; clause 3 proves the credentials the helpers now use
	// cannot open the bundle. Clause 3 is a NEGATIVE CONTROL — the fence is
	// proven by proving a decrypt FAILS. An IAM simulator verdict is not a
	// substitute: AWS reports an unsatisfiable condition identically to a
	// missing statement, so the simulator says what the policy SAYS, not what
	// AWS ENFORCES.
	if s.FenceEnabled {
		probe := o.FenceProbe
		if probe == nil {
			probe = s.runFenceProbe
		}
		imdsBlocked, stsWorks, decryptDenied, detail := probe()
		switch {
		case !imdsBlocked:
			checks = append(checks, Check{"fence", false, true,
				"uid sandbox can still reach IMDS at 169.254.169.254: the fence is not " +
					"fencing (systemctl status km-imds-fence): " + detail})
		case !stsWorks:
			checks = append(checks, Check{"fence", false, true,
				"uid sandbox cannot call sts:GetCallerIdentity: the fence took the helpers " +
					"with it — km-github, km-slack, km-h1 and the git credential helpers " +
					"all read AWS as this uid (check /home/sandbox/.aws/config and " +
					"/opt/km/bin/km-creds): " + detail})
		case !decryptDenied:
			checks = append(checks, Check{"fence", false, true,
				"the narrowed credentials CAN still decrypt the secrets bundle: the session " +
					"policy Deny is not matching, so the fence buys nothing (check the " +
					"kms:ResourceAliases condition against the key's real aliases): " + detail})
		default:
			checks = append(checks, Check{"fence", true, true, detail})
		}
	}
```

And the real probe, in `credentials.go`:

```go
// runFenceProbe executes assertion 6's three clauses against the live box.
//
// Every clause runs AS UID SANDBOX (runuser), because uid sandbox is what the
// fence is about; running any of them as root would prove nothing at all.
func (s *Server) runFenceProbe() (imdsBlocked, stsWorks, decryptDenied bool, detail string) {
	var notes []string

	// 1. IMDS must fail for uid sandbox. A short --connect-timeout keeps a
	//    misconfigured DROP (rather than REJECT) from stalling the boot.
	err := exec.Command("runuser", "-u", "sandbox", "--", "curl", "-sS", "-o", "/dev/null",
		"--connect-timeout", "3", "-X", "PUT",
		"http://169.254.169.254/latest/api/token",
		"-H", "X-aws-ec2-metadata-token-ttl-seconds: 60").Run()
	imdsBlocked = err != nil
	notes = append(notes, fmt.Sprintf("imds-blocked=%v", imdsBlocked))

	// 2. The helpers must still work: credential_process must actually resolve.
	err = exec.Command("runuser", "-u", "sandbox", "--",
		"aws", "sts", "get-caller-identity").Run()
	stsWorks = err == nil
	notes = append(notes, fmt.Sprintf("sts-ok=%v", stsWorks))

	// 3. THE NEGATIVE CONTROL. The narrowed credentials must FAIL to decrypt the
	//    bundle. A success here means the Deny is not matching and the whole
	//    fence buys nothing, which no other clause can detect.
	out, err := exec.Command("runuser", "-u", "sandbox", "--",
		"/opt/km/bin/sops", "--decrypt", s.CiphertextPath).CombinedOutput()
	decryptDenied = err != nil
	if !decryptDenied {
		// Never let the plaintext reach a log line.
		zero(out)
		notes = append(notes, "decrypt-denied=false (bundle DECRYPTED as uid sandbox)")
	} else {
		notes = append(notes, "decrypt-denied=true")
	}

	return imdsBlocked, stsWorks, decryptDenied, strings.Join(notes, " ")
}
```

Add `"os/exec"` to `credentials.go`'s imports.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./cmd/km-secretsd/ -v 2>&1 | tail -30; echo "exit=$?"
```

Expected: PASS, including the five new fence tests and every Wave 1 test.

- [ ] **Step 5: Correct the spec**

In `docs/superpowers/specs/2026-09-04-brokered-secret-unsealing-design.md` §4.4,
replace the sentence beginning "Cost: the trust policy must name its own ARN…"
with:

```markdown
Cost: the trust policy cannot name its own ARN. IAM resolves a principal ARN to a
unique principal id when the policy is saved, so a role naming itself at
CreateRole time fails with `MalformedPolicyDocument: Invalid principal in policy`
— verified live against the application account, 2026-09-04. The single-pass
equivalent is the account-root principal narrowed by an `aws:PrincipalArn`
condition naming the role, plus a matching identity-based `sts:AssumeRole` grant
(root-principal delegation authorizes nothing on its own). This is exactly as
narrow: `aws:PrincipalArn` is a global condition key AWS populates on every
request, unlike the `aws:RequestTag`-on-`RunInstances` trap of Phase 126. Role
chaining caps sessions at one hour; `credential_process` refreshes, so this is
invisible.
```

Also add, at the end of §4.4:

```markdown
**The fence ships as `km-imds-fence.service`, not a bare userdata command.**
There is no iptables persistence anywhere in this codebase and userdata does not
re-run on stop/start, but `km-secretsd.service` and every shim are enabled units
and do come back — so a userdata-only rule would leave a resumed box with a live
broker, working agents, and no fence. `km-secrets-check.service` is ordered
`After=km-imds-fence.service` so assertion 6 cannot race the rule it asserts.
```

Answer §11's open questions in place: Q1 — nothing running as uid `sandbox` reads
IMDS for metadata (`grep -rn '169.254.169.254' skills/ cmd/km-github cmd/km-slack
cmd/km-h1` finds nothing; sandbox id and region come from
`/etc/profile.d/km-identity.sh`), and clause 2 of assertion 6 is the live check.
Q2 — self-assume was confirmed working in this account's SCP environment by the
2026-09-04 spike.

- [ ] **Step 6: Commit**

```bash
git add cmd/km-secretsd/selftest.go cmd/km-secretsd/selftest_test.go \
        cmd/km-secretsd/credentials.go \
        docs/superpowers/specs/2026-09-04-brokered-secret-unsealing-design.md
git commit -m "feat(km-secretsd): assertion 6 — prove the fence by proving a decrypt fails

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01LJgXTjthChRMjYCLp7RD8Z"
```

---

### Task 8: Whole-repo verification, Linux coverage, docs

**Files:**
- Modify: `docs/brokered-secrets.md`
- Modify: `CLAUDE.md`
- Modify: `profiles/` — one demo profile turning the fence on (pick whichever
  Wave 1 demo profile already carries `sopsFile`; `git show 8476c163 --stat --
  profiles/` names them)

- [ ] **Step 1: Run the whole suite**

```bash
go build ./... && go test ./... 2>&1 | grep -Ev '^(ok|---)' | head -40; echo "exit=$?"
```

Expected: no FAIL. Five named `Bootstrap`/`Cluster` tests in `internal/app/cmd`
fail deterministically on a dev machine (the AWS fast-fail seam) — that is the
known baseline, not a regression ([[project_cmd_suite_pre_existing_failures]]).
Anything else that fails is yours.

- [ ] **Step 2: Cover the Linux-only broker paths**

`go test ./...` on macOS does not exercise the unix-socket, `SO_PEERCRED` or
`runuser` paths at all. Cross-compile the test binaries and run them under
Docker; do not compile inside qemu, and keep `CGO_ENABLED=0`
([[project_linux_only_test_via_crosscompiled_binary]]):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o /tmp/secretsd.test ./cmd/km-secretsd
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o /tmp/creds.test    ./cmd/km-creds
docker run --rm -v /tmp:/t --platform linux/amd64 debian:12 \
  sh -c '/t/secretsd.test -test.v 2>&1 | tail -30; /t/creds.test -test.v 2>&1 | tail -20'
find /tmp -maxdepth 1 \( -name 'secretsd.test' -o -name 'creds.test' \) -delete
```

Expected: PASS in both, and a visibly higher test count than the macOS run.

- [ ] **Step 3: Validate every profile**

```bash
make build && ./scripts/validate-all-profiles.sh 2>&1 | tail -20; echo "exit=$?"
```

Add the demo profile to the script's **hardcoded array** — it enumerates files
explicitly, not by glob, so a new profile is silently outside the gate until
listed.

- [ ] **Step 4: Write the docs**

Add a `## The IMDS fence` section to `docs/brokered-secrets.md` covering: what
`fenceIMDS: true` turns on; the two Denies verbatim; **why the fence is not a
containment boundary against a determined caller** (uid sandbox can still speak
the broker protocol and still ask `km-creds` for credentials — the fence removes
the *unmediated* path to the role, it does not make the role unreachable, exactly
the §5.2/§6 disposition `grants` already has); the `credential_process` chain and
what breaks if `~/.aws/config` is lost; how to read assertion 6's three clauses
when it fails; and the deploy surface with the do-not-split warning.

Add a Phase 133 Wave 2 block to `CLAUDE.md` in the house style of the Phase 131 /
132 entries — lead with the three non-obvious findings (self-ARN trust is
rejected by IAM; the fence must be a unit or it evaporates on resume; `km-creds`
asks the broker because it is itself fenced), then the deploy surface, then the
pointer to `docs/brokered-secrets.md`.

- [ ] **Step 5: Commit**

```bash
git add docs/brokered-secrets.md CLAUDE.md profiles/ scripts/validate-all-profiles.sh
git commit -m "docs: the IMDS fence — runbook, limits, deploy surface

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01LJgXTjthChRMjYCLp7RD8Z"
```

---

## Live UAT — the only real proof

None of the three clauses of assertion 6 can be exercised off a real box, and
Wave 1's own live UAT found three defects that no review caught. Run this against
a real sandbox before calling Wave 2 done.

Deploy: `make build && make build-lambdas && km init --dry-run=false`. Not
`--sidecars`. Then `km create <fenced-profile> fence-uat`.

| # | Assertion | How |
|---|---|---|
| 1 | Boot completes and the selftest reports `fence ok` | `km logs <id>` \| grep km-secrets-check |
| 2 | uid sandbox cannot reach IMDS | `runuser -u sandbox -- curl -sS --connect-timeout 3 -X PUT http://169.254.169.254/latest/api/token -H 'X-aws-ec2-metadata-token-ttl-seconds: 60'` → non-zero |
| 3 | root still can | same curl without `runuser` → a token |
| 4 | The helpers still work | `runuser -u sandbox -- aws sts get-caller-identity`, then a real `km-github`/`km-slack` call end to end |
| 5 | **Negative control** | `runuser -u sandbox -- /opt/km/bin/sops --decrypt /etc/sandbox-secrets.enc.yaml` → AccessDenied, NOT plaintext |
| 6 | The agent still gets its key | dispatch a turn; the agent authenticates |
| 7 | Nothing leaked | `runuser -u sandbox -- env` and `/proc/<agent>/environ` carry no bundle key outside the agent process |
| 8 | **The fence survives resume** | `km stop <id>` then `km resume <id>`, then re-run 2, 4 and 5. This is what C2 exists for — if 2 passes before the stop and fails after, the unit is not enabled |
| 9 | Dormant profiles are untouched | create a `sopsFile` profile with no `fenceIMDS`; no `km-imds-fence` unit, no `~/.aws/config`, agents work |

Use `runuser`, never `sudo -u sandbox`, for every probe: `sudo -u sandbox curl`
skips `HTTPS_PROXY` and egresses directly, and `sudo` does not preserve the
cgroup ([[project_live_sandbox_probe_traps]]). Send SSM payloads base64-wrapped
through bash ([[project_ssm_send_command_runs_dash]]). Expect
`IncorrectSpotRequestState` on the first resume after a stop; retry ~90s later.

---

## Self-review

**Spec coverage.** §4.4 half one → Task 6. §4.4 half two (`km-creds`,
self-assume, session policy, the two Denies) → Tasks 2–5. §5 schema `fenceIMDS` →
Task 1. §7.1 assertion 6 → Task 7. §9 deploy surface → Global Constraints and
Task 8. §11 open questions → answered in Task 7 Step 5. §10.3 (flipping the
default) is explicitly a follow-on phase and stays out; the `*bool` in Task 1 is
what makes it cheap later.

**Deliberately out of scope, stated so nobody adds it silently:** the nat-table
DNAT rules' identical evaporate-on-resume behaviour under `enforcement: proxy`
(pre-existing, unrelated to secrets); the Wave 1 deferred minors carried in
`[[project_phase133_wave2_starting_context]]`; §10.1 proxy-side credential
substitution; §10.2 anomaly rules.

**Known limit, to be written down rather than designed around:** the fence
removes uid sandbox's *unmediated* path to the instance role. It does not make
the role unreachable — anything running as that uid can still invoke `km-creds`,
or speak the broker protocol directly. That is the same honest boundary `grants`
already has (§5.2, §6), and Task 8 Step 4 requires it in the operator docs
verbatim so nobody later reads the fence as containment.
