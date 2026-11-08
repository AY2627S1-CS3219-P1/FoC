variable "account_id" {
  description = "Expected AWS account. Provider refuses credentials for any other account."
  type        = string
  validation {
    condition     = can(regex("^[0-9]{12}$", var.account_id))
    error_message = "Supply a 12-digit AWS account ID."
  }
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
    condition     = can(regex("^[a-z][a-z0-9-]{2,24}$", var.name))
    error_message = "Use 3-25 lowercase letters, digits, or hyphens, starting with a letter."
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

variable "operator_cidr" {
  description = "Operator's public IPv4 /32, allowed to reach the EKS API (not the application)."
  type        = string
  validation {
    condition     = can(cidrnetmask(var.operator_cidr)) && endswith(var.operator_cidr, "/32")
    error_message = "Supply a single public IPv4 address with /32, never an internet-wide CIDR."
  }
}

variable "public_hostname" {
  description = "Owned DNS hostname for FoC HTTPS and S3 browser CORS. No scheme or path."
  type        = string
  validation {
    condition     = can(regex("^[a-z0-9]([a-z0-9.-]*[a-z0-9])?\\.[a-z]{2,}$", var.public_hostname))
    error_message = "Supply a lowercase fully qualified hostname without a scheme, port, or path."
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
