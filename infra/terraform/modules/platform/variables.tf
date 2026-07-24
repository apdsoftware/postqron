variable "admin_cidrs" {
  description = "CIDR blocks allowed to connect over SSH."
  type        = list(string)

  validation {
    condition     = length(var.admin_cidrs) > 0
    error_message = "At least one restricted SSH source CIDR is required."
  }
}

variable "api_domain" {
  description = "Fully qualified domain name for the API."
  type        = string
}

variable "app_domain" {
  description = "Fully qualified domain name for the Nuxt application."
  type        = string
}

variable "cloudflare_zone_id" {
  description = "Cloudflare zone identifier. It is configuration, not a secret."
  type        = string
}

variable "deployment_ssh_public_key" {
  description = "Public key authorized for the unprivileged deployment user."
  type        = string
}

variable "environment" {
  description = "Deployment environment name."
  type        = string

  validation {
    condition     = contains(["staging", "production"], var.environment)
    error_message = "environment must be staging or production."
  }
}

variable "location" {
  description = "Hetzner location."
  type        = string
  default     = "nbg1"
}

variable "server_type" {
  description = "Hetzner server type."
  type        = string
  default     = "cx23"
}
