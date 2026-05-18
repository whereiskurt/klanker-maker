# pkg/terragrunt/testdata/synthetic-prefix-whereiskurt.tf
# Synthetic fixture for Phase 84.4 audit tests. Not applied by terragrunt.
# 11-char prefix "whereiskurt" — upper-boundary case for SCP 5KB size test in Plan 02.

variable "resource_prefix" {
  type    = string
  default = "whereiskurt"
}

resource "aws_iam_role" "synthetic" {
  name = "${var.resource_prefix}-synthetic-role"
}

resource "aws_efs_file_system" "synthetic" {
  creation_token = "${var.resource_prefix}-shared-use1"
}
