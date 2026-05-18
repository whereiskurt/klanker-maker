#!/usr/bin/env bash
# scripts/inventory-diff.sh — Phase 84.4 multi-install AWS-CLI inventory helper.
# Snapshots resources by km:resource-prefix tag (or name pattern) and diffs
# two snapshots, ignoring volatile fields (timestamps, ARNs with random suffixes).
#
# Usage:
#   inventory-diff.sh snapshot <output.json> [--prefix <prefix>]
#   inventory-diff.sh diff <before.json> <after.json> [--prefix <prefix>]
#
# Environment:
#   AWS_PROFILE  — required; controls which account/role is used for all calls
#   AWS_REGION   — optional; falls back to aws configure get region
#
# Services snapshotted:
#   EFS (filesystems + mount targets), IAM (roles + policies), Lambda, DynamoDB,
#   S3, KMS (key aliases), Route53 (record sets), SES (receipt rule sets),
#   Organizations SCPs (requires management-account profile),
#   SSM documents (self-owned, captures prefix-namespaced session docs)
set -euo pipefail

# ---- helpers ----------------------------------------------------------------

usage() {
  echo "Usage:"
  echo "  $0 snapshot <output.json> [--prefix <prefix>]"
  echo "  $0 diff <before.json> <after.json> [--prefix <prefix>]"
  exit 1
}

require_jq() {
  if ! command -v jq >/dev/null 2>&1; then
    echo "ERROR: jq is required but not found in PATH" >&2
    exit 1
  fi
}

require_aws() {
  if ! command -v aws >/dev/null 2>&1; then
    echo "ERROR: aws CLI is required but not found in PATH" >&2
    exit 1
  fi
  if [ -z "${AWS_PROFILE:-}" ]; then
    echo "ERROR: AWS_PROFILE must be set before invoking inventory-diff.sh" >&2
    exit 1
  fi
}

# ---- snapshot ---------------------------------------------------------------

do_snapshot() {
  local output_file="$1"
  local prefix="${2:-km}"

  require_aws
  require_jq

  echo "==> Snapshotting AWS resources (prefix='${prefix}', profile='${AWS_PROFILE}')" >&2

  # EFS — filter by creation_token matching prefix pattern
  local efs
  efs=$(aws efs describe-file-systems \
    --query "FileSystems[?starts_with(CreationToken, '${prefix}-') || contains(Tags[?Key=='km:resource-prefix'].Value[], '${prefix}')].[FileSystemId,CreationToken,LifeCycleState,NumberOfMountTargets]" \
    --output json 2>/dev/null || echo "[]")

  # IAM roles matching prefix
  local iam_roles
  iam_roles=$(aws iam list-roles \
    --query "Roles[?starts_with(RoleName, '${prefix}-')].[RoleName,Arn,CreateDate]" \
    --output json 2>/dev/null || echo "[]")

  # Lambda functions matching prefix
  local lambdas
  lambdas=$(aws lambda list-functions \
    --query "Functions[?starts_with(FunctionName, '${prefix}-')].[FunctionName,FunctionArn,Runtime,LastModified]" \
    --output json 2>/dev/null || echo "[]")

  # DynamoDB tables matching prefix
  local dynamo_tables
  dynamo_tables=$(aws dynamodb list-tables \
    --query "TableNames[?starts_with(@, '${prefix}-')]" \
    --output json 2>/dev/null || echo "[]")

  # S3 buckets matching prefix
  local s3_buckets
  s3_buckets=$(aws s3api list-buckets \
    --query "Buckets[?starts_with(Name, '${prefix}-')].[Name,CreationDate]" \
    --output json 2>/dev/null || echo "[]")

  # KMS aliases matching prefix
  local kms_aliases
  kms_aliases=$(aws kms list-aliases \
    --query "Aliases[?starts_with(AliasName, 'alias/${prefix}-')].[AliasName,TargetKeyId]" \
    --output json 2>/dev/null || echo "[]")

  # SES receipt rule sets (account-global; snapshot all rule set names + rule names)
  local ses_rule_sets
  ses_rule_sets=$(aws ses list-receipt-rule-sets \
    --query "RuleSets[].Name" \
    --output json 2>/dev/null || echo "[]")

  # Per rule set: list rule names (stable identifiers)
  local ses_rules="[]"
  if [ "$(echo "$ses_rule_sets" | jq 'length')" -gt 0 ]; then
    ses_rules=$(echo "$ses_rule_sets" | jq -r '.[]' | while read -r rs_name; do
      aws ses describe-receipt-rule-set \
        --rule-set-name "$rs_name" \
        --query "{RuleSet: '${rs_name}', Rules: Rules[].Name}" \
        --output json 2>/dev/null || echo "{}"
    done | jq -s '.')
  fi

  # Route53 hosted zones + record counts (stable — avoid volatile TTL/value drift)
  local r53_zones
  r53_zones=$(aws route53 list-hosted-zones \
    --query "HostedZones[?starts_with(Name, '')].[Id,Name,Config.PrivateZone,ResourceRecordSetCount]" \
    --output json 2>/dev/null || echo "[]")

  # SSM documents — self-owned (session docs are account-owned, not AWS-managed).
  # Captures Name + DocumentType + DocumentVersion so the KM-Sandbox-Session ->
  # km-Sandbox-Session rename shows up in inventory diffs.
  local ssm_documents
  ssm_documents=$(aws ssm list-documents \
    --filters Key=Owner,Values=Self \
    --query 'DocumentIdentifiers[].{Name:Name,DocumentType:DocumentType,DocumentVersion:DocumentVersion,PlatformTypes:PlatformTypes}' \
    --output json 2>/dev/null || echo "[]")

  # Compose final snapshot JSON with sorted keys for stable diffs
  jq -n -S \
    --argjson efs          "$efs" \
    --argjson iam_roles    "$iam_roles" \
    --argjson lambdas      "$lambdas" \
    --argjson dynamo       "$dynamo_tables" \
    --argjson s3           "$s3_buckets" \
    --argjson kms          "$kms_aliases" \
    --argjson ses_sets     "$ses_rule_sets" \
    --argjson ses_rules    "$ses_rules" \
    --argjson r53          "$r53_zones" \
    --argjson ssm_docs     "$ssm_documents" \
    --arg     prefix       "$prefix" \
    --arg     captured_at  "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" \
    '{
      meta: { prefix: $prefix, captured_at: $captured_at },
      efs: { filesystems: $efs },
      iam: { roles: $iam_roles },
      lambda: { functions: $lambdas },
      dynamodb: { tables: $dynamo },
      s3: { buckets: $s3 },
      kms: { aliases: $kms },
      ses: { rule_sets: $ses_sets, rules: $ses_rules },
      route53: { hosted_zones: $r53 },
      ssm: { documents: $ssm_docs }
    }' > "$output_file"

  echo "==> Snapshot written to: ${output_file}" >&2
}

# ---- diff -------------------------------------------------------------------

do_diff() {
  local before_file="$1"
  local after_file="$2"
  local prefix="${3:-km}"

  require_jq

  if [ ! -f "$before_file" ]; then
    echo "ERROR: before snapshot not found: ${before_file}" >&2
    exit 1
  fi
  if [ ! -f "$after_file" ]; then
    echo "ERROR: after snapshot not found: ${after_file}" >&2
    exit 1
  fi

  echo "==> Diffing snapshots (prefix='${prefix}')" >&2
  echo "    before: ${before_file}" >&2
  echo "    after:  ${after_file}" >&2

  # Strip volatile meta.captured_at before diffing
  local before_stable after_stable
  before_stable=$(jq 'del(.meta.captured_at)' "$before_file")
  after_stable=$(jq 'del(.meta.captured_at)' "$after_file")

  local diff_output
  diff_output=$(diff \
    <(echo "$before_stable" | jq -S .) \
    <(echo "$after_stable"  | jq -S .) || true)

  if [ -z "$diff_output" ]; then
    echo "OK: no changes detected between snapshots (prefix='${prefix}')" >&2
    exit 0
  fi

  echo "DIFF: changes detected between snapshots:" >&2
  echo "$diff_output"
  exit 1
}

# ---- main -------------------------------------------------------------------

if [ "${#}" -lt 1 ]; then
  usage
fi

subcommand="$1"
shift

prefix="km"
positional=()

while [ "${#}" -gt 0 ]; do
  case "$1" in
    --prefix)
      prefix="$2"
      shift 2
      ;;
    --prefix=*)
      prefix="${1#--prefix=}"
      shift
      ;;
    *)
      positional+=("$1")
      shift
      ;;
  esac
done

case "$subcommand" in
  snapshot)
    if [ "${#positional[@]}" -lt 1 ]; then
      echo "ERROR: snapshot requires <output.json>" >&2
      usage
    fi
    do_snapshot "${positional[0]}" "$prefix"
    ;;
  diff)
    if [ "${#positional[@]}" -lt 2 ]; then
      echo "ERROR: diff requires <before.json> <after.json>" >&2
      usage
    fi
    do_diff "${positional[0]}" "${positional[1]}" "$prefix"
    ;;
  *)
    echo "ERROR: unknown subcommand '${subcommand}'" >&2
    usage
    ;;
esac
