terraform {
  required_version = ">= 1.6"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
  }

  # Remote state is required (see README). Replace the bucket name before the
  # first `terraform init`; the bucket is created by hand, not by this root.
  backend "gcs" {
    bucket = "CHANGE-ME-mg-tfstate"
    prefix = "mg/gcp"
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

locals {
  zone          = "${var.region}-b"
  registry_repo = "${var.region}-docker.pkg.dev/${var.project_id}/${var.repo_name}"
  image         = "${local.registry_repo}/app:${var.image_tag}"
  site_address  = var.domain != "" ? var.domain : ":80"
  cookie_secure = var.domain != "" ? "true" : "false"
}

# ---------------------------------------------------------------------------
# APIs. Enabling them costs nothing; disable_on_destroy keeps other stacks safe.
# ---------------------------------------------------------------------------
resource "google_project_service" "apis" {
  for_each = toset([
    "compute.googleapis.com",
    "artifactregistry.googleapis.com",
    "iam.googleapis.com",
    "run.googleapis.com",
  ])

  service            = each.key
  disable_on_destroy = false
}

# ---------------------------------------------------------------------------
# Container images. Free tier: 0.5 GB of Artifact Registry storage per month.
# ---------------------------------------------------------------------------
resource "google_artifact_registry_repository" "app" {
  location      = var.region
  repository_id = var.repo_name
  format        = "DOCKER"
  description   = "mg container images"

  depends_on = [google_project_service.apis]
}

# Identity of the VM. It can only read images from the registry.
resource "google_service_account" "vm" {
  account_id   = "mg-vm"
  display_name = "mg VM (pulls images, nothing else)"
}

resource "google_artifact_registry_repository_iam_member" "vm_reader" {
  location   = google_artifact_registry_repository.app.location
  repository = google_artifact_registry_repository.app.repository_id
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${google_service_account.vm.email}"
}

# ---------------------------------------------------------------------------
# Firewall. No static IP is reserved. SSH goes through IAP, not the internet.
# ---------------------------------------------------------------------------
resource "google_compute_firewall" "web" {
  name          = "mg-allow-web"
  network       = "default"
  direction     = "INGRESS"
  source_ranges = ["0.0.0.0/0"]
  target_tags   = ["mg-web"]

  allow {
    protocol = "tcp"
    ports    = ["80", "443"]
  }

  depends_on = [google_project_service.apis]
}

resource "google_compute_firewall" "iap_ssh" {
  name          = "mg-allow-iap-ssh"
  network       = "default"
  direction     = "INGRESS"
  source_ranges = ["35.235.240.0/20"] # Google IAP range
  target_tags   = ["mg-web"]

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  depends_on = [google_project_service.apis]
}

# ---------------------------------------------------------------------------
# Secrets. Stored in Terraform state and in instance metadata (see README).
# ---------------------------------------------------------------------------
resource "random_password" "db" {
  length  = 32
  special = false
}

resource "random_password" "jwt" {
  length  = 64
  special = false
}

# ---------------------------------------------------------------------------
# The VM: Debian 12, docker compose with Postgres, the app and Caddy.
# ---------------------------------------------------------------------------
resource "google_compute_instance" "app" {
  name         = "mg-app"
  machine_type = var.machine_type
  zone         = local.zone
  tags         = ["mg-web"]

  boot_disk {
    initialize_params {
      image = "debian-cloud/debian-12"
      size  = var.boot_disk_gb
      type  = "pd-standard"
    }
  }

  network_interface {
    network = "default"
    # Empty access_config = ephemeral external IP (no static address reserved).
    access_config {}
  }

  service_account {
    email  = google_service_account.vm.email
    scopes = ["cloud-platform"]
  }

  metadata = {
    enable-oslogin = "TRUE"
  }

  # Runs on every boot. Idempotent: secrets come from state, not regenerated.
  metadata_startup_script = <<-EOT
    #!/bin/bash
    set -euo pipefail

    if [ ! -f /swapfile ]; then
      fallocate -l 1G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
      echo '/swapfile none swap sw 0 0' >> /etc/fstab
    fi

    if ! command -v docker >/dev/null 2>&1; then
      apt-get update -y
      apt-get install -y ca-certificates curl
      curl -fsSL https://get.docker.com | sh
    fi

    mkdir -p /opt/mg
    umask 077

    cat > /opt/mg/.env <<'ENVEOF'
    DB_PASSWORD=${random_password.db.result}
    JWT_SECRET=${random_password.jwt.result}
    APP_IMAGE=${local.image}
    COOKIE_SECURE=${local.cookie_secure}
    ENVEOF

    cat > /opt/mg/Caddyfile <<'CADDYEOF'
    ${local.site_address} {
      encode gzip
      reverse_proxy app:8080
    }
    CADDYEOF

    cat > /opt/mg/docker-compose.yml <<'YAMLEOF'
    services:
      db:
        image: postgres:16-alpine
        restart: unless-stopped
        environment:
          POSTGRES_USER: mg
          POSTGRES_DB: mg
          POSTGRES_PASSWORD: $${DB_PASSWORD}
        volumes:
          - pgdata:/var/lib/postgresql/data
      app:
        image: $${APP_IMAGE}
        restart: unless-stopped
        depends_on:
          - db
        environment:
          PORT: "8080"
          STORE: db
          DB_DRIVER: postgres
          DATABASE_URL: postgres://mg:$${DB_PASSWORD}@db:5432/mg?sslmode=disable
          JWT_SECRET: $${JWT_SECRET}
          COOKIE_SECURE: $${COOKIE_SECURE}
      proxy:
        image: caddy:2
        restart: unless-stopped
        depends_on:
          - app
        ports:
          - "80:80"
          - "443:443"
        volumes:
          - ./Caddyfile:/etc/caddy/Caddyfile:ro
          - caddy_data:/data
    volumes:
      pgdata: {}
      caddy_data: {}
    YAMLEOF

    # Registry login with the VM's own service account (no gcloud needed).
    TOKEN=$(curl -fsS -H "Metadata-Flavor: Google" \
      http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token \
      | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')
    echo "$TOKEN" | docker login -u oauth2accesstoken --password-stdin https://${var.region}-docker.pkg.dev

    cd /opt/mg
    # The app image may not be pushed yet on the first boot; the app container
    # then restarts until it is (docker compose pull; docker compose up -d).
    docker compose pull || true
    docker compose up -d
  EOT

  allow_stopping_for_update = true

  depends_on = [
    google_project_service.apis,
    google_artifact_registry_repository_iam_member.vm_reader,
  ]
}

# ---------------------------------------------------------------------------
# OPTIONAL Cloud Run demo (enable_cloud_run = true). Off by default.
# Stateless: STORE=memory, because Cloud Run cannot reach the VM's Postgres
# without a paid Serverless VPC connector. Free tier: 2M requests, 180k vCPU-s
# and 360k GiB-s per month in Tier 1 regions (us-central1, us-east1, us-west1).
# ---------------------------------------------------------------------------
resource "google_cloud_run_v2_service" "app" {
  count = var.enable_cloud_run ? 1 : 0

  name     = "mg"
  location = var.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  template {
    scaling {
      min_instance_count = 0
      max_instance_count = var.cloud_run_max_instances
    }

    containers {
      image = local.image

      ports {
        container_port = 8080
      }

      env {
        name  = "PORT"
        value = "8080"
      }
      env {
        name  = "STORE"
        value = "memory"
      }
      env {
        name  = "JWT_SECRET"
        value = random_password.jwt.result
      }
      env {
        name  = "COOKIE_SECURE"
        value = "true"
      }
    }
  }

  depends_on = [google_project_service.apis]
}

resource "google_cloud_run_v2_service_iam_member" "public" {
  count = var.enable_cloud_run ? 1 : 0

  project  = google_cloud_run_v2_service.app[0].project
  location = google_cloud_run_v2_service.app[0].location
  name     = google_cloud_run_v2_service.app[0].name
  role     = "roles/run.invoker"
  member   = "allUsers"
}
