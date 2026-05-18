# pkg/terragrunt/testdata/synthetic-prefix-kph.tf
# Synthetic fixture for Phase 84.4 audit tests. Not applied by terragrunt.
# 3-char prefix "kph" — lower-bound case for SCP-size unit test in Plan 02.

variable "resource_prefix" {
  type    = string
  default = "kph"
}

resource "aws_iam_role" "synthetic" {
  name = "${var.resource_prefix}-synthetic-role"
}

resource "aws_efs_file_system" "synthetic" {
  creation_token = "${var.resource_prefix}-shared-use1"
}
