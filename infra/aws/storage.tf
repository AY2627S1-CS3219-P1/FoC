locals {
  images = toset([
    "user-service", "supplier-service", "order-service", "credit-service",
    "communication-service", "content-moderation-service", "frontend", "migrations", "kafka-init",
  ])
}

resource "aws_ecr_repository" "images" {
  for_each             = local.images
  name                 = "${var.name}/${each.key}"
  image_tag_mutability = "IMMUTABLE"
  force_delete         = false
  image_scanning_configuration {
    scan_on_push = true
  }
}

resource "aws_ecr_lifecycle_policy" "images" {
  for_each   = aws_ecr_repository.images
  repository = each.value.name
  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Remove untagged build artifacts after one day"
      selection = {
        tagStatus   = "untagged"
        countType   = "sinceImagePushed"
        countUnit   = "days"
        countNumber = 1
      }
      action = { type = "expire" }
    }]
  })
}

resource "aws_s3_bucket" "orders" {
  bucket_prefix = "${var.name}-orders-"
  force_destroy = false
}

resource "aws_s3_bucket_public_access_block" "orders" {
  bucket                  = aws_s3_bucket.orders.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "orders" {
  bucket = aws_s3_bucket.orders.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_cors_configuration" "orders" {
  bucket = aws_s3_bucket.orders.id
  cors_rule {
    allowed_headers = ["*"]
    allowed_methods = ["GET", "HEAD", "PUT"]
    allowed_origins = [local.public_origin]
    expose_headers  = ["ETag"]
    max_age_seconds = 3600
  }
}

data "aws_iam_policy_document" "bucket" {
  statement {
    sid     = "DenyInsecureTransport"
    effect  = "Deny"
    actions = ["s3:*"]
    resources = [
      aws_s3_bucket.orders.arn,
      "${aws_s3_bucket.orders.arn}/*",
    ]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "orders" {
  bucket = aws_s3_bucket.orders.id
  policy = data.aws_iam_policy_document.bucket.json
}

resource "aws_iam_role" "order" {
  name               = "${var.name}-order-storage"
  assume_role_policy = data.aws_iam_policy_document.workload_trust["order"].json
}

resource "aws_iam_role_policy" "order" {
  role = aws_iam_role.order.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["s3:ListBucket", "s3:GetBucketLocation"]
        Resource = aws_s3_bucket.orders.arn
      },
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
        Resource = "${aws_s3_bucket.orders.arn}/*"
      },
    ]
  })
}
