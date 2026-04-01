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
- DynamoDB tables: `km-budgets` (budget enforcement), `km-identities` (sandbox identity), `km-sandboxes` (sandbox metadata)

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
3. Copies the network Terragrunt template from `infra/templates/network/`
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

---

## 11. km bootstrap

`km bootstrap` validates your platform configuration and provisions the shared bootstrap infrastructure: Terraform state backend, DynamoDB tables, KMS key, and the SCP containment policy. Run it once after `km configure` and before `km init`.

```bash
# Dry run (default) — shows what would be created without making changes
km bootstrap

# Provision for real — requires management account credentials for SCP deployment
km bootstrap --dry-run=false
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--dry-run` | `true` | Print planned resources without making any AWS API calls |

### Prerequisite

`km bootstrap` requires a `km-config.yaml` file (in the current directory or the repo root). Create it with `km configure` first:

```bash
km configure
# Set domain, primary region, management/terraform/application account IDs, SSO start URL
```

### What it provisions

**Dry run output:**

```
Config: /path/to/km-config.yaml
Domain:  klankermaker.ai
Region:  us-east-1
Management account: 111111111111
Application account: 333333333333

Dry run — the following infrastructure would be created:

  S3 bucket:         km-terraform-state-<hash>
    Purpose:         Terraform state and sandbox metadata
    Encryption:      aws:kms (KMS key below)
    Versioning:      enabled

  DynamoDB table:    km-terraform-lock
    Purpose:         Terraform state locking
    Billing:         PAY_PER_REQUEST

  KMS key:           km-terraform-state
    Purpose:         S3 state bucket encryption
    Deletion window: 30 days

  DynamoDB table:    km-budgets
    Purpose:         Sandbox budget enforcement tracking
    Billing:         PAY_PER_REQUEST

  SCP Policy:        km-sandbox-containment
    Target:          Application account (333333333333)
    Threat coverage: SG mutation, network escape, instance mutation,
                     IAM escalation, storage exfiltration, SSM pivot,
                     Organizations discovery, region lock
    Trusted roles:   AWSReservedSSO_*_*, km-provisioner-*, km-lifecycle-*,
                     km-ecs-spot-handler, km-ttl-handler
    Deploy via:      km bootstrap (management account credentials required)

Run 'km bootstrap --dry-run=false' to provision.
```

The `km-identities` DynamoDB table (for sandbox identity / Ed25519 public keys) is provisioned separately via the `infra/live/{region}/dynamodb-identities/` Terragrunt unit.

### SCP deployment

With `--dry-run=false` and a management account ID configured, `km bootstrap` runs `terragrunt apply` against `infra/live/management/scp/` to attach the `km-sandbox-containment` SCP to the application account. This requires credentials for the management account.

If no management account is configured (single-account setup), SCP deployment is skipped with a notice.

---

## 12. Budget Enforcement Infrastructure

Budget enforcement is a three-part system: DynamoDB tables for limit and spend tracking, a per-sandbox Lambda for compute cost enforcement, and an EventBridge schedule for periodic evaluation.

### DynamoDB km-budgets Table

The `km-budgets` table stores budget limits and cumulative spend per sandbox.

**Table schema:**

| Attribute | Type | Role |
|-----------|------|------|
| `sandbox_id` | String (S) | Hash key |
| `compute_limit` | Number (N) | Maximum compute spend in USD |
| `ai_limit` | Number (N) | Maximum AI API spend in USD |
| `compute_spent` | Number (N) | Accumulated compute spend via ADD expression |
| `ai_spent` | Number (N) | Accumulated AI API spend via ADD expression |
| `warning_threshold` | Number (N) | Fraction (0–1) at which warning email fires (default 0.80) |
| `created_at` | String (S) | ISO8601 sandbox creation timestamp |
| `spot_rate_usd` | Number (N) | EC2 spot instance rate (USD/hr, embedded at creation time) |

DynamoDB Streams are enabled with `NEW_AND_OLD_IMAGES` so the budget enforcer Lambda can read before/after spend values on stream events.

### Budget Enforcer Lambda

Each sandbox gets a dedicated Lambda named `km-budget-enforcer-{sandbox-id}`. The Lambda:

1. Reads the sandbox's `spot_rate_usd` and `created_at` from DynamoDB
2. Calculates accumulated compute cost: `(now - created_at) * spot_rate_usd`
3. Writes the computed value back with DynamoDB SET (idempotent — Lambda recalculates absolute cost each run)
4. If compute spend exceeds `compute_limit`: stops the EC2 instance or ECS task and sends a warning notification
5. If AI spend exceeds `ai_limit` (tracked by http-proxy MITM): detaches the Bedrock IAM policy from the sandbox role

The Lambda is triggered by an EventBridge Scheduler schedule (one per sandbox) that fires every minute.

**Naming convention:** `km-budget-enforcer-{sandbox-id}` (e.g., `km-budget-enforcer-sb-7f3a9e12`)

**Build:** `make build-lambdas` produces `build/budget-enforcer.zip` (Go, arm64, `provided.al2023` runtime).

### Budget Top-Up with km budget add

When a sandbox is suspended due to budget exhaustion:

```bash
# Add $5 to the compute budget and $3 to the AI budget
km budget add sb-7f3a9e12 --compute 5.00 --ai 3.00

# Output:
# Budget updated: compute $2.00/$7.00, AI $4.80/$7.80
# Sandbox sb-7f3a9e12 resumed.
```

`km budget add` reads the current budget from DynamoDB, adds the top-up amount, writes the new limits, then auto-resumes the sandbox:
- **EC2**: starts stopped instances via `StartInstances`
- **ECS**: re-provisions the task by re-running `terragrunt apply` with the stored profile
- **IAM**: re-attaches `AmazonBedrockFullAccess` if it was detached by the budget enforcer

See the [Budget Guide](budget-guide.md) for detailed AI spend metering (per-model token pricing, proxy interception, DynamoDB ADD atomics).

---

## 13. SCP Sandbox Containment

The `km-sandbox-containment` SCP is an AWS Organizations Service Control Policy applied to the application account. It provides a second layer of defense — even if a sandbox IAM role is misconfigured or a policy is missing, the SCP blocks the most dangerous breakout actions.

### Deployment

Deploy via `km bootstrap --dry-run=false` (requires management account credentials), or apply directly:

```bash
cd infra/live/management/scp
terragrunt apply
```

The Terragrunt unit runs against the management account profile (`klanker-terraform` by default in the SCP apply path). It creates the `km-sandbox-containment` SCP and attaches it to the application account.

### Prerequisites

The management account must have Organizations SCPs enabled:

```bash
aws organizations enable-policy-type \
  --root-id r-xxxx \
  --policy-type SERVICE_CONTROL_POLICY \
  --profile klanker-management
```

### Deny Statements

The SCP has 8 deny statements:

| Statement | Blocked Actions | Carve-outs |
|-----------|----------------|------------|
| `DenySGMutation` | Create/delete/modify security groups | `trusted_arns_base` (SSO, provisioner, lifecycle roles) |
| `DenyNetworkEscape` | Create VPC, subnet, route table, IGW, NAT gateway, VPC peering, Transit Gateway | `trusted_arns_base` |
| `DenyInstanceMutation` | `RunInstances`, `ModifyInstanceAttribute`, `ModifyInstanceMetadataOptions` | `trusted_arns_instance` (base + `km-ecs-spot-handler`) |
| `DenyIAMEscalation` | `CreateRole`, `AttachRolePolicy`, `DetachRolePolicy`, `PassRole`, `AssumeRole` | `trusted_arns_iam` (base + `km-budget-enforcer-*`) |
| `DenyStorageExfiltration` | `CreateSnapshot`, `CopySnapshot`, `CreateImage`, `CopyImage`, `ExportImage` | `trusted_arns_base` |
| `DenySSMPivot` | `SendCommand`, `StartSession` | `trusted_arns_ssm` (EC2 SSM roles, `km-github-token-refresher-*`, SSO roles only) |
| `DenyOrganizationsDiscovery` | `ListAccounts`, `DescribeOrganization`, `ListRoots`, etc. | **None** — applies to ALL roles |
| `DenyOutsideAllowedRegions` | All regional actions outside `var.allowed_regions` | Global services (`iam:*`, `sts:*`, `route53:*`, `s3:ListAllMyBuckets`, etc.) are excluded via `NotAction`; **no role carve-out** |

**Key design decisions:**

- `km-ecs-task-*` is intentionally NOT carved out from any deny — it IS the sandbox workload and must be fully contained
- `DenyOrganizationsDiscovery` has no condition — all application account roles are blocked from enumerating the org structure
- Region lock uses `NotAction` (not `Deny` on specific actions) so global services work regardless of the requested region; it applies to operators too, not just sandbox roles
- `km-budget-enforcer-*` gets an IAM carve-out specifically for `AttachRolePolicy`/`DetachRolePolicy` (Bedrock revocation on budget breach) — it does not need `CreateRole` or `PassRole`

### Trusted Role Arns (variables.tf)

Pass `trusted_role_arns` to the module with your SSO permission set ARNs and any provisioner/lifecycle roles:

```hcl
# infra/live/management/scp/terragrunt.hcl
inputs = {
  application_account_id = "333333333333"
  allowed_regions        = ["us-east-1", "us-west-2"]
  trusted_role_arns = [
    "arn:aws:iam::*:role/AWSReservedSSO_KMAdministratorAccess_*",
    "arn:aws:iam::333333333333:role/km-provisioner",
    "arn:aws:iam::333333333333:role/km-ttl-handler",
  ]
}
```

See [Security Model](security-model.md) for the full threat analysis of each SCP statement.

---

## 14. Sidecar Build Pipeline

Klanker Maker sidecars are platform-level processes injected into every sandbox at launch time. On EC2, they are OS-level binaries; on ECS Fargate, they are sidecar containers in the task definition.

### Sidecars

| Sidecar | Role | EC2 Artifact | ECS Image |
|---------|------|-------------|-----------|
| `dns-proxy` | DNS allowlist enforcement — NXDOMAIN for unlisted names | S3 binary | `km-dns-proxy` ECR image |
| `http-proxy` | HTTP/HTTPS MITM proxy — enforces host allowlist, meters AI API calls | S3 binary | `km-http-proxy` ECR image |
| `audit-log` | Command and process audit logging to CloudWatch | S3 binary | `km-audit-log` ECR image |
| `tracing` | OpenTelemetry trace collection (config only) | S3 config.yaml | `km-tracing` ECR image |

### Building EC2 Binaries (make sidecars)

`make sidecars` cross-compiles the three Go sidecars for `linux/amd64` and uploads them to the artifacts S3 bucket under `sidecars/`:

```bash
# Set the artifacts bucket
export KM_ARTIFACTS_BUCKET=km-artifacts-333333333333-use1

# Cross-compile and upload
make sidecars
```

Output:
```
s3://km-artifacts-333333333333-use1/sidecars/dns-proxy
s3://km-artifacts-333333333333-use1/sidecars/http-proxy
s3://km-artifacts-333333333333-use1/sidecars/audit-log
s3://km-artifacts-333333333333-use1/sidecars/tracing/config.yaml
```

**Build flags:** `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`

To compile locally without uploading:
```bash
make build-sidecars
# Writes: build/dns-proxy, build/http-proxy, build/audit-log
```

### Building ECS Container Images (make ecr-push)

`make ecr-push` builds Docker images for all four sidecars and pushes them to ECR. It calls `ecr-login` and `ecr-repos` first to ensure repositories exist:

```bash
# Build and push with default VERSION=latest
make ecr-push

# Build and push a specific version tag
VERSION=1.2.3 make ecr-push
```

ECR repository names: `km-dns-proxy`, `km-http-proxy`, `km-audit-log`, `km-tracing`

The ECR registry is derived from the calling AWS account: `{account-id}.dkr.ecr.{region}.amazonaws.com`

**Platform:** `linux/amd64` (Docker `buildx build --platform`)

**Tracing build context:** `sidecars/tracing/` (tracing is not a Go binary and does not import shared packages from `pkg/`)

**Audit-log build context:** Uses `sidecars/audit-log/Dockerfile` building from `sidecars/audit-log/cmd/` (the `cmd/` subdirectory holds `package main`; the root is the `auditlog` library package)

### Building Lambda Deployment Packages (make build-lambdas)

Lambda functions (ttl-handler, budget-enforcer, github-token-refresher) are Go binaries built for `linux/arm64`:

```bash
make build-lambdas
```

Output:
```
build/ttl-handler.zip          — TTL auto-destroy handler
build/budget-enforcer.zip      — Per-sandbox compute spend enforcer
build/github-token-refresher.zip — GitHub installation token refresh
```

All Lambda zips use `GOARCH=arm64` matching the `architectures=[arm64]` Terraform variable in each Lambda module (mismatch causes exec format error).

### KM_SIDECAR_VERSION

The `KM_SIDECAR_VERSION` environment variable controls which ECR image tag the compiler emits for ECS task definitions. Defaults to `latest` when unset. The deploy pipeline sets an explicit version tag (e.g., `1.2.3`) using `VERSION=1.2.3 make ecr-push` and then `KM_SIDECAR_VERSION=1.2.3 km create ...`.

---

## 15. GitHub App Setup

GitHub App integration provides sandboxes with short-lived, scoped installation tokens for private repository access. Each sandbox gets a dedicated Lambda (`km-github-token-refresher-{sandbox-id}`) that refreshes the token into SSM every 45 minutes.

### Automated Setup (--setup flag)

The fastest path uses the GitHub manifest flow — one command opens the browser, creates the App, and stores credentials in SSM automatically:

```bash
km configure github --setup
```

Flow:
1. Starts a local HTTP callback server on a random port
2. Builds the GitHub App manifest JSON (name: `klanker-maker-sandbox`, contents:write permissions)
3. Opens `https://github.com/settings/apps/new?manifest=...` in the system browser
4. Waits up to 5 minutes for the GitHub OAuth callback
5. Exchanges the code for App credentials via `POST /app-manifests/{code}/conversions`
6. Writes three SSM parameters (see below)
7. Fetches installations via `GET /app/installations` — if found, writes installation ID automatically

If the browser does not open, the URL is printed so you can copy it manually. The callback server listens only on `127.0.0.1`.

**If no installations are found after App creation:**

```bash
# 1. Visit the App's installation URL (printed by km configure github --setup):
#    https://github.com/apps/klanker-maker-sandbox/installations/new
# 2. Select the org or account to install on
# 3. Note the installation ID from the URL
# 4. Register it:
km configure github --installation-id <ID>
```

### Manual Setup

If you create the GitHub App manually in GitHub Settings:

```bash
km configure github
# Interactive wizard prompts for:
# - GitHub App Client ID (e.g. Iv1.abc123)
# - Path to private key PEM file (downloaded from GitHub App settings)
# - Installation ID
```

Or non-interactively (for CI):

```bash
km configure github \
  --non-interactive \
  --app-client-id "Iv1.abc123" \
  --private-key-file /path/to/private-key.pem \
  --installation-id 12345678
```

To overwrite existing parameters:

```bash
km configure github --force ...
```

### SSM Parameter Layout

`km configure github` writes three SSM parameters in the `klanker-terraform` AWS profile:

| Parameter | Type | Contents |
|-----------|------|----------|
| `/km/config/github/app-client-id` | String | GitHub App client ID (e.g. `Iv1.abc123`) |
| `/km/config/github/private-key` | SecureString | Full PEM-encoded RSA private key |
| `/km/config/github/installation-id` | String | Installation ID for the target org/user |

The private key is stored as a SecureString using the SSM default service KMS key (or the platform KMS key when configured).

### GitHub Token Refresh Lambda

When `km create` is run with a profile that includes `sourceAccess.github`, it provisions:

- Lambda: `km-github-token-refresher-{sandbox-id}` (Go, arm64, `provided.al2023`)
- EventBridge Scheduler schedule firing every 45 minutes
- SSM path for the token: `/sandbox/{sandbox-id}/github-token` (KMS-encrypted)

The Lambda generates a GitHub App installation token, encrypts it with the platform KMS key, and puts it in SSM. The sandbox reads it at git time via `GIT_ASKPASS` — the token never appears in environment variables.

**IAM / SCP interaction:** `km-github-token-refresher-*` is carved out of `DenySSMPivot` only (it needs `ssm:GetParameter` and `ssm:PutParameter`). It is not carved out from instance mutation, IAM escalation, or network escape statements.

**Cleanup:** The Lambda and EventBridge schedule are destroyed when the sandbox is destroyed via `km destroy`.

---

## 16. DynamoDB km-identities Table

The `km-identities` table stores sandbox identity records — Ed25519 key pairs generated at sandbox creation time for signed email and inter-sandbox trust.

### Table Schema

| Attribute | Type | Role |
|-----------|------|------|
| `sandbox_id` | String (S) | Hash key (sole key — no sort key) |
| `public_key` | String (S) | Base64-encoded Ed25519 public key |
| `created_at` | String (S) | ISO8601 creation timestamp |
| `email_sign_policy` | String (S) | Profile's `spec.email.signing` value (omitted if not set) |
| `email_verify_policy` | String (S) | Profile's `spec.email.verifyInbound` value (omitted if not set) |
| `email_encrypt_policy` | String (S) | Profile's `spec.email.encryption` value (omitted if not set) |
| `alias` | String (S) | Human-friendly alias from `spec.email.alias` (omitted if not set) |
| `allowed_senders` | StringSet (SS) | Allowed sender addresses from `spec.email.allowedSenders` (omitted if not set) |

**Design notes:**
- `sandbox_id` is the sole hash key — one identity row per sandbox
- No DynamoDB Streams — identity reads are on-demand lookups, no Lambda trigger needed
- Empty-string attributes are omitted (not written) to preserve legacy row compatibility without schema migration
- A GSI (`alias-index`) is available for looking up identity records by alias

### Provisioning

The `km-identities` table is deployed separately from the `km-budgets` table:

```bash
# Deploy the identities DynamoDB table
cd infra/live/use1/dynamodb-identities
terragrunt apply
```

`km doctor` checks for the table's existence and reports `WARN` (not `ERROR`) if absent — the identity table is treated as optional infrastructure.

### Key Generation

Ed25519 key pairs are generated at `km create` time using KMS. The private key is stored encrypted in KMS (alias: `km-platform` or from `KM_PLATFORM_KMS_KEY_ARN`). The public key is stored in plaintext in `km-identities` for verification by other sandboxes.

See [Multi-Agent Email](multi-agent-email.md) for the full signed email and NaCl encryption protocol.

---

## 16b. DynamoDB km-sandboxes Table

The `km-sandboxes` table stores sandbox metadata -- the same data that was previously stored as `metadata.json` files in S3. Moving to DynamoDB makes `km list` significantly faster (single Scan vs N+1 S3 GetObject calls) and makes `km lock`/`km unlock` atomic (conditional UpdateItem, no read-modify-write race condition).

### Table Schema

| Attribute | Type | Role |
|-----------|------|------|
| `sandbox_id` | String (S) | Hash key |
| `profile` | String (S) | Profile name used to create the sandbox |
| `substrate` | String (S) | `ec2`, `ecs`, or `docker` |
| `region` | String (S) | AWS region |
| `status` | String (S) | Current status: `running`, `paused`, `stopped`, `killed`, `reaped` |
| `alias` | String (S) | Human-friendly alias |
| `locked` | Boolean (BOOL) | Whether the sandbox is locked (prevents destroy/stop/pause) |
| `locked_at` | String (S) | ISO8601 timestamp of when lock was applied |
| `created_at` | String (S) | ISO8601 creation timestamp |
| `ttl_expiry` | String (S) | ISO8601 TTL expiry timestamp |
| `ttl_remaining` | String (S) | Human-readable TTL remaining |

### Provisioning

The `km-sandboxes` table is created by `km init`:

```bash
km init --region us-east-1
```

The table uses `PAY_PER_REQUEST` billing mode. No DynamoDB Streams are needed.

### Backward Compatibility

All commands that read sandbox metadata try DynamoDB first. If the `km-sandboxes` table does not exist (`ResourceNotFoundException`), they fall back to S3 (`metadata.json` files in the state bucket). This means the platform works without the table -- it just uses the slower S3 path.

S3 is still used for:
- Terraform state files
- Sandbox artifacts (uploaded on exit)
- Profile YAML copies
- Rsync snapshots
- Inbound email storage

### Lambda Environment

Lambda functions that need to read sandbox metadata (TTL handler, lifecycle handler) use the `SANDBOX_TABLE_NAME` environment variable:

```hcl
environment {
  variables = {
    SANDBOX_TABLE_NAME = "km-sandboxes"
  }
}
```

---

## 17. km doctor

`km doctor` checks platform health and bootstrap verification. Run it after initial setup and when sandboxes behave unexpectedly.

```bash
km doctor
```

### What It Checks

All checks run in parallel. Results are sorted alphabetically by check name.

| Check | What it verifies |
|-------|-----------------|
| `Config` | Required config fields present: `domain`, `management_account_id`, `terraform_account_id`, `application_account_id`, `sso_start_url`, `primary_region` |
| `Credentials (klanker-terraform)` | STS `GetCallerIdentity` succeeds with the configured AWS profile |
| `State Bucket` | S3 `HeadBucket` on the configured state bucket |
| `Budget Table (km-budgets)` | DynamoDB `DescribeTable` on `km-budgets` (or custom budget table name) |
| `Identity Table (km-identities)` | DynamoDB `DescribeTable` on `km-identities` — reported as WARN not ERROR if absent |
| `KMS Key (km-platform)` | KMS `DescribeKey` on `alias/km-platform` |
| `SCP (Sandbox Containment)` | Organizations `ListPoliciesForTarget` on the application account — verifies `km-sandbox-containment` is attached |
| `GitHub App Config` | SSM `GetParameter` for `/km/config/github/app-client-id` and `installation-id` — reported as WARN if missing (GitHub integration is optional) |
| `VPC (us-east-1)` | EC2 `DescribeVpcs` filtered by `km:managed=true` in the primary region |
| `Active Sandboxes` | Lists all sandboxes from the state bucket and reports a count summary |

If `KM_REPLICA_REGION` is set, a second `VPC` check runs for the replica region.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--json` | `false` | Output results as a JSON array |
| `--quiet` | `false` | Suppress OK and SKIPPED results; show only WARN and ERROR |

### Exit Codes

- `0` — all checks passed (OK or WARN — warnings do not fail)
- `1` — one or more checks returned ERROR

### Output Formats

**Standard (terminal):**

```
✓ Active Sandboxes                    total=2 (running=2)
✓ Budget Table (km-budgets)           table "km-budgets" exists
✓ Config                              domain=klankermaker.ai region=us-east-1
✓ Credentials (klanker-terraform)     authenticated as arn:aws:iam::333333333333:role/...
⚠ GitHub App Config                   parameter "/km/config/github/app-client-id" not found — GitHub integration not configured
  → Run 'km configure github' to set up GitHub App integration
✓ Identity Table (km-identities)      table "km-identities" exists
✓ KMS Key (km-platform)               key "alias/km-platform" exists
✓ SCP (Sandbox Containment)           policy "km-sandbox-containment" attached to account 333333333333
✓ State Bucket                        bucket "tf-km-state-use1" is accessible
✓ VPC (us-east-1)                     found 1 km-managed VPC(s) in us-east-1

11 checks passed, 1 warnings, 0 errors
```

**JSON output (for CI):**

```bash
km doctor --json | jq '.[] | select(.status != "OK")'
```

```json
{
  "name": "GitHub App Config",
  "status": "WARN",
  "message": "parameter \"/km/config/github/app-client-id\" not found — GitHub integration not configured",
  "remediation": "Run 'km configure github' to set up GitHub App integration"
}
```

**Quiet mode (CI — exit code only):**

```bash
km doctor --quiet && echo "platform healthy" || echo "platform has issues"
```

### CI Usage Patterns

```yaml
# GitHub Actions — block deploys if platform health fails
- name: Platform health check
  run: km doctor --quiet
  env:
    KM_STATE_BUCKET: ${{ vars.KM_STATE_BUCKET }}
    AWS_PROFILE: klanker-terraform

# Parse errors programmatically
- name: Check for errors
  run: |
    ERRORS=$(km doctor --json | jq '[.[] | select(.status == "ERROR")] | length')
    if [ "$ERRORS" -gt 0 ]; then exit 1; fi
```

**Nil-client behavior:** Any AWS client that fails to initialize (expired credentials, missing config) causes its checks to return `SKIPPED` — `km doctor` never panics on missing clients.

---

## eBPF Network Enforcement

### Overview

Phase 40 introduced eBPF-based network enforcement as an alternative to the traditional iptables DNAT + proxy sidecar approach. When a profile sets `spec.network.enforcement: "ebpf"` or `"both"`, the sandbox uses cgroup-attached BPF programs to enforce network allowlists at the kernel level.

### How It Works

The `km-ebpf-enforcer` systemd service runs `km ebpf-attach` which:

1. Creates a cgroup at `/sys/fs/cgroup/km.slice/km-{sandboxID}.scope`
2. Loads 4 BPF programs and attaches them to the cgroup
3. Pre-seeds the `allowed_cidrs` LPM trie map with VPC CIDRs, IMDS, and pre-resolved hosts
4. Starts a DNS resolver on 127.0.0.1:53 that enforces `allowedDNSSuffixes` and dynamically populates the BPF map with resolved IPs
5. Starts a ring buffer consumer for structured audit events
6. Pins all programs and maps to `/sys/fs/bpf/km/{sandboxID}/`

### BPF Programs

| Program | Hook | Purpose |
|---------|------|---------|
| `connect4` | cgroup/connect4 | TCP connect() — allow/deny/redirect to proxy |
| `sendmsg4` | cgroup/sendmsg4 | UDP — redirect DNS to resolver |
| `sockops` | sockops | TCP state — map port→cookie for proxy |
| `egress` | cgroup_skb/egress | Packet-level L3 backstop — drop denied |

### BPF Maps (pinned to bpffs)

| Map | Type | Purpose |
|-----|------|---------|
| `allowed_cidrs` | LPM_TRIE | IPv4 CIDR allowlist (populated by DNS resolver) |
| `http_proxy_ips` | HASH | IPs that need L7 proxy inspection |
| `sock_to_original_ip/port` | HASH | Real destination before BPF rewrite |
| `src_port_to_sock` | HASH | Proxy looks up socket by peer port |
| `socket_pid_map` | HASH | PID for audit logging |
| `events` | RINGBUF | Deny/redirect events to userspace |

### Enforcement Modes

Set via `spec.network.enforcement` in the profile:

- **`proxy`** (default) — traditional iptables DNAT + proxy sidecars. Backward compatible. Works on all substrates.
- **`ebpf`** — pure eBPF cgroup enforcement. No iptables. Root bypass fixed. EC2 only.
- **`both`** — eBPF primary + proxy sidecars for L7 inspection. EC2 only.

### Cleanup

`km destroy` calls `cleanupEBPF(sandboxID)` which:
1. Checks if BPF programs are pinned at `/sys/fs/bpf/km/{sandboxID}/`
2. Removes all pinned programs and maps
3. Removes the sandbox cgroup

On remote destroy (Lambda), bpffs is cleaned up automatically when the EC2 instance terminates (bpffs is an in-memory filesystem).

### Build

eBPF bytecode is compiled via `make generate-ebpf` using a Docker container with clang/bpf2go. The compiled bytecode is embedded in the `km` binary — no runtime clang dependency on the sandbox instance.

### Requirements

- Linux kernel 5.15+ (AL2023 ships 6.1+)
- Cgroup v2 (AL2023 default)
- bpffs mounted at `/sys/fs/bpf` (AL2023 default)
- EC2 substrate only (Docker/ECS fall back to proxy mode)

### Diagram

See [`docs/diagrams/ebpf-architecture.excalidraw`](diagrams/ebpf-architecture.excalidraw) for the full architecture diagram.
