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

resource "aws_security_group" "vpc_link" {
  name        = "${var.name}-vpc-link"
  description = "API Gateway private integration"
  vpc_id      = module.vpc.vpc_id
}

resource "aws_security_group" "ingress" {
  name        = "${var.name}-ingress"
  description = "Internal NLB; only the API Gateway VPC link may connect"
  vpc_id      = module.vpc.vpc_id
}

resource "aws_vpc_security_group_egress_rule" "vpc_link" {
  security_group_id            = aws_security_group.vpc_link.id
  referenced_security_group_id = aws_security_group.ingress.id
  ip_protocol                  = "tcp"
  from_port                    = 80
  to_port                      = 80
}

resource "aws_vpc_security_group_ingress_rule" "ingress" {
  security_group_id            = aws_security_group.ingress.id
  referenced_security_group_id = aws_security_group.vpc_link.id
  ip_protocol                  = "tcp"
  from_port                    = 80
  to_port                      = 80
}

# The pinned Istio gateway chart maps Service HTTP port 80 to pod port 80.
# Verify the generated EndpointSlices before changing the gateway chart.
resource "aws_vpc_security_group_egress_rule" "gateway" {
  security_group_id            = aws_security_group.ingress.id
  referenced_security_group_id = module.eks.node_security_group_id
  ip_protocol                  = "tcp"
  from_port                    = 80
  to_port                      = 80
}

resource "aws_vpc_security_group_ingress_rule" "gateway" {
  security_group_id            = module.eks.node_security_group_id
  referenced_security_group_id = aws_security_group.ingress.id
  ip_protocol                  = "tcp"
  from_port                    = 80
  to_port                      = 80
}

resource "aws_lb" "ingress" {
  name                             = "${var.name}-nlb"
  internal                         = true
  load_balancer_type               = "network"
  subnets                          = module.vpc.private_subnets
  security_groups                  = [aws_security_group.ingress.id]
  enable_cross_zone_load_balancing = true
}

resource "aws_lb_target_group" "gateway" {
  name        = "${var.name}-gw"
  port        = 80
  protocol    = "TCP"
  target_type = "ip"
  vpc_id      = module.vpc.vpc_id

  health_check {
    protocol = "TCP"
    port     = "traffic-port"
  }
}

resource "aws_lb_listener" "gateway" {
  load_balancer_arn = aws_lb.ingress.arn
  port              = 80
  protocol          = "TCP"
  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.gateway.arn
  }
}

resource "aws_apigatewayv2_api" "public" {
  name          = var.name
  protocol_type = "HTTP"
  # FoC owns authentication. Proxy frontend and RPC paths through one origin.
}

resource "aws_apigatewayv2_vpc_link" "ingress" {
  name               = var.name
  subnet_ids         = module.vpc.private_subnets
  security_group_ids = [aws_security_group.vpc_link.id]
}

resource "aws_apigatewayv2_integration" "ingress" {
  api_id                 = aws_apigatewayv2_api.public.id
  integration_type       = "HTTP_PROXY"
  integration_method     = "ANY"
  integration_uri        = aws_lb_listener.gateway.arn
  connection_type        = "VPC_LINK"
  connection_id          = aws_apigatewayv2_vpc_link.ingress.id
  payload_format_version = "1.0"
  timeout_milliseconds   = 30000
  request_parameters = {
    "overwrite:path" = "$request.path"
    # Istio's HTTP listener has no public-host constraint. The Node frontend
    # uses its explicit ORIGIN, not an untrusted forwarded-host header.
  }
}

resource "aws_apigatewayv2_route" "public" {
  api_id             = aws_apigatewayv2_api.public.id
  route_key          = "$default"
  target             = "integrations/${aws_apigatewayv2_integration.ingress.id}"
  authorization_type = "NONE"
}

resource "aws_cloudwatch_log_group" "api" {
  name              = "/aws/apigateway/${var.name}"
  retention_in_days = 7
}

resource "aws_apigatewayv2_stage" "public" {
  api_id      = aws_apigatewayv2_api.public.id
  name        = "$default"
  auto_deploy = true
  default_route_settings {
    throttling_burst_limit = 5000
    throttling_rate_limit  = 10000
  }
  access_log_settings {
    destination_arn = aws_cloudwatch_log_group.api.arn
    # Exclude query strings, cookies, headers, and bodies containing credentials.
    format = jsonencode({
      requestId = "$context.requestId"
      method    = "$context.httpMethod"
      status    = "$context.status"
      latency   = "$context.responseLatency"
    })
  }
  depends_on = [aws_apigatewayv2_route.public]
}

locals {
  public_origin   = aws_apigatewayv2_api.public.api_endpoint
  public_hostname = trimprefix(local.public_origin, "https://")
}
