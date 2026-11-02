output "vm_external_ip" {
  description = "Ephemeral external IPv4 of the VM (changes if the VM is recreated). Point DNS here when using var.domain."
  value       = google_compute_instance.app.network_interface[0].access_config[0].nat_ip
}

output "site_url" {
  description = "URL to open the app on the VM."
  value       = var.domain != "" ? "https://${var.domain}" : "http://${google_compute_instance.app.network_interface[0].access_config[0].nat_ip}"
}

output "artifact_registry_repo" {
  description = "Push target for the app image: <this>/app:<tag>"
  value       = local.registry_repo
}

output "ssh_command" {
  description = "SSH through IAP (no public SSH port is opened)."
  value       = "gcloud compute ssh ${google_compute_instance.app.name} --zone ${local.zone} --project ${var.project_id} --tunnel-through-iap"
}

output "cloud_run_url" {
  description = "URL of the optional Cloud Run demo service (null when enable_cloud_run is false)."
  value       = var.enable_cloud_run ? google_cloud_run_v2_service.app[0].uri : null
}

output "schema_command" {
  description = "Run once after first boot to create the tables (the app does not migrate Postgres itself)."
  value       = "gcloud compute ssh ${google_compute_instance.app.name} --zone ${local.zone} --project ${var.project_id} --tunnel-through-iap --command 'cd /opt/mg && sudo docker compose exec -T db psql -U mg -d mg' < ../../backend/db/schema.sql"
}
