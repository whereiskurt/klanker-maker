resource "aws_efs_file_system" "shared" {
  creation_token   = "km-shared-${var.region_label}"
  performance_mode = "generalPurpose"
  throughput_mode  = "elastic"
  encrypted        = true

  tags = {
    Name           = "km-shared-efs-${var.region_label}"
    "km:label"     = var.km_label
    "km:purpose"   = "shared-sandbox-filesystem"
    "km:region"    = var.region_label
  }
}

resource "aws_security_group" "efs" {
  name        = "km-efs-${var.region_label}"
  description = "NFS ingress for km EFS shared filesystem"
  vpc_id      = var.vpc_id

  ingress {
    from_port       = 2049
    to_port         = 2049
    protocol        = "tcp"
    security_groups = [var.sandbox_sg_id]
    description     = "NFS from sandbox instances"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Allow all outbound"
  }

  tags = {
    Name         = "km-efs-${var.region_label}"
    "km:label"   = var.km_label
    "km:purpose" = "efs-nfs-ingress"
    "km:region"  = var.region_label
  }
}

resource "aws_efs_mount_target" "shared" {
  count           = length(var.subnet_ids)
  file_system_id  = aws_efs_file_system.shared.id
  subnet_id       = var.subnet_ids[count.index]
  security_groups = [aws_security_group.efs.id]
}
