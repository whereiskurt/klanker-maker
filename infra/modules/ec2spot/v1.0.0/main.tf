## Per-sandbox VPC (created when vpc_id is empty)
data "aws_availability_zones" "available" {
  count = var.vpc_id == "" ? 1 : 0
  state = "available"
}

locals {
  # Use provided or auto-discovered AZs
  effective_azs     = length(var.availability_zones) > 0 ? var.availability_zones : (var.vpc_id == "" ? slice(data.aws_availability_zones.available[0].names, 0, 2) : [])
  create_vpc        = var.vpc_id == ""
}

resource "aws_vpc" "sandbox" {
  count      = local.create_vpc ? 1 : 0
  cidr_block = "10.0.0.0/16"

  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = {
    Name            = "km-sandbox-${var.sandbox_id}"
    "km:sandbox-id" = var.sandbox_id
  }
}

resource "aws_internet_gateway" "sandbox" {
  count  = local.create_vpc ? 1 : 0
  vpc_id = aws_vpc.sandbox[0].id

  tags = {
    Name            = "km-sandbox-${var.sandbox_id}-igw"
    "km:sandbox-id" = var.sandbox_id
  }
}

resource "aws_subnet" "sandbox" {
  count             = local.create_vpc ? length(local.effective_azs) : 0
  vpc_id            = aws_vpc.sandbox[0].id
  cidr_block        = cidrsubnet("10.0.0.0/16", 8, count.index)
  availability_zone = local.effective_azs[count.index]

  map_public_ip_on_launch = true

  tags = {
    Name            = "km-sandbox-${var.sandbox_id}-${local.effective_azs[count.index]}"
    "km:sandbox-id" = var.sandbox_id
  }
}

resource "aws_route_table" "sandbox" {
  count  = local.create_vpc ? 1 : 0
  vpc_id = aws_vpc.sandbox[0].id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.sandbox[0].id
  }

  tags = {
    Name            = "km-sandbox-${var.sandbox_id}-rt"
    "km:sandbox-id" = var.sandbox_id
  }
}

resource "aws_route_table_association" "sandbox" {
  count          = local.create_vpc ? length(local.effective_azs) : 0
  subnet_id      = aws_subnet.sandbox[count.index].id
  route_table_id = aws_route_table.sandbox[0].id
}

locals {
  # Resolve effective VPC, subnets, AZs — either provided or auto-created
  effective_vpc_id  = local.create_vpc ? aws_vpc.sandbox[0].id : var.vpc_id
  effective_subnets = length(var.public_subnets) > 0 ? var.public_subnets : aws_subnet.sandbox[*].id

  # Filter EC2 spot instances for the current region
  region_ec2spots = [
    for ec2spot in var.ec2spots :
    ec2spot if ec2spot.region == var.region_full
  ]

  # Calculate total number of EC2 spot instances in this region
  total_ec2spot_count = length(local.region_ec2spots) > 0 ? sum([for b in local.region_ec2spots : b.count]) : 0

  # Create a flattened list of EC2 spot instances
  ec2spot_instances = flatten([
    for idx, ec2spot in local.region_ec2spots : [
      for instance_idx in range(ec2spot.count) : {
        key                    = "${ec2spot.region}-${idx}-${instance_idx}"
        region                 = ec2spot.region
        instance_type          = ec2spot.instance_type
        spot_price_multiplier  = ec2spot.spot_price_multiplier
        spot_price_offset      = ec2spot.spot_price_offset
        block_duration_minutes = ec2spot.block_duration_minutes
        user_data              = ec2spot.user_data
        availability_zone      = local.effective_azs[instance_idx % length(local.effective_azs)]
        subnet_id              = local.effective_subnets[instance_idx % length(local.effective_subnets)]
        sandbox_id             = ec2spot.sandbox_id
        user_data_base64       = ec2spot.user_data_base64
        use_spot               = ec2spot.use_spot
        instance_name          = "km-sandbox-${ec2spot.sandbox_id}-${instance_idx}"
      }
    ]
  ])

  ec2spot_map = {
    for ec2spot in local.ec2spot_instances :
    ec2spot.key => ec2spot
    if ec2spot.use_spot
  }

  ec2_ondemand_map = {
    for ec2spot in local.ec2spot_instances :
    ec2spot.key => ec2spot
    if !ec2spot.use_spot
  }
}

# Get latest Amazon Linux 2023 ARM64 AMI
data "aws_ami" "base_ami" {
  count = local.total_ec2spot_count > 0 ? 1 : 0

  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "name"
    values = ["al2023-ami-2023.*-x86_64"]
  }

  filter {
    name   = "architecture"
    values = ["x86_64"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

# Get spot price for spot instances only
data "aws_ec2_spot_price" "price" {
  for_each = local.ec2spot_map

  instance_type     = each.value.instance_type
  availability_zone = each.value.availability_zone

  filter {
    name   = "product-description"
    values = ["Linux/UNIX"]
  }
}

# Security group for EC2 spot instances (SSM-only; no SSH ingress)
# Egress left empty — Phase 2 profile compiler configures per-profile egress rules
resource "aws_security_group" "ec2spot" {
  count = local.total_ec2spot_count > 0 ? 1 : 0

  name        = "km-ec2spot-${var.sandbox_id}-${var.region_label}"
  description = "Security group for km sandbox EC2 spot hosts (SSM-only access)"
  vpc_id      = local.effective_vpc_id

  # No SSH ingress — SSM-only access via IAM role
  # No egress rules — Phase 2 profile compiler adds per-profile egress

  tags = {
    Name            = "km-ec2spot-${var.region_label}"
    "km:sandbox-id" = var.sandbox_id
  }
}

# Egress rules compiled from the sandbox profile (NETW-01)
# The profile compiler populates sg_egress_rules via service.hcl module_inputs.
resource "aws_security_group_rule" "ec2spot_egress" {
  count = local.total_ec2spot_count > 0 ? length(var.sg_egress_rules) : 0

  type              = "egress"
  from_port         = var.sg_egress_rules[count.index].from_port
  to_port           = var.sg_egress_rules[count.index].to_port
  protocol          = var.sg_egress_rules[count.index].protocol
  cidr_blocks       = var.sg_egress_rules[count.index].cidr_blocks
  description       = var.sg_egress_rules[count.index].description
  security_group_id = aws_security_group.ec2spot[0].id
}

# IAM role for SSM access (no SSH needed)
resource "aws_iam_role" "ec2spot_ssm" {
  count = local.total_ec2spot_count > 0 ? 1 : 0

  name                 = "km-ec2spot-ssm-${var.sandbox_id}-${var.region_label}"
  max_session_duration = var.iam_session_policy.max_session_duration

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "ec2.amazonaws.com"
        }
      }
    ]
  })

  tags = {
    Name            = "km-ec2spot-ssm-${var.region_label}"
    "km:sandbox-id" = var.sandbox_id
  }
}

# Optional region-lock inline policy (NETW-04): restricts API calls to allowed regions only.
# Only created when iam_session_policy.allowed_regions is non-empty.
resource "aws_iam_role_policy" "ec2spot_region_lock" {
  count = (local.total_ec2spot_count > 0 && length(var.iam_session_policy.allowed_regions) > 0) ? 1 : 0

  name = "km-ec2spot-region-lock-${var.region_label}"
  role = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = "*"
        Resource = "*"
        Condition = {
          StringEquals = {
            "aws:RequestedRegion" = var.iam_session_policy.allowed_regions
          }
        }
      }
    ]
  })
}

resource "aws_iam_role_policy_attachment" "ec2spot_ssm" {
  count = local.total_ec2spot_count > 0 ? 1 : 0

  role       = aws_iam_role.ec2spot_ssm[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

# Policy: EventBridge PutEvents so the audit-log sidecar can publish SandboxIdle events (PROV-06)
# Note: PutEvents does not support resource-level restrictions for the default event bus.
resource "aws_iam_role_policy" "ec2spot_eventbridge" {
  count = local.total_ec2spot_count > 0 ? 1 : 0
  name  = "km-${var.sandbox_id}-eventbridge"
  role  = aws_iam_role.ec2spot_ssm[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["events:PutEvents"]
      Resource = ["*"]
    }]
  })
}

resource "aws_iam_instance_profile" "ec2spot" {
  count = local.total_ec2spot_count > 0 ? 1 : 0

  name = "km-ec2spot-profile-${var.sandbox_id}-${var.region_label}"
  role = aws_iam_role.ec2spot_ssm[0].name

  tags = {
    Name            = "km-ec2spot-profile-${var.region_label}"
    "km:sandbox-id" = var.sandbox_id
  }
}

# Default user data: SSM agent only (no SSH config)
locals {
  default_user_data = <<-EOF
    #!/bin/bash
    yum update -y
    yum install -y amazon-ssm-agent
    systemctl enable amazon-ssm-agent
    systemctl start amazon-ssm-agent
  EOF
}

# Spot instance requests
resource "aws_spot_instance_request" "ec2spot" {
  for_each = local.ec2spot_map

  ami                    = data.aws_ami.base_ami[0].image_id
  instance_type          = each.value.instance_type
  spot_price             = format("%.6f", (data.aws_ec2_spot_price.price[each.key].spot_price * each.value.spot_price_multiplier) + each.value.spot_price_offset)
  user_data_base64       = each.value.user_data_base64 != "" ? each.value.user_data_base64 : base64encode(local.default_user_data)
  user_data              = null  # use user_data_base64 instead
  subnet_id              = each.value.subnet_id
  availability_zone      = each.value.availability_zone
  vpc_security_group_ids = [aws_security_group.ec2spot[0].id]
  iam_instance_profile   = aws_iam_instance_profile.ec2spot[0].name

  # IMDSv2 enforcement — http_tokens = required means only v2 token-based requests allowed
  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
    http_endpoint               = "enabled"
  }

  associate_public_ip_address = true
  wait_for_fulfillment        = true

  tags = {
    Name            = each.value.instance_name
    "km:sandbox-id" = each.value.sandbox_id
  }

  lifecycle {
    ignore_changes = [
      vpc_security_group_ids,
      spot_price
    ]
  }
}

# Tag the actual EC2 instances (spot requests don't propagate tags)
resource "aws_ec2_tag" "ec2spot_name" {
  for_each = local.ec2spot_map

  resource_id = aws_spot_instance_request.ec2spot[each.key].spot_instance_id
  key         = "Name"
  value       = each.value.instance_name
}

resource "aws_ec2_tag" "ec2spot_km_label" {
  for_each = local.ec2spot_map

  resource_id = aws_spot_instance_request.ec2spot[each.key].spot_instance_id
  key         = "km:label"
  value       = var.km_label
}

resource "aws_ec2_tag" "ec2spot_sandbox_id" {
  for_each = local.ec2spot_map

  resource_id = aws_spot_instance_request.ec2spot[each.key].spot_instance_id
  key         = "km:sandbox-id"
  value       = each.value.sandbox_id
}

resource "aws_ec2_tag" "ec2spot_region" {
  for_each = local.ec2spot_map

  resource_id = aws_spot_instance_request.ec2spot[each.key].spot_instance_id
  key         = "Region"
  value       = var.region_label
}

# ============================================================
# On-demand instances (when use_spot = false / --on-demand flag)
# ============================================================

resource "aws_instance" "ec2_ondemand" {
  for_each = local.ec2_ondemand_map

  ami                    = data.aws_ami.base_ami[0].image_id
  instance_type          = each.value.instance_type
  user_data_base64       = each.value.user_data_base64 != "" ? each.value.user_data_base64 : base64encode(local.default_user_data)
  subnet_id              = each.value.subnet_id
  availability_zone      = each.value.availability_zone
  vpc_security_group_ids = [aws_security_group.ec2spot[0].id]
  iam_instance_profile   = aws_iam_instance_profile.ec2spot[0].name

  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
    http_endpoint               = "enabled"
  }

  associate_public_ip_address = true

  tags = {
    Name            = each.value.instance_name
    "km:sandbox-id" = each.value.sandbox_id
    "km:label"      = var.km_label
    "Region"        = var.region_label
  }
}
