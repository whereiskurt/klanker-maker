# gpu-launcher-account/v1.0.0
#
# Provisions the entire account-B footprint for cross-account capacity borrowing
# (Phase 126): the bounded launcher role, the pre-baked box role with its
# permissions boundary, and the results bucket. Account B (typically the org
# management account) is exempt from Service Control Policies, so the IAM
# policies in this file ARE the entire security boundary — there is no SCP
# backstop the way there is in the home application account. Applied by the
# `km account add` enrollment command with account B's own admin credentials —
# never by the platform's regional `km init`. See the module README and
# docs/superpowers/specs/2026-06-29-cross-account-gpu-launch-design.md §
# "The IAM containment".

data "aws_caller_identity" "current" {}

locals {
  account_id = data.aws_caller_identity.current.account_id

  # Whether to add the account-root ArnLike-pattern trust statement. Opt-in —
  # empty patterns (the default) mean only the exact-ARN statement below is
  # ever rendered as a real Allow.
  allow_root_delegation = length(var.trusted_principal_arn_patterns) > 0

  # Subnet / AZ / SG the launcher's IAM policy and the box instances are scoped
  # to — either what this module provisions itself (network.tf) or an existing
  # network passed straight through when provision_network = false.
  subnet_ids = var.provision_network ? aws_subnet.launcher[*].id : [var.subnet_id]
  availability_zones = var.provision_network ? aws_subnet.launcher[*].availability_zone : (
    length(data.aws_subnet.existing) > 0 ? [data.aws_subnet.existing[0].availability_zone] : [""]
  )
  security_group_id = var.provision_network ? aws_security_group.launcher[0].id : var.security_group_id

  subnet_arns = [for id in local.subnet_ids : "arn:aws:ec2:${var.region}:${local.account_id}:subnet/${id}"]

  results_bucket_name = "${var.resource_prefix}-results-${local.account_id}"
}

# ==============================================================================
# Launcher role — the single cross-account door into this account
# ==============================================================================

resource "aws_iam_role" "launcher" {
  name = "${var.resource_prefix}-gpu-launcher"

  # Trust policy. Three account-A principals are meant to be named up front in
  # var.trusted_principal_arns from the very first apply — the operator's own
  # role (local create), the *-create-handler Lambda role (remote create), and
  # the *-ttl-handler Lambda role (auto-reap on TTL expiry) — so that no second
  # privileged trip through this account's admin credentials is ever required.
  # Every statement carries the external-id condition; the external_id variable
  # validation refuses to render a condition-free (unbounded) trust policy.
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat(
      length(var.trusted_principal_arns) > 0 ? [
        {
          Sid    = "TrustNamedAccountAPrincipals"
          Effect = "Allow"
          Principal = {
            AWS = var.trusted_principal_arns
          }
          Action = "sts:AssumeRole"
          Condition = {
            StringEquals = {
              "sts:ExternalId" = var.external_id
            }
          }
        }
      ] : [],
      local.allow_root_delegation ? [
        {
          # Escape hatch for a principal whose exact ARN isn't knowable in
          # advance (e.g. an IAM Identity Center role, whose ARN embeds a
          # randomly-assigned suffix). This statement delegates to account A's
          # own administrator — broader than the exact-ARN statement above —
          # so it is opt-in (empty patterns by default) and is always paired
          # with BOTH the external-id condition AND an ArnLike test on the
          # calling principal's own ARN.
          Sid    = "TrustAccountARootWithArnPattern"
          Effect = "Allow"
          Principal = {
            AWS = "arn:aws:iam::${var.trust_account_id}:root"
          }
          Action = "sts:AssumeRole"
          Condition = {
            StringEquals = {
              "sts:ExternalId" = var.external_id
            }
            ArnLike = {
              "aws:PrincipalArn" = var.trusted_principal_arn_patterns
            }
          }
        }
      ] : []
    )
  })

  tags = {
    Name                 = "${var.resource_prefix}-gpu-launcher"
    "km:resource-prefix" = var.resource_prefix
    "km:purpose"         = "cross-account-gpu-launcher"
  }
}

# Launcher permission policy — the door's shape. Five statements, Sids kept
# stable so operators can correlate each one with the simulate-principal-policy
# matrix in 126-RESEARCH.md § Security Domain.
#
# Deliberately absent, on purpose, not by oversight: any iam:CreateRole /
# iam:PutRolePolicy / iam:AttachRolePolicy (or any other IAM write action), any
# organizations:* action (this IS the org management account — a careless
# policy here could accidentally grant Organizations API reach), any s3:*
# action (the launcher never touches S3 directly; only the box role and the
# results-bucket policy do), and any action scoped to another account. Even a
# fully compromised launcher role can only ever produce "one tagged, allowlisted
# GPU box in one subnet" — nothing else.
resource "aws_iam_role_policy" "launcher" {
  name = "${var.resource_prefix}-gpu-launcher-policy"
  role = aws_iam_role.launcher.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "LaunchOnlyGpuBoxes"
        Effect   = "Allow"
        Action   = "ec2:RunInstances"
        Resource = "arn:aws:ec2:${var.region}:${local.account_id}:instance/*"
        Condition = {
          StringEquals = {
            "ec2:InstanceType"             = var.instance_types
            "aws:RequestTag/km:managed-by" = "klankermaker"
          }
          # ArnEquals against a LIST of subnet ARNs matches ANY element in the
          # list — because network provisioning now creates one subnet per AZ
          # (T-126-22), this condition must test list membership, not equality
          # against a single scalar subnet ARN.
          ArnEquals = {
            "ec2:Subnet" = local.subnet_arns
          }
          StringLike = {
            "aws:RequestTag/km:sandbox-id" = "*"
          }
        }
      },
      {
        Sid    = "SupportingRunInstancesResources"
        Effect = "Allow"
        Action = "ec2:RunInstances"
        Resource = concat(
          [
            "arn:aws:ec2:${var.region}:${local.account_id}:volume/*",
            "arn:aws:ec2:${var.region}:${local.account_id}:network-interface/*",
            "arn:aws:ec2:${var.region}:${local.account_id}:security-group/${local.security_group_id}",
            "arn:aws:ec2:${var.region}::image/*",
            "arn:aws:ec2:${var.region}:${local.account_id}:key-pair/*",
          ],
          local.subnet_arns
        )
      },
      {
        # The launcher can attach ONLY the one pre-baked box role — never a
        # role it (or an attacker holding the launcher) creates on the fly —
        # and only when EC2 is the passed-to service. The launcher never needs
        # any role-creation permission at all.
        Sid      = "PassOnlyBoxRole"
        Effect   = "Allow"
        Action   = "iam:PassRole"
        Resource = aws_iam_role.box.arn
        Condition = {
          StringEquals = {
            "iam:PassedToService" = "ec2.amazonaws.com"
          }
        }
      },
      {
        # CREATE-time actions must gate on aws:RequestTag, never aws:ResourceTag.
        # At authorization time the resource does not exist yet, so it carries no
        # tags and an aws:ResourceTag condition can never match — the call is
        # denied unconditionally. This was originally folded into the
        # LifecycleTaggedOnly statement below and made every cross-account create
        # fail with:
        #
        #   UnauthorizedOperation: not authorized to perform: ec2:CreateVolume on
        #   resource: .../volume/* because no identity-based policy allows it
        #
        # ec2:CreateTags stays here too: the tag-on-create call terraform issues
        # alongside CreateVolume/RunInstances is likewise authorized against the
        # REQUESTED tags.
        Sid    = "CreateTaggedResources"
        Effect = "Allow"
        Action = [
          "ec2:CreateVolume",
          "ec2:CreateTags",
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "aws:RequestTag/km:managed-by" = "klankermaker"
          }
        }
      },
      {
        # MUTATE-time actions on resources that already exist, so aws:ResourceTag
        # is the correct condition key here — this is what confines the launcher
        # to boxes km itself created.
        Sid    = "LifecycleTaggedOnly"
        Effect = "Allow"
        Action = [
          "ec2:TerminateInstances",
          "ec2:StopInstances",
          "ec2:StartInstances",
          "ec2:AttachVolume",
          "ec2:DetachVolume",
          "ec2:DeleteVolume",
          "ec2:CreateTags",
        ]
        Resource = "*"
        Condition = {
          StringEquals = {
            "aws:ResourceTag/km:managed-by" = "klankermaker"
          }
        }
      },
      {
        Sid    = "ReadOnlyForCapacityCheck"
        Effect = "Allow"
        Action = [
          "ec2:Describe*",
          "servicequotas:GetServiceQuota",
          "servicequotas:ListServiceQuotas",
        ]
        Resource = "*"
      },
    ]
  })
}

# ==============================================================================
# Box role — the pre-baked, permissions-boundaried role attached to launched
# instances. The launcher can PassRole only this one role (see PassOnlyBoxRole
# above), so the box role's own envelope is the ceiling of what any launched
# instance can ever do, independent of the launcher.
# ==============================================================================

resource "aws_iam_role" "box" {
  name = "${var.resource_prefix}-gpu-box"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  # Load-bearing precisely because this account is exempt from Service Control
  # Policies — there is no SCP backstop here the way there is in the home
  # application account, so this boundary IS what stops a later widening of the
  # box role's own inline policy (below) from escaping its intended envelope.
  permissions_boundary = aws_iam_policy.box_boundary.arn

  tags = {
    Name                 = "${var.resource_prefix}-gpu-box"
    "km:resource-prefix" = var.resource_prefix
    "km:purpose"         = "cross-account-gpu-box"
  }
}

resource "aws_iam_instance_profile" "box" {
  name = "${var.resource_prefix}-gpu-box"
  role = aws_iam_role.box.name
}

resource "aws_iam_role_policy_attachment" "box_ssm" {
  role       = aws_iam_role.box.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

# Box role's own inline policy. Kept minimal for a lean serve box: SSM is
# granted via the managed-policy attachment above; this is everything else.
resource "aws_iam_role_policy" "box" {
  name = "${var.resource_prefix}-gpu-box-policy"
  role = aws_iam_role.box.id

  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = concat(local.box_statements, local.box_bedrock_statement)
  })
}

# Permissions boundary — caps the box role's EFFECTIVE permissions to the same
# envelope as its own inline policy above, independent of that policy, so a
# later widening of the inline policy alone cannot escape it.
resource "aws_iam_policy" "box_boundary" {
  name        = "${var.resource_prefix}-gpu-box-boundary"
  description = "Permissions boundary capping ${var.resource_prefix}-gpu-box's effective permissions. Mandatory (not defence-in-depth) because this account has no SCP layer."

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat(
      [
        {
          Sid    = "SSMCore"
          Effect = "Allow"
          Action = [
            "ssm:UpdateInstanceInformation",
            "ssmmessages:CreateControlChannel",
            "ssmmessages:CreateDataChannel",
            "ssmmessages:OpenControlChannel",
            "ssmmessages:OpenDataChannel",
            "ec2messages:AcknowledgeMessage",
            "ec2messages:DeleteMessage",
            "ec2messages:FailMessage",
            "ec2messages:GetEndpoint",
            "ec2messages:GetMessages",
            "ec2messages:SendReply",
          ]
          Resource = "*"
        },
      ],
      local.box_statements,
      local.box_bedrock_statement
    )
  })
}

locals {
  # Shared between the box role's own inline policy and its permissions
  # boundary, so the two envelopes stay in lockstep by construction.
  box_statements = [
    {
      # Object-get only — no PutObject — which is what makes this direction of
      # the cross-account boundary read-only (T-126-20).
      Sid      = "ReadOnlyHomeArtifacts"
      Effect   = "Allow"
      Action   = ["s3:GetObject"]
      Resource = "${var.artifacts_bucket_arn}/*"
    },
    {
      Sid    = "ReadWriteOwnResultsBucket"
      Effect = "Allow"
      Action = [
        "s3:GetObject",
        "s3:PutObject",
        "s3:ListBucket",
      ]
      Resource = [
        aws_s3_bucket.results.arn,
        "${aws_s3_bucket.results.arn}/*",
      ]
    },
    {
      Sid      = "ReadOwnInstanceTags"
      Effect   = "Allow"
      Action   = ["ec2:DescribeTags", "ec2:DescribeInstances"]
      Resource = "*"
    },
  ]

  box_bedrock_statement = var.enable_bedrock ? [
    {
      # Off by default. Bedrock traffic from a lean box in this account is
      # unmetered by design — there is no http-proxy MITM sidecar / budget
      # table in this account the way there is in the home account.
      Sid      = "OptionalBedrockInvoke"
      Effect   = "Allow"
      Action   = ["bedrock:InvokeModel", "bedrock:InvokeModelWithResponseStream"]
      Resource = "*"
    }
  ] : []
}

# ==============================================================================
# Results bucket — the outbound cross-account data path (account A reads this,
# read-only). Bucket-owner-enforced (ACLs disabled, access is policy-only),
# public access fully blocked, versioned and encrypted to match the home
# artifacts bucket's posture.
# ==============================================================================

resource "aws_s3_bucket" "results" {
  bucket = local.results_bucket_name

  tags = {
    Name                 = local.results_bucket_name
    "km:resource-prefix" = var.resource_prefix
    "km:purpose"         = "cross-account-gpu-results"
  }
}

resource "aws_s3_bucket_ownership_controls" "results" {
  bucket = aws_s3_bucket.results.id
  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_public_access_block" "results" {
  bucket = aws_s3_bucket.results.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_versioning" "results" {
  bucket = aws_s3_bucket.results.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "results" {
  bucket = aws_s3_bucket.results.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# Bucket policy: exactly two statements. The absence of any cross-account WRITE
# grant here is the invariant this module enforces, not an oversight — see
# "Storage model — the no-cross-account-write invariant" in the design spec.
resource "aws_s3_bucket_policy" "results" {
  bucket = aws_s3_bucket.results.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "BoxRoleReadWrite"
        Effect = "Allow"
        Principal = {
          AWS = aws_iam_role.box.arn
        }
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:ListBucket",
        ]
        Resource = [
          aws_s3_bucket.results.arn,
          "${aws_s3_bucket.results.arn}/*",
        ]
      },
      {
        # Read-shaped only: GetObject + ListBucket. Never PutObject — account
        # A's principals can read a B-launched box's results, never write them.
        Sid    = "TrustingAccountReadOnly"
        Effect = "Allow"
        Principal = {
          AWS = length(var.trusted_principal_arns) > 0 ? var.trusted_principal_arns : ["arn:aws:iam::${var.trust_account_id}:root"]
        }
        Action = [
          "s3:GetObject",
          "s3:ListBucket",
        ]
        Resource = [
          aws_s3_bucket.results.arn,
          "${aws_s3_bucket.results.arn}/*",
        ]
      },
    ]
  })

  depends_on = [aws_s3_bucket_public_access_block.results]
}
