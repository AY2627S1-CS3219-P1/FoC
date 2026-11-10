variable "account_id" {
  description = "Expected AWS account. Provider refuses credentials for any other account."
  type        = string
  validation {
    condition     = can(regex("^[0-9]{12}$", var.account_id))
    error_message = "Supply a 12-digit AWS account ID."
  }
}

variable "enable_moderation_service" {
  description = "Prepare isolated AWS resources for reports and appeals. Does not deploy the moderation runtime."
  type        = bool
  default     = false
}

variable "region" {
  description = "AWS deployment region."
  type        = string
  default     = "ap-southeast-1"
}

variable "name" {
  description = "Unique deployment name used for AWS resources."
  type        = string
  default     = "foc-demo"
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,23}[a-z0-9]$", var.name)) && !strcontains(var.name, "--") && !startswith(var.name, "internal-")
    error_message = "Use 3-25 lowercase letters, digits, or single internal hyphens, starting with a letter, ending with a letter or digit, and not the ELB-reserved internal- prefix."
  }
}

variable "expires_on" {
  description = "Planned teardown date (YYYY-MM-DD). A tag only, NOT automatic shutdown."
  type        = string
  validation {
    condition     = can(formatdate("YYYY-MM-DD", "${var.expires_on}T00:00:00Z"))
    error_message = "Supply the planned teardown date as YYYY-MM-DD."
  }
}

locals {
  # IANA IPv4 Special-Purpose registry, plus multicast and deprecated 6to4 relay space.
  nonpublic_operator_ipv4_ranges = [
    "0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
    "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
    "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15",
    "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
  ]
}

variable "operator_cidr" {
  description = "Operator's currently observed public IPv4 /32, allowed to reach the EKS API (not the application)."
  type        = string
  validation {
    condition = try(
      can(cidrnetmask(var.operator_cidr)) && endswith(var.operator_cidr, "/32") && (
        # IANA marks these two assignments within 192.0.0.0/24 globally reachable.
        contains(["192.0.0.9", "192.0.0.10"], cidrhost(var.operator_cidr, 0)) ||
        alltrue([
          for block in local.nonpublic_operator_ipv4_ranges :
          cidrhost("${cidrhost(var.operator_cidr, 0)}/${split("/", block)[1]}", 0) != cidrhost(block, 0)
        ])
      ),
      false,
    )
    error_message = "Supply your currently observed public IPv4 address with /32, not private, special-use, multicast, deprecated relay space, or a wider CIDR. Validation cannot establish your actual NAT/VPN egress address."
  }
}

variable "kubernetes_version" {
  description = "EKS version. Verify regional availability and Istio compatibility before apply."
  type        = string
  default     = "1.35"
}

variable "postgres_version" {
  description = "RDS PostgreSQL major version. Verify PostGIS support before apply."
  type        = string
  default     = "18"
}

variable "postgres_instance_class" {
  description = "Single-AZ demo database size. Re-estimate cost before changing."
  type        = string
  default     = "db.t4g.small"
}

variable "public_domain" {
  description = "Optional public hostname, such as foc.example.com, served through API Gateway. Null keeps the default execute-api hostname."
  type        = string
  default     = null
  validation {
    condition     = var.public_domain == null || can(regex("^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}$", var.public_domain))
    error_message = "Supply a lowercase hostname without scheme, path, or port, or null for the default API Gateway hostname."
  }
}

variable "ses_sender_email" {
  description = "Verified regional SES sender. Null keeps SMTP and creates no email role."
  type        = string
  default     = null
  validation {
    condition     = var.ses_sender_email == null || can(regex("^[^@\\s<>]+@[^@\\s<>]+\\.[^@\\s<>]+$", var.ses_sender_email))
    error_message = "Supply a bare verified email address, without a display name, or null for SMTP."
  }
}
