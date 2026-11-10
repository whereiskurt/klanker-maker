# ============================================================
# SCP: Org-level sandbox containment backstop (v3.0.0)
#
# v3.0.0 deploys a policy document it does NOT author. The document is
# rendered by pkg/scp from km-config.yaml (resource_prefix,
# accounts.application, region, and the optional scp: block) and arrives as
# var.policy_json through KM_SCP_POLICY_JSON.
#
# Why: v2.0.0 wrote the policy as an aws_iam_policy_document while the
# `km bootstrap --scp` preview wrote it again in Go, and the two drifted (the
# preview denied ec2:DeleteSnapshot, ec2:DeregisterImage and ec2:CreateTags; the
# deployed policy did not). Install-specific exemptions also had nowhere to live
# but a hand edit in the console, which the next apply silently reverted. One
# renderer makes the preview, the deployed policy and the `km doctor` content
# check the same bytes.
#
# Resource addresses are unchanged from v2.0.0, so moving the pin is an
# in-place update of the policy content, never a replace.
# ============================================================

resource "aws_organizations_policy" "sandbox_containment" {
  name        = "${var.resource_prefix}-sandbox-containment"
  description = "Org-level backstop preventing sandbox role EC2/network/IAM breakout"
  type        = "SERVICE_CONTROL_POLICY"
  content     = var.policy_json

  tags = {
    "km:component"       = "scp"
    "km:managed"         = "true"
    "km:resource-prefix" = var.resource_prefix
  }
}

resource "aws_organizations_policy_attachment" "sandbox_containment" {
  policy_id = aws_organizations_policy.sandbox_containment.id
  target_id = var.application_account_id
}

# 5,000-byte safety threshold (AWS hard limit 5,120). pkg/scp.Render enforces the
# same bound; this precondition catches a hand-set KM_SCP_POLICY_JSON.
resource "terraform_data" "scp_size_guard" {
  lifecycle {
    precondition {
      condition     = length(var.policy_json) <= 5000
      error_message = "SCP policy is ${length(var.policy_json)} bytes — exceeds the 5,000-byte safety threshold (AWS hard limit is 5,120). Trim the scp: trusted role lists in km-config.yaml."
    }
  }
}
