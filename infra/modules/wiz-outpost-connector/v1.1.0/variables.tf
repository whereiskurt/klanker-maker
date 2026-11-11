variable "stack_name" {
  description = "CloudFormation stack name ({resource_prefix}-wiz-outpost-connector)"
  type        = string
}

variable "parameters" {
  description = "CloudFormation parameters for the Wiz Outpost connector template (from km-config.yaml wiz.outpost via pkg/wizoutpost)"
  type        = map(string)
}
