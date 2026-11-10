mock_provider "aws" {
  override_during = plan
  mock_data "aws_eks_cluster" {
    defaults = {
      arn                   = "arn:aws:eks:ap-southeast-1:123456789012:cluster/foc-demo"
      endpoint              = "https://fixture.eks.amazonaws.com"
      certificate_authority = [{ data = "Zml4dHVyZQ==" }]
      vpc_config            = [{ vpc_id = "vpc-0123456789abcdef0", cluster_security_group_id = "sg-0123456789abcdef0" }]
    }
  }
  mock_data "aws_vpc" { defaults = { cidr_block = "10.42.0.0/16" } }
  mock_resource "aws_iam_openid_connect_provider" {
    defaults = { arn = "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com" }
  }
  mock_resource "aws_s3_bucket" {
    defaults = { arn = "arn:aws:s3:::foc-demo-123456789012-ap-southeast-1-releases" }
  }
  mock_resource "aws_iam_role" {
    defaults = { arn = "arn:aws:iam::123456789012:role/foc-ci-test" }
  }
  mock_resource "aws_iam_instance_profile" {
    defaults = { arn = "arn:aws:iam::123456789012:instance-profile/foc-demo-ci-runner-bootstrap" }
  }
}
variables {
  account_id        = "123456789012"
  region            = "ap-southeast-1"
  expires_on        = "2026-11-19"
  cluster_name      = "foc-demo"
  private_subnet_id = "subnet-0123456789abcdef0"
  runner_ami        = "ami-0123456789abcdef0"
}
run "isolated_release_identity_and_private_disposable_runner" {
  command = plan
  assert {
    condition     = jsondecode(aws_iam_role.release.assume_role_policy).Statement[0].Condition.StringEquals["token.actions.githubusercontent.com:sub"] == "repo:AY2627S1-CS3219-P1/FoC:environment:aws-demo"
    error_message = "Only the repository's protected environment may assume the release identity."
  }
  assert {
    condition     = length(jsondecode(aws_iam_role_policy.release.policy).Statement[2].Resource) == 8 && !strcontains(aws_iam_role_policy.release.policy, "tfstate") && !strcontains(aws_iam_role_policy.release.policy, "iam:")
    error_message = "Deployment permissions must cover eight repositories and exclude infrastructure/state administration."
  }
  assert {
    condition     = tostring(aws_launch_template.runner.network_interfaces[0].associate_public_ip_address) == "false" && aws_launch_template.runner.metadata_options[0].http_tokens == "required" && aws_launch_template.runner.instance_initiated_shutdown_behavior == "terminate"
    error_message = "The runner must be private, require IMDSv2, and terminate on shutdown."
  }
  assert {
    condition     = aws_launch_template.runner.block_device_mappings[0].ebs[0].encrypted && aws_launch_template.runner.block_device_mappings[0].ebs[0].delete_on_termination && aws_launch_template.runner.credit_specification[0].cpu_credits == "standard"
    error_message = "Disposable disk must be encrypted and deleted; CPU credits must not be Unlimited."
  }
  assert {
    condition     = aws_s3_bucket_versioning.configuration.versioning_configuration[0].status == "Enabled" && aws_s3_bucket_public_access_block.configuration.block_public_policy && aws_s3_bucket_public_access_block.configuration.restrict_public_buckets
    error_message = "Configuration storage must be private and versioned."
  }
  assert {
    condition     = strcontains(aws_iam_role_policy.bootstrap.policy, "parameter/foc-demo/ci/runner-jit") && !strcontains(aws_iam_role_policy.bootstrap.policy, "s3:") && !strcontains(aws_iam_role_policy.bootstrap.policy, "eks:")
    error_message = "Bootstrap identity must not inherit deployment or state permissions."
  }
}
