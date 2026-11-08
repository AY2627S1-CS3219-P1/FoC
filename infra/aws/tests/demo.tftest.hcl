# Expected outcomes come from the agreed demo topology and AWS security contracts.
# All providers and the upstream cluster/network modules are mocked. No AWS calls.
mock_provider "aws" {
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
  account_id      = "123456789012"
  operator_cidr   = "203.0.113.10/32"
  public_hostname = "foc.example.com"
  expires_on      = "2026-11-16"
}

run "private_demo_data_and_images" {
  command = plan

  assert {
    condition     = length(aws_ecr_repository.images) == 8 && alltrue([for image in aws_ecr_repository.images : image.image_tag_mutability == "IMMUTABLE" && !image.force_delete])
    error_message = "The demo needs eight immutable image repositories with explicit data deletion."
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
    condition     = one(aws_s3_bucket_cors_configuration.orders.cors_rule).allowed_origins == toset(["https://foc.example.com"]) && aws_s3_bucket_public_access_block.orders.block_public_policy
    error_message = "Browser uploads require exact-origin CORS while the bucket stays private."
  }
  assert {
    condition     = aws_vpc_security_group_ingress_rule.database.from_port == 5432 && aws_vpc_security_group_ingress_rule.database.referenced_security_group_id == "sg-00000000000000001"
    error_message = "PostgreSQL ingress must be restricted to worker workloads."
  }
  assert {
    condition     = aws_vpc_endpoint.s3.vpc_endpoint_type == "Gateway" && length(aws_vpc_endpoint.s3.route_table_ids) > 0
    error_message = "Private worker S3 traffic must use a gateway endpoint."
  }
}

run "reject_open_operator_api" {
  command = plan
  variables {
    operator_cidr = "0.0.0.0/0"
  }
  expect_failures = [var.operator_cidr]
}
