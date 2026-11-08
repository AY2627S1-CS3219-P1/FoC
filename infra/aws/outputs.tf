output "deployment" {
  description = "Non-secret inputs for the generated production overlay and operator commands."
  value = {
    region              = var.region
    cluster_name        = module.eks.cluster_name
    vpc_id              = module.vpc.vpc_id
    public_hostname     = var.public_hostname
    image_prefix        = "${var.account_id}.dkr.ecr.${var.region}.amazonaws.com/${var.name}"
    order_bucket        = aws_s3_bucket.orders.id
    order_role_arn      = aws_iam_role.order.arn
    ingress_role_arn    = aws_iam_role.ingress.arn
    database_host       = aws_db_instance.database.address
    database_port       = aws_db_instance.database.port
    database_admin      = aws_db_instance.database.username
    database_secret_arn = aws_db_instance.database.master_user_secret[0].secret_arn
    public_subnet_ids   = module.vpc.public_subnets
  }
}

output "mail_dns" {
  description = "Publish these SES verification and DKIM records in the authoritative DNS zone."
  value = {
    verification = {
      name  = "_amazonses.${var.public_hostname}"
      type  = "TXT"
      value = aws_ses_domain_identity.mail.verification_token
    }
    dkim = [for token in aws_ses_domain_dkim.mail.dkim_tokens : {
      name  = "${token}._domainkey.${var.public_hostname}"
      type  = "CNAME"
      value = "${token}.dkim.amazonses.com"
    }]
  }
}
