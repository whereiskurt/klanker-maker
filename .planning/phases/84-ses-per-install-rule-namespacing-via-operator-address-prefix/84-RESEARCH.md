# Phase 84: SES per-install rule namespacing via operator address prefix — Research

**Researched:** 2026-05-16
**Domain:** AWS SES inbound receipt rules + Terraform module split (foundation vs regional) + Go Lambda env-var routing + km configure derivation
**Confidence:** HIGH (codebase reads are exact); MEDIUM on AWS SES "all rules execute in order" claim (verified via official AWS dev guide).

## Summary

Phase 84 hard-removes Phase 82.1's `activate_rule_set` opt-out mechanism and replaces it with an **address-namespacing** model. The SES receipt rule set is promoted from a per-install regional resource (`infra/modules/ses/v1.0.0/`) to an account-shared foundation resource (`infra/modules/ses-shared-rule-set/v1.0.0/`). Each `km` install stops owning the rule set and instead writes two `aws_ses_receipt_rule` resources (`${prefix}-operator-inbound`, `${prefix}-sandbox-catchall`) into the always-active shared rule set. The operator inbox shifts from `operator@sandboxes.${domain}` to `operator-${resource_prefix}@sandboxes.${domain}`, derived in `km configure` and consumed by both `km-send` (default `--to`) and the `email-create-handler` Lambda (recipient-match routing).

The research validates the architectural premise (AWS SES does execute all matching rules in declared order; recipient lists support full addresses and domain names but NOT wildcards). It surfaces three load-bearing constraints the planner must thread:

1. **The current regional `ses/` module also owns the `aws_ses_domain_identity`, DKIM records, MX record, and consolidated S3 bucket policy.** Phase 84 must decide which of those resources stay regional and which (if any) move to foundation. The CONTEXT.md decisions imply only the rule set + active-rule-set pointer move; the domain identity + DNS records remain regional — but this only works if installs use distinct `email_subdomain` values OR share a domain via a yet-to-design pattern. **OPEN QUESTION raised below.**
2. **SES recipient matching has no wildcard syntax in classic receipt rules.** A `${prefix}-sandbox-catchall` rule that uses the domain name `sandboxes.${domain}` as its sole recipient matches **all** addresses on that domain — but that creates ambiguity between installs sharing one domain because every install's catchall would match every other install's mail. The architecture works only if catchall recipients are either (a) the install's domain identity (e.g., `sandboxes-kph.example.com` per-install subdomain) or (b) a compiler-emitted explicit recipient list of known sandbox addresses for that install. Decision needed.
3. **`KM_SES_ACTIVATE_RULESET` is consumed only in `infra/live/use1/ses/terragrunt.hcl` line 47 and documented in `OPERATOR-GUIDE.md`** — no Go code reads it. Removal scope is narrow (3 files plus the SES module variable + resource).

**Primary recommendation:** Treat Phase 84 as a 3-surface refactor: (1) Terraform module split (new foundation module + slim regional module + bootstrap wiring), (2) Go-layer address derivation (km configure → email-handler Lambda → sandbox userdata), (3) operator-facing surface (km doctor check, hard removal of Phase 82.1 docs + env var, update `km-send` and `pkg/aws/ses.go` operator-address literals). Use `aws_ses_receipt_rule.rule_set_name` referencing a string constant `sandbox-email-shared` in both modules (no Terraform cross-state data source needed — SES rule_set_name is a free-form string identifier, not a Terraform-resource attribute).

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Architecture: where the rule set lives** — New foundation/bootstrap module `infra/modules/ses-shared-rule-set/v1.0.0/`. Reason: rule set is conceptually account-shared state (analogous to an IAM SAML provider). Putting it in a foundation layer makes ownership unambiguous, prevents first-install-wins coupling, and keeps the regional `ses/` module focused on rules only. Not chosen: conditional-create in existing `ses/` module (data-source-then-create-if-missing) — rejected because it couples rule-set lifecycle to whichever install ran first, complicates `km uninit` semantics, and creates "who owns this resource" ambiguity.

**Cleanup of Phase 82.1** — Hard removal of `activate_rule_set` variable, `aws_ses_active_receipt_rule_set` resource (from the per-install regional `ses` module), `KM_SES_ACTIVATE_RULESET` env-var path, and CLAUDE.md "Phase 82.1 follow-up" runbook section. Scope: `infra/modules/ses/v1.0.0/variables.tf`, `infra/modules/ses/v1.0.0/main.tf`, any compiler code that exports `KM_SES_ACTIVATE_RULESET` (research confirms: NONE), CLAUDE.md Phase 82.1 section, ROADMAP.md `82.1-03-PLAN.md` description (mark superseded, do not delete the historical plan file).

**Operator address format** — `operator-${resource_prefix}@sandboxes.${domain}` (single hyphen, prefix appended). Default install (`prefix=km`) yields `operator-km@sandboxes.{domain}`, NOT legacy `operator@`.

**Sandbox addresses** — Stay flat at `{sandbox-id}@sandboxes.${domain}`. No prefix injection. Sandbox IDs already have sufficient entropy. Avoiding the change keeps `km-send` / `km-recv` and the entire sandbox-side email plumbing untouched.

**Catch-all rule** — `${prefix}-sandbox-catchall` matches `*@sandboxes.${domain}` for that install's known sandbox IDs (compiler-emitted recipient list) OR a literal wildcard if AWS SES supports per-rule wildcards within a shared rule set. **Resolve in research phase.** (See § Catch-All Rule Semantics below for the answer.)

**km configure wiring** — `km configure` derives `KM_OPERATOR_EMAIL=operator-${resource_prefix}@sandboxes.${email_subdomain}` from prefix + email subdomain already prompted for. No new prompt. Inherits Phase 82.1's preserve behavior for `resource_prefix` (locked at first configure; reset via `--reset-prefix`).

**email-handler Lambda resolution** — Lambda reads `${prefix}` from its own env (existing `state_prefix` from Phase 82-B2), constructs the expected operator-address pattern `operator-${prefix}@`, and routes inbound to operator-dispatch path. Other prefixes (foreign operator addresses) are silently dropped (logged at INFO, not delivered cross-install). Why silent drop: misconfiguration on sender side, not a security event.

**km doctor orphan check** — Add `ses_rules_healthy` check. Lists rules in the shared rule set, parses `${prefix}-` from each rule name, cross-references against local `km-config.yaml`'s `resource_prefix`. WARN-level on mismatch. Not ERROR-level: other installs' rules are *expected*.

**Rule naming convention** — `${prefix}-operator-inbound`, `${prefix}-sandbox-catchall`. Two rules per install. Rule ordering: operator-inbound BEFORE catchall (specific recipient match should win on declared order; see § AWS SES Rule Semantics below).

**Foundation module bootstrap surface** — Bootstrapped via existing foundation/bootstrap layer (same layer that owns the foundation S3 buckets, KMS keys, etc.). Idempotent re-apply required — second install's `km init` MUST NOT recreate the rule set. Naming: rule set is named `sandbox-email-shared` (no prefix). Resource is `aws_ses_receipt_rule_set` with `lifecycle.prevent_destroy = true` to guard against accidental teardown.

**Active rule set** — Foundation module also owns `aws_ses_active_receipt_rule_set` pointing at `sandbox-email-shared`. Exactly ONE active rule set, owned by foundation, activated once at bootstrap, never touched by regional installs.

### Claude's Discretion

- Versioning of the regional `ses` module: bump to `v2.0.0` (breaking — rule set removed) vs create new `v2.0.0` alongside and leave `v1.0.0` in tree. Research finds **no prior versioning of `ses` module** (only v1.0.0 exists) and no convention for "v2 alongside v1." Cluster-IRSA, email-handler, and lambda-slack-bridge all use a single `v1.0.0` directory. Recommendation: **modify `v1.0.0/` in place** — this is a breaking schema change but no other regional module references the SES rule-set ownership directly; consistent with how Phase 82 made breaking changes inside v1.0.0 modules.
- `km-config.yaml` schema: explicit `operator_email` field OR derive on-the-fly from `resource_prefix + email_subdomain + domain`. Recommendation: **keep derived** (no schema change) — the existing `OperatorEmail` field stays for any operator override use, but `km configure` writes the derived value. Avoids dual-source-of-truth bugs.
- Bootstrap surface placement: new directory `infra/live/use1/ses-shared-rule-set/`, OR fold into existing bootstrap path. Recommendation: **new regional bootstrap directory** because SES receipt rules are regional resources (rule sets live in a specific AWS region). The "foundation" layer in this codebase is actually `km bootstrap` (which provisions account-wide SCP + per-region KMS + S3) — Phase 84's foundation belongs in the same execution flow but lands in `infra/live/<regionLabel>/ses-shared-rule-set/`. See § Bootstrap Layer Mechanics for prior art.

### Deferred Ideas (OUT OF SCOPE)

- Sandbox-address namespacing (if cross-install sandbox ID collision is ever observed, address in a follow-up phase).
- Operator orphan-rule auto-cleanup (`km doctor --backfill-tags` style) — defer until WARN-level check identifies real-world need.
- Cross-account SES rule sharing — not on roadmap; SES is account-local by AWS design.
- Migration shim for legacy `operator@` addresses — not needed (no live deployments use it).
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| SES-PREFIX-ADDRESS | Operator inbound address becomes `operator-${resource_prefix}@sandboxes.${domain}` | `km configure` writes `KM_OPERATOR_EMAIL` directly (configure.go line 338); compiler `userdata.go` injects `KM_OPERATOR_EMAIL` env var (line 191); `pkg/aws/ses.go:271` and `pkg/compiler/userdata.go:1621,1653` carry hardcoded `operator@${domain}` literals that must change. |
| SES-SHARED-RULESET | Account-shared SES receipt rule set owned by foundation/bootstrap layer | New `infra/modules/ses-shared-rule-set/v1.0.0/` module with `aws_ses_receipt_rule_set "shared" { rule_set_name = "sandbox-email-shared" }` + `aws_ses_active_receipt_rule_set` + `prevent_destroy = true`. Lives in `infra/live/<regionLabel>/ses-shared-rule-set/` (regional bootstrap stage). `km bootstrap` or a new `--shared-ses` flag triggers apply. Idempotent on re-apply via SES API (the rule set is a singleton named identity). |
| SES-PER-INSTALL-RULES | Per-install rules attached to shared rule set | Regional `ses` module retains `aws_ses_receipt_rule` resources but changes `rule_set_name` to constant `"sandbox-email-shared"` (string literal, NOT a resource reference). Names become `${var.resource_prefix}-operator-inbound` and `${var.resource_prefix}-sandbox-catchall`. The `aws_ses_receipt_rule_set "km_sandbox"` resource is REMOVED. The `aws_ses_active_receipt_rule_set` resource is REMOVED. |
| SES-82.1-REMOVAL | Hard removal of Phase 82.1 mechanism | Files: `infra/modules/ses/v1.0.0/variables.tf` (remove `activate_rule_set` variable), `infra/modules/ses/v1.0.0/main.tf` (remove `aws_ses_active_receipt_rule_set` resource + `count` gating + related `depends_on` references), `infra/live/use1/ses/terragrunt.hcl` (remove `activate_rule_set` input + `KM_SES_ACTIVATE_RULESET` env-var read), `OPERATOR-GUIDE.md:646-677` (delete SES activation handoff section), `CLAUDE.md:35-65` (delete Phase 82.1 follow-up section), `ROADMAP.md` (mark 82.1-03 superseded with reference to 84). No Go code reads `KM_SES_ACTIVATE_RULESET` (grep confirmed). |
| SES-CONFIGURE-WIRING | `km configure` derives `KM_OPERATOR_EMAIL` from prefix | `internal/app/cmd/configure.go` runConfigure function builds `pc.OperatorEmail` at line 338. Phase 84 changes the prompt default OR auto-derives. Two strategies: (a) derive `operatorEmail = "operator-" + resourcePrefix + "@" + emailSubdomain + "." + domain` if user enters blank or leaves default; (b) replace the free-form prompt with a fixed `[shown] {derived}` confirmation. Recommendation: option (a) — preserve operator override capability. The `OperatorEmail` field in `platformConfig` stays. |
| SES-HANDLER-LOOKUP | Lambda resolves `operator-${prefix}@` for inbound dispatch | `cmd/email-create-handler/main.go` reads `KM_RESOURCE_PREFIX` env var via `resourcePrefix()` helper (lines 113-118); add `expectedOperatorAddr := "operator-" + resourcePrefix() + "@" + os.Getenv("KM_EMAIL_DOMAIN")`. The Handle function (line 230+) extracts the recipient via `mail.ParseAddress(msg.Header.Get("To"))` or via S3 object-key prefix (today uses `mail/create/` from the S3 receipt rule action — separate rules can use separate prefixes); recipient verification happens BEFORE allowlist + safe-phrase checks. Foreign-prefix emails get silently dropped with INFO log. |
| SES-DOCTOR-ORPHANS | New `km doctor` check for orphan rules | Pattern: `internal/app/cmd/doctor.go:881-927` (`checkSESIdentity`). Add `checkSESRules` function with new narrow-interface `SESReceiptRuleAPI` covering `DescribeReceiptRuleSet` (classic SES v1 API — **NOT sesv2**, see § Critical SDK Note below). Reads `sandbox-email-shared` rule set, extracts `${prefix}-` from each rule name, compares against `cfg.GetResourcePrefix()` (the local install). Mismatched prefixes → WARN with remediation message ("rule belongs to install with prefix {x}; run km uninit on that install or use aws ses delete-receipt-rule manually"). |

</phase_requirements>

## Standard Stack

### Core

| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `aws-sdk-go-v2/service/ses` (classic, v1) | matches existing go.mod | SES classic receipt-rule APIs (`DescribeReceiptRuleSet`, `ListReceiptRuleSets`, `DescribeActiveReceiptRuleSet`) | sesv2 SDK does NOT expose receipt-rule operations; classic SES v1 API is required for ANY interaction with receipt rule sets. The codebase currently uses only `sesv2` (for `GetEmailIdentity`, `SendEmail`, `DeleteEmailIdentity`). **Adding the classic `ses` SDK module is a new dependency.** |
| `terraform-provider-aws` >= 5.0 | matches root.hcl | `aws_ses_receipt_rule`, `aws_ses_receipt_rule_set`, `aws_ses_active_receipt_rule_set` | Already used in v1.0.0/main.tf — provider supports all three resources |
| `gopkg.in/yaml.v3` | existing | km-config.yaml read/write | Already used by `configure.go` |

### Supporting

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `aws-sdk-go-v2/service/sesv2` | existing | Send replies, get email identity verification | Existing usage — no changes from Phase 84 |
| `terraform.io/hashicorp/aws_iam_role` | existing | IAM perms for email-handler Lambda | Email-handler Lambda already has SES + S3 access; no new IAM scoping needed for Phase 84 since the Lambda reads the same S3 prefix |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Classic SES v1 SDK for doctor check | Pure AWS CLI shell-out (`aws ses describe-receipt-rule-set --rule-set-name sandbox-email-shared --output json`) | Shelling out matches the precedent of `init.go:1216` (Terraform download) and `cluster.go` (kubectl/aws ecr describe). Avoids a new go.mod dependency but adds a shell-process dependency and JSON-parse step. **Recommendation: use the SDK** — keeps doctor as a single Go process, follows the pattern of all other doctor checks, and avoids shelling out to AWS CLI which may not be installed. |
| Foundation rule-set + Terraform `moved {}` block | Hard delete from regional state + state surgery | The Phase 82.1 plan used Terraform 1.x count-index auto-migration. Phase 84 is a true resource move BETWEEN Terraform states (regional → foundation) — `moved {}` only works WITHIN a single state. Recommendation: write an operator runbook (in `OPERATOR-GUIDE.md`) for the one-time state migration: `terraform state rm aws_ses_receipt_rule_set.km_sandbox` in the regional state, then apply the new foundation module to import (or recreate). For greenfield installs nothing to migrate. |
| In-module idempotency for foundation module | Pure resource creation with `lifecycle.prevent_destroy` | If a second install bootstraps after the first, the SES rule set already exists. Foundation Terraform must either (a) be skipped by the second operator (manual step), or (b) use `terraform import` semantics, or (c) the foundation module simply runs the same `aws_ses_receipt_rule_set` creation — AWS API returns idempotently for "create rule set" when the name already exists? **Verification needed** — AWS docs suggest `CreateReceiptRuleSet` returns `AlreadyExistsException`. Recommendation: have foundation module use `terraform import` + data-source-fallback OR document that only one install per account runs `km bootstrap --shared-ses` and other installs skip it. |

**Installation:**
```bash
# New Go module dependency for the classic SES v1 SDK (doctor check only)
go get github.com/aws/aws-sdk-go-v2/service/ses
```

## Architecture Patterns

### Recommended Project Structure

```
infra/modules/
├── ses/v1.0.0/                       # Regional — slim down: domain identity, DKIM, DNS, S3 policy, per-install rules
│   ├── main.tf                       # REMOVE: aws_ses_receipt_rule_set, aws_ses_active_receipt_rule_set
│   │                                  # KEEP: aws_ses_domain_identity, aws_ses_domain_dkim, aws_route53_record.*, aws_s3_bucket_policy, aws_ses_receipt_rule.* (point at "sandbox-email-shared")
│   ├── variables.tf                  # REMOVE: activate_rule_set
│   └── outputs.tf                    # KEEP: domain_identity_arn, REMOVE: receipt_rule_set_name (now constant)
└── ses-shared-rule-set/v1.0.0/       # NEW foundation/bootstrap — account-shared
    ├── main.tf                       # aws_ses_receipt_rule_set + aws_ses_active_receipt_rule_set
    │                                  # lifecycle.prevent_destroy = true
    ├── variables.tf                  # rule_set_name (default "sandbox-email-shared")
    └── outputs.tf                    # rule_set_name (string), active_rule_set (bool)

infra/live/use1/
├── ses-shared-rule-set/              # NEW — applied once per account/region (operator coordinates)
│   └── terragrunt.hcl
└── ses/                              # Existing — applied per install
    └── terragrunt.hcl                # REMOVE: activate_rule_set, KM_SES_ACTIVATE_RULESET

internal/app/cmd/
├── configure.go                      # MODIFY: derive operator_email default from resource_prefix + email_subdomain + domain
├── init.go                           # NO CHANGE — regional module list unchanged
├── bootstrap.go                      # MODIFY: optionally invoke ses-shared-rule-set after KMS/S3 (or new --shared-ses flag)
└── doctor.go                         # ADD: SESReceiptRuleAPI interface, checkSESRules function

cmd/email-create-handler/
└── main.go                           # MODIFY: recipient verification — accept only operator-${prefix}@${domain}; silent drop foreign

pkg/compiler/userdata.go              # MODIFY: lines 1621, 1653 — replace `operator@` literal with `${KM_OPERATOR_EMAIL}` env-var ref
pkg/aws/ses.go                        # MODIFY: line 271 — interpolate prefix into operator address in notification body
```

### Pattern 1: Cross-state Rule-Set Reference via String Constant

**What:** Foundation module creates `aws_ses_receipt_rule_set "shared" { rule_set_name = "sandbox-email-shared" }`. Regional module creates `aws_ses_receipt_rule "operator_inbound" { rule_set_name = "sandbox-email-shared" }` — both reference the rule set by its string name, not by resource reference.

**When to use:** When two separate Terraform states must contribute to the same singleton AWS resource. SES rule_set_name is a free-form string used as the parent identifier. No Terraform data source is needed because rule_set_name is a string the operator picks, not an AWS-generated ID.

**Example:**
```hcl
# infra/modules/ses-shared-rule-set/v1.0.0/main.tf
resource "aws_ses_receipt_rule_set" "shared" {
  rule_set_name = var.rule_set_name  # "sandbox-email-shared"

  lifecycle {
    prevent_destroy = true  # Block accidental teardown of shared state.
  }
}

resource "aws_ses_active_receipt_rule_set" "shared" {
  rule_set_name = aws_ses_receipt_rule_set.shared.rule_set_name
}
```

```hcl
# infra/modules/ses/v1.0.0/main.tf — regional, per install
resource "aws_ses_receipt_rule" "operator_inbound" {
  name          = "${var.resource_prefix}-operator-inbound"
  rule_set_name = "sandbox-email-shared"  # String constant — matches foundation module
  recipients    = ["operator-${var.resource_prefix}@${var.domain}"]
  enabled       = true
  scan_enabled  = false

  s3_action {
    bucket_name       = var.artifact_bucket_name
    object_key_prefix = "mail/create/${var.resource_prefix}/"  # Prefix-scoped S3 location
    position          = 1
  }
}

resource "aws_ses_receipt_rule" "sandbox_catchall" {
  name          = "${var.resource_prefix}-sandbox-catchall"
  rule_set_name = "sandbox-email-shared"
  recipients    = [var.domain]  # Whole-domain match; see § Catch-All Rule Semantics
  enabled       = true
  scan_enabled  = false

  s3_action {
    bucket_name       = var.artifact_bucket_name
    object_key_prefix = "mail/${var.resource_prefix}/"
    position          = 1
  }

  after = aws_ses_receipt_rule.operator_inbound.name  # Specific match before catchall
}
```

### Pattern 2: Lambda env-var-driven recipient routing

**What:** The `email-create-handler` Lambda has `KM_RESOURCE_PREFIX` injected at Lambda creation (already done in email-handler/v1.0.0/main.tf line 261). Phase 84 adds derivation: parse the `Delivery.recipients` field from the SES S3 event OR parse the `To:` header from the raw MIME message; check that it equals `operator-${prefix}@${domain}`; reject (silently drop, INFO log) if it's a different prefix.

**When to use:** For ANY Lambda receiving inbound SES email where multiple installs share an S3 bucket. The prefix-aware S3 key (`mail/create/${prefix}/`) helps but doesn't replace recipient verification — a misconfigured rule could write to the wrong prefix.

**Example:**
```go
// cmd/email-create-handler/main.go
func (h *OperatorEmailHandler) expectedOperatorAddress() string {
    return fmt.Sprintf("operator-%s@%s", resourcePrefix(), h.Domain)
}

// In Handle, BEFORE allowlist/safe-phrase check:
toHeader := msg.Header.Get("To")
toAddr := extractEmail(toHeader)  // existing helper
expected := h.expectedOperatorAddress()
if !strings.EqualFold(toAddr, expected) {
    fmt.Fprintf(os.Stderr, "[operator-email] silently dropping email to %s: expected %s (foreign install or misroute)\n", toAddr, expected)
    return nil  // Not an error — drop and exit cleanly
}
```

### Pattern 3: Doctor narrow-interface health check (existing pattern)

Already established in `internal/app/cmd/doctor.go`. New addition:
```go
// SESReceiptRuleAPI covers SES classic-v1 DescribeReceiptRuleSet for orphan-rule check.
type SESReceiptRuleAPI interface {
    DescribeReceiptRuleSet(ctx context.Context, params *ses.DescribeReceiptRuleSetInput, optFns ...func(*ses.Options)) (*ses.DescribeReceiptRuleSetOutput, error)
}

func checkSESRules(ctx context.Context, client SESReceiptRuleAPI, localPrefix string) CheckResult {
    out, err := client.DescribeReceiptRuleSet(ctx, &ses.DescribeReceiptRuleSetInput{
        RuleSetName: aws.String("sandbox-email-shared"),
    })
    if err != nil {
        return CheckResult{Name: "SES rules", Status: StatusWarn, Message: fmt.Sprintf("could not list rules: %v", err), Remediation: "Run km bootstrap to create the shared rule set"}
    }
    orphans := []string{}
    for _, rule := range out.Rules {
        name := aws.ToString(rule.Name)
        prefix := strings.SplitN(name, "-", 2)[0]
        if prefix != localPrefix {
            orphans = append(orphans, name)
        }
    }
    if len(orphans) > 0 {
        return CheckResult{
            Name: "SES rules",
            Status: StatusWarn,
            Message: fmt.Sprintf("rules for foreign prefixes detected: %s", strings.Join(orphans, ", ")),
            Remediation: "Run km uninit on the foreign install or aws ses delete-receipt-rule manually",
        }
    }
    return CheckResult{Name: "SES rules", Status: StatusOK, Message: fmt.Sprintf("%d rules, all match prefix %q", len(out.Rules), localPrefix)}
}
```

### Anti-Patterns to Avoid

- **Using Terraform `aws_ses_receipt_rule_set` as data source to cross-reference foundation state.** SES rule_set_name is a free-form string; no need for data-source. A string constant `"sandbox-email-shared"` in both modules is simpler and avoids the data-source lookup race (`terraform plan` against foundation state from regional state).
- **Treating `aws_ses_active_receipt_rule_set` as multi-resource.** AWS allows only ONE active rule set per account/region. Foundation owns this resource exclusively. Regional installs must NEVER declare it (Phase 84's hard removal of `activate_rule_set` enforces this).
- **Relying on first-match-wins for rule ordering.** AWS SES executes ALL matching rules in declared order (verified in dev guide). `Stop rule set` actions are explicit termination; absence of `stop_action` means catchall fires AFTER operator-inbound when both match. This is desired for Phase 84 — operator-inbound writes to `mail/create/`, catchall writes to `mail/` — they don't overlap on S3 but both fire on `operator-kph@sandboxes.example.com` (a recipient that matches BOTH the explicit `operator-kph@` rule AND the catchall domain `sandboxes.example.com`).
- **Hardcoded operator email strings.** Existing literals at `pkg/compiler/userdata.go:1621,1653`, `pkg/aws/ses.go:271`, and `cmd/email-create-handler/main.go:861` (the `from` string for outbound replies — uses `operator@%s` literal). All four must change to `operator-${prefix}@`.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Cross-install rule deconfliction | Custom polling Lambda that watches all rules | AWS SES rule ordering + `after` argument | The natural rule-set semantics handle ordering; the only convention needed is "operator-inbound BEFORE catchall WITHIN an install's rule pair" — `after = aws_ses_receipt_rule.operator_inbound.name` does this. |
| Rule cleanup on uninit | Custom AWS CLI script | Terragrunt's natural destroy semantics | When the regional `ses` module destroys, its `aws_ses_receipt_rule` resources naturally remove only that install's rules from the shared rule set. Foundation's `aws_ses_receipt_rule_set` is untouched by regional uninit. |
| Detecting which install owns which rule | Tag-based ownership | Name-prefix parsing | SES rules don't support resource tags. Convention `${prefix}-${role}` is the only signal — encoded into name. Doctor check parses the prefix from the name. |
| Foundation-module idempotency | Custom Lambda to import-or-create | Terraform `prevent_destroy` + operator runbook | A second operator running `km bootstrap` against the same account will get an `AlreadyExistsException` on the `aws_ses_receipt_rule_set` resource. Document this as expected: only ONE operator (the primary) runs `km bootstrap --shared-ses`; others skip the step. Alternatively, the foundation module uses `terraform import` semantics or a flag like `register_shared_rule_set = false` (parallels Phase 80's cluster-irsa `register_oidc_provider = auto` pattern). **Recommendation: emit `auto` detection in `km bootstrap` similar to cluster-irsa — list-rulesets, skip create if exists.** |

**Key insight:** This phase's biggest risk is NOT building the wrong thing — it's failing to verify the SES recipient-matching semantics. The AWS SES docs are explicit: "all of the receipt rules within your active rule set are applied in the order you've defined." Two installs' rules CAN coexist in one rule set without interfering as long as their recipients don't overlap. Operator-inbound rules don't overlap (each install has a unique `operator-${prefix}@` address). Catchall rules DO overlap if both use the whole-domain recipient — both will fire on every inbound email to that domain. This is acceptable IF both installs' catchall S3 actions write to DIFFERENT S3 prefixes (`mail/${prefix}/`), so each install only reads its own sandbox mail. **The compiler must verify the catchall S3 prefix isolation.**

## Common Pitfalls

### Pitfall 1: SES SDK confusion (v2 vs v1 classic)
**What goes wrong:** Engineer reaches for `aws-sdk-go-v2/service/sesv2` to list receipt rule sets, finds no `DescribeReceiptRuleSet` method, gets stuck.
**Why it happens:** sesv2 SDK covers SendEmail, GetEmailIdentity, DKIM — but receipt-rule APIs were left in the classic SES v1 API namespace. AWS never migrated them to v2.
**How to avoid:** Use `aws-sdk-go-v2/service/ses` (classic, v1 in newer SDK) for ANY receipt-rule operation. Existing codebase uses only sesv2; **adding the v1 SES module is a new go.mod dependency**.
**Warning signs:** "Cannot find method `DescribeReceiptRuleSet` on `*sesv2.Client`" — switch to `ses.Client`.

### Pitfall 2: Rule_set_name not a Terraform reference
**What goes wrong:** Engineer writes `rule_set_name = data.aws_ses_receipt_rule_set.shared.rule_set_name` and discovers there's no `aws_ses_receipt_rule_set` Terraform data source.
**Why it happens:** Terraform AWS provider supports the resource but NOT a corresponding data source.
**How to avoid:** Use a string constant in both modules. `rule_set_name = "sandbox-email-shared"` works because SES uses the name as the parent identifier, not a UUID.
**Warning signs:** `terraform plan` fails with "Invalid data source: aws_ses_receipt_rule_set" — switch to literal string.

### Pitfall 3: Catch-all rule recipient ambiguity
**What goes wrong:** Both installs use `recipients = [var.domain]` (e.g., `sandboxes.example.com`). Inbound mail to `sandbox-abc@sandboxes.example.com` matches BOTH installs' catchall rules. Each S3 action writes to its install's prefix — BUT both installs see the mail. Mail meant for install A's sandbox is also visible in install B's S3 prefix.
**Why it happens:** SES has no wildcard support in recipients; whole-domain match is the only way to express "any address on this domain"; both installs share one domain.
**How to avoid:** Two options:
  - **Option A: Compiler-emitted recipient list.** The compiler enumerates known sandbox addresses and writes them into the `recipients` array. Pro: precise per-install isolation. Con: requires Terraform re-apply on every `km create` / `km destroy`. The CONTEXT calls this out: "compiler-emitted recipient list."
  - **Option B: Per-install email subdomain.** Install A uses `sandboxes-kph.example.com`, install B uses `sandboxes-rg.example.com`. Each install owns its own `aws_ses_domain_identity` + DKIM + MX + catchall rule using its own subdomain. No collision. **This breaks the CONTEXT.md decision that operator address is `operator-${prefix}@sandboxes.${domain}`** — instead would be `operator-${prefix}@sandboxes-${prefix}.${domain}`. The operator address becomes redundant ("prefix in two places") but the architecture is simpler.
  - **Recommendation: Open Question for planner — surface to operator for decision.** Option A is cleaner-looking but adds per-create infra-state-churn; Option B is more verbose but matches existing per-install regional resource patterns.
**Warning signs:** Two installs in same account, same domain, both with active sandboxes — inbound mail shows up in BOTH installs' S3. Manual inspection of `aws s3 ls` shows mail/{prefix}/<unrelated-sandbox-id>/.

### Pitfall 4: Foundation module not idempotent on second `km bootstrap`
**What goes wrong:** Operator A runs `km bootstrap` → creates `sandbox-email-shared` rule set. Operator B (in same account) runs `km bootstrap` → `aws_ses_receipt_rule_set.shared` apply fails with `AlreadyExistsException`.
**Why it happens:** Terraform `aws_ses_receipt_rule_set` resource creation is NOT idempotent by default.
**How to avoid:** Mirror Phase 80's `cluster-irsa` `register_oidc_provider = auto` pattern. In `km bootstrap`, call `aws ses list-receipt-rule-sets` (classic SDK or CLI shell-out) before invoking the foundation module's Terragrunt directory; if `sandbox-email-shared` exists, skip the apply step with an informational log. Phase 80 prior art: `internal/app/cmd/cluster.go:368` ("true"/"false" override for `register_oidc_provider`).
**Warning signs:** Second operator's `km bootstrap --dry-run=false` fails on `terragrunt apply` of `ses-shared-rule-set`. Log says `EntityAlreadyExists: Rule set named 'sandbox-email-shared' already exists.`

### Pitfall 5: Stale `KM_OPERATOR_EMAIL` after re-configure
**What goes wrong:** Operator changes `resource_prefix` via `km configure --reset-prefix` but `operator_email` in km-config.yaml still has the old value.
**Why it happens:** `OperatorEmail` is a free-form field; `km configure` reads it from existing config and re-uses it on re-run. Phase 82 preserve-on-rerun logic carries over old values.
**How to avoid:** On Phase 84's `km configure` change, ALSO clear `operator_email` when `--reset-prefix` is passed, OR auto-overwrite `operator_email` whenever the prefix changes (detect change, prompt operator to confirm).
**Warning signs:** `km doctor` reports `OperatorEmail = operator-km@...` but `resource_prefix = rg`. The doctor check should catch this.

### Pitfall 6: Sandbox userdata uses old `operator@` literal
**What goes wrong:** Phase 84 ships, operator runs `km create` from a stale binary — userdata.go line 1621 still emits `operator@${KM_SANDBOX_DOMAIN}`. Sandbox sends `km-send` with default `--to`, mail goes to non-existent address, SES bounces.
**Why it happens:** Sandbox-side `km-send` heredoc is inlined into `pkg/compiler/userdata.go` at line 1621 and 1653 — both are pure bash strings with hardcoded `operator@` literals.
**How to avoid:** Plan must explicitly call out these two literal locations + `pkg/aws/ses.go:271` (operator address in notification body) + `cmd/email-create-handler/main.go:861` (outbound reply From). All four become `${KM_OPERATOR_EMAIL}` shell-var refs OR Go-string interpolations.
**Warning signs:** Operator reports "sandbox sent to wrong address" — `cat /opt/km/bin/km-send | grep operator@` reveals the hardcoded literal.

## Code Examples

### Example 1: Foundation module main.tf
```hcl
# infra/modules/ses-shared-rule-set/v1.0.0/main.tf
# Account-shared SES receipt rule set + active-rule-set pointer.
# Bootstrapped once per AWS account/region by `km bootstrap --shared-ses`.
# Per-install modules attach prefix-namespaced rules to this rule set.

resource "aws_ses_receipt_rule_set" "shared" {
  rule_set_name = var.rule_set_name

  lifecycle {
    prevent_destroy = true  # Multi-install state; never destroy on uninit.
  }
}

# AWS allows ONE active rule set per account/region.
# Owned exclusively here; per-install modules never declare this resource.
resource "aws_ses_active_receipt_rule_set" "shared" {
  rule_set_name = aws_ses_receipt_rule_set.shared.rule_set_name
}
```

### Example 2: km configure derivation of operator address
```go
// internal/app/cmd/configure.go — runConfigure modifications
defaultOperatorEmail := ""
if operatorEmail == "" && resourcePrefix != "" && emailSubdomain != "" && domain != "" {
    // Derive: operator-${prefix}@${email_subdomain}.${domain}
    defaultOperatorEmail = fmt.Sprintf("operator-%s@%s.%s", resourcePrefix, emailSubdomain, domain)
}

// Prompt path (interactive):
prompt := "Operator email for sandbox notifications (TTL, idle, budget)"
operatorEmail, err = promptWithDefault(out, scanner, prompt, defaultOperatorEmail)
// promptWithDefault: shows derived default in brackets; bare Enter accepts derived;
// operator can override to any value.

// Non-interactive: if --operator-email empty, fall through to derived default.
if !nonInteractive && operatorEmail == "" {
    operatorEmail = defaultOperatorEmail
}
```

### Example 3: Email-handler recipient verification
```go
// cmd/email-create-handler/main.go — Handle method, after sender extraction
// (existing code around line 240)
toAddrs, _ := msg.Header.AddressList("To")
deliveredTo := ""
for _, a := range toAddrs {
    if a != nil {
        deliveredTo = strings.ToLower(a.Address)
        break
    }
}
expectedAddr := strings.ToLower(fmt.Sprintf("operator-%s@%s",
    resourcePrefix(), h.Domain))
if deliveredTo != expectedAddr {
    fmt.Fprintf(os.Stderr, "[operator-email] silently dropping email to %s (expected %s)\n",
        deliveredTo, expectedAddr)
    return nil
}
// proceed with existing safe-phrase + allowlist checks
```

### Example 4: Doctor SES rules check
```go
// internal/app/cmd/doctor.go — new function
type SESReceiptRuleAPI interface {
    DescribeReceiptRuleSet(ctx context.Context, params *ses.DescribeReceiptRuleSetInput, optFns ...func(*ses.Options)) (*ses.DescribeReceiptRuleSetOutput, error)
}

func checkSESRules(ctx context.Context, client SESReceiptRuleAPI, localPrefix string) CheckResult {
    if client == nil {
        return CheckResult{
            Name: "SES rules",
            Status: StatusSkipped,
            Message: "SES classic SDK client unavailable",
        }
    }
    out, err := client.DescribeReceiptRuleSet(ctx, &ses.DescribeReceiptRuleSetInput{
        RuleSetName: aws.String("sandbox-email-shared"),
    })
    if err != nil {
        return CheckResult{
            Name: "SES rules",
            Status: StatusError,
            Message: fmt.Sprintf("DescribeReceiptRuleSet: %v", err),
            Remediation: "Verify the shared rule set exists; run `km bootstrap --shared-ses`",
        }
    }
    orphans := []string{}
    own := 0
    for _, r := range out.Rules {
        n := aws.ToString(r.Name)
        // ${prefix}-{operator-inbound|sandbox-catchall}
        idx := strings.Index(n, "-")
        if idx < 0 { orphans = append(orphans, n); continue }
        prefix := n[:idx]
        if prefix == localPrefix {
            own++
        } else {
            orphans = append(orphans, n)
        }
    }
    if len(orphans) > 0 {
        return CheckResult{
            Name: "SES rules",
            Status: StatusWarn,
            Message: fmt.Sprintf("orphan rules: %s", strings.Join(orphans, ", ")),
            Remediation: "These belong to other installs; if no install owns them, run `aws ses delete-receipt-rule --rule-set-name sandbox-email-shared --rule-name <name>`",
        }
    }
    return CheckResult{
        Name: "SES rules",
        Status: StatusOK,
        Message: fmt.Sprintf("%d rules for prefix %q", own, localPrefix),
    }
}
```

## State of the Art

### AWS SES Rule Semantics (verified)

| Question | Answer | Source |
|----------|--------|--------|
| Multiple rules matching same email — all fire or first-match-wins? | **All matching rules fire** in declared order. Use `Stop rule set` action explicitly to terminate. | AWS SES Dev Guide — receiving-email-concepts.md "Email-receiving process": "all of the receipt rules within your active rule set are applied in the order you've defined" |
| Recipient list format | Full email addresses OR domain names. **NO wildcards** in classic SES receipt rules. | CloudFormation AWS::SES::ReceiptRule.Rule.Recipients docs: "The recipient domains and email addresses that the receipt rule applies to." No wildcard syntax documented. |
| One active rule set per account/region | Yes, hard limit | AWS SES Dev Guide: "while you can create multiple rule sets, only one can be active at a time" |
| Order of rules within rule set | Operator-controlled via `after` argument; otherwise insert-order; cross-state writes may produce arbitrary order | Terraform provider docs (Issue #24067) — no global ordering primitive for multi-state-managed rule sets |

### Old vs Current Approach

| Old Approach (Phase 82.1) | Current Approach (Phase 84) | When Changed | Impact |
|--------------------------|---------------------------|--------------|--------|
| Each install owns its own rule set; only one can be active | Single shared rule set; each install adds rules | Phase 84 | Second install needs zero opt-out env var; no race for activation |
| `KM_SES_ACTIVATE_RULESET=false` opt-out env var | Removed entirely | Phase 84 | Operator runbook simplified — no env-var step before `km init` |
| `aws_ses_active_receipt_rule_set` in regional module | Owned exclusively by foundation | Phase 84 | Regional uninit cannot accidentally deactivate inbound email |
| Manual handoff via `aws ses set-active-receipt-rule-set` | Never needed | Phase 84 | Removes operator footgun |

### Deprecated/Outdated

- **Phase 82.1's `activate_rule_set` variable in `infra/modules/ses/v1.0.0/variables.tf`** — DELETED in Phase 84.
- **`KM_SES_ACTIVATE_RULESET` env var** — DELETED in Phase 84. Not referenced by any Go code (grep verified); only by `infra/live/use1/ses/terragrunt.hcl:47` and `OPERATOR-GUIDE.md`.

## Open Questions

1. **Catch-all rule recipient — domain-match vs explicit address list (CRITICAL)**
   - What we know: AWS SES recipient list does NOT support wildcards. A whole-domain recipient (`sandboxes.example.com`) matches ALL addresses on that domain.
   - What's unclear: If two installs share a domain, both installs' catchall rules fire on every sandbox-inbound email. Are dual-fire S3 writes (each to its own `mail/${prefix}/` prefix) acceptable, or do we need per-install address enumeration?
   - Recommendation: **Surface to operator decision**. Option A (compiler-emitted recipient list) is more precise but causes Terraform re-apply on every `km create`/`km destroy`. Option B (per-install email subdomain) is simpler but requires changing the CONTEXT-locked address format. Option C (accept dual S3 writes, document that each install reads only its prefix) is acceptable IF mail volume is low and storage cost is bounded. **Default recommendation if operator doesn't decide: Option C** — both installs write to their own S3 prefix, the cross-contamination is purely read-only and bounded by sandbox volume.

2. **`aws_ses_domain_identity` and DNS records — foundation or regional?**
   - What we know: The current regional `ses/v1.0.0/main.tf` owns `aws_ses_domain_identity` (single domain per install), `aws_ses_domain_dkim` (3 DKIM CNAME records), `aws_route53_record.mx`, `aws_route53_record.ses_verification`. If two installs use the SAME domain, the second install's Terraform apply fails on `EntityAlreadyExists` for the domain identity.
   - What's unclear: Does CONTEXT.md assume two installs use distinct domains (i.e., distinct `email_subdomain` values produce `sandboxes-kph.example.com` vs `sandboxes-rg.example.com`), or share a domain?
   - Recommendation: **Surface to operator decision**. The CONTEXT.md operator address `operator-${prefix}@sandboxes.${domain}` implies SHARED domain (both installs use `sandboxes.example.com`). If shared, the domain identity + DKIM + DNS records also need to move to foundation OR be conditionally created (first-install-wins). If distinct, the rule-set shared-state design works as-is. **Default recommendation if operator doesn't decide: Treat domain identity as foundation-level too** — minimal additional scope, eliminates the EntityAlreadyExists failure mode entirely.

3. **Foundation module idempotency — `register_shared_rule_set` flag or pre-check in `km bootstrap`?**
   - What we know: Phase 80's `cluster-irsa` uses `register_oidc_provider = auto` — `km cluster add` lists existing providers and sets `false` if the provider already exists.
   - What's unclear: Should the SES foundation module follow this exact pattern, or rely on operator coordination (only one operator runs `km bootstrap --shared-ses`)?
   - Recommendation: **Follow cluster-irsa pattern**. Add `register_shared_rule_set` variable (bool, default `auto` via env var or `true`); `km bootstrap` calls `aws ses list-receipt-rule-sets`, sets `false` if `sandbox-email-shared` exists; module uses count-based `aws_ses_receipt_rule_set` creation gated on the variable. This makes the foundation module safe to re-apply from any install context.

4. **Operator runbook for one-time migration of existing Phase 82.1 installs**
   - What we know: Phase 84 hard-removes the `aws_ses_active_receipt_rule_set` from the regional `ses` module. Operators with an EXISTING Phase 82.1 deployment will see Terraform plan show "destroying aws_ses_active_receipt_rule_set" on first `km init --dry-run=false` after the Phase 84 upgrade.
   - What's unclear: CONTEXT.md says "no live operator inbox in any deployment uses the old design" — but what about the existing `km-sandbox-email` rule set? If it's still active in any account, the Phase 84 upgrade would DELETE its activation and replace with `sandbox-email-shared`. That's a deliberate cutover.
   - Recommendation: Plan includes an "upgrade preconditions" section in OPERATOR-GUIDE.md: (a) Run `km bootstrap --shared-ses` BEFORE first Phase 84 `km init`; (b) The Phase 84 `km init` will plan-destroy the old `aws_ses_active_receipt_rule_set.km_sandbox` (because Phase 84 removes it from regional state); (c) If the operator has live sandbox email, expect brief inbound gap during the cutover.

## Validation Architecture

### Test Framework

| Property | Value |
|----------|-------|
| Framework | Go standard `testing` package + `aws-sdk-go-v2` SDK mocks via narrow interfaces |
| Config file | `go.mod` (existing); no new test framework |
| Quick run command | `go test ./internal/app/cmd/... ./cmd/email-create-handler/... ./pkg/compiler/... -short -count=1` |
| Full suite command | `go test ./... -count=1` |
| Terraform tests | None — terragrunt plan output verification is operator-facing UAT (no automated terratest in repo) |

### Phase Requirements → Test Map

| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|--------------|
| SES-PREFIX-ADDRESS | `km configure` derives `operator-${prefix}@${email_subdomain}.${domain}` | unit | `go test ./internal/app/cmd/ -run TestConfigure_DerivesOperatorEmailFromPrefix -count=1` | ❌ Wave 0 — add to `configure_test.go` |
| SES-PREFIX-ADDRESS | Sandbox-side `km-send` defaults `--to` to derived operator address | unit | `go test ./pkg/compiler/ -run TestUserdata_KmSendOperatorAddressUsesEnvVar -count=1` | ❌ Wave 0 — add to `pkg/compiler/userdata_test.go` |
| SES-PREFIX-ADDRESS | `pkg/aws/ses.go` notification body interpolates operator address | unit | `go test ./pkg/aws/ -run TestSendCreateNotification_OperatorAddressUsesPrefix -count=1` | ❌ Wave 0 — extend `ses_test.go` |
| SES-SHARED-RULESET | Foundation module Terragrunt plan succeeds on greenfield apply | terraform plan smoke | `cd infra/live/use1/ses-shared-rule-set && terragrunt plan` — verify `aws_ses_receipt_rule_set.shared` planned-create | manual UAT — no automated terratest |
| SES-PER-INSTALL-RULES | Regional `ses` module no longer plans `aws_ses_receipt_rule_set` or `aws_ses_active_receipt_rule_set` | terraform plan smoke | `cd infra/live/use1/ses && terragrunt plan` — assert grep absent | manual UAT |
| SES-PER-INSTALL-RULES | Regional `aws_ses_receipt_rule.operator_inbound.rule_set_name == "sandbox-email-shared"` | unit (Terraform via tfplan parse) | Inspect `terragrunt.hcl` inputs in plan output | manual UAT |
| SES-82.1-REMOVAL | `KM_SES_ACTIVATE_RULESET` and `activate_rule_set` absent from codebase | grep-based assert | `! grep -rn "KM_SES_ACTIVATE_RULESET\|activate_rule_set" infra/ internal/ pkg/ cmd/ OPERATOR-GUIDE.md CLAUDE.md` | new Wave 0 test (CI command) |
| SES-CONFIGURE-WIRING | `km configure` writes derived operator email when blank/default | unit | `go test ./internal/app/cmd/ -run TestConfigure_BlankOperatorEmail_DerivesFromPrefix -count=1` | ❌ Wave 0 |
| SES-CONFIGURE-WIRING | `km configure --reset-prefix` also clears `operator_email` | unit | `go test ./internal/app/cmd/ -run TestConfigure_ResetPrefix_ClearsOperatorEmail -count=1` | ❌ Wave 0 |
| SES-HANDLER-LOOKUP | Email-handler accepts `operator-${ownPrefix}@${domain}` | unit | `go test ./cmd/email-create-handler/ -run TestHandle_OperatorAddress_OwnPrefix -count=1` | ❌ Wave 0 — add to `main_test.go` |
| SES-HANDLER-LOOKUP | Email-handler silently drops `operator-${otherPrefix}@${domain}` | unit | `go test ./cmd/email-create-handler/ -run TestHandle_OperatorAddress_ForeignPrefix_Drops -count=1` | ❌ Wave 0 |
| SES-DOCTOR-ORPHANS | `checkSESRules` returns OK when all rules match local prefix | unit | `go test ./internal/app/cmd/ -run TestCheckSESRules_AllOwn -count=1` | ❌ Wave 0 — add to `doctor_test.go` |
| SES-DOCTOR-ORPHANS | `checkSESRules` returns WARN when rules from other prefixes exist | unit | `go test ./internal/app/cmd/ -run TestCheckSESRules_Orphans -count=1` | ❌ Wave 0 |
| End-to-end | Two-prefix install scenario: both installs operate without colliding | operator UAT | Documented runbook in `OPERATOR-GUIDE.md`; verify via `aws ses describe-receipt-rule-set --rule-set-name sandbox-email-shared` shows 4 rules (2 per prefix) | manual UAT |
| End-to-end | `km uninit` on prefix A leaves prefix B's rules intact | operator UAT | `terragrunt destroy` in install A's `infra/live/.../ses/`; verify `aws ses describe-receipt-rule-set` still shows install B's 2 rules | manual UAT |

### Sampling Rate

- **Per task commit:** `go test ./internal/app/cmd/... ./cmd/email-create-handler/... -run <relevant subset> -short -count=1` (under 30s)
- **Per wave merge:** `go test ./... -count=1` (full suite — typically 2-5 minutes)
- **Phase gate:** Full Go test suite green + manual `terragrunt plan` against a real two-prefix scenario before `/gsd:verify-work`

### Wave 0 Gaps

- [ ] `internal/app/cmd/configure_test.go` — add 3 new test functions for SES-CONFIGURE-WIRING + SES-PREFIX-ADDRESS derivation
- [ ] `cmd/email-create-handler/main_test.go` — add 2 new test functions for SES-HANDLER-LOOKUP recipient verification (own vs foreign prefix)
- [ ] `internal/app/cmd/doctor_test.go` — add 2 new test functions for SES-DOCTOR-ORPHANS (all-own, orphans-present)
- [ ] `internal/app/cmd/doctor_ses_rules_test.go` — NEW file with mock SESReceiptRuleAPI for unit testing checkSESRules in isolation (mirrors pattern in `doctor_slack_inbound_test.go`)
- [ ] `pkg/compiler/userdata_84_test.go` — NEW file verifying generated userdata uses `${KM_OPERATOR_EMAIL}` env-var ref instead of `operator@` literal (pattern: `userdata_82_02_test.go` for the prefix-aware substring assertions)
- [ ] `pkg/aws/ses_test.go` — extend with `TestSendCreateNotification_OperatorAddressUsesPrefix`
- [ ] CI grep-based check for SES-82.1-REMOVAL — add to `Makefile` test target: `! grep -rn "KM_SES_ACTIVATE_RULESET\|activate_rule_set" infra/ internal/ pkg/ cmd/ OPERATOR-GUIDE.md CLAUDE.md || (echo "Phase 82.1 leftovers found"; exit 1)`
- [ ] No new Go module dependency required if `checkSESRules` is gated behind an interface; production wiring adds `aws-sdk-go-v2/service/ses` to go.mod via `go get github.com/aws/aws-sdk-go-v2/service/ses`

## Sources

### Primary (HIGH confidence)

- [AWS SES Receipt Rules — Email-receiving process](https://docs.aws.amazon.com/ses/latest/dg/receiving-email-concepts.html) — "all of the receipt rules within your active rule set are applied in the order you've defined"
- [AWS SES Receipt Rules Console Walkthrough](https://docs.aws.amazon.com/ses/latest/dg/receiving-email-receipt-rules-console-walkthrough.html) — recipient conditions and ordering
- [AWS CloudFormation::SES::ReceiptRule.Rule](https://docs.aws.amazon.com/AWSCloudFormation/latest/UserGuide/aws-properties-ses-receiptrule-rule.html) — Recipients: "recipient domains and email addresses" (no wildcards documented)
- Codebase reads (exact line numbers verified):
  - `infra/modules/ses/v1.0.0/main.tf:61-71` (current rule set + active resource)
  - `infra/modules/ses/v1.0.0/variables.tf:33-37` (`activate_rule_set` variable)
  - `infra/live/use1/ses/terragrunt.hcl:47` (`KM_SES_ACTIVATE_RULESET` env-var read)
  - `infra/modules/email-handler/v1.0.0/main.tf:261` (`KM_SSM_PREFIX` injection)
  - `internal/app/cmd/configure.go:33,338` (`OperatorEmail` struct field + write)
  - `internal/app/cmd/init.go:600-637` (`ExportConfigEnvVars`)
  - `internal/app/cmd/init.go:140-181` (`regionalModules` ordering, SES last)
  - `internal/app/cmd/bootstrap.go:224-266,776-784` (`km bootstrap` flow)
  - `internal/app/cmd/doctor.go:152-155, 881-927` (existing SES check pattern)
  - `pkg/compiler/userdata.go:1621, 1653` (sandbox-side `km-send` operator literal)
  - `pkg/aws/ses.go:247-298` (`SendCreateNotification` with operator@ literal)
  - `cmd/email-create-handler/main.go:108-138, 141-164, 861` (resourcePrefix, outbound From)
  - `infra/modules/cluster-irsa/v1.0.0/main.tf:51` (`register_oidc_provider = auto` prior art)

### Secondary (MEDIUM confidence)

- [HashiCorp Terraform Issue #24067 — SES rule ordering](https://github.com/hashicorp/terraform-provider-aws/issues/24067) — confirms `after` is the only rule-ordering primitive; no global ordering for cross-state rule sets
- [Terraform aws_ses_receipt_rule docs](https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/ses_receipt_rule) — argument schema (sparse, but confirms `recipients` accepts string list with no wildcard documentation)
- [AWS SDK for Go v2 — service/ses (classic v1)](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/ses) — Inferred from sesv2 module exclusion; classic-v1 SDK module hosts `DescribeReceiptRuleSet`. **Not directly verified in this research — verify SDK availability before relying on the new dependency.**

### Tertiary (LOW confidence)

- AWS Messaging Blog "Manage Incoming Emails at Scale with Amazon SES" (referenced by WebSearch result; NOT directly fetched) — search snippets suggested wildcard syntax `user*@example.com` exists, but that's for **Mail Manager / SES v2** not classic receipt rules. **Treat as misleading.** Classic SES receipt rules: NO wildcards.
- Doc snippet from WebSearch about "Address list with wildcard entries (e.g., user*@example.com)" — verified to refer to SES Mail Manager (newer service), not classic receipt rules. **Discard.**

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — codebase reads exact; AWS provider resources documented in Terraform registry; SDK split (sesv2 vs classic) verified by go.mod inspection.
- Architecture: HIGH on the rule-set string-constant pattern (no Terraform data source exists, verified); MEDIUM on foundation module idempotency strategy (Phase 80 prior art is the recommendation; not exhaustively cross-verified).
- Pitfalls: HIGH on SES SDK confusion, rule_set_name not a Terraform reference, hardcoded operator literals (grep-verified file locations); MEDIUM on catch-all ambiguity (depends on operator decision on Open Question 1).
- AWS SES semantics (all-rules-match-in-order, no wildcards): HIGH — direct AWS dev-guide reads.

**Research date:** 2026-05-16
**Valid until:** 2026-06-15 (30 days — AWS SES receipt-rule semantics are stable; primary risk is the operator-deferred Open Questions)

## RESEARCH COMPLETE
