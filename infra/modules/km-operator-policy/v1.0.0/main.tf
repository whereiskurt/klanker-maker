# cloudwatch_logs intentionally NOT extracted — Lambda-specific (see RESEARCH.md Open Question 1)

data "aws_caller_identity" "current" {}

# Policy: S3 artifact bucket access (profile download, artifact upload)
resource "aws_iam_role_policy" "s3_artifacts" {
  name = "${var.resource_prefix}-create-handler-s3"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "ArtifactBucketAccess"
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:ListBucket",
          "s3:DeleteObject",
        ]
        Resource = [
          var.artifact_bucket_arn,
          "${var.artifact_bucket_arn}/*",
        ]
      }
    ]
  })
}

# Policy: DynamoDB access for Terraform state locking and budget tracking
resource "aws_iam_role_policy" "dynamodb" {
  name = "${var.resource_prefix}-create-handler-dynamodb"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "TerraformStateLock"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
          # Terragrunt v0.69+ calls DescribeTable on the lock table during
          # backend init to confirm it exists / decide whether to bootstrap.
          # Without it init fails with AccessDenied on the lock table even
          # when item-level read/write would work fine.
          "dynamodb:DescribeTable",
        ]
        Resource = "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.dynamodb_table_name}"
      },
      {
        Sid    = "BudgetTable"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
        ]
        Resource = var.dynamodb_budget_table_arn
      }
    ]
  })
}

# Policy: DynamoDB km-sandboxes — read/write sandbox metadata
resource "aws_iam_role_policy" "dynamodb_sandboxes" {
  name = "${var.resource_prefix}-create-handler-dynamodb-sandboxes"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SandboxMetadataTable"
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
          "dynamodb:Scan",
          "dynamodb:Query",
        ]
        Resource = [
          "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.sandbox_table_name}",
          "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.sandbox_table_name}/index/alias-index",
          "arn:aws:dynamodb:*:${data.aws_caller_identity.current.account_id}:table/${var.identities_table_name}",
        ]
      }
    ]
  })
}

# Policy: Terraform state S3 access (state read/write for km create subprocess)
resource "aws_iam_role_policy" "terraform_state" {
  name = "${var.resource_prefix}-create-handler-tf-state"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "TerraformStateAccess"
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject",
          "s3:ListBucket",
          # Terragrunt v0.69+ introspects bucket-level config (policy,
          # public-access block, encryption, versioning, lifecycle,
          # replication) before each terraform init to decide whether to
          # bootstrap the backend. Without these, init fails with
          # AccessDenied on bucket-level ops even when state read/write
          # works fine. GetBucketPolicy + GetBucketPublicAccessBlock
          # surfaced after the bucket-level batch in 76a614f shipped.
          "s3:GetBucketPolicy",
          "s3:GetBucketPublicAccessBlock",
          "s3:GetBucketVersioning",
          "s3:GetBucketLocation",
          "s3:GetEncryptionConfiguration",
          "s3:GetLifecycleConfiguration",
          "s3:GetReplicationConfiguration",
        ]
        Resource = [
          "arn:aws:s3:::${var.state_bucket}",
          "arn:aws:s3:::${var.state_bucket}/*",
        ]
      }
    ]
  })
}

# Policy: EC2 provisioning for sandbox creation
resource "aws_iam_role_policy" "ec2_provisioning" {
  name = "${var.resource_prefix}-create-handler-ec2"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "EC2Provision"
        Effect = "Allow"
        Action = [
          "ec2:RunInstances",
          "ec2:DescribeInstances",
          "ec2:DescribeInstanceTypes",
          "ec2:DescribeInstanceAttribute",
          "ec2:DescribeInstanceCreditSpecifications",
          "ec2:CreateVolume",
          "ec2:AttachVolume",
          "ec2:DetachVolume",
          "ec2:DeleteVolume",
          "ec2:DescribeVolumes",
          "ec2:CreateSecurityGroup",
          "ec2:AuthorizeSecurityGroupEgress",
          "ec2:AuthorizeSecurityGroupIngress",
          "ec2:RevokeSecurityGroupEgress",
          "ec2:DeleteSecurityGroup",
          "ec2:DescribeSecurityGroups",
          "ec2:DescribeSecurityGroupRules",
          "ec2:TerminateInstances",
          "ec2:CreateTags",
          "ec2:DeleteTags",
          "ec2:DescribeTags",
          "ec2:DescribeVpcs",
          "ec2:DescribeSubnets",
          "ec2:DescribeAvailabilityZones",
          "ec2:DescribeImages",
          "ec2:DescribeSpotPriceHistory",
          "ec2:RequestSpotInstances",
          "ec2:CancelSpotInstanceRequests",
          "ec2:DescribeSpotInstanceRequests",
          "ec2:DescribeNetworkInterfaces",
          "ec2:DescribeVolumes",
          "ec2:ModifyInstanceAttribute",
        ]
        Resource = "*"
      }
    ]
  })
}

# Policy: IAM role and instance profile management for sandbox EC2 roles
resource "aws_iam_role_policy" "iam_sandbox" {
  name = "${var.resource_prefix}-create-handler-iam"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "IAMRoleManagement"
        Effect = "Allow"
        Action = [
          "iam:CreateRole",
          "iam:PutRolePolicy",
          "iam:DeleteRolePolicy",
          "iam:DeleteRole",
          "iam:GetRole",
          "iam:GetRolePolicy",
          "iam:ListRolePolicies",
          "iam:AttachRolePolicy",
          "iam:DetachRolePolicy",
          "iam:ListAttachedRolePolicies",
          "iam:TagRole",
          "iam:UntagRole",
        ]
        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.resource_prefix}-*",
        ]
      },
      {
        Sid    = "IAMInstanceProfile"
        Effect = "Allow"
        Action = [
          "iam:CreateInstanceProfile",
          "iam:AddRoleToInstanceProfile",
          "iam:RemoveRoleFromInstanceProfile",
          "iam:DeleteInstanceProfile",
          "iam:GetInstanceProfile",
          "iam:ListInstanceProfilesForRole",
          "iam:TagInstanceProfile",
          "iam:UntagInstanceProfile",
        ]
        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:instance-profile/${var.resource_prefix}-*",
        ]
      },
      {
        Sid    = "IAMPassRole"
        Effect = "Allow"
        Action = ["iam:PassRole"]
        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.resource_prefix}-*",
        ]
      },
      {
        Sid      = "STSCallerIdentity"
        Effect   = "Allow"
        Action   = ["sts:GetCallerIdentity"]
        Resource = "*"
      }
    ]
  })
}

# Policy: ECS cluster and task management for sandbox workloads
resource "aws_iam_role_policy" "ecs_provisioning" {
  name = "${var.resource_prefix}-create-handler-ecs"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "ECSManagement"
        Effect = "Allow"
        Action = [
          "ecs:CreateCluster",
          "ecs:DeleteCluster",
          "ecs:RegisterTaskDefinition",
          "ecs:DeregisterTaskDefinition",
          "ecs:CreateService",
          "ecs:DeleteService",
          "ecs:UpdateService",
          "ecs:DescribeServices",
          "ecs:DescribeClusters",
          "ecs:DescribeTaskDefinition",
          "ecs:ListTaskDefinitions",
          "ecs:TagResource",
          "ecs:UntagResource",
        ]
        Resource = "*"
      }
    ]
  })
}

# Policy: EventBridge Scheduler for TTL schedule creation
resource "aws_iam_role_policy" "scheduler" {
  name = "${var.resource_prefix}-create-handler-scheduler"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SchedulerManagement"
        Effect = "Allow"
        Action = [
          "scheduler:CreateSchedule",
          "scheduler:DeleteSchedule",
          "scheduler:GetSchedule",
          "scheduler:UpdateSchedule",
        ]
        Resource = "arn:aws:scheduler:*:${data.aws_caller_identity.current.account_id}:schedule/default/${var.resource_prefix}-*"
      },
      {
        Sid    = "SchedulerPassRole"
        Effect = "Allow"
        Action = ["iam:PassRole"]
        Resource = [
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.resource_prefix}-ttl-scheduler",
          "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/${var.resource_prefix}-budget-scheduler-*",
        ]
      }
    ]
  })
}

# Policy: SSM Parameter Store for safe phrase and GitHub token
resource "aws_iam_role_policy" "ssm" {
  name = "${var.resource_prefix}-create-handler-ssm"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SSMParameterAccess"
        Effect = "Allow"
        Action = [
          "ssm:GetParameter",
          "ssm:PutParameter",
          "ssm:DeleteParameter",
        ]
        Resource = [
          # Operator config + per-sandbox identity all live under the
          # resource_prefix (see pkg/aws/identity.go:99). The previous
          # hardcoded /km/* and /sandbox/* denied access on non-default-prefix
          # installs — Lambda couldn't read /kph/config/... or write
          # /kph/sandbox/{id}/signing-key.
          "arn:aws:ssm:*:${data.aws_caller_identity.current.account_id}:parameter/${var.resource_prefix}/*",
        ]
      }
    ]
  })
}

# Policy: SSM SendCommand for runtime env var injection into newly-launched
# sandboxes. Used by:
#   - Phase 63 Step 11d (KM_SLACK_CHANNEL_ID injection — non-fatal)
#   - Phase 67 Step 11e (KM_SLACK_INBOUND_QUEUE_URL injection — fatal)
# Document scope: AWS-RunShellScript only. Target scope: km-tagged EC2
# instances (KMSandboxID tag matches sandbox IDs, prevents lateral movement).
resource "aws_iam_role_policy" "ssm_send_command" {
  name = "${var.resource_prefix}-create-handler-ssm-send-command"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SSMSendCommandDocument"
        Effect = "Allow"
        Action = [
          "ssm:SendCommand",
        ]
        Resource = [
          "arn:aws:ssm:*::document/AWS-RunShellScript",
        ]
      },
      {
        Sid    = "SSMSendCommandTaggedInstances"
        Effect = "Allow"
        Action = [
          "ssm:SendCommand",
        ]
        Resource = [
          "arn:aws:ec2:*:${data.aws_caller_identity.current.account_id}:instance/*",
        ]
        Condition = {
          StringLike = {
            "ssm:resourceTag/KMSandboxID" = "*"
          }
        }
      },
      {
        Sid    = "SSMGetCommandInvocation"
        Effect = "Allow"
        Action = [
          "ssm:GetCommandInvocation",
          "ssm:ListCommandInvocations",
        ]
        Resource = "*"
      }
    ]
  })
}

# Policy: SES for sandbox creation notification emails
resource "aws_iam_role_policy" "ses_send" {
  name = "${var.resource_prefix}-create-handler-ses"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["ses:SendEmail", "ses:SendRawEmail"]
        Resource = "*"
      }
    ]
  })
}

# Policy: Lambda management for per-sandbox budget-enforcer functions
resource "aws_iam_role_policy" "lambda_budget" {
  name = "${var.resource_prefix}-create-handler-lambda"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "LambdaPerSandbox"
        Effect = "Allow"
        Action = ["lambda:*"]
        Resource = [
          "arn:aws:lambda:*:${data.aws_caller_identity.current.account_id}:function:${var.resource_prefix}-*",
        ]
      }
    ]
  })
}

# Policy: KMS for encrypting/decrypting sandbox secrets
resource "aws_iam_role_policy" "kms" {
  name = "${var.resource_prefix}-create-handler-kms"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["kms:*"]
        Resource = "*"
      }
    ]
  })
}

# Policy: EventBridge for publishing sandbox lifecycle events (destroy, create).
# km kill publishes a destroy event to the default bus to trigger the teardown
# Lambda. Scoped to the default bus in the account only.
resource "aws_iam_role_policy" "eventbridge" {
  name = "${var.resource_prefix}-create-handler-eventbridge"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "EventBridgePutEvents"
        Effect   = "Allow"
        Action   = ["events:PutEvents"]
        Resource = "arn:aws:events:*:${data.aws_caller_identity.current.account_id}:event-bus/default"
      }
    ]
  })
}

# Policy: SQS for Phase 67 inbound queue lifecycle (per-sandbox FIFO queues
# named km-slack-inbound-<sandbox-id>.fifo). km create provisions the queue
# at create time; rollback deletes it on failure. Scoped to the inbound
# queue ARN pattern only.
resource "aws_iam_role_policy" "sqs_slack_inbound" {
  name = "${var.resource_prefix}-create-handler-sqs-slack-inbound"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SlackInboundQueueLifecycle"
        Effect = "Allow"
        Action = [
          "sqs:CreateQueue",
          "sqs:DeleteQueue",
          "sqs:GetQueueAttributes",
          "sqs:GetQueueUrl",
          "sqs:SetQueueAttributes",
          "sqs:TagQueue",
        ]
        Resource = "arn:aws:sqs:*:${data.aws_caller_identity.current.account_id}:${var.resource_prefix}-slack-inbound-*.fifo"
      }
    ]
  })
}

# Policy: IAM OIDC provider management for cluster-irsa module.
# register=true branch: Terraform creates/deletes/tags aws_iam_openid_connect_provider.this[0].
# register=false branch: Terraform reads data.aws_iam_openid_connect_provider.existing[0]
#   via ListOpenIDConnectProviders + GetOpenIDConnectProvider.
# Note: iam:ListOpenIDConnectProviders only accepts Resource: "*" per IAM docs (no
# resource-level restriction supported for List actions). Using "*" for all actions
# matches the existing kms and ses_send policies for consistency.
resource "aws_iam_role_policy" "oidc_provider" {
  name = "${var.resource_prefix}-create-handler-oidc"
  role = var.role_id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "OIDCProviderManagement"
        Effect = "Allow"
        Action = [
          "iam:CreateOpenIDConnectProvider",
          "iam:DeleteOpenIDConnectProvider",
          "iam:GetOpenIDConnectProvider",
          "iam:ListOpenIDConnectProviders",
          "iam:TagOpenIDConnectProvider",
          "iam:UntagOpenIDConnectProvider",
        ]
        Resource = "*"
      }
    ]
  })
}
