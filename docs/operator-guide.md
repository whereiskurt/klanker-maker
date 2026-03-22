# Klanker Maker Operator Guide

This guide covers everything an operator needs to do **before** running `km init` — the full AWS account setup, cross-account wiring, and infrastructure bootstrap. When you finish this guide, you will have three AWS accounts configured, SSO working, Terraform state backends provisioned, SES verified, Lambda functions deployed, and `km init` successfully creating shared VPC infrastructure in your first region.

**Audience:** An experienced AWS operator who will set up the platform from scratch. You should be comfortable with AWS Organizations, IAM, Terraform, and the AWS CLI.

**Domain used in examples:** `klankermaker.ai` — replace with your own domain throughout.

**Account IDs used in examples:**

| Account | ID | Purpose |
|---|---|---|
| Management | `111111111111` | Route53, SSO, Organizations root |
| Terraform | `222222222222` | State storage, cross-account provisioning role |
| Application | `333333333333` | Sandbox workloads, all runtime infrastructure |

---

## 1. Account Structure

Klanker Maker uses a 3-account AWS model. Each account has a narrow, well-defined responsibility.

### Management Account (111111111111)

- **AWS Organizations** root account
- **Route53** hosted zone for your domain
- **IAM Identity Center (SSO)** — all human access flows through here
- No sandbox workloads run here

### Terraform Account (222222222222)

- **S3 state bucket** — stores all Terraform/Terragrunt state files
- **DynamoDB lock table** — prevents concurrent state modifications
- **Cross-account IAM role** — allows Terraform running with Terraform-account credentials to provision resources in the Application account
- No sandbox workloads run here

### Application Account (333333333333)

This is where sandboxes live. Resources include:

- VPCs, subnets, security groups (provisioned by `km init`)
- EC2 instances and/or ECS Fargate tasks (provisioned by `km create`)
- IAM roles and instance profiles for sandboxes
- SES domain identity with DKIM (sandbox email)
- S3 artifacts bucket (sandbox artifacts + inbound email storage)
- KMS key (`alias/km-sops`) for SOPS secret encryption
- TTL handler Lambda (Go, arm64, `provided.al2023`)
- ECS spot handler Lambda (Python, EventBridge-triggered)
- EventBridge Scheduler role for TTL enforcement
- CloudWatch log groups for sandbox and Lambda logs
- DynamoDB budget tables (future)

---

## 2. AWS Organizations Setup

### Create the Organization

From the account you want to be the management root:

```bash
aws organizations create-organization --feature-set ALL \
  --profile klanker-management
```

### Create the Terraform Account

```bash
aws organizations create-account \
  --email terraform@klankermaker.ai \
  --account-name "KlankerMaker-Terraform" \
  --profile klanker-management
```

Note the account ID from the output. Wait for the account to reach `ACTIVE` state:

```bash
aws organizations list-accounts --profile klanker-management \
  --query "Accounts[?Name=='KlankerMaker-Terraform'].[Id,Status]" \
  --output table
```

### Create the Application Account

```bash
aws organizations create-account \
  --email application@klankermaker.ai \
  --account-name "KlankerMaker-Application" \
  --profile klanker-management
```

### Enable IAM Identity Center

```bash
aws sso-admin list-instances --profile klanker-management
```

If no instances are returned, enable IAM Identity Center in the AWS Console (Management account > IAM Identity Center > Enable). SSO must be enabled in the same region as your Organizations home region.

---

## 3. SSO Configuration

### Create Permission Sets

Create an `AdministratorAccess` permission set for initial setup. You can scope this down later.

```bash
SSO_INSTANCE_ARN=$(aws sso-admin list-instances \
  --profile klanker-management \
  --query "Instances[0].InstanceArn" --output text)

aws sso-admin create-permission-set \
  --instance-arn "$SSO_INSTANCE_ARN" \
  --name "KMAdministratorAccess" \
  --session-duration "PT8H" \
  --profile klanker-management
```

### Assign Permission Set to Accounts

For each account (Management, Terraform, Application), assign your SSO user or group:

```bash
# Get the permission set ARN
PS_ARN=$(aws sso-admin list-permission-sets \
  --instance-arn "$SSO_INSTANCE_ARN" \
  --profile klanker-management \
  --query "PermissionSets[0]" --output text)

# Assign to each account — replace PRINCIPAL_ID and PRINCIPAL_TYPE
for ACCT_ID in 111111111111 222222222222 333333333333; do
  aws sso-admin create-account-assignment \
    --instance-arn "$SSO_INSTANCE_ARN" \
    --target-id "$ACCT_ID" \
    --target-type AWS_ACCOUNT \
    --permission-set-arn "$PS_ARN" \
    --principal-type USER \
    --principal-id "YOUR_SSO_USER_ID" \
    --profile klanker-management
done
```

### Configure AWS CLI Profiles

Add three named profiles to `~/.aws/config`. Replace the `sso_start_url`, `sso_account_id` values, and `sso_region` with your actual values:

```ini
# ~/.aws/config

[profile klanker-management]
sso_start_url  = https://d-1234567890.awsapps.com/start
sso_region     = us-east-1
sso_account_id = 111111111111
sso_role_name  = KMAdministratorAccess
region         = us-east-1
output         = json

[profile klanker-terraform]
sso_start_url  = https://d-1234567890.awsapps.com/start
sso_region     = us-east-1
sso_account_id = 222222222222
sso_role_name  = KMAdministratorAccess
region         = us-east-1
output         = json

[profile klanker-application]
sso_start_url  = https://d-1234567890.awsapps.com/start
sso_region     = us-east-1
sso_account_id = 333333333333
sso_role_name  = KMAdministratorAccess
region         = us-east-1
output         = json
```

Login to all three:

```bash
aws sso login --profile klanker-management
aws sso login --profile klanker-terraform
aws sso login --profile klanker-application
```

Verify each one:

```bash
aws sts get-caller-identity --profile klanker-management
aws sts get-caller-identity --profile klanker-terraform
aws sts get-caller-identity --profile klanker-application
```

---

## 4. Management Account Setup

### Register or Transfer Your Domain

If you have not already registered your domain:

```bash
# Check availability
aws route53domains check-domain-availability \
  --domain-name klankermaker.ai \
  --profile klanker-management
```

Domain registration is typically done through the Console (Route53 > Registered domains > Register).

### Create a Route53 Hosted Zone

If one does not already exist for your domain:

```bash
aws route53 create-hosted-zone \
  --name klankermaker.ai \
  --caller-reference "km-$(date +%s)" \
  --profile klanker-management
```

Note the hosted zone ID and the NS records from the output. If you registered the domain elsewhere, update your registrar's nameservers to point to the Route53 NS records.

### Delegate a Subdomain to the Application Account (Optional)

If you want sandbox DNS managed in the Application account (recommended for SES), create a hosted zone in the Application account and delegate from Management:

```bash
# In the Application account — create the zone
aws route53 create-hosted-zone \
  --name klankermaker.ai \
  --caller-reference "km-app-$(date +%s)" \
  --profile klanker-application

# Note the NS records from the output, then add them in Management:
MGMT_ZONE_ID="Z0123456789ABCDEFGHIJ"  # your management hosted zone ID

aws route53 change-resource-record-sets \
  --hosted-zone-id "$MGMT_ZONE_ID" \
  --profile klanker-management \
  --change-batch '{
    "Changes": [{
      "Action": "UPSERT",
      "ResourceRecordSet": {
        "Name": "klankermaker.ai",
        "Type": "NS",
        "TTL": 300,
        "ResourceRecords": [
          {"Value": "ns-111.awsdns-11.com."},
          {"Value": "ns-222.awsdns-22.net."},
          {"Value": "ns-333.awsdns-33.org."},
          {"Value": "ns-444.awsdns-44.co.uk."}
        ]
      }
    }]
  }'
```

If you keep DNS entirely in the Management account, the SES Terraform module will need cross-account Route53 access. The delegation approach is simpler.

---

## 5. Terraform Account Setup

### S3 State Bucket

Klanker Maker uses the naming convention `tf-km-state-{region-label}` where region labels are shortened forms (e.g., `use1` for `us-east-1`, `cac1` for `ca-central-1`). See the `RegionLabel()` function in `pkg/compiler/region.go` for the full mapping.

Region label examples:
- `us-east-1` -> `use1`
- `us-west-2` -> `usw2`
- `ca-central-1` -> `cac1`
- `eu-west-1` -> `euw1`
- `ap-southeast-1` -> `apse1`

Create the state bucket for your primary region:

```bash
REGION_LABEL="use1"

aws s3api create-bucket \
  --bucket "tf-km-state-${REGION_LABEL}" \
  --region us-east-1 \
  --profile klanker-terraform

# Enable versioning (required for state safety)
aws s3api put-bucket-versioning \
  --bucket "tf-km-state-${REGION_LABEL}" \
  --versioning-configuration Status=Enabled \
  --profile klanker-terraform

# Enable server-side encryption
aws s3api put-bucket-encryption \
  --bucket "tf-km-state-${REGION_LABEL}" \
  --server-side-encryption-configuration '{
    "Rules": [{
      "ApplyServerSideEncryptionByDefault": {
        "SSEAlgorithm": "aws:kms"
      },
      "BucketKeyEnabled": true
    }]
  }' \
  --profile klanker-terraform

# Block all public access
aws s3api put-public-access-block \
  --bucket "tf-km-state-${REGION_LABEL}" \
  --public-access-block-configuration '{
    "BlockPublicAcls": true,
    "IgnorePublicAcls": true,
    "BlockPublicPolicy": true,
    "RestrictPublicBuckets": true
  }' \
  --profile klanker-terraform
```

### DynamoDB Lock Table

```bash
aws dynamodb create-table \
  --table-name "tf-km-locks-${REGION_LABEL}" \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST \
  --region us-east-1 \
  --profile klanker-terraform
```

### Cross-Account Provisioning Role

This IAM role lives in the **Application account** (333333333333) but is assumable by the Terraform account (222222222222). It grants the Terraform account the ability to provision sandbox infrastructure.

Create the role in the Application account:

```bash
aws iam create-role \
  --role-name km-terraform-provisioner \
  --profile klanker-application \
  --assume-role-policy-document '{
    "Version": "2012-10-17",
    "Statement": [{
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::222222222222:root"
      },
      "Action": "sts:AssumeRole",
      "Condition": {
        "StringEquals": {
          "sts:ExternalId": "km-provisioner"
        }
      }
    }]
  }'
```

Attach a managed policy. For initial setup, `AdministratorAccess` works; scope it down once you know exactly which services are needed:

```bash
aws iam attach-role-policy \
  --role-name km-terraform-provisioner \
  --policy-arn arn:aws:iam::aws:policy/AdministratorAccess \
  --profile klanker-application
```

Grant the Terraform account permission to assume this role. In the Terraform account, create a policy and attach it to the SSO/CI role that runs Terraform:

```bash
aws iam create-policy \
  --policy-name km-assume-application \
  --profile klanker-terraform \
  --policy-document '{
    "Version": "2012-10-17",
    "Statement": [{
      "Effect": "Allow",
      "Action": "sts:AssumeRole",
      "Resource": "arn:aws:iam::333333333333:role/km-terraform-provisioner"
    }]
  }'
```

---

## 6. Application Account Setup

All commands in this section use `--profile klanker-application`.

### KMS Key for SOPS

Klanker Maker uses SOPS for encrypting secrets in the repo. The KMS key alias must be `alias/km-sops` (referenced in `infra/live/site.hcl`).

```bash
KEY_ID=$(aws kms create-key \
  --description "Klanker Maker SOPS encryption key" \
  --profile klanker-application \
  --query "KeyMetadata.KeyId" --output text)

aws kms create-alias \
  --alias-name alias/km-sops \
  --target-key-id "$KEY_ID" \
  --profile klanker-application
```

Allow the Terraform account to use this key for encrypting/decrypting SOPS files:

```bash
aws kms create-grant \
  --key-id "$KEY_ID" \
  --grantee-principal "arn:aws:iam::222222222222:root" \
  --operations Decrypt Encrypt GenerateDataKey \
  --profile klanker-application
```

### S3 Artifacts Bucket

This bucket stores sandbox artifacts (uploaded on exit) and inbound SES email under the `mail/` prefix:

```bash
ARTIFACTS_BUCKET="km-artifacts-333333333333-use1"

aws s3api create-bucket \
  --bucket "$ARTIFACTS_BUCKET" \
  --region us-east-1 \
  --profile klanker-application

aws s3api put-bucket-versioning \
  --bucket "$ARTIFACTS_BUCKET" \
  --versioning-configuration Status=Enabled \
  --profile klanker-application

aws s3api put-public-access-block \
  --bucket "$ARTIFACTS_BUCKET" \
  --public-access-block-configuration '{
    "BlockPublicAcls": true,
    "IgnorePublicAcls": true,
    "BlockPublicPolicy": true,
    "RestrictPublicBuckets": true
  }' \
  --profile klanker-application
```

### SES Domain Verification with DKIM

The SES Terraform module (`infra/modules/ses/v1.0.0`) handles this declaratively, but if you want to verify manually or bootstrap before Terraform:

```bash
# Verify domain identity
aws ses verify-domain-identity \
  --domain klankermaker.ai \
  --profile klanker-application \
  --region us-east-1

# Enable DKIM signing
aws ses verify-domain-dkim \
  --domain klankermaker.ai \
  --profile klanker-application \
  --region us-east-1
```

The output provides DKIM tokens. Create three CNAME records in your Route53 hosted zone:

```
<token1>._domainkey.klankermaker.ai  CNAME  <token1>.dkim.amazonses.com
<token2>._domainkey.klankermaker.ai  CNAME  <token2>.dkim.amazonses.com
<token3>._domainkey.klankermaker.ai  CNAME  <token3>.dkim.amazonses.com
```

Also create the verification TXT record and MX record:

```
_amazonses.klankermaker.ai   TXT   "<verification-token>"
klankermaker.ai              MX    10 inbound-smtp.us-east-1.amazonaws.com
```

**Production SES access:** By default, new SES accounts are in sandbox mode (can only send to verified addresses). Request production access through the AWS Console (SES > Account dashboard > Request production access) once you are ready to send lifecycle notification emails to arbitrary recipients.

### TTL Handler Lambda

The TTL handler is a Go Lambda (arm64, `provided.al2023` runtime) that fires when a sandbox TTL expires. It uploads artifacts and sends notification emails.

Build the Lambda binary:

```bash
cd lambda/ttl-handler
GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o bootstrap .
zip ttl-handler.zip bootstrap
```

Deploy using the Terraform module at `infra/modules/ttl-handler/v1.0.0`:

```hcl
# infra/live/use1/ttl-handler/terragrunt.hcl
terraform {
  source = "${local.repo_root}/infra/modules/ttl-handler/v1.0.0"
}

inputs = {
  lambda_zip_path     = "${get_terragrunt_dir()}/../../../../lambda/ttl-handler/ttl-handler.zip"
  artifact_bucket_name = "km-artifacts-333333333333-use1"
  artifact_bucket_arn  = "arn:aws:s3:::km-artifacts-333333333333-use1"
  email_domain         = "klankermaker.ai"
  operator_email       = "ops@klankermaker.ai"
}
```

The module creates:
- IAM role `km-ttl-handler` with policies for CloudWatch Logs, S3, SES, and EventBridge Scheduler
- Lambda function `km-ttl-handler`
- CloudWatch log group `/aws/lambda/km-ttl-handler`
- Permission for EventBridge Scheduler to invoke the Lambda

### ECS Spot Handler Lambda

The ECS spot handler catches Fargate Spot interruptions and triggers artifact upload via ECS Exec before the task is terminated. Deploy using `infra/modules/ecs-spot-handler/v1.0.0`:

```hcl
# infra/live/use1/ecs-spot-handler/terragrunt.hcl
terraform {
  source = "${local.repo_root}/infra/modules/ecs-spot-handler/v1.0.0"
}

inputs = {
  artifact_bucket_name = "km-artifacts-333333333333-use1"
  ecs_cluster_arn      = "arn:aws:ecs:us-east-1:333333333333:cluster/km-use1"
}
```

The module creates:
- IAM role `km-ecs-spot-handler` with policies for CloudWatch Logs and ECS Exec
- Lambda function `km-ecs-spot-handler` (Python 3.12)
- EventBridge rule watching for `ECS Task State Change` events with `stopCode: SpotInterruption`
- CloudWatch log group `/aws/lambda/km-ecs-spot-handler`

### EventBridge Scheduler Role

The `km create` command creates one-time EventBridge Scheduler rules for TTL enforcement. The scheduler needs an IAM role to invoke the TTL Lambda:

```bash
aws iam create-role \
  --role-name km-scheduler-invoke-ttl \
  --profile klanker-application \
  --assume-role-policy-document '{
    "Version": "2012-10-17",
    "Statement": [{
      "Effect": "Allow",
      "Principal": {
        "Service": "scheduler.amazonaws.com"
      },
      "Action": "sts:AssumeRole"
    }]
  }'

TTL_LAMBDA_ARN="arn:aws:lambda:us-east-1:333333333333:function:km-ttl-handler"

aws iam put-role-policy \
  --role-name km-scheduler-invoke-ttl \
  --policy-name invoke-ttl-lambda \
  --profile klanker-application \
  --policy-document "{
    \"Version\": \"2012-10-17\",
    \"Statement\": [{
      \"Effect\": \"Allow\",
      \"Action\": \"lambda:InvokeFunction\",
      \"Resource\": \"${TTL_LAMBDA_ARN}\"
    }]
  }"
```

---

## 7. km configure

The `km configure` command (or manual file creation) writes `~/.km/config.yaml` with the values needed by the CLI. Configuration is loaded from this file, then overridden by `KM_` environment variables, then by CLI flags.

Create the file:

```yaml
# ~/.km/config.yaml

# Directories searched for SandboxProfile YAML files (in order)
profile_search_paths:
  - ./profiles
  - ~/.km/profiles

# Log verbosity: trace, debug, info, warn, error
log_level: info

# S3 bucket for Terraform state (in the Terraform account)
state_bucket: tf-km-state-use1

# TTL Lambda ARN — set to enable TTL enforcement via EventBridge Scheduler
ttl_lambda_arn: arn:aws:lambda:us-east-1:333333333333:function:km-ttl-handler

# EventBridge Scheduler role ARN — used when creating TTL schedules
scheduler_role_arn: arn:aws:iam::333333333333:role/km-scheduler-invoke-ttl
```

These values can also be set via environment variables:

```bash
export KM_STATE_BUCKET="tf-km-state-use1"
export KM_TTL_LAMBDA_ARN="arn:aws:lambda:us-east-1:333333333333:function:km-ttl-handler"
export KM_SCHEDULER_ROLE_ARN="arn:aws:iam::333333333333:role/km-scheduler-invoke-ttl"
```

---

## 8. km init

`km init` provisions the shared VPC and networking that all sandboxes in a region use. Run it once per region before your first `km create` targeting that region.

```bash
km init --region us-east-1 --aws-profile klanker-application
```

### What It Provisions

The init command:

1. Creates the region directory structure: `infra/live/<region-label>/network/` and `infra/live/<region-label>/sandboxes/`
2. Writes `region.hcl` with the region label and full region name
3. Copies the network Terragrunt template from `infra/templates/network.terragrunt.hcl`
4. Runs `terragrunt apply` against the `network/v1.0.0` module
5. Saves outputs to `infra/live/<region-label>/network/outputs.json`

### Resources Created

- **VPC** with CIDR `10.0.0.0/16` (configurable in the network template)
- **2 public subnets** (`10.0.1.0/24`, `10.0.2.0/24`) across 2 AZs
- **2 private subnets** (`10.0.101.0/24`, `10.0.102.0/24`)
- **Internet gateway** with public route table
- **NAT gateway** (disabled by default; enable in the template if needed)
- **Security group: sandbox-mgmt** — SSM-only access, no SSH ingress
- **Security group: sandbox-internal** — intra-VPC sidecar communication

### Region Label Mapping

The `RegionLabel()` function in `pkg/compiler/region.go` converts full region names to short labels used in resource naming:

| Region | Label | State Bucket | Lock Table |
|---|---|---|---|
| `us-east-1` | `use1` | `tf-km-state-use1` | `tf-km-locks-use1` |
| `us-west-2` | `usw2` | `tf-km-state-usw2` | `tf-km-locks-usw2` |
| `ca-central-1` | `cac1` | `tf-km-state-cac1` | `tf-km-locks-cac1` |
| `eu-west-1` | `euw1` | `tf-km-state-euw1` | `tf-km-locks-euw1` |
| `ap-southeast-1` | `apse1` | `tf-km-state-apse1` | `tf-km-locks-apse1` |

### outputs.json

After init completes, the outputs file is written to `infra/live/<region-label>/network/outputs.json` and contains:

```json
{
  "vpc_id": { "value": "vpc-0abc123def456789" },
  "public_subnets": { "value": ["subnet-0aaa...", "subnet-0bbb..."] },
  "availability_zones": { "value": ["us-east-1a", "us-east-1b"] },
  "sandbox_mgmt_sg_id": { "value": "sg-0ccc..." }
}
```

The `km create` command reads this file to place sandboxes in the correct VPC and subnets.

---

## 9. Verification Checklist

Run through this checklist after completing all setup steps.

### Identity Verification

```bash
# Each should return the correct account ID
aws sts get-caller-identity --profile klanker-management
# Expected: Account = 111111111111

aws sts get-caller-identity --profile klanker-terraform
# Expected: Account = 222222222222

aws sts get-caller-identity --profile klanker-application
# Expected: Account = 333333333333
```

### Terraform State Access

```bash
# Verify the state bucket exists and is accessible
aws s3 ls s3://tf-km-state-use1/ --profile klanker-terraform

# Verify the lock table exists
aws dynamodb describe-table \
  --table-name tf-km-locks-use1 \
  --profile klanker-terraform \
  --query "Table.TableStatus"
```

### Cross-Account Role Assumption

```bash
aws sts assume-role \
  --role-arn arn:aws:iam::333333333333:role/km-terraform-provisioner \
  --role-session-name km-test \
  --external-id km-provisioner \
  --profile klanker-terraform \
  --query "Credentials.AccessKeyId"
```

### KMS Key

```bash
aws kms describe-key \
  --key-id alias/km-sops \
  --profile klanker-application \
  --query "KeyMetadata.KeyState"
# Expected: Enabled
```

### SES Verification

```bash
# Check domain verification status
aws ses get-identity-verification-attributes \
  --identities klankermaker.ai \
  --profile klanker-application \
  --region us-east-1
# Expected: VerificationStatus = Success

# Check DKIM verification
aws ses get-identity-dkim-attributes \
  --identities klankermaker.ai \
  --profile klanker-application \
  --region us-east-1
# Expected: DkimVerificationStatus = Success
```

### SES Send Test

```bash
aws ses send-email \
  --from "test@klankermaker.ai" \
  --destination "ToAddresses=your-verified-email@example.com" \
  --message "Subject={Data='KM SES Test'},Body={Text={Data='SES is working.'}}" \
  --profile klanker-application \
  --region us-east-1
```

Note: while still in SES sandbox mode, the `--destination` address must be a verified email address.

### Lambda Functions

```bash
aws lambda invoke \
  --function-name km-ttl-handler \
  --payload '{"sandbox_id":"test","action":"ping"}' \
  --profile klanker-application \
  /dev/stdout

aws lambda get-function \
  --function-name km-ecs-spot-handler \
  --profile klanker-application \
  --query "Configuration.State"
# Expected: Active
```

### Network (post km-init)

```bash
# Verify VPC was created
aws ec2 describe-vpcs \
  --filters "Name=tag:km:purpose,Values=shared-sandbox-vpc" \
  --profile klanker-application \
  --region us-east-1 \
  --query "Vpcs[0].VpcId"
```

---

## 10. Multi-Region

### Initialize Additional Regions

Each new region needs its own state bucket and lock table in the Terraform account, then a `km init` run.

```bash
# Example: add ca-central-1 (label: cac1)
NEW_REGION="ca-central-1"
NEW_LABEL="cac1"

# 1. Create state bucket in the Terraform account
aws s3api create-bucket \
  --bucket "tf-km-state-${NEW_LABEL}" \
  --region "$NEW_REGION" \
  --create-bucket-configuration LocationConstraint="$NEW_REGION" \
  --profile klanker-terraform

aws s3api put-bucket-versioning \
  --bucket "tf-km-state-${NEW_LABEL}" \
  --versioning-configuration Status=Enabled \
  --profile klanker-terraform

aws s3api put-bucket-encryption \
  --bucket "tf-km-state-${NEW_LABEL}" \
  --server-side-encryption-configuration '{
    "Rules": [{
      "ApplyServerSideEncryptionByDefault": {
        "SSEAlgorithm": "aws:kms"
      },
      "BucketKeyEnabled": true
    }]
  }' \
  --profile klanker-terraform

aws s3api put-public-access-block \
  --bucket "tf-km-state-${NEW_LABEL}" \
  --public-access-block-configuration '{
    "BlockPublicAcls": true,
    "IgnorePublicAcls": true,
    "BlockPublicPolicy": true,
    "RestrictPublicBuckets": true
  }' \
  --profile klanker-terraform

# 2. Create DynamoDB lock table
aws dynamodb create-table \
  --table-name "tf-km-locks-${NEW_LABEL}" \
  --attribute-definitions AttributeName=LockID,AttributeType=S \
  --key-schema AttributeName=LockID,KeyType=HASH \
  --billing-mode PAY_PER_REQUEST \
  --region "$NEW_REGION" \
  --profile klanker-terraform

# 3. Create artifacts bucket in the Application account
aws s3api create-bucket \
  --bucket "km-artifacts-333333333333-${NEW_LABEL}" \
  --region "$NEW_REGION" \
  --create-bucket-configuration LocationConstraint="$NEW_REGION" \
  --profile klanker-application

# 4. Initialize the region
km init --region "$NEW_REGION" --aws-profile klanker-application
```

### S3 Replication

The `infra/modules/s3-replication/v1.0.0` module replicates the `artifacts/` prefix from a source bucket to a destination bucket in another region. The `mail/` prefix is not replicated (it contains ephemeral inbound email).

Replication requires versioning on both buckets (handled by the module).

```hcl
# infra/live/use1/s3-replication/terragrunt.hcl
terraform {
  source = "${local.repo_root}/infra/modules/s3-replication/v1.0.0"
}

inputs = {
  source_bucket_name      = "km-artifacts-333333333333-use1"
  source_bucket_arn       = "arn:aws:s3:::km-artifacts-333333333333-use1"
  destination_bucket_name = "km-artifacts-333333333333-cac1"
  destination_region      = "ca-central-1"
}
```

The module creates:
- A replica bucket in the destination region
- An IAM role (`km-s3-replication-<source-bucket>`) with replication permissions
- Replication configuration on the source bucket filtering to `artifacts/` prefix

When deploying additional regions, consider whether you need:
- **Unidirectional replication** (primary -> secondary) — one replication module instance
- **Bidirectional replication** (both directions) — two module instances, one per direction

SES is region-specific. If you need email in a new region, deploy the SES module there too and verify the domain in that region.
