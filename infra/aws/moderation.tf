# Reports and appeals use separate storage and identity from other workloads.
resource "aws_s3_bucket" "moderation_evidence" {
  count         = var.enable_moderation_service ? 1 : 0
  bucket_prefix = "${var.name}-evidence-"
  force_destroy = false
}

resource "aws_s3_bucket_public_access_block" "moderation_evidence" {
  count                   = var.enable_moderation_service ? 1 : 0
  bucket                  = aws_s3_bucket.moderation_evidence[0].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "moderation_evidence" {
  count  = var.enable_moderation_service ? 1 : 0
  bucket = aws_s3_bucket.moderation_evidence[0].id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_cors_configuration" "moderation_evidence" {
  count  = var.enable_moderation_service ? 1 : 0
  bucket = aws_s3_bucket.moderation_evidence[0].id
  cors_rule {
    allowed_headers = ["*"]
    allowed_methods = ["GET", "HEAD", "PUT"]
    allowed_origins = [local.public_origin]
    expose_headers  = ["ETag"]
    max_age_seconds = 3600
  }
}

resource "aws_s3_bucket_policy" "moderation_evidence" {
  count  = var.enable_moderation_service ? 1 : 0
  bucket = aws_s3_bucket.moderation_evidence[0].id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "DenyInsecureTransport"
      Effect    = "Deny"
      Principal = "*"
      Action    = "s3:*"
      Resource  = [aws_s3_bucket.moderation_evidence[0].arn, "${aws_s3_bucket.moderation_evidence[0].arn}/*"]
      Condition = { Bool = { "aws:SecureTransport" = "false" } }
    }]
  })
}

resource "aws_iam_role" "moderation" {
  count              = var.enable_moderation_service ? 1 : 0
  name               = "${var.name}-moderation-evidence"
  assume_role_policy = data.aws_iam_policy_document.workload_trust["moderation"].json
}

resource "aws_iam_role_policy" "moderation" {
  count = var.enable_moderation_service ? 1 : 0
  role  = aws_iam_role.moderation[0].id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
      Resource = "${aws_s3_bucket.moderation_evidence[0].arn}/evidence/*"
    }]
  })
}

output "moderation" {
  description = "Prepared reports/appeals resources; runtime and release wiring are separate. Null when disabled."
  value = var.enable_moderation_service ? {
    image_repository_url = aws_ecr_repository.images["moderation-service"].repository_url
    evidence_bucket      = aws_s3_bucket.moderation_evidence[0].id
    evidence_role_arn    = aws_iam_role.moderation[0].arn
    namespace            = "foc"
    service_account      = "moderation-service"
    database_name        = "moderation_db"
    database_role        = "foc_moderation"
    database_secret      = "moderation-db"
  } : null
}
