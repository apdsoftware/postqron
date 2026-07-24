output "deployment_host" {
  description = "IPv4 address used by the deployment workflow."
  value       = hcloud_server.platform.ipv4_address
}

output "server_id" {
  description = "Hetzner server identifier."
  value       = hcloud_server.platform.id
}
