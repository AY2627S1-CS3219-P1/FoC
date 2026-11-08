resource "aws_iam_role" "ingress" {
  name               = "${var.name}-load-balancer"
  assume_role_policy = data.aws_iam_policy_document.workload_trust["ingress"].json
}

resource "aws_iam_policy" "ingress" {
  name = "${var.name}-load-balancer"
  # Upstream v3.6.2 policy. Provenance and checksum are recorded in README.md.
  policy = file("${path.module}/policies/load-balancer-controller.json")
}

resource "aws_iam_role_policy_attachment" "ingress" {
  role       = aws_iam_role.ingress.name
  policy_arn = aws_iam_policy.ingress.arn
}

# DNS records must be published by the domain owner before SES can send mail.
resource "aws_ses_domain_identity" "mail" {
  domain = var.public_hostname
}

resource "aws_ses_domain_dkim" "mail" {
  domain = aws_ses_domain_identity.mail.domain
}
