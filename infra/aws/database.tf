resource "aws_security_group" "database" {
  name        = "${var.name}-database"
  description = "Private PostgreSQL; only EKS worker workloads may connect"
  vpc_id      = module.vpc.vpc_id
}

resource "aws_vpc_security_group_ingress_rule" "database" {
  security_group_id            = aws_security_group.database.id
  referenced_security_group_id = module.eks.node_security_group_id
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
}

resource "aws_db_parameter_group" "database" {
  name_prefix = "${var.name}-"
  family      = "postgres${var.postgres_version}"
  parameter {
    name         = "rds.force_ssl"
    value        = "1"
    apply_method = "pending-reboot"
  }
  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_db_instance" "database" {
  identifier                   = var.name
  engine                       = "postgres"
  engine_version               = var.postgres_version
  instance_class               = var.postgres_instance_class
  allocated_storage            = 20
  storage_type                 = "gp3"
  storage_encrypted            = true
  db_name                      = "postgres"
  username                     = "foc_admin"
  manage_master_user_password  = true
  db_subnet_group_name         = module.vpc.database_subnet_group_name
  vpc_security_group_ids       = [aws_security_group.database.id]
  parameter_group_name         = aws_db_parameter_group.database.name
  publicly_accessible          = false
  multi_az                     = false
  backup_retention_period      = 7
  copy_tags_to_snapshot        = true
  auto_minor_version_upgrade   = true
  deletion_protection          = true
  skip_final_snapshot          = false
  final_snapshot_identifier    = "${var.name}-final"
  delete_automated_backups     = true
  performance_insights_enabled = false
}
