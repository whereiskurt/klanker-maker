---
phase: 126-cross-account-capacity-borrowing-launch-sandboxes-into-a-lin
plan: 07
subsystem: infra
tags: [aws, iam, s3-bucket-policy, ssm-secure-string, terragrunt, cross-account]

requires:
  - phase: 126-05
    provides: "km account add — the target-account half of enrollment; AccountLinkFragment, accountLinkUnitDir, GenerateAccountLinkHCL, LinkState{Bucket,LockTable,Key}Name helpers"
  - phase: 126-01
    provides: "launch_accounts config block, config.LaunchAccountConfig, Config.GetLaunchAccount(s)"
provides:
  - "km account register — writes the home-account half of a link: km-config.yaml entry, SSM SecureString external id, one read-only artifacts-bucket grant statement"
  - "km account list — lists configured links without exposing the secret"
  - "km account rm — idempotent home-side removal, optional target-account destroy (--target-aws-profile), optional shared-backend purge (--purge-backend)"
affects: [126-08, 126-09, 126-10, docs/superpowers/specs/2026-06-29-cross-account-gpu-launch-design.md]

tech-stack:
  added: []
  patterns:
    - "Home-account bucket-policy read-modify-write scoped by a deterministic, prefix-qualified Sid — same derivation used by both the upsert and the removal so a grant can never be orphaned by a naming mismatch"
    - "Idempotent-removal primitives (grant/parameter/bucket/table) that treat 'already gone' as success, so a retry after a partial failure never errors"

key-files:
  created:
    - internal/app/cmd/account_register.go
    - internal/app/cmd/account_register_test.go
  modified:
    - internal/app/cmd/account.go

key-decisions:
  - "Re-confirmed the plan's precondition: ensureArtifactsBucket (bootstrap.go) creates the artifacts bucket directly via the S3 API with no Terraform-owned bucket-policy resource, so a Go read-modify-write of its policy is safe from being reverted by a later km init apply. The mail bucket (infra/modules/ses/v2.0.0/main.tf aws_s3_bucket_policy.mail) is Terraform-owned and explicitly out of bounds for this pattern."
  - "The grant Sid is prefix-scoped (\"{resource_prefix}-account-link-{name}-read\") rather than a bare \"km-account-link-\" literal — multi-instance support means two installs can share one AWS account, so the Sid must not collide. Caught by pkg/hygiene's TestGoSourceNamesUseResourcePrefix during self-check; fixed before commit (Rule 1 auto-fix — see Deviations)."
  - "The generated grant is a SINGLE statement with Action: [s3:GetObject, s3:ListBucket] and Resource: [bucket ARN, bucket ARN + /*] — matches the plan's literal wording ('the grant statement... object-read plus bucket-list actions only') and keeps the removal/idempotency logic (keyed by one Sid) simple."
  - "km account rm's ordering follows the plan's <action> text literally: remove the grant, delete the parameter, remove the km-config.yaml entry — unconditionally, in that order — then destroy the target-account footprint only when --target-aws-profile was supplied. Because config removal is unconditional, the printed no-profile follow-up command is a SELF-CONTAINED raw terragrunt command against the (undeleted) unit directory rather than a second `km account rm --target-aws-profile` invocation, which would fail the name lookup once the config entry is gone."
  - "'Every removal step is idempotent' (Task 2 acceptance criteria) is proven at the primitive level (RemoveArtifactsReadGrant called twice; RunAccountRm tolerating an already-deleted parameter) rather than by invoking RunAccountRm twice with the SAME already-removed link name — that scenario is Test 8's territory (unknown link name is a clear error, not a silent success), which RunAccountRm still satisfies once its own config entry is gone."
  - "--purge-backend requires --target-aws-profile (added guard, not explicitly listed as a precondition in the plan's flag table but necessary since the backend lives in the target account) and refuses when ANY other configured link's StateBucket or LockTable string matches this link's — a direct field comparison against config.LaunchAccountConfig rather than re-deriving names, since StateBucketOverride/LockTableOverride at add-time can make the deterministic-name derivation diverge from what's actually stored."
  - "Explicit-flag registration (no --from-fragment) has no --az flag in the plan's flag list, so AvailabilityZones is left empty in that path; documented in code as a known gap — a link registered this way needs availability_zones filled in by hand before ResolveLaunchTarget's length-parity guard will accept it. --from-fragment (the primary path, always fully populated by km account add) is unaffected."

patterns-established:
  - "artifactsGrantSid(prefix, linkName) factored into one helper consumed by both the upsert and the removal call sites, preventing a Sid-derivation mismatch from silently orphaning a grant (T-126-38)."

requirements-completed: [REQ-126-REGISTER]

coverage:
  - id: D1
    description: "km account register writes the km-config.yaml launch_accounts entry, an SSM SecureString external id, and one read-only artifacts-bucket grant statement — idempotently, from either a km account add fragment or explicit flags"
    requirement: "REQ-126-REGISTER"
    verification:
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRegister_CarriesStateBackendFields"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRegister_FromFragment_WritesConfigEntry"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRegister_ExternalIDWrittenAsSecureString"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRegister_ArtifactsGrant_NoExistingPolicy"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRegister_ArtifactsGrant_PreservesExisting"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRegister_ArtifactsGrant_Idempotent"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRegister_ArtifactsGrant_NoWriteActions"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRegister_MissingFragmentAndFlags_Error"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRegister_RefusesSelfEnrollment"
        status: pass
    human_judgment: false
  - id: D2
    description: "km account list renders every configured link (name, account id, region, subnet count, results bucket, param path) without ever printing the external id"
    requirement: "REQ-126-REGISTER"
    verification:
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountList_RendersRows"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountList_NeverPrintsExternalID"
        status: pass
      - kind: manual_procedural
        ref: "km account list (empty km-config.yaml) — captured below"
        status: pass
    human_judgment: false
  - id: D3
    description: "km account rm removes the home-side footprint idempotently, optionally destroys the target-account footprint (regenerating a missing unit directory), and gates/refuses the shared-backend purge correctly"
    requirement: "REQ-126-REGISTER"
    verification:
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRm_RemovesOwnGrantOnly"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRm_DeletesParamAndConfigEntry"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRm_NoTargetProfile_PrintsFollowup"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRm_WithTargetProfile_InvokesDestroyOnce"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRm_RegeneratesMissingUnitDir"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRm_ConfirmationRequired_NoChangeWithoutYes"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRm_UnknownLink_Error"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRm_NoBackendDeleteByDefault"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRm_PurgeBackend_DeletesWhenUnshared"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRm_PurgeBackend_RefusesWhenShared"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRm_PurgeBackend_RequiresTargetProfile"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestRemoveArtifactsReadGrant_IdempotentAcrossCalls"
        status: pass
      - kind: unit
        ref: "internal/app/cmd/account_register_test.go#TestAccountRm_ParameterAlreadyGone_StillSucceeds"
        status: pass
      - kind: manual_procedural
        ref: "km account rm --help — captured below"
        status: pass
    human_judgment: false

duration: 65min
completed: 2026-08-22
status: complete
---

# Phase 126 Plan 07: km account register/list/rm Summary

**Home-account enrollment for cross-account capacity borrowing: `km account register` writes exactly three things (a km-config.yaml entry, an SSM SecureString external id, and one read-only artifacts-bucket grant statement); `km account list` and `km account rm` manage them without ever exposing the secret, with rm offering a safe default (home-only) and an explicit escalation path (--target-aws-profile, --purge-backend) for full teardown.**

## Performance

- **Duration:** ~65 min
- **Tasks:** 2 completed
- **Files modified:** 3 (2 created, 1 modified)

## Accomplishments

- `km account register` assembles the link record from a `km account add` fragment (primary path) or explicit flags (recovery path when the fragment is lost), validates the resolved home account never equals the link's own target account, writes the external id as an SSM SecureString (`Overwrite: true`), upserts a read-only grant statement on the home artifacts bucket, and persists `launch_accounts.<name>` into km-config.yaml — the raw secret never touches the config file, only its parameter path does.
- `km account list` renders every configured link as a tabwriter table (name, account id, region, subnet count, results bucket, external-id parameter *path*) and never the secret itself.
- `km account rm` removes the artifacts grant, the SSM parameter, and the config entry — all idempotently, all unconditionally — then either destroys the target-account footprint (when `--target-aws-profile` is supplied, regenerating the unit directory from the link record if it's missing) or prints a self-contained, pasteable `terragrunt destroy` command against the still-on-disk unit directory. `--purge-backend` additionally deletes the (versioned) state bucket and lock table, but only after confirming no other configured link still shares them — refusing and naming the conflicting link otherwise.
- Both the register and remove sides derive the artifacts-bucket grant's `Sid` from the same `artifactsGrantSid(prefix, linkName)` helper, so a naming mismatch can never orphan a grant.

## Task Commits

1. **Tasks 1 & 2 (combined — see Process Note below): km account register — config/parameter/grant; km account list and km account rm** - `81dd66f4` (feat)

**Plan metadata:** _pending — see State Updates below_

### Process note on commit granularity

The plan scopes both Task 1 (register) and Task 2 (list/rm) to the **identical** three-file set (`account.go`, `account_register.go`, `account_register_test.go`), and Task 2's implementation directly reuses Task 1's helpers (`artifactsGrantSid`, `ArtifactsPolicyS3API`/`RemoveArtifactsReadGrant`, `AccountSSMAPI`, `PersistLaunchAccountsConfig`). Given that shared-file, shared-helper coupling, the two tasks were implemented and verified together before the first commit rather than as two separate commits with an intermediate register-only state. Both tasks' full behavior lists (Test 0–7 for Task 1, Test 1–10 for Task 2) are covered by tests, and both `go test` invocations named in the plan's per-task `<verify>` blocks pass. This is a deviation from the "commit atomically per task" default, made because the plan's own scoping made a clean intermediate commit awkward to reconstruct without artificial risk; flagging explicitly rather than silently deviating.

## Files Created/Modified

- `internal/app/cmd/account_register.go` — new: artifacts-bucket grant read-modify-write (`ArtifactsPolicyS3API`, `UpsertArtifactsReadGrant`, `RemoveArtifactsReadGrant`, `artifactsGrantSid`), the external-id SSM parameter helpers (`AccountSSMAPI`, `putSecureParameter`, `deleteParameterIdempotent`), `PersistLaunchAccountsConfig`, `RunAccountRegister`, `RunAccountList`, `RunAccountRm`, the `--purge-backend` bucket/table purge helpers, unit-directory regeneration, and the three new Cobra subcommands.
- `internal/app/cmd/account_register_test.go` — new: 24 tests covering every behavior bullet in both tasks.
- `internal/app/cmd/account.go` — modified: `NewAccountCmd` now wires `register`/`list`/`rm` alongside the existing `add`; doc comment updated.

## Decisions Made

See `key-decisions` in the frontmatter above — duplicated here for scanning convenience:

1. Re-confirmed the artifacts bucket has no Terraform-owned policy (bootstrap.go's `ensureArtifactsBucket`; contrast the Terraform-owned mail bucket in `infra/modules/ses/v2.0.0/main.tf`) — this plan's precondition holds.
2. The grant `Sid` is prefix-scoped, not a bare `"km-"` literal (multi-instance safety; caught and fixed via the repo's `pkg/hygiene` hardcoded-prefix gate — see Deviations).
3. The generated grant is one statement with `Action: [s3:GetObject, s3:ListBucket]` and two resources (bucket ARN + bucket ARN `/*`) — matches "object-read plus bucket-list actions only" and keeps Sid-scoped idempotency simple.
4. `rm`'s ordering is literal: grant → parameter → config entry (always) → destroy (conditional on `--target-aws-profile`). The no-profile follow-up command is therefore a raw `terragrunt destroy --terragrunt-working-dir <unitDir>` invocation, not a second `km account rm` call (which would fail the name lookup once the config entry is gone).
5. "Every removal step is idempotent" is proven at the primitive level, not by calling `RunAccountRm` twice on the same already-removed link name (which is exactly Test 8's "unknown link is an error" case).
6. `--purge-backend` requires `--target-aws-profile` and refuses when another link's `StateBucket`/`LockTable` string matches this link's (direct field comparison, not re-derivation, since overrides can diverge from the deterministic names).
7. Explicit-flag registration has no `--az` flag (per the plan's flag list) — `AvailabilityZones` stays empty in that path; documented as a known gap in code, does not affect the primary `--from-fragment` path.

## Verified Precondition (plan-mandated re-confirmation)

Confirmed by reading `internal/app/cmd/bootstrap.go`'s `ensureArtifactsBucket`: it creates the artifacts bucket directly via `s3.CreateBucket` + `PutBucketVersioning`/`PutPublicAccessBlock`/`PutBucketEncryption` — no `aws_s3_bucket_policy` Terraform resource exists for it anywhere in `infra/modules/`. A Go read-modify-write of that bucket's policy is therefore safe from being silently reverted by a later `km init` apply. Contrast `infra/modules/ses/v2.0.0/main.tf`'s `aws_s3_bucket_policy.mail`, which IS Terraform-owned and carries an explicit "only ONE policy resource per bucket" comment — this file never touches the mail bucket.

## Generated Grant Statement — exact action strings

```json
{
  "Sid": "{resource_prefix}-account-link-{name}-read",
  "Effect": "Allow",
  "Principal": {"AWS": "<box_role_arn>"},
  "Action": ["s3:GetObject", "s3:ListBucket"],
  "Resource": ["arn:aws:s3:::{artifacts_bucket}", "arn:aws:s3:::{artifacts_bucket}/*"]
}
```

No write action (`PutObject`, `DeleteObject`, etc.) appears anywhere in the generated statement — asserted by `TestAccountRegister_ArtifactsGrant_NoWriteActions` against the statement's decoded `Action` list, not against source text.

## Test 5's follow-up command — verbatim

Printed by `km account rm <name>` when `--target-aws-profile` is **not** supplied:

```
The target account (<account-id>) still holds the launcher role, box role and network — finish teardown with that account's credentials:
  AWS_PROFILE=<target-account-profile> terragrunt destroy --terragrunt-non-interactive --terragrunt-working-dir <unitDir>
```

Confirmed pasteable: it is a complete, standalone shell command referencing the still-on-disk unit directory (home-side removal never deletes it) — no dependency on the (now-removed) km-config.yaml entry.

## --purge-backend refusal message — verbatim

When a second configured link still shares the same state bucket or lock table:

```
refusing --purge-backend: launch_accounts.<other-name> still shares state bucket "<bucket>" or lock table "<table>" with this link — remove <other-name> first, or omit --purge-backend to leave the shared backend intact
```

Verified by `TestAccountRm_PurgeBackend_RefusesWhenShared` (asserts the other link's name appears in the error) and `TestAccountRm_PurgeBackend_DeletesWhenUnshared`/`TestAccountRm_NoBackendDeleteByDefault` (neither the bucket nor the table is touched unless `--purge-backend` is explicitly passed and unshared).

## CLI Output Captured

```
$ km account list
(no launch account links configured)

$ km account rm --help
Remove a cross-account capacity-borrowing link

Usage:
  km account rm <name> [flags]

Flags:
      --aws-profile string          AWS profile for the HOME account's credentials (default "klanker-application")
  -h, --help                        help for rm
      --purge-backend               also delete the target-account state bucket and lock table (requires --target-aws-profile; refuses if another link still shares them)
      --target-aws-profile string   AWS profile for the TARGET account's credentials — when set, also destroys the target-account footprint
      --verbose                     stream terragrunt output
      --yes                         skip the confirmation prompt

$ km account register --help
Register a cross-account capacity-borrowing link into the home account

Usage:
  km account register <name> [flags]

Flags:
      --account-id string       target AWS account id (explicit-flags alternative to --from-fragment)
      --aws-profile string      AWS profile for the HOME account's credentials (default "klanker-application")
      --box-role-arn string     box role ARN (explicit-flags alternative to --from-fragment)
      --efs-id string           target-account EFS filesystem id (explicit-flags alternative to --from-fragment)
      --external-id string      STS ExternalId (explicit-flags alternative to --from-fragment)
      --from-fragment string    path to the link fragment written by km account add (default: ~/.km/account-links/<name>.link.yaml if present)
  -h, --help                    help for register
      --launcher-arn string     launcher role ARN (explicit-flags alternative to --from-fragment)
      --region string           target-account region (explicit-flags alternative to --from-fragment)
      --results-bucket string   target-account results bucket (explicit-flags alternative to --from-fragment)
      --sg string                security group id (explicit-flags alternative to --from-fragment)
      --subnet stringArray      target-account subnet id (repeatable; explicit-flags alternative to --from-fragment)
```

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Artifacts-grant Sid was not prefix-qualified (multi-instance collision risk)**
- **Found during:** Task 1, before commit — `go test ./...` surfaced `pkg/hygiene`'s `TestGoSourceNamesUseResourcePrefix` failure.
- **Issue:** `artifactsGrantSid` hard-coded the literal `"km-account-link-"` prefix. The project explicitly supports multiple installs (different `resource_prefix` values) inside one AWS account (see CLAUDE.md § Multi-instance support); a bare `"km-"` Sid would collide if two installs' links ever needed to coexist in the same policy document.
- **Fix:** `artifactsGrantSid` now takes `prefix` as its first argument and derives `"{prefix}-account-link-{name}-read"`; all three call sites pass `cfg.GetResourcePrefix()`.
- **Files modified:** `internal/app/cmd/account_register.go`.
- **Commit:** `81dd66f4` (folded into the single implementation commit — the hygiene-test failure was caught and fixed before that commit was made, so there is no separate fix commit).

Or: no other deviations — the plan's precondition, flag list, and behavior lists were implemented as specified; see "Process note on commit granularity" above for the one procedural (not behavioral) deviation.

## Known Stubs

None — every code path is fully wired (no hardcoded empty returns, no placeholder UI, no TODO markers left in shipped code). The one intentionally-incomplete surface (explicit-flag registration leaving `AvailabilityZones` empty, no `--az` flag in the plan's flag list) is documented in code as a scoped limitation of a secondary/recovery path, not a stub blocking the plan's primary (`--from-fragment`) flow.

## Threat Flags

None beyond the plan's own `<threat_model>` (T-126-35 through T-126-40), all of which are mitigated and test-covered as described above. No new network endpoint, auth path, or schema change was introduced beyond what the plan specified.

## Self-Check: PASSED
