# Loki owns log retention through its compactor. Keep log objects separate from
# Order uploads and Terraform state; teardown requires an explicit data decision.
resource "aws_s3_bucket" "loki" {
  bucket_prefix = "${var.name}-loki-"
  force_destroy = false
}

resource "aws_s3_bucket_public_access_block" "loki" {
  bucket                  = aws_s3_bucket.loki.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "loki" {
  bucket = aws_s3_bucket.loki.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_policy" "loki" {
  bucket = aws_s3_bucket.loki.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "DenyInsecureTransport"
      Effect    = "Deny"
      Principal = "*"
      Action    = "s3:*"
      Resource  = [aws_s3_bucket.loki.arn, "${aws_s3_bucket.loki.arn}/*"]
      Condition = { Bool = { "aws:SecureTransport" = "false" } }
    }]
  })
}

resource "aws_iam_role" "loki" {
  name               = "${var.name}-loki"
  assume_role_policy = data.aws_iam_policy_document.workload_trust["loki"].json
}

resource "aws_iam_role_policy" "loki" {
  role = aws_iam_role.loki.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["s3:ListBucket"]
        Resource = aws_s3_bucket.loki.arn
      },
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"]
        Resource = "${aws_s3_bucket.loki.arn}/*"
      },
    ]
  })
}
