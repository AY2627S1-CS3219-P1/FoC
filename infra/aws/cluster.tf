module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "21.29.0"

  name               = var.name
  kubernetes_version = var.kubernetes_version
  vpc_id             = module.vpc.vpc_id
  subnet_ids         = module.vpc.private_subnets

  endpoint_private_access                  = true
  endpoint_public_access                   = true
  endpoint_public_access_cidrs             = [var.operator_cidr]
  enable_cluster_creator_admin_permissions = true
  enable_irsa                              = true
  create_kms_key                           = false
  encryption_config                        = null
  upgrade_policy                           = { support_type = "STANDARD" }

  enabled_log_types                      = ["api", "audit", "authenticator"]
  cloudwatch_log_group_retention_in_days = 7

  addons = {
    vpc-cni = {
      before_compute = true
    }
    kube-proxy = {}
    coredns    = {}
    aws-ebs-csi-driver = {
      service_account_role_arn = aws_iam_role.ebs.arn
    }
  }

  # Istio injection webhook and xDS are served on these non-default ports.
  node_security_group_additional_rules = {
    control_plane_istio_injection = {
      description                   = "EKS API to Istio injection webhook"
      from_port                     = 15017
      to_port                       = 15017
      protocol                      = "tcp"
      source_cluster_security_group = true
    }
    node_mesh = {
      description = "Istio mesh and probes between worker nodes"
      from_port   = 0
      to_port     = 65535
      protocol    = "tcp"
      self        = true
    }
  }

  eks_managed_node_groups = {
    demo = {
      instance_types       = ["t3.xlarge"]
      capacity_type        = "ON_DEMAND"
      ami_type             = "AL2023_x86_64_STANDARD"
      min_size             = 2
      max_size             = 2
      desired_size         = 2
      update_config        = { max_unavailable = 1, update_strategy = "MINIMAL" }
      credit_specification = { cpu_credits = "standard" }
      enable_monitoring    = false

      metadata_options = {
        http_endpoint               = "enabled"
        http_tokens                 = "required"
        http_put_response_hop_limit = 1
      }
      block_device_mappings = {
        root = {
          device_name = "/dev/xvda"
          ebs = {
            volume_size           = 20
            volume_type           = "gp3"
            encrypted             = true
            delete_on_termination = true
          }
        }
      }
    }
  }
}

locals {
  oidc_issuer = replace(module.eks.cluster_oidc_issuer_url, "https://", "")
  workload_identities = merge({
    order   = "system:serviceaccount:foc:order-service"
    ebs     = "system:serviceaccount:kube-system:ebs-csi-controller-sa"
    ingress = "system:serviceaccount:kube-system:aws-load-balancer-controller"
    loki    = "system:serviceaccount:foc-observability:loki"
    }, var.ses_sender_email == null ? {} : {
    email = "system:serviceaccount:foc:communication-service"
    }, var.enable_moderation_service ? {
    moderation = "system:serviceaccount:foc:moderation-service"
  } : {})
}

data "aws_iam_policy_document" "workload_trust" {
  for_each = local.workload_identities
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [module.eks.oidc_provider_arn]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_issuer}:sub"
      values   = [each.value]
    }
    condition {
      test     = "StringEquals"
      variable = "${local.oidc_issuer}:aud"
      values   = ["sts.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "ebs" {
  name               = "${var.name}-ebs"
  assume_role_policy = data.aws_iam_policy_document.workload_trust["ebs"].json
}

resource "aws_iam_role_policy_attachment" "ebs" {
  role       = aws_iam_role.ebs.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
}
