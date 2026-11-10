terraform {
  required_version = ">= 1.13.0, < 2.0.0"
  backend "s3" {}
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 6.59" }
  }
}

variable "account_id" { type = string }
variable "region" { type = string }
variable "expires_on" { type = string }
variable "cluster_name" { type = string }
variable "private_subnet_id" { type = string }
variable "runner_ami" { type = string }

provider "aws" {
  region              = var.region
  allowed_account_ids = [var.account_id]
  default_tags {
    tags = { Project = "foc", ManagedBy = "terraform", ExpiresOn = var.expires_on }
  }
}

data "aws_eks_cluster" "demo" { name = var.cluster_name }
data "aws_vpc" "demo" { id = data.aws_eks_cluster.demo.vpc_config[0].vpc_id }

locals {
  name         = "${var.cluster_name}-ci"
  bucket       = "${var.cluster_name}-${var.account_id}-${var.region}-releases"
  prefix       = "foc-releases"
  parameter    = "/${var.cluster_name}/ci/runner-jit"
  images       = ["user-service", "supplier-service", "order-service", "credit-service", "communication-service", "content-moderation-service", "moderation-service", "frontend", "kafka-init", "migrations"]
  repositories = [for image in local.images : "arn:aws:ecr:${var.region}:${var.account_id}:repository/${var.cluster_name}/${image}"]
}

resource "aws_s3_bucket" "configuration" {
  bucket        = local.bucket
  force_destroy = false
  lifecycle { prevent_destroy = true }
}
resource "aws_s3_bucket_versioning" "configuration" {
  bucket = aws_s3_bucket.configuration.id
  versioning_configuration { status = "Enabled" }
}
resource "aws_s3_bucket_server_side_encryption_configuration" "configuration" {
  bucket = aws_s3_bucket.configuration.id
  rule {
    apply_server_side_encryption_by_default { sse_algorithm = "AES256" }
  }
}
resource "aws_s3_bucket_public_access_block" "configuration" {
  bucket                  = aws_s3_bucket.configuration.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}
resource "aws_s3_bucket_policy" "configuration" {
  bucket = aws_s3_bucket.configuration.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "RequireTLS", Effect = "Deny", Principal = "*", Action = "s3:*"
      Resource  = [aws_s3_bucket.configuration.arn, "${aws_s3_bucket.configuration.arn}/*"]
      Condition = { Bool = { "aws:SecureTransport" = "false" } }
    }]
  })
}

resource "aws_iam_openid_connect_provider" "github" {
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
}
resource "aws_iam_role" "release" {
  name                 = "${local.name}-release"
  max_session_duration = 3600
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow", Action = "sts:AssumeRoleWithWebIdentity"
      Principal = { Federated = aws_iam_openid_connect_provider.github.arn }
      Condition = { StringEquals = {
        "token.actions.githubusercontent.com:aud" = "sts.amazonaws.com"
        "token.actions.githubusercontent.com:sub" = "repo:AY2627S1-CS3219-P1/FoC:environment:aws-demo"
      } }
    }]
  })
}
resource "aws_iam_role_policy" "release" {
  name = "deploy-only"
  role = aws_iam_role.release.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      { Effect = "Allow", Action = ["eks:DescribeCluster"], Resource = data.aws_eks_cluster.demo.arn },
      { Effect = "Allow", Action = ["ecr:GetAuthorizationToken"], Resource = "*" },
      { Effect = "Allow", Action = ["ecr:DescribeRepositories", "ecr:DescribeImages", "ecr:BatchCheckLayerAvailability", "ecr:GetDownloadUrlForLayer", "ecr:BatchGetImage", "ecr:InitiateLayerUpload", "ecr:UploadLayerPart", "ecr:CompleteLayerUpload", "ecr:PutImage"], Resource = local.repositories },
      { Effect = "Allow", Action = ["s3:GetBucketVersioning"], Resource = aws_s3_bucket.configuration.arn },
      { Effect = "Allow", Action = ["s3:GetObjectVersion"], Resource = "${aws_s3_bucket.configuration.arn}/${local.prefix}/config/aws-demo/*" },
      { Effect = "Allow", Action = ["s3:PutObject", "s3:DeleteObjectVersion"], Resource = "${aws_s3_bucket.configuration.arn}/${local.prefix}/locks/aws-demo.json" }
    ]
  })
}
resource "aws_eks_access_entry" "release" {
  cluster_name      = var.cluster_name
  principal_arn     = aws_iam_role.release.arn
  kubernetes_groups = ["foc-release"]
  type              = "STANDARD"
}

resource "aws_security_group" "runner" {
  name        = "${local.name}-runner"
  description = "Disposable release runner; no inbound access"
  vpc_id      = data.aws_vpc.demo.id
}
resource "aws_vpc_security_group_egress_rule" "https" {
  security_group_id = aws_security_group.runner.id
  ip_protocol       = "tcp"
  from_port         = 443
  to_port           = 443
  cidr_ipv4         = "0.0.0.0/0"
}
# Existing Debian-based image builds fetch signed package indexes over HTTP.
resource "aws_vpc_security_group_egress_rule" "packages" {
  security_group_id = aws_security_group.runner.id
  ip_protocol       = "tcp"
  from_port         = 80
  to_port           = 80
  cidr_ipv4         = "0.0.0.0/0"
}
resource "aws_vpc_security_group_egress_rule" "dns" {
  security_group_id = aws_security_group.runner.id
  ip_protocol       = "udp"
  from_port         = 53
  to_port           = 53
  cidr_ipv4         = data.aws_vpc.demo.cidr_block
}
resource "aws_vpc_security_group_ingress_rule" "private_eks" {
  security_group_id            = data.aws_eks_cluster.demo.vpc_config[0].cluster_security_group_id
  referenced_security_group_id = aws_security_group.runner.id
  ip_protocol                  = "tcp"
  from_port                    = 443
  to_port                      = 443
  description                  = "Private EKS API from the disposable CI runner"
}
resource "aws_iam_role" "bootstrap" {
  name = "${local.name}-runner-bootstrap"
  assume_role_policy = jsonencode({
    Version = "2012-10-17", Statement = [{ Effect = "Allow", Action = "sts:AssumeRole", Principal = { Service = "ec2.amazonaws.com" } }]
  })
}
resource "aws_iam_role_policy" "bootstrap" {
  name = "one-time-registration"
  role = aws_iam_role.bootstrap.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      { Effect = "Allow", Action = ["ssm:GetParameter", "ssm:DeleteParameter"], Resource = "arn:aws:ssm:${var.region}:${var.account_id}:parameter${local.parameter}" },
      { Effect = "Allow", Action = ["kms:Decrypt"], Resource = "arn:aws:kms:${var.region}:${var.account_id}:key/*", Condition = { StringEquals = {
        "kms:ViaService"                      = "ssm.${var.region}.amazonaws.com"
        "kms:EncryptionContext:PARAMETER_ARN" = "arn:aws:ssm:${var.region}:${var.account_id}:parameter${local.parameter}"
      } } }
    ]
  })
}
resource "aws_iam_instance_profile" "bootstrap" {
  name = "${local.name}-runner-bootstrap"
  role = aws_iam_role.bootstrap.name
}
resource "aws_launch_template" "runner" {
  name                                 = "${local.name}-runner"
  image_id                             = var.runner_ami
  instance_type                        = "t3.large"
  instance_initiated_shutdown_behavior = "terminate"
  update_default_version               = true
  iam_instance_profile { arn = aws_iam_instance_profile.bootstrap.arn }
  credit_specification { cpu_credits = "standard" }
  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
    instance_metadata_tags      = "disabled"
  }
  network_interfaces {
    associate_public_ip_address = false
    subnet_id                   = var.private_subnet_id
    security_groups             = [aws_security_group.runner.id]
    delete_on_termination       = true
  }
  block_device_mappings {
    device_name = "/dev/xvda"
    ebs {
      volume_size           = 80
      volume_type           = "gp3"
      encrypted             = true
      delete_on_termination = true
    }
  }
  tag_specifications {
    resource_type = "instance"
    tags          = { Name = "${local.name}-runner", Project = "foc", ExpiresOn = var.expires_on, ManagedBy = "terraform" }
  }
  user_data = base64encode(templatefile("${path.module}/runner.sh.tftpl", {
    region   = var.region, parameter = local.parameter,
    endpoint = data.aws_eks_cluster.demo.endpoint, ca = data.aws_eks_cluster.demo.certificate_authority[0].data
  }))
}
output "ci" {
  value = {
    account_id              = var.account_id, region = var.region, bucket = aws_s3_bucket.configuration.id, prefix = local.prefix,
    role_arn                = aws_iam_role.release.arn, launch_template_id = aws_launch_template.runner.id,
    launch_template_version = aws_launch_template.runner.latest_version, registration_parameter = local.parameter,
    runner_name             = "${local.name}-runner"
  }
}
