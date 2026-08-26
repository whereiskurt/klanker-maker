# Wiz Sensor on EC2 Sandboxes — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run the Wiz Runtime Sensor as a root systemd daemon on opted-in EC2 sandboxes, delivered as a composable profile fragment — and fix the `iam.allowedSecretPaths` IAM gap that currently makes any such credential delivery silently fail.

**Architecture:** Three layers, built bottom-up. (1) `pkg/profile` gains a `{{prefix}}` token for `iam.allowedSecretPaths` with fail-closed validation — requiring every path to be prefix-relative is what structurally prevents a profile from granting the sandbox role SSM access outside its own install's namespace. (2) `pkg/compiler` resolves the token at compile time and emits the resolved paths into `module_inputs`, where a new `ec2spot/v1.4.0` turns them into a scoped `ssm:GetParameter` grant. (3) A `profiles/base/security/wiz.yaml` fragment installs the sensor via Wiz's own installer and hardens it with a systemd drop-in.

**Tech Stack:** Go (stdlib only for this work), Terraform/Terragrunt (HCL), YAML SandboxProfiles, systemd.

**Spec:** `docs/superpowers/specs/2026-08-25-wiz-sensor-design.md`

## Global Constraints

- **Dormant by default.** A profile that sets no `{{prefix}}` path and does not extend the Wiz fragment MUST compile byte-identically to today. `secret_paths` is emitted into `module_inputs` only when non-empty; the golden fixtures depend on this.
- **Fail loud, never default.** When any `{{prefix}}` path is present and `KM_RESOURCE_PREFIX` is unset, compilation ERRORS. Do not fall back to `"km"` — on a non-default install that silently renders an IAM policy pointing at another install's namespace.
- **Every `allowedSecretPaths` entry MUST start with `{{prefix}}/`.** Absolute paths and unknown tokens are validation errors, not warnings. This is a security guard, not a style rule.
- **Module directories are immutable.** Do not edit `infra/modules/ec2spot/v1.3.0/`. Create `v1.4.0/` as a copy and change the pin.
- **Module version pins live in the per-substrate map**, `infra/templates/sandbox/terragrunt.hcl` `locals.substrate_module_versions` (REQ-125-SUBPIN) — not a shared literal.
- **Deploy surface for this work:** `make build` then `make build-lambdas` then `km init --dry-run=false`. NOT `--sidecars` (no sidecar binary changes; the new IAM statement needs a full terragrunt apply).
- **Test suite baseline:** 43 packages ok; `internal/app/cmd` has 5 pre-existing failures (`TestBootstrapSCP*`, `TestCluster*`) that fail on `no real AWS credentials (fast-fail seam)`. Any OTHER failure is a real regression.
- Run tests with an explicit timeout and check the command's own exit code — `go test ./... | tail` returns tail's exit code and masks a FAIL.

---

## File Structure

| File | Responsibility |
|---|---|
| `pkg/profile/secretpath.go` (new) | Owns the `{{prefix}}` token: the constant, validation, and interpolation. Single source of truth for the token's semantics. |
| `pkg/profile/secretpath_test.go` (new) | Token validation + interpolation tests. |
| `pkg/profile/validate.go` (modify) | Wires `ValidateSecretPaths` into `ValidateSemantic`. |
| `pkg/compiler/security.go` (modify) | `compileSecrets` resolves the token at compile time, failing loud. |
| `pkg/compiler/secretpath_test.go` (new) | Compile-time resolution tests, including the fail-loud path. |
| `pkg/compiler/service_hcl.go` (modify) | Emits `secret_paths` into ec2spot `module_inputs`, only when non-empty. |
| `pkg/compiler/service_hcl_secret_paths_test.go` (new) | Asserts emission and, critically, non-emission. |
| `infra/modules/ec2spot/v1.4.0/` (new) | Copy of v1.3.0 plus the `secret_paths` variable and its scoped IAM policy. |
| `infra/templates/sandbox/terragrunt.hcl` (modify) | Bumps the ec2spot pin to v1.4.0. |
| `profiles/base/security/wiz.yaml` (new) | The opt-in fragment: SSM paths, installer, systemd drop-in, `chattr +i`. |
| `profiles/wiz-demo.yaml` (new) | A leaf that extends the fragment, so `km validate` exercises the merged bytes. |
| `docs/wiz-sensor.md` (new) | Operator runbook: SSM parameter setup, deploy surface, threat model, troubleshooting. |

---

## Task 1: The `{{prefix}}` token — validation

**Files:**
- Create: `pkg/profile/secretpath.go`
- Create: `pkg/profile/secretpath_test.go`
- Modify: `pkg/profile/validate.go` (add call near line 554, beside `validateLimits(p)`)

**Interfaces:**
- Consumes: `SandboxProfile.Spec.IAM.AllowedSecretPaths []string`, `ValidationError{Path, Message, IsWarning}`
- Produces:
  - `const SecretPathPrefixToken = "{{prefix}}"`
  - `func ValidateSecretPaths(paths []string) []ValidationError`
  - (Task 2 adds `InterpolateSecretPaths` to the same file)

- [ ] **Step 1: Write the failing test**

Create `pkg/profile/secretpath_test.go`:

```go
package profile_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

func TestValidateSecretPaths(t *testing.T) {
	tests := []struct {
		name    string
		paths   []string
		wantErr bool
		wantSub string // substring expected in the message
	}{
		{
			name:    "prefix-relative path is accepted",
			paths:   []string{"{{prefix}}/wiz/wiz-api-client-id"},
			wantErr: false,
		},
		{
			name:    "multiple prefix-relative paths are accepted",
			paths:   []string{"{{prefix}}/wiz/wiz-api-client-id", "{{prefix}}/wiz/wiz-api-client-secret"},
			wantErr: false,
		},
		{
			name:    "empty list is accepted",
			paths:   nil,
			wantErr: false,
		},
		{
			name:    "absolute path is rejected",
			paths:   []string{"/km/wiz/wiz-api-client-id"},
			wantErr: true,
			wantSub: "must start with",
		},
		{
			name:    "path escaping the install namespace is rejected",
			paths:   []string{"/*"},
			wantErr: true,
			wantSub: "must start with",
		},
		{
			name:    "unknown token is rejected",
			paths:   []string{"{{prefix2}}/wiz/x"},
			wantErr: true,
			wantSub: "unknown token",
		},
		{
			name:    "token in a later segment is rejected",
			paths:   []string{"{{prefix}}/wiz/{{sandbox}}"},
			wantErr: true,
			wantSub: "unknown token",
		},
		{
			name:    "bare token with no trailing slash is rejected",
			paths:   []string{"{{prefix}}"},
			wantErr: true,
			wantSub: "must start with",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errs := profile.ValidateSecretPaths(tc.paths)
			if tc.wantErr && len(errs) == 0 {
				t.Fatalf("ValidateSecretPaths(%q) = no errors, want an error", tc.paths)
			}
			if !tc.wantErr && len(errs) != 0 {
				t.Fatalf("ValidateSecretPaths(%q) = %+v, want no errors", tc.paths, errs)
			}
			if tc.wantErr {
				if errs[0].IsWarning {
					t.Errorf("error must be blocking, not a warning: %+v", errs[0])
				}
				if !strings.Contains(errs[0].Message, tc.wantSub) {
					t.Errorf("message = %q, want substring %q", errs[0].Message, tc.wantSub)
				}
				if errs[0].Path != "spec.iam.allowedSecretPaths" {
					t.Errorf("Path = %q, want %q", errs[0].Path, "spec.iam.allowedSecretPaths")
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./pkg/profile/ -run TestValidateSecretPaths -v`

Expected: FAIL to compile — `undefined: profile.ValidateSecretPaths`

- [ ] **Step 3: Write the implementation**

Create `pkg/profile/secretpath.go`:

```go
package profile

import (
	"fmt"
	"regexp"
	"strings"
)

// SecretPathPrefixToken is the ONLY token recognised in
// spec.iam.allowedSecretPaths. It expands to "/" + the install's
// resource_prefix at compile time (see InterpolateSecretPaths).
const SecretPathPrefixToken = "{{prefix}}"

// secretPathTokenRe matches any {{...}} token so unknown ones can be rejected
// rather than silently passed through into an IAM policy.
var secretPathTokenRe = regexp.MustCompile(`\{\{[^}]*\}\}`)

// ValidateSecretPaths enforces that every allowedSecretPaths entry is
// prefix-relative.
//
// This is a SECURITY GUARD, not a style rule. These paths are compiled into the
// sandbox role's ssm:GetParameter grant. Requiring the {{prefix}} token as the
// leading segment makes it structurally impossible for a profile to grant the
// sandbox read access to SSM parameters outside its own install's namespace —
// an absolute path such as "/*" or "/other-install/..." cannot be expressed.
//
// Validation is shape-only: it deliberately does NOT need to know the prefix's
// value, so km validate works on a workstation with no configured install.
func ValidateSecretPaths(paths []string) []ValidationError {
	var errs []ValidationError

	for _, p := range paths {
		// Reject any token that is not exactly SecretPathPrefixToken, wherever
		// it appears. Checked first so "{{prefix2}}/x" reports the precise
		// cause rather than the generic leading-segment error.
		for _, tok := range secretPathTokenRe.FindAllString(p, -1) {
			if tok != SecretPathPrefixToken {
				errs = append(errs, ValidationError{
					Path: "spec.iam.allowedSecretPaths",
					Message: fmt.Sprintf(
						"%q contains unknown token %q — the only supported token is %q",
						p, tok, SecretPathPrefixToken),
				})
			}
		}

		if !strings.HasPrefix(p, SecretPathPrefixToken+"/") {
			errs = append(errs, ValidationError{
				Path: "spec.iam.allowedSecretPaths",
				Message: fmt.Sprintf(
					"%q must start with %q/ — paths are compiled into the sandbox role's "+
						"ssm:GetParameter grant, and requiring the token keeps that grant "+
						"inside this install's own namespace",
					p, SecretPathPrefixToken),
			})
		}
	}

	return errs
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./pkg/profile/ -run TestValidateSecretPaths -v`

Expected: PASS, all 8 subtests.

- [ ] **Step 5: Wire it into ValidateSemantic**

In `pkg/profile/validate.go`, find the line `errs = append(errs, validateLimits(p)...)` (near line 554) and add immediately after it:

```go
	// Wiz sensor phase: allowedSecretPaths entries are compiled into the
	// sandbox role's ssm:GetParameter grant, so the prefix-relative rule is a
	// security guard — see pkg/profile/secretpath.go.
	errs = append(errs, ValidateSecretPaths(p.Spec.IAM.AllowedSecretPaths)...)
```

- [ ] **Step 6: Verify no existing profile regresses**

Run: `go test ./pkg/profile/ -timeout 300s`

Expected: PASS.

Then confirm no shipped profile already uses an absolute secret path:

Run: `grep -rn "allowedSecretPaths" -A4 profiles/ || echo "none declared"`

Expected: either no matches, or only entries you are about to convert. If a shipped profile declares an absolute path, STOP — converting it is a behaviour change that needs its own review, and it means the field was in use despite never being granted.

- [ ] **Step 7: Run the full profile validation gate**

Run: `bash scripts/validate-all-profiles.sh`

Expected: exit 0.

- [ ] **Step 8: Commit**

```bash
git add pkg/profile/secretpath.go pkg/profile/secretpath_test.go pkg/profile/validate.go
git commit -m "feat(profile): require allowedSecretPaths to be prefix-relative"
```

---

## Task 2: Compile-time interpolation, fail-loud

**Files:**
- Modify: `pkg/profile/secretpath.go` (add `InterpolateSecretPaths`)
- Modify: `pkg/profile/secretpath_test.go` (add interpolation tests)
- Modify: `pkg/compiler/security.go` (`compileSecrets`, lines 65-77)
- Modify: `pkg/compiler/compiler.go` (call sites at lines 109 and 230)
- Modify: `pkg/compiler/compose.go` (call site at line 48)
- Create: `pkg/compiler/secretpath_test.go`

**Interfaces:**
- Consumes: `profile.SecretPathPrefixToken` (Task 1)
- Produces:
  - `func profile.InterpolateSecretPaths(paths []string, resourcePrefix string) ([]string, error)`
  - `compileSecrets(p *profile.SandboxProfile) ([]string, error)` — **signature change**, was `[]string`

- [ ] **Step 1: Write the failing interpolation test**

Append to `pkg/profile/secretpath_test.go`:

```go
func TestInterpolateSecretPaths(t *testing.T) {
	t.Run("token expands to a leading-slash prefix segment", func(t *testing.T) {
		got, err := profile.InterpolateSecretPaths(
			[]string{"{{prefix}}/wiz/wiz-api-client-id"}, "km")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "/km/wiz/wiz-api-client-id"
		if len(got) != 1 || got[0] != want {
			t.Errorf("got %q, want [%q]", got, want)
		}
	})

	t.Run("non-default prefix", func(t *testing.T) {
		got, err := profile.InterpolateSecretPaths(
			[]string{"{{prefix}}/wiz/wiz-api-client-secret"}, "km2")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "/km2/wiz/wiz-api-client-secret"
		if got[0] != want {
			t.Errorf("got %q, want %q", got[0], want)
		}
	})

	t.Run("empty prefix with a token present is an ERROR, never a default", func(t *testing.T) {
		_, err := profile.InterpolateSecretPaths(
			[]string{"{{prefix}}/wiz/wiz-api-client-id"}, "")
		if err == nil {
			t.Fatal("expected an error when resourcePrefix is empty, got nil — " +
				"defaulting would render an IAM policy for the wrong install")
		}
		if !strings.Contains(err.Error(), "KM_RESOURCE_PREFIX") {
			t.Errorf("error should name the env var, got: %v", err)
		}
	})

	t.Run("empty path list with empty prefix is fine", func(t *testing.T) {
		got, err := profile.InterpolateSecretPaths(nil, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %q, want empty", got)
		}
	})
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/profile/ -run TestInterpolateSecretPaths -v`

Expected: FAIL to compile — `undefined: profile.InterpolateSecretPaths`

- [ ] **Step 3: Implement InterpolateSecretPaths**

Append to `pkg/profile/secretpath.go`:

```go
// InterpolateSecretPaths expands SecretPathPrefixToken to "/" + resourcePrefix.
//
// "{{prefix}}/wiz/wiz-api-client-id" with prefix "km" becomes
// "/km/wiz/wiz-api-client-id".
//
// FAIL LOUD: when any path carries the token and resourcePrefix is empty, this
// returns an error rather than defaulting. Other call sites in this codebase
// default an empty KM_RESOURCE_PREFIX to "km"; doing that here would silently
// render an IAM policy pointing at a DIFFERENT install's SSM namespace on any
// non-default install. An error at compile time is the only safe outcome.
func InterpolateSecretPaths(paths []string, resourcePrefix string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !strings.Contains(p, SecretPathPrefixToken) {
			// Should be unreachable: ValidateSecretPaths rejects these. Kept as
			// a defence in depth so an un-validated path can never reach IAM.
			return nil, fmt.Errorf(
				"allowedSecretPaths entry %q does not start with %s — refusing to compile",
				p, SecretPathPrefixToken)
		}
		if resourcePrefix == "" {
			return nil, fmt.Errorf(
				"allowedSecretPaths entry %q needs the install's resource prefix, but "+
					"KM_RESOURCE_PREFIX is unset — refusing to default to \"km\", which "+
					"would grant the sandbox role access to another install's SSM namespace",
				p)
		}
		out = append(out, strings.Replace(p, SecretPathPrefixToken, "/"+resourcePrefix, 1))
	}
	return out, nil
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./pkg/profile/ -run TestInterpolateSecretPaths -v`

Expected: PASS, all 4 subtests.

- [ ] **Step 5: Change the compiler chokepoint**

In `pkg/compiler/security.go`, replace the whole `compileSecrets` function (lines 65-77) with:

```go
// compileSecrets builds the list of SSM parameter paths to inject at boot.
// It reads iam.allowedSecretPaths from the profile and expands the {{prefix}}
// token against KM_RESOURCE_PREFIX.
//
// Note: The GitHub token is NOT injected via SecretPaths — it is stored per-sandbox
// at /sandbox/{sandbox-id}/github-token and read at git-operation time by the
// GIT_ASKPASS credential helper script installed in section 4 of userdata.go.
func compileSecrets(p *profile.SandboxProfile) ([]string, error) {
	return profile.InterpolateSecretPaths(
		p.Spec.IAM.AllowedSecretPaths,
		os.Getenv("KM_RESOURCE_PREFIX"),
	)
}
```

Add `"os"` to that file's imports if it is not already present.

- [ ] **Step 6: Update the three call sites**

`pkg/compiler/compiler.go` line ~109 and ~230 — replace `secretPaths := compileSecrets(p)` with:

```go
	secretPaths, err := compileSecrets(p)
	if err != nil {
		return nil, fmt.Errorf("compile secrets: %w", err)
	}
```

`pkg/compiler/compose.go` line ~48 — same replacement. If the enclosing function's return signature cannot carry an error, propagate it to the nearest one that can rather than swallowing it; do NOT log-and-continue. A swallowed error here reproduces the exact failure mode this whole task exists to remove.

- [ ] **Step 7: Write the compiler-level test**

Create `pkg/compiler/secretpath_test.go`:

```go
package compiler_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/compiler"
)

// TestCompile_SecretPathsFailLoud proves a {{prefix}} path with no
// KM_RESOURCE_PREFIX aborts the compile instead of defaulting to "km".
func TestCompile_SecretPathsFailLoud(t *testing.T) {
	t.Setenv("KM_RESOURCE_PREFIX", "")

	p := minimalProfile(t)
	p.Spec.IAM.AllowedSecretPaths = []string{"{{prefix}}/wiz/wiz-api-client-id"}

	_, err := compiler.Compile(p)
	if err == nil {
		t.Fatal("Compile() succeeded with an unset KM_RESOURCE_PREFIX; " +
			"it must fail rather than render IAM for the wrong install")
	}
	if !strings.Contains(err.Error(), "KM_RESOURCE_PREFIX") {
		t.Errorf("error should name the env var, got: %v", err)
	}
}

// TestCompile_SecretPathsResolve proves the token expands against the env var.
func TestCompile_SecretPathsResolve(t *testing.T) {
	t.Setenv("KM_RESOURCE_PREFIX", "km2")

	p := minimalProfile(t)
	p.Spec.IAM.AllowedSecretPaths = []string{"{{prefix}}/wiz/wiz-api-client-id"}

	got, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	want := "/km2/wiz/wiz-api-client-id"
	found := false
	for _, sp := range got.SecretPaths {
		if sp == want {
			found = true
		}
	}
	if !found {
		t.Errorf("SecretPaths = %q, want to contain %q", got.SecretPaths, want)
	}
}
```

**Note for the implementer:** `minimalProfile(t)` is a helper you must locate or write. Look first in `pkg/compiler/compiler_secrets_test.go` and `pkg/compiler/userdata_secrets_test.go` for an existing minimal-profile builder and reuse it verbatim. If none is reusable, add one to your new test file returning the smallest `*profile.SandboxProfile` that `Compile` accepts — copy the field set from the existing tests rather than inventing one.

- [ ] **Step 8: Run the compiler tests**

Run: `go test ./pkg/compiler/ -run 'TestCompile_SecretPaths' -v`

Expected: PASS both.

- [ ] **Step 9: Run the full suite and compare against baseline**

Run: `go test ./... -timeout 900s > /tmp/t2.txt 2>&1; echo "EXIT=$?"; grep -E '^--- FAIL' /tmp/t2.txt`

Expected: only the 5 baseline `internal/app/cmd` failures. Any other FAIL is a regression from this task — fix it before committing.

- [ ] **Step 10: Commit**

```bash
git add pkg/profile/secretpath.go pkg/profile/secretpath_test.go pkg/compiler/security.go pkg/compiler/compiler.go pkg/compiler/compose.go pkg/compiler/secretpath_test.go
git commit -m "feat(compiler): expand the prefix token in allowedSecretPaths, failing loud"
```

---

## Task 3: `ec2spot/v1.4.0` — the IAM grant

**Files:**
- Create: `infra/modules/ec2spot/v1.4.0/` (copy of `v1.3.0/`: `main.tf`, `variables.tf`, `outputs.tf`)
- Modify: `infra/templates/sandbox/terragrunt.hcl` (`locals.substrate_module_versions`, line ~29)

**Interfaces:**
- Consumes: `module_inputs.secret_paths` (a `list(string)` of already-resolved absolute SSM paths, produced by Task 4)
- Produces: an `aws_iam_role_policy` granting `ssm:GetParameter` on exactly those paths

- [ ] **Step 1: Create the new module version**

```bash
cp -R infra/modules/ec2spot/v1.3.0 infra/modules/ec2spot/v1.4.0
ls infra/modules/ec2spot/v1.4.0
```

Expected: `main.tf`, `outputs.tf`, `variables.tf`.

Do NOT edit `v1.3.0` — module directories are immutable in this repo.

- [ ] **Step 2: Add the variable**

Append to `infra/modules/ec2spot/v1.4.0/variables.tf`:

```hcl
# ---------------------------------------------------------------------------
# Profile-declared SSM secret paths (spec.iam.allowedSecretPaths)
# ---------------------------------------------------------------------------
# Additive with an empty default: an unset list is byte-identical to v1.3.0.
#
# Paths arrive ALREADY RESOLVED and absolute (e.g. "/km/wiz/wiz-api-client-id").
# The prefix token is expanded in the Go compiler, which fails loudly when the
# install's resource prefix is unknown — so a path reaching this module is
# always inside this install's own namespace.
#
# Before v1.4.0 this profile field rendered an SSM fetch loop into user-data but
# was never plumbed into the role, so every fetch returned AccessDenied and the
# loop warned and continued. This variable closes that gap.
variable "secret_paths" {
  type        = list(string)
  description = "Absolute SSM parameter paths the sandbox role may read at boot, from spec.iam.allowedSecretPaths. Empty = grant nothing (default)."
  default     = []
}
```

- [ ] **Step 3: Add the IAM policy**

Append to `infra/modules/ec2spot/v1.4.0/main.tf`:

```hcl
# ---------------------------------------------------------------------------
# Profile-declared secret paths -> scoped ssm:GetParameter
# ---------------------------------------------------------------------------
# A separate role policy rather than another statement in the existing document,
# so the zero-path case creates no resource at all and the existing policy stays
# byte-identical.
#
# Resource ARNs are exact paths, not wildcards: an SSM parameter "/km/wiz/x" is
# addressed as "...:parameter/km/wiz/x", so the leading slash of the path
# supplies the separator after "parameter".
resource "aws_iam_role_policy" "ec2spot_profile_secret_paths" {
  count = (local.create_instance_role && length(var.secret_paths) > 0) ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-profile-secret-paths"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SSMReadProfileSecretPaths"
        Effect = "Allow"
        Action = ["ssm:GetParameter"]
        Resource = [
          for p in var.secret_paths :
          "arn:aws:ssm:*:${data.aws_caller_identity.current.account_id}:parameter${p}"
        ]
      }
    ]
  })
}
```

- [ ] **Step 4: Verify the KMS decrypt grant covers SecureString reads**

`ssm:GetParameter --with-decryption` on a SecureString ALSO needs `kms:Decrypt` on the key that encrypted it. The module already has a KMS statement guarded by `kms:ViaService = ssm.<region>.amazonaws.com` (near `main.tf:415`) — but confirm it covers the key these parameters will use rather than assuming it.

Run: `grep -n -B8 -A12 'kms:ViaService' infra/modules/ec2spot/v1.4.0/main.tf`

Check the statement's `Resource` list. Three possible outcomes:

- **Resource is `"*"` (or the account's SSM-service-scoped key set)** — covered. The `ViaService` condition already constrains it to SSM. No change needed; note this in the commit message.
- **Resource names a specific key ARN** (e.g. the sandbox-secrets CMK) — then a parameter encrypted with the SSM *default* key (`alias/aws/ssm`) will fail to decrypt. Either document in `docs/wiz-sensor.md` that operators MUST encrypt these parameters with that named key (`aws ssm put-parameter --key-id <arn>`), or widen the statement. Prefer documenting — widening a KMS grant deserves its own review.
- **No such statement reaches the sandbox role at all** — then SecureString reads cannot work; STOP and raise it, because it invalidates the credential-delivery design and the spec needs updating before you build on it.

Record which outcome you hit; it determines a line in the operator doc (Task 5, Step 5, item 2).

- [ ] **Step 5: Validate the HCL**

```bash
cd infra/modules/ec2spot/v1.4.0 && terraform init -backend=false && terraform validate
```

Expected: `Success! The configuration is valid.`

Then return to the worktree root before continuing.

- [ ] **Step 6: Bump the pin**

In `infra/templates/sandbox/terragrunt.hcl`, inside `locals.substrate_module_versions` (line ~29), change `ec2spot = "v1.3.0"` to `ec2spot = "v1.4.0"`.

Change ONLY the `ec2spot` entry. Other substrates keep their own pins — that map is per-substrate for exactly this reason (REQ-125-SUBPIN).

- [ ] **Step 7: Confirm no stale module lock files**

Run: `find infra/modules -name '.terraform.lock.hcl' -o -name '.terraform' -type d`

Expected: no output. If `terraform init` in Step 5 left artifacts inside `v1.4.0/`, delete them — stray locks under module source dirs get copied into the terragrunt cache and cause provider-checksum drift:

```bash
find infra/modules/ec2spot/v1.4.0 -name '.terraform.lock.hcl' -delete
find infra/modules/ec2spot/v1.4.0 -name '.terraform' -type d -exec rm -rf {} +
```

- [ ] **Step 8: Commit**

```bash
git add infra/modules/ec2spot/v1.4.0 infra/templates/sandbox/terragrunt.hcl
git commit -m "feat(ec2spot): v1.4.0 grants ssm:GetParameter on profile secret paths"
```

---

## Task 4: Emit `secret_paths` into `module_inputs`

**Files:**
- Modify: `pkg/compiler/service_hcl.go` (params struct near line 609; ec2spot template `module_inputs` block near line 113; params assembly near line 955)
- Create: `pkg/compiler/service_hcl_secret_paths_test.go`

**Interfaces:**
- Consumes: `compileSecrets` output (Task 2) — resolved absolute paths
- Produces: `secret_paths = ["/km/wiz/..."]` inside ec2spot `module_inputs`, **only when non-empty**

- [ ] **Step 1: Write the failing test**

Create `pkg/compiler/service_hcl_secret_paths_test.go`:

```go
package compiler_test

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/compiler"
)

// TestServiceHCL_SecretPathsEmitted asserts resolved paths reach module_inputs.
func TestServiceHCL_SecretPathsEmitted(t *testing.T) {
	t.Setenv("KM_RESOURCE_PREFIX", "km")

	p := minimalProfile(t)
	p.Spec.IAM.AllowedSecretPaths = []string{
		"{{prefix}}/wiz/wiz-api-client-id",
		"{{prefix}}/wiz/wiz-api-client-secret",
	}

	got, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	want := `secret_paths = ["/km/wiz/wiz-api-client-id", "/km/wiz/wiz-api-client-secret"]`
	if !strings.Contains(got.ServiceHCL, want) {
		t.Errorf("service.hcl missing %q\n--- got ---\n%s", want, got.ServiceHCL)
	}
}

// TestServiceHCL_SecretPathsAbsentWhenUnset is the byte-identity guard. A
// profile that declares no secret paths must render NO secret_paths line, or
// every frozen golden fixture churns.
func TestServiceHCL_SecretPathsAbsentWhenUnset(t *testing.T) {
	t.Setenv("KM_RESOURCE_PREFIX", "km")

	p := minimalProfile(t)
	p.Spec.IAM.AllowedSecretPaths = nil

	got, err := compiler.Compile(p)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if strings.Contains(got.ServiceHCL, "secret_paths") {
		t.Errorf("service.hcl contains secret_paths for a profile that declares none; "+
			"emission must be conditional\n--- got ---\n%s", got.ServiceHCL)
	}
}
```

**Note for the implementer:** the field holding rendered service.hcl on the `Compile` result may not be named `ServiceHCL`. Check the `CompiledSandbox` struct in `pkg/compiler/compiler.go` and use the real field name. Reuse the same `minimalProfile(t)` helper from Task 2.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/compiler/ -run 'TestServiceHCL_SecretPaths' -v`

Expected: `TestServiceHCL_SecretPathsEmitted` FAILs (no `secret_paths` in output). `TestServiceHCL_SecretPathsAbsentWhenUnset` should already PASS — it guards a property you must not break.

- [ ] **Step 3: Add the params field**

In `pkg/compiler/service_hcl.go`, in the params struct near line 609 (the one holding `KMSecretPaths string`), add:

```go
	// SecretPaths are resolved absolute SSM paths from spec.iam.allowedSecretPaths,
	// emitted into ec2spot module_inputs ONLY when non-empty so a profile that
	// declares none renders byte-identically to pre-v1.4.0.
	SecretPaths []string
```

- [ ] **Step 4: Emit it in the template**

In the ec2spot `module_inputs` block (near line 113), immediately after the `iam_session_policy` block, add:

```
{{- if .SecretPaths }}

    # spec.iam.allowedSecretPaths, prefix token already expanded. Grants the
    # sandbox role ssm:GetParameter on exactly these paths (ec2spot v1.4.0).
    secret_paths = [{{ joinStrings .SecretPaths }}]
{{- end }}
```

`joinStrings` is the existing helper at line ~693; it quotes each element and joins with `", "`, producing `["/km/wiz/a", "/km/wiz/b"]`.

- [ ] **Step 5: Populate the field**

In the ec2spot params assembly (the literal near line 955 that sets `AdditionalVolumeDeviceName`), add:

```go
		SecretPaths: secretPaths,
```

`secretPaths` is the local already produced by `compileSecrets` in the enclosing compile function. If it is not in scope at this point, thread it in from the caller rather than calling `compileSecrets` a second time — one resolution per compile.

- [ ] **Step 6: Run the tests**

Run: `go test ./pkg/compiler/ -run 'TestServiceHCL_SecretPaths' -v`

Expected: both PASS.

- [ ] **Step 7: Run the full suite**

Run: `go test ./... -timeout 900s > /tmp/t4.txt 2>&1; echo "EXIT=$?"; grep -E '^--- FAIL' /tmp/t4.txt`

Expected: only the 5 baseline failures. **If a golden fixture test fails here, the conditional emission is broken** — a profile with no secret paths is rendering a `secret_paths` line. Fix the `{{- if .SecretPaths }}` guard rather than re-capturing the golden.

- [ ] **Step 8: Commit**

```bash
git add pkg/compiler/service_hcl.go pkg/compiler/service_hcl_secret_paths_test.go
git commit -m "feat(compiler): emit secret_paths into ec2spot module_inputs"
```

---

## Task 5: The Wiz fragment, demo leaf, and operator doc

**Files:**
- Create: `profiles/base/security/wiz.yaml`
- Create: `profiles/wiz-demo.yaml`
- Create: `docs/wiz-sensor.md`
- Modify: `CLAUDE.md` (add a "Where to look" row)

**Interfaces:**
- Consumes: the prefix token (Task 1), interpolation (Task 2), IAM grant (Task 3), emission (Task 4)
- Produces: an abstract fragment other profiles reach via `extends:`

**Before starting:** the spec's §10 lists two blocking unknowns — the tenant's `WIZ_DOMAIN`/region, and the connector's inventory sync cadence. Neither blocks this task. Leave `WIZ_SENSOR_VERSION` as a documented `REPLACE-ME` the operator fills in, and say so in `docs/wiz-sensor.md`. Do NOT invent a tenant domain.

- [ ] **Step 1: Write the fragment**

Create `profiles/base/security/wiz.yaml`:

```yaml
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: base-security-wiz
  abstract: true

# Wiz Runtime Sensor — opt-in runtime visibility for EC2 sandboxes.
#
# Deliberately absent:
#   spec.network  — root traffic bypasses both the iptables DNAT (userdata.go
#                   uid-0 RETURN) and the eBPF cgroup, and the installer honours
#                   its own WIZ_HTTP_PROXY_URL rather than HTTPS_PROXY. No
#                   allowlist entry, no CA trust, no cert-pinning risk.
#   spec.secrets  — sopsFile is a SCALAR: setting it here would clobber any leaf
#                   that declares its own bundle. Its output also lands in
#                   /etc/sandbox-secrets.env (0440 root:sandbox), readable by the
#                   very agent being monitored.
#   spec.runtime  — Phase 117 bool zero-value trap; mixed-bool blocks stay in leaves.
#
# NOT tamper-proof. On spec.execution.privileged: true the agent has sudo and can
# stop, mask, or unpick any of this. The real control is privileged: false; see
# docs/wiz-sensor.md.
spec:
  iam:
    # A list, so it unions cleanly with a leaf's own entries under Phase 117 merge.
    # Names are chosen so userdata's basename|upper|'-'->'_' derivation yields
    # exactly WIZ_API_CLIENT_ID / WIZ_API_CLIENT_SECRET — the names the Wiz
    # installer reads. No glue code between the SSM fetch and the install.
    allowedSecretPaths:
      - "{{prefix}}/wiz/wiz-api-client-id"
      - "{{prefix}}/wiz/wiz-api-client-secret"

  execution:
    # Appends after the merged initCommands rather than replacing them.
    initCommandsAppend:
      # Pin the version. The installer defaults to latest; in a fleet that
      # recreates constantly, unpinned means a bad upstream release reaches every
      # new sandbox at once. Bump this like any other profile change.
      - "curl -fsSL https://downloads.wiz.io/sensor/sensor_install.sh -o /tmp/wiz_install.sh"
      - "WIZ_SENSOR_VERSION=REPLACE-ME sh /tmp/wiz_install.sh && rm -f /tmp/wiz_install.sh"
      # The installer creates and enables wiz-sensor.service itself, so km adds
      # only a drop-in. A drop-in also survives a package upgrade, which a
      # replaced unit file would not.
      - "install -d -m 0755 /etc/systemd/system/wiz-sensor.service.d"
      - "printf '[Service]\\nRestart=always\\nRestartSec=5\\nStartLimitIntervalSec=0\\n' > /etc/systemd/system/wiz-sensor.service.d/10-km-hardening.conf"
      - "systemctl daemon-reload && systemctl restart wiz-sensor || true"
      # Immutable so the hardening cannot be edited or deleted. Root can clear
      # +i; on an unprivileged sandbox it is a kernel guarantee.
      - "chattr +i /etc/systemd/system/wiz-sensor.service.d/10-km-hardening.conf || true"
```

- [ ] **Step 2: Write the demo leaf**

Create `profiles/wiz-demo.yaml`:

```yaml
apiVersion: klankermaker.ai/v1alpha2
kind: SandboxProfile
metadata:
  name: wiz-demo

extends:
  - base/platform
  - base/os/debian
  - base/network/locked
  - base/security/wiz

spec:
  # privileged stays FALSE — this is the configuration in which the sensor's
  # tamper resistance is a real guarantee rather than a speed bump.
  execution:
    privileged: false

  runtime:
    instanceType: t3.medium
    region: us-east-1

  lifecycle:
    ttl: "2h"
    idleTimeout: "20m"
```

**Note for the implementer:** the `extends:` entries must match real fragment paths. Run `ls profiles/base profiles/base/os profiles/base/network` and correct the list to the actual names before validating. Do not invent fragment names. If a leaf needs additional required fields to validate, copy them from an existing lean leaf such as `profiles/github.yaml` rather than guessing.

- [ ] **Step 3: Validate the merged profile**

Run: `go run ./cmd/km validate profiles/wiz-demo.yaml`

Expected: valid. `km validate` resolves the whole `extends:` DAG and validates the MERGED bytes, so this exercises the fragment's prefix-token paths through Task 1's validator.

Run: `go run ./cmd/km validate profiles/base/security/wiz.yaml`

Expected: exit 0 with a SKIP message — abstract fragments are skipped.

- [ ] **Step 4: Run the profile gate**

Run: `bash scripts/validate-all-profiles.sh`

Expected: exit 0. The script excludes `profiles/base/` automatically but WILL pick up `profiles/wiz-demo.yaml`, so if the inventory count is asserted anywhere, update it.

- [ ] **Step 5: Write the operator doc**

Create `docs/wiz-sensor.md` covering, in this order:

1. **What it is** — opt-in runtime sensor, `extends: base/security/wiz`.
2. **Prerequisite: create the two SSM parameters.** Give exact commands, noting the prefix must match the install's `resource_prefix`:
   ```bash
   PREFIX=$(yq -r .resource_prefix km-config.yaml)
   aws ssm put-parameter --type SecureString \
     --name "/$PREFIX/wiz/wiz-api-client-id"     --value "<SENSOR service-account client id>"
   aws ssm put-parameter --type SecureString \
     --name "/$PREFIX/wiz/wiz-api-client-secret" --value "<SENSOR service-account client secret>"
   ```
   State explicitly: use a **`SENSOR`**-type Wiz service account, not a general API client.
3. **Pin `WIZ_SENSOR_VERSION`** — replace `REPLACE-ME` in the fragment; explain why unpinned is unacceptable in a fleet that recreates constantly.
4. **Deploy surface** — `make build`, then `make build-lambdas`, then `km init --dry-run=false`. NOT `--sidecars`. Existing sandboxes need `km destroy && km create`.
5. **Threat model** — reproduce §6 of the spec faithfully, including "the real control is `privileged: false`" and that detection belongs off-box in the Wiz tenant.
6. **Correlation** — km already tags `km:sandbox-id`; the connector ingests tags; the gap is sandboxes shorter-lived than the sync cadence.
7. **Troubleshooting** — `systemctl status wiz-sensor`; `systemctl show wiz-sensor -p Restart` should print `always`; if the sensor never enrolls, check cloud-init output for `WARNING: failed to fetch secret`, which means the SSM parameter is missing or the IAM grant did not land (confirm the sandbox was created AFTER the `km init` that applied ec2spot v1.4.0).
8. **Open items** — the two §10 blocking questions, so operators know what is unsettled.

- [ ] **Step 6: Add the CLAUDE.md pointer**

In `CLAUDE.md`, in the "Where to look" table, add a row:

```markdown
| Wiz Runtime Sensor on a sandbox — opt-in `base/security/wiz` fragment, the prefix-relative SSM-path rule, why it is not tamper-proof, deploy surface | `docs/wiz-sensor.md` |
```

- [ ] **Step 7: Full suite, final**

Run: `go test ./... -timeout 900s > /tmp/t5.txt 2>&1; echo "EXIT=$?"; grep -E '^--- FAIL' /tmp/t5.txt`

Expected: only the 5 baseline failures.

- [ ] **Step 8: Commit**

```bash
git add profiles/base/security/wiz.yaml profiles/wiz-demo.yaml docs/wiz-sensor.md CLAUDE.md
git commit -m "feat(profiles): base/security/wiz — opt-in Wiz Runtime Sensor fragment"
```

---

## Verification before opening a PR

- [ ] `go test ./... -timeout 900s` — only the 5 baseline `internal/app/cmd` failures
- [ ] `bash scripts/validate-all-profiles.sh` — exit 0
- [ ] `cd infra/modules/ec2spot/v1.4.0 && terraform init -backend=false && terraform validate` — valid
- [ ] `find infra/modules -name '.terraform.lock.hcl'` — no output
- [ ] `git diff --stat main` — no changes under `infra/modules/ec2spot/v1.3.0/`
- [ ] A profile with no `allowedSecretPaths` renders no `secret_paths` line (Task 4 Step 6 proves this)

**Live UAT is NOT covered by this plan.** The spec's §7 lists seven live checks — most importantly Wiz-sensor/km-eBPF coexistence, which cannot be settled from code. Run those on one sandbox after `km init --dry-run=false` and record the results before rolling the fragment out to real profiles.
