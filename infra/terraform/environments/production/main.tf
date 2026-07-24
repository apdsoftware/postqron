terraform {
  required_version = ">= 1.10.0"

  required_providers {
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 5.0"
    }
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.52"
    }
  }

  backend "s3" {}
}

provider "cloudflare" {}
provider "hcloud" {}

module "platform" {
  source = "../../modules/platform"

  admin_cidrs               = var.admin_cidrs
  api_domain                = var.api_domain
  app_domain                = var.app_domain
  cloudflare_zone_id        = var.cloudflare_zone_id
  deployment_ssh_public_key = var.deployment_ssh_public_key
  environment               = "production"
  location                  = var.location
  server_type               = var.server_type
}

output "deployment_host" {
  value = module.platform.deployment_host
}
