variable "admin_cidrs" {
  type = list(string)
}

variable "api_domain" {
  type = string
}

variable "app_domain" {
  type = string
}

variable "cloudflare_zone_id" {
  type = string
}

variable "deployment_ssh_public_key" {
  type = string
}

variable "location" {
  type    = string
  default = "nbg1"
}

variable "server_type" {
  type    = string
  default = "cx23"
}
