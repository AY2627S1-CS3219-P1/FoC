terraform {
  required_version = ">= 1.13.0, < 2.0.0"

  backend "s3" {}

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.59"
    }
  }
}

provider "aws" {
  region              = var.region
  allowed_account_ids = [var.account_id]

  default_tags {
    tags = {
      Project   = "foc"
      ManagedBy = "terraform"
      ExpiresOn = var.expires_on
    }
  }
}
