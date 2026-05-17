#!/usr/bin/env bash
# Phase 84.1 UAT — GAP-6 verification (in-place upgrade safety)
#
# Runs `terragrunt plan` against infra/live/use1/ses with env vars sourced from
# km-config.yaml, then greps for destroys of shared SES resources. Zero destroys
# means regional v2.0.0's removed{} blocks are correctly suppressing the
# cutover destroy that nuked the domain identity / DKIM / MX / verification TXT
# in Phase 84's UAT (84-10-UAT.md lines 53-72).
#
# Usage:
#   bash .planning/phases/84.1-ses-upgrade-safety-gap-closure/uat-gap6-check.sh [AWS_PROFILE]
#
# AWS_PROFILE defaults to klanker-terraform (override as first arg or via env).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
cd "$REPO_ROOT"

CONFIG="$REPO_ROOT/km-config.yaml"
LOG="/tmp/terragrunt-plan-ses-84.1.log"

if [[ ! -f "$CONFIG" ]]; then
  echo "ERROR: $CONFIG not found" >&2
  exit 2
fi

# 1. Parse km-config.yaml for terragrunt env vars
eval "$(awk '
  /^resource_prefix:/  { print "export KM_RESOURCE_PREFIX="$2 }
  /^domain:/           { print "export KM_DOMAIN="$2 }
  /^email_subdomain:/  { print "export KM_EMAIL_SUBDOMAIN="$2 }
  /^route53_zone_id:/  { print "export KM_ROUTE53_ZONE_ID="$2 }
  /^artifacts_bucket:/ { print "export KM_ARTIFACTS_BUCKET="$2 }
' "$CONFIG" | tr -d '"')"

export KM_REGION="${KM_REGION:-us-east-1}"
export KM_REGION_LABEL="${KM_REGION_LABEL:-use1}"
export AWS_PROFILE="${1:-${AWS_PROFILE:-klanker-terraform}}"

# 2. Verify all required env vars are set
missing=()
for v in KM_RESOURCE_PREFIX KM_DOMAIN KM_EMAIL_SUBDOMAIN KM_ROUTE53_ZONE_ID KM_ARTIFACTS_BUCKET KM_REGION KM_REGION_LABEL AWS_PROFILE; do
  if [[ -z "${!v:-}" ]]; then
    missing+=("$v")
  fi
done
if [[ ${#missing[@]} -gt 0 ]]; then
  echo "ERROR: missing env vars: ${missing[*]}" >&2
  echo "Check $CONFIG has the matching top-level keys (resource_prefix, domain, email_subdomain, route53_zone_id, artifacts_bucket)." >&2
  exit 2
fi

echo "── Phase 84.1 GAP-6 verification ──"
echo "Env:"
env | grep -E '^(KM_|AWS_PROFILE)=' | sort | sed 's/^/  /'
echo ""

# 3. Run terragrunt plan against the regional ses module
cd "$REPO_ROOT/infra/live/use1/ses"

echo "── Running: terragrunt plan (infra/live/use1/ses) ──"
if ! terragrunt plan 2>&1 | tee "$LOG"; then
  echo ""
  echo "ERROR: terragrunt plan failed. See $LOG for details." >&2
  exit 3
fi

cd "$REPO_ROOT"

# 4. Grep for destroys of shared SES resources
echo ""
echo "── GAP-6 assertion: zero destroys of shared SES resources ──"

DESTROY_HITS=$(grep -E "will be destroyed|destroy.*aws_ses_(domain|receipt_rule_set|active_receipt_rule_set)|destroy.*aws_route53_record\.(dkim|mx|ses_verification)" "$LOG" || true)

if [[ -z "$DESTROY_HITS" ]]; then
  echo "PASS: zero destroys of shared SES resources detected."
  echo "GAP-6 mitigation holds — regional v2.0.0 removed{} blocks correctly suppress the cutover destroy."
  exit 0
else
  echo "FAIL: shared SES resources scheduled for destruction:"
  echo "$DESTROY_HITS" | sed 's/^/  /'
  echo ""
  echo "GAP-6 regression — file a Phase 84.2 gap entry. Full plan in $LOG"
  exit 1
fi
