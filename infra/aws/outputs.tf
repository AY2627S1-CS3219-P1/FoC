output "deployment" {
  description = "Non-secret inputs for the generated production overlay and operator commands."
  value = {
    moderation_enabled           = var.enable_moderation_service
    ses_sender_email             = var.ses_sender_email
    user_email_role_arn          = try(aws_iam_role.email[0].arn, null)
    communication_email_role_arn = try(aws_iam_role.communication_email[0].arn, null)
    region                       = var.region
    cluster_name                 = module.eks.cluster_name
    vpc_id                       = module.vpc.vpc_id
    public_hostname              = local.public_hostname
    public_origin                = local.public_origin
    gateway_target_arn           = aws_lb_target_group.gateway.arn
    image_prefix                 = "${var.account_id}.dkr.ecr.${var.region}.amazonaws.com/${var.name}"
    order_bucket                 = aws_s3_bucket.orders.id
    order_role_arn               = aws_iam_role.order.arn
    loki_bucket                  = aws_s3_bucket.loki.id
    loki_role_arn                = aws_iam_role.loki.arn
    evidence_bucket              = try(aws_s3_bucket.moderation_evidence[0].id, null)
    evidence_role_arn            = try(aws_iam_role.moderation[0].arn, null)
    ingress_role_arn             = aws_iam_role.ingress.arn
    database_host                = aws_db_instance.database.address
    database_port                = aws_db_instance.database.port
    database_admin               = aws_db_instance.database.username
    database_secret_arn          = aws_db_instance.database.master_user_secret[0].secret_arn
  }
}
