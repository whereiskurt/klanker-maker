# Optional lean network for this account's boxes (var.provision_network). This
# account's subnets are always public by design — there is no cross-account
# NAT-gateway or EIP concept here the way the home account has for its private
# subnets — so a single route table with a default route straight to the
# internet gateway, associated with every subnet, is sufficient. See
# 126-RESEARCH.md § C5.
#
# ONE PUBLIC SUBNET PER AVAILABILITY ZONE (not one shared subnet) so the
# Phase 124 AZ-failover sweep retains more than one attempt when this account
# reports capacity exhaustion for a GPU family. Re-provisioning this later
# would require another privileged trip through this account's admin
# credentials, so it is provisioned up front.
#
# Every resource below is count-driven off var.provision_network / the
# availability-zone list and is fully absent when the toggle is false.

data "aws_availability_zones" "available" {
  count = var.provision_network ? 1 : 0
  state = "available"
}

locals {
  network_azs = var.provision_network ? slice(
    data.aws_availability_zones.available[0].names,
    0,
    min(var.az_count, length(data.aws_availability_zones.available[0].names)),
  ) : []
}

resource "aws_vpc" "launcher" {
  count = var.provision_network ? 1 : 0

  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Name                 = "${var.resource_prefix}-gpu-launcher-vpc"
    "km:resource-prefix" = var.resource_prefix
  }
}

resource "aws_internet_gateway" "launcher" {
  count = var.provision_network ? 1 : 0

  vpc_id = aws_vpc.launcher[0].id

  tags = {
    Name                 = "${var.resource_prefix}-gpu-launcher-igw"
    "km:resource-prefix" = var.resource_prefix
  }
}

resource "aws_subnet" "launcher" {
  count = var.provision_network ? length(local.network_azs) : 0

  vpc_id                  = aws_vpc.launcher[0].id
  cidr_block              = cidrsubnet(var.vpc_cidr, 8, count.index)
  availability_zone       = local.network_azs[count.index]
  map_public_ip_on_launch = true

  tags = {
    Name                 = "${var.resource_prefix}-gpu-launcher-${local.network_azs[count.index]}"
    "km:resource-prefix" = var.resource_prefix
  }
}

# Single shared route table — every provisioned subnet is public, so all of
# them get the identical default route to the internet gateway.
resource "aws_route_table" "launcher" {
  count = var.provision_network ? 1 : 0

  vpc_id = aws_vpc.launcher[0].id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.launcher[0].id
  }

  tags = {
    Name                 = "${var.resource_prefix}-gpu-launcher-rt"
    "km:resource-prefix" = var.resource_prefix
  }
}

resource "aws_route_table_association" "launcher" {
  count = var.provision_network ? length(aws_subnet.launcher) : 0

  subnet_id      = aws_subnet.launcher[count.index].id
  route_table_id = aws_route_table.launcher[0].id
}

resource "aws_security_group" "launcher" {
  count = var.provision_network ? 1 : 0

  name        = "${var.resource_prefix}-gpu-launcher-sg"
  description = "km GPU launcher account box security group"
  vpc_id      = aws_vpc.launcher[0].id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Allow all outbound (SSM / HTTPS egress to the home account artifacts bucket)"
  }

  tags = {
    Name                 = "${var.resource_prefix}-gpu-launcher-sg"
    "km:resource-prefix" = var.resource_prefix
  }
}

# Fetches the availability zone of an existing, operator-supplied subnet when
# provision_network = false, so subnet_ids/availability_zones stay index-
# parallel regardless of which branch produced them.
data "aws_subnet" "existing" {
  count = var.provision_network ? 0 : 1
  id    = var.subnet_id
}

# ------------------------------------------------------------------------------
# Optional B-local EFS (var.provision_efs) — a live, B-internal shared POSIX
# filesystem for the box fleet, e.g. a shared weights cache so a multi-box GPU
# fleet downloads model weights once and mounts them everywhere. Complements
# the results bucket in main.tf; it does not replace it. Reachable only from
# inside this account's own VPC and mounted only by this account's boxes — it
# NEVER crosses the account boundary, so it has no bearing on the
# no-cross-account-write invariant (account A still reaches results only
# through the results bucket).
# ------------------------------------------------------------------------------

resource "aws_efs_file_system" "launcher" {
  count = var.provision_efs ? 1 : 0

  creation_token   = "${var.resource_prefix}-gpu-launcher-efs"
  encrypted        = true
  performance_mode = "generalPurpose"
  throughput_mode  = "elastic"

  tags = {
    Name                 = "${var.resource_prefix}-gpu-launcher-efs"
    "km:resource-prefix" = var.resource_prefix
  }
}

resource "aws_security_group_rule" "efs_nfs_ingress" {
  count = var.provision_efs ? 1 : 0

  type              = "ingress"
  from_port         = 2049
  to_port           = 2049
  protocol          = "tcp"
  security_group_id = local.security_group_id
  self              = true
  description       = "NFS from other boxes sharing this security group"
}

resource "aws_efs_mount_target" "launcher" {
  count = var.provision_efs ? length(local.subnet_ids) : 0

  file_system_id  = aws_efs_file_system.launcher[0].id
  subnet_id       = local.subnet_ids[count.index]
  security_groups = [local.security_group_id]
}
