# Wiz Outpost cloud-connector roles, deployed from Wiz's own CloudFormation
# template (vendored beside this file). Providers come from infra/live/root.hcl.
locals {
  template = jsondecode(file("${path.module}/wiz-outpost-connector.json"))
}

resource "aws_cloudformation_stack" "this" {
  name          = var.stack_name
  template_body = jsonencode(local.template)
  # WizVersionLastUpdated is set explicitly so the deployed version is pinned to the
  # vendored template and outputs.tf can read it back.
  parameters = merge(var.parameters, {
    WizVersionLastUpdated = local.template.Parameters.WizVersionLastUpdated.Default
  })
  capabilities = ["CAPABILITY_NAMED_IAM"]

  tags = {
    "km:managed"   = "true"
    "km:component" = "wiz-outpost-connector"
  }
}
