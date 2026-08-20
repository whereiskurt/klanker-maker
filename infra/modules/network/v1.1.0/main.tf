# Fetch available availability zones in the region
data "aws_availability_zones" "available" {
  state = "available"
}

# Calculate the availability zones to use based on the count
locals {
  availability_zones = slice(
    data.aws_availability_zones.available.names,
    0,
    var.vpc.availability_zone_count
  )
}

# VPC
resource "aws_vpc" "vpc" {
  cidr_block           = var.vpc.cidr_block
  enable_dns_hostnames = var.vpc.enable_dns_hostnames
  enable_dns_support   = var.vpc.enable_dns_support

  tags = merge(
    var.vpc.tags,
    {
      Name            = "${var.km_label}-${var.region_label}-vpc"
      "km:sandbox-id" = var.sandbox_id
    }
  )
}

# Public Subnets
resource "aws_subnet" "public_subnet" {
  vpc_id                  = aws_vpc.vpc.id
  count                   = length(var.vpc.public_subnets_cidr)
  cidr_block              = element(var.vpc.public_subnets_cidr, count.index)
  availability_zone       = element(local.availability_zones, count.index)
  map_public_ip_on_launch = true

  tags = merge(
    var.vpc.tags,
    {
      Name            = "${var.km_label}-${var.region_label}-public-${element(local.availability_zones, count.index)}"
      "km:sandbox-id" = var.sandbox_id
    }
  )
}

# Private Subnets
resource "aws_subnet" "private_subnet" {
  vpc_id                  = aws_vpc.vpc.id
  count                   = length(var.vpc.private_subnets_cidr)
  cidr_block              = element(var.vpc.private_subnets_cidr, count.index)
  availability_zone       = element(local.availability_zones, count.index)
  map_public_ip_on_launch = false

  tags = merge(
    var.vpc.tags,
    {
      Name            = "${var.km_label}-${var.region_label}-private-${element(local.availability_zones, count.index)}"
      "km:sandbox-id" = var.sandbox_id
    }
  )
}

# Route Tables
# Per-AZ private route table (Phase 125): one route table per private subnet so
# each AZ can carry its own 0.0.0.0/0 NAT target. A single shared route table
# cannot hold four different default routes to four different NAT gateways.
resource "aws_route_table" "private" {
  count  = length(var.vpc.private_subnets_cidr)
  vpc_id = aws_vpc.vpc.id

  tags = merge(
    var.vpc.tags,
    {
      Name            = "${var.km_label}-${var.region_label}-private-rt-${count.index}"
      "km:sandbox-id" = var.sandbox_id
    }
  )
}

moved {
  from = aws_route_table.private
  to   = aws_route_table.private[0]
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.vpc.id

  tags = merge(
    var.vpc.tags,
    {
      Name            = "${var.km_label}-${var.region_label}-public-rt"
      "km:sandbox-id" = var.sandbox_id
    }
  )
}

# Route Table Associations
resource "aws_route_table_association" "public" {
  count          = length(var.vpc.public_subnets_cidr)
  subnet_id      = element(aws_subnet.public_subnet[*].id, count.index)
  route_table_id = aws_route_table.public.id
}

resource "aws_route_table_association" "private" {
  count          = length(var.vpc.private_subnets_cidr)
  subnet_id      = element(aws_subnet.private_subnet[*].id, count.index)
  route_table_id = aws_route_table.private[count.index].id
}

# Internet Gateway
resource "aws_internet_gateway" "ig" {
  vpc_id = aws_vpc.vpc.id

  tags = merge(
    var.vpc.tags,
    {
      Name            = "${var.km_label}-${var.region_label}-igw"
      "km:sandbox-id" = var.sandbox_id
    }
  )
}

# Route to Internet Gateway (public subnets)
resource "aws_route" "public_igw" {
  route_table_id         = aws_route_table.public.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.ig.id
}

# NAT Gateway EIP
# Per-AZ NAT (Phase 125): one EIP per private subnet/AZ, not a single shared
# EIP. Phase 124's AZ-failover sweep rotates sandboxes across all AZs; a
# single NAT would add cross-AZ data-transfer charges to every egress byte
# and make one AZ a SPOF for the whole install's internet access.
resource "aws_eip" "nat" {
  count  = var.nat_gateway.enabled ? length(var.vpc.private_subnets_cidr) : 0
  domain = "vpc"

  tags = merge(
    var.vpc.tags,
    {
      Name            = "${var.km_label}-${var.region_label}-nat-eip-${count.index}"
      "km:sandbox-id" = var.sandbox_id
    }
  )
}

moved {
  from = aws_eip.nat
  to   = aws_eip.nat[0]
}

# NAT Gateway
# Each NAT sits in the public subnet of the SAME AZ as the private subnet it
# serves (subnet_id indexed by count.index, not fixed to index 0).
resource "aws_nat_gateway" "nat" {
  count         = var.nat_gateway.enabled ? length(var.vpc.private_subnets_cidr) : 0
  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = element(aws_subnet.public_subnet[*].id, count.index)

  tags = merge(
    var.vpc.tags,
    {
      Name            = "${var.km_label}-${var.region_label}-nat-${count.index}"
      "km:sandbox-id" = var.sandbox_id
    }
  )

  depends_on = [aws_internet_gateway.ig]
}

moved {
  from = aws_nat_gateway.nat
  to   = aws_nat_gateway.nat[0]
}

# NAT route in private route table
# Each private route table's default route points at its own AZ's NAT gateway.
resource "aws_route" "private_nat_gateway" {
  count                  = var.nat_gateway.enabled ? length(var.vpc.private_subnets_cidr) : 0
  route_table_id         = aws_route_table.private[count.index].id
  destination_cidr_block = "0.0.0.0/0"
  nat_gateway_id         = aws_nat_gateway.nat[count.index].id
}

# Security Group: Sandbox management (SSM-only; no SSH ingress)
# Egress is intentionally empty here — Phase 2 profile compiler will add
# per-profile egress rules based on allowlists.
resource "aws_security_group" "sandbox_mgmt" {
  name        = "${var.km_label}-${var.region_label}-sandbox-mgmt"
  description = "Sandbox management security group (SSM access, proxy egress)"
  vpc_id      = aws_vpc.vpc.id

  # No SSH ingress — SSM-only access
  # No egress rules — Phase 2 will configure per-profile egress

  tags = merge(
    var.vpc.tags,
    {
      Name            = "${var.km_label}-${var.region_label}-sandbox-mgmt"
      "km:sandbox-id" = var.sandbox_id
    }
  )
}

# Security Group: Sandbox internal (intra-VPC communication between sidecars and main container)
resource "aws_security_group" "sandbox_internal" {
  name        = "${var.km_label}-${var.region_label}-sandbox-internal"
  description = "Sandbox intra-task communication (sidecars and main container)"
  vpc_id      = aws_vpc.vpc.id

  ingress {
    description = "Allow all intra-VPC traffic (internal sidecar communication)"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    self        = true
  }

  tags = merge(
    var.vpc.tags,
    {
      Name            = "${var.km_label}-${var.region_label}-sandbox-internal"
      "km:sandbox-id" = var.sandbox_id
    }
  )
}
