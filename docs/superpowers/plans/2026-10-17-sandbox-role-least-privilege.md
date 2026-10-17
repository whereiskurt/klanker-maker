# Sandbox Instance Role Least Privilege Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the sandbox instance role's `Allow */*` region lock with a region Deny plus explicit, sandbox-scoped grants, scope SSM parameter reads and their KMS decrypts to the paths the box actually uses, and stop the pollers from writing the `sandbox_id` authorization attribute on the threads tables.

**Architecture:** A new immutable Terraform module `infra/modules/ec2spot/v1.8.0` carries the role; the compiler (`pkg/compiler`) emits two new module inputs (`email_domain`, `otp_secret_paths`) and rewrites the pollers' DynamoDB calls to fit the IAM conditions; guard tests in `pkg/terragrunt` pin the invariants; `km validate` and `km doctor` gain one WARN each; docs and memory record the closed hole. The canary is `ir-bot`, recreated after the apply.

**Tech Stack:** Terraform HCL (jsonencode IAM documents), Go 1.25 (`pkg/compiler`, `pkg/profile`, `pkg/terragrunt`, `internal/app/cmd`), bash userdata rendered by Go templates, AWS CLI v2 on the box.

**Spec:** `docs/superpowers/specs/2026-10-17-sandbox-role-least-privilege-design.md` (read it first; §3 is the grant table every task below implements).

**Census:** `/private/tmp/claude-501/-Users-khundeck-working-klankrmkr/975c00a8-dda7-4f6c-87ad-24d8b25c312e/scratchpad/region-lock-census.md` (every runtime AWS call with file:line; the module in Task 1 must cover each row of its §A table).

## Global Constraints

- New module directory only: `infra/modules/ec2spot/v1.8.0`. Never edit `v1.7.0` or any older version.
- The ec2spot pin has THREE tracking sites that move together: `infra/templates/sandbox/terragrunt.hcl` (`substrate_module_versions.ec2spot`), `pkg/compiler/ec2spot_timeout_test.go` (`ec2spotModuleDir`), `pkg/terragrunt/substrate_version_pin_test.go` (`TestSubstrateVersionPinResolvesPerSubstrate`).
- Region Deny `NotAction` list is exactly: `iam:*, sts:*, organizations:*, support:*, health:*, trustedadvisor:*, cloudfront:*, waf:*, shield:*, route53:*, route53domains:*, budgets:*, ce:*, cur:*, globalaccelerator:*, networkmanager:*, pricing:*, bedrock:*, bedrock-mantle:*, aws-marketplace:*, s3:GetAccountPublicAccessBlock, s3:ListAllMyBuckets, s3:PutAccountPublicAccessBlock` (the SCP `DenyOutsideRegion` list in `infra/modules/scp/v2.0.0/main.tf:196-218` plus `bedrock-mantle:*` and `aws-marketplace:*`).
- `sandbox_id` must not appear in any `dynamodb:Attributes` set granted to the sandbox role, and no poller update expression may SET it.
- Slack threads attribute set: `channel_id, thread_ts, claude_session_id, agent_type, last_assistant_msg, last_turn_ts, last_processed_event_ts, ttl_expiry`. GitHub: `repo, number, agent_session_id, agent_type`. H1: `report_id, target, agent_session_id, agent_type`.
- IAM aggregate inline-policy size per role is 10,240 characters (whitespace excluded). If the canary apply fails with `LimitExceeded … Maximum policy size`, reduce in this order: drop `Sid`s, then collapse the `kms:EncryptionContext:PARAMETER_ARN` list to the single `parameter/{p}/sandbox/{id}/*` entry. Record the choice in the ledger.
- Frozen golden `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh` is hand-patched, never recaptured (`CAPTURE_PRE92_BASELINE=1` corrupts it). `userdata_additional_volume_only.golden.sh` is regenerated with `CAPTURE_ADDVOL_GOLDEN=1`; `h1_byte_identity_golden.txt` with `CAPTURE_PRE_H1_BASELINE=1`.
- Commit by pathspec only (`git add <files>`), never `git add -A`. Every commit message ends with:
  `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` and `Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt`.
- Docs use generic placeholders (`example.com`, synthetic account ids). The operator's real address is never committed.
- Run `go test ./pkg/terragrunt/ ./pkg/compiler/ ./pkg/profile/` after each task; capture the command's own exit code, never `| tail`.
- Do not deploy, apply, or touch AWS from any task except Task 10, which the controller runs.

## File map

| File | Responsibility |
|---|---|
| `infra/modules/ec2spot/v1.8.0/{main,variables,outputs}.tf` | the role and every grant (Task 1) |
| `pkg/terragrunt/ec2spot_role_grants_test.go` | structural assertions on the pinned module: no Allow-star, region Deny shape, no managed Core, scoped SSM (Task 1) |
| `pkg/terragrunt/slack_threads_sandbox_role_guard_test.go` | rewritten: threads-table grants must carry an attribute condition without `sandbox_id`, scoped to the pinned module (Task 2) |
| `pkg/terragrunt/sandbox_role_call_coverage_test.go` | every `aws <svc> <op>` in `userdata.go` and every SDK action the sidecars use has an Allow in the pinned module (Task 3) |
| `pkg/compiler/userdata.go` | poller DynamoDB calls, `km-` fallbacks (Tasks 4, 5) |
| `pkg/compiler/service_hcl.go` | `email_domain`, `otp_secret_paths` module inputs (Task 6) |
| `pkg/profile/validate.go` | `aws` in initCommands WARN (Task 7) |
| `internal/app/cmd/doctor_sandbox_role.go` | `km doctor` WARN for running sandboxes still on the v1.7.0 role shape (Task 8) |
| docs, CLAUDE.md, memory | Task 9 |

---

### Task 1: Module `infra/modules/ec2spot/v1.8.0` — region Deny + explicit grants

**Files:**
- Create: `infra/modules/ec2spot/v1.8.0/main.tf`, `variables.tf`, `outputs.tf` (copies of v1.7.0, then edited as below)
- Test: `pkg/terragrunt/ec2spot_role_grants_test.go` (new)

**Interfaces:**
- Consumes: module inputs already emitted by the compiler (`sandbox_id`, `resource_prefix`, `region_label`, `artifacts_bucket`, `slack_stream_messages_table_name`, `secret_paths`, `fence_imds`, `enable_bedrock`, `iam_session_policy.allowed_regions`).
- Produces: two NEW inputs Task 6 emits: `variable "email_domain"` (string, default `""`) and `variable "otp_secret_paths"` (list(string), default `[]`). Outputs unchanged.

- [ ] **Step 1: Copy the module**

```bash
cp -R infra/modules/ec2spot/v1.7.0 infra/modules/ec2spot/v1.8.0
ls infra/modules/ec2spot/v1.8.0   # main.tf outputs.tf variables.tf, no .terraform* files
```

- [ ] **Step 2: Write the failing structural test**

Create `pkg/terragrunt/ec2spot_role_grants_test.go`. It reads `infra/modules/ec2spot/v1.8.0/main.tf` directly (the pin moves in Task 2; this file names the version so Task 1 is testable before the pin bump — Task 2 switches it to the pinned module).

```go
package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ec2spotGrantsModuleDir is switched to the live pin in Task 2 of the
// least-privilege plan; until then it names the module under construction.
const ec2spotGrantsModuleDir = "infra/modules/ec2spot/v1.8.0"

func readEC2SpotMainTF(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(findRepoRoot(t), filepath.FromSlash(ec2spotGrantsModuleDir), "main.tf"))
	if err != nil {
		t.Fatalf("read module: %v", err)
	}
	return tfCommentRe.ReplaceAllString(string(raw), "")
}

// Every IAM statement block in the file: the innermost {...} around each Action/NotAction key.
func iamStatements(t *testing.T, s string) []string {
	t.Helper()
	seen := map[int]bool{}
	var out []string
	for _, m := range tfActionKeyRe.FindAllStringIndex(s, -1) {
		a, b := enclosingBlock(s, m[0])
		if a == -1 || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, s[a:b])
	}
	if len(out) == 0 {
		t.Fatal("no IAM statements found; the parser has drifted from the module")
	}
	return out
}

func TestEC2SpotRole_NoAllowStar(t *testing.T) {
	s := readEC2SpotMainTF(t)
	allowStar := regexp.MustCompile(`Action\s*=\s*"\*"`)
	for _, stmt := range iamStatements(t, s) {
		if strings.Contains(stmt, `Effect = "Deny"`) || strings.Contains(stmt, `Effect   = "Deny"`) {
			continue
		}
		if allowStar.MatchString(stmt) {
			t.Errorf("an Allow statement grants Action \"*\": %s", stmt)
		}
	}
	if strings.Contains(s, `"ec2spot_region_lock"`) {
		t.Error("ec2spot_region_lock (the Allow-star) must not exist in v1.8.0")
	}
	if strings.Contains(s, "AmazonSSMManagedInstanceCore") {
		t.Error("the managed AmazonSSMManagedInstanceCore attachment grants ssm:GetParameter* on *; v1.8.0 carries an inline copy without it")
	}
}

func TestEC2SpotRole_RegionDenyShape(t *testing.T) {
	s := readEC2SpotMainTF(t)
	var deny string
	for _, stmt := range iamStatements(t, s) {
		if regexp.MustCompile(`Effect\s*=\s*"Deny"`).MatchString(stmt) && strings.Contains(stmt, "aws:RequestedRegion") {
			deny = stmt
		}
	}
	if deny == "" {
		t.Fatal("no Deny statement conditioned on aws:RequestedRegion")
	}
	if !strings.Contains(deny, "NotAction") || !strings.Contains(deny, "StringNotEquals") {
		t.Errorf("region deny must be NotAction + StringNotEquals: %s", deny)
	}
	for _, exempt := range []string{`"sts:*"`, `"iam:*"`, `"bedrock:*"`, `"bedrock-mantle:*"`, `"aws-marketplace:*"`} {
		if !strings.Contains(deny, exempt) {
			t.Errorf("region deny NotAction is missing %s (cross-region Bedrock inference and the IMDS-fence self-assume both need it)", exempt)
		}
	}
}

func TestEC2SpotRole_SSMParameterReadsAreScoped(t *testing.T) {
	s := readEC2SpotMainTF(t)
	getParam := regexp.MustCompile(`"ssm:GetParameters?"`)
	for _, stmt := range iamStatements(t, s) {
		if !getParam.MatchString(stmtField(stmt, "Action")) {
			continue
		}
		res := stmtField(stmt, "Resource")
		if strings.Contains(res, `"*"`) || !strings.Contains(res, "parameter") {
			t.Errorf("ssm:GetParameter must be granted on parameter ARNs only, never *: %s", stmt)
		}
	}
	kmsVia := false
	for _, stmt := range iamStatements(t, s) {
		if strings.Contains(stmt, `"kms:Decrypt"`) && strings.Contains(stmt, "kms:ViaService") {
			kmsVia = true
			if !strings.Contains(stmt, "kms:EncryptionContext:PARAMETER_ARN") {
				t.Errorf("the SSM-path kms:Decrypt must be pinned to kms:EncryptionContext:PARAMETER_ARN: %s", stmt)
			}
		}
	}
	if !kmsVia {
		t.Error("no kms:Decrypt statement conditioned on kms:ViaService = ssm (SecureString reads would fail)")
	}
}

func TestEC2SpotRole_NewInputsDeclared(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(findRepoRoot(t), filepath.FromSlash(ec2spotGrantsModuleDir), "variables.tf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{`variable "email_domain"`, `variable "otp_secret_paths"`} {
		if !strings.Contains(string(raw), v) {
			t.Errorf("variables.tf missing %s", v)
		}
	}
}
```

`tfCommentRe`, `tfActionKeyRe`, `enclosingBlock`, `stmtField` and `findRepoRoot` already exist in package `terragrunt_test` (`slack_threads_sandbox_role_guard_test.go`, `substrate_version_pin_test.go`).

- [ ] **Step 3: Run it to verify it fails**

Run: `cd pkg/terragrunt && go test . -run 'TestEC2SpotRole_' -v; echo exit=$?`
Expected: FAIL — `ec2spot_region_lock` present, `AmazonSSMManagedInstanceCore` present, `variables.tf missing`.

- [ ] **Step 4: Add the two variables**

Append to `infra/modules/ec2spot/v1.8.0/variables.tf`:

```hcl
# ---------------------------------------------------------------------------
# v1.8.0 — least privilege (region Deny + explicit grants)
# ---------------------------------------------------------------------------

# The sandboxes email domain ({email_subdomain}.{domain}, e.g. "sandboxes.example.com").
# The SES send grant is scoped to this domain identity and to From addresses the
# box legitimately uses ({sandbox_id}@domain, the alias display-name form, and
# notifications@domain). Empty = no SES grant (km-send then fails loudly).
variable "email_domain" {
  type        = string
  description = "Sandbox email domain the role may send from (spec.notification / install email_subdomain). Empty = no ses:SendEmail grant."
  default     = ""
}

# spec.otp.secrets: one-time parameters the bootstrap reads once then deletes.
# Absolute SSM paths, already resolved. Each gets ssm:GetParameter AND
# ssm:DeleteParameter (the delete used to ride on the Allow-star).
variable "otp_secret_paths" {
  type        = list(string)
  description = "Absolute SSM parameter paths from spec.otp.secrets; granted GetParameter + DeleteParameter. Empty = nothing (default)."
  default     = []
}
```

- [ ] **Step 5: Replace the IAM policy block in main.tf**

In `infra/modules/ec2spot/v1.8.0/main.tf` DELETE these whole resources (each runs from its `resource "aws_iam_…"` line to its closing `})` + `}`): `aws_iam_role_policy.ec2spot_region_lock`, `aws_iam_role_policy_attachment.ec2spot_ssm`, `aws_iam_role_policy.ec2spot_budget_dynamo`, `aws_iam_role_policy.ec2spot_github_token`, `aws_iam_role_policy.ec2spot_slack_inbound_sqs`, `aws_iam_role_policy.ec2spot_github_inbound_sqs`, `aws_iam_role_policy.ec2spot_slack_transcript_s3`, and (near the end of the file) `aws_iam_role_policy.ec2spot_profile_secret_paths`. KEEP unchanged: `ec2spot_eventbridge`, `ec2spot_bedrock`, `ec2spot_slack_transcript_ddb`, `ec2spot_sandbox_secrets_kms`, `ec2spot_fence_self_assume`, `ec2spot_sandbox_secrets_s3`, `aws_iam_instance_profile.ec2spot`, and the role itself.

Insert the following block where `ec2spot_region_lock` was (immediately after `resource "aws_iam_role" "ec2spot_ssm"`):

```hcl
# ============================================================
# v1.8.0 — least privilege. Every grant below names the sandbox ({id}) or the
# install ({prefix}); nothing is granted that no on-box code calls. The census
# behind each statement is docs/superpowers/specs/2026-10-17-sandbox-role-least-privilege-design.md §3.
# ============================================================

locals {
  acct = data.aws_caller_identity.current.account_id
  reg  = data.aws_region.current.name

  # Every SSM parameter the box reads. The same list pins the KMS decrypt below
  # through the SecureString encryption context, so the grant can never widen
  # to a parameter this list does not name.
  ssm_param_arns = concat(
    [
      "arn:aws:ssm:*:${local.acct}:parameter/${var.resource_prefix}/sandbox/${var.sandbox_id}/*",
      "arn:aws:ssm:*:${local.acct}:parameter/${var.resource_prefix}/slack/bridge-url",
      "arn:aws:ssm:*:${local.acct}:parameter/${var.resource_prefix}/config/remote-create/safe-phrase",
      "arn:aws:ssm:*:${local.acct}:parameter/${var.resource_prefix}/config/h1/api-username",
      "arn:aws:ssm:*:${local.acct}:parameter/${var.resource_prefix}/config/h1/api-token",
    ],
    [for p in var.secret_paths : "arn:aws:ssm:*:${local.acct}:parameter${p}"],
    [for p in var.otp_secret_paths : "arn:aws:ssm:*:${local.acct}:parameter${p}"],
  )
  otp_param_arns = [for p in var.otp_secret_paths : "arn:aws:ssm:*:${local.acct}:parameter${p}"]
  ddb_table_arn  = "arn:aws:dynamodb:${local.reg}:${local.acct}:table"
}

# Region lock is a DENY, not an Allow. The NotAction list is the SCP's
# DenyOutsideRegion exemption list plus bedrock-mantle and aws-marketplace:
# Bedrock cross-region inference profiles route to other regions and the
# IMDS-fence self-assume is STS.
resource "aws_iam_role_policy" "ec2spot_region_deny" {
  count = (local.create_instance_role && length(var.iam_session_policy.allowed_regions) > 0) ? 1 : 0

  name = "${var.resource_prefix}-ec2spot-region-deny-${var.region_label}"
  role = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Deny"
      NotAction = [
        "iam:*", "sts:*", "organizations:*", "support:*", "health:*", "trustedadvisor:*",
        "cloudfront:*", "waf:*", "shield:*", "route53:*", "route53domains:*", "budgets:*",
        "ce:*", "cur:*", "globalaccelerator:*", "networkmanager:*", "pricing:*",
        "bedrock:*", "bedrock-mantle:*", "aws-marketplace:*",
        "s3:GetAccountPublicAccessBlock", "s3:ListAllMyBuckets", "s3:PutAccountPublicAccessBlock",
      ]
      Resource = "*"
      Condition = {
        StringNotEquals = { "aws:RequestedRegion" = var.iam_session_policy.allowed_regions }
      }
    }]
  })
}

# Inline copy of AmazonSSMManagedInstanceCore (policy v2, verified live 2026-10-17)
# MINUS ssm:GetParameter and ssm:GetParameters, which that policy grants on "*"
# and which let any box read and decrypt every parameter in the account.
resource "aws_iam_role_policy" "ec2spot_ssm_core" {
  count = local.create_instance_role ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-ssm-core"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "ssm:DescribeAssociation", "ssm:GetDeployablePatchSnapshotForInstance", "ssm:GetDocument",
          "ssm:DescribeDocument", "ssm:GetManifest", "ssm:ListAssociations", "ssm:ListInstanceAssociations",
          "ssm:PutInventory", "ssm:PutComplianceItems", "ssm:PutConfigurePackageResult",
          "ssm:UpdateAssociationStatus", "ssm:UpdateInstanceAssociationStatus", "ssm:UpdateInstanceInformation",
        ]
        Resource = "*"
      },
      {
        Effect   = "Allow"
        Action   = ["ssmmessages:CreateControlChannel", "ssmmessages:CreateDataChannel", "ssmmessages:OpenControlChannel", "ssmmessages:OpenDataChannel"]
        Resource = "*"
      },
      {
        Effect   = "Allow"
        Action   = ["ec2messages:AcknowledgeMessage", "ec2messages:DeleteMessage", "ec2messages:FailMessage", "ec2messages:GetEndpoint", "ec2messages:GetMessages", "ec2messages:SendReply"]
        Resource = "*"
      },
    ]
  })
}

# Parameter reads, per path. Replaces ec2spot_github_token's SSM statements and
# ec2spot_profile_secret_paths; adds the two config paths that rode on Core.
resource "aws_iam_role_policy" "ec2spot_ssm_params" {
  count = local.create_instance_role ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-ssm-params"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat(
      [{
        Effect   = "Allow"
        Action   = ["ssm:GetParameter"]
        Resource = local.ssm_param_arns
      }],
      length(local.otp_param_arns) > 0 ? [{
        Effect   = "Allow"
        Action   = ["ssm:DeleteParameter"]
        Resource = local.otp_param_arns
      }] : []
    )
  })
}

# SecureString decrypt, pinned to the parameters above: SSM calls KMS with the
# parameter ARN as encryption context, so the key need not be named.
resource "aws_iam_role_policy" "ec2spot_ssm_kms" {
  count = local.create_instance_role ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-ssm-kms"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["kms:Decrypt"]
      Resource = ["arn:aws:kms:*:${local.acct}:key/*"]
      Condition = {
        StringEquals = { "kms:ViaService" = "ssm.${local.reg}.amazonaws.com" }
        StringLike   = { "kms:EncryptionContext:PARAMETER_ARN" = local.ssm_param_arns }
      }
    }]
  })
}

# Artifacts bucket: reads the boot and the sidecars depend on, the shared
# prefixes (mail/, rsync/, models/, binaries/), and this sandbox's own keys.
resource "aws_iam_role_policy" "ec2spot_artifacts_read" {
  count = (local.create_instance_role && var.artifacts_bucket != "") ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-artifacts-read"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = ["s3:GetObject"]
        Resource = [
          "arn:aws:s3:::${var.artifacts_bucket}/sidecars/*",
          "arn:aws:s3:::${var.artifacts_bucket}/binaries/*",
          "arn:aws:s3:::${var.artifacts_bucket}/artifacts/${var.sandbox_id}/*",
          "arn:aws:s3:::${var.artifacts_bucket}/slack-inbound/${var.sandbox_id}/*",
          "arn:aws:s3:::${var.artifacts_bucket}/rsync/*",
          "arn:aws:s3:::${var.artifacts_bucket}/mail/*",
          "arn:aws:s3:::${var.artifacts_bucket}/models/*",
        ]
      },
      {
        Effect   = "Allow"
        Action   = ["s3:ListBucket"]
        Resource = "arn:aws:s3:::${var.artifacts_bucket}"
        Condition = {
          StringLike = { "s3:prefix" = ["mail/", "mail/*", "artifacts/${var.sandbox_id}/*", "models/*"] }
        }
      },
    ]
  })
}

# Artifacts bucket: writes, all under this sandbox's own prefixes. Replaces
# ec2spot_slack_transcript_s3 and adds the four prefixes that rode on the Allow-star.
resource "aws_iam_role_policy" "ec2spot_artifacts_write" {
  count = (local.create_instance_role && var.artifacts_bucket != "") ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-artifacts-write"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = ["s3:PutObject", "s3:AbortMultipartUpload"]
        Resource = [
          for p in ["transcripts", "captures", "learn", "execs", "artifacts", "traces", "logs", "metrics"] :
          "arn:aws:s3:::${var.artifacts_bucket}/${p}/${var.sandbox_id}/*"
        ]
      },
      {
        Effect   = "Allow"
        Action   = ["s3:DeleteObject"]
        Resource = "arn:aws:s3:::${var.artifacts_bucket}/artifacts/${var.sandbox_id}/clone-workspace.tar.gz"
      },
    ]
  })
}

# Audit stream (km-audit-log) and the idle detector's reads, on this sandbox's
# own log group. The trailing * covers the group and its streams.
resource "aws_iam_role_policy" "ec2spot_audit_logs" {
  count = local.create_instance_role ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-audit-logs"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["logs:CreateLogGroup", "logs:PutRetentionPolicy", "logs:CreateLogStream", "logs:PutLogEvents", "logs:GetLogEvents"]
      Resource = "arn:aws:logs:${local.reg}:${local.acct}:log-group:/${var.resource_prefix}/sandboxes/${var.sandbox_id}/*"
    }]
  })
}

# km-send and the spot-interruption notice. The domain identity is the sending
# identity; the From condition pins the box to its own address (bare and in the
# `"alias" <addr>` form km-send renders) and to notifications@.
resource "aws_iam_role_policy" "ec2spot_ses_send" {
  count = (local.create_instance_role && var.email_domain != "") ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-ses-send"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["ses:SendEmail", "ses:SendRawEmail"]
      Resource = "arn:aws:ses:${local.reg}:${local.acct}:identity/${var.email_domain}"
      Condition = {
        StringLike = {
          "ses:FromAddress" = [
            "${var.sandbox_id}@${var.email_domain}",
            "*<${var.sandbox_id}@${var.email_domain}>",
            "notifications@${var.email_domain}",
          ]
        }
      }
    }]
  })
}

# The four per-sandbox inbound FIFO queues (Slack, GitHub, H1, webhook). H1 and
# webhook had no grant at all before v1.8.0; their pollers worked through the
# Allow-star only.
resource "aws_iam_role_policy" "ec2spot_inbound_sqs" {
  count = local.create_instance_role ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-inbound-sqs"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes", "sqs:GetQueueUrl", "sqs:ChangeMessageVisibility"]
      Resource = [
        for src in ["slack", "github", "h1", "webhook"] :
        "arn:aws:sqs:*:${local.acct}:${var.resource_prefix}-${src}-inbound-${var.sandbox_id}.fifo"
      ]
    }]
  })
}

# Threads tables: the pollers keep session continuity here, but sandbox_id is
# the bridge's authorization attribute and is in NO attribute set below, so a
# box cannot write it (the forged-row hole of docs/slack-default-route.md §6.4).
# dynamodb:Attributes needs ReturnValues pinned on writes and Select on reads;
# the pollers pass --return-values NONE and --projection-expression accordingly.
resource "aws_iam_role_policy" "ec2spot_threads_rw" {
  count = local.create_instance_role ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-threads-rw"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["dynamodb:GetItem", "dynamodb:UpdateItem"]
        Resource = "${local.ddb_table_arn}/${var.resource_prefix}-slack-threads"
        Condition = {
          "ForAllValues:StringEquals" = {
            "dynamodb:Attributes" = ["channel_id", "thread_ts", "claude_session_id", "agent_type", "last_assistant_msg", "last_turn_ts", "last_processed_event_ts", "ttl_expiry"]
          }
          StringEqualsIfExists = {
            "dynamodb:Select"       = "SPECIFIC_ATTRIBUTES"
            "dynamodb:ReturnValues" = ["NONE", "UPDATED_OLD", "UPDATED_NEW"]
          }
        }
      },
      {
        Effect   = "Allow"
        Action   = ["dynamodb:GetItem", "dynamodb:UpdateItem"]
        Resource = "${local.ddb_table_arn}/${var.resource_prefix}-github-threads"
        Condition = {
          "ForAllValues:StringEquals" = { "dynamodb:Attributes" = ["repo", "number", "agent_session_id", "agent_type"] }
          StringEqualsIfExists = {
            "dynamodb:Select"       = "SPECIFIC_ATTRIBUTES"
            "dynamodb:ReturnValues" = ["NONE", "UPDATED_OLD", "UPDATED_NEW"]
          }
        }
      },
      {
        Effect   = "Allow"
        Action   = ["dynamodb:GetItem", "dynamodb:UpdateItem"]
        Resource = "${local.ddb_table_arn}/${var.resource_prefix}-h1-threads"
        Condition = {
          "ForAllValues:StringEquals" = { "dynamodb:Attributes" = ["report_id", "target", "agent_session_id", "agent_type"] }
          StringEqualsIfExists = {
            "dynamodb:Select"       = "SPECIFIC_ATTRIBUTES"
            "dynamodb:ReturnValues" = ["NONE", "UPDATED_OLD", "UPDATED_NEW"]
          }
        }
      },
    ]
  })
}

# km-recv signature verification reads the sender's public key.
resource "aws_iam_role_policy" "ec2spot_identities_read" {
  count = local.create_instance_role ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-identities-read"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["dynamodb:GetItem"]
      Resource = "${local.ddb_table_arn}/${var.resource_prefix}-identities"
    }]
  })
}

# Budget metering (http-proxy), the idle re-arm read on the sandboxes row, and
# the action-quota counters. Budgets/sandboxes are exactly v1.7.0's
# ec2spot_budget_dynamo; action-quota is new and keyed {sandbox}#{action}, so
# LeadingKeys pins it to this sandbox's own rows.
resource "aws_iam_role_policy" "ec2spot_budget_quota" {
  count = local.create_instance_role ? 1 : 0
  name  = "${var.resource_prefix}-${var.sandbox_id}-budget-quota"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = ["dynamodb:GetItem", "dynamodb:UpdateItem", "dynamodb:Query"]
        Resource = [
          "${local.ddb_table_arn}/${var.resource_prefix}-budgets",
          "${local.ddb_table_arn}/${var.resource_prefix}-budgets/index/*",
        ]
      },
      {
        Effect   = "Allow"
        Action   = ["dynamodb:GetItem"]
        Resource = "${local.ddb_table_arn}/${var.resource_prefix}-sandboxes"
      },
      {
        Effect   = "Allow"
        Action   = ["dynamodb:UpdateItem"]
        Resource = "${local.ddb_table_arn}/${var.resource_prefix}-action-quota"
        Condition = {
          "ForAllValues:StringLike" = { "dynamodb:LeadingKeys" = ["${var.sandbox_id}#*"] }
        }
      },
    ]
  })
}
```

Also update the header comment of the file (line 1 area is data sources; add one comment line above `resource "aws_iam_role" "ec2spot_ssm"`): `# v1.8.0: least-privilege role — see the v1.8.0 block below; v1.7.0's Allow-star region lock and managed Core attachment are gone.`

- [ ] **Step 6: Validate the HCL with terraform**

```bash
cd infra/modules/ec2spot/v1.8.0 && terraform init -backend=false -input=false >/dev/null && terraform validate; echo exit=$?
cd - && find infra/modules/ec2spot/v1.8.0 -name '.terraform*' -prune -exec rm -rf {} + 2>/dev/null; git status --short infra/modules/ec2spot/v1.8.0
```
Expected: `Success! The configuration is valid.` and no `.terraform` / `.terraform.lock.hcl` left in the module dir (memory: module-source lock drift). Use `find -delete` if the `rm` guard intercepts.

- [ ] **Step 7: Run the structural test**

Run: `cd pkg/terragrunt && go test . -run 'TestEC2SpotRole_|TestModule' -v; echo exit=$?`
Expected: PASS. (`TestSandboxRole_HasNoWriteGrantOnSlackThreadsTable` is expected to FAIL now on v1.8.0's threads grant — Task 2 rewrites it. Report it, do not "fix" it here.)

- [ ] **Step 8: Commit**

```bash
git add infra/modules/ec2spot/v1.8.0 pkg/terragrunt/ec2spot_role_grants_test.go
git commit -m "feat(ec2spot): v1.8.0 — region Deny + explicit sandbox-scoped grants, scoped SSM reads

Replaces the Allow-*/* region lock with a NotAction Deny, drops the managed
AmazonSSMManagedInstanceCore attachment for an inline copy without
ssm:GetParameter*, pins kms:Decrypt to the parameter ARNs via encryption
context, and grants every census call explicitly (artifacts, logs, SES, the
four inbound queues, threads tables with attribute conditions that exclude
sandbox_id, identities, action-quota by LeadingKeys). Not yet pinned.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 2: Rewrite the threads guard and move the pin to v1.8.0

**Files:**
- Modify: `pkg/terragrunt/slack_threads_sandbox_role_guard_test.go` (whole file rewritten)
- Modify: `infra/templates/sandbox/terragrunt.hcl:29` (`ec2spot = "v1.7.0"` → `"v1.8.0"`)
- Modify: `pkg/compiler/ec2spot_timeout_test.go:21` (`ec2spotModuleDir`)
- Modify: `pkg/terragrunt/substrate_version_pin_test.go` (the `v1.7.0` expectation in `TestSubstrateVersionPinResolvesPerSubstrate`)
- Modify: `pkg/terragrunt/ec2spot_role_grants_test.go` (switch `ec2spotGrantsModuleDir` to read the live pin)

**Interfaces:**
- Produces: `pinnedEC2SpotModuleDir(t) string` in package `terragrunt_test` — reads `substrate_module_versions.ec2spot` from the sandbox template (via the existing `parseSandboxTemplate` + `extractSubstrateVersionMap`) and returns `infra/modules/ec2spot/<pin>`. Task 3 uses it.

- [ ] **Step 1: Rewrite the guard test (it must fail against v1.7.0 if pointed there, and pass against v1.8.0)**

Replace the body of `pkg/terragrunt/slack_threads_sandbox_role_guard_test.go` from `var slackThreadsGuardKnownWildcardPolicies` down to the end with:

```go
// pinnedEC2SpotModuleDir returns the repo-relative directory of the ec2spot
// module version sandboxes render against today. Older versions stay on disk
// as history and are deliberately out of scope: the invariant is about the
// role a NEW sandbox gets.
func pinnedEC2SpotModuleDir(t *testing.T) string {
	t.Helper()
	versions := extractSubstrateVersionMap(t, parseSandboxTemplate(t, findRepoRoot(t)))
	pin, ok := versions["ec2spot"]
	if !ok {
		t.Fatal("sandbox template has no ec2spot pin")
	}
	return "infra/modules/ec2spot/" + pin
}

// TestSandboxRole_ThreadsWriteGrantExcludesSandboxID replaces the pre-v1.8.0
// "no grant at all" guard. The sandbox role MAY read and update the threads
// tables for session continuity, but only under a dynamodb:Attributes
// condition that names each attribute it may touch, and sandbox_id (the
// bridge's authorization attribute) must not be in that set. Any Allow on a
// threads table without the condition, any set containing sandbox_id, and any
// Allow of DynamoDB writes on a wildcard resource fail the build.
func TestSandboxRole_ThreadsWriteGrantExcludesSandboxID(t *testing.T) {
	dir := filepath.Join(findRepoRoot(t), filepath.FromSlash(pinnedEC2SpotModuleDir(t)))
	files, err := filepath.Glob(filepath.Join(dir, "*.tf"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no .tf files under %s (err=%v)", dir, err)
	}
	sort.Strings(files)
	attrsRe := regexp.MustCompile(`"dynamodb:Attributes"\s*=\s*\[([^\]]*)\]`)
	sawThreads := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := tfCommentRe.ReplaceAllString(string(raw), "")
		seen := map[int]bool{}
		for _, m := range tfActionKeyRe.FindAllStringIndex(s, -1) {
			a, b := enclosingBlock(s, m[0])
			if a == -1 || seen[a] {
				continue
			}
			seen[a] = true
			stmt := s[a:b]
			if regexp.MustCompile(`Effect\s*=\s*"Deny"`).MatchString(stmt) {
				continue
			}
			policy := owningPolicy(s, a)
			actions := stmtField(stmt, "Action") + stmtField(stmt, "NotAction")
			resources := stmtField(stmt, "Resource") + stmtField(stmt, "NotResource")
			if ddbWriteRe.MatchString(actions) && wildcardResRe.MatchString(resources) {
				t.Errorf("%s (policy %q): DynamoDB write on a wildcard resource reaches every threads table", filepath.Base(f), policy)
			}
			if !threadsTableRe.MatchString(stmt) {
				continue
			}
			sawThreads++
			am := attrsRe.FindStringSubmatch(stmt)
			if am == nil {
				t.Errorf("%s (policy %q): an Allow on a threads table has no dynamodb:Attributes condition; a box could write sandbox_id and forge a routed-thread row", filepath.Base(f), policy)
				continue
			}
			if strings.Contains(am[1], `"sandbox_id"`) {
				t.Errorf("%s (policy %q): sandbox_id is in the attribute set; the bridge trusts that attribute to authorize posts", filepath.Base(f), policy)
			}
			if !strings.Contains(stmt, "dynamodb:ReturnValues") || !strings.Contains(stmt, "dynamodb:Select") {
				t.Errorf("%s (policy %q): dynamodb:Attributes needs ReturnValues and Select pinned or the condition is bypassable", filepath.Base(f), policy)
			}
		}
	}
	if sawThreads == 0 {
		t.Fatal("no Allow statement names a threads table; the pollers' session continuity would 403 — or the table regex drifted")
	}
}
```

Keep the regex vars (`tfCommentRe` … `wildcardResRe`), `enclosingBlock`, `tfStmtKeyRe`, `stmtField`, `owningPolicy`. Delete `slackThreadsGuardKnownWildcardPolicies` and the old `TestSandboxRole_HasNoWriteGrantOnSlackThreadsTable`. Rewrite the file's top comment: the row is still trusted by `threadRoutedToSender`; what protects it now is that the sandbox role can write only a named attribute set that excludes `sandbox_id`, so a row the box creates has no `sandbox_id` and `LookupSandbox` reads it as unrouted. Add `"strings"` to the imports.

- [ ] **Step 2: Run — expect the pin mismatch**

Run: `cd pkg/terragrunt && go test . -run 'TestSandboxRole_|TestSubstrate' -v; echo exit=$?`
Expected: `TestSandboxRole_ThreadsWriteGrantExcludesSandboxID` FAILS (pin still v1.7.0: its region-lock statement is a wildcard write and there is no threads Allow ⇒ `no Allow statement names a threads table`).

- [ ] **Step 3: Bump the three pin sites**

```bash
sed -i '' 's/ec2spot = "v1.7.0"/ec2spot = "v1.8.0"/' infra/templates/sandbox/terragrunt.hcl
sed -i '' 's#ec2spot/v1.7.0"#ec2spot/v1.8.0"#' pkg/compiler/ec2spot_timeout_test.go
sed -i '' 's/got != "v1.7.0"/got != "v1.8.0"/; s/want v1.6.0/want v1.8.0/' pkg/terragrunt/substrate_version_pin_test.go
grep -n 'v1\.[78]\.0' infra/templates/sandbox/terragrunt.hcl pkg/compiler/ec2spot_timeout_test.go pkg/terragrunt/substrate_version_pin_test.go
```
Append to the comment above `ec2spotModuleDir` in `ec2spot_timeout_test.go`: `, and again when it moved v1.7.0 -> v1.8.0 (least-privilege role: region Deny + explicit grants).`

In `pkg/terragrunt/ec2spot_role_grants_test.go` replace the constant with a call: delete `const ec2spotGrantsModuleDir` and change `readEC2SpotMainTF`/`TestEC2SpotRole_NewInputsDeclared` to use `pinnedEC2SpotModuleDir(t)`.

- [ ] **Step 4: Run the two packages**

Run: `cd pkg/terragrunt && go test . ; echo exit=$?` then `cd ../compiler && go test . -run 'TestEC2S|TestEC2Spot'; echo exit=$?`
Expected: both exit=0. `TestEC2ServiceHCL_SpotTimeout`/`_OnDemandBoundedTimeout` now read v1.8.0 (the copied resource blocks still carry the timeouts).

- [ ] **Step 5: Commit**

```bash
git add pkg/terragrunt/slack_threads_sandbox_role_guard_test.go pkg/terragrunt/ec2spot_role_grants_test.go pkg/terragrunt/substrate_version_pin_test.go pkg/compiler/ec2spot_timeout_test.go infra/templates/sandbox/terragrunt.hcl
git commit -m "feat(ec2spot): pin v1.8.0 at all three sites; threads guard now asserts the attribute condition excludes sandbox_id

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 3: Call-coverage guard — every box-side AWS call has a grant in the pinned module

**Files:**
- Create: `pkg/terragrunt/sandbox_role_call_coverage_test.go`

**Interfaces:**
- Consumes: `pinnedEC2SpotModuleDir(t)`, `tfCommentRe`, `tfActionKeyRe`, `enclosingBlock`, `stmtField` (Tasks 1–2).

- [ ] **Step 1: Write the test**

```go
package terragrunt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// cliActionMap maps an `aws <svc> <op>` CLI call found in the rendered userdata
// to the IAM action(s) it needs. An op absent from the map fails the test, so a
// new CLI call in userdata.go forces someone to decide its grant.
var cliActionMap = map[string][]string{
	"s3 cp":                         {"s3:GetObject", "s3:PutObject"}, // direction is not knowable from text; both must be granted somewhere
	"s3 ls":                         {"s3:ListBucket"},
	"s3 rm":                         {"s3:DeleteObject"},
	"s3 sync":                       {"s3:GetObject", "s3:ListBucket"},
	"ssm get-parameter":             {"ssm:GetParameter"},
	"ssm delete-parameter":          {"ssm:DeleteParameter"},
	"sqs receive-message":           {"sqs:ReceiveMessage"},
	"sqs delete-message":            {"sqs:DeleteMessage"},
	"sqs change-message-visibility": {"sqs:ChangeMessageVisibility"},
	"dynamodb get-item":             {"dynamodb:GetItem"},
	"dynamodb update-item":          {"dynamodb:UpdateItem"},
	"dynamodb put-item":             {"dynamodb:PutItem"},
	"sesv2 send-email":              {"ses:SendEmail"},
	"sts get-caller-identity":       {}, // needs no permission
}

// sdkActions are the calls the Go sidecars and helpers make through the SDK,
// with the source that makes them. Maintained by hand because the SDK call
// sites are spread over several binaries; the census file names each.
var sdkActions = map[string]string{
	"logs:CreateLogGroup":     "pkg/aws/cloudwatch.go EnsureLogGroup (km-audit-log)",
	"logs:PutRetentionPolicy": "pkg/aws/cloudwatch.go EnsureLogGroup",
	"logs:CreateLogStream":    "pkg/aws/cloudwatch.go EnsureLogGroup",
	"logs:PutLogEvents":       "pkg/aws/cloudwatch.go PutLogEvents",
	"logs:GetLogEvents":       "pkg/lifecycle/idle.go via cloudwatch.go",
	"events:PutEvents":        "sidecars/audit-log/cmd/main.go",
	"dynamodb:Query":          "pkg/aws/budget.go (http-proxy metering)",
	"dynamodb:PutItem":        "cmd/km-slack record-mapping (stream-messages table)",
	"kms:Decrypt":             "SSM SecureString reads; cmd/km-secretsd/bundle.go (sops)",
	"kms:DescribeKey":         "cmd/km-secretsd/bundle.go",
	"sts:AssumeRole":          "cmd/km-secretsd/credentials.go (IMDS fence)",
	"bedrock:InvokeModel":     "the agent on Bedrock",
	"bedrock:InvokeModelWithResponseStream": "the agent on Bedrock",
	"bedrock-mantle:CreateInference":        "Bifrost gpt-oss routes",
}

var awsCLIRe = regexp.MustCompile(`\baws\s+(s3|s3api|ssm|sqs|dynamodb|sesv2|ses|logs|sts|kms|ec2)\s+([a-z-]+)`)

func allowedActions(t *testing.T) map[string]bool {
	t.Helper()
	dir := filepath.Join(findRepoRoot(t), filepath.FromSlash(pinnedEC2SpotModuleDir(t)))
	files, _ := filepath.Glob(filepath.Join(dir, "*.tf"))
	actRe := regexp.MustCompile(`"([a-z0-9-]+:[A-Za-z*]+)"`)
	out := map[string]bool{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := tfCommentRe.ReplaceAllString(string(raw), "")
		seen := map[int]bool{}
		for _, m := range tfActionKeyRe.FindAllStringIndex(s, -1) {
			a, b := enclosingBlock(s, m[0])
			if a == -1 || seen[a] {
				continue
			}
			seen[a] = true
			stmt := s[a:b]
			if regexp.MustCompile(`Effect\s*=\s*"Deny"`).MatchString(stmt) {
				continue
			}
			for _, am := range actRe.FindAllStringSubmatch(stmtField(stmt, "Action"), -1) {
				out[am[1]] = true
			}
		}
	}
	return out
}

func TestSandboxRole_EveryUserdataCLICallHasAGrant(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(findRepoRoot(t), "pkg", "compiler", "userdata.go"))
	if err != nil {
		t.Fatal(err)
	}
	allowed := allowedActions(t)
	calls := map[string]bool{}
	for _, m := range awsCLIRe.FindAllStringSubmatch(string(raw), -1) {
		calls[m[1]+" "+m[2]] = true
	}
	if len(calls) < 8 {
		t.Fatalf("found only %d distinct aws CLI calls in userdata.go; the regex drifted", len(calls))
	}
	var missing []string
	for call := range calls {
		actions, known := cliActionMap[call]
		if !known {
			missing = append(missing, call+" (not in cliActionMap — decide its grant)")
			continue
		}
		for _, a := range actions {
			if !allowed[a] {
				missing = append(missing, call+" needs "+a)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("userdata CLI calls without a grant in %s:\n  %s", pinnedEC2SpotModuleDir(t), strings.Join(missing, "\n  "))
	}
}

func TestSandboxRole_EverySDKActionHasAGrant(t *testing.T) {
	allowed := allowedActions(t)
	var missing []string
	for a, src := range sdkActions {
		if !allowed[a] {
			missing = append(missing, a+" ("+src+")")
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("SDK actions without a grant in the pinned module:\n  %s", strings.Join(missing, "\n  "))
	}
}
```

- [ ] **Step 2: Run it**

Run: `cd pkg/terragrunt && go test . -run 'TestSandboxRole_Every' -v; echo exit=$?`
Expected: PASS against v1.8.0. If a call is reported `not in cliActionMap`, add the mapping (it is a real call the module must cover) — do not delete the call. Then verify the guard bites: temporarily point `pinnedEC2SpotModuleDir` at `v1.7.0` (edit, run, revert) and confirm `TestSandboxRole_EveryUserdataCLICallHasAGrant` fails on `logs:*`, `ses:SendEmail`, `s3:ListBucket`, `s3:DeleteObject`, `ssm:DeleteParameter`. Mention the result in the report.

- [ ] **Step 3: Commit**

```bash
git add pkg/terragrunt/sandbox_role_call_coverage_test.go
git commit -m "test(terragrunt): every box-side AWS call in userdata and the sidecars has a grant in the pinned ec2spot module

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 4: Pollers stop writing `sandbox_id`; reads project, writes pin ReturnValues

**Files:**
- Modify: `pkg/compiler/userdata.go` — Slack poller get-item (~line 2227) and update-item (~2705); GitHub update-items (~3177 REMOVE, ~3218 SET); H1 update-items (~3587 REMOVE, ~3624 SET). Grep `aws dynamodb update-item` and `aws dynamodb get-item` — line numbers drift.
- Modify: `pkg/compiler/userdata_slack_inbound_test.go` (`TestPoller_ThreadRowWriteIsUpdateNotPut`, ~line 930)
- Create: `pkg/compiler/userdata_threads_iam_fit_test.go`
- Modify (hand-patch): `pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh`

**Interfaces:**
- Consumes: the IAM conditions of Task 1 (`dynamodb:Attributes` sets; `ReturnValues` ∈ NONE/UPDATED_OLD/UPDATED_NEW; `Select = SPECIFIC_ATTRIBUTES`).
- Produces: nothing other tasks consume.

- [ ] **Step 1: Update the existing write test to the new contract**

In `TestPoller_ThreadRowWriteIsUpdateNotPut`: delete the `"sandbox_id": ":sb",` entry from `owned` and the `":sb": {"S", "sb-1"},` entry from `want`; add after the `owned` loop:

```go
	if strings.Contains(expr, "sandbox_id") {
		t.Errorf("update expression names sandbox_id — the bridge's authorization attribute; IAM (v1.8.0) denies the write and the box must never need it: %q", expr)
	}
	if rv := flag("--return-values"); rv != "NONE" {
		t.Errorf("--return-values = %q, want NONE (dynamodb:Attributes conditions require ReturnValues pinned)", rv)
	}
```

- [ ] **Step 2: Write the fit test for every threads-table call in the template**

Create `pkg/compiler/userdata_threads_iam_fit_test.go` (package `compiler`, so `userDataTemplate` is in scope):

```go
package compiler

import (
	"strings"
	"testing"
)

// The sandbox role's threads-table grants (ec2spot v1.8.0) carry a
// dynamodb:Attributes condition. That condition is only enforceable when every
// UpdateItem pins ReturnValues and every GetItem names its attributes, and it
// only closes the forged-row hole if no update ever names sandbox_id. These
// tests read the raw template so every poller (Slack, GitHub, H1, and any
// future one) is covered without an edit.
func threadsTableCalls(t *testing.T, verb string) []string {
	t.Helper()
	var out []string
	rest := userDataTemplate
	for {
		i := strings.Index(rest, "aws dynamodb "+verb)
		if i < 0 {
			break
		}
		block := rest[i:]
		end := strings.Index(block, "\n\n")
		if end > 0 {
			block = block[:end]
		}
		if strings.Contains(block, "THREADS_TABLE") {
			out = append(out, block)
		}
		rest = rest[i+len("aws dynamodb "+verb):]
	}
	if len(out) == 0 {
		t.Fatalf("no `aws dynamodb %s` against a *THREADS_TABLE found in the template", verb)
	}
	return out
}

func TestThreadsTableUpdates_FitTheIAMCondition(t *testing.T) {
	for _, call := range threadsTableCalls(t, "update-item") {
		if strings.Contains(call, "sandbox_id") {
			t.Errorf("threads-table update names sandbox_id:\n%s", call)
		}
		if !strings.Contains(call, "--return-values NONE") {
			t.Errorf("threads-table update lacks --return-values NONE:\n%s", call)
		}
	}
}

func TestThreadsTableReads_ProjectOnlyOwnedAttributes(t *testing.T) {
	for _, call := range threadsTableCalls(t, "get-item") {
		if !strings.Contains(call, "--projection-expression") {
			t.Errorf("threads-table get-item has no --projection-expression (Select=SPECIFIC_ATTRIBUTES condition):\n%s", call)
		}
		if strings.Contains(call, "sandbox_id") {
			t.Errorf("threads-table get-item projects sandbox_id, which is not in the role's attribute set:\n%s", call)
		}
	}
}
```

- [ ] **Step 3: Run both — expect failures**

Run: `cd pkg/compiler && go test . -run 'TestPoller_ThreadRowWriteIsUpdateNotPut|TestThreadsTable' -v; echo exit=$?`
Expected: FAIL on `sandbox_id`, missing `--return-values`, Slack get-item missing projection.

- [ ] **Step 4: Edit the template**

Slack get-item: after the `--key "{\"channel_id\"…}"` line add
```bash
        --projection-expression "claude_session_id, agent_type, last_assistant_msg, last_processed_event_ts" \
```
Slack update-item: change the update expression to
```bash
          --update-expression "SET claude_session_id = :sid, agent_type = :agent, last_assistant_msg = :msg, last_turn_ts = :lt, last_processed_event_ts = :ev, ttl_expiry = :ttl" \
```
delete the `\":sb\":{\"S\":\"$SANDBOX_ID\"},` line, and add `--return-values NONE \` after the `--key` line. Update the comment above it: the bridge writes `sandbox_id` at dispatch (`Upsert`) and the v1.8.0 role cannot write it.

GitHub and H1: add `--return-values NONE \` after the `--key` line of all four update-items (two `REMOVE agent_session_id`, two `SET agent_session_id = :sid, agent_type = :at`).

- [ ] **Step 5: Run the compiler package; hand-patch the frozen golden**

Run: `cd pkg/compiler && go test . ; echo exit=$?`
Expected: the two new tests and the updated one PASS; `TestUserdataLearnV2Phase92ByteIdentity` FAILS with a diff confined to the poller hunks. Apply exactly those hunks to `testdata/userdata_learn_v2_pre92_baseline.golden.sh` (the Slack projection line, the shortened SET and removed `:sb` line, and `--return-values NONE` on each update-item present in that golden). NEVER run `CAPTURE_PRE92_BASELINE=1`. Re-run; `git diff --stat testdata/` must show only that golden. `userdata_additional_volume_only.golden.sh` and `h1_byte_identity_golden.txt` contain no threads-table calls and must be unchanged.

- [ ] **Step 6: Commit**

```bash
git add pkg/compiler/userdata.go pkg/compiler/userdata_slack_inbound_test.go pkg/compiler/userdata_threads_iam_fit_test.go pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh
git commit -m "fix(pollers): never write sandbox_id on the threads tables; pin --return-values NONE and project reads

The bridge writes sandbox_id when it dispatches; the v1.8.0 role's
dynamodb:Attributes condition excludes it, so the poller's SET had to drop it
or every end-of-turn write would be denied. Frozen pre-92 golden hand-patched.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 5: `km-` name fallbacks become an error path

**Files:**
- Modify: `pkg/compiler/userdata.go` lines with `${KM_RESOURCE_PREFIX:-km}` (~1979 `THREADS_TABLE`, ~2870 `GITHUB_THREADS_TABLE`, ~3317 `H1_THREADS_TABLE`, ~4991 `IDENTITIES_TABLE`)
- Modify: `sidecars/http-proxy/main.go:86`, `sidecars/audit-log/cmd/main.go:99`
- Test: `pkg/compiler/userdata_prefix_fallback_test.go` (new), plus a one-case test in each sidecar's existing test file if one exists for main (if `main.go` has no test seam, the Go change is a `log.Fatal`/error log and is covered by the template test only — say so in the report).
- Regenerate: `userdata_additional_volume_only.golden.sh` (`CAPTURE_ADDVOL_GOLDEN=1`), `h1_byte_identity_golden.txt` (`CAPTURE_PRE_H1_BASELINE=1`); hand-patch `userdata_learn_v2_pre92_baseline.golden.sh` (three `:-km}` lines).

- [ ] **Step 1: Write the template test**

```go
package compiler

import (
	"regexp"
	"strings"
	"testing"
)

// A {prefix}-scoped IAM grant cannot cover a literal km-* table on a non-km
// install; the Allow-star used to hide that. Resource names derived from
// KM_RESOURCE_PREFIX must fail loudly when it is unset rather than default to km.
func TestUserdata_NoLiteralKmResourceNameFallback(t *testing.T) {
	re := regexp.MustCompile(`\$\{KM_RESOURCE_PREFIX:-km\}`)
	for i, line := range strings.Split(userDataTemplate, "\n") {
		if re.MatchString(line) {
			t.Errorf("template line %d defaults the resource prefix to km: %s", i+1, strings.TrimSpace(line))
		}
	}
}

// Every unit or script that derives a name from KM_RESOURCE_PREFIX must receive it.
func TestUserdata_PollersReceiveResourcePrefix(t *testing.T) {
	for _, needle := range []string{
		`THREADS_TABLE="${KM_SLACK_THREADS_TABLE:-${KM_RESOURCE_PREFIX:?`,
		`GITHUB_THREADS_TABLE="${KM_GITHUB_THREADS_TABLE:-${KM_RESOURCE_PREFIX:?`,
		`H1_THREADS_TABLE="${KM_H1_THREADS_TABLE:-${KM_RESOURCE_PREFIX:?`,
		`IDENTITIES_TABLE="${KM_IDENTITIES_TABLE:-${KM_RESOURCE_PREFIX:?`,
	} {
		if !strings.Contains(userDataTemplate, needle) {
			t.Errorf("template missing the fail-loud form: %s", needle)
		}
	}
}
```

- [ ] **Step 2: Run — expect FAIL on the four lines**

Run: `cd pkg/compiler && go test . -run 'TestUserdata_NoLiteralKm|TestUserdata_PollersReceive' -v; echo exit=$?`

- [ ] **Step 3: Edit the four lines and the two sidecars**

Each `${KM_RESOURCE_PREFIX:-km}` → `${KM_RESOURCE_PREFIX:?KM_RESOURCE_PREFIX is unset; the install prefix is required to name AWS resources}`. Then confirm the variable actually reaches each reader: the Slack/GitHub/H1 poller units must have `EnvironmentFile=/etc/km/notify.env` (which carries `KM_RESOURCE_PREFIX` when notify is configured) or an explicit `Environment=KM_RESOURCE_PREFIX={{ .ResourcePrefix }}` line; `km-recv` (the `IDENTITIES_TABLE` reader) runs under a login shell where `/etc/profile.d/km-identity.sh` exports it, and under the mail poller unit — check that unit too and add the `Environment=` line where missing. Record in the report which units needed the line.

`sidecars/http-proxy/main.go`: replace `budgetTableName = getEnv("KM_BUDGET_TABLE", "km-budgets")` with
```go
		budgetTableName = os.Getenv("KM_BUDGET_TABLE")
		if budgetTableName == "" {
			log.Fatal().Msg("KM_BUDGET_TABLE is unset while budget enforcement is enabled; a km- default would miss the install's table (and its IAM grant)")
		}
```
`sidecars/audit-log/cmd/main.go`: replace `sandboxTable := envOr("KM_SANDBOX_TABLE", "km-sandboxes")` with
```go
			sandboxTable := os.Getenv("KM_SANDBOX_TABLE")
			if sandboxTable == "" {
				log.Printf("KM_SANDBOX_TABLE unset; idle re-arm will not read updated timeouts")
			}
```
and keep the downstream code path that tolerates an empty table (read it; if it does not, skip constructing the DynamoDB client when the name is empty). Use the file's existing logger, whatever it is.

- [ ] **Step 4: Goldens**

Run: `cd pkg/compiler && go test . ; echo exit=$?`. Hand-patch the three `:-km}` lines in `userdata_learn_v2_pre92_baseline.golden.sh`; regenerate the other two with their sanctioned flags:
```bash
CAPTURE_ADDVOL_GOLDEN=1 go test . -run TestUserdataAdditionalVolume >/dev/null; CAPTURE_PRE_H1_BASELINE=1 go test . -run TestCapturePreH1Userdata >/dev/null
git diff --stat testdata/   # exactly the three goldens; inspect each diff is only the :-km lines
go test . ; echo exit=$?
```
Then `cd ../../sidecars/http-proxy && go build ./... && go test ./...; cd ../audit-log && go build ./... && go test ./...`.

- [ ] **Step 5: Commit**

```bash
git add pkg/compiler/userdata.go pkg/compiler/userdata_prefix_fallback_test.go pkg/compiler/testdata/userdata_learn_v2_pre92_baseline.golden.sh pkg/compiler/testdata/userdata_additional_volume_only.golden.sh pkg/compiler/testdata/h1_byte_identity_golden.txt sidecars/http-proxy/main.go sidecars/audit-log/cmd/main.go
git commit -m "fix: resource names derived from KM_RESOURCE_PREFIX fail loudly when it is unset instead of defaulting to km

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 6: service.hcl emits `email_domain` and `otp_secret_paths`

**Files:**
- Modify: `pkg/compiler/service_hcl.go` — `ec2HCLParams` (after `FenceIMDS`), the EC2 template (after `slack_stream_messages_table_name`), `generateEC2ServiceHCL` param construction
- Modify: `pkg/compiler/userdata.go` `generateUserData` (the `"sandboxes.klankermaker.ai"` literal → shared const)
- Test: `pkg/compiler/service_hcl_least_privilege_test.go` (new)

**Interfaces:**
- Produces: `const defaultEmailDomain = "sandboxes.klankermaker.ai"` and `func effectiveEmailDomain(network *NetworkConfig) string` in package `compiler`.

- [ ] **Step 1: Write the test** (mirror the fixture/NetworkConfig setup of `TestSpotRateEC2NonZero` in `service_hcl_test.go`; load `testdata/ec2-basic.yaml`)

```go
package compiler

import (
	"strings"
	"testing"

	"github.com/whereiskurt/klanker-maker/pkg/profile"
)

func TestEC2ServiceHCL_EmitsEmailDomainAndOTPPaths(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml") // use the same loader the other service_hcl tests use
	p.Spec.OTP = &profile.OTPSpec{Secrets: []string{"/km/sandbox/sb-1/otp/github-token"}}
	network := &NetworkConfig{RegionLabel: "use1", EmailDomain: "sandboxes.example.com"}
	out, err := generateEC2ServiceHCL(p, "sb-1", true, nil, &IAMSessionPolicy{MaxSessionDuration: 3600}, "", network, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`email_domain = "sandboxes.example.com"`,
		`otp_secret_paths = ["/km/sandbox/sb-1/otp/github-token"]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("service.hcl missing %s\n%s", want, out)
		}
	}
}

func TestEC2ServiceHCL_OTPOmittedWhenAbsent_EmailDomainDefaults(t *testing.T) {
	p := loadTestProfile(t, "ec2-basic.yaml")
	p.Spec.OTP = nil
	out, err := generateEC2ServiceHCL(p, "sb-1", true, nil, &IAMSessionPolicy{MaxSessionDuration: 3600}, "", &NetworkConfig{RegionLabel: "use1"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "otp_secret_paths") {
		t.Error("otp_secret_paths must be omitted when the profile has no OTP secrets")
	}
	if !strings.Contains(out, `email_domain = "`+defaultEmailDomain+`"`) {
		t.Errorf("email_domain must always be emitted (empty would drop the SES grant):\n%s", out)
	}
}
```
If no `loadTestProfile` helper exists, write the three lines that read `testdata/ec2-basic.yaml` and `profile.Parse` it, as the neighbouring tests do.

- [ ] **Step 2: Run — expect compile failure / FAIL**

Run: `cd pkg/compiler && go test . -run 'TestEC2ServiceHCL_EmitsEmailDomain|TestEC2ServiceHCL_OTPOmitted' -v; echo exit=$?`

- [ ] **Step 3: Implement**

In `service_hcl.go`:
```go
	// EmailDomain scopes the v1.8.0 ses:SendEmail grant (identity + From
	// addresses). Always emitted: an empty value creates no SES grant and
	// km-send fails loudly on the box.
	EmailDomain string
	// OTPSecretPaths (spec.otp.secrets) get ssm:GetParameter + DeleteParameter
	// in v1.8.0; emitted only when non-empty.
	OTPSecretPaths []string
```
Template, after the `slack_stream_messages_table_name` line:
```
    # v1.8.0 — the SES send grant is scoped to this domain identity and to this
    # sandbox's own From addresses.
    email_domain                      = "{{ .EmailDomain }}"
{{- if .OTPSecretPaths }}

    # spec.otp.secrets: read-once parameters; v1.8.0 grants GetParameter + DeleteParameter.
    otp_secret_paths = [{{ joinStrings .OTPSecretPaths }}]
{{- end }}
```
In `generateEC2ServiceHCL` params: `EmailDomain: effectiveEmailDomain(network),` and `OTPSecretPaths: otpSecretPaths(p),` with
```go
const defaultEmailDomain = "sandboxes.klankermaker.ai"

// effectiveEmailDomain is the one place the nil-network fallback lives, so
// userdata (KM_EMAIL_ADDRESS) and the SES grant can never name different domains.
func effectiveEmailDomain(network *NetworkConfig) string {
	if network != nil && network.EmailDomain != "" {
		return network.EmailDomain
	}
	return defaultEmailDomain
}

func otpSecretPaths(p *profile.SandboxProfile) []string {
	if p.Spec.OTP == nil {
		return nil
	}
	return p.Spec.OTP.Secrets
}
```
In `generateUserData`, replace the literal `"sandboxes.klankermaker.ai"` with `defaultEmailDomain` (keep its TODO comment).

- [ ] **Step 4: Run the package**

Run: `cd pkg/compiler && go test . ; echo exit=$?` — expected exit=0 (the ECS golden and the IAM golden are untouched by the EC2 template; if any EC2 service.hcl test compares whole output, update its expectation and say so).

- [ ] **Step 5: Commit**

```bash
git add pkg/compiler/service_hcl.go pkg/compiler/userdata.go pkg/compiler/service_hcl_least_privilege_test.go
git commit -m "feat(compiler): emit email_domain and otp_secret_paths module inputs for the v1.8.0 sandbox role

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 7: `km validate` warns on `aws` calls in initCommands the role will not cover

**Files:**
- Create: `pkg/profile/validate_initcommands_aws.go`, `pkg/profile/validate_initcommands_aws_test.go`
- Modify: `pkg/profile/validate.go` — one line in `ValidateSemantic` before `return errs`: `errs = append(errs, validateInitCommandAWSCalls(p)...)`

**Interfaces:**
- Produces: `func validateInitCommandAWSCalls(p *SandboxProfile) []ValidationError` (all `IsWarning: true`).

- [ ] **Step 1: Write the test**

```go
package profile

import (
	"strings"
	"testing"
)

func TestValidateInitCommandAWSCalls(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		otp  []string
		warn bool
	}{
		{"no aws", "npm install -g @anthropic-ai/claude-code", nil, false},
		{"caller identity", "aws sts get-caller-identity", nil, false},
		{"models sync is granted", "aws s3 sync s3://${KM_ARTIFACTS_BUCKET}/models/qwen/ /data/models", nil, false},
		{"binaries cp is granted", `aws s3 cp "s3://${KM_ARTIFACTS_BUCKET}/binaries/herdr" /usr/local/bin/herdr`, nil, false},
		{"foreign bucket", "aws s3 cp s3://some-other-bucket/tool.tgz /tmp/", nil, true},
		{"own bucket, ungranted prefix", "aws s3 ls s3://${KM_ARTIFACTS_BUCKET}/sandboxes/", nil, true},
		{"ssm path in allowedSecretPaths", "aws ssm get-parameter --name {{prefix}}/wiz/client-id", nil, false},
		{"ssm path not listed", "aws ssm get-parameter --name /km/config/slack/bot-token", nil, true},
		{"otp path listed", "aws ssm get-parameter --name /km/sandbox/x/otp/tok", []string{"/km/sandbox/x/otp/tok"}, false},
		{"dynamodb anything", "aws dynamodb scan --table-name km-sandboxes", nil, true},
		{"aws inside a pipeline", "curl x | aws s3 cp - s3://b/k", nil, true},
		{"word aws in text", "echo 'uses aws-cli later'", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &SandboxProfile{}
			p.Spec.Execution.InitCommands = []string{c.cmd}
			p.Spec.IAM.AllowedSecretPaths = []string{"{{prefix}}/wiz/client-id"}
			if c.otp != nil {
				p.Spec.OTP = &OTPSpec{Secrets: c.otp}
			}
			errs := validateInitCommandAWSCalls(p)
			if c.warn && len(errs) == 0 {
				t.Fatalf("expected a warning for %q", c.cmd)
			}
			if !c.warn && len(errs) != 0 {
				t.Fatalf("unexpected warning for %q: %v", c.cmd, errs)
			}
			for _, e := range errs {
				if !e.IsWarning || !strings.HasPrefix(e.Path, "spec.execution.initCommands[") {
					t.Errorf("want a warning at spec.execution.initCommands[i], got %+v", e)
				}
			}
		})
	}
}

func TestValidateInitCommandAWSCalls_CoversInitCommandsAppend(t *testing.T) {
	p := &SandboxProfile{}
	p.Spec.Execution.InitCommandsAppend = []string{"aws dynamodb put-item --table-name t --item {}"}
	errs := validateInitCommandAWSCalls(p)
	if len(errs) != 1 || errs[0].Path != "spec.execution.initCommandsAppend[0]" {
		t.Fatalf("got %+v", errs)
	}
}
```
(Check the exact field path of `InitCommands`/`InitCommandsAppend` and `IAM.AllowedSecretPaths` on `SandboxProfile`; `types.go:576-585` places them under `Spec.Execution`.)

- [ ] **Step 2: Run — expect `undefined: validateInitCommandAWSCalls`**

Run: `cd pkg/profile && go test . -run TestValidateInitCommandAWSCalls -v; echo exit=$?`

- [ ] **Step 3: Implement**

```go
package profile

import (
	"fmt"
	"regexp"
	"strings"
)

// The v1.8.0 sandbox role grants only what km's own code calls. An operator
// `aws …` line in initCommands used to work through the Allow-star; now it
// fails under set -e in km-init.sh and aborts the remaining steps. This is a
// best-effort textual check: it cannot see indirect AWS use inside installers.
var initAWSCallRe = regexp.MustCompile(`(?:^|[\s;&|(])aws\s+(s3|s3api|ssm|dynamodb|sqs|logs|ses|sesv2|kms|sts|ec2|iam|secretsmanager|lambda|events)\s+([a-z-]+)((?:\s+\S+)*)`)

var initAWSS3GrantedPrefixRe = regexp.MustCompile(`s3://[^/\s"']+/(models|sidecars|binaries|rsync)/`)

func validateInitCommandAWSCalls(p *SandboxProfile) []ValidationError {
	var errs []ValidationError
	allowedSSM := map[string]bool{}
	for _, s := range p.Spec.IAM.AllowedSecretPaths {
		allowedSSM[s] = true
	}
	if p.Spec.OTP != nil {
		for _, s := range p.Spec.OTP.Secrets {
			allowedSSM[s] = true
		}
	}
	check := func(path string, cmds []string) {
		for i, cmd := range cmds {
			for _, m := range initAWSCallRe.FindAllStringSubmatch(cmd, -1) {
				svc, op, rest := m[1], m[2], m[3]
				switch {
				case svc == "sts" && op == "get-caller-identity":
					continue
				case svc == "s3" && (op == "cp" || op == "sync" || op == "ls") && initAWSS3GrantedPrefixRe.MatchString(rest):
					continue
				case svc == "ssm" && op == "get-parameter" && ssmNameListed(rest, allowedSSM):
					continue
				}
				errs = append(errs, ValidationError{
					Path: fmt.Sprintf("%s[%d]", path, i),
					Message: fmt.Sprintf("calls `aws %s %s`: the sandbox role grants only what km itself needs, so this step will fail (and abort the remaining initCommands) unless the target is covered. "+
						"For an SSM parameter add it to spec.iam.allowedSecretPaths; for S3 use the artifacts bucket's models/, binaries/, rsync/ prefixes; anything else needs a profile grant field — ask for one.", svc, op),
					IsWarning: true,
				})
			}
		}
	}
	check("spec.execution.initCommands", p.Spec.Execution.InitCommands)
	check("spec.execution.initCommandsAppend", p.Spec.Execution.InitCommandsAppend)
	return errs
}

func ssmNameListed(rest string, allowed map[string]bool) bool {
	fields := strings.Fields(rest)
	for i, f := range fields {
		if f == "--name" && i+1 < len(fields) {
			return allowed[strings.Trim(fields[i+1], `"'`)]
		}
		if strings.HasPrefix(f, "--name=") {
			return allowed[strings.Trim(strings.TrimPrefix(f, "--name="), `"'`)]
		}
	}
	return false
}
```

- [ ] **Step 4: Run the package and the profile inventory**

Run: `cd pkg/profile && go test . ; echo exit=$?` then `cd ../.. && scripts/validate-all-profiles.sh; echo exit=$?` (needs `make build` first if `km` is stale — this is a laptop binary build, allowed). Expected: exit 0; the `gpu-*` profiles' `aws s3 sync … /models/` lines produce NO warning. List in the report any shipped profile that now warns.

- [ ] **Step 5: Commit**

```bash
git add pkg/profile/validate.go pkg/profile/validate_initcommands_aws.go pkg/profile/validate_initcommands_aws_test.go
git commit -m "feat(validate): warn on aws CLI calls in initCommands the v1.8.0 sandbox role does not cover

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 8: `km doctor` warns about running sandboxes still on the v1.7.0 role shape

**Files:**
- Create: `internal/app/cmd/doctor_sandbox_role.go`, `internal/app/cmd/doctor_sandbox_role_test.go`
- Modify: `internal/app/cmd/doctor.go` — append one check after the `checkInitFailed` registration (~line 4812), using `deps.IAMCleanupClient`, `deps.Lister`, and the install prefix already in scope there (the variable the stale-roles check uses as `resourcePrefix`).

**Interfaces:**
- Produces: `type roleShapeIAM interface { ListRolePolicies(...); ListAttachedRolePolicies(...) }` (the two methods with the exact `iam` SDK signatures as in `IAMCleanupAPI`, so `IAMCleanupAPI` satisfies it) and `func checkSandboxRoleShape(ctx, iamClient roleShapeIAM, lister SandboxLister, resourcePrefix string) CheckResult`.

- [ ] **Step 1: Write the test**

```go
package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	kmaws "github.com/whereiskurt/klanker-maker/pkg/aws"
)

type fakeRoleShapeIAM struct {
	inline   map[string][]string // role name -> inline policy names
	attached map[string][]string // role name -> attached policy ARNs
	missing  map[string]bool     // role name -> NoSuchEntity
}

func (f *fakeRoleShapeIAM) ListRolePolicies(_ context.Context, in *iam.ListRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error) {
	if f.missing[*in.RoleName] {
		return nil, &iamtypes.NoSuchEntityException{Message: aws.String("no role")}
	}
	return &iam.ListRolePoliciesOutput{PolicyNames: f.inline[*in.RoleName]}, nil
}

func (f *fakeRoleShapeIAM) ListAttachedRolePolicies(_ context.Context, in *iam.ListAttachedRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	var out []iamtypes.AttachedPolicy
	for _, arn := range f.attached[*in.RoleName] {
		out = append(out, iamtypes.AttachedPolicy{PolicyArn: aws.String(arn)})
	}
	return &iam.ListAttachedRolePoliciesOutput{AttachedPolicies: out}, nil
}

type fakeSandboxLister struct{ recs []kmaws.SandboxRecord }

func (f *fakeSandboxLister) ListSandboxes(context.Context, bool) ([]kmaws.SandboxRecord, error) { return f.recs, nil }

func TestCheckSandboxRoleShape(t *testing.T) {
	lister := &fakeSandboxLister{recs: []kmaws.SandboxRecord{
		{SandboxID: "sb-old", Region: "us-east-1", Status: "running"},
		{SandboxID: "sb-new", Region: "us-east-1", Status: "running"},
		{SandboxID: "sb-core", Region: "us-east-1", Status: "running"},
		{SandboxID: "sb-stopped", Region: "us-east-1", Status: "stopped"},
		{SandboxID: "sb-linked", Region: "us-east-1", Status: "running"},
	}}
	iamc := &fakeRoleShapeIAM{
		inline: map[string][]string{
			"km-ec2spot-ssm-sb-old-use1":  {"km-ec2spot-region-lock-use1", "km-sb-old-bedrock"},
			"km-ec2spot-ssm-sb-new-use1":  {"km-ec2spot-region-deny-use1", "km-sb-new-ssm-core"},
			"km-ec2spot-ssm-sb-core-use1": {"km-ec2spot-region-deny-use1"},
		},
		attached: map[string][]string{
			"km-ec2spot-ssm-sb-core-use1": {"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
		},
		missing: map[string]bool{"km-ec2spot-ssm-sb-linked-use1": true},
	}
	r := checkSandboxRoleShape(context.Background(), iamc, lister, "km")
	if r.Status != CheckWarn {
		t.Fatalf("status = %v, want WARN: %+v", r.Status, r)
	}
	for _, want := range []string{"sb-old", "sb-core"} {
		if !strings.Contains(r.Message, want) {
			t.Errorf("message should name %s: %s", want, r.Message)
		}
	}
	for _, not := range []string{"sb-new", "sb-stopped", "sb-linked"} {
		if strings.Contains(r.Message, not) {
			t.Errorf("message must not name %s: %s", not, r.Message)
		}
	}
	if !strings.Contains(r.Remediation, "km destroy") {
		t.Errorf("remediation should say to recreate: %s", r.Remediation)
	}
}

func TestCheckSandboxRoleShape_AllNewIsOK(t *testing.T) {
	lister := &fakeSandboxLister{recs: []kmaws.SandboxRecord{{SandboxID: "sb-new", Region: "us-east-1", Status: "running"}}}
	iamc := &fakeRoleShapeIAM{inline: map[string][]string{"km-ec2spot-ssm-sb-new-use1": {"km-ec2spot-region-deny-use1"}}}
	if r := checkSandboxRoleShape(context.Background(), iamc, lister, "km"); r.Status != CheckOK {
		t.Fatalf("want OK, got %+v", r)
	}
}

func TestCheckSandboxRoleShape_SkipsWithoutIAM(t *testing.T) {
	if r := checkSandboxRoleShape(context.Background(), nil, &fakeSandboxLister{}, "km"); r.Status != CheckSkipped {
		t.Fatalf("want SKIP, got %+v", r)
	}
}
```
If a fake named `fakeSandboxLister` already exists in the package tests, reuse it instead of redefining.

- [ ] **Step 2: Run — expect compile failure**

Run: `cd internal/app/cmd && go test . -run TestCheckSandboxRoleShape -v; echo exit=$?` (this package's tests are slow; `-run` keeps it bounded. Memory: a few pre-existing Bootstrap/Cluster/Configure tests fail on a clean tree — not yours.)

- [ ] **Step 3: Implement `doctor_sandbox_role.go`**

```go
package cmd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/whereiskurt/klanker-maker/pkg/compiler"
)

// roleShapeIAM is the slice of IAMCleanupAPI this check needs.
type roleShapeIAM interface {
	ListRolePolicies(ctx context.Context, params *iam.ListRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error)
	ListAttachedRolePolicies(ctx context.Context, params *iam.ListAttachedRolePoliciesInput, optFns ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error)
}

// checkSandboxRoleShape WARNs for every running sandbox whose instance role
// still has the pre-v1.8.0 shape: the Allow-*/* "region-lock" inline policy or
// the managed AmazonSSMManagedInstanceCore attachment. The role is created per
// sandbox at km create, so the fleet converges only as boxes are recreated; this
// makes the remaining ones visible. A role that does not exist in this account
// (a linked-account launch) is skipped.
func checkSandboxRoleShape(ctx context.Context, iamClient roleShapeIAM, lister SandboxLister, resourcePrefix string) CheckResult {
	const name = "Sandbox role shape"
	if iamClient == nil || lister == nil {
		return CheckResult{Name: name, Status: CheckSkipped, Message: "IAM client or sandbox lister not configured"}
	}
	records, err := lister.ListSandboxes(ctx, false)
	if err != nil {
		return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not list sandboxes: %v", err)}
	}
	var old []string
	checked := 0
	for _, r := range records {
		if r.Status != "running" {
			continue
		}
		label := compiler.RegionLabel(r.Region)
		role := fmt.Sprintf("%s-ec2spot-ssm-%s-%s", resourcePrefix, r.SandboxID, label)
		inline, err := iamClient.ListRolePolicies(ctx, &iam.ListRolePoliciesInput{RoleName: aws.String(role)})
		if err != nil {
			var nse *iamtypes.NoSuchEntityException
			if errors.As(err, &nse) {
				continue
			}
			return CheckResult{Name: name, Status: CheckWarn, Message: fmt.Sprintf("could not read role %s: %v", role, err)}
		}
		checked++
		reason := ""
		for _, pn := range inline.PolicyNames {
			if strings.HasSuffix(pn, "-ec2spot-region-lock-"+label) {
				reason = "Allow-* region lock"
			}
		}
		attached, err := iamClient.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{RoleName: aws.String(role)})
		if err == nil {
			for _, ap := range attached.AttachedPolicies {
				if ap.PolicyArn != nil && strings.HasSuffix(*ap.PolicyArn, "/AmazonSSMManagedInstanceCore") {
					if reason != "" {
						reason += " + "
					}
					reason += "managed SSM Core (ssm:GetParameter on *)"
				}
			}
		}
		if reason != "" {
			old = append(old, fmt.Sprintf("%s (%s)", r.SandboxID, reason))
		}
	}
	sort.Strings(old)
	if len(old) == 0 {
		return CheckResult{Name: name, Status: CheckOK, Message: fmt.Sprintf("%d running sandbox role(s) on the least-privilege shape", checked)}
	}
	return CheckResult{
		Name:        name,
		Status:      CheckWarn,
		Message:     fmt.Sprintf("%d running sandbox(es) still hold the pre-v1.8.0 role (in-region Allow-* and/or account-wide SSM reads):\n    %s", len(old), strings.Join(old, "\n    ")),
		Remediation: "The role is created at km create. Recreate each: 'km destroy <id> --remote --yes && km create <profile> <alias>'. See docs/operational-gotchas.md § The sandbox role is least-privilege since v1.8.0.",
	}
}
```
Wire it in `doctor.go` right after the `checkInitFailed` closure:
```go
	// v1.8.0 least-privilege role: name the running sandboxes still on the old shape.
	iamForRoleShape := deps.IAMCleanupClient
	listerForRoleShape := deps.Lister
	checks = append(checks, func(ctx context.Context) CheckResult {
		return checkSandboxRoleShape(ctx, iamForRoleShape, listerForRoleShape, <resourcePrefix var in scope>)
	})
```
(Find the `resourcePrefix` identifier the stale-roles check passes at its registration and reuse it. `IAMCleanupClient` may be nil when the IAM client is unavailable — the check skips.)

- [ ] **Step 4: Run**

Run: `cd internal/app/cmd && go test . -run 'TestCheckSandboxRoleShape|TestDoctor' ; echo exit=$?` Expected exit 0 (except memory-listed pre-existing failures, which do not match `TestDoctor`). Then `go vet ./internal/app/cmd/`.

- [ ] **Step 5: Commit**

```bash
git add internal/app/cmd/doctor_sandbox_role.go internal/app/cmd/doctor_sandbox_role_test.go internal/app/cmd/doctor.go
git commit -m "feat(doctor): WARN for running sandboxes whose role still has the Allow-* region lock or managed SSM Core

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 9: Docs, CLAUDE.md, memory

**Files:**
- Modify: `docs/operational-gotchas.md` (§ "Never grant the sandbox role access to `km-slack-threads`", lines ~295-313)
- Modify: `docs/slack-default-route.md` §6.4 (~line 311) and the table row at ~line 270
- Modify: `docs/slack-notifications.md` (~lines 3462-3472 and 3566-3580)
- Modify: `CLAUDE.md` (the Slack default-route bullet's "Open hole" sentences, ~line 60-72; add a new dated bullet at the top of the Project section, before "Slack 👀 quiet period")
- Modify: `docs/sandbox-secrets.md`, `docs/brokered-secrets.md` (one sentence each where the per-sandbox SSM path grant is described)
- Modify (memory): `/Users/khundeck/.claude/projects/-Users-khundeck-working-klankrmkr/memory/project_ec2spot_region_lock_is_allow_star.md` and the matching line in `MEMORY.md` (memory is outside the repo: edit, do not commit)

- [ ] **Step 1: operational-gotchas**

Replace the section heading and body with `### The sandbox role is least-privilege since ec2spot v1.8.0 (2026-10-17)`:
- What was wrong (three holes, two sentences each): the Allow-* "region lock"; the managed Core policy's account-wide `ssm:GetParameter*` plus `kms:Decrypt` via SSM on every key; the pollers writing the `sandbox_id` authorization attribute.
- What v1.8.0 does: region Deny with the SCP exemption list + `bedrock-mantle:*` + `aws-marketplace:*`; one inline policy per concern, every grant naming `{id}` or `{p}`; SSM reads per path with the KMS decrypt pinned via `kms:EncryptionContext:PARAMETER_ARN`; threads tables writable only for a named attribute set that excludes `sandbox_id` (a row the box creates reads as unrouted); `action-quota` by `dynamodb:LeadingKeys`.
- Consequences: an operator `aws` call in `initCommands` now fails under `set -e` (`km validate` WARNs; add SSM paths to `iam.allowedSecretPaths`); the role is per sandbox, so existing boxes keep the old shape until recreate and `km doctor` "Sandbox role shape" lists them; `KM_RESOURCE_PREFIX` unset now aborts a poller instead of silently targeting `km-*`.
- Guards: `TestEC2SpotRole_*`, `TestSandboxRole_ThreadsWriteGrantExcludesSandboxID`, `TestSandboxRole_Every*HasAGrant`, `TestThreadsTable*`.
- Deploy: `make build` → `make build-lambdas` → `km init --dry-run=false` → recreate; no-go = repin v1.7.0 at the three sites.
- IAM aggregate inline-policy size: 10,240 chars; what to drop first if a create ever fails with `LimitExceeded`.

- [ ] **Step 2: slack-default-route.md §6.4**

Retitle `### 6.4 The ec2spot_region_lock finding — closed in v1.8.0 (2026-10-17)`. Keep the first two paragraphs as history (past tense), then one paragraph on how it closed: region Deny; the threads grant's attribute condition excludes `sandbox_id`; the poller no longer SETs it; `LookupSandbox` treats a row without `sandbox_id` as unrouted, so a box-created row authorizes nothing; residual: a box can still touch non-authz fields of another thread's row (nuisance); the bridge action that removes the box's table access entirely remains the follow-up. Update the table row at ~line 270 to name the new test. Update §11's follow-up list accordingly.

- [ ] **Step 3: slack-notifications.md**

At ~3462-3472: replace "The sandbox role has no explicit grant on the table … Never grant …" with: the role has a conditioned grant (session attributes only, `sandbox_id` excluded) pinned by `TestSandboxRole_ThreadsWriteGrantExcludesSandboxID`; never add `sandbox_id` to that set. Delete the "One known exception is open" sentences. At ~3566-3580: replace the two bullets with one: "**`sandbox_id` is bridge-written only.** Since ec2spot v1.8.0 the sandbox role can update a threads row only for its session attributes; the region lock is a Deny. A box-created row has no `sandbox_id` and routes nothing."

- [ ] **Step 4: CLAUDE.md**

In the default-route bullet (~line 60-72) replace from "That row is written only by bridge-side components" through "`permalink`/`update` there stay" with: "`sandbox_id` on that row is written only by bridge-side components; since ec2spot v1.8.0 (2026-10-17) the sandbox role may update a threads row only for its session attributes (`dynamodb:Attributes` condition, `sandbox_id` excluded — `TestSandboxRole_ThreadsWriteGrantExcludesSandboxID`), and the region lock is a Deny, so a forged row cannot carry `sandbox_id` and authorizes nothing. `permalink`/`update` there stay". Then add a new top bullet:

```
**Sandbox instance role is least-privilege — ec2spot v1.8.0 (2026-10-17):**
- **`ec2spot_region_lock` was an `Allow */*` conditioned on region, and the managed `AmazonSSMManagedInstanceCore` granted `ssm:GetParameter*` on `*`** with `kms:Decrypt` via SSM on every key: any box could read every parameter in the account and write every in-region table. v1.8.0 replaces them with a region **Deny** (`NotAction` = the SCP exemption list + `bedrock-mantle:*`, `aws-marketplace:*`) and one explicit grant per concern, every resource naming `{id}` or `{p}`; SSM reads are per path, KMS pinned by `kms:EncryptionContext:PARAMETER_ARN`; threads tables are writable only for a named attribute set that excludes `sandbox_id`; `action-quota` by `LeadingKeys`.
- **The role is created per sandbox at `km create`**, so existing boxes keep the old shape until recreate; `km doctor` "Sandbox role shape" lists them. Operator `aws` calls in `initCommands` now fail under `set -e` — `km validate` WARNs; use `iam.allowedSecretPaths` for SSM. `KM_RESOURCE_PREFIX` unset aborts a poller rather than targeting `km-*`.
- **Deploy = `make build` → `make build-lambdas` → `km init --dry-run=false` → recreate (canary first).** No-go = repin v1.7.0 at the three sites. IAM aggregate inline-policy limit is 10,240 chars; drop `Sid`s, then collapse the PARAMETER_ARN list, if a create ever hits `LimitExceeded`. See `docs/operational-gotchas.md` § The sandbox role is least-privilege.
```
Also update the "Where to look" row for the default-route design doc to drop "the `ec2spot_region_lock` security finding" → "the closed `ec2spot_region_lock` finding".

- [ ] **Step 5: secrets docs + memory**

`docs/sandbox-secrets.md` and `docs/brokered-secrets.md`: where each says the instance role can read `/{prefix}/sandbox/{id}/*` (grep `GetParameter`), add: "Since ec2spot v1.8.0 that path list is the whole SSM read surface of the role (the managed Core policy's `GetParameter` on `*` is gone) and the SSM `kms:Decrypt` is pinned to the same parameter ARNs."

Memory file: change `description` to `"FIXED 2026-10-17 (ec2spot v1.8.0): the Allow-*/* region lock is a Deny + explicit grants; threads tables writable only for session attributes (no sandbox_id); existing boxes keep the old role until recreate"`, prepend a `**Status: FIXED 2026-10-17** …` paragraph naming v1.8.0, the guards, and `km doctor` "Sandbox role shape"; keep the history. Update its `MEMORY.md` line to `— FIXED in ec2spot v1.8.0 (region Deny + explicit grants); old boxes until recreate`.

- [ ] **Step 6: Commit (repo files only)**

```bash
git add docs/operational-gotchas.md docs/slack-default-route.md docs/slack-notifications.md CLAUDE.md docs/sandbox-secrets.md docs/brokered-secrets.md
git commit -m "docs: sandbox role least privilege (v1.8.0) — gotcha, §6.4 closed, CLAUDE.md bullet

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01WiMBVL8Bp4oSYyRukJfVSt"
```

### Task 10: Deploy and canary (controller-run, main tree only)

**Not a subagent task.** The controller runs this in the main tree (`km-config.yaml` and `infra/live` outputs live only there). Destructive step (destroying `ir-bot`) is pre-approved by the operator ("full, canary ir-bot"). Record each result in the ledger.

- [ ] **Step 1: Build and deploy**

```bash
make build && make build-lambdas && go test ./pkg/terragrunt/ ./pkg/compiler/ ./pkg/profile/; echo exit=$?
km --version          # GitCommit must be HEAD
km init --plan 2>&1 | tail -40     # expect no destroy-class trips; sandbox roles are per-sandbox so the fleet plan is small
km init --dry-run=false
```

- [ ] **Step 2: Recreate the canary**

```bash
km list | grep ir-bot            # note the current id (sl-40741914 at plan time)
km destroy <id> --remote --yes
km create profiles/ir-bot.yaml ir-bot --wait
km status <new-id>               # SANDBOX_READY, Init: not FAILED
```
If `km create` fails at terraform apply with `LimitExceeded … Maximum policy size of 10240`, apply the Global Constraints reduction order to v1.8.0 (edit in place — the module is unreleased until this canary passes), rebuild, re-run.

- [ ] **Step 3: Negative controls (as uid `sandbox`, via `km shell <id>`; expect AccessDenied on each)**

```bash
aws ssm get-parameter --name /km/sandbox/<some-OTHER-sandbox-id>/signing-key --with-decryption
aws dynamodb update-item --table-name km-slack-threads --key '{"channel_id":{"S":"CFORGE"},"thread_ts":{"S":"1.1"}}' --update-expression 'SET sandbox_id = :s' --expression-attribute-values '{":s":{"S":"'$KM_SANDBOX_ID'"}}' --return-values NONE
aws dynamodb scan --table-name km-sandboxes --limit 1
aws s3 ls s3://<artifacts-bucket>/sandboxes/
aws s3 ls --region eu-west-1 s3://<artifacts-bucket>/sidecars/
aws sts get-caller-identity --region eu-west-1    # expect SUCCESS (sts exempt)
aws ssm get-parameter --name /km/slack/bot-token --with-decryption    # expect AccessDenied (install-wide secret)
```
Also record (informational) what `aws dynamodb get-item --table-name km-slack-threads --key …` WITHOUT a projection returns for one of the box's own rows — the plan assumes the attribute condition does not block an unprojected read; if it does, that is fine too (the poller projects).

- [ ] **Step 4: Positive controls**

- boot: `SANDBOX_READY`, sidecars present (`ls /opt/km/bin`), `journalctl -u km-audit-log` clean, audit events in CloudWatch `/km/sandboxes/<id>/`
- Slack: @-mention in a public channel (routed thread) → reply in thread; a message in `#sb-…` → reply; thread row has `claude_session_id` AND `last_dispatch_ts`/`sandbox_id` (bridge-written) after the turn
- `km-slack dm --to <operator-id> "canary"` from the box → DM arrives
- `km-send` to the operator → mail delivered; mail poller reads `mail/`
- quota: with limits configured, one Slack turn → `km-action-quota` row for `<id>#…` incremented (`aws dynamodb query` as the operator)
- Bedrock: agent answers on Bedrock (cross-region inference profile) — check the proxy log for a 200
- `km stop <id>` / `km resume <id>` → pollers up, SSM reachable
- idle: `GetLogEvents` by the role appears in CloudTrail (or `km-audit-log` log shows the idle detector read)
- `km doctor` → "Sandbox role shape" OK for the canary; WARN naming the remaining old boxes; nothing else new red
- aggregate inline size: `for p in $(aws iam list-role-policies --role-name km-ec2spot-ssm-<id>-use1 --query 'PolicyNames[]' --output text); do aws iam get-role-policy --role-name km-ec2spot-ssm-<id>-use1 --policy-name $p --query PolicyDocument --output json | tr -d ' \n' | wc -c; done` — sum < 10240, record the number

- [ ] **Step 5: Go/no-go**

Go: every control above green → write the UAT record into `docs/superpowers/specs/2026-10-17-sandbox-role-least-privilege-design.md` (§5 results, dated) and commit; open the PR. No-go: repin v1.7.0 at the three sites, `make build`, `km init --dry-run=false`, recreate the canary, record what failed.

---

## Self-review (done at plan time)

- **Spec coverage.** §2 region Deny → T1; explicit grants → T1 (+T3 proves coverage); SSM scope + KMS pin → T1; threads conditions + poller SET change → T1, T4; stream table unchanged → T1 (kept); module versioning + three pins → T2; `km-` fallbacks → T5; initCommands WARN → T7; mail/CA unchanged → T1; canary → T10. §3 every row → T1 block. §4 compiler env emission → T5 (fail-loud form chosen over emitting four more env vars, because `KM_RESOURCE_PREFIX` already reaches every reader — ruling recorded in the ledger); update expressions/projection → T4; `LeadingKeys` → T1 (action-quota only; budgets left exactly as v1.7.0 per §3 "as today"); validate WARN → T7; guards → T2, T3, T4; docs + memory → T9. §5 module tests → T1 (`terraform validate` + structural); live controls → T10. §6 rollout → T10; `km doctor` WARN → T8. §7 half-deploys: no task needed (behaviour follows from the pin + binary). §8 out of scope: untouched.
- **Decisions the implementer must not re-open:** the four inbound queues are granted unconditionally (as v1.7.0 granted Slack/GitHub; the spec's "gated on its inbound flag as today" describes today's non-gating); SES From condition includes the display-name form; `s3:AbortMultipartUpload` added beside `PutObject` (large transcript/capture uploads are multipart).
- **Type consistency.** `pinnedEC2SpotModuleDir(t)` (T2) is used by T1's test after T2 and by T3; `defaultEmailDomain`/`effectiveEmailDomain` (T6) only inside `pkg/compiler`; `roleShapeIAM`/`checkSandboxRoleShape` (T8) only inside `internal/app/cmd`; `validateInitCommandAWSCalls` (T7) only inside `pkg/profile`.
- **Known risk, stated.** The `dynamodb:Attributes` + `StringEqualsIfExists` shape is from AWS's own fine-grained-access example; whether an unprojected GetItem is blocked is unverified and does not matter for the hole (writes always name attributes). The IAM aggregate inline size is unverified until the canary apply; the reduction order is pre-decided.
