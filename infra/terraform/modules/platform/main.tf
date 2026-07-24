resource "hcloud_firewall" "platform" {
  name = "postqron-${var.environment}"

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "22"
    source_ips = var.admin_cidrs
  }

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "80"
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = "443"
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction       = "out"
    protocol        = "icmp"
    destination_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction       = "out"
    protocol        = "tcp"
    port            = "1-65535"
    destination_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction       = "out"
    protocol        = "udp"
    port            = "53"
    destination_ips = ["0.0.0.0/0", "::/0"]
  }
}

resource "hcloud_server" "platform" {
  name        = "postqron-${var.environment}"
  image       = "ubuntu-24.04"
  location    = var.location
  server_type = var.server_type
  user_data = templatefile("${path.module}/cloud-init.yaml.tftpl", {
    deployment_ssh_public_key = var.deployment_ssh_public_key
  })

  firewall_ids = [hcloud_firewall.platform.id]

  labels = {
    environment = var.environment
    service     = "postqron"
  }

  lifecycle {
    prevent_destroy = true
  }
}

resource "cloudflare_dns_record" "app_ipv4" {
  zone_id = var.cloudflare_zone_id
  name    = var.app_domain
  content = hcloud_server.platform.ipv4_address
  type    = "A"
  proxied = true
  ttl     = 1
}

resource "cloudflare_dns_record" "api_ipv4" {
  zone_id = var.cloudflare_zone_id
  name    = var.api_domain
  content = hcloud_server.platform.ipv4_address
  type    = "A"
  proxied = true
  ttl     = 1
}
