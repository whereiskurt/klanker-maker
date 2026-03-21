# VPC Outputs
output "vpc_id" {
  description = "ID of the VPC"
  value       = aws_vpc.vpc.id
}

output "vpc_cidr_block" {
  description = "CIDR block of the VPC"
  value       = aws_vpc.vpc.cidr_block
}

output "availability_zones" {
  description = "List of availability zones used by the VPC"
  value       = local.availability_zones
}

# Subnet Outputs
output "public_subnets" {
  description = "List of IDs of public subnets"
  value       = aws_subnet.public_subnet[*].id
}

output "private_subnets" {
  description = "List of IDs of private subnets"
  value       = aws_subnet.private_subnet[*].id
}

output "public_subnet_ids" {
  description = "Alias for public_subnets"
  value       = aws_subnet.public_subnet[*].id
}

output "private_subnet_ids" {
  description = "Alias for private_subnets"
  value       = aws_subnet.private_subnet[*].id
}

# Route Table Outputs
output "public_route_table_id" {
  description = "ID of the public route table"
  value       = aws_route_table.public.id
}

output "private_route_table_id" {
  description = "ID of the private route table"
  value       = aws_route_table.private.id
}

# Internet Gateway Output
output "internet_gateway_id" {
  description = "ID of the Internet Gateway"
  value       = aws_internet_gateway.ig.id
}

# NAT Gateway Outputs
output "nat_gateway_id" {
  description = "ID of the NAT Gateway (if enabled)"
  value       = var.nat_gateway.enabled ? aws_nat_gateway.nat[0].id : null
}

output "nat_eip_public_ip" {
  description = "Public IP of the NAT Gateway EIP (if enabled)"
  value       = var.nat_gateway.enabled ? aws_eip.nat[0].public_ip : null
}

# Security Group Outputs
output "security_group_ids" {
  description = "Map of security group IDs by name"
  value = {
    sandbox_mgmt     = aws_security_group.sandbox_mgmt.id
    sandbox_internal = aws_security_group.sandbox_internal.id
  }
}

output "sandbox_mgmt_sg_id" {
  description = "Security group ID for sandbox management"
  value       = aws_security_group.sandbox_mgmt.id
}

output "sandbox_internal_sg_id" {
  description = "Security group ID for sandbox internal communication"
  value       = aws_security_group.sandbox_internal.id
}
