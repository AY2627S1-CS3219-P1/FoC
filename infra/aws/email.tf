resource "aws_iam_role" "email" {
  count              = var.ses_sender_email == null ? 0 : 1
  name               = "${var.name}-communication-email"
  assume_role_policy = data.aws_iam_policy_document.workload_trust["email"].json
}

# SESv2 SendEmail with Raw MIME content also requires ses:SendRawEmail.
resource "aws_iam_role_policy" "email" {
  count = var.ses_sender_email == null ? 0 : 1
  role  = aws_iam_role.email[0].id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["ses:SendEmail", "ses:SendRawEmail"]
      Resource = "arn:aws:ses:${var.region}:${var.account_id}:identity/${var.ses_sender_email}"
      Condition = {
        StringEquals = { "ses:FromAddress" = var.ses_sender_email }
      }
    }]
  })
}
