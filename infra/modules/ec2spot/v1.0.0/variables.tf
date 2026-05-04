variable "km_label" {
  type        = string
  description = "Klanker Maker site label (e.g. 'km')"
}

variable "km_random_suffix" {
  type        = string
  description = "Random suffix for globally-unique IAM resource names"
  default     = ""
}

variable "region_label" {
  type        = string
  description = "Short AWS region label (e.g. 'use1', 'usw2')"
}

variable "region_full" {
  type        = string
  description = "Full AWS region name (e.g. 'us-east-1')"
}

variable "sandbox_id" {
  type        = string
  description = "Sandbox identifier for tagging all resources"
}

variable "ec2spots" {
  type = list(object({
    count                  = number
    region                 = string
    sandbox_id             = string
    instance_type          = string
    spot_price_multiplier  = optional(number, 1.00)
    spot_price_offset      = optional(number, 0.0005)
    block_duration_minutes = optional(number, 0)
    user_data              = optional(string, "")
    user_data_base64       = optional(string, "")
    use_spot               = optional(bool, true)
  }))
  description = "List of EC2 spot instance configurations per region"
  default     = []
}

variable "vpc_id" {
  type        = string
  description = "VPC ID where EC2 spot instances will be created. If empty, a per-sandbox VPC is created."
  default     = ""
}

variable "public_subnets" {
  type        = list(string)
  description = "List of public subnet IDs. If empty, subnets are created in the per-sandbox VPC."
  default     = []
}

variable "availability_zones" {
  type        = list(string)
  description = "List of availability zones. If empty, first two AZs in the region are used."
  default     = []
}

variable "sg_egress_rules" {
  type = list(object({
    from_port   = number
    to_port     = number
    protocol    = string
    cidr_blocks = list(string)
    description = string
  }))
  description = "Security group egress rules compiled from profile. Empty list = no egress allowed."
  default     = []
}

variable "iam_session_policy" {
  type = object({
    max_session_duration = optional(number, 3600)
    allowed_regions      = optional(list(string), [])
  })
  description = "IAM session policy constraints compiled from profile."
  default     = {}
}

variable "enable_bedrock" {
  type        = bool
  description = "Whether to attach Bedrock IAM policy to the sandbox role. Set to false with --no-bedrock."
  default     = true
}

variable "root_volume_size_gb" {
  type        = number
  description = "Root EBS volume size in GB. 0 uses AMI default."
  default     = 0
}

variable "hibernation_enabled" {
  type        = bool
  description = "Enable EC2 hibernation (on-demand only, requires encrypted root volume)"
  default     = false
}

variable "ami_slug" {
  type        = string
  description = "AMI slug for lookup: amazon-linux-2023, ubuntu-24.04, ubuntu-22.04"
  default     = "amazon-linux-2023"
}

variable "ami_id" {
  type        = string
  description = "Raw EC2 AMI ID (ami-xxxxxxxx) — Phase 33.1. When non-empty, ami_slug is ignored and no data.aws_ami lookup is performed. Region-scoped: must exist in the target AWS region (operator/Phase 56 owns region alignment)."
  default     = ""
}

variable "additional_volume_size_gb" {
  type        = number
  description = "Additional EBS data volume size in GB. 0 means no additional volume."
  default     = 0
}

variable "additional_volume_encrypted" {
  type        = bool
  description = "Encrypt the additional EBS volume"
  default     = false
}

variable "additional_volume_device_name" {
  type        = string
  description = "Device name for the additional EBS volume attachment. Defaults to /dev/sdf; the compiler picks the first non-colliding name from /dev/sd[f-p] when the source AMI's BDMs already include /dev/sdf (Phase 56.1 BDM collision fix)."
  default     = "/dev/sdf"
}

variable "resource_prefix" {
  type        = string
  description = "Phase 66 multi-instance resource prefix (e.g. 'km', 'stg', 'kpf'). Applied to SQS queue names and IAM policy names scoped to this sandbox instance. Default 'km' matches the platform default."
  default     = "km"
}

# ============================================================
# Phase 68 — Slack transcript streaming (per-turn chat + gzipped JSONL upload)
# ============================================================

variable "artifacts_bucket" {
  type        = string
  description = "Name of the project-wide S3 artifacts bucket (KM_ARTIFACTS_BUCKET). Used to scope per-sandbox PutObject policies for transcripts and other sidecar uploads. When empty, the transcript S3 IAM policy is skipped."
  default     = ""
}

variable "slack_stream_messages_table_name" {
  type        = string
  description = "DynamoDB table name for Slack transcript stream-messages mapping. When non-empty, the sandbox EC2 role gains dynamodb:PutItem on this table (Phase 68)."
  default     = ""
}
