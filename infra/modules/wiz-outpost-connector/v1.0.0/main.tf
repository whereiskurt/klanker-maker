# Wiz Outpost cloud-connector roles, deployed from Wiz's own CloudFormation
# template (vendored beside this file). Providers come from infra/live/root.hcl.
resource "aws_cloudformation_stack" "this" {
  name          = var.stack_name
  template_body = jsonencode(jsondecode(file("${path.module}/wiz-outpost-connector.json")))
  parameters    = var.parameters
  capabilities  = ["CAPABILITY_NAMED_IAM"]

  tags = {
    "km:managed"   = "true"
    "km:component" = "wiz-outpost-connector"
  }
}
