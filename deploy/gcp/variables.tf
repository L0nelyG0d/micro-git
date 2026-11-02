variable "project_id" {
  description = "GCP project ID that hosts the resources (not the project number or name)."
  type        = string
}

variable "region" {
  description = "Region for the VM, Artifact Registry and (optional) Cloud Run. Free-tier e2-micro is only free in these three US regions."
  type        = string
  default     = "us-central1"

  validation {
    condition     = contains(["us-west1", "us-central1", "us-east1"], var.region)
    error_message = "region must be one of us-west1, us-central1, us-east1 (the e2-micro free-tier regions)."
  }
}

variable "machine_type" {
  description = "Compute Engine machine type. Only e2-micro is in the always-free allowance."
  type        = string
  default     = "e2-micro"

  validation {
    condition     = var.machine_type == "e2-micro"
    error_message = "Only e2-micro is kept free-tier friendly; change this only knowingly."
  }
}

variable "boot_disk_gb" {
  description = "Boot disk size in GB (standard persistent disk). Free tier covers 30 GB total."
  type        = number
  default     = 30

  validation {
    condition     = var.boot_disk_gb <= 30
    error_message = "Keep boot_disk_gb at or below 30 to stay in the free tier."
  }
}

variable "repo_name" {
  description = "Artifact Registry repository ID."
  type        = string
  default     = "mg"
}

variable "image_tag" {
  description = "Tag of the app image pushed to Artifact Registry (image name is 'app')."
  type        = string
  default     = "latest"
}

variable "domain" {
  description = "Optional public hostname. When set, Caddy issues a Let's Encrypt certificate and COOKIE_SECURE is true. Empty means plain HTTP on port 80 (login cookies are then not Secure)."
  type        = string
  default     = ""
}

variable "enable_cloud_run" {
  description = "Also deploy a Cloud Run service. It cannot reach the VM's Postgres without a paid VPC connector, so it runs with STORE=memory (demo only, data is lost on restart). Off by default."
  type        = bool
  default     = false
}

variable "cloud_run_max_instances" {
  description = "Max instances for the optional Cloud Run service (min is always 0)."
  type        = number
  default     = 1
}
