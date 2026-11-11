# Expected outcomes come from the agreed demo topology and AWS security contracts.
# All providers and the upstream cluster/network modules are mocked. No AWS calls.
mock_provider "aws" {
  override_during = plan
  mock_resource "aws_apigatewayv2_api" {
    defaults = {
      id           = "a1b2c3d4"
      api_endpoint = "https://a1b2c3d4.execute-api.ap-southeast-1.amazonaws.com"
    }
  }
  mock_resource "aws_lb" {
    defaults = { arn = "arn:aws:elasticloadbalancing:ap-southeast-1:123456789012:loadbalancer/net/foc-demo-nlb/0123456789abcdef" }
  }
  mock_resource "aws_lb_target_group" {
    defaults = { arn = "arn:aws:elasticloadbalancing:ap-southeast-1:123456789012:targetgroup/foc-demo-gw/0123456789abcdef" }
  }
  mock_resource "aws_lb_listener" {
    defaults = { arn = "arn:aws:elasticloadbalancing:ap-southeast-1:123456789012:listener/net/foc-demo-nlb/0123456789abcdef/0123456789abcdef" }
  }
  mock_resource "aws_cloudwatch_log_group" {
    defaults = { arn = "arn:aws:logs:ap-southeast-1:123456789012:log-group:/aws/apigateway/foc-demo" }
  }
  mock_data "aws_availability_zones" {
    defaults = { names = ["ap-southeast-1a", "ap-southeast-1b"] }
  }
  mock_data "aws_iam_policy_document" {
    defaults = { json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}" }
  }
  mock_resource "aws_db_instance" {
    defaults = {
      address = "demo.database.invalid"
      port    = 5432
      master_user_secret = [{
        secret_arn    = "arn:aws:secretsmanager:ap-southeast-1:123456789012:secret:demo"
        secret_status = "active"
        kms_key_id    = "arn:aws:kms:ap-southeast-1:123456789012:key/00000000-0000-0000-0000-000000000001"
      }]
    }
  }
  mock_resource "aws_s3_bucket" {
    defaults = {
      arn = "arn:aws:s3:::demo-orders"
      id  = "demo-orders"
    }
  }
  mock_resource "aws_iam_role" {
    defaults = { arn = "arn:aws:iam::123456789012:role/demo" }
  }
  mock_resource "aws_iam_policy" {
    defaults = { arn = "arn:aws:iam::123456789012:policy/demo" }
  }
}

override_resource {
  target          = aws_s3_bucket.loki
  override_during = plan
  values = {
    id  = "demo-loki"
    arn = "arn:aws:s3:::demo-loki"
  }
}

override_module {
  target = module.vpc
  outputs = {
    vpc_id                     = "vpc-00000000000000001"
    public_subnets             = ["subnet-00000000000000001", "subnet-00000000000000002"]
    private_subnets            = ["subnet-00000000000000003", "subnet-00000000000000004"]
    private_route_table_ids    = ["rtb-00000000000000001"]
    database_subnet_group_name = "demo-database"
  }
}

override_module {
  target = module.eks
  outputs = {
    cluster_name            = "foc-demo"
    node_security_group_id  = "sg-00000000000000001"
    cluster_oidc_issuer_url = "https://oidc.eks.ap-southeast-1.amazonaws.com/id/EXAMPLE"
    oidc_provider_arn       = "arn:aws:iam::123456789012:oidc-provider/oidc.eks.ap-southeast-1.amazonaws.com/id/EXAMPLE"
  }
}

variables {
  account_id    = "123456789012"
  operator_cidr = "8.8.8.8/32"
  expires_on    = "2026-11-16"
}

run "private_demo_data_and_images" {
  command = plan

  # Capacity requested for the load-test environment, not measured throughput.
  assert {
    condition     = aws_db_instance.database.instance_class == "db.m7g.large"
    error_message = "Load testing needs the non-burstable 8 GiB database default."
  }
  assert {
    condition     = one(aws_apigatewayv2_stage.public.default_route_settings).throttling_rate_limit == 10000 && one(aws_apigatewayv2_stage.public.default_route_settings).throttling_burst_limit == 5000
    error_message = "Load testing needs a 10,000 RPS gateway limit and 5,000-request burst capacity."
  }

  assert {
    condition     = length(aws_ecr_repository.images) == 9 && contains(keys(aws_ecr_repository.images), "content-moderation-service") && alltrue([for image in aws_ecr_repository.images : image.image_tag_mutability == "IMMUTABLE" && !image.force_delete])
    error_message = "The demo needs nine immutable image repositories including CMS with explicit data deletion."
  }
  assert {
    condition     = !aws_db_instance.database.publicly_accessible && !aws_db_instance.database.multi_az && aws_db_instance.database.storage_encrypted && aws_db_instance.database.manage_master_user_password
    error_message = "RDS must be private, single-AZ, encrypted, and use an AWS-managed administrator secret."
  }
  assert {
    condition     = aws_db_instance.database.deletion_protection && !aws_db_instance.database.skip_final_snapshot && !aws_s3_bucket.orders.force_destroy
    error_message = "Teardown must not silently discard database or object data."
  }
  assert {
    condition     = one(aws_s3_bucket_cors_configuration.orders.cors_rule).allowed_origins == toset(["https://a1b2c3d4.execute-api.ap-southeast-1.amazonaws.com"]) && aws_s3_bucket_public_access_block.orders.block_public_policy
    error_message = "Browser uploads require exact-origin CORS while the bucket stays private."
  }
  assert {
    condition     = aws_vpc_security_group_ingress_rule.database.from_port == 5432 && aws_vpc_security_group_ingress_rule.database.referenced_security_group_id == "sg-00000000000000001"
    error_message = "PostgreSQL ingress must be restricted to worker workloads."
  }
  assert {
    condition     = aws_lb.ingress.internal && aws_lb_listener.gateway.port == 80 && aws_lb_target_group.gateway.target_type == "ip"
    error_message = "API Gateway requires a private NLB HTTP ingress with pod IP targets."
  }
  assert {
    condition     = aws_vpc_security_group_ingress_rule.gateway.from_port == 80 && aws_vpc_security_group_ingress_rule.gateway.to_port == 80 && aws_vpc_security_group_egress_rule.gateway.from_port == 80 && aws_vpc_security_group_egress_rule.gateway.to_port == 80
    error_message = "NLB target traffic must be restricted to the pinned Istio gateway port 80."
  }
  assert {
    condition     = aws_apigatewayv2_api.public.protocol_type == "HTTP" && aws_apigatewayv2_stage.public.name == "$default" && aws_apigatewayv2_route.public.route_key == "$default"
    error_message = "Frontend and RPCs must share the default API Gateway HTTPS endpoint."
  }
  assert {
    condition     = aws_apigatewayv2_integration.ingress.connection_type == "VPC_LINK" && aws_apigatewayv2_integration.ingress.integration_method == "ANY" && aws_apigatewayv2_integration.ingress.request_parameters["overwrite:path"] == "$request.path" && aws_apigatewayv2_integration.ingress.timeout_milliseconds == 30000
    error_message = "Private HTTP proxy must forward all methods and preserve RPC paths without stage prefixes."
  }
  assert {
    condition     = aws_vpc_endpoint.s3.vpc_endpoint_type == "Gateway" && length(aws_vpc_endpoint.s3.route_table_ids) > 0
    error_message = "Private worker S3 traffic must use a gateway endpoint."
  }
}

# AWS CreateLoadBalancer prohibits names starting with internal-.
run "reject_reserved_nlb_name" {
  command = plan
  variables {
    name = "internal-demo"
  }
  expect_failures = [var.name]
}

# RDS CreateDBInstance identifiers cannot end with a hyphen or contain consecutive hyphens.
run "reject_trailing_hyphen_name" {
  command = plan
  variables {
    name = "demo-"
  }
  expect_failures = [var.name]
}

run "reject_consecutive_hyphens_name" {
  command = plan
  variables {
    name = "demo--blue"
  }
  expect_failures = [var.name]
}

run "accept_internal_hyphen_name" {
  command = plan
  variables {
    name = "demo-blue1"
  }
  assert {
    condition     = aws_db_instance.database.identifier == "demo-blue1"
    error_message = "A valid RDS identifier must reach the database unchanged."
  }
}

run "reject_open_operator_api" {
  command = plan
  variables {
    operator_cidr = "0.0.0.0/0"
  }
  expect_failures = [var.operator_cidr]
}

run "smtp_creates_no_email_role" {
  command = plan
  assert {
    condition     = length(aws_iam_role.email) == 0 && length(aws_iam_role_policy.email) == 0 && length(aws_iam_role.communication_email) == 0 && length(aws_iam_role_policy.communication_email) == 0 && !contains(keys(local.workload_identities), "email") && !contains(keys(local.workload_identities), "communication_email")
    error_message = "SMTP must remain the default without granting SES permissions."
  }
}

# Preserve User's existing SES identity while adding Communication independently.
run "ses_is_sender_and_service_scoped" {
  command = plan
  variables {
    ses_sender_email = "sender@example.test"
  }
  assert {
    condition     = length(aws_iam_role.email) == 1 && aws_iam_role.email[0].name == "foc-demo-user-email" && local.workload_identities["email"] == "system:serviceaccount:foc:user-service"
    error_message = "User's original role and Kubernetes identity must remain available."
  }
  assert {
    condition     = length(aws_iam_role.communication_email) == 1 && aws_iam_role.communication_email[0].name == "foc-demo-communication-email" && local.workload_identities["communication_email"] == "system:serviceaccount:foc:communication-service"
    error_message = "Communication requires a separate role bound to its own Kubernetes identity."
  }
  assert {
    condition = alltrue([for policy in [aws_iam_role_policy.email[0].policy, aws_iam_role_policy.communication_email[0].policy] :
      jsondecode(policy).Statement[0].Action == ["ses:SendEmail", "ses:SendRawEmail"] && jsondecode(policy).Statement[0].Resource == "arn:aws:ses:ap-southeast-1:123456789012:identity/sender@example.test" && jsondecode(policy).Statement[0].Condition.StringEquals["ses:FromAddress"] == "sender@example.test"
    ])
    error_message = "Both SES policies must remain restricted to the verified regional sender."
  }
  assert {
    condition = alltrue([for key, subject in {
      email               = "system:serviceaccount:foc:user-service"
      communication_email = "system:serviceaccount:foc:communication-service"
      } :
      toset(one([for condition in one(data.aws_iam_policy_document.workload_trust[key].statement).condition : condition.values if endswith(condition.variable, ":sub")])) == toset([subject]) && toset(one([for condition in one(data.aws_iam_policy_document.workload_trust[key].statement).condition : condition.values if endswith(condition.variable, ":aud")])) == toset(["sts.amazonaws.com"])
    ])
    error_message = "Each role must trust only its own exact service account and STS audience."
  }
}

run "reject_display_name_sender" {
  command = plan
  variables {
    ses_sender_email = "FoC <sender@example.test>"
  }
  expect_failures = [var.ses_sender_email]
}

# IANA special-purpose registry and RFC 5771 define these non-public source ranges.
run "reject_operator_private_class_a" {
  command = plan
  variables {
    operator_cidr = "10.0.0.1/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_private_class_b" {
  command = plan
  variables {
    operator_cidr = "172.16.0.1/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_private_class_c" {
  command = plan
  variables {
    operator_cidr = "192.168.1.20/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_loopback" {
  command = plan
  variables {
    operator_cidr = "127.1.2.3/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_link_local" {
  command = plan
  variables {
    operator_cidr = "169.254.1.1/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_shared_address_space" {
  command = plan
  variables {
    operator_cidr = "100.64.0.0/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_benchmark" {
  command = plan
  variables {
    operator_cidr = "198.18.0.1/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_documentation_one" {
  command = plan
  variables {
    operator_cidr = "192.0.2.1/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_documentation_two" {
  command = plan
  variables {
    operator_cidr = "198.51.100.1/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_documentation_three" {
  command = plan
  variables {
    operator_cidr = "203.0.113.10/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_this_network" {
  command = plan
  variables {
    operator_cidr = "0.1.2.3/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_protocol_assignment" {
  command = plan
  variables {
    operator_cidr = "192.0.0.8/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_deprecated_relay" {
  command = plan
  variables {
    operator_cidr = "192.88.99.2/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_multicast" {
  command = plan
  variables {
    operator_cidr = "224.0.0.1/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_reserved" {
  command = plan
  variables {
    operator_cidr = "240.0.0.1/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_broadcast" {
  command = plan
  variables {
    operator_cidr = "255.255.255.255/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_ipv6" {
  command = plan
  variables {
    operator_cidr = "2001:4860:4860::8888/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_malformed" {
  command = plan
  variables {
    operator_cidr = "not-an-address/32"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_operator_wider_public" {
  command = plan
  variables {
    operator_cidr = "8.8.8.8/31"
  }
  expect_failures = [var.operator_cidr]
}

run "reject_null_operator" {
  command = plan
  variables {
    operator_cidr = null
  }
  expect_failures = [var.operator_cidr]
}

# These are mock-only globally reachable addresses, never operator deployment defaults.
run "accept_operator_pcp_anycast" {
  command = plan
  variables {
    operator_cidr = "192.0.0.9/32"
  }
}

run "accept_operator_turn_anycast" {
  command = plan
  variables {
    operator_cidr = "192.0.0.10/32"
  }
}

run "accept_operator_shared_space_boundary" {
  command = plan
  variables {
    operator_cidr = "100.63.255.255/32"
  }
}

run "accept_operator_global_special_purpose" {
  command = plan
  variables {
    operator_cidr = "192.31.196.1/32"
  }
}

run "logging_has_dedicated_private_storage_and_identity" {
  command = plan
  assert {
    condition     = !aws_s3_bucket.loki.force_destroy && aws_s3_bucket.loki.bucket_prefix != aws_s3_bucket.orders.bucket_prefix && aws_s3_bucket_public_access_block.loki.block_public_acls && aws_s3_bucket_public_access_block.loki.block_public_policy && aws_s3_bucket_public_access_block.loki.ignore_public_acls && aws_s3_bucket_public_access_block.loki.restrict_public_buckets
    error_message = "Logging needs its own private bucket with deliberate object deletion."
  }
  assert {
    condition     = one(one(aws_s3_bucket_server_side_encryption_configuration.loki.rule).apply_server_side_encryption_by_default).sse_algorithm == "AES256" && jsondecode(aws_s3_bucket_policy.loki.policy).Statement[0].Condition.Bool["aws:SecureTransport"] == "false"
    error_message = "Log storage must encrypt at rest and deny insecure transport."
  }
  assert {
    condition     = local.workload_identities["loki"] == "system:serviceaccount:foc-observability:loki" && toset(one([for condition in one(data.aws_iam_policy_document.workload_trust["loki"].statement).condition : condition.values if endswith(condition.variable, ":sub")])) == toset(["system:serviceaccount:foc-observability:loki"]) && toset(one([for condition in one(data.aws_iam_policy_document.workload_trust["loki"].statement).condition : condition.values if endswith(condition.variable, ":aud")])) == toset(["sts.amazonaws.com"])
    error_message = "Only the Helm-owned Loki service account may assume the logging role."
  }
  assert {
    condition     = jsondecode(aws_iam_role_policy.loki.policy).Statement[0].Resource == "arn:aws:s3:::demo-loki" && jsondecode(aws_iam_role_policy.loki.policy).Statement[0].Action == ["s3:ListBucket"] && jsondecode(aws_iam_role_policy.loki.policy).Statement[1].Resource == "arn:aws:s3:::demo-loki/*" && toset(jsondecode(aws_iam_role_policy.loki.policy).Statement[1].Action) == toset(["s3:GetObject", "s3:PutObject", "s3:DeleteObject"])
    error_message = "Loki may list and manage objects only in its own bucket, including compactor deletion."
  }
}

# The extraction requires an isolated service identity/database. The AWS demo
# preparation contract keeps evidence private and leaves runtime deployment off.
run "build_cache_is_separate_and_mutable" {
  command = plan
  assert {
    condition     = aws_ecr_repository.build_cache.name == "foc-demo/build-cache" && aws_ecr_repository.build_cache.image_tag_mutability == "MUTABLE" && !aws_ecr_repository.build_cache.force_delete && !contains(keys(aws_ecr_repository.images), "build-cache")
    error_message = "Layer cache tags are rewritten each release, so the cache needs its own mutable repository outside the immutable release images."
  }
}

run "moderation_is_disabled_by_default" {
  command = plan
  assert {
    condition     = !contains(keys(aws_ecr_repository.images), "moderation-service") && length(aws_s3_bucket.moderation_evidence) == 0 && length(aws_iam_role.moderation) == 0 && length(aws_iam_role_policy.moderation) == 0 && !contains(keys(local.workload_identities), "moderation") && output.moderation == null && !output.deployment.moderation_enabled
    error_message = "Default plans must not provision moderation resources or database credentials."
  }
}

run "moderation_has_isolated_private_evidence_resources" {
  command = plan
  variables {
    enable_moderation_service = true
  }
  override_resource {
    target          = aws_s3_bucket.moderation_evidence[0]
    override_during = plan
    values = {
      id  = "demo-moderation-evidence"
      arn = "arn:aws:s3:::demo-moderation-evidence"
    }
  }
  assert {
    condition     = length(aws_ecr_repository.images) == 10 && aws_ecr_repository.images["moderation-service"].name == "foc-demo/moderation-service" && aws_ecr_repository.images["moderation-service"].image_tag_mutability == "IMMUTABLE" && !aws_ecr_repository.images["moderation-service"].force_delete
    error_message = "Moderation requires one additional immutable, retained image repository."
  }
  assert {
    condition     = aws_s3_bucket.moderation_evidence[0].id != aws_s3_bucket.orders.id && aws_s3_bucket.moderation_evidence[0].id != aws_s3_bucket.loki.id && !aws_s3_bucket.moderation_evidence[0].force_destroy && aws_s3_bucket_public_access_block.moderation_evidence[0].block_public_acls && aws_s3_bucket_public_access_block.moderation_evidence[0].block_public_policy && aws_s3_bucket_public_access_block.moderation_evidence[0].ignore_public_acls && aws_s3_bucket_public_access_block.moderation_evidence[0].restrict_public_buckets && one(one(aws_s3_bucket_server_side_encryption_configuration.moderation_evidence[0].rule).apply_server_side_encryption_by_default).sse_algorithm == "AES256"
    error_message = "Evidence must stay encrypted, private, separate from Order/Loki, and retained during teardown."
  }
  assert {
    condition     = one(aws_s3_bucket_cors_configuration.moderation_evidence[0].cors_rule).allowed_origins == toset(["https://a1b2c3d4.execute-api.ap-southeast-1.amazonaws.com"]) && one(aws_s3_bucket_cors_configuration.moderation_evidence[0].cors_rule).allowed_methods == toset(["GET", "HEAD", "PUT"])
    error_message = "Direct evidence transfers require exact-origin browser CORS, not public access."
  }
  assert {
    condition     = jsondecode(aws_iam_role_policy.moderation[0].policy).Statement[0].Action == ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"] && jsondecode(aws_iam_role_policy.moderation[0].policy).Statement[0].Resource == "arn:aws:s3:::demo-moderation-evidence/evidence/*" && length(jsondecode(aws_iam_role_policy.moderation[0].policy).Statement) == 1
    error_message = "Moderation may read/write/delete only its own evidence prefix, not list buckets, send mail, or administer infrastructure."
  }
  assert {
    condition     = aws_iam_role.moderation[0].name == "foc-demo-moderation-evidence" && local.workload_identities["moderation"] == "system:serviceaccount:foc:moderation-service" && toset(one([for condition in one(data.aws_iam_policy_document.workload_trust["moderation"].statement).condition : condition.values if endswith(condition.variable, ":sub")])) == toset(["system:serviceaccount:foc:moderation-service"]) && toset(one([for condition in one(data.aws_iam_policy_document.workload_trust["moderation"].statement).condition : condition.values if endswith(condition.variable, ":aud")])) == toset(["sts.amazonaws.com"])
    error_message = "Only the exact moderation Kubernetes identity may assume its evidence role using STS."
  }
  assert {
    condition     = jsondecode(aws_s3_bucket_policy.moderation_evidence[0].policy).Statement[0].Effect == "Deny" && jsondecode(aws_s3_bucket_policy.moderation_evidence[0].policy).Statement[0].Action == "s3:*" && jsondecode(aws_s3_bucket_policy.moderation_evidence[0].policy).Statement[0].Condition.Bool["aws:SecureTransport"] == "false" && toset(jsondecode(aws_s3_bucket_policy.moderation_evidence[0].policy).Statement[0].Resource) == toset(["arn:aws:s3:::demo-moderation-evidence", "arn:aws:s3:::demo-moderation-evidence/*"])
    error_message = "Evidence storage must reject insecure transport for the bucket and every object."
  }
  assert {
    condition     = output.deployment.moderation_enabled && output.moderation.database_name == "moderation_db" && output.moderation.database_role == "foc_moderation" && output.moderation.database_secret == "moderation-db" && output.moderation.service_account == "moderation-service" && output.moderation.namespace == "foc"
    error_message = "Operator outputs must describe the isolated moderation identity and database contract."
  }
}
